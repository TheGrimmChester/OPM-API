package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The push mapping must be total over BoardColumns: a column with no mapping
// would silently skip the state push.
func TestIssueStateForColumnCoversEveryBoardColumn(t *testing.T) {
	for _, col := range BoardColumns {
		state, ok := issueStateForColumn(col)
		if !ok {
			t.Fatalf("board column %q has no issue-state mapping", col)
		}
		if state != "open" && state != "closed" {
			t.Fatalf("column %q mapped to invalid state %q", col, state)
		}
	}
	if state, _ := issueStateForColumn("done"); state != "closed" {
		t.Fatalf("done must map to closed, got %q", state)
	}
	if state, _ := issueStateForColumn("human_review"); state != "open" {
		t.Fatalf("human_review must map to open, got %q", state)
	}
	if _, ok := issueStateForColumn("not_a_column"); ok {
		t.Fatal("unknown column must not map")
	}
}

// Pull is deliberately conservative: an open issue must not drag a task out of
// review columns, because five columns map to "open".
func TestColumnForIssueState(t *testing.T) {
	cases := []struct {
		issueState, current, wantColumn string
		wantMove                        bool
	}{
		{"closed", "in_progress", "done", true},
		{"closed", "human_review", "done", true},
		{"closed", "done", "", false},          // already there
		{"open", "done", "in_progress", true},  // reopened
		{"open", "human_review", "", false},    // no information in "open"
		{"open", "backlog", "", false},
		{"OPEN", "done", "in_progress", true},  // case-insensitive
		{"weird", "backlog", "", false},
	}
	for _, tc := range cases {
		col, move := columnForIssueState(tc.issueState, tc.current)
		if col != tc.wantColumn || move != tc.wantMove {
			t.Fatalf("state=%q current=%q → (%q,%v), want (%q,%v)",
				tc.issueState, tc.current, col, move, tc.wantColumn, tc.wantMove)
		}
	}
}

func TestApplyIssueMirrorReportsChangesAndClearsError(t *testing.T) {
	task := &Task{
		Status: "in_progress", GithubIssueNumber: 7,
		GithubIssueState: "open", GithubIssueTitle: "Old",
		GithubIssueSyncError: "previous failure",
	}
	meta := issueMeta{
		Number: 7, Title: "New", State: "closed", HTMLURL: "https://github.com/a/b/issues/7",
		Labels: []string{"bug"}, Milestone: 3, MilestoneTitle: "M3", Assignees: []string{"alice", "bob"},
	}
	changed := applyIssueMirror(task, meta)
	for _, want := range []string{"state", "title", "assignee", "labels", "milestone"} {
		if !containsStr(changed, want) {
			t.Fatalf("expected %q in changed=%v", want, changed)
		}
	}
	if task.GithubIssueAssignee != "alice" {
		t.Fatalf("primary assignee should be first: %q", task.GithubIssueAssignee)
	}
	if task.GithubMilestoneNumber != 3 || task.GithubMilestoneTitle != "M3" {
		t.Fatalf("milestone not mirrored: %+v", task)
	}
	if task.GithubIssueSyncError != "" {
		t.Fatal("a successful mirror must clear the stale sync error")
	}
	if task.GithubIssueSyncedAt == nil {
		t.Fatal("syncedAt must be stamped")
	}
	// Mirroring must never touch the OPM-owned title.
	if task.Title != "" {
		t.Fatalf("mirror must not write the task title, got %q", task.Title)
	}
	// A second identical mirror reports no changes.
	if again := applyIssueMirror(task, meta); len(again) != 0 {
		t.Fatalf("idempotent mirror should report nothing, got %v", again)
	}
}

func TestClearIssueLinkWipesEveryField(t *testing.T) {
	now := nowUTC()
	task := &Task{
		GithubIssueNumber: 5, GithubIssueURL: "u", GithubIssueState: "open",
		GithubIssueTitle: "t", GithubIssueAssignee: "a", GithubIssueLabels: []string{"l"},
		GithubIssueSyncedAt: &now, GithubIssueSyncError: "e",
		Title: "keep me", GithubMilestoneNumber: 9,
	}
	clearIssueLink(task)
	if task.GithubIssueNumber != 0 || task.GithubIssueURL != "" || task.GithubIssueState != "" ||
		task.GithubIssueTitle != "" || task.GithubIssueAssignee != "" || task.GithubIssueLabels != nil ||
		task.GithubIssueSyncedAt != nil || task.GithubIssueSyncError != "" {
		t.Fatalf("link not fully cleared: %+v", task)
	}
	// Unlink must not disturb unrelated task state.
	if task.Title != "keep me" || task.GithubMilestoneNumber != 9 {
		t.Fatalf("unlink touched unrelated fields: %+v", task)
	}
}

func TestApplyIssuePatchClearsLinkOnZero(t *testing.T) {
	now := nowUTC()
	task := &Task{GithubIssueNumber: 4, GithubIssueURL: "u", GithubIssueSyncedAt: &now}
	applyIssuePatch(task, map[string]interface{}{"githubIssueNumber": float64(0)})
	if task.GithubIssueNumber != 0 || task.GithubIssueURL != "" || task.GithubIssueSyncedAt != nil {
		t.Fatalf("zero must clear the link: %+v", task)
	}
	task2 := &Task{}
	applyIssuePatch(task2, map[string]interface{}{
		"githubIssueNumber": float64(11), "githubIssueUrl": "https://x/11", "githubIssueState": "open",
	})
	if task2.GithubIssueNumber != 11 || task2.GithubIssueURL != "https://x/11" {
		t.Fatalf("patch not applied: %+v", task2)
	}
}

func TestDecodeIssueMetaRejectsGarbage(t *testing.T) {
	if _, err := decodeIssueMeta(map[string]interface{}{"ok": true}); err == nil {
		t.Fatal("missing issue object must error, not yield a zero issue")
	}
	if _, err := decodeIssueMeta(map[string]interface{}{"issue": map[string]interface{}{"title": "x"}}); err == nil {
		t.Fatal("issue without a number must error")
	}
	m, err := decodeIssueMeta(map[string]interface{}{"issue": map[string]interface{}{
		"number": float64(8), "title": "T", "state": "open",
		"labels": []interface{}{"a", "", "b"}, "assignees": []interface{}{"z"},
		"milestone": float64(2), "milestone_title": "M2",
		"html_url": "https://github.com/a/b/issues/8",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if m.Number != 8 || m.State != "open" || m.MilestoneTitle != "M2" {
		t.Fatalf("decode: %+v", m)
	}
	if len(m.Labels) != 2 {
		t.Fatalf("blank labels should be dropped: %v", m.Labels)
	}
	if m.primaryAssignee() != "z" {
		t.Fatalf("assignee: %q", m.primaryAssignee())
	}
}

func TestIssueHTTPStatusMapping(t *testing.T) {
	cases := []struct {
		fault    *peerIssueFault
		wantCode int
		wantStat string
	}{
		{&peerIssueFault{HTTPStatus: 403, Status: "missing_issues_permission"}, http.StatusForbidden, "missing_issues_permission"},
		{&peerIssueFault{HTTPStatus: 404, Status: "issue_not_found"}, http.StatusNotFound, "issue_not_found"},
		{&peerIssueFault{HTTPStatus: 502, Status: "upstream_error"}, http.StatusBadGateway, "upstream_error"},
	}
	for _, tc := range cases {
		code, status := issueHTTPStatus(tc.fault)
		if code != tc.wantCode || status != tc.wantStat {
			t.Fatalf("%+v → (%d,%q) want (%d,%q)", tc.fault, code, status, tc.wantCode, tc.wantStat)
		}
	}
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// --- end-to-end over the real mux against a stub ORA peer ---

type stubORA struct {
	server   *httptest.Server
	getCode  int
	getBody  string
	updCode  int
	updBody  string
	crtCode  int
	crtBody  string
	lastPath string
	lastBody map[string]interface{}
}

func newStubORA(t *testing.T) *stubORA {
	t.Helper()
	s := &stubORA{getCode: 200, updCode: 200, crtCode: 200}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.lastPath = r.URL.Path
		s.lastBody = map[string]interface{}{}
		_ = json.NewDecoder(r.Body).Decode(&s.lastBody)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/issues/get"):
			w.WriteHeader(s.getCode)
			_, _ = w.Write([]byte(s.getBody))
		case strings.HasSuffix(r.URL.Path, "/issues/update"):
			w.WriteHeader(s.updCode)
			_, _ = w.Write([]byte(s.updBody))
		case strings.HasSuffix(r.URL.Path, "/issues/create"):
			w.WriteHeader(s.crtCode)
			_, _ = w.Write([]byte(s.crtBody))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":"unexpected path"}`))
		}
	}))
	t.Cleanup(s.server.Close)
	return s
}

func issueSyncFixture(t *testing.T, ora *stubORA) (*http.ServeMux, *Store, Project, Task) {
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
	task, err := store.CreateTask(p.ID, "Local title", "Local body", false, true, "", "")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	reg := func(pattern string, h http.HandlerFunc) { mux.HandleFunc(pattern, h) }
	registerOPMMux(mux, store, reg, reg)
	return mux, store, p, task
}

func postIssue(t *testing.T, mux *http.ServeMux, p Project, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost,
		"/api/projects/"+p.ID+"/github/issues/"+path, strings.NewReader(string(raw)))
	req.Header.Set("X-Organization-ID", "default-org")
	req.Header.Set("X-Project-ID", p.ID)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func TestIssueLinkAttachThenPushThenPull(t *testing.T) {
	ora := newStubORA(t)
	ora.getBody = `{"ok":true,"issue":{"number":42,"title":"Local title","state":"open",
		"html_url":"https://github.com/acme/demo/issues/42","labels":["bug"],"assignees":["alice"],
		"milestone":0}}`
	mux, store, p, task := issueSyncFixture(t, ora)

	// Attach
	rr := postIssue(t, mux, p, "link", map[string]interface{}{"specId": task.SpecID, "issueNumber": 42})
	if rr.Code != 200 {
		t.Fatalf("link: %d %s", rr.Code, rr.Body.String())
	}
	var linked map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &linked)
	if linked["mode"] != "attached" {
		t.Fatalf("mode: %v", linked["mode"])
	}
	stored, _ := store.GetTask(p.ID, task.SpecID)
	if stored.GithubIssueNumber != 42 || stored.GithubIssueAssignee != "alice" {
		t.Fatalf("link not persisted: %+v", stored)
	}

	// Push from the default backlog column → open
	ora.updBody = `{"ok":true,"applied":["title","body","state"],"issue":{"number":42,
		"title":"Local title","state":"open","html_url":"https://github.com/acme/demo/issues/42"}}`
	rr = postIssue(t, mux, p, "push", map[string]interface{}{"specId": task.SpecID})
	if rr.Code != 200 {
		t.Fatalf("push: %d %s", rr.Code, rr.Body.String())
	}
	if got := strFromAny(ora.lastBody["state"]); got != "open" {
		t.Fatalf("backlog must push state=open, sent %q", got)
	}
	if got := strFromAny(ora.lastBody["title"]); got != "Local title" {
		t.Fatalf("push must send the task title, sent %q", got)
	}

	// Move the task to done, push again → closed
	if _, err := store.MoveTask(p.ID, task.SpecID, "done"); err != nil {
		t.Fatal(err)
	}
	rr = postIssue(t, mux, p, "push", map[string]interface{}{"specId": task.SpecID})
	if rr.Code != 200 {
		t.Fatalf("push done: %d %s", rr.Code, rr.Body.String())
	}
	if got := strFromAny(ora.lastBody["state"]); got != "closed" {
		t.Fatalf("done must push state=closed, sent %q", got)
	}

	// Pull an issue that was reopened on GitHub → task returns to in_progress
	ora.getBody = `{"ok":true,"issue":{"number":42,"title":"Renamed on GitHub","state":"open",
		"html_url":"https://github.com/acme/demo/issues/42","assignees":["bob"],"milestone":0}}`
	rr = postIssue(t, mux, p, "pull", map[string]interface{}{"specId": task.SpecID})
	if rr.Code != 200 {
		t.Fatalf("pull: %d %s", rr.Code, rr.Body.String())
	}
	var pulled map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &pulled)
	if pulled["movedTo"] != "in_progress" {
		t.Fatalf("reopened issue must move task to in_progress, got %v", pulled["movedTo"])
	}
	if diverged, _ := pulled["titleDiverged"].(bool); !diverged {
		t.Fatal("a renamed issue must be reported as diverged")
	}
	stored, _ = store.GetTask(p.ID, task.SpecID)
	if stored.Title != "Local title" {
		t.Fatalf("pull must not overwrite the task title without adoptTitle, got %q", stored.Title)
	}
	if stored.GithubIssueTitle != "Renamed on GitHub" || stored.GithubIssueAssignee != "bob" {
		t.Fatalf("mirror fields not updated: %+v", stored)
	}

	// Opt in to adopting the issue title
	rr = postIssue(t, mux, p, "pull", map[string]interface{}{"specId": task.SpecID, "adoptTitle": true})
	if rr.Code != 200 {
		t.Fatalf("pull adopt: %d %s", rr.Code, rr.Body.String())
	}
	stored, _ = store.GetTask(p.ID, task.SpecID)
	if stored.Title != "Renamed on GitHub" {
		t.Fatalf("adoptTitle must take the issue title, got %q", stored.Title)
	}
}

func TestIssueCreateFromTask(t *testing.T) {
	ora := newStubORA(t)
	ora.crtBody = `{"ok":true,"issue":{"number":101,"title":"Local title","state":"open",
		"html_url":"https://github.com/acme/demo/issues/101"}}`
	mux, store, p, task := issueSyncFixture(t, ora)

	rr := postIssue(t, mux, p, "link", map[string]interface{}{"specId": task.SpecID, "create": true})
	if rr.Code != 200 {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var out map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out["mode"] != "created" {
		t.Fatalf("mode: %v", out["mode"])
	}
	if got := strFromAny(ora.lastBody["title"]); got != "Local title" {
		t.Fatalf("create must default to the task title, sent %q", got)
	}
	if got := strFromAny(ora.lastBody["body"]); got != "Local body" {
		t.Fatalf("create must default to the task description, sent %q", got)
	}
	stored, _ := store.GetTask(p.ID, task.SpecID)
	if stored.GithubIssueNumber != 101 {
		t.Fatalf("created link not persisted: %+v", stored)
	}
}

// A missing GitHub permission must surface as 403 with the concrete reason,
// be persisted on the task, and land in the spec log — never a silent success.
func TestIssuePushMissingPermissionIsHonest(t *testing.T) {
	ora := newStubORA(t)
	ora.getBody = `{"ok":true,"issue":{"number":42,"title":"Local title","state":"open",
		"html_url":"https://github.com/acme/demo/issues/42"}}`
	mux, store, p, task := issueSyncFixture(t, ora)
	if rr := postIssue(t, mux, p, "link", map[string]interface{}{"specId": task.SpecID, "issueNumber": 42}); rr.Code != 200 {
		t.Fatalf("link: %d %s", rr.Code, rr.Body.String())
	}

	ora.updCode = 403
	ora.updBody = `{"ok":false,"status":"missing_issues_permission",
		"error":"GitHub App installation is missing Issues write on this repository",
		"missing":["issues"],"note":"Grant Issues: Read and write"}`

	rr := postIssue(t, mux, p, "push", map[string]interface{}{"specId": task.SpecID})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d %s", rr.Code, rr.Body.String())
	}
	var out map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out["status"] != "missing_issues_permission" {
		t.Fatalf("status: %v", out["status"])
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "missing Issues write") {
		t.Fatalf("error must carry the concrete reason, got %q", msg)
	}
	if missing, ok := out["missing"].([]interface{}); !ok || len(missing) != 1 {
		t.Fatalf("missing permissions must be reported, got %v", out["missing"])
	}

	stored, _ := store.GetTask(p.ID, task.SpecID)
	if !strings.Contains(stored.GithubIssueSyncError, "missing Issues write") {
		t.Fatalf("failure must be persisted on the task, got %q", stored.GithubIssueSyncError)
	}
	logs, err := store.GetSpecLogs(p.ID, task.SpecID)
	if err != nil {
		t.Fatal(err)
	}
	entry := logs[issueSyncLogPhase]
	if !strings.Contains(entry, "FAILED") || !strings.Contains(entry, "missing_issues_permission") {
		t.Fatalf("failure must be logged, got %q", entry)
	}
}

// A deleted issue must surface as 404 issue_not_found, not a fabricated success.
func TestIssuePullMissingIssueIsHonest(t *testing.T) {
	ora := newStubORA(t)
	ora.getBody = `{"ok":true,"issue":{"number":42,"title":"t","state":"open","html_url":"u"}}`
	mux, store, p, task := issueSyncFixture(t, ora)
	if rr := postIssue(t, mux, p, "link", map[string]interface{}{"specId": task.SpecID, "issueNumber": 42}); rr.Code != 200 {
		t.Fatalf("link: %d %s", rr.Code, rr.Body.String())
	}

	ora.getCode = 404
	ora.getBody = `{"ok":false,"status":"issue_not_found",
		"error":"the linked GitHub issue is not reachable (deleted, transferred, or not visible to this connector)",
		"detail":"get issue #42: github returned 404: {\"message\":\"Not Found\"}"}`

	rr := postIssue(t, mux, p, "pull", map[string]interface{}{"specId": task.SpecID})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d %s", rr.Code, rr.Body.String())
	}
	var out map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out["status"] != "issue_not_found" {
		t.Fatalf("status: %v", out["status"])
	}
	if d, _ := out["detail"].(string); !strings.Contains(d, "404") {
		t.Fatalf("detail must carry the upstream answer, got %q", d)
	}
	stored, _ := store.GetTask(p.ID, task.SpecID)
	if stored.GithubIssueNumber != 42 {
		t.Fatal("a failed pull must not silently drop the link")
	}
	if !strings.Contains(stored.GithubIssueSyncError, "not reachable") {
		t.Fatalf("failure must be persisted, got %q", stored.GithubIssueSyncError)
	}
}

func TestIssuePushRequiresLink(t *testing.T) {
	ora := newStubORA(t)
	mux, _, p, task := issueSyncFixture(t, ora)
	rr := postIssue(t, mux, p, "push", map[string]interface{}{"specId": task.SpecID})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d %s", rr.Code, rr.Body.String())
	}
	var out map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out["status"] != "no_issue_linked" {
		t.Fatalf("status: %v", out["status"])
	}
}

func TestIssueUnlinkLeavesIssueAlone(t *testing.T) {
	ora := newStubORA(t)
	ora.getBody = `{"ok":true,"issue":{"number":42,"title":"t","state":"open","html_url":"u"}}`
	mux, store, p, task := issueSyncFixture(t, ora)
	if rr := postIssue(t, mux, p, "link", map[string]interface{}{"specId": task.SpecID, "issueNumber": 42}); rr.Code != 200 {
		t.Fatalf("link: %d %s", rr.Code, rr.Body.String())
	}
	ora.lastPath = ""

	rr := postIssue(t, mux, p, "unlink", map[string]interface{}{"specId": task.SpecID})
	if rr.Code != 200 {
		t.Fatalf("unlink: %d %s", rr.Code, rr.Body.String())
	}
	if ora.lastPath != "" {
		t.Fatalf("unlink must not call GitHub, called %q", ora.lastPath)
	}
	stored, _ := store.GetTask(p.ID, task.SpecID)
	if stored.GithubIssueNumber != 0 {
		t.Fatalf("link not cleared: %+v", stored)
	}
}
