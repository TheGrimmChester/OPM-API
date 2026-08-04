package main

import (
	"context"
	"os/exec"
	"strings"
	"time"

	openjob "github.com/TheGrimmChester/open-job-go"
)

// runnerSpawnProbe reports honest readiness for containerized agent spawn.
// Real docker run of opm-runner-task remains follow-on; this surfaces docker.sock
// and image presence so operators can see how close the stack is.
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
		Honesty:     "Builtin in-process executor is active. Containerized agent spawn is not wired yet — orchestrator probes docker + runner image only.",
	}

	if _, err := exec.LookPath("docker"); err != nil {
		out.Honesty = "docker CLI not found in PATH; container spawn unavailable. Builtin executor remains the default."
		return out
	}
	out.DockerCLI = true

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").Run(); err != nil {
		out.Honesty = "docker CLI present but daemon unreachable (mount docker.sock on orchestrator). Builtin executor remains the default."
		return out
	}
	out.DockerDaemon = true

	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	inspect := exec.CommandContext(ctx2, "docker", "image", "inspect", "--format", "{{.Id}}", img)
	if err := inspect.Run(); err != nil {
		out.Honesty = "docker daemon reachable; runner image " + img + " not present locally. Build/pull *:nas (never smoke) before container spawn work."
		return out
	}
	out.RunnerImagePresent = true
	// SpawnReady stays false until executeJob actually docker-runs the agent image.
	out.SpawnReady = false
	out.Honesty = "docker + " + img + " present. Jobs still use builtin artifact writer; real container agent spawn is the next step."
	return out
}
