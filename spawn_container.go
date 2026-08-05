package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

type containerRunOutcome struct {
	Stdout    string
	Result    RunnerResult
	HasResult bool
}

// containerRepoPath is where the prepared clone appears inside the runner.
const containerRepoPath = "/repo"

// repoMountSource returns the host path to bind at /repo, or "" when there is no
// usable clone. A workspace without .git is the credential-less placeholder
// prepareJobWorkspace writes; mounting it would tell the runner it has source
// when it does not.
func repoMountSource(workDir string) string {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return ""
	}
	if st, err := os.Stat(filepath.Join(workDir, ".git")); err != nil || st == nil {
		return ""
	}
	return workDir
}

// runTaskContainer starts one hardened ephemeral opm-runner-task container.
// When OPM_MODEL_API_KEY or CURSOR_API_KEY is set, enables network + passes model env via --env-file
// (Cursor Agent CLI by default; OpenAI when OPM_MODEL_PROVIDER=openai).
// (Open-Job-Go ScrubEnv strips API_KEY from -e flags). Output lands in /out/result.json.
//
// workDir is the prepared clone of the linked repository. For run-implementation it
// is the task-scoped session repo (read-write). Other actions mount read-only at /repo.
func runTaskContainer(store *Store, j Job, workDir string) (containerRunOutcome, error) {
	var out containerRunOutcome
	if _, err := exec.LookPath("docker"); err != nil {
		return out, fmt.Errorf("docker CLI not found: %w", err)
	}
	img := strings.TrimSpace(j.RunnerImage)
	if img == "" {
		img = runnerImageName()
	}

	inDir := filepath.Join(jobTmpRoot(), j.RunID, "runner-in")
	outDir := filepath.Join(jobTmpRoot(), j.RunID, "runner-out")
	_ = os.MkdirAll(inDir, 0o755)
	_ = os.MkdirAll(outDir, 0o755)

	repoMount := repoMountSource(workDir)
	repoMounted := repoMount != ""
	if repoMounted {
		wantIndex := j.Action == "run-ideation" ||
			j.Action == "run-roadmap-discovery" ||
			j.Action == "run-roadmap-features"
		if wantIndex {
			_, _ = ensureProjectIndex(repoMount, filepath.Join(inDir, "project_index.json"))
		}
	}
	if err := writeRunnerInputJSON(store, j, filepath.Join(inDir, "input.json"), repoMounted, repoMount); err != nil {
		return out, err
	}

	env := map[string]string{
		"OPM_ACTION":  j.Action,
		"OPM_RUN_ID":  j.RunID,
		"OPM_SPEC_ID": j.SpecID,
		"OPM_PROJECT": j.ProjectID,
		"OPM_IN_FILE": "/in/input.json",
		"OPM_OUT_DIR": "/out",
	}
	if repoMounted {
		env["OPM_REPO_DIR"] = containerRepoPath
	}
	if v := envOr("OPM_MODEL_BASE_URL", ""); v != "" {
		env["OPM_MODEL_BASE_URL"] = v
	}
	env["OPM_MODEL"] = envOr("OPM_MODEL", "auto")
	for _, k := range []string{
		"OPM_MODEL_PLANNING", "OPM_MODEL_CODING", "OPM_MODEL_REVIEW",
		"OPM_MODEL_IDEATION", "OPM_MODEL_ROADMAP_DISCOVERY", "OPM_MODEL_ROADMAP_FEATURES",
	} {
		env[k] = phaseModel(k)
	}
	if v := envOr("OPM_MODEL_PROVIDER", ""); v != "" {
		env["OPM_MODEL_PROVIDER"] = v
	}
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			env[k] = v
		}
	}

	labels := openjob.Labels{
		Product:  "opm",
		JobID:    j.RunID,
		Instance: envOr("OPM_INSTANCE", "default"),
	}
	base := openjob.DockerRunArgv(img, labels, env)
	name := "opm-job-" + sanitizeDockerName(j.RunID)
	args := make([]string, 0, len(base)+16)
	if len(base) == 0 || base[0] != "run" {
		return out, fmt.Errorf("unexpected docker argv")
	}
	args = append(args, "run", "--name", name)
	args = append(args, base[1:]...)

	// Cursor agent needs writable HOME + /tmp under --read-only (same as ORA job argv).
	args = insertBeforeImage(args, img,
		"--tmpfs", "/tmp:rw,exec,nosuid,nodev,uid=65532,gid=65532,mode=1777,size=1g",
		"--tmpfs", "/home/opm:rw,exec,nosuid,nodev,uid=65532,gid=65532,mode=1777,size=256m",
	)
	args = insertBeforeImage(args, img, "-v", inDir+":/in:ro", "-v", outDir+":/out")
	if repoMount != "" {
		mountOpt := ":ro"
		if j.Action == "run-implementation" {
			mountOpt = ":rw"
		}
		args = insertBeforeImage(args, img, "-v", repoMount+":"+containerRepoPath+mountOpt)
	}

	wantModel := modelAPIKeyPresent()
	if wantModel {
		netName := envOr("OPM_RUNNER_NETWORK", "bridge")
		args = replaceNetwork(args, netName)

		envFile, cleanup, err := writeModelEnvFile()
		if err != nil {
			return out, err
		}
		defer cleanup()
		args = insertBeforeImage(args, img, "--env-file", envFile)
	}

	timeout := 2 * time.Minute
	if wantModel {
		timeout = 12 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	combined, err := cmd.CombinedOutput()
	out.Stdout = string(combined)
	if b, rerr := os.ReadFile(filepath.Join(outDir, "result.json")); rerr == nil {
		if rr, ok := parseRunnerResultJSON(b); ok {
			out.Result = rr
			out.HasResult = true
		}
	} else if rr, ok := parseRunnerResultJSON(combined); ok {
		out.Result = rr
		out.HasResult = true
	}
	if err != nil {
		return out, fmt.Errorf("docker run %s: %w (%s)", img, err, truncateBytes(combined, 240))
	}
	return out, nil
}

func writeModelEnvFile() (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "opm-model-env-*")
	if err != nil {
		return "", func() {}, err
	}
	path = f.Name()
	cleanup = func() { _ = os.Remove(path) }
	key := strings.TrimSpace(os.Getenv("OPM_MODEL_API_KEY"))
	if key == "" {
		key = strings.TrimSpace(os.Getenv("CURSOR_API_KEY"))
	}
	lines := []string{
		"OPM_MODEL_API_KEY=" + key,
		"CURSOR_API_KEY=" + key,
		"OPM_MODEL=" + envOr("OPM_MODEL", "auto"),
	}
	if v := envOr("OPM_MODEL_BASE_URL", ""); v != "" {
		lines = append(lines, "OPM_MODEL_BASE_URL="+v)
	}
	if v := envOr("OPM_MODEL_PROVIDER", ""); v != "" {
		lines = append(lines, "OPM_MODEL_PROVIDER="+v)
	}
	if v := envOr("OPM_CURSOR_AGENT_BIN", ""); v != "" {
		lines = append(lines, "OPM_CURSOR_AGENT_BIN="+v)
	}
	for _, k := range []string{
		"OPM_MODEL_PLANNING", "OPM_MODEL_CODING", "OPM_MODEL_REVIEW",
		"OPM_MODEL_IDEATION", "OPM_MODEL_ROADMAP_DISCOVERY", "OPM_MODEL_ROADMAP_FEATURES",
	} {
		lines = append(lines, k+"="+phaseModel(k))
	}
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		_ = f.Close()
		cleanup()
		return "", func() {}, err
	}
	_ = f.Close()
	_ = os.Chmod(path, 0o600)
	return path, cleanup, nil
}

func insertBeforeImage(args []string, image string, extra ...string) []string {
	for i := len(args) - 1; i >= 0; i-- {
		if args[i] == image {
			out := make([]string, 0, len(args)+len(extra))
			out = append(out, args[:i]...)
			out = append(out, extra...)
			out = append(out, args[i:]...)
			return out
		}
	}
	return append(args, extra...)
}

func replaceNetwork(args []string, network string) []string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--network" {
			args[i+1] = network
			return args
		}
	}
	return insertBeforeImage(args, args[len(args)-1], "--network", network)
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
