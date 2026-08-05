package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPopulateProjectIndexAndroid(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.gradle.kts"), []byte("rootProject.name = \"ArrCompanion\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build.gradle.kts"), []byte("plugins {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := populateProjectIndex(dir)
	svc, ok := idx.Services["android"]
	if !ok {
		t.Fatalf("services=%v", idx.Services)
	}
	if !containsString(svc.TechStack, "android") || !containsString(svc.TechStack, "kotlin") {
		t.Fatalf("tech=%v", svc.TechStack)
	}
}

func TestCloneHonestyOperatorNote(t *testing.T) {
	p := Project{OwnerRepo: "o/r", ConnectorID: "c1"}
	note := cloneHonestyOperatorNote(p, "")
	if note == "" {
		t.Fatal("expected note for empty workDir")
	}
	if !strings.Contains(note, "o/r") {
		t.Fatalf("note=%q", note)
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "README.opm-workspace"), []byte("stub"), 0o644)
	if cloneHonestyOperatorNote(p, dir) == "" {
		t.Fatal("stub workspace should not mount")
	}
	gitDir := filepath.Join(dir, ".git")
	_ = os.MkdirAll(gitDir, 0o755)
	if cloneHonestyOperatorNote(p, dir) != "" {
		t.Fatal("real clone should not produce honesty note")
	}
}

func TestPopulateProjectIndexGoMod(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/app\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := populateProjectIndex(dir)
	if idx.ProjectType != "single" {
		t.Fatalf("project_type=%q", idx.ProjectType)
	}
	svc, ok := idx.Services["go"]
	if !ok {
		t.Fatalf("services=%v", idx.Services)
	}
	if !containsString(svc.TechStack, "go") {
		t.Fatalf("tech=%v", svc.TechStack)
	}
	if !idx.Infrastructure.Docker {
		t.Fatal("expected docker")
	}
	if idx.Conventions.Testing == nil || *idx.Conventions.Testing != "go test" {
		t.Fatalf("testing=%v", idx.Conventions.Testing)
	}
}

func TestEnsureProjectIndexWrites(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"x","scripts":{"test":"jest"}}`), 0o644)
	out := filepath.Join(t.TempDir(), "project_index.json")
	idx, err := ensureProjectIndex(dir, out)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var got ProjectIndex
	if json.Unmarshal(b, &got) != nil {
		t.Fatalf("bad json %s", b)
	}
	if got.ProjectType != idx.ProjectType {
		t.Fatal("mismatch")
	}
}

func TestBuildRunnerContextDiscoveryExcludesIdeaBodies(t *testing.T) {
	t.Setenv("OPM_FORCE_BUILTIN", "1")
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{Name: "ArrM", OwnerRepo: "o/arrm", ConnectorID: "c", OrganizationID: "default-org"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.InitProject(p.ID)
	ideas := emptyIdeation()
	ideas["security"] = []Idea{{
		ID: "idea-1", Title: "Secret leak", Description: "SECRET_BODY_SHOULD_NOT_APPEAR_IN_DISCOVERY",
		Status: "open", CreatedAt: nowUTC().Format("2006-01-02"),
	}}
	if err := store.PutIdeation(p.ID, ideas); err != nil {
		t.Fatal(err)
	}
	_ = store.PutRoadmap(p.ID, Roadmap{
		ID: "r1", ProjectName: "ArrM", Vision: "Media manager",
		Phases: []RoadmapPhase{{ID: "p1", Name: "Foundation", Order: 1, Status: "planned"}},
		Features: []RoadmapFeature{{ID: "f1", Title: "Indexer", Status: "planned"}},
	})

	repo := t.TempDir()
	_ = os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module arrm\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("# ArrM\nMedia library\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(repo, ".git"), 0o755)

	j := Job{RunID: "r-disc", ProjectID: p.ID, Action: "run-roadmap-discovery"}
	ctx := buildRunnerContext(store, j, repo, true)
	raw, _ := json.Marshal(ctx)
	s := string(raw)
	if strings.Contains(s, "SECRET_BODY_SHOULD_NOT_APPEAR_IN_DISCOVERY") {
		t.Fatalf("discovery context leaked ideation body: %s", s)
	}
	if strings.Contains(s, "ideationContext") {
		t.Fatal("discovery must not include ideationContext")
	}
	if ctx.ProjectIndex == nil {
		t.Fatal("expected projectIndex")
	}
	if ctx.ReadmeExcerpt == "" {
		t.Fatal("expected readme")
	}
}

func TestBuildRunnerContextIdeationWritesContext(t *testing.T) {
	t.Setenv("OPM_FORCE_BUILTIN", "1")
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{Name: "x", OwnerRepo: "o/r", ConnectorID: "c", OrganizationID: "default-org"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.InitProject(p.ID)
	task, err := store.CreateTask(p.ID, "Done idea title", "d", false, "idea-done", "security")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = store.MoveTask(p.ID, task.SpecID, "done")

	j := Job{RunID: "r-idea", ProjectID: p.ID, Action: "run-ideation", IdeationType: "security"}
	ctx := buildRunnerContext(store, j, "", false)
	if ctx.IdeationContext == nil {
		t.Fatal("expected ideationContext")
	}
	if !containsString(ctx.ImplementedIdeaIDs, "idea-done") {
		t.Fatalf("ids=%v", ctx.ImplementedIdeaIDs)
	}
	if !containsString(ctx.ImplementedIdeaTitles, "done idea title") {
		t.Fatalf("titles=%v", ctx.ImplementedIdeaTitles)
	}
	if ctx.CloneHonestyNote == "" {
		t.Fatal("expected clone honesty note when unmounted")
	}
}

func TestWriteRunnerInputJSONNestedContext(t *testing.T) {
	t.Setenv("OPM_FORCE_BUILTIN", "1")
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateProject(Project{Name: "x", OwnerRepo: "o/r", ConnectorID: "c", OrganizationID: "default-org"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.InitProject(p.ID)
	path := filepath.Join(t.TempDir(), "input.json")
	j := Job{RunID: "r1", ProjectID: p.ID, Action: "run-roadmap-features"}
	if err := writeRunnerInputJSON(store, j, path, false, ""); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var payload map[string]any
	if json.Unmarshal(b, &payload) != nil {
		t.Fatal(string(b))
	}
	ctx, ok := payload["context"].(map[string]any)
	if !ok {
		t.Fatalf("missing context: %s", b)
	}
	if _, ok := ctx["needsDiscovery"]; !ok {
		t.Fatalf("expected needsDiscovery: %v", ctx)
	}
	if note, _ := payload["cloneHonestyNote"].(string); note == "" {
		t.Fatal("expected cloneHonestyNote")
	}
}
