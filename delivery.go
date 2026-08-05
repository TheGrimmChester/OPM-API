package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Code delivery: turn a recorded change set into a branch, a commit, a push and a
// pull request on the linked repository.
//
// The contract is fail-closed and honest, matching the issue-sync surface:
//   - every failure is persisted on the task (DeliveryStatus + DeliveryError),
//     appended to the task's spec log, and returned with a machine-readable
//     `status` — never swallowed
//   - a run that produced no file changes answers no_changes_produced, and is
//     never reported as a delivery
//   - the push credential comes from ORA per request and is discarded with the
//     workspace; OPM writes no GitHub credential to disk or logs
//
// Every step that can fail has its own status, so an operator (and the UI) can
// tell "grant a permission" apart from "the model produced nothing".
const (
	deliveryStatusDelivered = "delivered"
	// deliveryStatusNoChanges is the honest answer when there is nothing to ship.
	deliveryStatusNoChanges             = "no_changes_produced"
	deliveryStatusNotLinkedRepo         = "not_linked_repo"
	deliveryStatusPeerNotConfigured     = "peer_not_configured"
	deliveryStatusTaskNotFound          = "task_not_found"
	deliveryStatusInvalidRequest        = "invalid_request"
	deliveryStatusWorkspaceUnavailable  = "workspace_unavailable"
	deliveryStatusUnsafePath            = "unsafe_path"
	deliveryStatusChangeSetTooLarge     = "change_set_too_large"
	deliveryStatusApplyFailed           = "apply_failed"
	deliveryStatusGitFailed             = "git_failed"
	deliveryStatusPushRejected          = "push_rejected"
	deliveryStatusPushFailed            = "push_failed"
	deliveryStatusMissingPushPermission = "missing_contents_permission"
	deliveryStatusMissingPRPermission   = "missing_pull_requests_permission"
	deliveryStatusUpstreamError         = "upstream_error"
)

// deliveryLogPhase is the spec-log phase every delivery event is written to.
const deliveryLogPhase = "delivery"

// deliveryHTTPCode maps a delivery status onto the HTTP code OPM answers with.
func deliveryHTTPCode(status string) int {
	switch status {
	case deliveryStatusDelivered:
		return http.StatusOK
	case deliveryStatusInvalidRequest, deliveryStatusNotLinkedRepo,
		deliveryStatusUnsafePath, deliveryStatusChangeSetTooLarge:
		return http.StatusBadRequest
	case deliveryStatusTaskNotFound:
		return http.StatusNotFound
	case deliveryStatusNoChanges, deliveryStatusPushRejected:
		return http.StatusConflict
	case deliveryStatusMissingPushPermission, deliveryStatusMissingPRPermission:
		return http.StatusForbidden
	case deliveryStatusPeerNotConfigured:
		return http.StatusServiceUnavailable
	case deliveryStatusApplyFailed, deliveryStatusGitFailed,
		deliveryStatusWorkspaceUnavailable, deliveryStatusPushFailed:
		return http.StatusUnprocessableEntity
	default:
		return http.StatusBadGateway
	}
}

// deliveryFaultStatus translates an ORA fault into an OPM delivery status, so
// ORA's reason reaches the caller instead of a generic upstream error.
func deliveryFaultStatus(err error) string {
	var fault *peerDeliveryFault
	if errors.As(err, &fault) {
		switch fault.Status {
		case "missing_pull_requests_permission":
			return deliveryStatusMissingPRPermission
		case "missing_contents_permission":
			return deliveryStatusMissingPushPermission
		case "no_commits_between":
			return deliveryStatusNoChanges
		case "head_branch_not_found":
			return deliveryStatusPushFailed
		}
		if fault.HTTPStatus == http.StatusForbidden {
			return deliveryStatusMissingPRPermission
		}
	}
	return deliveryStatusUpstreamError
}

// deliveryResult is the outcome of one delivery attempt.
type deliveryResult struct {
	Status    string
	Branch    string
	CommitSha string
	PRNumber  int
	PRURL     string
	PRState   string
	Files     []string
	Skipped   []string
	// AlreadyExisted is true when the pull request was already open.
	AlreadyExisted bool
	Note           string
}

// recordDeliveryFailure persists the concrete reason on the task and in the spec
// log, then writes the error response. Nothing about a failed delivery is silent.
func recordDeliveryFailure(w http.ResponseWriter, store *Store, p Project, specID, status string, err error, extra map[string]interface{}) {
	reason := err.Error()
	_, _ = store.MutateTask(p.ID, specID, func(t *Task) {
		t.DeliveryStatus = status
		t.DeliveryError = reason
	})
	_ = store.AppendSpecLog(p.ID, specID, deliveryLogPhase,
		fmt.Sprintf("[%s] deliver FAILED status=%s: %s\n",
			nowUTC().Format(time.RFC3339), status, reason))

	payload := map[string]interface{}{"ok": false, "error": reason, "status": status, "specId": specID}
	var fault *peerDeliveryFault
	if errors.As(err, &fault) {
		if fault.Detail != "" {
			payload["detail"] = fault.Detail
		}
		if len(fault.Missing) > 0 {
			payload["missing"] = fault.Missing
		}
	}
	for k, v := range extra {
		payload[k] = v
	}
	writeJSONStatus(w, deliveryHTTPCode(status), payload)
}

// deliverCommitMessage builds the commit subject/body. The recorded message from
// the runner wins; otherwise a plain subject derived from the task is used. No
// co-author trailers are ever added.
func deliverCommitMessage(t Task, cs ChangeSet) string {
	if msg := strings.TrimSpace(cs.CommitMessage); msg != "" {
		return msg
	}
	title := strings.TrimSpace(t.Title)
	if title == "" {
		title = t.SpecID
	}
	subject := truncateRunes(title, 68)
	body := strings.TrimSpace(t.Description)
	out := subject + "\n\nTask: " + t.SpecID
	if body != "" {
		out += "\n\n" + truncateRunes(body, 600)
	}
	return out
}

// deliverPullRequestBody describes the delivery for reviewers. It states plainly
// what produced the change so a reviewer is never misled about its origin.
func deliverPullRequestBody(t Task, cs ChangeSet, files []string) string {
	var b strings.Builder
	b.WriteString("Delivers OPM task `" + t.SpecID + "`")
	if strings.TrimSpace(t.Title) != "" {
		b.WriteString(" — " + t.Title)
	}
	b.WriteString(".\n\n")
	if desc := strings.TrimSpace(t.Description); desc != "" {
		b.WriteString(truncateRunes(desc, 1000) + "\n\n")
	}
	b.WriteString("### Changed files\n\n")
	for _, f := range files {
		b.WriteString("- `" + f + "`\n")
	}
	b.WriteString("\n### Provenance\n\n")
	switch cs.Source {
	case "model":
		b.WriteString("- Produced by the OPM task runner")
		if cs.Model != "" {
			b.WriteString(" (model `" + cs.Model + "`)")
		}
		b.WriteString("\n")
	default:
		b.WriteString("- Produced by the OPM task runner\n")
	}
	if cs.RunID != "" {
		b.WriteString("- Run id: `" + cs.RunID + "`\n")
	}
	if t.GithubIssueNumber > 0 {
		b.WriteString(fmt.Sprintf("- Linked issue: #%d\n", t.GithubIssueNumber))
	}
	if t.GithubMilestoneNumber > 0 {
		b.WriteString(fmt.Sprintf("- Milestone: #%d %s\n", t.GithubMilestoneNumber, t.GithubMilestoneTitle))
	}
	b.WriteString("\nReview this diff as you would any other pull request before merging.\n")
	return b.String()
}

// handleTaskDeliver serves POST /api/projects/{id}/tasks/{specId}/deliver.
// It runs the full chain synchronously: workspace → apply → commit → push → PR.
func handleTaskDeliver(w http.ResponseWriter, r *http.Request, store *Store, projectID, specID string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	org := resolveRequestOrg(r)
	p, err := store.GetProjectForOrg(projectID, org)
	if err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{
			"ok": false, "error": err.Error(), "status": deliveryStatusTaskNotFound,
		})
		return
	}

	var body struct {
		Branch string `json:"branch"`
		Base   string `json:"base"`
		Draft  bool   `json:"draft"`
	}
	// An absent body is fine — every field is optional.
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	if strings.TrimSpace(specID) == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]interface{}{
			"ok": false, "error": "specId required", "status": deliveryStatusInvalidRequest,
		})
		return
	}
	t, err := store.GetTask(p.ID, specID)
	if err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]interface{}{
			"ok": false, "error": err.Error(), "status": deliveryStatusTaskNotFound, "specId": specID,
		})
		return
	}
	if strings.TrimSpace(p.OwnerRepo) == "" || strings.TrimSpace(p.ConnectorID) == "" {
		recordDeliveryFailure(w, store, p, specID, deliveryStatusNotLinkedRepo,
			fmt.Errorf("this OPM project has no linked GitHub repository or connector"), nil)
		return
	}
	if !peerORAConfigured() {
		recordDeliveryFailure(w, store, p, specID, deliveryStatusPeerNotConfigured,
			fmt.Errorf("PEER_ORA_URL is not configured — push credentials and pull requests live in ORA"), nil)
		return
	}

	cs, err := store.GetChangeSet(p.ID, specID)
	if err != nil {
		recordDeliveryFailure(w, store, p, specID, deliveryStatusUpstreamError,
			fmt.Errorf("reading the recorded change set: %w", err), nil)
		return
	}
	if len(cs.Files) == 0 {
		note := strings.TrimSpace(cs.Note)
		if note == "" {
			note = "no automation run has recorded file changes for this task yet"
		}
		recordDeliveryFailure(w, store, p, specID, deliveryStatusNoChanges,
			fmt.Errorf("nothing to deliver: %s", note),
			map[string]interface{}{"changes": changeSetSummary(cs)})
		return
	}

	res, status, derr := runDelivery(r.Context(), store, p, t, cs, body.Branch, body.Base, body.Draft)
	if derr != nil {
		recordDeliveryFailure(w, store, p, specID, status, derr, map[string]interface{}{
			"branch": res.Branch, "commitSha": res.CommitSha,
		})
		return
	}

	now := nowUTC()
	updated, uerr := store.MutateTask(p.ID, specID, func(t *Task) {
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
	if uerr != nil {
		writeError(w, 400, uerr.Error())
		return
	}
	_ = store.ClearChangeSet(p.ID, specID)
	_ = store.AppendSpecLog(p.ID, specID, deliveryLogPhase,
		fmt.Sprintf("[%s] deliver OK branch=%s commit=%s pr=#%d files=%s\n",
			now.Format(time.RFC3339), res.Branch, shortSha(res.CommitSha), res.PRNumber,
			strings.Join(res.Files, ",")))

	out := map[string]interface{}{
		"ok": true, "status": deliveryStatusDelivered, "specId": specID,
		"branch": res.Branch, "commitSha": res.CommitSha,
		"prNumber": res.PRNumber, "prUrl": res.PRURL, "prState": res.PRState,
		"files": res.Files, "task": updated,
	}
	if len(res.Skipped) > 0 {
		out["skipped"] = res.Skipped
	}
	if res.AlreadyExisted {
		out["alreadyExisted"] = true
		out["note"] = "a pull request for this branch was already open; OPM reported the existing one"
	}
	writeJSON(w, out)
}

// runDeliveryFromWorkspace commits paths already applied in workDir (implementation session).
func runDeliveryFromWorkspace(ctx context.Context, p Project, t Task, workDir string, cs ChangeSet, wantBranch, wantBase string, draft bool) (deliveryResult, string, error) {
	res := deliveryResult{}
	if workspaceIsStub(workDir) {
		return res, deliveryStatusWorkspaceUnavailable,
			fmt.Errorf("the session workspace is not a git clone")
	}
	paths := cs.paths()
	if len(paths) == 0 {
		return res, deliveryStatusNoChanges, fmt.Errorf("nothing to deliver: no paths in change set")
	}

	base := strings.TrimSpace(wantBase)
	if base == "" {
		base = nz(p.DefaultBranch, "main")
	}
	branch := deliveryBranchName(t.SpecID, wantBranch)
	if branch == base {
		return res, deliveryStatusInvalidRequest,
			fmt.Errorf("the delivery branch would equal the base branch (%s)", base)
	}
	res.Branch = branch

	g := newGitRunner(workDir)
	defer g.close()
	commit, status, err := gitCommitOnBranch(g, branch, deliverCommitMessage(t, cs), paths)
	if err != nil {
		return res, status, err
	}
	res.CommitSha = commit.Sha
	res.Files = commit.Staged

	cred, err := peerPushCredentials(ctx, p.OrganizationID, p.ConnectorID, p.OwnerRepo)
	if err != nil {
		return res, deliveryFaultStatus(err), fmt.Errorf("push credentials: %w", err)
	}
	g.withToken(cred.Token)
	if _, status, err := gitPushBranch(g, cred.CloneURL, branch); err != nil {
		return res, status, err
	}
	g.close()

	title := strings.TrimSpace(t.Title)
	if title == "" {
		title = "Deliver " + t.SpecID
	}
	pr, err := peerCreatePullRequest(ctx, p.OrganizationID, p.ConnectorID, p.OwnerRepo,
		truncateRunes(title, 120), deliverPullRequestBody(t, cs, commit.Staged),
		branch, base, draft)
	if err != nil {
		return res, deliveryFaultStatus(err), fmt.Errorf("opening the pull request: %w", err)
	}
	res.PRNumber = pr.Number
	res.PRURL = pr.HTMLURL
	res.PRState = pr.State
	res.AlreadyExisted = pr.AlreadyExisted
	res.Status = deliveryStatusDelivered
	return res, "", nil
}

// runDelivery performs workspace → apply → commit → push → pull request.
// It returns the partial result alongside the failing status so the caller can
// persist how far the delivery got.
func runDelivery(ctx context.Context, store *Store, p Project, t Task, cs ChangeSet, wantBranch, wantBase string, draft bool) (deliveryResult, string, error) {
	res := deliveryResult{}

	deliveryID := "deliver-" + uuid.NewString()
	defer cleanupWorkspace(deliveryID)
	workDir, err := prepareJobWorkspace(p, deliveryID)
	if err != nil {
		return res, deliveryStatusWorkspaceUnavailable,
			fmt.Errorf("preparing the delivery workspace: %w", err)
	}
	if workspaceIsStub(workDir) {
		return res, deliveryStatusWorkspaceUnavailable, fmt.Errorf(
			"the workspace is not a git clone — ORA clone credentials are unavailable for %s", p.OwnerRepo)
	}

	applied, err := applyChangeSet(workDir, cs, deliveryLimits())
	res.Files = applied.Applied
	res.Skipped = applied.Skipped
	if err != nil {
		status := applied.Status
		if status == "" {
			status = deliveryStatusApplyFailed
		}
		return res, status, err
	}

	base := strings.TrimSpace(wantBase)
	if base == "" {
		base = nz(p.DefaultBranch, "main")
	}
	branch := deliveryBranchName(t.SpecID, wantBranch)
	if branch == base {
		return res, deliveryStatusInvalidRequest,
			fmt.Errorf("the delivery branch would equal the base branch (%s)", base)
	}
	res.Branch = branch

	g := newGitRunner(workDir)
	defer g.close()
	commit, status, err := gitCommitOnBranch(g, branch, deliverCommitMessage(t, cs), applied.Applied)
	if err != nil {
		return res, status, err
	}
	res.CommitSha = commit.Sha

	cred, err := peerPushCredentials(ctx, p.OrganizationID, p.ConnectorID, p.OwnerRepo)
	if err != nil {
		return res, deliveryFaultStatus(err), fmt.Errorf("push credentials: %w", err)
	}
	g.withToken(cred.Token)
	if _, status, err := gitPushBranch(g, cred.CloneURL, branch); err != nil {
		return res, status, err
	}
	// The credential has served its single purpose; drop it before the peer call.
	g.close()

	title := strings.TrimSpace(t.Title)
	if title == "" {
		title = "Deliver " + t.SpecID
	}
	pr, err := peerCreatePullRequest(ctx, p.OrganizationID, p.ConnectorID, p.OwnerRepo,
		truncateRunes(title, 120), deliverPullRequestBody(t, cs, applied.Applied),
		branch, base, draft)
	if err != nil {
		return res, deliveryFaultStatus(err), fmt.Errorf("opening the pull request: %w", err)
	}
	res.PRNumber = pr.Number
	res.PRURL = pr.HTMLURL
	res.PRState = pr.State
	res.AlreadyExisted = pr.AlreadyExisted
	res.Status = deliveryStatusDelivered
	return res, "", nil
}

// handleTaskChanges serves GET /api/projects/{id}/tasks/{specId}/changes — the
// pending change set summary. Paths and modes only; never file bodies.
func handleTaskChanges(w http.ResponseWriter, r *http.Request, store *Store, projectID, specID string) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	cs, err := store.GetChangeSet(projectID, specID)
	if err != nil {
		writeError(w, 404, err.Error())
		return
	}
	out := changeSetSummary(cs)
	out["specId"] = specID
	out["deliverable"] = len(cs.Files) > 0
	if len(cs.Files) == 0 {
		out["note"] = nz(cs.Note, "no automation run has recorded file changes for this task yet")
	}
	writeJSON(w, out)
}

func shortSha(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
