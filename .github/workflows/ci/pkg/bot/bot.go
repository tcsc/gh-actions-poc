package bot

import (
	"os"
	"regexp"
	"strings"

	"github.com/gravitational/gh-actions-poc/.github/workflows/ci"
	"github.com/gravitational/gh-actions-poc/.github/workflows/ci/pkg/environment"
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

func getRepositoryMetadata() (repositoryOwner string, repositoryName string, err error) {
	repository := os.Getenv(ci.GithubRepository)
	if repository == "" {
		return "", "", trace.BadParameter("environment variable GITHUB_REPOSITORY is not set")
	}
	metadata := strings.Split(repository, "/")
	if len(metadata) != 2 {
		return "", "", trace.BadParameter("environment variable GITHUB_REPOSITORY is not in the correct format,\n the valid format is '<repo owner>/<repo name>'")
	}
	return metadata[0], metadata[1], nil
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

// reg is used for validating various fields on Github types.
// Only allow strings that contain alphanumeric characters,
// underscores, and dashes for fields.
var reg = regexp.MustCompile(`^[\da-zA-Z-_]+$`)

func validateField(field string) error {
	found := reg.MatchString(field)
	if !found {
		return trace.BadParameter("invalid field, %s contains illegal characters or is empty", field)
	}
	return nil
}
