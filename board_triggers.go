package main

import (
	"fmt"
	"strings"
)

// Dragging a card to a column starts the job that column represents.
//
// ────────────────────────────────────────────────────────────────────────────
// THE TRAP THIS FILE AVOIDS
// ────────────────────────────────────────────────────────────────────────────
// Jobs already move cards themselves. model_apply.go, job_runner.go and
// autopilot.go all call MoveTask as a *result* of finishing, and
// github_issue_sync.go moves cards when a GitHub issue is pulled.
//
// MoveTask is the single validated choke point for column changes, which makes it
// the obvious place to hook a trigger — and the wrong one. Hooking it there means
// every job's own status update starts the next job: review finishes → moves the
// card to human_review → fires the trigger → review again. An autopilot task would
// spin, burning model tokens, and the cause would look like a board bug.
//
// So the trigger lives at the HTTP boundary instead: only a real board drag calls
// maybeStartJobForColumn. Internal MoveTask calls stay silent. If a future caller
// needs the choice, thread an explicit source through rather than inferring it.
// ────────────────────────────────────────────────────────────────────────────

// columnJobAction maps a target column to the job it starts. Columns absent from
// this map deliberately start nothing:
//
//   - backlog       parking, not work
//   - human_review  this IS the human gate — where autopilot parks to wait
//   - done          autopilot already merges and moves here itself
var columnJobAction = map[string]string{
	"queue":       "run-planning",
	"in_progress": "run-implementation",
	"review":      "run-review",
}

// boardTriggerOutcome describes what a drag did, for the API response and the log.
// A no-op always carries a reason: "nothing happened" with no explanation is the
// state people file bugs about.
type boardTriggerOutcome struct {
	Started bool   `json:"started"`
	Action  string `json:"action,omitempty"`
	RunID   string `json:"runId,omitempty"`
	Reason  string `json:"reason"`
}

// moveResponse is the board move reply. Task is EMBEDDED, not nested, so its
// fields stay at the top level exactly as before and `trigger` is additive — an
// existing caller reading task fields off this response keeps working.
type moveResponse struct {
	Task
	Trigger boardTriggerOutcome `json:"trigger"`
}

// maybeStartJobForColumn starts the job for a column after a human moved a card.
//
// Call it ONLY from the board move endpoint. It never reverses state: dragging
// review → in_progress re-opens implementation, but it does not revert a delivered
// branch or close a PR. Undoing delivery is a deliberate action, not a side effect
// of a drag.
func maybeStartJobForColumn(store *Store, p Project, t Task, actor JobOrigin) boardTriggerOutcome {
	column := strings.TrimSpace(t.Status)
	action, ok := columnJobAction[column]
	if !ok {
		return boardTriggerOutcome{Reason: fmt.Sprintf("column %q starts no job", column)}
	}
	if !autoStartOnMove(p) {
		return boardTriggerOutcome{Reason: "autoStartOnMove is off for this project"}
	}
	if strings.TrimSpace(t.SpecID) == "" {
		return boardTriggerOutcome{Reason: "task has no spec"}
	}
	// run-planning needs a spec to plan against; the others need one too, but this
	// is where a card dragged straight out of backlog would otherwise fail
	// mid-runner instead of saying so here.
	if reason, blocked := boardTriggerBlocked(store, p, t, action); blocked {
		return boardTriggerOutcome{Reason: reason}
	}

	origin := actor
	j, err := store.CreateJobWithOrigin(p.ID, action, t.SpecID, runnerImageName(), origin)
	if err != nil {
		return boardTriggerOutcome{Reason: "could not create job: " + err.Error()}
	}
	j.Execution = "container"
	j.Message = fmt.Sprintf("Queued by board move to %s.", column)
	_ = store.UpdateJob(p.ID, j)
	go executeJob(store, j)

	return boardTriggerOutcome{
		Started: true,
		Action:  action,
		RunID:   j.RunID,
		Reason:  fmt.Sprintf("board move to %s started %s", column, action),
	}
}

// boardTriggerBlocked is the dedupe gate.
//
// A double drag, an optimistic-UI retry, or a drag while a job is already running
// must not fan out duplicate jobs — two run-implementation jobs on one task would
// have both writing the same shared workspace.
func boardTriggerBlocked(store *Store, p Project, t Task, action string) (string, bool) {
	// A paused task stops automation everywhere else (taskPausedBlock); a drag
	// must not be a back door around it.
	if prog, err := store.GetProgress(p.ID, t.SpecID); err == nil && prog.Paused {
		return "task is paused — resume it before automation continues", true
	}
	jobs, err := store.ListJobs(p.ID)
	if err != nil {
		// Cannot prove there is no duplicate: refuse rather than risk two jobs on
		// one workspace.
		return "could not check for running jobs: " + err.Error(), true
	}
	for _, j := range jobs {
		if j.SpecID != t.SpecID {
			continue
		}
		if !jobIsActive(j.State) {
			continue
		}
		// Any active job on this task blocks a new one, not just the same action:
		// a running review and a new implementation would race on the same task.
		return fmt.Sprintf("job %s (%s) is already %s for this task", j.RunID, j.Action, j.State), true
	}
	return "", false
}

// jobIsActive reports whether a job still owns the task.
func jobIsActive(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "queued", "starting", "running":
		return true
	default:
		return false
	}
}
