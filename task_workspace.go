package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Task-scoped workspaces live under OPM_JOB_TMP/tasks/{projectId}/{specId}/repo
// and persist across automation jobs until done, human_review gate, or cleanup.

func taskWorkspaceRoot(projectID, specID string) string {
	return filepath.Join(jobTmpRoot(), "tasks", sanitizePathSegment(projectID), sanitizePathSegment(specID))
}

func taskRepoDir(projectID, specID string) string {
	return filepath.Join(taskWorkspaceRoot(projectID, specID), "repo")
}

func sanitizePathSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "_"
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "_"
	}
	return out
}

// ensureTaskWorkspace returns the persistent repo dir for a task session.
// Clones once; later jobs reuse the same tree.
func ensureTaskWorkspace(ctx context.Context, p Project, specID, userID string) (string, error) {
	root := taskWorkspaceRoot(p.ID, specID)
	repo := filepath.Join(root, "repo")
	if st, err := os.Stat(filepath.Join(repo, ".git")); err == nil && st != nil {
		return repo, nil
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	return cloneRepoInto(ctx, p, root, "repo", userID)
}

// releaseTaskWorkspace removes the task-scoped tree (after done, human gate, or cleanup).
func releaseTaskWorkspace(projectID, specID string) {
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(specID) == "" {
		return
	}
	_ = os.RemoveAll(taskWorkspaceRoot(projectID, specID))
}

// cleanupRunnerScratch removes per-run runner in/out dirs only; task workspaces persist.
func cleanupRunnerScratch(runID string) {
	if strings.TrimSpace(runID) == "" {
		return
	}
	_ = os.RemoveAll(filepath.Join(jobTmpRoot(), runID))
}

// usesTaskWorkspace reports whether a job should mount the persistent task session repo.
func usesTaskWorkspace(action string, autopilot bool) bool {
	_ = autopilot
	switch action {
	case "run-implementation", "run-pipeline":
		return true
	default:
		return false
	}
}

// taskWorkspaceWriteMount is true when the runner may modify files in the session repo.
func taskWorkspaceWriteMount(action string) bool {
	return action == "run-implementation" || action == "run-pipeline"
}

func hasPendingCodingSubtasks(plan ImplementationPlan) bool {
	for _, ph := range plan.Phases {
		if ph.Type != "coding" {
			continue
		}
		for _, st := range ph.Subtasks {
			switch st.Status {
			case "completed", "stuck":
				continue
			default:
				return true
			}
		}
	}
	return false
}

func allCodingSubtasksComplete(plan ImplementationPlan) bool {
	found := false
	for _, ph := range plan.Phases {
		if ph.Type != "coding" {
			continue
		}
		for _, st := range ph.Subtasks {
			found = true
			if st.Status != "completed" {
				return false
			}
		}
	}
	return found
}

func implAutoChainEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(envOr("OPM_IMPL_AUTO_CHAIN", "1"))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// implAutoDeliverEnabled is retained for callers that still gate optional
// auto-deliver behaviour. Coding complete parks in review for human deliver —
// this flag no longer opens a PR after a local review job.
func implAutoDeliverEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(envOr("OPM_IMPL_AUTO_DELIVER", "1"))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func taskWorkspaceHonestyNote(p Project, specID, workDir string) string {
	if workDir == "" || workspaceIsStub(workDir) {
		if strings.TrimSpace(p.ConnectorID) != "" && strings.TrimSpace(p.OwnerRepo) != "" {
			return "Linked repository configured but no mountable clone was prepared for this implementation session (check ORA clone credentials)."
		}
		return ""
	}
	return fmt.Sprintf("Task session workspace: %s (persists until done, human_review gate, or cleanup).", taskRepoDir(p.ID, specID))
}
