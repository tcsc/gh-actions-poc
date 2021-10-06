package bot

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"

	"log"
	"sort"

	"github.com/gravitational/gh-actions-poc/tool/ci"
	"github.com/gravitational/gh-actions-poc/tool/ci/pkg/environment"
	"github.com/gravitational/trace"

	"github.com/google/go-github/v37/github"
)

// Config is used to configure Bot
type Config struct {
	Environment  *environment.PullRequestEnvironment
	GithubClient *github.Client
}

// Bot assigns reviewers and checks assigned reviewers for a pull request
type Bot struct {
	Environment  *environment.PullRequestEnvironment
	GithubClient GithubClient
}

// GithubClient is a wrapper around the Github client
// to be used on methods that require the client, but don't
// don't need the full functionality of Bot with
// Environment.
type GithubClient struct {
	Client *github.Client
}

// New returns a new instance of  Bot
func New(c Config) (*Bot, error) {
	var bot Bot
	err := c.CheckAndSetDefaults()
	if err != nil {
		return nil, trace.Wrap(err)
	}
	if c.Environment != nil {
		bot.Environment = c.Environment
	}
	bot.GithubClient = GithubClient{
		Client: c.GithubClient,
	}
	return &bot, nil
}

// CheckAndSetDefaults verifies configuration and sets defaults
func (c *Config) CheckAndSetDefaults() error {
	if c.GithubClient == nil {
		return trace.BadParameter("missing parameter GithubClient")
	}
	return nil
}

// HasWorkflowRunApproval checks if a PR has approval for the rest of the check
// workflow to run.
// Currently, there is not a way to approve a workflow run for pull requests from
// forks on the `pull_request_target` event in the Web UI. This is because
// `pull_request_target` is considered a trusted workflow (we are granting GH
// actions additional permissions).
//
// To work around this, this method is being called to check the comments of the
// pull request in the current context for permission from a repository owner
// to run the rest of the workflow.
func (c *Bot) HasWorkflowRunApproval(ctx context.Context) error {
	pr := c.Environment.Metadata
	if c.Environment.IsInternal(pr.Author) {
		return nil
	}
	log.Println("Checking comments...")
	fmt.Printf("%+v", pr)
	comments, resp, err := c.Environment.Client.PullRequests.ListComments(ctx,
		pr.RepoOwner,
		pr.RepoName,
		pr.Number,
		&github.PullRequestListCommentsOptions{},
	)
	fmt.Println("~~~~~~~~", resp.Status)
	if err != nil {
		return trace.Wrap(err)
	}
	fmt.Printf("comments ----> %+v", comments)
	log.Println("Ranging over comments...")
	for _, comment := range comments {
		if ok := c.commentPermitsRun(comment); ok {
			fmt.Println(*comment.Body)
			return nil
		}
	}
	return trace.BadParameter("workflow runs have not been approved for this pull request")
}

// commentPermitsRun checks if a comment to run the rest of the github workflow is valid.
// This function ensures:
// 		- Commit ID the comment was posted at and the head of the PR
//		  are equal.
// 		- The comment body contains the string "run ci".
// 		- The author relationship to the pull request's repository is an owner.
func (c *Bot) commentPermitsRun(comment *github.PullRequestComment) bool {
	pr := c.Environment.Metadata
	if *comment.CommitID != pr.HeadSHA {
		log.Println("commit doesn't contain most recent commit")
		return false
	}
	if !strings.Contains(*comment.Body, ci.RUNCI) {
		log.Println("body does not contain run ci")

		return false
	}
	admins := c.Environment.GetReviewersForAuthor("")
	log.Printf("admins %+v", admins)
	for _, admin := range admins {
		if /* *comment.AuthorAssociation == ci.Owner && */ *comment.User.Login == admin {
			return true
		}
	}
	return false
}

// DimissStaleWorkflowRunsForExternalContributors dismisses stale workflow runs for external contributors.
// Dismissing stale workflows for external contributors is done on a cron job and checks the whole repo for
// stale runs on PRs.
func (c *Bot) DimissStaleWorkflowRunsForExternalContributors(ctx context.Context, repoOwner, repoName string) error {
	clt := c.GithubClient.Client
	pullReqs, _, err := clt.PullRequests.List(ctx, repoOwner, repoName, &github.PullRequestListOptions{State: ci.Open})
	if err != nil {
		return err
	}
	for _, pull := range pullReqs {
		err := c.DismissStaleWorkflowRuns(ctx, *pull.Base.User.Login, *pull.Base.Repo.Name, *pull.Head.Ref)
		if err != nil {
			return trace.Wrap(err)
		}
	}
	return nil
}

// DismissStaleWorkflowRuns dismisses stale Check workflow runs.
// Stale workflow runs are workflow runs that were previously ran and are no longer valid
// due to a new event triggering thus a change in state. The workflow running in the current context is the source of truth for
// the state of checks.
func (c *Bot) DismissStaleWorkflowRuns(ctx context.Context, owner, repoName, branch string) error {
	// Get the target workflow to know get runs triggered by the `Check` workflow.
	// The `Check` workflow is being targeted because it is the only workflow
	// that runs multiple times per PR.
	workflow, err := c.getWorkflow(ctx, owner, repoName, ci.CheckWorkflow)
	if err != nil {
		return trace.Wrap(err)
	}
	runs, err := c.getSortedWorkflowRuns(ctx, owner, repoName, branch, *workflow.ID)
	if err != nil {
		return trace.Wrap(err)
	}

	err = c.deleteRuns(ctx, owner, repoName, runs)
	if err != nil {
		return trace.Wrap(err)
	}
	return nil
}

func (c *Bot) ReRunWorkflows(ctx context.Context) error {
	pr := c.Environment.Metadata
	// get both workflow files
	checkWorkflow, err := c.getWorkflow(ctx, pr.RepoOwner, pr.RepoName, ci.CheckWorkflow)
	if err != nil {
		return trace.Wrap(err)
	}
	err = c.redoMostRecentRun(ctx, *checkWorkflow.ID)
	if err != nil {
		return trace.Wrap(err)
	}
	assignWorkflow, err := c.getWorkflow(ctx, pr.RepoOwner, pr.RepoName, ci.AssignWorkflow)
	if err != nil {
		return trace.Wrap(err)
	}
	err = c.redoMostRecentRun(ctx, *assignWorkflow.ID)
	if err != nil {
		return trace.Wrap(err)
	}
	return nil
}

func (c *Bot) redoMostRecentRun(ctx context.Context, workflowID int64) error {
	var targetRun *github.WorkflowRun
	pr := c.Environment.Metadata
	runs, err := c.getSortedWorkflowRuns(ctx, pr.RepoOwner, pr.RepoName, pr.BranchName, workflowID)
	if err != nil {
		return trace.Wrap(err)
	}
	if len(runs) < 1 {
		return trace.NotFound("workflow run not found")
	}
	targetRun = runs[len(runs)-1]
	_, err = c.Environment.Client.Actions.RerunWorkflowByID(ctx, pr.RepoOwner, pr.RepoName, targetRun.GetID())
	if err != nil {
		return trace.Wrap(err)
	}
	return nil
}

// deleteRuns deletes all workflow runs except the most recent one because that is
// the run in the current context.
func (c *Bot) deleteRuns(ctx context.Context, owner, repoName string, runs []*github.WorkflowRun) error {
	// Deleting all runs except the most recent one.
	for i := 0; i < len(runs)-1; i++ {
		run := runs[i]
		err := c.deleteRun(ctx, owner, repoName, *run.ID)
		if err != nil {
			return trace.Wrap(err)
		}
	}
	return nil
}

func (c *Bot) getSortedWorkflowRuns(ctx context.Context, owner, repoName, branchName string, workflowID int64) ([]*github.WorkflowRun, error) {
	var runs []*github.WorkflowRun
	clt := c.GithubClient.Client
	list, _, err := clt.Actions.ListWorkflowRunsByID(ctx, owner, repoName, workflowID, &github.ListWorkflowRunsOptions{Branch: branchName})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	runs = list.WorkflowRuns
	sort.Slice(runs, func(i, j int) bool {
		time1, time2 := runs[i].CreatedAt, runs[j].CreatedAt
		return time1.Time.Before(time2.Time)
	})
	return runs, nil
}

// getWorkflow gets the workflow named 'Check'.
func (c *Bot) getWorkflow(ctx context.Context, owner, repoName, workflowName string) (*github.Workflow, error) {
	clt := c.GithubClient.Client
	workflows, _, err := clt.Actions.ListWorkflows(ctx, owner, repoName, &github.ListOptions{})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	for _, w := range workflows.Workflows {
		if *w.Name == workflowName {
			return w, nil
		}
	}
	return nil, trace.NotFound("workflow %s not found", workflowName)
}

const (
	// GithubAPIHostname is the Github API hostname.
	GithubAPIHostname = "api.github.com"
	// Scheme is the protocol scheme used when making
	// a request to delete a workflow run to the Github API.
	Scheme = "https"
)

// deleteRun deletes a workflow run.
// Note: the go-github client library does not support this endpoint.
func (c *Bot) deleteRun(ctx context.Context, owner, repo string, runID int64) error {
	clt := c.GithubClient.Client
	// Construct url
	s := fmt.Sprintf("repos/%s/%s/actions/runs/%v", owner, repo, runID)
	cleanPath := path.Join("/", s)
	url := url.URL{
		Scheme: Scheme,
		Host:   GithubAPIHostname,
		Path:   cleanPath,
	}
	req, err := clt.NewRequest("DELETE", url.String(), nil)
	if err != nil {
		return trace.Wrap(err)
	}
	_, err = clt.Do(ctx, req, nil)
	if err != nil {
		return trace.Wrap(err)
	}
	return nil
}
