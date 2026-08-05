package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHumanReviewRequiredDefaultTrue(t *testing.T) {
	t.Parallel()
	if !humanReviewRequired(Task{}) {
		t.Fatal("nil HumanReviewRequired should default to true")
	}
	if humanReviewRequired(Task{HumanReviewRequired: boolPtr(false)}) {
		t.Fatal("explicit false should disable human gate")
	}
}

func TestUsesTaskWorkspaceAutopilot(t *testing.T) {
	t.Parallel()
	if !usesTaskWorkspace("run-implementation", false) {
		t.Fatal("implementation always uses task workspace")
	}
	if usesTaskWorkspace("run-review", false) {
		t.Fatal("review with human gate should not use task workspace")
	}
	if !usesTaskWorkspace("run-review", true) {
		t.Fatal("autopilot review should use task workspace")
	}
	if !usesTaskWorkspace("run-qa-fix", true) {
		t.Fatal("autopilot qa-fix should use task workspace")
	}
	if usesTaskWorkspace("run-qa-fix", false) {
		t.Fatal("manual qa-fix should not use task workspace")
	}
}

func TestTaskWorkspaceWriteMount(t *testing.T) {
	t.Parallel()
	if !taskWorkspaceWriteMount("run-implementation") {
		t.Fatal("implementation needs rw")
	}
	if !taskWorkspaceWriteMount("run-qa-fix") {
		t.Fatal("qa-fix needs rw")
	}
	if taskWorkspaceWriteMount("run-review") {
		t.Fatal("review should be read-only mount")
	}
}

func TestReviewPassStatus(t *testing.T) {
	t.Parallel()
	hr := Task{HumanReviewRequired: boolPtr(true)}
	if reviewPassStatus(hr) != "human_review" {
		t.Fatalf("got %q", reviewPassStatus(hr))
	}
	auto := Task{HumanReviewRequired: boolPtr(false)}
	if reviewPassStatus(auto) != "review" {
		t.Fatalf("got %q", reviewPassStatus(auto))
	}
}

func TestCreateTaskHumanReviewDefaultTrue(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{OwnerRepo: "acme/x", ConnectorID: "c", OrganizationID: "default-org"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.InitProject(p.ID)
	task, err := store.CreateTask(p.ID, "T", "d", false, true, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !humanReviewRequired(task) {
		t.Fatal("CreateTask should store humanReviewRequired true by default")
	}
	auto, err := store.CreateTask(p.ID, "Auto", "d", false, false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if humanReviewRequired(auto) {
		t.Fatal("explicit false should enable autopilot")
	}
}

func TestBuiltinReviewAutopilotMovesToDoneWithoutPR(t *testing.T) {
	t.Setenv("OPM_FORCE_BUILTIN", "1")
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{OwnerRepo: "acme/x", ConnectorID: "c", OrganizationID: "default-org"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.InitProject(p.ID)
	task, err := store.CreateTask(p.ID, "Auto task", "d", false, false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	pj, _ := store.CreateJob(p.ID, "run-planning", task.SpecID, "opm-runner-task:nas")
	executeJob(store, pj)
	for i := 0; i < 10; i++ {
		ij, _ := store.CreateJob(p.ID, "run-implementation", task.SpecID, "opm-runner-task:nas")
		executeJob(store, ij)
		cur, _ := store.GetTask(p.ID, task.SpecID)
		if cur.Status == "review" || cur.Status == "done" {
			break
		}
	}
	rj, _ := store.CreateJob(p.ID, "run-review", task.SpecID, "opm-runner-task:nas")
	executeJob(store, rj)
	cur, _ := store.GetTask(p.ID, task.SpecID)
	if cur.Status == "human_review" {
		t.Fatal("autopilot review PASS should not stop at human_review")
	}
	if cur.Status != "done" {
		t.Fatalf("want done (no PR / soft complete) got %s", cur.Status)
	}
}

func TestPostPlanningChainsImplementation(t *testing.T) {
	t.Setenv("OPM_FORCE_BUILTIN", "1")
	t.Setenv("OPM_IMPL_AUTO_CHAIN", "1")
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{OwnerRepo: "acme/x", ConnectorID: "c", OrganizationID: "default-org"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.InitProject(p.ID)
	task, err := store.CreateTask(p.ID, "Auto", "d", true, false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	j, err := store.CreateJobWithOrigin(p.ID, "run-planning", task.SpecID, "opm-runner-task:nas", JobOrigin{})
	if err != nil {
		t.Fatal(err)
	}
	executeJob(store, j)
	j, _ = store.GetJob(p.ID, j.RunID)
	if j.State != "completed" {
		t.Fatalf("planning job=%+v", j)
	}
	if !strings.Contains(j.Message, "chained run-implementation") {
		t.Fatalf("expected chain, message=%s", j.Message)
	}
	// Wait briefly for chained job to appear / finish planning auto-approve path.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		jobs, _ := store.ListJobs(p.ID)
		for _, jj := range jobs {
			if jj.Action == "run-implementation" && jj.SpecID == task.SpecID {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("expected chained run-implementation job")
}

func TestCreateTaskHTTPEnqueuesPlanning(t *testing.T) {
	t.Setenv("OPM_FORCE_BUILTIN", "1")
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{OwnerRepo: "acme/x", ConnectorID: "c", OrganizationID: "default-org"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.InitProject(p.ID)
	mux := http.NewServeMux()
	registerOPMMux(mux, store, func(path string, h http.HandlerFunc) {
		mux.HandleFunc(path, h)
	}, func(string, http.HandlerFunc) {})
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+p.ID+"/tasks", strings.NewReader(`{"title":"Ship","description":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Organization-ID", "default-org")
	req.Header.Set("X-Project-ID", p.ID)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var created Task
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if humanReviewRequired(created) {
		t.Fatal("create should default humanReviewRequired=false")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		jobs, _ := store.ListJobs(p.ID)
		for _, j := range jobs {
			if j.Action == "run-pipeline" && j.SpecID == created.SpecID {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("expected auto-enqueued run-pipeline")
}
