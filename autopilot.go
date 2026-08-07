package main

import (
	"fmt"
	"strings"
)

// humanReviewRequired reports whether the task waits for a human after coding
// parks in review (or after require-review-before-coding planning).
func humanReviewRequired(t Task) bool {
	if t.HumanReviewRequired == nil {
		return true
	}
	return *t.HumanReviewRequired
}

func taskAutopilot(t Task) bool {
	return !humanReviewRequired(t)
}

// chainJobAsync is the single funnel for autopilot chaining. It carries the
// parent's acting identity forward; CreateJobWithOrigin drops the pinned model
// when the next action is a different agent phase.
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
// (plan → all subtasks → park in review for human approve/deliver).
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
// when needed, then chains run-implementation. Delivery is human-only
// (approve-for-coding / deliver), not auto after local review.
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

// parkTaskForHumanReview moves the card to the review column after coding.
// Deep automated review is ORA's domain — OPM does not enqueue run-review.
func parkTaskForHumanReview(store *Store, j Job) (string, error) {
	if j.SpecID == "" {
		return "", nil
	}
	if blocked, _ := taskPausedBlock(store, j); blocked {
		return "Coding complete — task paused; shared workspace retained.", nil
	}
	if _, err := store.MoveTask(j.ProjectID, j.SpecID, "review"); err != nil {
		return "", err
	}
	_ = store.AppendSpecLog(j.ProjectID, j.SpecID, "implementation",
		fmt.Sprintf("[%s] coding complete → review (human approve/deliver)\n", j.RunID))
	return "All coding subtasks complete — parked in review for human approve/deliver.", nil
}

// unsupportedReviewAction is the hard-reject message for retired local review jobs.
func unsupportedReviewAction(action string) error {
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "run-review", "run-qa-fix":
		return fmt.Errorf("unsupported action %q: deep review is owned by ORA (gone)", action)
	default:
		return nil
	}
}
