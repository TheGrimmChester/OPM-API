package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// The Anthropic Messages path, plus the two agent-CLI drivers.
//
// Before this the runner could reach exactly two things: an OpenAI-compatible
// endpoint over HTTP, and the Cursor CLI. There was no Anthropic HTTP path at all,
// so an org holding an Anthropic key could configure a binding that no runner
// could execute.
//
// Raw HTTP rather than the Anthropic SDK is deliberate here. This function's job
// is to call an *Anthropic-compatible* endpoint — the official API, but equally a
// self-hosted proxy or a gateway — and the request shape is the contract those
// implement. It also keeps the runner image dependency-free, which matters because
// it builds on golang:1.22-alpine and ships into every job container. The sibling
// callChat does the same for the OpenAI shape.

// anthropicVersion is the API version header the Messages API requires. Sent for
// compatible endpoints too: proxies that mirror the API accept it, and one that
// rejects it was never Anthropic-compatible.
const anthropicVersion = "2023-06-01"

type anthropicReq struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// anthropicDefaultMaxTokens is required by the API — unlike the OpenAI shape,
// max_tokens is not optional, so a missing value is a 400 rather than a default.
const anthropicDefaultMaxTokens = 8192

func callAnthropic(base, key, model, system, user string) (string, error) {
	maxTokens := anthropicDefaultMaxTokens
	if n := envInt("OPM_MODEL_MAX_TOKENS", 0); n > 0 {
		maxTokens = n
	}
	body, _ := json.Marshal(anthropicReq{
		Model:     model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  []anthropicMessage{{Role: "user", Content: user}},
	})
	req, err := http.NewRequest(http.MethodPost, base+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	// x-api-key, not a Bearer token: that is the Messages API's scheme, and sending
	// the wrong one is a 401 that looks like a bad key.
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", anthropicVersion)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The status code is kept in the message because retryable()/quotaLike()
		// classify on it — a 429 here is what triggers the move to the next
		// endpoint.
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(raw), 240))
	}
	var ar anthropicResp
	if err := json.Unmarshal(raw, &ar); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if ar.Error != nil && ar.Error.Message != "" {
		return "", fmt.Errorf("%s: %s", ar.Error.Type, ar.Error.Message)
	}
	// Concatenate every text block rather than taking the first: a response split
	// across blocks would otherwise be silently truncated to its opening fragment.
	var sb strings.Builder
	for _, block := range ar.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	out := strings.TrimSpace(sb.String())
	if out == "" {
		return "", fmt.Errorf("empty content (stop_reason=%s)", ar.StopReason)
	}
	return out, nil
}

// callClaudeCode drives the Claude Code CLI in non-interactive mode.
func callClaudeCode(c candidate, system, user string) (string, error) {
	bin := c.CLICommand
	if bin == "" {
		bin = envOr("OPM_CLAUDE_CODE_BIN", "claude")
	}
	if _, err := exec.LookPath(bin); err != nil {
		return "", fmt.Errorf("claude code binary %q not found in PATH (rebuild the runner image with it)", bin)
	}
	args := []string{"-p", "--output-format", "text"}
	if m := strings.TrimSpace(c.Model); m != "" && m != "auto" {
		args = append(args, "--model", m)
	}
	args = append(args, c.CLIArgs...)
	return runAgentCLI(bin, args, system+"\n\n"+user, "ANTHROPIC_API_KEY="+c.APIKey, c.APIKey)
}

// callGenericCLI drives an arbitrary configured agent CLI.
//
// The prompt goes on stdin rather than as an argument: an argv-passed prompt shows
// up in the container's process table, and prompts here carry task descriptions
// and repository context.
//
// The credential is passed as the env var the endpoint names, defaulting to
// AGENT_API_KEY. It is never interpolated into the argument template, for the same
// process-table reason.
func callGenericCLI(c candidate, system, user string) (string, error) {
	bin := c.CLICommand
	if bin == "" {
		return "", fmt.Errorf("endpoint %s is kind cli_generic but names no command", c.name())
	}
	if _, err := exec.LookPath(bin); err != nil {
		return "", fmt.Errorf("agent binary %q not found in PATH", bin)
	}
	keyEnv := envOr("OPM_GENERIC_CLI_KEY_ENV", "AGENT_API_KEY")
	return runAgentCLI(bin, c.CLIArgs, system+"\n\n"+user, keyEnv+"="+c.APIKey, c.APIKey)
}

// runAgentCLI is the shared CLI driver: prompt on stdin, credential in one env
// var, and the credential scrubbed from whatever comes back.
//
// The child gets os.Environ() plus the one key. That inheritance is the same
// pattern callCursorAgent already uses; the credential-scrubbing on output is the
// part that matters, because an agent CLI that echoes its configuration would
// otherwise write the key into the job's captured output.
func runAgentCLI(bin string, args []string, prompt, keyEnv, key string) (string, error) {
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), keyEnv)
	cmd.Stdin = strings.NewReader(prompt)
	if repoDir := strings.TrimSpace(os.Getenv("OPM_REPO_DIR")); repoDir != "" {
		if st, err := os.Stat(repoDir); err == nil && st.IsDir() {
			cmd.Dir = repoDir
		}
	}
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if key != "" {
		text = strings.ReplaceAll(text, key, "***")
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w (%s)", bin, err, truncate(text, 240))
	}
	if text == "" {
		return "", fmt.Errorf("%s returned empty output", bin)
	}
	return text, nil
}
