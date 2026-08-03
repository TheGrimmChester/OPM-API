package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func jobTmpRoot() string {
	return envOr("OPM_JOB_TMP", filepath.Join(os.TempDir(), "opm-jobs"))
}

// prepareJobWorkspace creates an ephemeral clone under OPM_JOB_TMP/{runId}.
// Credentials come from ORA (never stored in OPM). Caller must cleanupWorkspace.
func prepareJobWorkspace(p Project, runID string) (string, error) {
	root := filepath.Join(jobTmpRoot(), runID)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	work := filepath.Join(root, "repo")
	if !peerORAConfigured() || p.ConnectorID == "" || p.OwnerRepo == "" {
		// No peer credentials — empty workspace for stub runners.
		if err := os.MkdirAll(work, 0o755); err != nil {
			return "", err
		}
		_ = os.WriteFile(filepath.Join(work, "README.opm-workspace"), []byte(
			"Ephemeral OPM job workspace (no clone — PEER_ORA_URL / connector missing).\n"), 0o644)
		return work, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	creds, err := peerCloneCredentials(ctx, p.OrganizationID, p.ConnectorID, p.OwnerRepo)
	if err != nil {
		_ = os.RemoveAll(root)
		return "", fmt.Errorf("clone credentials: %w", err)
	}
	token, _ := creds["token"].(string)
	cloneURL, _ := creds["clone_url"].(string)
	if token == "" || cloneURL == "" {
		_ = os.RemoveAll(root)
		return "", fmt.Errorf("clone credentials incomplete")
	}

	askPass := filepath.Join(root, "askpass.sh")
	script := "#!/bin/sh\necho \"$OPA_GIT_ASKPASS_TOKEN\"\n"
	if err := os.WriteFile(askPass, []byte(script), 0o700); err != nil {
		_ = os.RemoveAll(root)
		return "", err
	}

	branch := nz(p.DefaultBranch, "main")
	cmd := exec.Command("git", "clone", "--depth", "1", "--branch", branch, cloneURL, work)
	cmd.Env = append(os.Environ(),
		"GIT_ASKPASS="+askPass,
		"OPA_GIT_ASKPASS_TOKEN="+token,
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=never",
	)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Retry without branch pin (default branch may differ).
		_ = os.RemoveAll(work)
		cmd = exec.Command("git", "clone", "--depth", "1", cloneURL, work)
		cmd.Env = append(os.Environ(),
			"GIT_ASKPASS="+askPass,
			"OPA_GIT_ASKPASS_TOKEN="+token,
			"GIT_TERMINAL_PROMPT=0",
			"GCM_INTERACTIVE=never",
		)
		cmd.Dir = root
		out2, err2 := cmd.CombinedOutput()
		if err2 != nil {
			_ = os.RemoveAll(root)
			return "", fmt.Errorf("git clone: %v (%s / %s)", err2, truncateBytes(out, 200), truncateBytes(out2, 200))
		}
	}
	_ = os.Remove(askPass)
	return work, nil
}

func cleanupWorkspace(runID string) {
	if strings.TrimSpace(runID) == "" {
		return
	}
	_ = os.RemoveAll(filepath.Join(jobTmpRoot(), runID))
}

func truncateBytes(b []byte, n int) string {
	s := string(b)
	if len(s) <= n {
		return s
	}
	return s[:n]
}
