package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	openjobenv "github.com/TheGrimmChester/open-job-env-go"
)

func jobTmpRoot() string {
	return envOr("OPM_JOB_TMP", filepath.Join(os.TempDir(), "opm-jobs"))
}

// prepareJobWorkspace creates an ephemeral clone under OPM_JOB_TMP/{runId}.
// Credentials come from ORA (never stored in OPM). Caller must cleanupWorkspace.
// userID is stamped on the peer service JWT for personal (empty-org) connectors.
func prepareJobWorkspace(ctx context.Context, p Project, runID, userID string) (string, error) {
	root := filepath.Join(jobTmpRoot(), runID)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	return cloneRepoInto(ctx, p, root, "repo", userID)
}

// cloneRepoInto shallow-clones ownerRepo into root/subdir when credentials exist.
func cloneRepoInto(ctx context.Context, p Project, root, subdir, userID string) (string, error) {
	work := filepath.Join(root, subdir)
	if !peerORAConfigured() || p.ConnectorID == "" || p.OwnerRepo == "" {
		if err := os.MkdirAll(work, 0o755); err != nil {
			return "", err
		}
		_ = os.WriteFile(filepath.Join(work, "README.opm-workspace"), []byte(
			"Ephemeral OPM job workspace (no clone — PEER_ORA_URL / connector missing).\n"), 0o644)
		return work, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	creds, err := peerCloneCredentials(ctx, p.OrganizationID, userID, p.ConnectorID, p.OwnerRepo)
	if err != nil {
		return "", fmt.Errorf("clone credentials: %w", err)
	}
	token, _ := creds["token"].(string)
	cloneURL, _ := creds["clone_url"].(string)
	if token == "" || cloneURL == "" {
		return "", fmt.Errorf("clone credentials incomplete")
	}

	askPass := filepath.Join(root, "askpass.sh")
	script := "#!/bin/sh\necho \"$OPA_GIT_ASKPASS_TOKEN\"\n"
	if err := os.WriteFile(askPass, []byte(script), 0o700); err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(askPass) }()

	branch := nz(p.DefaultBranch, "main")
	cmd := exec.Command("git", "clone", "--depth", "1", "--branch", branch, cloneURL, work)
	cmd.Env = append(openjobenv.HostToolEnv(),
		"GIT_ASKPASS="+askPass,
		"OPA_GIT_ASKPASS_TOKEN="+token,
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=never",
	)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(work)
		cmd = exec.Command("git", "clone", "--depth", "1", cloneURL, work)
		cmd.Env = append(openjobenv.HostToolEnv(),
			"GIT_ASKPASS="+askPass,
			"OPA_GIT_ASKPASS_TOKEN="+token,
			"GIT_TERMINAL_PROMPT=0",
			"GCM_INTERACTIVE=never",
		)
		cmd.Dir = root
		out2, err2 := cmd.CombinedOutput()
		if err2 != nil {
			return "", fmt.Errorf("git clone: %v (%s / %s)", err2,
				redactSecret(truncateBytes(out, 200), token),
				redactSecret(truncateBytes(out2, 200), token))
		}
	}
	return work, nil
}

func cleanupWorkspace(runID string) {
	cleanupRunnerScratch(runID)
}

func truncateBytes(b []byte, n int) string {
	s := string(b)
	if len(s) <= n {
		return s
	}
	return s[:n]
}
