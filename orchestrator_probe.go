package main

import (
	"context"
	"os/exec"
	"strings"
	"time"

	openjob "github.com/TheGrimmChester/open-job-go"
)

// runnerSpawnProbe reports honest readiness for containerized agent spawn.
// When SpawnReady is true, jobs docker-run opm-runner-task before writing artifacts
// (builtin helpers remain the artifact writer and the fallback when docker fails).
type runnerSpawnProbe struct {
	RunnerImage        string `json:"runnerImage"`
	DockerCLI          bool   `json:"dockerCli"`
	DockerDaemon       bool   `json:"dockerDaemon"`
	RunnerImagePresent bool   `json:"runnerImagePresent"`
	SpawnReady         bool   `json:"spawnReady"`
	Honesty            string `json:"honesty"`
}

func probeRunnerSpawn() runnerSpawnProbe {
	tag := envOr("OPM_RUNNER_TAG", "nas")
	if tag == "smoke" || strings.HasSuffix(tag, "-smoke") || strings.Contains(tag, ":smoke") {
		tag = "nas"
	}
	img := openjob.RunnerImage("opm", "task", tag)
	out := runnerSpawnProbe{
		RunnerImage: img,
		Honesty:     "Container spawn unavailable. Jobs use the builtin in-process executor (OPM_FORCE_BUILTIN or missing docker).",
	}

	if _, err := exec.LookPath("docker"); err != nil {
		out.Honesty = "docker CLI not found in PATH; container spawn unavailable. Builtin executor remains the fallback."
		return out
	}
	out.DockerCLI = true

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").Run(); err != nil {
		out.Honesty = "docker CLI present but daemon unreachable (mount docker.sock on opm-api and opm-orchestrator). Builtin executor remains the fallback."
		return out
	}
	out.DockerDaemon = true

	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	inspect := exec.CommandContext(ctx2, "docker", "image", "inspect", "--format", "{{.Id}}", img)
	if err := inspect.Run(); err != nil {
		out.Honesty = "docker daemon reachable; runner image " + img + " not present locally. Build/pull *:nas (never smoke) before container spawn."
		return out
	}
	out.RunnerImagePresent = true
	out.SpawnReady = true
	out.Honesty = "docker + " + img + " ready. Jobs docker-run the runner when spawnReady; artifact writes use shared helpers with builtin fallback on spawn failure. Set OPM_FORCE_BUILTIN=1 to skip containers."
	return out
}
