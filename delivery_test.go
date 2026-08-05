package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	openauth "github.com/TheGrimmChester/open-auth-go"
	opentenant "github.com/TheGrimmChester/open-tenant-go"
)

const testPeerSecret = "delivery-test-service-secret"

// oraStub is a stand-in for ORA: it validates the service JWT scope on every
// call, hands out credentials for a local bare repository instead of GitHub, and
// records what OPM asked for so the test can assert the real contract.
type oraStub struct {
	server *httptest.Server
	// originURL is the file:// URL of the bare repository acting as the remote.
	originURL string

	// scopes seen per path, so a test can prove scm:pr is what OPM sends.
	scopes map[string]string
	// prRequest is the decoded body of the last pull-request create call.
	prRequest map[string]interface{}
	// pushCalls counts push-credential requests.
	pushCalls int
	// pushToken is handed back as the push credential.
	pushToken string

	// prStatus / prBody override the pull-request response for failure paths.
	prStatus int
	prBody   map[string]interface{}
	// pushStatus / pushBody override the push-credential response.
	pushStatus int
	pushBody   map[string]interface{}
}

func (s *oraStub) auth(w http.ResponseWriter, r *http.Request, wantScope string) bool {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	claims, err := openauth.ValidateServiceJWT(
		strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")), []byte(testPeerSecret), "ora-api")
	if err != nil {
		http.Error(w, "invalid service token", http.StatusUnauthorized)
		return false
	}
	s.scopes[r.URL.Path] = claims.Scope
	if err := openauth.RequireScope(claims, wantScope); err != nil {
		http.Error(w, "missing scope", http.StatusForbidden)
		return false
	}
	return true
}

func newORAStub(t *testing.T, originURL string) *oraStub {
	t.Helper()
	s := &oraStub{
		originURL: originURL,
		scopes:    map[string]string{},
		pushToken: "ghs_stub_push_token_0123456789",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/peer/scm/clone-credentials", func(w http.ResponseWriter, r *http.Request) {
		if !s.auth(w, r, "scm:clone") {
			return
		}
		writeJSON(w, map[string]interface{}{
			"ok": true, "token": "ghs_stub_clone_token_0123456789", "clone_url": s.originURL,
		})
	})
	mux.HandleFunc("/api/peer/scm/push-credentials", func(w http.ResponseWriter, r *http.Request) {
		if !s.auth(w, r, peerPRScope) {
			return
		}
		s.pushCalls++
		if s.pushStatus != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(s.pushStatus)
			_ = json.NewEncoder(w).Encode(s.pushBody)
			return
		}
		writeJSON(w, map[string]interface{}{
			"ok": true, "token": s.pushToken, "clone_url": s.originURL,
			"permissions": map[string]string{"contents": "write", "metadata": "read"},
		})
	})
	mux.HandleFunc("/api/peer/scm/pull-requests/create", func(w http.ResponseWriter, r *http.Request) {
		if !s.auth(w, r, peerPRScope) {
			return
		}
		s.prRequest = map[string]interface{}{}
		_ = json.NewDecoder(r.Body).Decode(&s.prRequest)
		if s.prStatus != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(s.prStatus)
			_ = json.NewEncoder(w).Encode(s.prBody)
			return
		}
		head, _ := s.prRequest["head"].(string)
		base, _ := s.prRequest["base"].(string)
		writeJSON(w, map[string]interface{}{
			"ok": true, "already_existed": false,
			"pull_request": map[string]interface{}{
				"number": 4242, "html_url": "https://github.com/acme/demo/pull/4242",
				"state": "open", "head_ref": head, "base_ref": base,
			},
		})
	})
	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)
	return s
}

// gitFixture is a bare repository standing in for the linked GitHub repo.
type gitFixture struct {
	Root      string
	OriginDir string
	OriginURL string
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@localhost",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@localhost")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// newGitFixture builds a bare repo with main containing greeting.txt.
func newGitFixture(t *testing.T) gitFixture {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "init", "-q", "-b", "main", src)
	if err := os.WriteFile(filepath.Join(src, "greeting.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "add", "greeting.txt")
	runGit(t, src, "commit", "-qm", "initial")
	origin := filepath.Join(root, "origin.git")
	runGit(t, root, "clone", "-q", "--bare", src, origin)
	return gitFixture{Root: root, OriginDir: origin, OriginURL: "file://" + origin}
}

func (f gitFixture) refExists(t *testing.T, ref string) bool {
	t.Helper()
	cmd := exec.Command("git", "--git-dir="+f.OriginDir, "show-ref", "--verify", "--quiet", ref)
	return cmd.Run() == nil
}

func (f gitFixture) fileAt(t *testing.T, ref, path string) string {
	t.Helper()
	cmd := exec.Command("git", "--git-dir="+f.OriginDir, "show", ref+":"+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("show %s:%s: %v\n%s", ref, path, err, out)
	}
	return string(out)
}

// deliveryEnv wires a store, a project, a task and the peer env for a delivery.
type deliveryEnv struct {
	store   *Store
	project Project
	task    Task
	stub    *oraStub
	fixture gitFixture
	mux     *http.ServeMux
}

func newDeliveryEnv(t *testing.T) *deliveryEnv {
	t.Helper()
	opentenant.SetAuthEnforced(false)
	t.Cleanup(func() { opentenant.SetAuthEnforced(false) })

	fixture := newGitFixture(t)
	stub := newORAStub(t, fixture.OriginURL)

	t.Setenv("OPEN_SERVICE_JWT_SECRET", testPeerSecret)
	t.Setenv("PEER_ORA_URL", stub.server.URL)
	t.Setenv("OPM_JOB_TMP", filepath.Join(t.TempDir(), "jobs"))
	t.Setenv("OPM_FORCE_BUILTIN", "1")

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{
		Name: "Demo", OwnerRepo: "acme/demo", ConnectorID: "conn-1",
		OrganizationID: "default-org", DefaultBranch: "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InitProject(p.ID); err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(p.ID, "Add farewell", "Ship a farewell file", false, true, "", "")
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	registerOPMMux(mux, store, func(path string, h http.HandlerFunc) {
		mux.HandleFunc(path, h)
	}, func(string, http.HandlerFunc) {})

	return &deliveryEnv{store: store, project: p, task: task, stub: stub, fixture: fixture, mux: mux}
}

func (e *deliveryEnv) post(t *testing.T, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body == "" {
		body = "{}"
	}
	rdr = strings.NewReader(body)
	req := httptest.NewRequest(http.MethodPost, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Organization-ID", "default-org")
	req.Header.Set("X-Project-ID", e.project.ID)
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func (e *deliveryEnv) deliver(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	return e.post(t, fmt.Sprintf("/api/projects/%s/tasks/%s/deliver", e.project.ID, e.task.SpecID), body)
}

func strPtr(s string) *string { return &s }

// unlinkProjectConnector clears the connector on a stored project, simulating a
// repository link that was removed after tasks already existed.
func unlinkProjectConnector(t *testing.T, store *Store, projectID string) {
	t.Helper()
	var reg projectsFile
	if err := store.readJSON(store.registryPath(), &reg); err != nil {
		t.Fatal(err)
	}
	found := false
	for i := range reg.Projects {
		if reg.Projects[i].ID == projectID {
			reg.Projects[i].ConnectorID = ""
			found = true
		}
	}
	if !found {
		t.Fatalf("project %s not in registry", projectID)
	}
	if err := store.writeJSON(store.registryPath(), reg); err != nil {
		t.Fatal(err)
	}
}

// recordFarewellChanges stores a change set that both creates a file and patches
// the file already in the repository, so apply covers write and patch modes.
func (e *deliveryEnv) recordFarewellChanges(t *testing.T) {
	t.Helper()
	patch := "--- a/greeting.txt\n+++ b/greeting.txt\n@@ -1 +1,2 @@\n hello\n+and goodbye\n"
	cs := ChangeSet{
		RunID: "run-1", Source: "model", Model: "stub-model",
		CommitMessage: "Add farewell text\n\nCloses the farewell task.",
		Files: []FileChange{
			{Path: "farewell.txt", Contents: strPtr("goodbye\n")},
			{Path: "greeting.txt", Patch: patch},
		},
	}
	if err := e.store.PutChangeSet(e.project.ID, e.task.SpecID, cs); err != nil {
		t.Fatal(err)
	}
}

// The whole chain, exercised for real: clone the linked repo, apply the recorded
// change set, commit on a task branch, push to the remote, open the pull request.
func TestDeliverHappyPathEndToEnd(t *testing.T) {
	e := newDeliveryEnv(t)
	e.recordFarewellChanges(t)

	rec := e.deliver(t, "{}")
	if rec.Code != http.StatusOK {
		t.Fatalf("deliver code=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		OK        bool     `json:"ok"`
		Status    string   `json:"status"`
		Branch    string   `json:"branch"`
		CommitSha string   `json:"commitSha"`
		PRNumber  int      `json:"prNumber"`
		PRURL     string   `json:"prUrl"`
		PRState   string   `json:"prState"`
		Files     []string `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body not json: %v", err)
	}
	if !out.OK || out.Status != deliveryStatusDelivered {
		t.Fatalf("want delivered, got %+v", out)
	}
	wantBranch := "opm/" + sanitizeBranchSegment(e.task.SpecID)
	if out.Branch != wantBranch {
		t.Fatalf("branch want %q got %q", wantBranch, out.Branch)
	}
	if len(out.CommitSha) < 20 {
		t.Fatalf("commitSha looks wrong: %q", out.CommitSha)
	}
	if out.PRNumber != 4242 || !strings.Contains(out.PRURL, "/pull/4242") || out.PRState != "open" {
		t.Fatalf("pull request not reported: %+v", out)
	}
	if len(out.Files) != 2 {
		t.Fatalf("want 2 changed files, got %v", out.Files)
	}

	// The remote genuinely carries the branch, with both changes applied.
	if !e.fixture.refExists(t, "refs/heads/"+wantBranch) {
		t.Fatalf("branch %s was not pushed to the remote", wantBranch)
	}
	if got := e.fixture.fileAt(t, "refs/heads/"+wantBranch, "farewell.txt"); got != "goodbye\n" {
		t.Fatalf("new file content on remote = %q", got)
	}
	if got := e.fixture.fileAt(t, "refs/heads/"+wantBranch, "greeting.txt"); got != "hello\nand goodbye\n" {
		t.Fatalf("patched file content on remote = %q", got)
	}
	// The base branch must be untouched.
	if got := e.fixture.fileAt(t, "refs/heads/main", "greeting.txt"); got != "hello\n" {
		t.Fatalf("base branch was modified: %q", got)
	}

	// The delivery is persisted on the task, with no stale error.
	task, err := e.store.GetTask(e.project.ID, e.task.SpecID)
	if err != nil {
		t.Fatal(err)
	}
	if task.DeliveryBranch != wantBranch || task.DeliveryCommitSha != out.CommitSha {
		t.Fatalf("task delivery fields: %+v", task)
	}
	if task.PRNumber != 4242 || task.PRURL == "" || task.PRState != "open" {
		t.Fatalf("task PR fields: %+v", task)
	}
	if task.DeliveryStatus != deliveryStatusDelivered || task.DeliveryError != "" {
		t.Fatalf("task status/error: %q / %q", task.DeliveryStatus, task.DeliveryError)
	}
	if len(task.DeliveryFiles) != 2 {
		t.Fatalf("task changed-file summary: %v", task.DeliveryFiles)
	}
	if task.DeliveredAt == nil {
		t.Fatal("deliveredAt not set")
	}

	// The change set is consumed, so a second deliver cannot re-commit it.
	cs, err := e.store.GetChangeSet(e.project.ID, e.task.SpecID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.Files) != 0 {
		t.Fatalf("change set should be cleared after delivery, got %d files", len(cs.Files))
	}
	again := e.deliver(t, "{}")
	if again.Code != http.StatusConflict {
		t.Fatalf("second deliver want 409, got %d body=%s", again.Code, again.Body.String())
	}

	// The delivery is in the spec log.
	logs, err := e.store.GetSpecLogs(e.project.ID, e.task.SpecID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs[deliveryLogPhase], "deliver OK") ||
		!strings.Contains(logs[deliveryLogPhase], wantBranch) {
		t.Fatalf("delivery log missing the success entry: %q", logs[deliveryLogPhase])
	}

	// Both write calls used the narrow scm:pr scope, and the clone used scm:clone.
	if got := e.stub.scopes["/api/peer/scm/push-credentials"]; got != peerPRScope {
		t.Fatalf("push credentials scope = %q, want %q", got, peerPRScope)
	}
	if got := e.stub.scopes["/api/peer/scm/pull-requests/create"]; got != peerPRScope {
		t.Fatalf("pull-request scope = %q, want %q", got, peerPRScope)
	}
	if got := e.stub.scopes["/api/peer/scm/clone-credentials"]; got != "scm:clone" {
		t.Fatalf("clone scope = %q, want scm:clone", got)
	}

	// The PR was opened from the task branch onto the project default branch, and
	// its body names the changed files rather than claiming unreviewed authority.
	if e.stub.prRequest["head"] != wantBranch || e.stub.prRequest["base"] != "main" {
		t.Fatalf("PR head/base: %#v", e.stub.prRequest)
	}
	prBody, _ := e.stub.prRequest["body"].(string)
	for _, want := range []string{"farewell.txt", "greeting.txt", e.task.SpecID} {
		if !strings.Contains(prBody, want) {
			t.Fatalf("PR body missing %q: %s", want, prBody)
		}
	}
}

// The push credential must never reach disk or the response. It is minted per
// request by ORA and discarded with the workspace.
func TestDeliverNeverPersistsPushCredential(t *testing.T) {
	e := newDeliveryEnv(t)
	e.recordFarewellChanges(t)
	rec := e.deliver(t, "{}")
	if rec.Code != http.StatusOK {
		t.Fatalf("deliver code=%d body=%s", rec.Code, rec.Body.String())
	}
	token := e.stub.pushToken
	if strings.Contains(rec.Body.String(), token) {
		t.Fatal("push token leaked into the delivery response")
	}
	var found []string
	_ = filepath.WalkDir(e.store.dataDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr == nil && strings.Contains(string(b), token) {
			found = append(found, path)
		}
		return nil
	})
	if len(found) > 0 {
		t.Fatalf("push token written under OPM_DATA_DIR: %v", found)
	}
	// The workspace, including its askpass helper, is gone.
	entries, _ := os.ReadDir(jobTmpRoot())
	for _, en := range entries {
		if strings.HasPrefix(en.Name(), "deliver-") {
			t.Fatalf("delivery workspace %s was not cleaned up", en.Name())
		}
	}
}

// A run that produced no file changes must not report a delivery. This is the
// case the builtin path always lands in, so the honest answer matters most here.
func TestDeliverNoChangesProduced(t *testing.T) {
	e := newDeliveryEnv(t)

	rec := e.deliver(t, "{}")
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body not json: %v", err)
	}
	if out["status"] != deliveryStatusNoChanges {
		t.Fatalf("status want %q got %v", deliveryStatusNoChanges, out["status"])
	}
	if ok, _ := out["ok"].(bool); ok {
		t.Fatal("ok must be false when nothing was delivered")
	}
	if out["branch"] != nil && out["branch"] != "" {
		t.Fatalf("no branch may be reported: %v", out["branch"])
	}
	if out["prNumber"] != nil {
		t.Fatalf("no PR may be reported: %v", out["prNumber"])
	}

	// The failure is persisted and logged, not swallowed.
	task, err := e.store.GetTask(e.project.ID, e.task.SpecID)
	if err != nil {
		t.Fatal(err)
	}
	if task.DeliveryStatus != deliveryStatusNoChanges || task.DeliveryError == "" {
		t.Fatalf("task must record the failure: %+v", task)
	}
	if task.DeliveryBranch != "" || task.PRNumber != 0 {
		t.Fatalf("task must not claim a delivery: %+v", task)
	}
	logs, _ := e.store.GetSpecLogs(e.project.ID, e.task.SpecID)
	if !strings.Contains(logs[deliveryLogPhase], deliveryStatusNoChanges) {
		t.Fatalf("delivery log missing the failure: %q", logs[deliveryLogPhase])
	}
	// Nothing reached the remote, and no credential was even requested.
	if e.stub.pushCalls != 0 {
		t.Fatalf("push credentials must not be requested with nothing to push (%d calls)", e.stub.pushCalls)
	}

	// A builtin implementation run records no change set, so deliver stays honest.
	t.Setenv("OPM_IMPL_AUTO_CHAIN", "0")
	j, err := e.store.CreateJob(e.project.ID, "run-planning", e.task.SpecID, "opm-runner-task:nas")
	if err != nil {
		t.Fatal(err)
	}
	executeJob(e.store, j)
	ij, err := e.store.CreateJob(e.project.ID, "run-implementation", e.task.SpecID, "opm-runner-task:nas")
	if err != nil {
		t.Fatal(err)
	}
	executeJob(e.store, ij)
	ij, _ = e.store.GetJob(e.project.ID, ij.RunID)
	if !strings.Contains(ij.Message, "No source changes were written") {
		t.Fatalf("builtin implementation must say it wrote no source: %q", ij.Message)
	}
	cs, _ := e.store.GetChangeSet(e.project.ID, e.task.SpecID)
	if len(cs.Files) != 0 {
		t.Fatalf("builtin run must not record file changes, got %d", len(cs.Files))
	}
}

// ORA refusing the pull request for a missing permission must surface as that
// exact status, with the branch that was already pushed still reported.
func TestDeliverMissingPullRequestPermission(t *testing.T) {
	e := newDeliveryEnv(t)
	e.recordFarewellChanges(t)
	e.stub.prStatus = http.StatusForbidden
	e.stub.prBody = map[string]interface{}{
		"ok": false, "status": "missing_pull_requests_permission",
		"error":   "GitHub App installation is missing Pull requests write on this repository",
		"detail":  "create pull request: github returned 403",
		"missing": []string{"pull_requests"},
	}

	rec := e.deliver(t, "{}")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["status"] != deliveryStatusMissingPRPermission {
		t.Fatalf("status want %q got %v", deliveryStatusMissingPRPermission, out["status"])
	}
	if out["detail"] == nil || out["missing"] == nil {
		t.Fatalf("ORA detail and missing set must be relayed: %#v", out)
	}
	wantBranch := "opm/" + sanitizeBranchSegment(e.task.SpecID)
	if out["branch"] != wantBranch {
		t.Fatalf("the pushed branch must still be reported: %#v", out["branch"])
	}
	// The push really happened, so the operator can retry the PR without re-running.
	if !e.fixture.refExists(t, "refs/heads/"+wantBranch) {
		t.Fatal("branch should already be on the remote")
	}
	task, _ := e.store.GetTask(e.project.ID, e.task.SpecID)
	if task.DeliveryStatus != deliveryStatusMissingPRPermission || task.DeliveryError == "" {
		t.Fatalf("task must record the permission failure: %+v", task)
	}
	if task.PRNumber != 0 || task.PRURL != "" {
		t.Fatalf("no PR may be persisted: %+v", task)
	}
	logs, _ := e.store.GetSpecLogs(e.project.ID, e.task.SpecID)
	if !strings.Contains(logs[deliveryLogPhase], deliveryStatusMissingPRPermission) {
		t.Fatalf("delivery log missing the failure: %q", logs[deliveryLogPhase])
	}
}

// A remote branch that already carries different commits must be reported as a
// rejected push, not silently force-pushed over.
func TestDeliverPushRejected(t *testing.T) {
	e := newDeliveryEnv(t)
	e.recordFarewellChanges(t)

	// Put a divergent commit on the delivery branch in the remote first.
	branch := "opm/" + sanitizeBranchSegment(e.task.SpecID)
	side := filepath.Join(e.fixture.Root, "side")
	runGit(t, e.fixture.Root, "clone", "-q", e.fixture.OriginDir, side)
	runGit(t, side, "checkout", "-qb", branch)
	if err := os.WriteFile(filepath.Join(side, "other.txt"), []byte("divergent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, side, "add", "other.txt")
	runGit(t, side, "commit", "-qm", "divergent work")
	runGit(t, side, "push", "-q", "origin", branch)

	rec := e.deliver(t, "{}")
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["status"] != deliveryStatusPushRejected {
		t.Fatalf("status want %q got %v (body=%s)", deliveryStatusPushRejected, out["status"], rec.Body.String())
	}
	if out["prNumber"] != nil {
		t.Fatalf("no PR may be reported after a rejected push: %#v", out)
	}
	// The divergent remote commit is intact.
	if got := e.fixture.fileAt(t, "refs/heads/"+branch, "other.txt"); got != "divergent\n" {
		t.Fatalf("remote branch was overwritten: %q", got)
	}
	task, _ := e.store.GetTask(e.project.ID, e.task.SpecID)
	if task.DeliveryStatus != deliveryStatusPushRejected || task.DeliveryError == "" {
		t.Fatalf("task must record the rejected push: %+v", task)
	}

	// Retrying onto a fresh branch succeeds, which is the documented recovery.
	retry := e.deliver(t, `{"branch":"opm-retry-branch"}`)
	if retry.Code != http.StatusOK {
		t.Fatalf("retry onto a fresh branch want 200, got %d body=%s", retry.Code, retry.Body.String())
	}
	if !e.fixture.refExists(t, "refs/heads/opm-retry-branch") {
		t.Fatal("retry branch not pushed")
	}
}

// Paths that would escape the workspace, rewrite git metadata or touch workflows
// must be refused before anything is committed.
func TestDeliverRefusesUnsafePaths(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"traversal", "../escape.txt"},
		{"nested_traversal", "src/../../escape.txt"},
		{"absolute", "/etc/passwd"},
		{"git_metadata", ".git/config"},
		{"workflow", ".github/workflows/ci.yml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newDeliveryEnv(t)
			if err := e.store.PutChangeSet(e.project.ID, e.task.SpecID, ChangeSet{
				Source: "model", CommitMessage: "nope",
				Files: []FileChange{{Path: tc.path, Contents: strPtr("x\n")}},
			}); err != nil {
				t.Fatal(err)
			}
			rec := e.deliver(t, "{}")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body.String())
			}
			var out map[string]interface{}
			_ = json.Unmarshal(rec.Body.Bytes(), &out)
			if out["status"] != deliveryStatusUnsafePath {
				t.Fatalf("status want %q got %v", deliveryStatusUnsafePath, out["status"])
			}
			if e.stub.pushCalls != 0 {
				t.Fatal("an unsafe change set must never reach the push stage")
			}
			// Nothing was pushed to the remote.
			if e.fixture.refExists(t, "refs/heads/opm/"+sanitizeBranchSegment(e.task.SpecID)) {
				t.Fatal("a branch was pushed despite the unsafe path")
			}
		})
	}
}

// A change set whose content already matches HEAD must not become an empty commit.
func TestDeliverIdenticalContentIsNoChange(t *testing.T) {
	e := newDeliveryEnv(t)
	if err := e.store.PutChangeSet(e.project.ID, e.task.SpecID, ChangeSet{
		Source: "model", CommitMessage: "no-op",
		Files: []FileChange{{Path: "greeting.txt", Contents: strPtr("hello\n")}},
	}); err != nil {
		t.Fatal(err)
	}
	rec := e.deliver(t, "{}")
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["status"] != deliveryStatusNoChanges {
		t.Fatalf("status want %q got %v", deliveryStatusNoChanges, out["status"])
	}
	if e.stub.pushCalls != 0 {
		t.Fatal("nothing to commit must not request a push credential")
	}
}

// A patch whose context does not match the real source must fail loudly rather
// than produce a mangled file.
func TestDeliverPatchThatDoesNotApplyFails(t *testing.T) {
	e := newDeliveryEnv(t)
	if err := e.store.PutChangeSet(e.project.ID, e.task.SpecID, ChangeSet{
		Source: "model", CommitMessage: "bad patch",
		Files: []FileChange{{
			Path:  "greeting.txt",
			Patch: "--- a/greeting.txt\n+++ b/greeting.txt\n@@ -1 +1,2 @@\n this line is not in the file\n+added\n",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	rec := e.deliver(t, "{}")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["status"] != deliveryStatusApplyFailed {
		t.Fatalf("status want %q got %v", deliveryStatusApplyFailed, out["status"])
	}
	task, _ := e.store.GetTask(e.project.ID, e.task.SpecID)
	if task.DeliveryStatus != deliveryStatusApplyFailed || task.DeliveryError == "" {
		t.Fatalf("task must record the apply failure: %+v", task)
	}
}

// The project without a linked repository, and the peer-less deployment, both get
// a specific status instead of a generic failure.
func TestDeliverPreconditionStatuses(t *testing.T) {
	t.Run("not_linked_repo", func(t *testing.T) {
		e := newDeliveryEnv(t)
		e.recordFarewellChanges(t)
		// A project whose connector link was removed must not attempt a delivery.
		unlinkProjectConnector(t, e.store, e.project.ID)
		rec := e.deliver(t, "{}")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body.String())
		}
		var out map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if out["status"] != deliveryStatusNotLinkedRepo {
			t.Fatalf("status want %q got %v", deliveryStatusNotLinkedRepo, out["status"])
		}
		if e.stub.pushCalls != 0 {
			t.Fatal("an unlinked project must not request a push credential")
		}
		task, _ := e.store.GetTask(e.project.ID, e.task.SpecID)
		if task.DeliveryStatus != deliveryStatusNotLinkedRepo || task.DeliveryError == "" {
			t.Fatalf("task must record the failure: %+v", task)
		}
	})

	t.Run("peer_not_configured", func(t *testing.T) {
		e := newDeliveryEnv(t)
		e.recordFarewellChanges(t)
		t.Setenv("PEER_ORA_URL", "")
		rec := e.deliver(t, "{}")
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("want 503, got %d body=%s", rec.Code, rec.Body.String())
		}
		var out map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if out["status"] != deliveryStatusPeerNotConfigured {
			t.Fatalf("status want %q got %v", deliveryStatusPeerNotConfigured, out["status"])
		}
	})

	t.Run("task_not_found", func(t *testing.T) {
		e := newDeliveryEnv(t)
		rec := e.post(t, fmt.Sprintf("/api/projects/%s/tasks/999-nope/deliver", e.project.ID), "{}")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("want 404, got %d body=%s", rec.Code, rec.Body.String())
		}
		var out map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if out["status"] != deliveryStatusTaskNotFound {
			t.Fatalf("status want %q got %v", deliveryStatusTaskNotFound, out["status"])
		}
	})
}

// The nested delivery route must inherit the org gate and the selected-project
// gate exactly like every other nested route.
func TestDeliverInheritsProjectAndOrgGates(t *testing.T) {
	e := newDeliveryEnv(t)
	e.recordFarewellChanges(t)

	// Mismatched X-Project-ID is refused before any git or peer work happens.
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/projects/%s/tasks/%s/deliver", e.project.ID, e.task.SpecID),
		strings.NewReader("{}"))
	req.Header.Set("X-Organization-ID", "default-org")
	req.Header.Set("X-Project-ID", "some-other-project")
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("mismatched X-Project-ID want 403, got %d body=%s", rec.Code, rec.Body.String())
	}
	if e.stub.pushCalls != 0 {
		t.Fatal("a gated request must not reach the push stage")
	}

	// A foreign organization cannot see the project at all.
	opentenant.SetAuthEnforced(true)
	t.Cleanup(func() { opentenant.SetAuthEnforced(false) })
	req2 := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/projects/%s/tasks/%s/deliver", e.project.ID, e.task.SpecID),
		strings.NewReader("{}"))
	req2.Header.Set("X-Organization-ID", "other-org")
	req2.Header.Set("X-Project-ID", e.project.ID)
	rec2 := httptest.NewRecorder()
	e.mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("foreign org want 404, got %d body=%s", rec2.Code, rec2.Body.String())
	}
}

// GET …/changes reports the pending change set as paths and modes only.
func TestTaskChangesEndpoint(t *testing.T) {
	e := newDeliveryEnv(t)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/projects/%s/tasks/%s/changes", e.project.ID, e.task.SpecID), nil)
	req.Header.Set("X-Organization-ID", "default-org")
	req.Header.Set("X-Project-ID", e.project.ID)
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var empty map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &empty)
	if empty["deliverable"] != false {
		t.Fatalf("empty change set must not be deliverable: %#v", empty)
	}

	e.recordFarewellChanges(t)
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/projects/%s/tasks/%s/changes", e.project.ID, e.task.SpecID), nil)
	req2.Header.Set("X-Organization-ID", "default-org")
	req2.Header.Set("X-Project-ID", e.project.ID)
	e.mux.ServeHTTP(rec2, req2)
	var out struct {
		Deliverable   bool   `json:"deliverable"`
		FileCount     int    `json:"fileCount"`
		CommitMessage string `json:"commitMessage"`
		Files         []struct {
			Path string `json:"path"`
			Mode string `json:"mode"`
		} `json:"files"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &out); err != nil {
		t.Fatalf("body not json: %v", err)
	}
	if !out.Deliverable || out.FileCount != 2 {
		t.Fatalf("summary wrong: %+v", out)
	}
	modes := map[string]string{}
	for _, f := range out.Files {
		modes[f.Path] = f.Mode
	}
	if modes["farewell.txt"] != "write" || modes["greeting.txt"] != "patch" {
		t.Fatalf("modes wrong: %#v", modes)
	}
	// File bodies must never be echoed by the summary endpoint.
	if strings.Contains(rec2.Body.String(), "goodbye") {
		t.Fatal("the change summary must not include file contents")
	}
}
