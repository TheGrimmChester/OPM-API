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

// OPM task ↔ GitHub Issue sync under /api/projects/{id}/github/issues/…
//
// Direction of authority is deliberate and asymmetric:
//   push — OPM owns title, description and board column; they overwrite the issue.
//   pull — GitHub owns state, assignee, labels and milestone; they are mirrored
//          onto the task. The issue title is mirrored into GithubIssueTitle but
//          does NOT overwrite the OPM title unless the caller opts in with
//          adoptTitle, so a refresh can never silently discard a local edit.
//
// Every failure is persisted on the task (GithubIssueSyncError) and written to
// the spec log before it is returned, so a broken link is visible in the API, the
// UI and the logs instead of degrading into a no-op.

const issueSyncLogPhase = "github-issue-sync"

// applyIssuePatch maps the issue-link fields of a PATCH body onto a task.
// Setting githubIssueNumber to 0 or less clears the whole link.
func applyIssuePatch(t *Task, patch map[string]interface{}) {
	if v, ok := patch["githubIssueNumber"].(float64); ok {
		t.GithubIssueNumber = int(v)
		if t.GithubIssueNumber <= 0 {
			clearIssueLink(t)
		}
	}
	if v, ok := patch["githubIssueUrl"].(string); ok {
		t.GithubIssueURL = v
	}
	if v, ok := patch["githubIssueState"].(string); ok {
		t.GithubIssueState = v
	}
	if v, ok := patch["githubIssueTitle"].(string); ok {
		t.GithubIssueTitle = v
	}
}

func clearIssueLink(t *Task) {
	t.GithubIssueNumber = 0
	t.GithubIssueURL = ""
	t.GithubIssueState = ""
	t.GithubIssueTitle = ""
	t.GithubIssueAssignee = ""
	t.GithubIssueLabels = nil
	t.GithubIssueSyncedAt = nil
	t.GithubIssueSyncError = ""
}

// issueMeta is the normalized issue view decoded from an ORA peer response.
type issueMeta struct {
	Number         int
	Title          string
	Body           string
	State          string
	HTMLURL        string
	Labels         []string
	Milestone      int
	MilestoneTitle string
	Assignees      []string
}

func (m issueMeta) primaryAssignee() string {
	if len(m.Assignees) == 0 {
		return ""
	}
	return m.Assignees[0]
}

func (m issueMeta) payload() map[string]interface{} {
	labels := m.Labels
	if labels == nil {
		labels = []string{}
	}
	return map[string]interface{}{
		"number": m.Number, "title": m.Title, "state": m.State, "htmlUrl": m.HTMLURL,
		"labels": labels, "milestone": m.Milestone, "milestoneTitle": m.MilestoneTitle,
		"assignee": m.primaryAssignee(),
	}
}

func decodeIssueMeta(out map[string]interface{}) (issueMeta, error) {
	raw, ok := out["issue"].(map[string]interface{})
	if !ok {
		return issueMeta{}, fmt.Errorf("ora response did not contain an issue object")
	}
	m := issueMeta{
		Number:         intFromAny(raw["number"]),
		Title:          strFromAny(raw["title"]),
		Body:           strFromAny(raw["body"]),
		State:          strFromAny(raw["state"]),
		HTMLURL:        strFromAny(raw["html_url"]),
		Milestone:      intFromAny(raw["milestone"]),
		MilestoneTitle: strFromAny(raw["milestone_title"]),
	}
	m.Labels = stringsFromAny(raw["labels"])
	m.Assignees = stringsFromAny(raw["assignees"])
	if m.Number <= 0 {
		return issueMeta{}, fmt.Errorf("ora response contained no issue number")
	}
	return m, nil
}

func stringsFromAny(v interface{}) []string {
	list, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s := strFromAny(item); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// issueHTTPStatus maps an ORA fault onto the status OPM answers with, so the
// caller can branch on the cause without parsing prose.
func issueHTTPStatus(err error) (int, string) {
	var fault *peerIssueFault
	if errors.As(err, &fault) {
		switch fault.Status {
		case "missing_issues_permission":
			return http.StatusForbidden, fault.Status
		case "issue_not_found":
			return http.StatusNotFound, fault.Status
		}
		if fault.HTTPStatus == http.StatusForbidden || fault.HTTPStatus == http.StatusNotFound {
			return fault.HTTPStatus, fault.Status
		}
		return http.StatusBadGateway, fault.Status
	}
	return http.StatusBadGateway, "upstream_error"
}

// recordIssueFailure persists the concrete reason on the task and in the spec log,
// then writes the error response. Nothing about a failed sync is silent.
func recordIssueFailure(w http.ResponseWriter, store *Store, p Project, specID, op string, err error) {
	reason := err.Error()
	code, status := issueHTTPStatus(err)

	_, _ = store.MutateTask(p.ID, specID, func(t *Task) {
		t.GithubIssueSyncError = op + ": " + reason
	})
	_ = store.AppendSpecLog(p.ID, specID, issueSyncLogPhase,
		fmt.Sprintf("[%s] %s FAILED status=%s: %s\n",
			nowUTC().Format(time.RFC3339), op, status, reason))

	payload := map[string]interface{}{"error": reason, "status": status, "specId": specID}
	var fault *peerIssueFault
	if errors.As(err, &fault) {
		if fault.Detail != "" {
			payload["detail"] = fault.Detail
		}
		if len(fault.Missing) > 0 {
			payload["missing"] = fault.Missing
		}
	}
	writeJSONStatus(w, code, payload)
}

func writeJSONStatus(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// issueSyncContext resolves the task and guards the preconditions shared by
// every issue operation.
func issueSyncContext(w http.ResponseWriter, store *Store, p Project, specID string, requireLink bool) (Task, bool) {
	if strings.TrimSpace(specID) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
			"error": "specId required", "status": "invalid_request",
		})
		return Task{}, false
	}
	if strings.TrimSpace(p.OwnerRepo) == "" || strings.TrimSpace(p.ConnectorID) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
			"error":  "this OPM project has no linked GitHub repository or connector",
			"status": "not_linked_repo",
		})
		return Task{}, false
	}
	t, err := store.GetTask(p.ID, specID)
	if err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{
			"error": err.Error(), "status": "task_not_found",
		})
		return Task{}, false
	}
	if requireLink && t.GithubIssueNumber <= 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
			"error":  "task is not linked to a GitHub issue — link one first",
			"status": "no_issue_linked", "specId": specID,
		})
		return Task{}, false
	}
	return t, true
}

// applyIssueMirror copies the GitHub-owned fields onto the task and clears the
// stale sync error. Returns the names of the fields that actually changed.
func applyIssueMirror(t *Task, m issueMeta) []string {
	changed := []string{}
	if t.GithubIssueState != m.State {
		t.GithubIssueState = m.State
		changed = append(changed, "state")
	}
	if t.GithubIssueTitle != m.Title {
		t.GithubIssueTitle = m.Title
		changed = append(changed, "title")
	}
	if a := m.primaryAssignee(); t.GithubIssueAssignee != a {
		t.GithubIssueAssignee = a
		changed = append(changed, "assignee")
	}
	if !sameStrings(t.GithubIssueLabels, m.Labels) {
		t.GithubIssueLabels = m.Labels
		changed = append(changed, "labels")
	}
	if m.Milestone != t.GithubMilestoneNumber {
		t.GithubMilestoneNumber = m.Milestone
		t.GithubMilestoneTitle = m.MilestoneTitle
		if m.Milestone <= 0 {
			t.GithubMilestoneTitle = ""
			t.GithubMilestoneURL = ""
		}
		changed = append(changed, "milestone")
	}
	t.GithubIssueNumber = m.Number
	t.GithubIssueURL = m.HTMLURL
	now := nowUTC()
	t.GithubIssueSyncedAt = &now
	t.GithubIssueSyncError = ""
	return changed
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func handleGitHubIssues(w http.ResponseWriter, r *http.Request, store *Store, p Project, rest []string) {
	if len(rest) == 0 {
		writeError(w, 404, "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	switch rest[0] {
	case "link":
		handleIssueLink(w, r, store, p)
	case "unlink":
		handleIssueUnlink(w, r, store, p)
	case "push":
		handleIssuePush(w, r, store, p)
	case "pull":
		handleIssuePull(w, r, store, p)
	default:
		writeError(w, 404, "not found")
	}
}

// handleIssueLink attaches an existing issue (issueNumber) or creates a new one
// from the task (create). Attaching verifies the issue exists first so a typo
// cannot persist a link that points at nothing.
func handleIssueLink(w http.ResponseWriter, r *http.Request, store *Store, p Project) {
	var body struct {
		SpecID      string   `json:"specId"`
		IssueNumber int      `json:"issueNumber"`
		Create      bool     `json:"create"`
		Title       string   `json:"title"`
		Body        string   `json:"body"`
		Labels      []string `json:"labels"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
			"error": "invalid json", "status": "invalid_request",
		})
		return
	}
	t, ok := issueSyncContext(w, store, p, body.SpecID, false)
	if !ok {
		return
	}
	if body.IssueNumber <= 0 && !body.Create {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
			"error":  "provide issueNumber to attach an existing issue, or create=true to open a new one",
			"status": "invalid_request",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	mode := "attached"
	var out map[string]interface{}
	var err error
	if body.IssueNumber > 0 {
		out, err = peerGetIssue(ctx, p.OrganizationID, p.ConnectorID, p.OwnerRepo, body.IssueNumber)
	} else {
		mode = "created"
		title := strings.TrimSpace(body.Title)
		if title == "" {
			title = t.Title
		}
		issueBody := body.Body
		if issueBody == "" {
			issueBody = t.Description
		}
		if strings.TrimSpace(title) == "" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
				"error": "cannot create an issue with an empty title", "status": "invalid_request",
			})
			return
		}
		out, err = peerCreateIssue(ctx, p.OrganizationID, p.ConnectorID, p.OwnerRepo,
			title, issueBody, body.Labels, t.GithubMilestoneNumber)
	}
	if err != nil {
		recordIssueFailure(w, store, p, body.SpecID, "link ("+mode+")", err)
		return
	}
	meta, err := decodeIssueMeta(out)
	if err != nil {
		recordIssueFailure(w, store, p, body.SpecID, "link ("+mode+")", err)
		return
	}

	updated, err := store.MutateTask(p.ID, body.SpecID, func(t *Task) {
		applyIssueMirror(t, meta)
	})
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	_ = store.AppendSpecLog(p.ID, body.SpecID, issueSyncLogPhase,
		fmt.Sprintf("[%s] link %s issue #%d %s\n",
			nowUTC().Format(time.RFC3339), mode, meta.Number, meta.HTMLURL))

	writeJSON(w, map[string]interface{}{
		"ok": true, "mode": mode, "specId": body.SpecID,
		"issue": meta.payload(), "task": updated,
	})
}

// handleIssueUnlink drops the link only. The GitHub issue is deliberately left
// untouched: OPM never closes or deletes an issue on unlink.
func handleIssueUnlink(w http.ResponseWriter, r *http.Request, store *Store, p Project) {
	var body struct {
		SpecID string `json:"specId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
			"error": "invalid json", "status": "invalid_request",
		})
		return
	}
	if strings.TrimSpace(body.SpecID) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
			"error": "specId required", "status": "invalid_request",
		})
		return
	}
	prev, err := store.GetTask(p.ID, body.SpecID)
	if err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{
			"error": err.Error(), "status": "task_not_found",
		})
		return
	}
	updated, err := store.MutateTask(p.ID, body.SpecID, clearIssueLink)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	_ = store.AppendSpecLog(p.ID, body.SpecID, issueSyncLogPhase,
		fmt.Sprintf("[%s] unlink issue #%d (issue left open on GitHub)\n",
			nowUTC().Format(time.RFC3339), prev.GithubIssueNumber))
	writeJSON(w, map[string]interface{}{
		"ok": true, "specId": body.SpecID, "unlinkedIssueNumber": prev.GithubIssueNumber,
		"note": "the GitHub issue was left untouched", "task": updated,
	})
}

// handleIssuePush sends the OPM-owned fields to the issue: title, body, and the
// state implied by the board column. Milestone is pushed when the task has one;
// a task without a milestone does not clear the issue's.
func handleIssuePush(w http.ResponseWriter, r *http.Request, store *Store, p Project) {
	var body struct {
		SpecID string `json:"specId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
			"error": "invalid json", "status": "invalid_request",
		})
		return
	}
	t, ok := issueSyncContext(w, store, p, body.SpecID, true)
	if !ok {
		return
	}

	state, mapped := issueStateForColumn(t.Status)
	if !mapped {
		state = ""
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	out, err := peerUpdateIssue(ctx, p.OrganizationID, p.ConnectorID, p.OwnerRepo,
		t.GithubIssueNumber, t.Title, t.Description, state, t.GithubMilestoneNumber, nil)
	if err != nil {
		recordIssueFailure(w, store, p, body.SpecID, "push", err)
		return
	}
	meta, err := decodeIssueMeta(out)
	if err != nil {
		recordIssueFailure(w, store, p, body.SpecID, "push", err)
		return
	}
	pushed := stringsFromAny(out["applied"])
	if pushed == nil {
		pushed = []string{}
	}

	updated, err := store.MutateTask(p.ID, body.SpecID, func(t *Task) {
		applyIssueMirror(t, meta)
	})
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	_ = store.AppendSpecLog(p.ID, body.SpecID, issueSyncLogPhase,
		fmt.Sprintf("[%s] push issue #%d column=%s state=%s fields=%s\n",
			nowUTC().Format(time.RFC3339), t.GithubIssueNumber, t.Status,
			nz(state, "(unmapped column, state not pushed)"), strings.Join(pushed, ",")))

	result := map[string]interface{}{
		"ok": true, "specId": body.SpecID, "pushed": pushed,
		"boardColumn": t.Status, "issueState": state,
		"issue": meta.payload(), "task": updated,
	}
	if !mapped {
		result["note"] = fmt.Sprintf("board column %q has no issue-state mapping; state was not pushed", t.Status)
	}
	writeJSON(w, result)
}

// handleIssuePull refreshes the task from the issue. adoptTitle opts in to
// overwriting the OPM task title with the issue title; without it a divergence
// is reported rather than resolved, so no local edit is lost silently.
func handleIssuePull(w http.ResponseWriter, r *http.Request, store *Store, p Project) {
	var body struct {
		SpecID     string `json:"specId"`
		AdoptTitle bool   `json:"adoptTitle"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
			"error": "invalid json", "status": "invalid_request",
		})
		return
	}
	t, ok := issueSyncContext(w, store, p, body.SpecID, true)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	out, err := peerGetIssue(ctx, p.OrganizationID, p.ConnectorID, p.OwnerRepo, t.GithubIssueNumber)
	if err != nil {
		recordIssueFailure(w, store, p, body.SpecID, "pull", err)
		return
	}
	meta, err := decodeIssueMeta(out)
	if err != nil {
		recordIssueFailure(w, store, p, body.SpecID, "pull", err)
		return
	}

	var changed []string
	updated, err := store.MutateTask(p.ID, body.SpecID, func(t *Task) {
		changed = applyIssueMirror(t, meta)
		if body.AdoptTitle && strings.TrimSpace(meta.Title) != "" && t.Title != meta.Title {
			t.Title = meta.Title
			changed = append(changed, "taskTitle")
		}
	})
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}

	// State drives the board column through the documented mapping.
	movedTo := ""
	if column, move := columnForIssueState(meta.State, updated.Status); move {
		moved, mErr := store.MoveTask(p.ID, body.SpecID, column)
		if mErr != nil {
			_ = store.AppendSpecLog(p.ID, body.SpecID, issueSyncLogPhase,
				fmt.Sprintf("[%s] pull issue #%d: column move to %s failed: %v\n",
					nowUTC().Format(time.RFC3339), meta.Number, column, mErr))
		} else {
			movedTo = column
			updated = moved
			changed = append(changed, "boardColumn")
		}
	}

	titleDiverged := strings.TrimSpace(meta.Title) != "" && updated.Title != meta.Title
	_ = store.AppendSpecLog(p.ID, body.SpecID, issueSyncLogPhase,
		fmt.Sprintf("[%s] pull issue #%d state=%s changed=%s movedTo=%s\n",
			nowUTC().Format(time.RFC3339), meta.Number, meta.State,
			nz(strings.Join(changed, ","), "(nothing)"), nz(movedTo, "-")))

	if changed == nil {
		changed = []string{}
	}
	result := map[string]interface{}{
		"ok": true, "specId": body.SpecID, "changed": changed,
		"issue": meta.payload(), "task": updated, "titleDiverged": titleDiverged,
	}
	if movedTo != "" {
		result["movedTo"] = movedTo
	} else {
		result["movedTo"] = nil
	}
	if titleDiverged {
		result["note"] = "the issue title differs from the task title; OPM kept the task title (send adoptTitle=true to take the issue title)"
	}
	writeJSON(w, result)
}
