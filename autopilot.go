package main

import (
	"context"
	"fmt"
	"strings"
)

// humanReviewRequired reports whether the task waits for a human after automated review.
func humanReviewRequired(t Task) bool {
	if t.HumanReviewRequired == nil {
		return true
	}
	return *t.HumanReviewRequired
}

func taskAutopilot(t Task) bool {
	return !humanReviewRequired(t)
}

// reviewPassStatus is the board column after a successful automated review.
func reviewPassStatus(t Task) string {
	if humanReviewRequired(t) {
		return "human_review"
	}
	return "review"
}

func chainJobAsync(store *Store, j Job, action string) (Job, error) {
	next, err := store.CreateJob(j.ProjectID, action, j.SpecID, nz(j.RunnerImage, runnerImageName()))
	if err != nil {
		return Job{}, err
	}
	go executeJob(store, next)
	return next, nil
}

// postReviewJob continues autopilot after run-review completes successfully.
func postReviewJob(store *Store, j Job, p Project, reviewPassed bool) (string, error) {
	if j.SpecID == "" {
		return "", nil
	}
	t, err := store.GetTask(j.ProjectID, j.SpecID)
	if err != nil {
		return "", err
	}
	if blocked, _ := taskPausedBlock(store, j); blocked {
		return "Review complete — task paused; automation stopped.", nil
	}
	if !reviewPassed {
		if !taskAutopilot(t) {
			return "Review FAIL — enqueue run-qa-fix or run-implementation when ready.", nil
		}
		if !implAutoChainEnabled() {
			return "Review FAIL — enqueue run-qa-fix to continue autopilot.", nil
		}
		next, err := chainJobAsync(store, j, "run-qa-fix")
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Review FAIL — chained run-qa-fix (job %s).", next.RunID), nil
	}
	if humanReviewRequired(t) {
		return "Review PASS — waiting for human approval in human_review.", nil
	}
	return autopilotMergeAndComplete(store, j, p, t)
}

// postQaFixJob chains the next autopilot step after qa-fix.
func postQaFixJob(store *Store, j Job) (string, error) {
	if j.SpecID == "" {
		return "", nil
	}
	t, err := store.GetTask(j.ProjectID, j.SpecID)
	if err != nil {
		return "", err
	}
	if !taskAutopilot(t) {
		return "QA fix applied — enqueue run-implementation or run-review when ready.", nil
	}
	if blocked, _ := taskPausedBlock(store, j); blocked {
		return "QA fix applied — task paused; shared workspace retained.", nil
	}
	if !implAutoChainEnabled() {
		return "QA fix applied — enqueue run-implementation to continue autopilot.", nil
	}
	next, err := chainJobAsync(store, j, "run-implementation")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("QA fix — chained run-implementation (job %s).", next.RunID), nil
}

func autopilotMergeAndComplete(store *Store, j Job, p Project, t Task) (string, error) {
	if t.PRNumber <= 0 {
		_ = store.AppendSpecLog(j.ProjectID, j.SpecID, deliveryLogPhase,
			fmt.Sprintf("[%s] autopilot merge skipped — no pull request on task\n", j.RunID))
		return "Review PASS — autopilot merge skipped (no PR on task). Workspace retained.", nil
	}
	if !peerORAConfigured() || strings.TrimSpace(p.ConnectorID) == "" || strings.TrimSpace(p.OwnerRepo) == "" {
		_ = store.AppendSpecLog(j.ProjectID, j.SpecID, deliveryLogPhase,
			fmt.Sprintf("[%s] autopilot merge skipped (repo not linked / ORA missing)\n", j.RunID))
		return "Review PASS — autopilot merge skipped (link repo + ORA). Workspace retained.", nil
	}
	meta, err := peerMergePullRequest(context.Background(), p.OrganizationID, p.ConnectorID, p.OwnerRepo, t.PRNumber)
	if err != nil {
		_ = store.AppendSpecLog(j.ProjectID, j.SpecID, deliveryLogPhase,
			fmt.Sprintf("[%s] autopilot merge failed: %v\n", j.RunID, err))
		return fmt.Sprintf("Review PASS — autopilot merge failed: %v. Workspace retained.", err), nil
	}
	_, _ = store.MutateTask(p.ID, j.SpecID, func(tt *Task) {
		tt.PRState = meta.State
		if tt.PRState == "" {
			tt.PRState = "merged"
		}
		tt.PRURL = nz(meta.HTMLURL, tt.PRURL)
	})
	moved, err := store.MoveTask(p.ID, j.SpecID, "done")
	if err != nil {
		return "", err
	}
	syncTaskGitHubAfterMove(store, p, moved)
	releaseTaskWorkspace(j.ProjectID, j.SpecID)
	_ = store.AppendSpecLog(j.ProjectID, j.SpecID, deliveryLogPhase,
		fmt.Sprintf("[%s] autopilot merged PR #%d → done; session workspace released\n", j.RunID, t.PRNumber))
	return fmt.Sprintf("Review PASS — merged PR #%d; moved to done; session workspace released.", t.PRNumber), nil
}

func chainReviewAfterDeliver(store *Store, j Job) (string, error) {
	if !implAutoChainEnabled() {
		return "Enqueue run-review when ready.", nil
	}
	next, err := chainJobAsync(store, j, "run-review")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Chained run-review (job %s).", next.RunID), nil
}

// lastReviewPassed inspects task status / plan to infer whether the just-finished review job passed.
func lastReviewPassed(store *Store, j Job) bool {
	t, err := store.GetTask(j.ProjectID, j.SpecID)
	if err != nil {
		return false
	}
	if humanReviewRequired(t) {
		return t.Status == "human_review"
	}
	plan, err := store.GetPlan(j.ProjectID, j.SpecID)
	if err != nil {
		return false
	}
	return plan.PlanStatus == "reviewed" || plan.Status == "reviewed"
}
