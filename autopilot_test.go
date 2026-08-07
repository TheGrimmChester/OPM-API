package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUsesTaskWorkspaceAutopilot(t *testing.T) {
	t.Parallel()
	if !usesTaskWorkspace("run-implementation", false) {
		t.Fatal("implementation always uses task workspace")
	}
	if !usesTaskWorkspace("run-pipeline", false) {
		t.Fatal("pipeline uses task workspace")
	}
	if usesTaskWorkspace("run-review", false) {
		t.Fatal("local review is gone — no task workspace for run-review")
	}
	if usesTaskWorkspace("run-qa-fix", true) {
		t.Fatal("local qa-fix is gone — no task workspace")
	}
}

func TestTaskWorkspaceWriteMount(t *testing.T) {
	t.Parallel()
	if !taskWorkspaceWriteMount("run-implementation") {
		t.Fatal("implementation needs rw")
	}
	if taskWorkspaceWriteMount("run-review") {
		t.Fatal("review should not mount write")
	}
	if taskWorkspaceWriteMount("run-qa-fix") {
		t.Fatal("qa-fix should not mount write")
	}
}

func TestHumanReviewRequiredDefaultTrue(t *testing.T) {
	t.Parallel()
	if !humanReviewRequired(Task{}) {
		t.Fatal("nil HumanReviewRequired should default to true")
	}
	if humanReviewRequired(Task{HumanReviewRequired: boolPtr(false)}) {
		t.Fatal("explicit false should disable human gate")
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

func TestCodingParksInReviewWithoutLocalReviewJob(t *testing.T) {
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
	task, err := store.CreateTask(p.ID, "Auto task", "d", false, false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	pj, _ := store.CreateJob(p.ID, "run-planning", task.SpecID, "opm-runner-task:nas")
	executeJob(store, pj)
	for i := 0; i < 10; i++ {
		ij, err := store.CreateJob(p.ID, "run-implementation", task.SpecID, "opm-runner-task:nas")
		if err != nil {
			t.Fatal(err)
		}
		executeJob(store, ij)
		cur, _ := store.GetTask(p.ID, task.SpecID)
		if cur.Status == "review" {
			break
		}
	}
	cur, _ := store.GetTask(p.ID, task.SpecID)
	if cur.Status != "review" {
		t.Fatalf("want review (human parking) got %s", cur.Status)
	}
	_, err = store.CreateJob(p.ID, "run-review", task.SpecID, "opm-runner-task:nas")
	if err == nil || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("run-review must be rejected, err=%v", err)
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

func drainCodingSubtasks(t *testing.T, store *Store, projectID, specID string) Job {
	t.Helper()
	var last Job
	for i := 0; i < 12; i++ {
		ij, err := store.CreateJob(projectID, "run-implementation", specID, "opm-runner-task:nas")
		if err != nil {
			t.Fatal(err)
		}
		executeJob(store, ij)
		last, _ = store.GetJob(projectID, ij.RunID)
		plan, _ := store.GetPlan(projectID, specID)
		if allCodingSubtasksComplete(plan) {
			return last
		}
	}
	t.Fatal("coding subtasks did not complete")
	return Job{}
}

func TestCodingCompleteParksInReviewWithoutPR(t *testing.T) {
	t.Setenv("OPM_FORCE_BUILTIN", "1")
	t.Setenv("OPM_IMPL_AUTO_CHAIN", "0")
	t.Setenv("OPM_IMPL_AUTO_DELIVER", "1")
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{OwnerRepo: "acme/x", ConnectorID: "c", OrganizationID: "default-org"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.InitProject(p.ID)
	task, err := store.CreateTask(p.ID, "Gate", "d", false, true, "", "")
	if err != nil {
		t.Fatal(err)
	}
	pj, _ := store.CreateJob(p.ID, "run-planning", task.SpecID, "opm-runner-task:nas")
	executeJob(store, pj)
	last := drainCodingSubtasks(t, store, p.ID, task.SpecID)
	if !strings.Contains(last.Message, "parked in review") {
		t.Fatalf("expected park in review, got %q", last.Message)
	}
	if strings.Contains(last.Message, "delivered") || strings.Contains(last.Message, "auto-deliver") {
		t.Fatalf("coding must not deliver: %q", last.Message)
	}
	cur, _ := store.GetTask(p.ID, task.SpecID)
	if cur.Status != "review" {
		t.Fatalf("want review got %s", cur.Status)
	}
	if cur.PRNumber != 0 {
		t.Fatalf("PR must not open before human deliver, got #%d", cur.PRNumber)
	}
	if cur.DeliveryStatus == deliveryStatusDelivered {
		t.Fatal("deliveryStatus=delivered must not be set before human deliver")
	}
}

func TestCreateReviewJobRejected(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{OwnerRepo: "acme/x", ConnectorID: "c", OrganizationID: "default-org"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.InitProject(p.ID)
	task, err := store.CreateTask(p.ID, "Fail", "d", false, false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateJob(p.ID, "run-review", task.SpecID, "opm-runner-task:nas")
	if err == nil || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("run-review must be rejected, err=%v", err)
	}
	_, err = store.CreateJob(p.ID, "run-qa-fix", task.SpecID, "opm-runner-task:nas")
	if err == nil || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("run-qa-fix must be rejected, err=%v", err)
	}
}
