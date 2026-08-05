package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubProjectORA is a fake ORA peer for the Projects v2 routes. It records what OPM
// sent so a test can assert the title actually left OPM.
type stubProjectORA struct {
	server    *httptest.Server
	upsCode   int
	upsBody   string
	statCode  int
	statBody  string
	lastPath  string
	lastBody  map[string]interface{}
	upsCalls  int
	statCalls int
}

func newStubProjectORA(t *testing.T) *stubProjectORA {
	t.Helper()
	s := &stubProjectORA{upsCode: 200, statCode: 200,
		upsBody:  `{"ok":true,"item_id":"PVTI_1","title_synced":true,"title_status":"ok","status_synced":true}`,
		statBody: `{"ok":true,"status_synced":true,"status":"ok"}`}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.lastPath = r.URL.Path
		s.lastBody = map[string]interface{}{}
		_ = json.NewDecoder(r.Body).Decode(&s.lastBody)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/projects/items/upsert"):
			s.upsCalls++
			w.WriteHeader(s.upsCode)
			_, _ = w.Write([]byte(s.upsBody))
		case strings.HasSuffix(r.URL.Path, "/projects/items/status"):
			s.statCalls++
			w.WriteHeader(s.statCode)
			_, _ = w.Write([]byte(s.statBody))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":"unexpected path"}`))
		}
	}))
	t.Cleanup(s.server.Close)
	return s
}

// projectSyncFixture wires the real mux against the stub, with a project and a task
// already bound to a board item.
func projectSyncFixture(t *testing.T, ora *stubProjectORA) (*http.ServeMux, *Store, Project, Task) {
	t.Helper()
	t.Setenv("PEER_ORA_URL", ora.server.URL)
	t.Setenv("OPEN_SERVICE_JWT_SECRET", "test-secret")
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{
		OwnerRepo: "acme/demo", ConnectorID: "c1", OrganizationID: "default-org",
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err = store.BindGitHubProject(p.ID, "PVT_board", "Board", "https://github.com/orgs/acme/projects/1")
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(p.ID, "Local title", "Local body", false, true, "", "")
	if err != nil {
		t.Fatal(err)
	}
	task, err = store.UpdateTask(p.ID, task.SpecID, map[string]interface{}{
		"githubProjectId": "PVT_board", "githubProjectItemId": "PVTI_1",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	reg := func(pattern string, h http.HandlerFunc) { mux.HandleFunc(pattern, h) }
	registerOPMMux(mux, store, reg, reg)
	return mux, store, p, task
}

func postProject(t *testing.T, mux *http.ServeMux, p Project, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost,
		"/api/projects/"+p.ID+"/github/"+path, strings.NewReader(string(raw)))
	req.Header.Set("X-Organization-ID", "default-org")
	req.Header.Set("X-Project-ID", p.ID)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func patchTask(t *testing.T, mux *http.ServeMux, p Project, specID string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch,
		"/api/projects/"+p.ID+"/tasks/"+specID, strings.NewReader(string(raw)))
	req.Header.Set("X-Organization-ID", "default-org")
	req.Header.Set("X-Project-ID", p.ID)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func decodeJSON(t *testing.T, rr *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rr.Body.String())
	}
	return out
}

// assertSyncRecorded checks the four-point fail-closed contract: the reason is on
// the task, in the spec log, and the log names the machine-readable status.
func assertSyncRecorded(t *testing.T, store *Store, p Project, specID, wantStatus, wantIn string) {
	t.Helper()
	stored, err := store.GetTask(p.ID, specID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.GithubProjectSyncError == "" {
		t.Fatal("the failure must be persisted on the task")
	}
	if wantIn != "" && !strings.Contains(stored.GithubProjectSyncError, wantIn) {
		t.Fatalf("task error %q should mention %q", stored.GithubProjectSyncError, wantIn)
	}
	if stored.GithubProjectSyncedAt != nil {
		t.Fatal("a failed sync must not stamp githubProjectSyncedAt")
	}
	logs, err := store.GetSpecLogs(p.ID, specID)
	if err != nil {
		t.Fatal(err)
	}
	logged := logs[projectSyncLogPhase]
	if !strings.Contains(logged, "FAILED") {
		t.Fatalf("spec log should record the failure, got %q", logged)
	}
	if !strings.Contains(logged, wantStatus) {
		t.Fatalf("spec log should name status %q, got %q", wantStatus, logged)
	}
}

// --- successful title sync ---------------------------------------------------

func TestProjectSyncItemSuccessClearsErrorAndStamps(t *testing.T) {
	ora := newStubProjectORA(t)
	mux, store, p, task := projectSyncFixture(t, ora)

	rr := postProject(t, mux, p, "projects/sync-item", map[string]interface{}{
		"specId": task.SpecID, "title": "Renamed",
	})
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	out := decodeJSON(t, rr)
	if synced, _ := out["titleSynced"].(bool); !synced {
		t.Fatalf("want titleSynced true: %v", out)
	}
	if got := strFromAny(out["status"]); got != projectSyncOK {
		t.Fatalf("status=%q", got)
	}
	// The title must actually have left OPM.
	if got := strFromAny(ora.lastBody["title"]); got != "Renamed" {
		t.Fatalf("ORA received title=%q", got)
	}
	stored, err := store.GetTask(p.ID, task.SpecID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.GithubProjectSyncedAt == nil {
		t.Fatal("a successful sync must stamp githubProjectSyncedAt")
	}
	if stored.GithubProjectSyncError != "" {
		t.Fatalf("error should be clear, got %q", stored.GithubProjectSyncError)
	}
}

// --- the regression: a 200 whose title did not land is not success ----------

func TestProjectSyncItemPartialFailureIsNotSuccess(t *testing.T) {
	ora := newStubProjectORA(t)
	// ORA accepted the call but could not rename: the card is a real Issue.
	ora.upsBody = `{"ok":true,"item_id":"PVTI_1","title_synced":false,
		"title_status":"title_sync_unsupported",
		"title_note":"project item is ISSUE, not DRAFT_ISSUE"}`
	mux, store, p, task := projectSyncFixture(t, ora)

	rr := postProject(t, mux, p, "projects/sync-item", map[string]interface{}{
		"specId": task.SpecID, "title": "Renamed",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", rr.Code, rr.Body.String())
	}
	out := decodeJSON(t, rr)
	if got := strFromAny(out["status"]); got != projectSyncTitleUnsupported {
		t.Fatalf("status=%q, want %s", got, projectSyncTitleUnsupported)
	}
	if synced, _ := out["titleSynced"].(bool); synced {
		t.Fatal("titleSynced must be false")
	}
	assertSyncRecorded(t, store, p, task.SpecID, projectSyncTitleUnsupported, "ISSUE")
}

// --- missing permission relayed from ORA ------------------------------------

func TestProjectSyncItemMissingOrganizationProjects(t *testing.T) {
	ora := newStubProjectORA(t)
	ora.upsCode = http.StatusForbidden
	ora.upsBody = `{"ok":false,"status":"missing_organization_projects",
		"error":"GitHub App installation is missing organization projects write",
		"missing":["organization_projects"]}`
	mux, store, p, task := projectSyncFixture(t, ora)

	rr := postProject(t, mux, p, "projects/sync-item", map[string]interface{}{
		"specId": task.SpecID, "title": "Renamed",
	})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", rr.Code, rr.Body.String())
	}
	out := decodeJSON(t, rr)
	if got := strFromAny(out["status"]); got != projectSyncMissingPerm {
		t.Fatalf("status=%q, want %s", got, projectSyncMissingPerm)
	}
	if missing, ok := out["missing"].([]interface{}); !ok || len(missing) == 0 {
		t.Fatalf("the grantable permission must be relayed: %v", out)
	}
	assertSyncRecorded(t, store, p, task.SpecID, projectSyncMissingPerm, "organization projects")
}

func TestProjectSyncItemUpstreamErrorRecorded(t *testing.T) {
	ora := newStubProjectORA(t)
	ora.upsCode = http.StatusBadGateway
	ora.upsBody = `{"ok":false,"status":"upstream_error","error":"graphql 500: boom"}`
	mux, store, p, task := projectSyncFixture(t, ora)

	rr := postProject(t, mux, p, "projects/sync-item", map[string]interface{}{
		"specId": task.SpecID, "title": "Renamed",
	})
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d", rr.Code)
	}
	assertSyncRecorded(t, store, p, task.SpecID, projectSyncUpstreamError, "boom")
}

// An ORA that reports nothing about the title must not be read as success.
func TestProjectSyncItemSilentORAIsNotSuccess(t *testing.T) {
	ora := newStubProjectORA(t)
	ora.upsBody = `{"ok":true,"item_id":"PVTI_1"}`
	mux, store, p, task := projectSyncFixture(t, ora)

	rr := postProject(t, mux, p, "projects/sync-item", map[string]interface{}{
		"specId": task.SpecID, "title": "Renamed",
	})
	if rr.Code == 200 {
		t.Fatalf("a silent ORA must not read as success, got 200: %s", rr.Body.String())
	}
	out := decodeJSON(t, rr)
	if got := strFromAny(out["status"]); got != projectSyncUpstreamError {
		t.Fatalf("status=%q, want %s", got, projectSyncUpstreamError)
	}
	assertSyncRecorded(t, store, p, task.SpecID, projectSyncUpstreamError, "title_status")
}

// --- sync-task endpoint -----------------------------------------------------

func TestSyncTaskFailsClosedOnTitleFailure(t *testing.T) {
	ora := newStubProjectORA(t)
	ora.upsBody = `{"ok":true,"item_id":"PVTI_1","title_synced":false,
		"title_status":"item_not_found","title_note":"project item carries no draft issue content"}`
	mux, store, p, task := projectSyncFixture(t, ora)

	req := httptest.NewRequest(http.MethodPost,
		"/api/projects/"+p.ID+"/github/sync-task/"+task.SpecID, nil)
	req.Header.Set("X-Organization-ID", "default-org")
	req.Header.Set("X-Project-ID", p.ID)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", rr.Code, rr.Body.String())
	}
	out := decodeJSON(t, rr)
	if ok, _ := out["ok"].(bool); ok {
		t.Fatal("ok must be false when the board did not update")
	}
	if got := strFromAny(out["status"]); got != projectSyncItemNotFound {
		t.Fatalf("status=%q", got)
	}
	assertSyncRecorded(t, store, p, task.SpecID, projectSyncItemNotFound, "draft issue content")
}

// --- re-sync on title / description change ----------------------------------

func TestPatchTaskTitleResyncsBoard(t *testing.T) {
	ora := newStubProjectORA(t)
	mux, store, p, task := projectSyncFixture(t, ora)

	rr := patchTask(t, mux, p, task.SpecID, map[string]interface{}{"title": "Renamed via PATCH"})
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if ora.upsCalls != 1 {
		t.Fatalf("a title change must re-sync the board, got %d upsert calls", ora.upsCalls)
	}
	if got := strFromAny(ora.lastBody["title"]); got != "Renamed via PATCH" {
		t.Fatalf("ORA received title=%q", got)
	}
	// Response shape is still the task itself.
	out := decodeJSON(t, rr)
	if got := strFromAny(out["title"]); got != "Renamed via PATCH" {
		t.Fatalf("PATCH must still return the task, got %v", out)
	}
	stored, _ := store.GetTask(p.ID, task.SpecID)
	if stored.GithubProjectSyncedAt == nil {
		t.Fatal("a successful re-sync must stamp githubProjectSyncedAt")
	}
}

func TestPatchTaskDescriptionResyncsBoard(t *testing.T) {
	ora := newStubProjectORA(t)
	mux, _, p, task := projectSyncFixture(t, ora)

	rr := patchTask(t, mux, p, task.SpecID, map[string]interface{}{"description": "New description"})
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	if ora.upsCalls != 1 {
		t.Fatalf("a description change must re-sync, got %d upsert calls", ora.upsCalls)
	}
	if got := strFromAny(ora.lastBody["body"]); got != "New description" {
		t.Fatalf("ORA received body=%q", got)
	}
}

// A PATCH that changes neither title nor description must not touch the board.
func TestPatchTaskUnrelatedFieldDoesNotResync(t *testing.T) {
	ora := newStubProjectORA(t)
	mux, _, p, task := projectSyncFixture(t, ora)

	rr := patchTask(t, mux, p, task.SpecID, map[string]interface{}{"requireReviewBeforeCoding": true})
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	if ora.upsCalls != 0 {
		t.Fatalf("no text change must mean no board call, got %d", ora.upsCalls)
	}
}

// The PATCH still succeeds when the board sync fails, but the failure is recorded.
func TestPatchTaskTitleRecordsSyncFailure(t *testing.T) {
	ora := newStubProjectORA(t)
	ora.upsCode = http.StatusForbidden
	ora.upsBody = `{"ok":false,"status":"missing_organization_projects",
		"error":"installation is missing organization projects write","missing":["organization_projects"]}`
	mux, store, p, task := projectSyncFixture(t, ora)

	rr := patchTask(t, mux, p, task.SpecID, map[string]interface{}{"title": "Renamed"})
	// The local edit succeeded, so the request does not fail...
	if rr.Code != 200 {
		t.Fatalf("the local edit should still succeed, got %d: %s", rr.Code, rr.Body.String())
	}
	out := decodeJSON(t, rr)
	if got := strFromAny(out["title"]); got != "Renamed" {
		t.Fatalf("the edit must be applied, got %v", out)
	}
	// ...but the board failure is not silent: it is on the returned task.
	if got := strFromAny(out["githubProjectSyncError"]); got == "" {
		t.Fatalf("the returned task must carry the sync error: %v", out)
	}
	assertSyncRecorded(t, store, p, task.SpecID, projectSyncMissingPerm, "organization projects")
}

// --- unbind -----------------------------------------------------------------

func TestProjectUnbindClearsBinding(t *testing.T) {
	ora := newStubProjectORA(t)
	mux, store, p, _ := projectSyncFixture(t, ora)

	rr := postProject(t, mux, p, "projects/unbind", map[string]interface{}{})
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	out := decodeJSON(t, rr)
	if got := strFromAny(out["unboundProjectId"]); got != "PVT_board" {
		t.Fatalf("unboundProjectId=%q", got)
	}
	got, err := store.GetProject(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.GithubProjectID != "" || got.GithubProjectTitle != "" || got.GithubProjectURL != "" {
		t.Fatalf("binding should be cleared, got %+v", got)
	}
}

func TestProjectUnbindWhenNotBound(t *testing.T) {
	ora := newStubProjectORA(t)
	mux, store, p, _ := projectSyncFixture(t, ora)
	if _, err := store.UnbindGitHubProject(p.ID); err != nil {
		t.Fatal(err)
	}
	fresh, _ := store.GetProject(p.ID)

	rr := postProject(t, mux, fresh, "projects/unbind", map[string]interface{}{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if got := strFromAny(decodeJSON(t, rr)["status"]); got != "not_bound" {
		t.Fatalf("status=%q", got)
	}
}

func TestTaskProjectUnbindClearsItemBinding(t *testing.T) {
	ora := newStubProjectORA(t)
	mux, store, p, task := projectSyncFixture(t, ora)

	rr := postProject(t, mux, p, "projects/items/unbind", map[string]interface{}{"specId": task.SpecID})
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	out := decodeJSON(t, rr)
	if got := strFromAny(out["unboundItemId"]); got != "PVTI_1" {
		t.Fatalf("unboundItemId=%q", got)
	}
	stored, err := store.GetTask(p.ID, task.SpecID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.GithubProjectID != "" || stored.GithubProjectItemID != "" {
		t.Fatalf("task binding should be cleared, got %+v", stored)
	}
	// A later text edit must not call the board any more.
	before := ora.upsCalls
	_ = patchTask(t, mux, p, task.SpecID, map[string]interface{}{"title": "After unbind"})
	if ora.upsCalls != before {
		t.Fatal("an unbound task must not sync to the board")
	}
}

func TestTaskProjectUnbindWhenNotBound(t *testing.T) {
	ora := newStubProjectORA(t)
	mux, store, p, _ := projectSyncFixture(t, ora)
	fresh, err := store.CreateTask(p.ID, "Unbound", "", false, true, "", "")
	if err != nil {
		t.Fatal(err)
	}
	rr := postProject(t, mux, p, "projects/items/unbind", map[string]interface{}{"specId": fresh.SpecID})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if got := strFromAny(decodeJSON(t, rr)["status"]); got != "not_bound" {
		t.Fatalf("status=%q", got)
	}
}

func TestClearProjectLinkWipesEveryField(t *testing.T) {
	now := nowUTC()
	tk := &Task{
		GithubProjectID:        "PVT_1",
		GithubProjectItemID:    "PVTI_1",
		GithubProjectSyncedAt:  &now,
		GithubProjectSyncError: "boom",
	}
	clearProjectLink(tk)
	if tk.GithubProjectID != "" || tk.GithubProjectItemID != "" ||
		tk.GithubProjectSyncedAt != nil || tk.GithubProjectSyncError != "" {
		t.Fatalf("every board field must be wiped, got %+v", tk)
	}
}

// A blank bind must be rejected rather than silently unbinding.
func TestBindGitHubProjectRejectsBlankID(t *testing.T) {
	ora := newStubProjectORA(t)
	_, store, p, _ := projectSyncFixture(t, ora)

	if _, err := store.BindGitHubProject(p.ID, "   ", "", ""); err == nil {
		t.Fatal("a blank projectId must be rejected")
	}
	got, _ := store.GetProject(p.ID)
	if got.GithubProjectID != "PVT_board" {
		t.Fatalf("binding must be untouched, got %q", got.GithubProjectID)
	}
}

// --- status mapping ---------------------------------------------------------

func TestProjectSyncHTTPStatusMapping(t *testing.T) {
	cases := []struct {
		status string
		want   int
	}{
		{projectSyncMissingPerm, http.StatusForbidden},
		{projectSyncItemNotFound, http.StatusNotFound},
		{projectSyncTitleUnsupported, http.StatusUnprocessableEntity},
		{projectSyncUpstreamError, http.StatusBadGateway},
	}
	for _, c := range cases {
		code, status := projectSyncHTTPStatus(&peerIssueFault{HTTPStatus: 500, Status: c.status})
		if code != c.want || status != c.status {
			t.Fatalf("%s -> (%d,%s), want (%d,%s)", c.status, code, status, c.want, c.status)
		}
	}
	if code, status := projectSyncHTTPStatus(errTest("plain")); code != http.StatusBadGateway || status != projectSyncUpstreamError {
		t.Fatalf("untyped -> (%d,%s)", code, status)
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

func TestDecodeProjectItemSyncTreatsSilenceAsUnknown(t *testing.T) {
	s := decodeProjectItemSync(map[string]interface{}{"item_id": "PVTI_9"})
	if s.TitleSynced {
		t.Fatal("silence must not read as synced")
	}
	if s.TitleStatus != projectSyncUpstreamError {
		t.Fatalf("TitleStatus=%q", s.TitleStatus)
	}
	ok := decodeProjectItemSync(map[string]interface{}{
		"item_id": "PVTI_9", "title_synced": true, "title_status": "ok",
	})
	if !ok.TitleSynced || ok.TitleStatus != projectSyncOK {
		t.Fatalf("%+v", ok)
	}
}
