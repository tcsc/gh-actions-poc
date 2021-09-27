package bot

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gravitational/teleport/tool/ci"
	"github.com/gravitational/teleport/tool/ci/pkg/environment"

	"github.com/google/go-github/v37/github"
	"github.com/gravitational/trace"
)

// Check checks if all the reviewers have approved the pull request in the current context.
func (c *Bot) Check(ctx context.Context) error {
	env := c.Environment
	pr := c.Environment.PullRequest
	// Remove any stale workflow runs. As only the current workflow run should
	// be shown because it is the workflow that reflects the correct state of the pull.
	//
	// Note: This is run for all workflow runs triggered by an event from an internal contributor,
	// but has to be run again in cron workflow because workflows triggered by external contributors do not
	// grant the Github actions token the correct permissions to dismiss workflow runs.
	if c.Environment.IsInternal(pr.Author) {
		err := c.GithubClient.DismissStaleWorkflowRuns(ctx, env.GetToken(), pr.RepoOwner, pr.RepoName, pr.BranchName)
		if err != nil {
			return trace.Wrap(err)
		}
	}
	// Check if the assigned reviewers have approved this PR.
	err := c.check(ctx)
	if err != nil {
		return trace.Wrap(err)
	}
	return nil
}

// check checks to see if all the required reviewers have approved and invalidates
// approvals for external contributors if a new commit is pushed
func (c *Bot) check(ctx context.Context) error {
	pr := c.Environment.PullRequest
	mostRecentReviews, err := c.getReviews(ctx)
	if err != nil {
		return err
	}
	if len(mostRecentReviews) == 0 {
		return trace.BadParameter("pull request has no reviews")
	}
	log.Printf("Checking if %v has approvals from the required reviewers %+v", pr.Author, c.Environment.GetReviewersForAuthor(pr.Author))
	err = approved(mostRecentReviews, c.Environment.GetReviewersForAuthor(pr.Author))
	if err != nil {
		return err
	}
	// For external contributors, invalidate all approvals if new commits have
	// been pushed since reviewers approved the PR.
	if hasNewCommit(pr.HeadSHA, mostRecentReviews) && !c.Environment.IsInternal(pr.Author) {
		err := c.verifyCommit(ctx)
		if err != nil {
			if validationErr := c.invalidateApprovals(ctx, dismissMessage(pr, c.Environment.GetReviewersForAuthor(pr.Author)), mostRecentReviews); validationErr != nil {
				return trace.Wrap(validationErr)
			}
			return trace.Wrap(err)
		}
	}
	return nil
}

func approved(mostRecentReviews []review, required []string) error {
	var waitingOnApprovalsFrom []string
	for _, requiredReviewer := range required {
		reviewer, ok := hasApproved(requiredReviewer, mostRecentReviews)
		if !ok {
			waitingOnApprovalsFrom = append(waitingOnApprovalsFrom, reviewer)
		}
	}
	switch {
	case len(waitingOnApprovalsFrom) == 1:
		return trace.BadParameter(fmt.Sprintf("required reviewers have not yet approved, waiting on an approval from %s",
			strings.Join(waitingOnApprovalsFrom, "")))
	case len(waitingOnApprovalsFrom) == 2:
		return trace.BadParameter(fmt.Sprintf("required reviewers have not yet approved, waiting for approvals from %s",
			strings.Join(waitingOnApprovalsFrom, " and ")))
	case len(waitingOnApprovalsFrom) > 2:
		lastReviewer := waitingOnApprovalsFrom[len(waitingOnApprovalsFrom)-1]
		waitingOnApprovalsFrom = waitingOnApprovalsFrom[:len(waitingOnApprovalsFrom)-1]
		return trace.BadParameter(fmt.Sprintf("required reviewers have not yet approved, waiting for approvals from %s, and %s",
			strings.Join(waitingOnApprovalsFrom, ", "), lastReviewer))
	}
	return nil
}

func (c *Bot) getReviews(ctx context.Context) ([]review, error) {
	env := c.Environment
	pr := c.Environment.PullRequest
	reviews, _, err := env.Client.PullRequests.ListReviews(ctx, pr.RepoOwner,
		pr.RepoName,
		pr.Number,
		&github.ListOptions{})
	if err != nil {
		return []review{}, trace.Wrap(err)
	}
	currentReviewsSlice := []review{}
	for _, rev := range reviews {
		err := checkReviewFields(rev)
		if err != nil {
			return []review{}, trace.Wrap(err)
		}
		currReview := review{
			name:        *rev.User.Login,
			status:      *rev.State,
			commitID:    *rev.CommitID,
			id:          *rev.ID,
			submittedAt: rev.SubmittedAt,
		}
		currentReviewsSlice = append(currentReviewsSlice, currReview)
	}
	return mostRecent(currentReviewsSlice), nil
}

// review is a pull request review
type review struct {
	name        string
	status      string
	commitID    string
	id          int64
	submittedAt *time.Time
}

func checkReviewFields(review *github.PullRequestReview) error {
	switch {
	case review.ID == nil:
		return trace.Errorf("review ID is nil. review: %+v", review)
	case review.State == nil:
		return trace.Errorf("review State is nil. review: %+v", review)
	case review.CommitID == nil:
		return trace.Errorf("review CommitID is nil. review: %+v", review)
	case review.SubmittedAt == nil:
		return trace.Errorf("review SubmittedAt is nil. review: %+v", review)
	case review.User.Login == nil:
		return trace.Errorf("reviewer User.Login is nil. review: %+v", review)
	}
	return nil
}

// mostRecent returns a list of the most recent review from each required reviewer.
func mostRecent(currentReviews []review) []review {
	mostRecentReviews := make(map[string]review)
	for _, rev := range currentReviews {
		val, ok := mostRecentReviews[rev.name]
		if !ok {
			mostRecentReviews[rev.name] = rev
		} else {
			setTime := val.submittedAt
			currTime := rev.submittedAt
			if currTime.After(*setTime) {
				mostRecentReviews[rev.name] = rev
			}
		}
	}
	reviews := []review{}
	for _, v := range mostRecentReviews {
		reviews = append(reviews, v)
	}
	return reviews
}

func hasApproved(reviewer string, reviews []review) (string, bool) {
	for _, rev := range reviews {
		if rev.name == reviewer && rev.status == ci.Approved {
			return "", true
		}
	}
	return reviewer, false
}

// dimissMessage returns the dimiss message when a review is dismissed
func dismissMessage(pr *environment.PullRequestMetadata, required []string) string {
	var buffer bytes.Buffer
	buffer.WriteString("new commit pushed, please re-review ")
	for _, reviewer := range required {
		fmt.Fprintf(&buffer, "@%v", reviewer)
	}
	return buffer.String()
}

// hasNewCommit sees if the pull request has a new commit
// by comparing commits after the push event
func hasNewCommit(headSHA string, revs []review) bool {
	for _, v := range revs {
		if v.commitID != headSHA {
			return true
		}
	}
	return false
}

// verifyCommit verfies GitHub is the commit author and that the commit is empty
func (c *Bot) verifyCommit(ctx context.Context) error {
	pr := c.Environment.PullRequest
	comparison, _, err := c.Environment.Client.Repositories.CompareCommits(
		ctx,
		pr.RepoOwner,
		pr.RepoName,
		pr.BaseSHA,
		pr.HeadSHA,
	)
	if err != nil {
		return trace.Wrap(err)
	}
	if len(comparison.Files) != 0 {
		return trace.BadParameter("detected file change")
	}
	commit, _, err := c.Environment.Client.Repositories.GetCommit(ctx, pr.RepoOwner, pr.RepoName, pr.HeadSHA)
	if err != nil {
		return trace.Wrap(err)
	}
	verification := commit.Commit.Verification
	if verification != nil {
		if verification.Payload != nil && verification.Verified != nil {
			payload := *verification.Payload
			// If commit is empty (no file changes) and the commit is signed by Github,
			// there is no need to invalidate the commit.
			if strings.Contains(payload, ci.GithubCommit) && *verification.Verified {
				return nil
			}
		}
	}
	return trace.BadParameter("commit is not verified and/or is not signed by GitHub")
}

// invalidateApprovals dismisses all approved reviews on a pull request.
func (c *Bot) invalidateApprovals(ctx context.Context, msg string, reviews []review) error {
	pr := c.Environment.PullRequest
	for _, v := range reviews {
		if v.status == ci.Approved {
			_, _, err := c.Environment.Client.PullRequests.DismissReview(ctx,
				pr.RepoOwner,
				pr.RepoName,
				pr.Number,
				v.id,
				&github.PullRequestReviewDismissalRequest{Message: &msg},
			)
			if err != nil {
				return trace.Wrap(err)
			}
		}
	}
	return nil
}
