package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Git side of a delivery: branch, stage the exact applied paths, commit, push.
//
// Credential handling rules, enforced here and nowhere else:
//   - the push token arrives from ORA per request and is never written to
//     OPM_DATA_DIR, never persisted on the task, never logged
//   - it reaches git only through GIT_ASKPASS + an environment variable, so it
//     never appears in argv (visible in `ps`) or in a remote URL (recorded in
//     .git/config and in git's own error text)
//   - every git output that can be returned to a caller passes through
//     redactSecret first, as a second line of defence

// gitRunner executes git in a workspace with an optional askpass credential.
type gitRunner struct {
	dir string
	// token is the short-lived push credential. Empty for local-only operations.
	token string
	// askPass is the helper script path, created lazily and removed by close().
	askPass string
}

func newGitRunner(dir string) *gitRunner { return &gitRunner{dir: dir} }

// withToken arms the runner with a push credential for the calls that need one.
func (g *gitRunner) withToken(token string) *gitRunner {
	g.token = strings.TrimSpace(token)
	return g
}

// close removes the askpass helper. Always defer it.
func (g *gitRunner) close() {
	if g.askPass != "" {
		_ = os.Remove(g.askPass)
		g.askPass = ""
	}
	g.token = ""
}

// ensureAskPass writes the helper that echoes the token from the environment.
func (g *gitRunner) ensureAskPass() (string, error) {
	if g.askPass != "" {
		return g.askPass, nil
	}
	if g.token == "" {
		return "", nil
	}
	path := filepath.Join(g.dir, ".opm-askpass.sh")
	script := "#!/bin/sh\necho \"$OPM_GIT_ASKPASS_TOKEN\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		return "", err
	}
	g.askPass = path
	return path, nil
}

// run executes git and returns its combined output with the credential redacted.
func (g *gitRunner) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.dir
	env := append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=never",
	)
	if g.token != "" {
		askPass, err := g.ensureAskPass()
		if err != nil {
			return "", err
		}
		env = append(env,
			"GIT_ASKPASS="+askPass,
			"OPM_GIT_ASKPASS_TOKEN="+g.token,
		)
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	text := redactSecret(string(out), g.token)
	if err != nil {
		return text, fmt.Errorf("git %s: %v (%s)", args[0], err,
			strings.TrimSpace(truncateRunes(text, 400)))
	}
	return text, nil
}

// redactSecret removes a credential from text that may be surfaced to a caller.
func redactSecret(text, secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" || len(secret) < 8 {
		return text
	}
	return strings.ReplaceAll(text, secret, "[redacted]")
}

// deliveryBranchPrefix namespaces delivery branches so they are recognisable in
// the repository and easy to protect with branch rules.
func deliveryBranchPrefix() string {
	return strings.Trim(envOr("OPM_DELIVERY_BRANCH_PREFIX", "opm"), "/")
}

// sanitizeBranchSegment reduces free text to a git-safe ref segment.
func sanitizeBranchSegment(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		case r == '.' || r == '_' || r == '-' || r == '/':
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-.")
	if len(out) > 60 {
		out = strings.Trim(out[:60], "-.")
	}
	return out
}

// deliveryBranchName builds the branch a task is delivered on. A caller-supplied
// branch is sanitized but otherwise honoured, so an operator can retry onto a
// fresh branch after a rejected push.
func deliveryBranchName(specID, requested string) string {
	if seg := sanitizeBranchSegment(requested); seg != "" {
		return seg
	}
	seg := sanitizeBranchSegment(specID)
	if seg == "" {
		seg = "task"
	}
	prefix := deliveryBranchPrefix()
	if prefix == "" {
		return seg
	}
	return prefix + "/" + seg
}

// gitIdentity is the committer OPM uses. Never a real person, and never a
// co-author trailer: the commit records the automation that made it.
func gitIdentity() (name, email string) {
	return envOr("OPM_GIT_AUTHOR_NAME", "opm-api"),
		envOr("OPM_GIT_AUTHOR_EMAIL", "opm-api@localhost")
}

// commitOutcome is what a successful commit produced.
type commitOutcome struct {
	Branch  string
	Sha     string
	Staged  []string
	Message string
}

// gitCommitOnBranch creates the branch, stages exactly the given paths and
// commits. It never runs `git add -A`: only paths the apply step actually wrote
// are staged, so unrelated workspace noise cannot ride along.
func gitCommitOnBranch(g *gitRunner, branch, message string, paths []string) (commitOutcome, string, error) {
	out := commitOutcome{Branch: branch, Message: message}
	if len(paths) == 0 {
		return out, deliveryStatusNoChanges, fmt.Errorf("nothing to stage")
	}
	if _, err := g.run("checkout", "-b", branch); err != nil {
		return out, deliveryStatusGitFailed, err
	}
	name, email := gitIdentity()
	args := append([]string{"add", "--"}, paths...)
	if _, err := g.run(args...); err != nil {
		return out, deliveryStatusGitFailed, err
	}
	out.Staged = paths

	// Nothing staged means the change set matched HEAD exactly. Report that
	// rather than creating an empty commit.
	if diff, err := g.run("diff", "--cached", "--name-only"); err == nil && strings.TrimSpace(diff) == "" {
		return out, deliveryStatusNoChanges,
			fmt.Errorf("the change set is identical to the current branch — nothing to commit")
	}

	if _, err := g.run("-c", "user.name="+name, "-c", "user.email="+email,
		"commit", "--no-verify", "-m", message); err != nil {
		return out, deliveryStatusGitFailed, err
	}
	sha, err := g.run("rev-parse", "HEAD")
	if err != nil {
		return out, deliveryStatusGitFailed, err
	}
	out.Sha = strings.TrimSpace(sha)
	return out, "", nil
}

// gitPushBranch pushes the branch to the given remote URL using the credential
// already armed on the runner. A non-fast-forward rejection is reported as
// push_rejected so the operator can retry onto a fresh branch instead of the
// delivery silently claiming success.
func gitPushBranch(g *gitRunner, remoteURL, branch string) (string, string, error) {
	if strings.TrimSpace(remoteURL) == "" {
		return "", deliveryStatusPushRejected, fmt.Errorf("no push URL available")
	}
	out, err := g.run("push", remoteURL, "refs/heads/"+branch+":refs/heads/"+branch)
	if err != nil {
		low := strings.ToLower(out)
		switch {
		case strings.Contains(low, "non-fast-forward") || strings.Contains(low, "rejected"),
			strings.Contains(low, "fetch first"):
			return out, deliveryStatusPushRejected, fmt.Errorf(
				"push rejected — the branch %s already exists on the remote with different commits: %s",
				branch, strings.TrimSpace(truncateRunes(out, 300)))
		case strings.Contains(low, "403") || strings.Contains(low, "permission") ||
			strings.Contains(low, "authentication failed") || strings.Contains(low, "denied"):
			return out, deliveryStatusMissingPushPermission, fmt.Errorf(
				"push refused by the remote — the credential lacks write access: %s",
				strings.TrimSpace(truncateRunes(out, 300)))
		}
		return out, deliveryStatusPushFailed, err
	}
	return out, "", nil
}

// workspaceIsStub reports whether prepareJobWorkspace produced the credential-less
// placeholder instead of a real clone. Committing into it would be meaningless.
func workspaceIsStub(workDir string) bool {
	if _, err := os.Stat(filepath.Join(workDir, ".git")); err != nil {
		return true
	}
	return false
}
