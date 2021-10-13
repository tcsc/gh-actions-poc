package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"github.com/gravitational/gh-actions-poc/.github/workflows/ci"
	"github.com/gravitational/gh-actions-poc/.github/workflows/ci/pkg/bot"
	bots "github.com/gravitational/gh-actions-poc/.github/workflows/ci/pkg/bot"
	"github.com/gravitational/gh-actions-poc/.github/workflows/ci/pkg/environment"
	"github.com/gravitational/trace"

	"github.com/google/go-github/v37/github"
	"golang.org/x/oauth2"
)

const (
	usage = "The following subcommands are supported:\n" +
		"\tassign-reviewers \n\t assigns reviewers to a pull request.\n" +
		"\tcheck-reviewers \n\t checks pull request for required reviewers.\n" +
		"\tdismiss-runs \n\t dismisses stale workflow runs for external contributors.\n"

	reviewersHelp = `reviewers is a string representing a json object that maps authors to 
	required reviewers for that author. example: "{\"author1\": [\"reviewer0\", \"reviewer1\"], \"author2\": 
	[\"reviewer2\", \"reviewer3\"],\"*\": [\"default-reviewer0\", \"default-reviewer1\"]}"`

	workflowRunTimeout = time.Minute
)

func main() {
	var token = flag.String("token", "", "token is the Github authentication token.")
	var reviewers = flag.String("reviewers", "", reviewersHelp)
	flag.Parse()

	if len(os.Args) < 2 {
		log.Fatalf("Subcommand required. %s\n", usage)
	}
	subcommand := os.Args[len(os.Args)-1]
	ctx, cancel := context.WithTimeout(context.Background(), workflowRunTimeout)
	defer cancel()

	client := makeGithubClient(ctx, *token)
	switch subcommand {
	case ci.AssignSubcommand:
		log.Println("Assigning reviewers.")
		bot, err := constructBot(ctx, client, *reviewers)
		if err != nil {
			log.Fatal(err)
		}
		err = bot.Assign(ctx)
		if err != nil {
			log.Fatal(err)
		}
		log.Print("Assign completed.")
	case ci.CheckSubcommand:
		log.Println("Checking reviewers.")
		bot, err := constructBot(ctx, client, *reviewers)
		if err != nil {
			log.Fatal(err)
		}
		err = bot.Check(ctx)
		if err != nil {
			log.Fatal(err)
		}
		log.Print("Check completed.")
	case ci.Dismiss:
		log.Println("Dismissing stale runs.")
		// Constructing Bot without PullRequestEnvironment.
		// Dismiss runs does not need PullRequestEnvironment because PullRequestEnvironment is only
		// is used for pull request or PR adjacent (PR reviews, pushes to PRs, PR opening, reopening, etc.) events.
		bot, err := bot.New(bots.Config{GithubClient: client})
		if err != nil {
			log.Fatal(err)
		}
		err = bot.DimissStaleWorkflowRunsForExternalContributors(ctx)
		if err != nil {
			log.Fatal(err)
		}
		log.Println("Stale workflow run removal completed.")
	default:
		log.Fatalf("Unknown subcommand: %v.\n%s", subcommand, usage)
	}

}

func constructBot(ctx context.Context, clt *github.Client, reviewers string) (*bots.Bot, error) {
	env, err := environment.New(environment.Config{Client: clt,
		Reviewers: reviewers,
		Context:   ctx,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}

	bot, err := bots.New(bots.Config{Environment: env, GithubClient: clt})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return bot, nil
}

func makeGithubClient(ctx context.Context, token string) *github.Client {
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	return github.NewClient(tc)
}
