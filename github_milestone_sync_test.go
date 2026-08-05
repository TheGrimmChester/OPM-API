package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubMilestoneORA is a fake ORA peer for the milestone routes. It counts calls so
// a test can prove that an unassign never reaches GitHub.
type stubMilestoneORA struct {
	server    *httptest.Server
	listCode  int
	listBody  string
	upsCode   int
	upsBody   string
	lastPath  string
	lastBody  map[string]interface{}
	listCalls int
	upsCalls  int
	otherHits []string
}

func newStubMilestoneORA(t *testing.T) *stubMilestoneORA {
	t.Helper()
	s := &stubMilestoneORA{listCode: 200, upsCode: 200,
		listBody: `{"ok":true,"milestones":[{"number":4,"title":"M4","state":"open"}]}`,
		upsBody:  `{"ok":true,"milestone":{"number":4,"title":"M4","html_url":"https://github.com/acme/demo/milestone/4"}}`}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.lastPath = r.URL.Path
		s.lastBody = map[string]interface{}{}
		_ = json.NewDecoder(r.Body).Decode(&s.lastBody)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/milestones/list"):
			s.listCalls++
			w.WriteHeader(s.listCode)
			_, _ = w.Write([]byte(s.listBody))
		case strings.HasSuffix(r.URL.Path, "/milestones/upsert"):
			s.upsCalls++
			w.WriteHeader(s.upsCode)
			_, _ = w.Write([]byte(s.upsBody))
		default:
			s.otherHits = append(s.otherHits, r.Method+" "+r.URL.Path)
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":"unexpected path"}`))
		}
	}))
	t.Cleanup(s.server.Close)
	return s
}

// milestoneFixture wires the real mux against the stub, with a task bound to
// milestone #4 and a roadmap whose feature and phase carry binds of their own.
func milestoneFixture(t *testing.T, ora *stubMilestoneORA) (*http.ServeMux, *Store, Project, Task) {
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
	task, err = store.UpdateTask(p.ID, task.SpecID, map[string]interface{}{
		"githubMilestoneNumber": float64(4),
		"githubMilestoneTitle":  "M4",
		"githubMilestoneUrl":    "https://github.com/acme/demo/milestone/4",
	})
	if err != nil {
		t.Fatal(err)
	}
	rm := Roadmap{
		ID: "r1", ProjectName: "demo", Version: "0.1.0",
		Phases: []RoadmapPhase{{
			ID: "ph1", Name: "Phase 1", Status: "planned", Order: 1,
			GithubMilestoneNumber: 9, GithubMilestoneTitle: "P9",
			GithubMilestoneURL: "https://github.com/acme/demo/milestone/9",
		}},
		Features: []RoadmapFeature{{
			ID: "f1", Title: "Feat", Priority: "high", Status: "planned", PhaseID: "ph1",
			GithubMilestoneNumber: 7, GithubMilestoneTitle: "F7",
			GithubMilestoneURL: "https://github.com/acme/demo/milestone/7",
		}},
	}
	if err := store.PutRoadmap(p.ID, rm); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	reg := func(pattern string, h http.HandlerFunc) { mux.HandleFunc(pattern, h) }
	registerOPMMux(mux, store, reg, reg)
	return mux, store, p, task
}

// --- the task bind ----------------------------------------------------------

func TestMilestoneUnassignClearsTaskBinding(t *testing.T) {
	ora := newStubMilestoneORA(t)
	mux, store, p, task := milestoneFixture(t, ora)

	rr := postProject(t, mux, p, "milestones/unassign", map[string]interface{}{"specId": task.SpecID})
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	out := decodeJSON(t, rr)
	if got := strFromAny(out["status"]); got != milestoneSyncOK {
		t.Fatalf("status=%q", got)
	}
	if got := intFromAny(out["unassignedMilestoneNumber"]); got != 4 {
		t.Fatalf("unassignedMilestoneNumber=%d, want 4", got)
	}
	if got := strFromAny(out["unassignedMilestoneTitle"]); got != "M4" {
		t.Fatalf("unassignedMilestoneTitle=%q", got)
	}
	if got := strFromAny(out["note"]); !strings.Contains(got, "left untouched") {
		t.Fatalf("the response must say GitHub was left alone, got %q", got)
	}

	stored, err := store.GetTask(p.ID, task.SpecID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.GithubMilestoneNumber != 0 || stored.GithubMilestoneTitle != "" || stored.GithubMilestoneURL != "" {
		t.Fatalf("every milestone field must be cleared, got %+v", stored)
	}
}

// The whole point of the route: OPM forgets the bind, GitHub keeps the milestone.
func TestMilestoneUnassignNeverCallsGitHub(t *testing.T) {
	ora := newStubMilestoneORA(t)
	mux, _, p, task := milestoneFixture(t, ora)

	rr := postProject(t, mux, p, "milestones/unassign", map[string]interface{}{"specId": task.SpecID})
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if ora.upsCalls != 0 || ora.listCalls != 0 {
		t.Fatalf("unassign must not touch the milestone on GitHub, got upsert=%d list=%d",
			ora.upsCalls, ora.listCalls)
	}
	if len(ora.otherHits) != 0 {
		t.Fatalf("unassign must reach no peer route at all, got %v", ora.otherHits)
	}
}

func TestMilestoneUnassignWritesSpecLog(t *testing.T) {
	ora := newStubMilestoneORA(t)
	mux, store, p, task := milestoneFixture(t, ora)

	if rr := postProject(t, mux, p, "milestones/unassign",
		map[string]interface{}{"specId": task.SpecID}); rr.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	logs, err := store.GetSpecLogs(p.ID, task.SpecID)
	if err != nil {
		t.Fatal(err)
	}
	logged := logs[milestoneSyncLogPhase]
	if !strings.Contains(logged, "unassign milestone #4") {
		t.Fatalf("spec log should name the milestone, got %q", logged)
	}
	if !strings.Contains(logged, "left untouched") {
		t.Fatalf("spec log should record that GitHub was left alone, got %q", logged)
	}
}

func TestMilestoneUnassignWhenNotBound(t *testing.T) {
	ora := newStubMilestoneORA(t)
	mux, store, p, _ := milestoneFixture(t, ora)
	fresh, err := store.CreateTask(p.ID, "Unbound", "", false, true, "", "")
	if err != nil {
		t.Fatal(err)
	}

	rr := postProject(t, mux, p, "milestones/unassign", map[string]interface{}{"specId": fresh.SpecID})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rr.Code, rr.Body.String())
	}
	out := decodeJSON(t, rr)
	if got := strFromAny(out["status"]); got != milestoneNotBound {
		t.Fatalf("status=%q, want %s", got, milestoneNotBound)
	}
	if got := strFromAny(out["specId"]); got != fresh.SpecID {
		t.Fatalf("the error must name the target, got %q", got)
	}
}

// A half-written bind — a title with no number — is still worth clearing.
func TestMilestoneUnassignClearsTitleOnlyBind(t *testing.T) {
	ora := newStubMilestoneORA(t)
	mux, store, p, _ := milestoneFixture(t, ora)
	partial, err := store.CreateTask(p.ID, "Half bound", "", false, true, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MutateTask(p.ID, partial.SpecID, func(tk *Task) {
		tk.GithubMilestoneTitle = "orphaned title"
	}); err != nil {
		t.Fatal(err)
	}

	rr := postProject(t, mux, p, "milestones/unassign", map[string]interface{}{"specId": partial.SpecID})
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	stored, _ := store.GetTask(p.ID, partial.SpecID)
	if stored.GithubMilestoneTitle != "" {
		t.Fatalf("a title-only bind must be cleared, got %q", stored.GithubMilestoneTitle)
	}
}

func TestMilestoneUnassignTaskNotFound(t *testing.T) {
	ora := newStubMilestoneORA(t)
	mux, _, p, _ := milestoneFixture(t, ora)

	rr := postProject(t, mux, p, "milestones/unassign", map[string]interface{}{"specId": "SPEC-nope"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", rr.Code, rr.Body.String())
	}
	if got := strFromAny(decodeJSON(t, rr)["status"]); got != milestoneTaskNotFound {
		t.Fatalf("status=%q, want %s", got, milestoneTaskNotFound)
	}
}

func TestMilestoneUnassignRequiresATarget(t *testing.T) {
	ora := newStubMilestoneORA(t)
	mux, _, p, _ := milestoneFixture(t, ora)

	rr := postProject(t, mux, p, "milestones/unassign", map[string]interface{}{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if got := strFromAny(decodeJSON(t, rr)["status"]); got != milestoneInvalidRequest {
		t.Fatalf("status=%q, want %s", got, milestoneInvalidRequest)
	}
}

func TestMilestoneUnassignRejectsInvalidJSON(t *testing.T) {
	ora := newStubMilestoneORA(t)
	mux, _, p, _ := milestoneFixture(t, ora)

	req := httptest.NewRequest(http.MethodPost,
		"/api/projects/"+p.ID+"/github/milestones/unassign", strings.NewReader("{not json"))
	req.Header.Set("X-Organization-ID", "default-org")
	req.Header.Set("X-Project-ID", p.ID)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if got := strFromAny(decodeJSON(t, rr)["status"]); got != milestoneInvalidRequest {
		t.Fatalf("status=%q, want %s", got, milestoneInvalidRequest)
	}
}

func TestMilestoneUnassignRejectsGET(t *testing.T) {
	ora := newStubMilestoneORA(t)
	mux, _, p, _ := milestoneFixture(t, ora)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+p.ID+"/github/milestones/unassign", nil)
	req.Header.Set("X-Organization-ID", "default-org")
	req.Header.Set("X-Project-ID", p.ID)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d: %s", rr.Code, rr.Body.String())
	}
}

// --- roadmap feature / phase binds ------------------------------------------

func TestMilestoneUnassignClearsFeatureAndPhase(t *testing.T) {
	ora := newStubMilestoneORA(t)
	mux, store, p, _ := milestoneFixture(t, ora)

	rr := postProject(t, mux, p, "milestones/unassign",
		map[string]interface{}{"featureId": "f1", "phaseId": "ph1"})
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	out := decodeJSON(t, rr)
	clearedRaw, ok := out["cleared"].([]interface{})
	if !ok || len(clearedRaw) != 2 {
		t.Fatalf("both targets must be reported, got %v", out["cleared"])
	}

	rm, err := store.GetRoadmap(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	f := rm.Features[0]
	if f.GithubMilestoneNumber != 0 || f.GithubMilestoneTitle != "" || f.GithubMilestoneURL != "" {
		t.Fatalf("feature bind must be cleared, got %+v", f)
	}
	ph := rm.Phases[0]
	if ph.GithubMilestoneNumber != 0 || ph.GithubMilestoneTitle != "" || ph.GithubMilestoneURL != "" {
		t.Fatalf("phase bind must be cleared, got %+v", ph)
	}
	if ora.upsCalls != 0 {
		t.Fatalf("roadmap unassign must not touch GitHub, got %d upserts", ora.upsCalls)
	}
}

func TestMilestoneUnassignUnknownFeature(t *testing.T) {
	ora := newStubMilestoneORA(t)
	mux, store, p, _ := milestoneFixture(t, ora)

	rr := postProject(t, mux, p, "milestones/unassign", map[string]interface{}{"featureId": "nope"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", rr.Code, rr.Body.String())
	}
	if got := strFromAny(decodeJSON(t, rr)["status"]); got != milestoneFeatureNotFound {
		t.Fatalf("status=%q, want %s", got, milestoneFeatureNotFound)
	}
	// A bad id must not have half-cleared the roadmap on its way out.
	rm, _ := store.GetRoadmap(p.ID)
	if rm.Features[0].GithubMilestoneNumber != 7 || rm.Phases[0].GithubMilestoneNumber != 9 {
		t.Fatalf("roadmap must be untouched, got %+v", rm)
	}
}

func TestMilestoneUnassignUnknownPhaseLeavesFeatureBound(t *testing.T) {
	ora := newStubMilestoneORA(t)
	mux, store, p, _ := milestoneFixture(t, ora)

	rr := postProject(t, mux, p, "milestones/unassign",
		map[string]interface{}{"featureId": "f1", "phaseId": "nope"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", rr.Code, rr.Body.String())
	}
	if got := strFromAny(decodeJSON(t, rr)["status"]); got != milestonePhaseNotFound {
		t.Fatalf("status=%q, want %s", got, milestonePhaseNotFound)
	}
	// The roadmap is written once, after both lookups succeed, so a bad phase id
	// cannot leave the feature half-unassigned on disk.
	rm, _ := store.GetRoadmap(p.ID)
	if rm.Features[0].GithubMilestoneNumber != 7 {
		t.Fatalf("feature bind must survive a rejected call, got %+v", rm.Features[0])
	}
}

// A roadmap that cannot be parsed must not be reported as "the feature isn't bound".
func TestMilestoneUnassignRoadmapUnreadable(t *testing.T) {
	ora := newStubMilestoneORA(t)
	dir := t.TempDir()
	t.Setenv("PEER_ORA_URL", ora.server.URL)
	t.Setenv("OPEN_SERVICE_JWT_SECRET", "test-secret")
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{
		OwnerRepo: "acme/demo", ConnectorID: "c1", OrganizationID: "default-org",
	})
	if err != nil {
		t.Fatal(err)
	}
	// The store scaffolds a missing roadmap.json on every access, so corrupt it
	// rather than deleting it.
	found := ""
	if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "roadmap.json" {
			found = path
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if found == "" {
		t.Fatal("expected a scaffolded roadmap.json")
	}
	if err := os.WriteFile(found, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	reg := func(pattern string, h http.HandlerFunc) { mux.HandleFunc(pattern, h) }
	registerOPMMux(mux, store, reg, reg)

	rr := postProject(t, mux, p, "milestones/unassign", map[string]interface{}{"featureId": "f1"})
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", rr.Code, rr.Body.String())
	}
	if got := strFromAny(decodeJSON(t, rr)["status"]); got != milestoneRoadmapUnreadable {
		t.Fatalf("status=%q, want %s", got, milestoneRoadmapUnreadable)
	}
}

// A project whose roadmap simply has no such feature is a feature-level 404.
func TestMilestoneUnassignEmptyRoadmapIsFeatureNotFound(t *testing.T) {
	ora := newStubMilestoneORA(t)
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
	mux := http.NewServeMux()
	reg := func(pattern string, h http.HandlerFunc) { mux.HandleFunc(pattern, h) }
	registerOPMMux(mux, store, reg, reg)

	rr := postProject(t, mux, p, "milestones/unassign", map[string]interface{}{"featureId": "f1"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", rr.Code, rr.Body.String())
	}
	if got := strFromAny(decodeJSON(t, rr)["status"]); got != milestoneFeatureNotFound {
		t.Fatalf("status=%q, want %s", got, milestoneFeatureNotFound)
	}
}

func TestMilestoneUnassignFeatureWhenNotBound(t *testing.T) {
	ora := newStubMilestoneORA(t)
	mux, store, p, _ := milestoneFixture(t, ora)
	rm, err := store.GetRoadmap(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	clearFeatureMilestoneLink(&rm.Features[0])
	if err := store.PutRoadmap(p.ID, rm); err != nil {
		t.Fatal(err)
	}

	rr := postProject(t, mux, p, "milestones/unassign", map[string]interface{}{"featureId": "f1"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if got := strFromAny(decodeJSON(t, rr)["status"]); got != milestoneNotBound {
		t.Fatalf("status=%q, want %s", got, milestoneNotBound)
	}
}

// --- assign → unassign round trip -------------------------------------------

func TestMilestoneAssignThenUnassignRoundTrip(t *testing.T) {
	ora := newStubMilestoneORA(t)
	mux, store, p, _ := milestoneFixture(t, ora)
	fresh, err := store.CreateTask(p.ID, "Round trip", "", false, true, "", "")
	if err != nil {
		t.Fatal(err)
	}

	rr := postProject(t, mux, p, "milestones/assign", map[string]interface{}{
		"specId": fresh.SpecID, "milestoneNumber": 4,
	})
	if rr.Code != 200 {
		t.Fatalf("assign: want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	bound, _ := store.GetTask(p.ID, fresh.SpecID)
	if bound.GithubMilestoneNumber != 4 || bound.GithubMilestoneURL == "" {
		t.Fatalf("assign should have stored the bind, got %+v", bound)
	}

	rr = postProject(t, mux, p, "milestones/unassign", map[string]interface{}{"specId": fresh.SpecID})
	if rr.Code != 200 {
		t.Fatalf("unassign: want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	after, _ := store.GetTask(p.ID, fresh.SpecID)
	if after.GithubMilestoneNumber != 0 || after.GithubMilestoneTitle != "" || after.GithubMilestoneURL != "" {
		t.Fatalf("unassign must undo assign, got %+v", after)
	}
	// Only the assign spoke to ORA.
	if ora.upsCalls != 1 {
		t.Fatalf("want exactly one upsert (from assign), got %d", ora.upsCalls)
	}
}

// --- the helper -------------------------------------------------------------

func TestClearMilestoneLinkWipesEveryField(t *testing.T) {
	tk := &Task{
		GithubMilestoneNumber: 4,
		GithubMilestoneTitle:  "M4",
		GithubMilestoneURL:    "https://github.com/acme/demo/milestone/4",
	}
	clearMilestoneLink(tk)
	if tk.GithubMilestoneNumber != 0 || tk.GithubMilestoneTitle != "" || tk.GithubMilestoneURL != "" {
		t.Fatalf("every milestone field must be wiped, got %+v", tk)
	}

	f := &RoadmapFeature{GithubMilestoneNumber: 7, GithubMilestoneTitle: "F7", GithubMilestoneURL: "u"}
	clearFeatureMilestoneLink(f)
	if f.GithubMilestoneNumber != 0 || f.GithubMilestoneTitle != "" || f.GithubMilestoneURL != "" {
		t.Fatalf("every feature milestone field must be wiped, got %+v", f)
	}

	ph := &RoadmapPhase{GithubMilestoneNumber: 9, GithubMilestoneTitle: "P9", GithubMilestoneURL: "u"}
	clearPhaseMilestoneLink(ph)
	if ph.GithubMilestoneNumber != 0 || ph.GithubMilestoneTitle != "" || ph.GithubMilestoneURL != "" {
		t.Fatalf("every phase milestone field must be wiped, got %+v", ph)
	}
}

func TestMilestoneBoundCountsPartialBinds(t *testing.T) {
	cases := []struct {
		number int
		title  string
		url    string
		want   bool
	}{
		{0, "", "", false},
		{4, "", "", true},
		{0, "M4", "", true},
		{0, "", "https://github.com/acme/demo/milestone/4", true},
		{-1, "", "", false},
	}
	for _, c := range cases {
		if got := milestoneBound(c.number, c.title, c.url); got != c.want {
			t.Fatalf("milestoneBound(%d,%q,%q)=%v, want %v", c.number, c.title, c.url, got, c.want)
		}
	}
}
