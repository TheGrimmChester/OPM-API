package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	openjob "github.com/TheGrimmChester/open-job-go"
)

// preferContainerSpawn is true when docker CLI + daemon + runner image are ready
// and OPM_FORCE_BUILTIN is not set. Jobs then docker-run opm-runner-task first.
func preferContainerSpawn() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OPM_FORCE_BUILTIN"))) {
	case "1", "true", "yes", "on":
		return false
	}
	p := probeRunnerSpawn()
	return p.SpawnReady
}

func runnerImageTag() string {
	tag := envOr("OPM_RUNNER_TAG", "nas")
	if tag == "smoke" || strings.HasSuffix(tag, "-smoke") || strings.Contains(tag, ":smoke") {
		tag = "nas"
	}
	return tag
}

func runnerImageName() string {
	return openjob.RunnerImage("opm", "task", runnerImageTag())
}

// runTaskContainer starts one hardened ephemeral opm-runner-task container.
// Artifact writes stay in the control plane until model-backed runners land.
func runTaskContainer(j Job) (string, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return "", fmt.Errorf("docker CLI not found: %w", err)
	}
	img := strings.TrimSpace(j.RunnerImage)
	if img == "" {
		img = runnerImageName()
	}
	env := map[string]string{
		"OPM_ACTION":  j.Action,
		"OPM_RUN_ID":  j.RunID,
		"OPM_SPEC_ID": j.SpecID,
		"OPM_PROJECT": j.ProjectID,
	}
	labels := openjob.Labels{
		Product:  "opm",
		JobID:    j.RunID,
		Instance: envOr("OPM_INSTANCE", "default"),
	}
	base := openjob.DockerRunArgv(img, labels, env)
	name := "opm-job-" + sanitizeDockerName(j.RunID)
	args := make([]string, 0, len(base)+3)
	if len(base) == 0 || base[0] != "run" {
		return "", fmt.Errorf("unexpected docker argv")
	}
	args = append(args, "run", "--name", name)
	args = append(args, base[1:]...)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("docker run %s: %w (%s)", img, err, truncateBytes(out, 240))
	}
	return string(out), nil
}

func sanitizeDockerName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := b.String()
	if out == "" {
		out = "anon"
	}
	if len(out) > 48 {
		out = out[:48]
	}
	return out
}
