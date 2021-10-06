package environment

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"os"

	"github.com/gravitational/gh-actions-poc/tool/ci"
	"github.com/gravitational/trace"

	"github.com/google/go-github/v37/github"
)

// Config is used to configure Environment
type Config struct {
	// Context is the context for Environment
	Context context.Context
	// Client is the authenticated Github client.
	Client *github.Client
	// Reviewers is a json object encoded as a string with
	// authors mapped to their respective required reviewers.
	Reviewers string
	// EventPath is the path of the file with the complete
	// webhook event payload on the runner.
	EventPath string
	// RepositoryOwner
	RepositoryOwner string
	// RepositoryName
	RepositoryName string
	// unmarshalReviewers is the function to unmarshal
	// the `Reviewers` string into map[string][]string.
	unmarshalReviewers unmarshalReviewersFn
}

// PullRequestEnvironment contains information about the environment
type PullRequestEnvironment struct {
	// Client is the authenticated Github client
	Client *github.Client
	// Metadata is the pull request in the
	// current context.
	Metadata *Metadata
	// reviewers is a map of reviewers where the key
	// is the user name of the author and the value is a list
	// of required reviewers.
	Reviewers map[string][]string
	// defaultReviewers is a list of reviewers used for authors whose
	// usernames are not a key in `reviewers`
	defaultReviewers []string
	// action is the action that triggered the workflow.
	action string
}

// Metadata is the current pull request metadata
type Metadata struct {
	// Author is the pull request author.
	Author string
	// RepoName is the repository name that the
	// current pull request is trying to merge into.
	RepoName string
	// RepoOwner is the owner of the repository the
	// author is trying to merge into.
	RepoOwner string
	// Number is the pull request number.
	Number int
	// HeadSHA is the commit sha of the author's branch.
	HeadSHA string
	// BaseSHA is the commit sha of the base branch.
	BaseSHA string
	// BranchName is the name of the branch the author
	// is trying to merge in.
	BranchName string
	// BaseFullName is base repository's full name (<user>/<repo>)
	BaseFullName string
	// HeadFullName is head repository's full name (<user>/<repo>)
	HeadFullName string
}

type unmarshalReviewersFn func(ctx context.Context, str string, client *github.Client) (map[string][]string, error)

// CheckAndSetDefaults verifies configuration and sets defaults.
func (c *Config) CheckAndSetDefaults() error {
	if c.Context == nil {
		c.Context = context.Background()
	}
	if c.Client == nil {
		return trace.BadParameter("missing parameter Client")
	}
	if c.Reviewers == "" {
		return trace.BadParameter("missing parameter Reviewers")
	}
	if c.EventPath == "" {
		return trace.BadParameter("missing parameter EventPath")
	}
	if c.RepositoryOwner == "" {
		return trace.BadParameter("missing parameter RepositoryOwner")
	}
	if c.RepositoryName == "" {
		return trace.BadParameter("missing parameter RepositoryName")
	}
	if c.unmarshalReviewers == nil {
		c.unmarshalReviewers = unmarshalReviewers
	}
	return nil
}

// New creates a new instance of Environment.
func New(c Config) (*PullRequestEnvironment, error) {
	err := c.CheckAndSetDefaults()
	if err != nil {
		return nil, trace.Wrap(err)
	}
	revs, err := c.unmarshalReviewers(c.Context, c.Reviewers, c.Client)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	pr, err := GetMetadata(c.Context, c.EventPath, c.RepositoryOwner, c.RepositoryName, c.Client)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return &PullRequestEnvironment{
		Client:           c.Client,
		Reviewers:        revs,
		defaultReviewers: revs[""],
		Metadata:         pr,
	}, nil
}

// unmarshalReviewers converts the passed in string representing json object into a map.
func unmarshalReviewers(ctx context.Context, str string, client *github.Client) (map[string][]string, error) {
	var hasDefaultReviewers bool
	if str == "" {
		return nil, trace.NotFound("reviewers not found")
	}
	m := make(map[string][]string)

	err := json.Unmarshal([]byte(str), &m)
	if err != nil {
		return nil, err
	}
	for author, requiredReviewers := range m {
		for _, reviewer := range requiredReviewers {
			_, err := userExists(ctx, reviewer, client)
			if err != nil {
				return nil, trace.Wrap(err)
			}
		}
		if author == "" {
			hasDefaultReviewers = true
			continue
		}
		_, err := userExists(ctx, author, client)
		if err != nil {
			return nil, trace.Wrap(err)
		}
	}
	if !hasDefaultReviewers {
		return nil, trace.BadParameter("default reviewers are not set. set default reviewers with an empty string as a key")
	}
	return m, nil

}

// userExists checks if a user exists.
func userExists(ctx context.Context, userLogin string, client *github.Client) (*github.User, error) {
	user, _, err := client.Users.Get(ctx, userLogin)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return user, nil
}

// GetReviewersForAuthor gets the required reviewers for the current user.
func (e *PullRequestEnvironment) GetReviewersForAuthor(user string) []string {
	value, ok := e.Reviewers[user]
	// Author is external or does not have set reviewers
	if !ok {
		return e.defaultReviewers
	}
	return value
}

// IsInternal determines if an author is an internal contributor.
func (e *PullRequestEnvironment) IsInternal(author string) bool {
	_, ok := e.Reviewers[author]
	return ok
}

// GetMetadata gets the pull request metadata in the current context.
func GetMetadata(ctx context.Context, path , repoOwner, repoName string, clt *github.Client) (*Metadata, error) {
	var actionType action
	var newPullRequest PullRequest
	var body []byte

	file, err := os.Open(path)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	defer file.Close()
	body, err = ioutil.ReadAll(file)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	err = json.Unmarshal(body, &actionType)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	switch actionType.Action {
	case ci.Ready, ci.Synchronize, ci.Assigned, ci.Opened, ci.Reopened, ci.Submitted:
		// Only unmarshalling the pull request number from each of these events as
		// each event's payload in this case contains the number at `pull_request.number`.
		// Later in the function, the api will be called with the number,
		// repository owner, and repository name to get the rest of the pull request metadata.
		err = json.Unmarshal(body, &newPullRequest)
		if err != nil {
			return nil, trace.Wrap(err)
		}
	case ci.Created:
		// This case is a PR comment event type. The payload of this event 
		// contains the pull request number at `issue.number`
		var comment PRComment
		err = json.Unmarshal(body, &comment)
		if err != nil {
			return nil, trace.Wrap(err)
		}
		newPullRequest = PullRequest{
			PullRequest: Number{
				Value: comment.Comment.Number.Value,
			},
		}
	default:
		return nil, trace.BadParameter("unknown action %s", actionType.Action)
	}
	pr, _, err := clt.PullRequests.Get(ctx,
		repoOwner,
		repoName,
		newPullRequest.PullRequest.Value)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return getMetadata(pr)
}

func getMetadata(pr *github.PullRequest) (*Metadata, error) {
	if err := validatePullRequest(pr); err != nil {
		return nil, trace.Wrap(err)
	}
	return &Metadata{
		Author:     *pr.User.Login,
		RepoOwner:  *pr.Base.Repo.Owner.Login,
		RepoName:   *pr.Base.Repo.Name,
		BaseSHA:    *pr.Base.SHA,
		HeadSHA:    *pr.Head.SHA,
		BranchName: *pr.Head.Ref,
		Number:     *pr.Number,
	}, nil
}

func validatePullRequest(pr *github.PullRequest) error {
	switch {
	case pr.Number == nil:
		return trace.BadParameter("missing pull request number")
	case pr.User.Login == nil:
		return trace.BadParameter("missing user login")
	case pr.Base.Repo.Owner.Login == nil:
		return trace.BadParameter("missing repository owner")
	case pr.Base.Repo.Name == nil:
		return trace.BadParameter("missing repository name")
	case pr.Head.SHA == nil:
		return trace.BadParameter("missing head commit sha")
	case pr.Base.SHA == nil:
		return trace.BadParameter("missing base commit sha")
	case pr.Head.Ref == nil:
		return trace.BadParameter("missing branch name")
	}
	return nil
}
