package bot

import (
	"context"
	"testing"
	"time"

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
			cfg:      Config{Environment: &environment.PullRequestEnvironment{Metadata: &environment.Metadata{Number: 1}}, GithubClient: clt},
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
	futureTime := time.Now().Add(1 * time.Minute)
	bot := Bot{Environment: &env}
	tests := []struct {
		expected      bool
		association   string
		commenterName string
		body          string
		desc          string
		time          time.Time
		checkErr      require.ErrorAssertionFunc
	}{
		{
			association:   "OWNER",
			commenterName: "test-user",
			body:          "&&&&&&&& ",
			expected:      false,
			desc:          "body does not contain `run ci`",
			time:          futureTime,
			checkErr:      require.Error,
		},
		{
			association:   "COLLABORATOR",
			commenterName: "test-user",
			body:          "run ci &&&&&&&& ",
			expected:      false,
			desc:          "author association is not OWNER",
			time:          futureTime,
			checkErr:      require.Error,
		},
		{
			association:   "OWNER",
			commenterName: "random-user",
			body:          "run ci &&&&&&&& ",
			expected:      false,
			desc:          "user is not in reviewer list for external contributors",
			time:          futureTime,
			checkErr:      require.Error,
		},
		{
			association:   "OWNER",
			commenterName: "test-user",
			body:          "run ci &&&&&&&& ",
			expected:      true,
			desc:          "comment permits run",
			time:          futureTime,
			checkErr:      require.NoError,
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			comment := &github.IssueComment{
				AuthorAssociation: &test.association,
				User:              &github.User{Login: &test.commenterName},
				Body:              &test.body,
				CreatedAt:         &test.time,
			}
			now := time.Now()
			err := bot.commentPermitsRun(context.TODO(), now, comment)
			test.checkErr(t, err)

		})
	}
}
