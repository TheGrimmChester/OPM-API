package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// callQwenCode drives the Qwen Code CLI in non-interactive mode.
//
// Qwen does not support Cursor-style "auto" model selection; an explicit model
// id from OAM resolve is required. Credentials travel via --openai-api-key so
// env-key naming (DASHSCOPE_API_KEY vs OPENAI_API_KEY) does not matter.
func callQwenCode(c candidate, system, user string) (string, error) {
	m := strings.TrimSpace(c.Model)
	if m == "" || strings.EqualFold(m, "auto") {
		return "", fmt.Errorf("cli_qwen_code requires an explicit model (empty and \"auto\" are not supported)")
	}
	bin := envOr("OPM_QWEN_CODE_BIN", "qwen")
	if _, err := exec.LookPath(bin); err != nil {
		return "", fmt.Errorf("qwen code binary %q not found in PATH (rebuild the runner image with it)", bin)
	}
	args := []string{
		"--auth-type", "openai",
		"--openai-api-key", c.APIKey,
		"--approval-mode", "yolo",
		"--output-format", "text",
		"--model", m,
	}
	if u := strings.TrimSpace(c.BaseURL); u != "" {
		args = append(args, "--openai-base-url", u)
	}
	if system != "" {
		args = append(args, "--system-prompt", system)
	}
	if repoDir := resolveRepoDir(); repoDir != "" {
		args = append(args, "--include-directories", repoDir)
	}
	args = append(args, c.CLIArgs...)
	return runAgentCLI(bin, args, user, "QWEN_CODE_SUPPRESS_YOLO_WARNING=1", c.APIKey)
}

// resolveRepoDir returns OPM_REPO_DIR or repoDir from input.json when mounted.
func resolveRepoDir() string {
	repoDir := strings.TrimSpace(os.Getenv("OPM_REPO_DIR"))
	if repoDir == "" {
		if b, err := os.ReadFile(envOr("OPM_IN_FILE", "/in/input.json")); err == nil {
			var tip struct {
				RepoDir string `json:"repoDir"`
			}
			_ = json.Unmarshal(b, &tip)
			repoDir = tip.RepoDir
		}
	}
	if repoDir != "" {
		if st, err := os.Stat(repoDir); err == nil && st.IsDir() {
			return repoDir
		}
	}
	return ""
}
