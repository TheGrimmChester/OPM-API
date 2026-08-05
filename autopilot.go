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

// chainJobAsync is the single funnel for autopilot chaining. It carries the
// parent's acting identity forward; CreateJobWithOrigin drops the pinned model
// when the next action is a different agent phase (coding → review → qa_fix each
// resolve their own).
func chainJobAsync(store *Store, j Job, action string) (Job, error) {
	next, err := store.CreateJobWithOrigin(j.ProjectID, action, j.SpecID,
		nz(j.RunnerImage, runnerImageName()), jobOriginFrom(j))
	if err != nil {
		return Job{}, err
	}
	go executeJob(store, next)
	return next, nil
}

// enqueuePipelineJob starts run-pipeline for a newly created backlog task
// (plan → all subtasks → review → deliver → done). PR opens only after every
// coding subtask is complete.
func enqueuePipelineJob(store *Store, projectID, specID string, origin JobOrigin) error {
	j, err := store.CreateJobWithOrigin(projectID, "run-pipeline", specID, runnerImageName(), origin)
	if err != nil {
		return err
	}
	if preferContainerSpawn() {
		j.Execution = "container"
		j.Message = "Queued by task create: run-pipeline."
	} else {
		j.Execution = "builtin"
		j.Message = "Queued by task create (builtin): run-pipeline."
	}
	_ = store.UpdateJob(projectID, j)
	go executeJob(store, j)
	return nil
}

// postPlanningJob continues after planning: auto-approves require-review-before-coding
// when needed, then chains run-implementation. PR open still waits until all coding
// subtasks complete (postImplementationJob / autoDeliverTaskSession).
func postPlanningJob(store *Store, j Job) (string, error) {
	if j.SpecID == "" {
		return "", nil
	}
	t, err := store.GetTask(j.ProjectID, j.SpecID)
	if err != nil {
		return "", err
	}
	if blocked, _ := taskPausedBlock(store, j); blocked {
		return "Planning complete — task paused; automation stopped.", nil
	}
	if t.RequireReviewBeforeCoding && t.Status == "human_review" {
		if !taskAutopilot(t) {
			return "Planning complete — approve for coding required before implementation.", nil
		}
		if _, aerr := store.ApproveForCoding(j.ProjectID, j.SpecID); aerr != nil {
			return "", aerr
		}
		_ = store.AppendSpecLog(j.ProjectID, j.SpecID, "planning",
			fmt.Sprintf("[%s] auto-approved for coding after planning\n", j.RunID))
	}
	if !implAutoChainEnabled() {
		return "Planning complete — enqueue run-implementation to continue.", nil
	}
	next, err := chainJobAsync(store, j, "run-implementation")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Planning complete — chained run-implementation (job %s).", next.RunID), nil
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
		if !implAutoChainEnabled() {
			return "Review FAIL — enqueue run-qa-fix to continue.", nil
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
	finishDone := func(note string) (string, error) {
		moved, err := store.MoveTask(p.ID, j.SpecID, "done")
		if err != nil {
			return "", err
		}
		syncTaskGitHubAfterMove(store, p, moved)
		releaseTaskWorkspace(j.ProjectID, j.SpecID)
		_ = store.AppendSpecLog(j.ProjectID, j.SpecID, deliveryLogPhase,
			fmt.Sprintf("[%s] autopilot → done: %s\n", j.RunID, note))
		return "Review PASS — " + note + "; moved to done; session workspace released.", nil
	}

	if t.PRNumber <= 0 {
		return finishDone("no pull request on task (deliver skipped or no changes)")
	}
	if !peerORAConfigured() || strings.TrimSpace(p.ConnectorID) == "" || strings.TrimSpace(p.OwnerRepo) == "" {
		return finishDone("repo not linked / ORA missing — merge skipped")
	}
	meta, err := peerMergePullRequest(context.Background(), p.OrganizationID, p.ConnectorID, p.OwnerRepo, t.PRNumber)
	if err != nil {
		_ = store.AppendSpecLog(j.ProjectID, j.SpecID, deliveryLogPhase,
			fmt.Sprintf("[%s] autopilot merge failed: %v\n", j.RunID, err))
		return fmt.Sprintf("Review PASS — autopilot merge failed: %v. Left for human; workspace retained.", err), nil
	}
	_, _ = store.MutateTask(p.ID, j.SpecID, func(tt *Task) {
		tt.PRState = meta.State
		if tt.PRState == "" {
			tt.PRState = "merged"
		}
		tt.PRURL = nz(meta.HTMLURL, tt.PRURL)
	})
	return finishDone(fmt.Sprintf("merged PR #%d", t.PRNumber))
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
