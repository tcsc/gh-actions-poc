package bot

import (
	"testing"

	"github.com/google/go-github/v37/github"
	"github.com/gravitational/gh-actions-poc/tool/ci/pkg/environment"

	"github.com/stretchr/testify/require"
)

func TestNewBot(t *testing.T) {
	clt := github.NewClient(nil)
	tests := []struct {
		cfg      Config
		checkErr require.ErrorAssertionFunc
		expected *Bot
	}{
		{
			cfg:      Config{Environment: &environment.PullRequestEnvironment{}, GithubClient: clt},
			checkErr: require.NoError,
		},
		{
			cfg:      Config{},
			checkErr: require.Error,
		},
	}
	for _, test := range tests {
		_, err := New(test.cfg)
		test.checkErr(t, err)
	}
}

func TestCommentPermitsRun(t *testing.T) {
	// comment has different sha
	// comment body does not contain ci
	// author association is not owner
	// author assocation is owner
	// author user is not in admin

	pr := environment.Metadata{
		Author:     "Codertocat",
		RepoName:   "Hello-World",
		RepoOwner:  "Codertocat",
		Number:     2,
		HeadSHA:    "ec26c3e57ca3a959ca5aad62de7213c562f8c821",
		BaseSHA:    "f95f852bd8fca8fcc58a9a2d6c842781e32a215e",
		BranchName: "changes",
	}
	env := environment.PullRequestEnvironment{
		Metadata:  &pr,
		Reviewers: map[string][]string{"foo": {"bar", "baz"}, "": {"test-user"}},
	}
	bot := Bot{Environment: &env}
	tests := []struct {
		expected      bool
		association   string
		commitSha     string
		commenterName string
		body          string
		desc          string
	}{
		{
			commitSha:     "f95f852bd8fca8fcc58a9a2d6c842781e32a215e",
			association:   "OWNER",
			commenterName: "test-user",
			body:          "run ci &&&&&&&& ",
			expected:      false,
			desc:          "commenter commented at a different commit sha than  where head is",
		},
		{
			commitSha:     "ec26c3e57ca3a959ca5aad62de7213c562f8c821",
			association:   "OWNER",
			commenterName: "test-user",
			body:          "&&&&&&&& ",
			expected:      false,
			desc:          "body does not contain `run ci`",
		},
		{
			commitSha:     "ec26c3e57ca3a959ca5aad62de7213c562f8c821",
			association:   "COLLABORATOR",
			commenterName: "test-user",
			body:          "run ci &&&&&&&& ",
			expected:      false,
			desc:          "author association is not OWNER",
		},
		{
			commitSha:     "ec26c3e57ca3a959ca5aad62de7213c562f8c821",
			association:   "OWNER",
			commenterName: "random-user",
			body:          "run ci &&&&&&&& ",
			expected:      false,
			desc:          "user is not in reviewer list for external contributors",
		},
		{
			commitSha:     "ec26c3e57ca3a959ca5aad62de7213c562f8c821",
			association:   "OWNER",
			commenterName: "test-user",
			body:          "run ci &&&&&&&& ",
			expected:      true,
			desc:          "comment permits run",
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			comment := &github.PullRequestComment{
				AuthorAssociation: &test.association,
				User:              &github.User{Login: &test.commenterName},
				CommitID:          &test.commitSha,
				Body:              &test.body,
			}
			ok := bot.commentPermitsRun(comment)
			require.Equal(t, test.expected, ok)
		})
	}
}
