package environment

import (
	"context"
	"encoding/json"
	"fmt"
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
	pr, err := GetMetadata(c.Context, c.EventPath, c.Client)
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
func GetMetadata(ctx context.Context, path string, clt *github.Client) (*Metadata, error) {
	var actionType action
	var newPullRequest NewPullRequest
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

	if actionType.Action != ci.Created {
		err = json.Unmarshal(body, &newPullRequest)
		if err != nil {
			return nil, trace.Wrap(err)
		}
	} else {
		var comment PRCommentEvent
		err = json.Unmarshal(body, &comment)
		if err != nil {
			return nil, trace.Wrap(err)
		}
		newPullRequest = NewPullRequest{
			PullRequest: NPR{
				Number: comment.Comment.PullRequest.Number,
			},
		}
	}

	pr, _, err := clt.PullRequests.Get(ctx,
		"gravitational",
		"gh-actions-poc",
		newPullRequest.PullRequest.Number)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	// Checking if action type is of type created a type of PR comment.
	// The payload of the comment event does not contain all the pull request metadata,
	// however it does contain the pull request url of where the comment is at.
	// The work around is checking if the event is a comment event and getting the payload
	// through the github API

	return getMetadata(pr)
}

func getMetadata(pr *github.PullRequest) (*Metadata, error) {
	fmt.Println(*pr.Head.Ref)
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

func (r *ReviewEvent) toMetadata() (*Metadata, error) {
	pr, err := validateData(r.PullRequest.Number,
		r.PullRequest.Author.Login,
		r.Repository.Owner.Name,
		r.Repository.Name,
		r.PullRequest.Head.SHA,
		r.PullRequest.Base.SHA,
		r.PullRequest.Head.BranchName,
		r.PullRequest.Head.Repo.FullName,
		r.PullRequest.Base.Repo.FullName,
	)
	if err != nil {
		return &Metadata{}, err
	}
	if r.Review.User.Login == "" {
		return &Metadata{}, trace.BadParameter("missing reviewer username")
	}
	return pr, nil
}

func (p *PullRequestEvent) toMetadata() (*Metadata, error) {
	return validateData(p.Number,
		p.PullRequest.User.Login,
		p.Repository.Owner.Name,
		p.Repository.Name,
		p.PullRequest.Head.SHA,
		p.PullRequest.Base.SHA,
		p.PullRequest.Head.BranchName,
		p.PullRequest.Head.Repo.FullName,
		p.PullRequest.Base.Repo.FullName,
	)
}

func (s *PushEvent) toMetadata() (*Metadata, error) {
	return validateData(s.Number,
		s.PullRequest.User.Login,
		s.Repository.Owner.Name,
		s.Repository.Name,
		s.CommitSHA,
		s.BeforeSHA,
		s.PullRequest.Head.BranchName,
		s.PullRequest.Head.Repo.FullName,
		s.PullRequest.Base.Repo.FullName,
	)
}

func validateData(num int, login, owner, repoName, headSHA, baseSHA, branchName, headFullName, baseFullName string) (*Metadata, error) {
	switch {
	case num == 0:
		return &Metadata{}, trace.BadParameter("missing pull request number")
	case login == "":
		return &Metadata{}, trace.BadParameter("missing user login")
	case owner == "":
		return &Metadata{}, trace.BadParameter("missing repository owner")
	case repoName == "":
		return &Metadata{}, trace.BadParameter("missing repository name")
	case headSHA == "":
		return &Metadata{}, trace.BadParameter("missing head commit sha")
	case baseSHA == "":
		return &Metadata{}, trace.BadParameter("missing base commit sha")
	case branchName == "":
		return &Metadata{}, trace.BadParameter("missing branch name")
	}

	return &Metadata{Number: num,
		Author:     login,
		RepoOwner:  owner,
		RepoName:   repoName,
		HeadSHA:    headSHA,
		BaseSHA:    baseSHA,
		BranchName: branchName}, nil
}
