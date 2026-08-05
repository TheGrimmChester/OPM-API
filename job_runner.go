package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// executeJob runs a task-automation job that writes artifacts into OPM_DATA_DIR.
// When spawnReady (docker CLI + daemon + opm-runner-task image), it docker-runs
// one ephemeral runner container first, then applies shared artifact helpers.
// On spawn failure or OPM_FORCE_BUILTIN=1, it falls back to builtin-only execution.
func executeJob(store *Store, j Job) {
	defer cleanupRunnerScratch(j.RunID)

	execution := "builtin"
	fail := func(msg string, err error) {
		now := nowUTC()
		j.State = "failed"
		j.Execution = execution
		if err != nil {
			j.Error = err.Error()
		}
		j.Message = msg
		j.CompletedAt = &now
		_ = store.UpdateJob(j.ProjectID, j)
	}

	p, err := store.GetProject(j.ProjectID)
	if err != nil {
		fail("Job failed: project not found.", err)
		return
	}

	time.Sleep(50 * time.Millisecond)
	now := nowUTC()
	j.State = "starting"
	j.Execution = execution
	j.Message = "Starting job: preparing workspace."
	j.StartedAt = &now
	_ = store.UpdateJob(j.ProjectID, j)

	var taskSessionDir string
	var workDir string
	var werr error
	autopilot := false
	if j.SpecID != "" {
		if task, terr := store.GetTask(j.ProjectID, j.SpecID); terr == nil {
			autopilot = taskAutopilot(task)
		}
	}
	switch {
	case usesTaskWorkspace(j.Action, autopilot):
		if j.SpecID == "" {
			fail(fmt.Sprintf("%s requires specId", j.Action), fmt.Errorf("specId required"))
			return
		}
		workDir, werr = ensureTaskWorkspace(p, j.SpecID)
		taskSessionDir = workDir
	case j.Action == "run-review" && j.SpecID != "" && !autopilot:
		releaseTaskWorkspace(j.ProjectID, j.SpecID)
		workDir, werr = prepareJobWorkspace(p, j.RunID)
	default:
		workDir, werr = prepareJobWorkspace(p, j.RunID)
	}
	if werr != nil {
		fail("Job failed while preparing workspace.", werr)
		return
	}
	if note := taskWorkspaceHonestyNote(p, j.SpecID, workDir); note != "" && usesTaskWorkspace(j.Action, autopilot) {
		_ = store.AppendSpecLog(j.ProjectID, j.SpecID, "implementation",
			fmt.Sprintf("[%s] %s\n", j.RunID, note))
	}

	cloneNote := ""
	if j.Action == "run-ideation" || j.Action == "run-roadmap-discovery" ||
		j.Action == "run-roadmap-features" || j.Action == "run-roadmap-competitor" {
		cloneNote = cloneHonestyOperatorNote(p, workDir)
	}

	j, err = store.GetJob(j.ProjectID, j.RunID)
	if err != nil || j.State == "cancelled" {
		return
	}

	spawnNote := ""
	if cloneNote != "" {
		spawnNote = cloneNote + " "
		_ = store.AppendProjectLog(j.ProjectID, j.Action,
			fmt.Sprintf("[%s] clone honesty: %s\n", j.RunID, cloneNote))
	}
	var runnerRR RunnerResult
	var hasRunnerRR bool
	if preferContainerSpawn() {
		j.State = "running"
		j.Execution = "container"
		j.Message = fmt.Sprintf("Spawning runner container (%s) for %s…", nz(j.RunnerImage, runnerImageName()), j.Action)
		pctStart := 10
		j.ProgressPct = &pctStart
		_ = store.UpdateJob(j.ProjectID, j)

		cout, serr := runTaskContainer(store, j, workDir)
		if serr != nil {
			spawnNote = fmt.Sprintf("Container spawn failed (%v); falling back to builtin. ", serr)
			execution = "builtin"
			j.Execution = execution
			j.Message = spawnNote + fmt.Sprintf("Running builtin action %s…", j.Action)
		} else {
			execution = "container"
			j.Execution = execution
			spawnNote = "Container spawn OK"
			if cout.HasResult {
				runnerRR = cout.Result
				hasRunnerRR = true
				spawnNote += fmt.Sprintf(" (runner mode=%s", runnerRR.Mode)
				if runnerRR.Reason != "" {
					spawnNote += "; " + truncateRunes(runnerRR.Reason, 80)
				}
				spawnNote += ")"
			} else if tip := strings.TrimSpace(cout.Stdout); tip != "" {
				spawnNote += " (" + truncateRunes(strings.ReplaceAll(tip, "\n", " "), 120) + ")"
			}
			spawnNote += ". "
			j.Message = spawnNote + fmt.Sprintf("Writing artifacts for %s…", j.Action)
		}
	} else {
		j.State = "running"
		j.Execution = "builtin"
		j.Message = fmt.Sprintf("Running builtin action %s…", j.Action)
	}
	pct := 20
	j.ProgressPct = &pct
	_ = store.UpdateJob(j.ProjectID, j)

	if blocked, msg := taskPausedBlock(store, j); blocked {
		fail(msg, fmt.Errorf("paused"))
		return
	}

	resultMsg := ""
	usedModel := false
	if hasRunnerRR {
		if handled, msg, aerr := applyRunnerResult(store, j, runnerRR, taskSessionDir); handled {
			resultMsg, err = msg, aerr
			usedModel = true
		} else if runnerRR.Mode == "fallback" && runnerRR.Reason != "" {
			spawnNote += "Model fallback: " + truncateRunes(runnerRR.Reason, 160) + ". "
			if j.SpecID != "" {
				phase := "planning"
				switch j.Action {
				case "run-implementation":
					phase = "implementation"
				case "run-review", "run-qa-fix":
					phase = "validation"
				}
				_ = store.AppendSpecLog(j.ProjectID, j.SpecID, phase,
					fmt.Sprintf("[%s] runner fallback: %s\n", j.RunID, runnerRR.Reason))
			}
		}
	}
	if !usedModel {
		switch j.Action {
		case "run-planning", "run-followup-planning":
			resultMsg, err = builtinPlanning(store, j)
		case "run-implementation":
			resultMsg, err = builtinImplementation(store, j)
		case "run-review":
			resultMsg, err = builtinReview(store, j)
		case "run-qa-fix":
			resultMsg, err = builtinQaFix(store, j)
		case "recover-subtask":
			resultMsg, err = builtinRecover(store, j)
		case "mark-stuck":
			resultMsg, err = builtinMarkStuck(store, j)
		case "pause-task":
			resultMsg, err = builtinPause(store, j)
		case "resume-task":
			resultMsg, err = builtinResume(store, j)
		case "generate-changelog":
			resultMsg, err = builtinChangelog(store, j)
		case "run-roadmap-discovery":
			resultMsg, err = builtinRoadmapDiscovery(store, j)
		case "run-roadmap-features":
			resultMsg, err = builtinRoadmapFeatures(store, j)
		case "run-roadmap-competitor":
			resultMsg, err = builtinRoadmapCompetitor(store, j)
		case "run-roadmap-publish":
			resultMsg, err = builtinRoadmapPublish(store, j)
		case "run-ideation":
			resultMsg, err = builtinIdeation(store, j)
		case "skip-to-phase":
			resultMsg, err = builtinSkipToPhase(store, j)
		default:
			resultMsg = "Unknown action; no artifacts written."
			err = fmt.Errorf("unsupported action %q", j.Action)
		}
	}

	j, gerr := store.GetJob(j.ProjectID, j.RunID)
	if gerr != nil || j.State == "cancelled" {
		return
	}
	done := nowUTC()
	if spawnNote != "" && !strings.HasPrefix(resultMsg, "Container") {
		resultMsg = spawnNote + resultMsg
	}
	if err != nil {
		j.State = "failed"
		j.Error = err.Error()
		j.Message = resultMsg
		j.Execution = execution
		j.CompletedAt = &done
		_ = store.UpdateJob(j.ProjectID, j)
		return
	}
	if j.Action == "run-implementation" && j.SpecID != "" {
		if extra, perr := postImplementationJob(store, j, p, taskSessionDir); perr != nil {
			resultMsg += fmt.Sprintf(" Post-implementation: %v.", perr)
		} else if extra != "" {
			resultMsg += " " + extra
		}
	}
	if j.Action == "run-review" && j.SpecID != "" {
		passed := lastReviewPassed(store, j)
		if extra, perr := postReviewJob(store, j, p, passed); perr != nil {
			resultMsg += fmt.Sprintf(" Post-review: %v.", perr)
		} else if extra != "" {
			resultMsg += " " + extra
		}
	}
	if j.Action == "run-qa-fix" && j.SpecID != "" {
		if extra, perr := postQaFixJob(store, j); perr != nil {
			resultMsg += fmt.Sprintf(" Post-qa-fix: %v.", perr)
		} else if extra != "" {
			resultMsg += " " + extra
		}
	}
	j.State = "completed"
	j.Execution = execution
	j.Message = resultMsg
	j.CompletedAt = &done
	pct = 100
	j.ProgressPct = &pct
	_ = store.UpdateJob(j.ProjectID, j)

	switch j.Action {
	case "mark-stuck", "pause-task", "resume-task", "recover-subtask", "skip-to-phase":
		// These actions write their own project status.json.
	default:
		st := idleStatus()
		st.LastUpdate = done.Format(time.RFC3339)
		st.RunID = j.RunID
		_ = store.withProject(j.ProjectID, func(proj Project) error {
			return store.writeJSON(store.projectDir(proj)+"/status.json", st)
		})
	}
}

func builtinPlanning(store *Store, j Job) (string, error) {
	if j.SpecID == "" {
		return "run-planning requires specId", fmt.Errorf("specId required")
	}
	task, err := store.GetTask(j.ProjectID, j.SpecID)
	if err != nil {
		return "Task not found", err
	}

	spec := fmt.Sprintf("# %s\n\n## Summary\n\n%s\n\n## Acceptance criteria\n\n- [ ] Core behavior works as described\n- [ ] Tests or verification steps documented\n- [ ] No regressions in related flows\n\n## Notes\n\nGenerated by OPM builtin planner (%s).\n",
		task.Title, nz(task.Description, "_No description provided._"), j.RunID)
	if err := store.PutSpecMarkdown(j.ProjectID, j.SpecID, spec); err != nil {
		return "Failed to write spec.md", err
	}

	plan := ImplementationPlan{
		Feature:          task.Title,
		WorkflowType:     "feature",
		ServicesInvolved: []string{"opm-api"},
		SpecFile:         "spec.md",
		Status:           "ready",
		PlanStatus:       "ready",
		FinalAcceptance: []string{
			"Behavior matches spec summary",
			"Verification steps completed",
		},
		Phases: []PlanPhase{
			{
				Phase: 1, Name: "Design", Type: "planning", ParallelSafe: false,
				Subtasks: []PlanSubtask{
					{ID: "1-0", Description: "Confirm scope and acceptance criteria", Status: "completed", Verification: "spec.md reviewed"},
				},
			},
			{
				Phase: 2, Name: "Implement", Type: "coding", ParallelSafe: false, DependsOn: []int{1},
				Subtasks: []PlanSubtask{
					{ID: "2-0", Description: "Implement core change for: " + task.Title, Status: "pending", FilesToModify: []string{}, Verification: "manual or automated check"},
					{ID: "2-1", Description: "Add or update tests / verification notes", Status: "pending", Verification: "tests pass or checklist done"},
				},
			},
			{
				Phase: 3, Name: "Review", Type: "review", ParallelSafe: false, DependsOn: []int{2},
				Subtasks: []PlanSubtask{
					{ID: "3-0", Description: "Self-review against acceptance criteria", Status: "pending", Verification: "checklist green"},
				},
			},
		},
	}
	if j.Action == "run-followup-planning" {
		existing, _ := store.GetPlan(j.ProjectID, j.SpecID)
		if len(existing.Phases) > 0 {
			next := len(existing.Phases) + 1
			existing.Phases = append(existing.Phases, PlanPhase{
				Phase: next, Name: "Follow-up", Type: "coding", ParallelSafe: false,
				Subtasks: []PlanSubtask{
					{ID: fmt.Sprintf("%d-0", next), Description: "Address follow-up request", Status: "pending"},
				},
			})
			existing.PlanStatus = "ready"
			existing.Status = "ready"
			plan = existing
		}
	}
	if err := store.PutPlan(j.ProjectID, j.SpecID, plan); err != nil {
		return "Failed to write implementation_plan.json", err
	}

	total, done := countPlanSubtasks(plan)
	prog := TaskProgress{
		IsRunning:        false,
		Action:           j.Action,
		Progress:         pct(done, total),
		SubtaskCompleted: done,
		SubtaskTotal:     total,
		CurrentPhaseName: "Design",
		RunID:            j.RunID,
	}
	completedAt := nowUTC()
	prog.CompletedAt = &completedAt
	_ = store.PutProgress(j.ProjectID, j.SpecID, prog)
	_ = store.AppendSpecLog(j.ProjectID, j.SpecID, "planning", fmt.Sprintf("[%s] builtin planning wrote spec.md + plan (%d subtasks)\n", j.RunID, total))

	toStatus := "queue"
	if task.RequireReviewBeforeCoding {
		toStatus = "human_review"
	}
	if _, err := store.MoveTask(j.ProjectID, j.SpecID, toStatus); err != nil {
		return "Plan written but move failed", err
	}
	if toStatus == "human_review" {
		return "Planning complete (builtin): wrote spec.md + implementation plan; moved to human_review (require review before coding).", nil
	}
	return "Planning complete (builtin): wrote spec.md + implementation plan; moved to queue.", nil
}

func builtinImplementation(store *Store, j Job) (string, error) {
	if j.SpecID == "" {
		return "run-implementation requires specId", fmt.Errorf("specId required")
	}
	task, err := store.GetTask(j.ProjectID, j.SpecID)
	if err != nil {
		return "Task not found", err
	}
	if task.RequireReviewBeforeCoding {
		approved := false
		_ = store.withProject(j.ProjectID, func(p Project) error {
			var st map[string]interface{}
			path := filepath.Join(store.projectDir(p), "specs", j.SpecID, "review_state.json")
			if err := store.readJSON(path, &st); err != nil {
				return nil
			}
			if v, ok := st["approved"].(bool); ok {
				approved = v
			}
			return nil
		})
		if !approved && task.Status == "human_review" {
			return "Blocked: approve-for-coding required before implementation", fmt.Errorf("not approved")
		}
	}

	plan, err := store.GetPlan(j.ProjectID, j.SpecID)
	if err != nil || len(plan.Phases) == 0 {
		return "No plan — run planning first", fmt.Errorf("plan missing")
	}

	_, _ = store.MoveTask(j.ProjectID, j.SpecID, "in_progress")

	completedID := ""
	for i := range plan.Phases {
		for k := range plan.Phases[i].Subtasks {
			st := &plan.Phases[i].Subtasks[k]
			if st.Status == "pending" || st.Status == "" || st.Status == "in_progress" {
				st.Status = "completed"
				completedID = st.ID
				goto doneOne
			}
		}
	}
doneOne:
	if completedID == "" {
		_, _ = store.MoveTask(j.ProjectID, j.SpecID, "review")
		return "All subtasks already completed; moved to review.", nil
	}
	plan.Status = "in_progress"
	plan.PlanStatus = "in_progress"
	if err := store.PutPlan(j.ProjectID, j.SpecID, plan); err != nil {
		return "Failed to update plan", err
	}
	total, done := countPlanSubtasks(plan)
	prog := TaskProgress{
		IsRunning:        false,
		Action:           j.Action,
		Progress:         pct(done, total),
		SubtaskCompleted: done,
		SubtaskTotal:     total,
		RunID:            j.RunID,
	}
	if done >= total {
		prog.CurrentPhaseName = "Complete"
		_, _ = store.MoveTask(j.ProjectID, j.SpecID, "review")
	} else {
		prog.CurrentPhaseName = "Implement"
	}
	_ = store.PutProgress(j.ProjectID, j.SpecID, prog)
	_ = store.AppendSpecLog(j.ProjectID, j.SpecID, "implementation", fmt.Sprintf("[%s] completed subtask %s (%d/%d)\n", j.RunID, completedID, done, total))

	notePath := ""
	_ = store.withProject(j.ProjectID, func(p Project) error {
		notePath = filepath.Join(store.projectDir(p), "specs", j.SpecID, "IMPLEMENTATION_NOTES.md")
		prev, _ := os.ReadFile(notePath)
		line := fmt.Sprintf("- %s: completed subtask %s (builtin runner)\n", nowUTC().Format(time.RFC3339), completedID)
		return os.WriteFile(notePath, append(prev, []byte(line)...), 0o644)
	})

	if done >= total {
		return fmt.Sprintf("Implementation complete (builtin): finished subtask %s; all %d subtasks done; moved to review. %s",
			completedID, total, builtinNoCodeNotice), nil
	}
	return fmt.Sprintf("Implementation step (builtin): completed subtask %s (%d/%d). Re-enqueue to continue. %s",
		completedID, done, total, builtinNoCodeNotice), nil
}

// builtinNoCodeNotice is the honest label on the builtin implementation path. The
// builtin runner advances plan state and notes only — it writes no source and
// records no change set, so `deliver` will correctly answer no_changes_produced.
const builtinNoCodeNotice = "No source changes were written: the builtin path tracks plan progress only. " +
	"Configure a model runner (OPM_MODEL_API_KEY) for the runner to produce file changes to deliver."

func builtinReview(store *Store, j Job) (string, error) {
	if j.SpecID == "" {
		return "run-review requires specId", fmt.Errorf("specId required")
	}
	plan, err := store.GetPlan(j.ProjectID, j.SpecID)
	if err != nil {
		return "Plan missing", err
	}
	total, done := countPlanSubtasks(plan)
	pass := total > 0 && done >= total
	result := "FAIL"
	if pass {
		result = "PASS"
	}
	review := fmt.Sprintf("# Review result\n\n- runId: %s\n- result: %s\n- subtasks: %d/%d completed\n\n", j.RunID, result, done, total)
	if !pass {
		review += "## Findings\n\n- Incomplete subtasks remain — run implementation until plan is complete, then re-review.\n"
		_ = store.PutSpecFile(j.ProjectID, j.SpecID, "QA_FIX_REQUEST.md", review+"\nPlease complete remaining pending subtasks.\n")
		_, _ = store.MoveTask(j.ProjectID, j.SpecID, "in_progress")
		_ = store.AppendSpecLog(j.ProjectID, j.SpecID, "validation", fmt.Sprintf("[%s] review FAIL\n", j.RunID))
		return "Review FAIL (builtin): incomplete plan; wrote QA_FIX_REQUEST.md; moved back to in_progress.", nil
	}
	_ = store.PutSpecFile(j.ProjectID, j.SpecID, "REVIEW.md", review)
	// Mark review-phase subtasks complete
	for i := range plan.Phases {
		if plan.Phases[i].Type == "review" {
			for k := range plan.Phases[i].Subtasks {
				plan.Phases[i].Subtasks[k].Status = "completed"
			}
		}
	}
	plan.Status = "reviewed"
	plan.PlanStatus = "reviewed"
	_ = store.PutPlan(j.ProjectID, j.SpecID, plan)
	total, done = countPlanSubtasks(plan)
	_ = store.PutProgress(j.ProjectID, j.SpecID, TaskProgress{
		Progress: pct(done, total), SubtaskCompleted: done, SubtaskTotal: total, RunID: j.RunID, CurrentPhaseName: "Review",
	})
	task, _ := store.GetTask(j.ProjectID, j.SpecID)
	toStatus := reviewPassStatus(task)
	_, _ = store.MoveTask(j.ProjectID, j.SpecID, toStatus)
	_ = store.AppendSpecLog(j.ProjectID, j.SpecID, "validation", fmt.Sprintf("[%s] review PASS → %s\n", j.RunID, toStatus))
	return fmt.Sprintf("Review PASS (builtin): wrote REVIEW.md; moved to %s.", toStatus), nil
}

func builtinQaFix(store *Store, j Job) (string, error) {
	if j.SpecID == "" {
		return "run-qa-fix requires specId", fmt.Errorf("specId required")
	}
	_ = store.PutSpecFile(j.ProjectID, j.SpecID, "QA_FIX_REQUEST.md", "# QA fix applied\n\nBuiltin runner cleared pending QA findings. Re-run review.\n")
	plan, _ := store.GetPlan(j.ProjectID, j.SpecID)
	for i := range plan.Phases {
		for k := range plan.Phases[i].Subtasks {
			if plan.Phases[i].Subtasks[k].Status == "failed" || plan.Phases[i].Subtasks[k].Status == "stuck" {
				plan.Phases[i].Subtasks[k].Status = "pending"
			}
		}
	}
	_ = store.PutPlan(j.ProjectID, j.SpecID, plan)
	_, _ = store.MoveTask(j.ProjectID, j.SpecID, "in_progress")
	_ = store.AppendSpecLog(j.ProjectID, j.SpecID, "implementation", fmt.Sprintf("[%s] qa-fix applied\n", j.RunID))
	return "QA fix (builtin): reset failed subtasks; moved to in_progress. Re-run implementation/review.", nil
}

func taskPausedBlock(store *Store, j Job) (bool, string) {
	switch j.Action {
	case "pause-task", "resume-task", "recover-subtask", "mark-stuck", "generate-changelog",
		"skip-to-phase", "run-roadmap-discovery", "run-roadmap-features",
		"run-roadmap-competitor", "run-roadmap-publish", "run-ideation":
		return false, ""
	}
	if j.SpecID == "" {
		return false, ""
	}
	prog, err := store.GetProgress(j.ProjectID, j.SpecID)
	if err != nil {
		return false, ""
	}
	if prog.Paused {
		return true, "Task is paused — enqueue resume-task before continuing automation."
	}
	return false, ""
}

func builtinPause(store *Store, j Job) (string, error) {
	if j.SpecID == "" {
		return "pause-task requires specId", fmt.Errorf("specId required")
	}
	prog, _ := store.GetProgress(j.ProjectID, j.SpecID)
	if prog.Paused {
		return "Task already paused.", nil
	}
	prog.Paused = true
	prog.IsRunning = false
	prog.Action = "paused"
	prog.RunID = j.RunID
	completedAt := nowUTC()
	prog.CompletedAt = &completedAt
	if err := store.PutProgress(j.ProjectID, j.SpecID, prog); err != nil {
		return "Failed to write pause state", err
	}
	st := idleStatus()
	st.State = "idle"
	st.Spec = j.SpecID
	st.LastUpdate = completedAt.Format(time.RFC3339)
	st.RunID = j.RunID
	_ = store.withProject(j.ProjectID, func(proj Project) error {
		return store.writeJSON(store.projectDir(proj)+"/status.json", st)
	})
	_ = store.AppendSpecLog(j.ProjectID, j.SpecID, "implementation", fmt.Sprintf("[%s] pause-task: automation paused\n", j.RunID))
	return "Task paused (builtin): further plan/build/review jobs blocked until resume-task.", nil
}

func builtinResume(store *Store, j Job) (string, error) {
	if j.SpecID == "" {
		return "resume-task requires specId", fmt.Errorf("specId required")
	}
	prog, _ := store.GetProgress(j.ProjectID, j.SpecID)
	if !prog.Paused {
		return "Task was not paused.", nil
	}
	prog.Paused = false
	prog.IsRunning = false
	prog.Action = "resumed"
	prog.RunID = j.RunID
	if err := store.PutProgress(j.ProjectID, j.SpecID, prog); err != nil {
		return "Failed to clear pause state", err
	}
	_ = store.AppendSpecLog(j.ProjectID, j.SpecID, "implementation", fmt.Sprintf("[%s] resume-task: automation resumed\n", j.RunID))
	return "Task resumed (builtin): plan/build/review jobs allowed again.", nil
}

func builtinRecover(store *Store, j Job) (string, error) {
	if j.SpecID == "" {
		return "recover-subtask requires specId", fmt.Errorf("specId required")
	}
	plan, err := store.GetPlan(j.ProjectID, j.SpecID)
	if err != nil || len(plan.Phases) == 0 {
		return "No plan — run planning first", fmt.Errorf("plan missing")
	}

	recovered := []string{}
	for i := range plan.Phases {
		for k := range plan.Phases[i].Subtasks {
			st := &plan.Phases[i].Subtasks[k]
			switch st.Status {
			case "stuck", "failed", "in_progress":
				st.Status = "pending"
				recovered = append(recovered, st.ID)
			}
		}
	}
	if len(recovered) == 0 {
		// Nothing explicitly stuck — mark the first pending coding subtask recoverable no-op message.
		return "Recover (builtin): no stuck/failed/in_progress subtasks to reset.", nil
	}
	plan.Status = "in_progress"
	plan.PlanStatus = "in_progress"
	if err := store.PutPlan(j.ProjectID, j.SpecID, plan); err != nil {
		return "Failed to update plan after recover", err
	}

	prog, _ := store.GetProgress(j.ProjectID, j.SpecID)
	total, done := countPlanSubtasks(plan)
	prog.Paused = false
	prog.IsRunning = false
	prog.Action = "recover-subtask"
	prog.StuckSubtaskID = ""
	prog.Progress = pct(done, total)
	prog.SubtaskCompleted = done
	prog.SubtaskTotal = total
	prog.CurrentPhaseName = "Recovered"
	prog.RunID = j.RunID
	_ = store.PutProgress(j.ProjectID, j.SpecID, prog)

	_, _ = store.MoveTask(j.ProjectID, j.SpecID, "in_progress")
	_ = store.AppendSpecLog(j.ProjectID, j.SpecID, "implementation",
		fmt.Sprintf("[%s] recover-subtask: reset %s to pending\n", j.RunID, strings.Join(recovered, ", ")))

	st := idleStatus()
	st.State = "idle"
	st.Spec = j.SpecID
	st.LastUpdate = nowUTC().Format(time.RFC3339)
	st.RunID = j.RunID
	_ = store.withProject(j.ProjectID, func(proj Project) error {
		return store.writeJSON(store.projectDir(proj)+"/status.json", st)
	})

	return fmt.Sprintf("Recover (builtin): reset %d subtask(s) (%s) to pending; moved to in_progress. Re-enqueue run-implementation.",
		len(recovered), strings.Join(recovered, ", ")), nil
}

func builtinMarkStuck(store *Store, j Job) (string, error) {
	if j.SpecID == "" {
		return "mark-stuck requires specId", fmt.Errorf("specId required")
	}
	plan, err := store.GetPlan(j.ProjectID, j.SpecID)
	if err != nil || len(plan.Phases) == 0 {
		return "No plan — run planning first", fmt.Errorf("plan missing")
	}
	stuckID := ""
	for i := range plan.Phases {
		for k := range plan.Phases[i].Subtasks {
			st := &plan.Phases[i].Subtasks[k]
			if st.Status == "pending" || st.Status == "" || st.Status == "in_progress" {
				st.Status = "stuck"
				stuckID = st.ID
				goto wrote
			}
		}
	}
wrote:
	if stuckID == "" {
		return "Mark stuck (builtin): no pending/in_progress subtask to mark.", fmt.Errorf("nothing to mark")
	}
	_ = store.PutPlan(j.ProjectID, j.SpecID, plan)
	prog, _ := store.GetProgress(j.ProjectID, j.SpecID)
	prog.IsRunning = false
	prog.Action = "stuck"
	prog.StuckSubtaskID = stuckID
	prog.RunID = j.RunID
	_ = store.PutProgress(j.ProjectID, j.SpecID, prog)
	_, _ = store.MoveTask(j.ProjectID, j.SpecID, "in_progress")
	st := idleStatus()
	st.State = "stuck"
	st.Spec = j.SpecID
	st.LastUpdate = nowUTC().Format(time.RFC3339)
	st.RunID = j.RunID
	_ = store.withProject(j.ProjectID, func(proj Project) error {
		return store.writeJSON(store.projectDir(proj)+"/status.json", st)
	})
	_ = store.AppendSpecLog(j.ProjectID, j.SpecID, "implementation",
		fmt.Sprintf("[%s] mark-stuck: subtask %s\n", j.RunID, stuckID))
	return fmt.Sprintf("Marked subtask %s stuck (builtin). Enqueue recover-subtask to continue.", stuckID), nil
}

// markJobCancelledStuck marks the next incomplete subtask stuck when a job is cancelled mid-run.
func markJobCancelledStuck(store *Store, j Job) {
	if j.SpecID == "" {
		return
	}
	switch j.Action {
	case "run-implementation", "run-planning", "run-followup-planning", "run-review", "run-qa-fix":
	default:
		return
	}
	plan, err := store.GetPlan(j.ProjectID, j.SpecID)
	if err != nil {
		return
	}
	stuckID := ""
	for i := range plan.Phases {
		for k := range plan.Phases[i].Subtasks {
			st := &plan.Phases[i].Subtasks[k]
			if st.Status == "pending" || st.Status == "" || st.Status == "in_progress" {
				st.Status = "stuck"
				stuckID = st.ID
				goto wrote
			}
		}
	}
wrote:
	if stuckID == "" {
		return
	}
	_ = store.PutPlan(j.ProjectID, j.SpecID, plan)
	prog, _ := store.GetProgress(j.ProjectID, j.SpecID)
	prog.IsRunning = false
	prog.Action = "stuck"
	prog.StuckSubtaskID = stuckID
	prog.RunID = j.RunID
	_ = store.PutProgress(j.ProjectID, j.SpecID, prog)
	st := idleStatus()
	st.State = "stuck"
	st.Spec = j.SpecID
	st.Active = false
	st.LastUpdate = nowUTC().Format(time.RFC3339)
	st.RunID = j.RunID
	_ = store.withProject(j.ProjectID, func(proj Project) error {
		return store.writeJSON(store.projectDir(proj)+"/status.json", st)
	})
	_ = store.AppendSpecLog(j.ProjectID, j.SpecID, "implementation",
		fmt.Sprintf("[%s] job cancelled — marked subtask %s stuck\n", j.RunID, stuckID))
}

func builtinChangelog(store *Store, j Job) (string, error) {
	board, err := store.GetBoard(j.ProjectID)
	if err != nil {
		return "Board missing", err
	}
	doneIDs := board["done"]
	var b strings.Builder
	b.WriteString("# Changelog\n\n")
	b.WriteString(fmt.Sprintf("Generated %s by OPM builtin runner (`%s`).\n\n", nowUTC().Format(time.RFC3339), j.RunID))
	b.WriteString("### Added\n\n")
	if len(doneIDs) == 0 {
		b.WriteString("- _(no done tasks yet)_\n")
	} else {
		for _, id := range doneIDs {
			t, err := store.GetTask(j.ProjectID, id)
			title := id
			if err == nil {
				title = t.Title
			}
			b.WriteString(fmt.Sprintf("- **%s** (`%s`)", title, id))
			if err == nil && t.Description != "" {
				b.WriteString(" — " + truncateRunes(t.Description, 120))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\n### Changed\n\n- _(none)_\n\n### Fixed\n\n- _(none)_\n")
	if err := store.PutChangelog(j.ProjectID, b.String()); err != nil {
		return "Failed to write changelog.md", err
	}
	return fmt.Sprintf("Changelog generated (builtin) from %d done task(s).", len(doneIDs)), nil
}

func projectContext(store *Store, projectID string) (Project, []string, error) {
	p, err := store.GetProject(projectID)
	if err != nil {
		return p, nil, err
	}
	tasks, _ := store.ListTasks(projectID)
	titles := make([]string, 0, len(tasks))
	for _, t := range tasks {
		if strings.TrimSpace(t.Title) != "" {
			titles = append(titles, t.Title)
		}
	}
	return p, titles, nil
}

func shortID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, nowUTC().UnixNano()%1_000_000_000)
}

func builtinRoadmapDiscovery(store *Store, j Job) (string, error) {
	p, taskTitles, err := projectContext(store, j.ProjectID)
	if err != nil {
		return "Project not found", err
	}
	rm, _ := store.GetRoadmap(j.ProjectID)
	name := nz(p.Name, p.OwnerRepo)
	if rm.ID == "" {
		rm.ID = "roadmap-1"
	}
	if rm.ProjectName == "" {
		rm.ProjectName = name
	}
	if rm.Version == "" {
		rm.Version = "0.1.0"
	}
	rm.Vision = fmt.Sprintf(
		"Deliver a reliable %s experience for operators: clear board workflow, automated task planning, and a living product roadmap grounded in %s.",
		name, nz(p.OwnerRepo, "the linked GitHub repository"),
	)
	rm.TargetAudience = "Engineering and product operators who manage GitHub-linked work in OPM."
	if rm.Metadata == nil {
		rm.Metadata = map[string]any{}
	}
	rm.Metadata["last_discovery_run"] = j.RunID
	rm.Metadata["discovered_at"] = nowUTC().Format(time.RFC3339)
	if len(taskTitles) > 0 {
		rm.Metadata["board_signals"] = taskTitles
	}

	if len(rm.Phases) == 0 {
		rm.Phases = []RoadmapPhase{
			{ID: shortID("phase"), Name: "Foundation", Description: "Core board, auth, and project linking for " + name, Order: 1, Status: "planned", Features: []string{}},
			{ID: shortID("phase"), Name: "Automation", Description: "Task planning, implementation, and review loops", Order: 2, Status: "planned", Features: []string{}},
			{ID: shortID("phase"), Name: "Insights", Description: "Roadmap, ideation, and changelog quality for operators", Order: 3, Status: "planned", Features: []string{}},
		}
	} else {
		for i := range rm.Phases {
			if rm.Phases[i].Status == "" {
				rm.Phases[i].Status = "planned"
			}
			if rm.Phases[i].Order == 0 {
				rm.Phases[i].Order = i + 1
			}
		}
	}

	if err := store.PutRoadmap(j.ProjectID, rm); err != nil {
		return "Failed to write roadmap.json", err
	}
	_ = store.AppendProjectLog(j.ProjectID, "run-roadmap-discovery",
		fmt.Sprintf("[%s] discovery wrote vision + %d phase(s) (builtin)\n", j.RunID, len(rm.Phases)))
	return fmt.Sprintf("Roadmap discovery (builtin): vision set; %d phase(s); %d board task signal(s).", len(rm.Phases), len(taskTitles)), nil
}

func builtinRoadmapFeatures(store *Store, j Job) (string, error) {
	p, taskTitles, err := projectContext(store, j.ProjectID)
	if err != nil {
		return "Project not found", err
	}
	rm, err := store.GetRoadmap(j.ProjectID)
	if err != nil {
		return "Roadmap missing", err
	}
	if len(rm.Phases) == 0 {
		if _, err := builtinRoadmapDiscovery(store, j); err != nil {
			return "No phases — discovery failed", err
		}
		rm, err = store.GetRoadmap(j.ProjectID)
		if err != nil || len(rm.Phases) == 0 {
			return "No roadmap phases after discovery", fmt.Errorf("phases missing")
		}
	}

	existing := map[string]bool{}
	for _, f := range rm.Features {
		existing[strings.ToLower(strings.TrimSpace(f.Title))] = true
	}

	added := 0
	seedTitles := append([]string{}, taskTitles...)
	if len(seedTitles) == 0 {
		seedTitles = []string{
			"Board workflow polish",
			"Task automation reliability",
			"Operator visibility",
		}
	}
	for i, ph := range rm.Phases {
		candidates := []string{
			fmt.Sprintf("%s: %s", ph.Name, seedTitles[i%len(seedTitles)]),
			fmt.Sprintf("%s checklist for %s", ph.Name, nz(p.Name, p.OwnerRepo)),
		}
		for _, title := range candidates {
			key := strings.ToLower(title)
			if existing[key] {
				continue
			}
			fid := shortID("feat")
			feat := RoadmapFeature{
				ID:                 fid,
				Title:              title,
				Description:        fmt.Sprintf("Agent-assisted feature for phase %q based on project %s.", ph.Name, nz(p.OwnerRepo, p.Name)),
				Rationale:          "Generated by OPM builtin roadmap features agent from board/project signals.",
				Priority:           "medium",
				Complexity:         "m",
				Impact:             "operator productivity",
				PhaseID:            ph.ID,
				Status:             "planned",
				AcceptanceCriteria: []string{"Behavior documented", "Verified on board or jobs UI"},
			}
			rm.Features = append(rm.Features, feat)
			rm.Phases[i].Features = append(rm.Phases[i].Features, fid)
			existing[key] = true
			added++
		}
	}
	if rm.Metadata == nil {
		rm.Metadata = map[string]any{}
	}
	rm.Metadata["last_features_run"] = j.RunID

	if err := store.PutRoadmap(j.ProjectID, rm); err != nil {
		return "Failed to write roadmap features", err
	}
	_ = store.AppendProjectLog(j.ProjectID, "run-roadmap-features",
		fmt.Sprintf("[%s] features agent added %d feature(s) (builtin)\n", j.RunID, added))
	return fmt.Sprintf("Roadmap features (builtin): added %d feature(s) across %d phase(s).", added, len(rm.Phases)), nil
}

func builtinIdeation(store *Store, j Job) (string, error) {
	p, taskTitles, err := projectContext(store, j.ProjectID)
	if err != nil {
		return "Project not found", err
	}
	ideas, _ := store.GetIdeation(j.ProjectID)
	if ideas == nil {
		ideas = emptyIdeation()
	}

	types := IdeationTypes
	if t := strings.TrimSpace(j.IdeationType); t != "" {
		types = []string{t}
	}

	signal := "general product quality"
	if len(taskTitles) > 0 {
		signal = taskTitles[0]
	}
	projLabel := nz(p.OwnerRepo, p.Name)
	templates := map[string][]struct{ title, desc string }{
		"code_improvements": {
			{"Simplify " + signal + " paths", "Reduce complexity around " + signal + " in " + projLabel},
			{"Shared helpers for board artifacts", "Consolidate repeated plan/progress helpers for maintainability"},
		},
		"security": {
			{"Tighten tenant header checks", "Audit org/project header enforcement on sensitive OPM routes"},
			{"Review job cancel authorization", "Ensure cancel and recover actions respect project ACL"},
		},
		"performance": {
			{"Cache project board reads", "Trim repeated filesystem reads for hot board polls"},
			{"Batch job status updates", "Reduce write amplification during long implementation loops"},
		},
		"documentation": {
			{"Document agent job actions", "Operator guide for roadmap, ideation, and skip-to-phase jobs"},
			{"NAS verify checklist", "Curl sequence for health, jobs, and roadmap generators"},
		},
		"ui_ux": {
			{"Clearer job outcome messaging", "Surface execution + message on roadmap/ideation generate buttons"},
			{"Skip-to-phase affordance", "Let operators jump plan phases from task detail"},
		},
		"code_quality": {
			{"Expand builtin generator tests", "Cover roadmap discovery/features and ideation merge behavior"},
			{"Normalize ideation type keys", "Keep underscored types consistent across API and UI"},
		},
	}

	added := 0
	now := nowUTC().Format(time.RFC3339)
	for _, typ := range types {
		bucket := ideas[typ]
		if bucket == nil {
			bucket = []Idea{}
		}
		have := map[string]bool{}
		for _, idea := range bucket {
			have[strings.ToLower(strings.TrimSpace(idea.Title))] = true
		}
		for _, tmpl := range templates[typ] {
			if have[strings.ToLower(tmpl.title)] {
				continue
			}
			bucket = append(bucket, Idea{
				ID:              shortID("idea"),
				Type:            typ,
				Title:           tmpl.title,
				Description:     tmpl.desc,
				Rationale:       "Generated by OPM builtin ideation agent.",
				EstimatedEffort: "S",
				Status:          "open",
				CreatedAt:       now,
			})
			have[strings.ToLower(tmpl.title)] = true
			added++
		}
		ideas[typ] = bucket
	}

	if err := store.PutIdeation(j.ProjectID, ideas); err != nil {
		return "Failed to write ideation.json", err
	}
	_ = store.AppendProjectLog(j.ProjectID, "run-ideation",
		fmt.Sprintf("[%s] ideation agent added %d idea(s) types=%v (builtin)\n", j.RunID, added, types))
	return fmt.Sprintf("Ideation (builtin): added %d idea(s) across %d type(s).", added, len(types)), nil
}

func builtinSkipToPhase(store *Store, j Job) (string, error) {
	if j.SpecID == "" {
		return "skip-to-phase requires specId", fmt.Errorf("specId required")
	}
	if j.TargetPhase < 1 {
		return "skip-to-phase requires targetPhase >= 1", fmt.Errorf("targetPhase required")
	}
	plan, err := store.GetPlan(j.ProjectID, j.SpecID)
	if err != nil || len(plan.Phases) == 0 {
		return "No plan — run planning first", fmt.Errorf("plan missing")
	}

	found := false
	targetName := ""
	skipped := 0
	for i := range plan.Phases {
		ph := &plan.Phases[i]
		num := ph.Phase
		if num == 0 {
			num = i + 1
		}
		if num == j.TargetPhase {
			found = true
			targetName = ph.Name
		}
		if num < j.TargetPhase {
			for k := range ph.Subtasks {
				st := &ph.Subtasks[k]
				if st.Status != "completed" && st.Status != "done" {
					st.Status = "completed"
					skipped++
				}
			}
		}
	}
	if !found {
		return fmt.Sprintf("No plan phase %d", j.TargetPhase), fmt.Errorf("unknown targetPhase")
	}

	plan.Status = "in_progress"
	plan.PlanStatus = "in_progress"
	if err := store.PutPlan(j.ProjectID, j.SpecID, plan); err != nil {
		return "Failed to update plan after skip", err
	}

	total, done := countPlanSubtasks(plan)
	prog, _ := store.GetProgress(j.ProjectID, j.SpecID)
	prog.Paused = false
	prog.IsRunning = false
	prog.Action = "skip-to-phase"
	prog.StuckSubtaskID = ""
	prog.Progress = pct(done, total)
	prog.SubtaskCompleted = done
	prog.SubtaskTotal = total
	prog.CurrentPhaseName = nz(targetName, fmt.Sprintf("Phase %d", j.TargetPhase))
	prog.RunID = j.RunID
	_ = store.PutProgress(j.ProjectID, j.SpecID, prog)

	toStatus := "in_progress"
	for _, ph := range plan.Phases {
		num := ph.Phase
		if num == j.TargetPhase && ph.Type == "review" {
			toStatus = "review"
		}
		if num == j.TargetPhase && ph.Type == "planning" {
			toStatus = "queue"
		}
	}
	_, _ = store.MoveTask(j.ProjectID, j.SpecID, toStatus)
	_ = store.AppendSpecLog(j.ProjectID, j.SpecID, "implementation",
		fmt.Sprintf("[%s] skip-to-phase → %d (%s); completed %d prior subtask(s)\n", j.RunID, j.TargetPhase, targetName, skipped))

	st := idleStatus()
	st.State = "idle"
	st.Spec = j.SpecID
	st.LastUpdate = nowUTC().Format(time.RFC3339)
	st.RunID = j.RunID
	_ = store.withProject(j.ProjectID, func(proj Project) error {
		return store.writeJSON(store.projectDir(proj)+"/status.json", st)
	})

	return fmt.Sprintf("Skip-to-phase (builtin): jumped to phase %d (%s); marked %d earlier subtask(s) complete; status %s.",
		j.TargetPhase, targetName, skipped, toStatus), nil
}

func countPlanSubtasks(plan ImplementationPlan) (total, done int) {
	for _, ph := range plan.Phases {
		for _, st := range ph.Subtasks {
			total++
			if st.Status == "completed" || st.Status == "done" {
				done++
			}
		}
	}
	return total, done
}

func pct(done, total int) int {
	if total <= 0 {
		return 0
	}
	return (done * 100) / total
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
