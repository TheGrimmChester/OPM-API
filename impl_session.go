package main

import (
	"context"
	"fmt"
	"strings"
)

// postImplementationJob chains the next coding subtask or auto-delivers when the
// coding phase is complete. The task session workspace is retained on pause.
func postImplementationJob(store *Store, j Job, p Project, taskSessionDir string) (string, error) {
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
		next, err := store.CreateJob(j.ProjectID, "run-implementation", j.SpecID, nz(j.RunnerImage, runnerImageName()))
		if err != nil {
			return "", err
		}
		go executeJob(store, next)
		return fmt.Sprintf("Chained next coding subtask (job %s).", next.RunID), nil
	}
	if !allCodingSubtasksComplete(plan) {
		return "", nil
	}
	if !implAutoDeliverEnabled() {
		return "All coding subtasks complete — enqueue deliver or run-review when ready.", nil
	}
	return autoDeliverTaskSession(store, j, p, taskSessionDir)
}

func autoDeliverTaskSession(store *Store, j Job, p Project, taskSessionDir string) (string, error) {
	if taskSessionDir == "" || workspaceIsStub(taskSessionDir) {
		return "All coding subtasks complete — no session workspace to deliver from.", nil
	}
	if !peerORAConfigured() || strings.TrimSpace(p.ConnectorID) == "" || strings.TrimSpace(p.OwnerRepo) == "" {
		_ = store.AppendSpecLog(j.ProjectID, j.SpecID, "implementation",
			fmt.Sprintf("[%s] coding phase complete — auto-deliver skipped (repo not linked / ORA missing)\n", j.RunID))
		return "All coding subtasks complete — auto-deliver skipped (link repo + ORA for push). Workspace retained.", nil
	}
	t, err := store.GetTask(j.ProjectID, j.SpecID)
	if err != nil {
		return "", err
	}
	cs, err := store.GetChangeSet(j.ProjectID, j.SpecID)
	if err != nil {
		return "", err
	}
	if len(cs.Files) == 0 {
		releaseTaskWorkspace(j.ProjectID, j.SpecID)
		return "All coding subtasks complete — no file changes recorded; session workspace released.", nil
	}

	res, status, derr := runDeliveryFromWorkspace(context.Background(), p, t, taskSessionDir, cs, "", "", false)
	if derr != nil {
		_ = store.AppendSpecLog(j.ProjectID, j.SpecID, deliveryLogPhase,
			fmt.Sprintf("[%s] auto-deliver failed (%s): %v\n", j.RunID, status, derr))
		return fmt.Sprintf("All coding subtasks complete — auto-deliver failed (%s): %v. Session workspace retained.", status, derr), nil
	}

	now := nowUTC()
	_, _ = store.MutateTask(j.ProjectID, j.SpecID, func(t *Task) {
		t.DeliveryBranch = res.Branch
		t.DeliveryCommitSha = res.CommitSha
		t.DeliveryFiles = res.Files
		t.DeliveredAt = &now
		t.PRNumber = res.PRNumber
		t.PRURL = res.PRURL
		t.PRState = res.PRState
		t.DeliveryStatus = deliveryStatusDelivered
		t.DeliveryError = ""
	})
	_ = store.ClearChangeSet(j.ProjectID, j.SpecID)
	releaseTaskWorkspace(j.ProjectID, j.SpecID)
	_ = store.AppendSpecLog(j.ProjectID, j.SpecID, deliveryLogPhase,
		fmt.Sprintf("[%s] auto-deliver OK branch=%s commit=%s pr=#%d files=%s\n",
			j.RunID, res.Branch, shortSha(res.CommitSha), res.PRNumber, strings.Join(res.Files, ",")))
	return fmt.Sprintf("All coding subtasks complete — delivered branch %s (commit %s, PR #%d); session workspace released.",
		res.Branch, shortSha(res.CommitSha), res.PRNumber), nil
}
