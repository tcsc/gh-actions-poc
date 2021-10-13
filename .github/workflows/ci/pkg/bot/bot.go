package bot

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"

	"github.com/gravitational/gh-actions-poc/.github/workflows/ci"
	"github.com/gravitational/gh-actions-poc/.github/workflows/ci/pkg/environment"
	"github.com/gravitational/trace"

	"github.com/google/go-github/v37/github"
)

// Config is used to configure Bot
type Config struct {
	Environment *environment.PullRequestEnvironment
}

// Bot assigns reviewers and checks assigned reviewers for a pull request
type Bot struct {
	Environment *environment.PullRequestEnvironment
}

// New returns a new instance of  Bot
func New(c Config) (*Bot, error) {
	err := c.CheckAndSetDefaults()
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return &Bot{
		Environment: c.Environment,
	}, nil
}

// CheckAndSetDefaults verifies configuration and sets defaults
func (c *Config) CheckAndSetDefaults() error {
	if c.Environment == nil {
		return trace.BadParameter("missing parameter Environment")
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
	workflow, err := c.getCheckWorkflow(ctx, owner, repoName)
	if err != nil {
		return trace.Wrap(err)
	}
	runs, err := c.findStaleWorkflowRuns(ctx, owner, repoName, branch, *workflow.ID)
	if err != nil {
		return trace.Wrap(err)
	}

	err = c.deleteRuns(ctx, owner, repoName, runs)
	if err != nil {
		return trace.Wrap(err)
	}
	return nil
}

// deleteRuns deletes all workflow runs except the most recent one because that is
// the run in the current context.
func (c *Bot) deleteRuns(ctx context.Context, owner, repoName string, runs []*github.WorkflowRun) error {
	// Sorting runs by time from oldest to newest.
	sort.Slice(runs, func(i, j int) bool {
		time1, time2 := runs[i].CreatedAt, runs[j].CreatedAt
		return time1.Time.Before(time2.Time)
	})
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

func (c *Bot) findStaleWorkflowRuns(ctx context.Context, owner, repoName, branchName string, workflowID int64) ([]*github.WorkflowRun, error) {
	clt := c.Environment.Client
	list, _, err := clt.Actions.ListWorkflowRunsByID(ctx, owner, repoName, workflowID, &github.ListWorkflowRunsOptions{Branch: branchName})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return list.WorkflowRuns, nil
}

// getCheckWorkflow gets the workflow named 'Check'.
func (c *Bot) getCheckWorkflow(ctx context.Context, owner, repoName string) (*github.Workflow, error) {
	clt := c.Environment.Client
	workflows, _, err := clt.Actions.ListWorkflows(ctx, owner, repoName, &github.ListOptions{})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	for _, w := range workflows.Workflows {
		if *w.Name == ci.CheckWorkflow {
			return w, nil
		}
	}
	return nil, trace.NotFound("workflow %s not found", ci.CheckWorkflow)
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
	clt := c.Environment.Client
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

// validatePullRequestFields checks that pull request fields needed for
// dismissing workflow runs are not nil.
func validatePullRequestFields(pr *github.PullRequest) error {
	switch {
	case pr.Base == nil:
		return trace.BadParameter("missing base branch")
	case pr.Base.User == nil:
		return trace.BadParameter("missing base branch user")
	case pr.Base.User.Login == nil:
		return trace.BadParameter("missing repository owner")
	case pr.Base.Repo == nil:
		return trace.BadParameter("missing base repository")
	case pr.Base.Repo.Name == nil:
		return trace.BadParameter("missing repository name")
	case pr.Head == nil:
		return trace.BadParameter("missing head branch")
	case pr.Head.Ref == nil:
		return trace.BadParameter("missing branch name")
	}
	if err := validateField(*pr.Base.User.Login); err != nil {
		return trace.Errorf("user login err: %v", err)
	}
	if err := validateField(*pr.Base.Repo.Name); err != nil {
		return trace.Errorf("repository name err: %v", err)
	}
	if err := validateField(*pr.Head.Ref); err != nil {
		return trace.Errorf("branch name err: %v", err)
	}
	return nil
}

func validateField(field string) error {
	// Only allow strings that contain alphanumeric characters,
	// underscores, and dashes.
	reg := regexp.MustCompile(`^[\da-zA-Z-_]+$`)
	found := reg.MatchString(field)
	if !found {
		return trace.BadParameter("invalid field, %s contains illegal characters or is empty", field)
	}
	return nil
}
