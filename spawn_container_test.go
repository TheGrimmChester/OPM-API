package main

import (
	"strings"
	"testing"

	openjob "github.com/TheGrimmChester/open-job-go"
)

func TestSanitizeDockerName(t *testing.T) {
	got := sanitizeDockerName("Run_ABC.123/xyz")
	if got != "run_abc-123-xyz" {
		t.Fatalf("got %q", got)
	}
	if sanitizeDockerName("") != "anon" {
		t.Fatal("empty")
	}
}

func TestDockerRunArgvHardened(t *testing.T) {
	args := openjob.DockerRunArgv("opm-runner-task:nas", openjob.Labels{
		Product: "opm", JobID: "r1", Instance: "nas",
	}, map[string]string{"OPM_ACTION": "run-planning", "JWT_SECRET": "nope"})
	joined := strings.Join(args, " ")
	for _, want := range []string{"--rm", "--cap-drop", "ALL", "--network", "none", "--read-only", "opm-runner-task:nas"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %v", want, args)
		}
	}
	if strings.Contains(joined, "JWT_SECRET") {
		t.Fatal("secret leaked into argv")
	}
	if strings.Contains(joined, "docker.sock") {
		t.Fatal("docker.sock must not be mounted into job containers")
	}
}

func TestPreferContainerSpawnForcedBuiltin(t *testing.T) {
	t.Setenv("OPM_FORCE_BUILTIN", "1")
	if preferContainerSpawn() {
		t.Fatal("expected false when OPM_FORCE_BUILTIN=1")
	}
}

func TestReplaceNetwork(t *testing.T) {
	args := []string{"run", "--rm", "--network", "none", "opm-runner-task:nas"}
	got := replaceNetwork(args, "bridge")
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "--network bridge") || strings.Contains(joined, "--network none") {
		t.Fatalf("%v", got)
	}
}

func TestInsertBeforeImage(t *testing.T) {
	args := []string{"run", "--rm", "opm-runner-task:nas"}
	got := insertBeforeImage(args, "opm-runner-task:nas", "-v", "/tmp/in:/in:ro")
	if got[len(got)-1] != "opm-runner-task:nas" || got[len(got)-3] != "-v" {
		t.Fatalf("%v", got)
	}
}
