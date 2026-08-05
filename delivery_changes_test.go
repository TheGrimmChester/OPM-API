package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathRejection(t *testing.T) {
	refused := map[string]string{
		"":                            "blank",
		"   ":                         "blank",
		"/etc/passwd":                 "absolute",
		"../escape":                   "escape",
		"a/../../escape":              "escape",
		".git/config":                 "metadata",
		".git":                        "metadata",
		".github/workflows/build.yml": "workflows",
		"windows\\path.go":            "slashes",
		".":                           "root",
	}
	for path := range refused {
		if pathRejection(path) == "" {
			t.Fatalf("path %q must be refused", path)
		}
	}
	for _, ok := range []string{
		"main.go", "src/app/index.js", "docs/api.md", ".github/CODEOWNERS",
		"a/b/c/deep.txt", "dotted.name.go",
	} {
		if reason := pathRejection(ok); reason != "" {
			t.Fatalf("path %q must be allowed, got %q", ok, reason)
		}
	}
}

func TestNormalizeChangeSetDropsUnusableEntries(t *testing.T) {
	body := "x\n"
	kept, dropped := normalizeChangeSet([]FileChange{
		{Path: "a.go", Contents: &body},
		{Path: "  ", Contents: &body},
		{Path: "b.go"},                  // no contents, patch or delete
		{Path: "a.go", Contents: &body}, // duplicate
		{Path: "c.go", Delete: true},
		{Path: "d.go", Patch: "--- a\n+++ b\n"},
	})
	if len(kept) != 3 {
		t.Fatalf("kept = %d (%v)", len(kept), kept)
	}
	if len(dropped) != 3 {
		t.Fatalf("dropped = %v", dropped)
	}
	if kept[0].mode() != "write" || kept[1].mode() != "delete" || kept[2].mode() != "patch" {
		t.Fatalf("modes wrong: %v", kept)
	}
}

func TestApplyChangeSetLimits(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("x", 100)

	many := ChangeSet{}
	for i := 0; i < 5; i++ {
		b := body
		many.Files = append(many.Files, FileChange{Path: "f" + string(rune('a'+i)) + ".txt", Contents: &b})
	}
	out, err := applyChangeSet(dir, many, changeSetLimits{MaxFiles: 3, MaxBytes: 1 << 20})
	if err == nil {
		t.Fatal("too many files must be refused")
	}
	if out.Status != deliveryStatusChangeSetTooLarge {
		t.Fatalf("status = %q", out.Status)
	}

	out, err = applyChangeSet(dir, many, changeSetLimits{MaxFiles: 50, MaxBytes: 10})
	if err == nil {
		t.Fatal("oversized change set must be refused")
	}
	if out.Status != deliveryStatusChangeSetTooLarge {
		t.Fatalf("status = %q", out.Status)
	}

	out, err = applyChangeSet(dir, ChangeSet{}, deliveryLimits())
	if err == nil || out.Status != deliveryStatusNoChanges {
		t.Fatalf("empty change set: status=%q err=%v", out.Status, err)
	}

	// Nothing may have been written while a limit was being enforced.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("a refused change set must not write files: %v", entries)
	}
}

func TestApplyChangeSetWritesDeletesAndRefusesEscape(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gone.txt"), []byte("bye\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "content\n"
	out, err := applyChangeSet(dir, ChangeSet{Files: []FileChange{
		{Path: "nested/dir/new.txt", Contents: &body},
		{Path: "gone.txt", Delete: true},
		{Path: "absent.txt", Delete: true},
	}}, deliveryLimits())
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(out.Applied) != 2 {
		t.Fatalf("applied = %v", out.Applied)
	}
	if len(out.Skipped) != 1 || !strings.Contains(out.Skipped[0], "already absent") {
		t.Fatalf("skipped = %v", out.Skipped)
	}
	got, err := os.ReadFile(filepath.Join(dir, "nested/dir/new.txt"))
	if err != nil || string(got) != body {
		t.Fatalf("nested write: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "gone.txt")); !os.IsNotExist(err) {
		t.Fatal("delete did not remove the file")
	}

	// A symlinked directory must not become an escape hatch.
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	escape, err := applyChangeSet(dir, ChangeSet{Files: []FileChange{
		{Path: "link/../../escaped.txt", Contents: &body},
	}}, deliveryLimits())
	if err == nil {
		t.Fatal("escaping via .. must be refused")
	}
	if escape.Status != deliveryStatusUnsafePath {
		t.Fatalf("status = %q", escape.Status)
	}
}

func TestSanitizeBranchSegmentAndName(t *testing.T) {
	cases := map[string]string{
		"001-add-login":    "001-add-login",
		"Ship Login!":      "ship-login",
		"  spaced  out  ":  "spaced-out",
		"weird///slashes":  "weird-slashes",
		"---":              "",
		"":                 "",
		"UPPER_Case.Thing": "upper-case-thing",
	}
	for in, want := range cases {
		if got := sanitizeBranchSegment(in); got != want {
			t.Fatalf("sanitize(%q) = %q want %q", in, got, want)
		}
	}
	if got := deliveryBranchName("001-add-login", ""); got != "opm/001-add-login" {
		t.Fatalf("default branch name = %q", got)
	}
	if got := deliveryBranchName("001-x", "My Retry Branch"); got != "my-retry-branch" {
		t.Fatalf("requested branch name = %q", got)
	}
	if got := deliveryBranchName("", ""); got != "opm/task" {
		t.Fatalf("fallback branch name = %q", got)
	}
	long := deliveryBranchName(strings.Repeat("a", 200), "")
	if len(long) > 70 {
		t.Fatalf("branch name not bounded: %d chars", len(long))
	}
}

func TestRedactSecret(t *testing.T) {
	tok := "ghs_supersecrettoken1234"
	got := redactSecret("remote: rejected "+tok+" end", tok)
	if strings.Contains(got, tok) {
		t.Fatalf("token not redacted: %q", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("redaction marker missing: %q", got)
	}
	// A short or empty secret must not turn into a wildcard replacement.
	if redactSecret("abc", "") != "abc" || redactSecret("abcabc", "abc") != "abcabc" {
		t.Fatal("short/empty secrets must be left alone")
	}
}

func TestDeliveryHTTPCodeMapping(t *testing.T) {
	cases := map[string]int{
		deliveryStatusDelivered:             200,
		deliveryStatusNoChanges:             409,
		deliveryStatusPushRejected:          409,
		deliveryStatusNotLinkedRepo:         400,
		deliveryStatusUnsafePath:            400,
		deliveryStatusChangeSetTooLarge:     400,
		deliveryStatusTaskNotFound:          404,
		deliveryStatusMissingPRPermission:   403,
		deliveryStatusMissingPushPermission: 403,
		deliveryStatusPeerNotConfigured:     503,
		deliveryStatusApplyFailed:           422,
		deliveryStatusGitFailed:             422,
		deliveryStatusUpstreamError:         502,
	}
	for status, want := range cases {
		if got := deliveryHTTPCode(status); got != want {
			t.Fatalf("status %q → %d want %d", status, got, want)
		}
	}
}

func TestDeliveryFaultStatusMapsORAStatuses(t *testing.T) {
	cases := map[string]string{
		"missing_pull_requests_permission": deliveryStatusMissingPRPermission,
		"missing_contents_permission":      deliveryStatusMissingPushPermission,
		"no_commits_between":               deliveryStatusNoChanges,
		"head_branch_not_found":            deliveryStatusPushFailed,
		"something_new":                    deliveryStatusUpstreamError,
	}
	for oraStatus, want := range cases {
		err := &peerDeliveryFault{HTTPStatus: 502, Status: oraStatus, Message: "x"}
		if got := deliveryFaultStatus(err); got != want {
			t.Fatalf("ORA %q → %q want %q", oraStatus, got, want)
		}
	}
	// A 403 without a recognised status is still a permission problem.
	if got := deliveryFaultStatus(&peerDeliveryFault{HTTPStatus: 403, Status: "odd"}); got != deliveryStatusMissingPRPermission {
		t.Fatalf("403 → %q", got)
	}
}

func TestChangeSetFromRunnerIsHonestAboutEmptyOutput(t *testing.T) {
	body := "package main\n"
	cs := changeSetFromRunner("run-9", RunnerResult{
		Mode: "model", Model: "m1", CommitMessage: "Add thing",
		Files: []FileChange{
			{Path: "main.go", Contents: &body},
			{Path: "", Contents: &body},
		},
	})
	if len(cs.Files) != 1 || cs.Source != "model" || cs.Model != "m1" {
		t.Fatalf("change set = %+v", cs)
	}
	if !strings.Contains(cs.Note, "dropped") {
		t.Fatalf("dropped entries must be explained: %q", cs.Note)
	}

	none := changeSetFromRunner("run-9", RunnerResult{Mode: "model", Model: "m1"})
	if len(none.Files) != 0 {
		t.Fatal("no files means no files")
	}
	if none.Note == "" {
		t.Fatal("an empty change set must explain itself rather than look like an oversight")
	}
}

func TestRecordImplementationChangeSetDoesNotInventWork(t *testing.T) {
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
	_ = store.InitProject(p.ID)
	task, err := store.CreateTask(p.ID, "T", "d", false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	j := Job{RunID: "run-1", ProjectID: p.ID, SpecID: task.SpecID, Action: "run-implementation"}

	msg := recordImplementationChangeSet(store, j, RunnerResult{Mode: "model", Model: "m"})
	if !strings.Contains(msg, "nothing to deliver") {
		t.Fatalf("empty output message = %q", msg)
	}
	cs, _ := store.GetChangeSet(p.ID, task.SpecID)
	if len(cs.Files) != 0 {
		t.Fatal("nothing must be recorded when the runner returned no files")
	}

	body := "x\n"
	msg = recordImplementationChangeSet(store, j, RunnerResult{
		Mode: "model", Model: "m", CommitMessage: "Add x",
		Files: []FileChange{{Path: "x.txt", Contents: &body}},
	})
	if !strings.Contains(msg, "1 file change") {
		t.Fatalf("recorded message = %q", msg)
	}
	cs, _ = store.GetChangeSet(p.ID, task.SpecID)
	if len(cs.Files) != 1 || cs.Files[0].Path != "x.txt" || cs.CommitMessage != "Add x" {
		t.Fatalf("change set = %+v", cs)
	}
	logs, _ := store.GetSpecLogs(p.ID, task.SpecID)
	if !strings.Contains(logs["implementation"], "recorded 1 file change") {
		t.Fatalf("implementation log = %q", logs["implementation"])
	}
}

// The pull-request body must tell a reviewer what changed and where it came
// from, without overstating it as reviewed or authoritative.
func TestDeliverPullRequestBodyStatesProvenance(t *testing.T) {
	task := Task{
		SpecID: "001-add-login", Title: "Add login", Description: "Ship the login form",
		GithubIssueNumber: 17, GithubMilestoneNumber: 4, GithubMilestoneTitle: "M4",
	}
	cs := ChangeSet{Source: "model", Model: "m1", RunID: "run-7"}
	body := deliverPullRequestBody(task, cs, []string{"src/login.go", "src/login_test.go"})

	for _, want := range []string{
		"001-add-login", "Add login", "Ship the login form",
		"`src/login.go`", "`src/login_test.go`",
		"m1", "run-7", "Linked issue: #17", "Milestone: #4 M4",
		"Review this diff",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("pull request body missing %q:\n%s", want, body)
		}
	}

	// With no issue or milestone linked, those lines must simply be absent.
	plain := deliverPullRequestBody(Task{SpecID: "002-x", Title: "T"}, ChangeSet{Source: "model"}, []string{"a.go"})
	if strings.Contains(plain, "Linked issue") || strings.Contains(plain, "Milestone:") {
		t.Fatalf("unlinked task must not claim an issue or milestone:\n%s", plain)
	}
}

// The commit message prefers what the runner produced, and never adds a
// co-author trailer.
func TestDeliverCommitMessage(t *testing.T) {
	task := Task{SpecID: "001-x", Title: "Add login", Description: "details"}

	fromRunner := deliverCommitMessage(task, ChangeSet{CommitMessage: "Add login form\n\nbody"})
	if fromRunner != "Add login form\n\nbody" {
		t.Fatalf("runner message must win verbatim: %q", fromRunner)
	}

	derived := deliverCommitMessage(task, ChangeSet{})
	if !strings.HasPrefix(derived, "Add login") || !strings.Contains(derived, "Task: 001-x") {
		t.Fatalf("derived message = %q", derived)
	}
	for _, msg := range []string{fromRunner, derived} {
		if strings.Contains(strings.ToLower(msg), "co-authored-by") {
			t.Fatalf("commit message must not carry a co-author trailer: %q", msg)
		}
	}

	// A task with no title still yields a usable subject.
	untitled := deliverCommitMessage(Task{SpecID: "003-y"}, ChangeSet{})
	if !strings.HasPrefix(untitled, "003-y") {
		t.Fatalf("untitled task message = %q", untitled)
	}
}

func TestRunnerResultCarriesFilesAndCommitMessage(t *testing.T) {
	raw := []byte(`{
	  "mode":"model","action":"run-implementation","model":"m1",
	  "commitMessage":"Add farewell",
	  "files":[
	    {"path":"farewell.txt","contents":"bye\n"},
	    {"path":"old.txt","delete":true},
	    {"path":"src/a.go","patch":"--- a\n+++ b\n"}
	  ]
	}`)
	rr, ok := parseRunnerResultJSON(raw)
	if !ok {
		t.Fatal("result did not parse")
	}
	if rr.CommitMessage != "Add farewell" || len(rr.Files) != 3 {
		t.Fatalf("parsed = %+v", rr)
	}
	if rr.Files[0].Contents == nil || *rr.Files[0].Contents != "bye\n" {
		t.Fatalf("contents not parsed: %+v", rr.Files[0])
	}
	if !rr.Files[1].Delete || rr.Files[2].Patch == "" {
		t.Fatalf("delete/patch not parsed: %+v", rr.Files[1:])
	}
	// A result with no files is still a valid result, just not a deliverable one.
	plain, ok := parseRunnerResultJSON([]byte(`{"mode":"model","implementationNotes":"looked around"}`))
	if !ok || len(plain.Files) != 0 {
		t.Fatalf("notes-only result = %+v ok=%v", plain, ok)
	}
}

func TestRepoMountSourceRequiresRealClone(t *testing.T) {
	if repoMountSource("") != "" {
		t.Fatal("no workspace means no mount")
	}
	stub := t.TempDir()
	if repoMountSource(stub) != "" {
		t.Fatal("a workspace without .git must not be mounted as a repository")
	}
	real := t.TempDir()
	if err := os.MkdirAll(filepath.Join(real, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if repoMountSource(real) != real {
		t.Fatal("a real clone must be mounted")
	}
	if workspaceIsStub(real) {
		t.Fatal("a clone must not be classified as a stub")
	}
	if !workspaceIsStub(stub) {
		t.Fatal("a placeholder workspace must be classified as a stub")
	}
}

// The runner container must receive the repository read-only at /repo, and must
// not receive it at all when there is no clone.
func TestRunnerInputAndMountReflectRepo(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{
		OwnerRepo: "acme/demo", ConnectorID: "c1", OrganizationID: "default-org", DefaultBranch: "trunk",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.InitProject(p.ID)
	task, err := store.CreateTask(p.ID, "T", "d", false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	j := Job{RunID: "run-1", ProjectID: p.ID, SpecID: task.SpecID, Action: "run-implementation"}

	path := filepath.Join(t.TempDir(), "input.json")
	if err := writeRunnerInputJSON(store, j, path, true, ""); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	for _, want := range []string{`"repoDir": "/repo"`, `"ownerRepo": "acme/demo"`, `"defaultBranch": "trunk"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("runner input missing %s: %s", want, body)
		}
	}
	if err := writeRunnerInputJSON(store, j, path, false, ""); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(path)
	if strings.Contains(string(b2), `"repoDir"`) {
		t.Fatalf("no repo means no repoDir: %s", b2)
	}

	// The bind mount is read-only, so the runner cannot mutate what will be committed.
	args := insertBeforeImage([]string{"run", "--rm", "opm-runner-task:nas"},
		"opm-runner-task:nas", "-v", "/host/repo:"+containerRepoPath+":ro")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "/host/repo:/repo:ro") {
		t.Fatalf("repo mount must be read-only: %v", args)
	}
}
