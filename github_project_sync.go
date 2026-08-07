package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// OPM task ↔ GitHub Projects v2 board item sync.
//
// Mirrors the fail-closed contract of the Issue sync: every failure is persisted
// on the task (GithubProjectSyncError), written to the spec log, and returned with
// a machine-readable status. A rename that did not reach the board must never look
// like success.
//
// ORA answers items/upsert with `title_synced` plus a `title_status`. A 200 with
// title_synced=false is a *partial* failure — the item exists but the rename did
// not land — and is recorded exactly like a hard failure.
//
// Note that Projects v2 needs the organization projects permission on the GitHub
// App installation, which the current installation does not have; until it is
// granted every call here comes back missing_organization_projects. That is the
// honest answer, not a silent no-op.

const projectSyncLogPhase = "github-project-sync"

// Statuses OPM answers with. The first group is relayed from ORA, the second is
// decided locally before any peer call.
const (
	projectSyncOK               = "ok"
	projectSyncNothingToSync    = "nothing_to_sync"
	projectSyncMissingPerm      = "missing_organization_projects"
	projectSyncTitleUnsupported = "title_sync_unsupported"
	projectSyncItemNotFound     = "item_not_found"
	projectSyncUpstreamError    = "upstream_error"

	projectSyncInvalidRequest = "invalid_request"
	projectSyncTaskNotFound   = "task_not_found"
	projectSyncNoProjectBound = "no_project_bound"
	projectSyncPeerNotConfig  = "peer_not_configured"
)

// projectItemSync is ORA's items/upsert answer, decoded.
type projectItemSync struct {
	ItemID       string
	TitleSynced  bool
	TitleStatus  string
	TitleNote    string
	StatusSynced bool
	StatusNote   string
}

func decodeProjectItemSync(out map[string]interface{}) projectItemSync {
	s := projectItemSync{
		ItemID:      strFromAny(out["item_id"]),
		TitleStatus: strFromAny(out["title_status"]),
		TitleNote:   strFromAny(out["title_note"]),
		StatusNote:  strFromAny(out["status_note"]),
	}
	s.TitleSynced, _ = out["title_synced"].(bool)
	s.StatusSynced, _ = out["status_synced"].(bool)
	// An older ORA that predates title reporting says nothing at all. Treat the
	// silence as unknown rather than as success.
	if s.TitleStatus == "" {
		s.TitleStatus = projectSyncUpstreamError
		if s.TitleNote == "" {
			s.TitleNote = "ORA did not report a title_status; it may predate Projects v2 title sync"
		}
	}
	return s
}

// projectSyncHTTPStatus maps an ORA fault onto the status OPM answers with.
func projectSyncHTTPStatus(err error) (int, string) {
	var fault *peerIssueFault
	if errors.As(err, &fault) {
		switch fault.Status {
		case projectSyncMissingPerm:
			return http.StatusForbidden, fault.Status
		case projectSyncItemNotFound:
			return http.StatusNotFound, fault.Status
		case projectSyncTitleUnsupported:
			return http.StatusUnprocessableEntity, fault.Status
		}
		if fault.HTTPStatus == http.StatusForbidden || fault.HTTPStatus == http.StatusNotFound {
			return fault.HTTPStatus, fault.Status
		}
		return http.StatusBadGateway, fault.Status
	}
	return http.StatusBadGateway, projectSyncUpstreamError
}

// projectStatusHTTPCode maps a relayed title_status onto an HTTP code.
func projectStatusHTTPCode(status string) int {
	switch status {
	case projectSyncMissingPerm:
		return http.StatusForbidden
	case projectSyncItemNotFound:
		return http.StatusNotFound
	case projectSyncTitleUnsupported:
		return http.StatusUnprocessableEntity
	case projectSyncOK, projectSyncNothingToSync:
		return http.StatusOK
	case projectSyncNoProjectBound, projectSyncInvalidRequest:
		return http.StatusBadRequest
	case projectSyncTaskNotFound:
		return http.StatusNotFound
	case projectSyncPeerNotConfig:
		return http.StatusServiceUnavailable
	}
	return http.StatusBadGateway
}

// recordProjectSyncFailure persists the concrete reason on the task and in the
// spec log. Returns the HTTP code and machine-readable status to answer with.
func recordProjectSyncFailure(store *Store, p Project, specID, op, status, reason string) {
	if specID == "" {
		return
	}
	_, _ = store.MutateTask(p.ID, specID, func(t *Task) {
		t.GithubProjectSyncError = op + ": " + reason
	})
	_ = store.AppendSpecLog(p.ID, specID, projectSyncLogPhase,
		fmt.Sprintf("[%s] %s FAILED status=%s: %s\n",
			nowUTC().Format(time.RFC3339), op, status, reason))
}

// recordProjectSyncSuccess stamps the sync time and clears the stale error, so the
// UI never shows a failure that has since been resolved.
func recordProjectSyncSuccess(store *Store, p Project, specID, ghProjectID, itemID string) (Task, error) {
	return store.MutateTask(p.ID, specID, func(t *Task) {
		if ghProjectID != "" {
			t.GithubProjectID = ghProjectID
		}
		if itemID != "" {
			t.GithubProjectItemID = itemID
		}
		now := nowUTC()
		t.GithubProjectSyncedAt = &now
		t.GithubProjectSyncError = ""
	})
}

// clearProjectLink drops the board binding from a task. The board item itself is
// left alone on GitHub — unbinding is an OPM-side operation.
func clearProjectLink(t *Task) {
	t.GithubProjectID = ""
	t.GithubProjectItemID = ""
	t.GithubProjectSyncedAt = nil
	t.GithubProjectSyncError = ""
}

// projectSyncOutcome is the machine-readable result of one sync attempt.
type projectSyncOutcome struct {
	Status  string
	Reason  string
	ItemID  string
	Synced  bool
	Missing []string
	Task    Task
}

// syncTaskProjectItem pushes a task's title, description and column to its board
// item, recording the outcome on the task and in the spec log either way.
//
// It never returns a bare nil for a sync that did not happen: Status always says
// what became of it.
func syncTaskProjectItem(ctx context.Context, store *Store, p Project, t Task, op, userID string) projectSyncOutcome {
	if !peerORAConfigured() {
		return projectSyncOutcome{
			Status: projectSyncPeerNotConfig,
			Reason: "PEER_ORA_URL is not set, so no board sync was attempted",
			Task:   t,
		}
	}
	ghProjectID := t.GithubProjectID
	if ghProjectID == "" {
		ghProjectID = p.GithubProjectID
	}
	if ghProjectID == "" {
		return projectSyncOutcome{
			Status: projectSyncNoProjectBound,
			Reason: "no GitHub Project is bound to this task or project",
			Task:   t,
		}
	}
	if strings.TrimSpace(t.Title) == "" {
		return projectSyncOutcome{
			Status: projectSyncNothingToSync,
			Reason: "task has no title to sync",
			Task:   t,
		}
	}

	out, err := peerUpsertProjectItem(ctx, p.OrganizationID, userID, p.ConnectorID, ghProjectID,
		t.GithubProjectItemID, t.Title, t.Description, t.Status)
	if err != nil {
		_, status := projectSyncHTTPStatus(err)
		reason := err.Error()
		recordProjectSyncFailure(store, p, t.SpecID, op, status, reason)
		res := projectSyncOutcome{Status: status, Reason: reason, Task: t}
		var fault *peerIssueFault
		if errors.As(err, &fault) {
			res.Missing = fault.Missing
		}
		if stored, gerr := store.GetTask(p.ID, t.SpecID); gerr == nil {
			res.Task = stored
		}
		return res
	}

	sync := decodeProjectItemSync(out)
	itemID := sync.ItemID
	if itemID == "" {
		itemID = t.GithubProjectItemID
	}

	// A 200 whose title did not land is still a failure worth persisting.
	if !sync.TitleSynced && sync.TitleStatus != projectSyncNothingToSync {
		reason := sync.TitleNote
		if reason == "" {
			reason = "ORA reported title_synced=false"
		}
		recordProjectSyncFailure(store, p, t.SpecID, op, sync.TitleStatus, reason)
		// The item id is still worth keeping — the card exists, only the rename failed.
		res := projectSyncOutcome{Status: sync.TitleStatus, Reason: reason, ItemID: itemID, Task: t}
		if itemID != "" && itemID != t.GithubProjectItemID {
			_, _ = store.MutateTask(p.ID, t.SpecID, func(tt *Task) {
				tt.GithubProjectID = ghProjectID
				tt.GithubProjectItemID = itemID
			})
		}
		if stored, gerr := store.GetTask(p.ID, t.SpecID); gerr == nil {
			res.Task = stored
		}
		return res
	}

	updated, uerr := recordProjectSyncSuccess(store, p, t.SpecID, ghProjectID, itemID)
	if uerr != nil {
		return projectSyncOutcome{
			Status: projectSyncUpstreamError,
			Reason: "board updated but the task could not be saved: " + uerr.Error(),
			ItemID: itemID, Task: t,
		}
	}
	status := sync.TitleStatus
	if status == "" {
		status = projectSyncOK
	}
	return projectSyncOutcome{Status: status, Reason: "", ItemID: itemID, Synced: true, Task: updated}
}

// payload renders the outcome for an HTTP response.
func (o projectSyncOutcome) payload() map[string]interface{} {
	out := map[string]interface{}{
		"status":       o.Status,
		"titleSynced":  o.Synced,
		"githubItemId": o.ItemID,
	}
	if o.Reason != "" {
		out["error"] = o.Reason
	}
	if len(o.Missing) > 0 {
		out["missing"] = o.Missing
	}
	return out
}

// ok is true when the board genuinely matches, or when there was deliberately
// nothing to push.
func (o projectSyncOutcome) ok() bool {
	return o.Synced || o.Status == projectSyncNothingToSync
}

// taskTextChanged is true when an edit touched a field the board item mirrors.
func taskTextChanged(prev, next Task) bool {
	return prev.Title != next.Title || prev.Description != next.Description
}

// syncTaskProjectAfterEdit re-syncs a task whose title or description changed.
// The caller's edit already succeeded, so this cannot fail the request — but the
// outcome is persisted on the task and logged, and returned for the response.
func syncTaskProjectAfterEdit(store *Store, p Project, t Task, userID string) projectSyncOutcome {
	if t.GithubProjectItemID == "" {
		return projectSyncOutcome{Status: projectSyncNoProjectBound, Task: t,
			Reason: "task is not bound to a board item"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return syncTaskProjectItem(ctx, store, p, t, "push title/description", userID)
}

// handleProjectUnbind removes the project-level Projects v2 binding.
func handleProjectUnbind(w http.ResponseWriter, r *http.Request, store *Store, p Project) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	prevID := p.GithubProjectID
	prevTitle := p.GithubProjectTitle
	if prevID == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
			"error": "no GitHub Project is bound to this OPM project", "status": "not_bound",
		})
		return
	}
	updated, err := store.UnbindGitHubProject(p.ID)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, map[string]interface{}{
		"ok": true, "status": projectSyncOK,
		"unboundProjectId": prevID, "unboundProjectTitle": prevTitle,
		"note":    "the GitHub Project and its items were left untouched; task-level binds are unaffected",
		"project": updated,
	})
}

// handleTaskProjectUnbind removes a single task's board-item binding.
func handleTaskProjectUnbind(w http.ResponseWriter, r *http.Request, store *Store, p Project) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	var body struct {
		SpecID string `json:"specId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
			"error": "invalid json", "status": projectSyncInvalidRequest,
		})
		return
	}
	specID := strings.TrimSpace(body.SpecID)
	if specID == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
			"error": "specId required", "status": projectSyncInvalidRequest,
		})
		return
	}
	prev, err := store.GetTask(p.ID, specID)
	if err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{
			"error": err.Error(), "status": projectSyncTaskNotFound,
		})
		return
	}
	if prev.GithubProjectItemID == "" && prev.GithubProjectID == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
			"error": "task is not bound to a board item", "status": "not_bound", "specId": specID,
		})
		return
	}
	updated, err := store.MutateTask(p.ID, specID, clearProjectLink)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	_ = store.AppendSpecLog(p.ID, specID, projectSyncLogPhase,
		fmt.Sprintf("[%s] unbind board item %s (item left on the board)\n",
			nowUTC().Format(time.RFC3339), prev.GithubProjectItemID))
	writeJSON(w, map[string]interface{}{
		"ok": true, "status": projectSyncOK, "specId": specID,
		"unboundItemId": prev.GithubProjectItemID,
		"note":          "the board item was left untouched",
		"task":          updated,
	})
}
