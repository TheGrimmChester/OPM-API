package main

import (
	"fmt"
)

// postImplementationJob chains the next coding subtask, or chains run-review when
// the coding phase is complete. Delivery (PR open) happens only after review PASS
// (see postReviewJob). The task session workspace is retained for review.
func postImplementationJob(store *Store, j Job) (string, error) {
	if j.SpecID == "" {
		return "", nil
	}
	plan, err := store.GetPlan(j.ProjectID, j.SpecID)
	if err != nil {
		return "", err
	}
	if blocked, _ := taskPausedBlock(store, j); blocked {
		return "Implementation session paused — shared workspace retained.", nil
	}
	if hasPendingCodingSubtasks(plan) {
		if !implAutoChainEnabled() {
			return "More coding subtasks remain — enqueue run-implementation to continue.", nil
		}
		// Inherit the parent's actor and pinned binding. Re-resolving here would
		// let an org config edit or a key rotation change models between subtask
		// 2.1 and 2.2 of the same task, on the same shared workspace.
		next, err := store.CreateJobWithOrigin(j.ProjectID, "run-implementation", j.SpecID,
			nz(j.RunnerImage, runnerImageName()), jobOriginFrom(j))
		if err != nil {
			return "", err
		}
		go executeJob(store, next)
		return fmt.Sprintf("Chained next coding subtask (job %s).", next.RunID), nil
	}
	if !allCodingSubtasksComplete(plan) {
		return "", nil
	}
	return chainReview(store, j)
}
