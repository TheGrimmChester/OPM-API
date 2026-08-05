package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The column → job mapping. Columns that start nothing are as load-bearing as the
// ones that do: human_review IS the human gate, and done is where autopilot has
// already merged and moved the card itself.
func TestColumnJobMapping(t *testing.T) {
	starts := map[string]string{
		"queue":       "run-planning",
		"in_progress": "run-implementation",
		"review":      "run-review",
	}
	for col, want := range starts {
		if got := columnJobAction[col]; got != want {
			t.Fatalf("column %q starts %q, want %q", col, got, want)
		}
	}
	for _, col := range []string{"backlog", "human_review", "done"} {
		if action, ok := columnJobAction[col]; ok {
			t.Fatalf("column %q must start nothing, got %q", col, action)
		}
	}
	// Every mapped column must be a real board column, or a drag could never
	// reach it.
	valid := map[string]bool{}
	for _, c := range BoardColumns {
		valid[c] = true
	}
	for col := range columnJobAction {
		if !valid[col] {
			t.Fatalf("column %q is not in BoardColumns", col)
		}
	}
	// Every action must be one the job runner recognises as an AI phase, or the
	// drag would start a job with no model binding.
	for col, action := range columnJobAction {
		if agentKeyForAction(action) == "" {
			t.Fatalf("column %q starts %q, which has no agent key", col, action)
		}
	}
}

// THE REGRESSION THAT MATTERS.
//
// Jobs move cards when they finish (model_apply.go, job_runner.go, autopilot.go)
// and issue-sync moves them on a GitHub pull. If the trigger were reachable from
// any of those, each move would start another job — review → human_review →
// review, forever, burning model tokens, and it would look like a board bug.
//
// This scans the actual sources rather than trusting a list: the thing that would
// silently regress is a new call site, and only the real call graph can catch that.
func TestTriggerIsReachableOnlyFromTheMoveHandler(t *testing.T) {
	const fn = "maybeStartJobForColumn"
	allowed := map[string]bool{
		"board_triggers.go":      true, // definition
		"board_triggers_test.go": true, // this test
		"handlers.go":            true, // the board move endpoint — the only caller
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var offenders []string
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		scanned++
		if strings.Contains(string(src), fn) && !allowed[name] {
			offenders = append(offenders, name)
		}
	}
	if scanned < 10 {
		t.Fatalf("only scanned %d files — the guard is not actually looking at the package", scanned)
	}
	if len(offenders) > 0 {
		t.Fatalf("%s must be called only from the HTTP move handler, but is referenced in %v. "+
			"A job that moves a card would then start another job, in a loop.",
			fn, offenders)
	}

	// And the store must not reach it, which is the specific mistake that looks
	// most reasonable (MoveTask is the single validated choke point for moves).
	store, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatalf("read store.go: %v", err)
	}
	if strings.Contains(string(store), fn) {
		t.Fatal("store.go must never trigger jobs: MoveTask is called by jobs themselves")
	}
}

func TestJobIsActive(t *testing.T) {
	for _, s := range []string{"queued", "starting", "running", "RUNNING", " queued "} {
		if !jobIsActive(s) {
			t.Fatalf("state %q should count as active (it still owns the task)", s)
		}
	}
	for _, s := range []string{"completed", "failed", "cancelled", ""} {
		if jobIsActive(s) {
			t.Fatalf("state %q should not block a new job", s)
		}
	}
}

func TestAutoStartOnMoveDefaultsOn(t *testing.T) {
	// A project stored before the field existed must get the behaviour.
	if !autoStartOnMove(Project{}) {
		t.Fatal("autoStartOnMove must default to on for existing projects")
	}
	off := false
	if autoStartOnMove(Project{AutoStartOnMove: &off}) {
		t.Fatal("explicit false must keep the board manual")
	}
	on := true
	if !autoStartOnMove(Project{AutoStartOnMove: &on}) {
		t.Fatal("explicit true must start jobs")
	}
}

// The move response must stay backwards compatible: Task fields at the top level,
// with `trigger` added alongside. A caller reading `.status` off this response
// predates the trigger and must keep working.
func TestMoveResponseKeepsTaskFieldsAtTopLevel(t *testing.T) {
	resp := moveResponse{
		Task:    Task{SpecID: "001-thing", Status: "in_progress", Title: "Thing"},
		Trigger: boardTriggerOutcome{Started: true, Action: "run-implementation", RunID: "run-1"},
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Task fields promoted, not nested under "task".
	if _, nested := out["task"]; nested {
		t.Fatal("task must be embedded (fields at the top level), not nested")
	}
	if out["status"] != "in_progress" {
		t.Fatalf("status missing from the top level: %v", out["status"])
	}
	if out["specId"] != "001-thing" {
		t.Fatalf("specId missing from the top level: %v", out["specId"])
	}
	trig, ok := out["trigger"].(map[string]interface{})
	if !ok {
		t.Fatalf("trigger missing: %v", out["trigger"])
	}
	if trig["started"] != true || trig["action"] != "run-implementation" {
		t.Fatalf("trigger payload wrong: %v", trig)
	}
}

// A no-op must always explain itself. "I dragged the card and nothing happened"
// with no reason is the bug report this prevents.
func TestNoOpOutcomesCarryAReason(t *testing.T) {
	cases := []boardTriggerOutcome{
		{Reason: "column \"backlog\" starts no job"},
		{Reason: "autoStartOnMove is off for this project"},
		{Reason: "task has no spec"},
	}
	for _, c := range cases {
		if c.Started {
			t.Fatal("these are no-ops")
		}
		if c.Reason == "" {
			t.Fatal("every no-op must state why")
		}
	}
}
