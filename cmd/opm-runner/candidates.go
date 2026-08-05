package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Endpoint candidates and failover.
//
// OAM resolves a job's model together with an ordered list of endpoints it may
// use, each with its own credential. The runner walks that list: try the first,
// and on a *retryable* failure move to the next.
//
// Before this the dispatch was a two-way branch — `useOpenAIProvider()` picked
// OpenAI-over-HTTP and everything else shelled out to the Cursor CLI — with one
// key carried under both OPM_MODEL_API_KEY and CURSOR_API_KEY. That could not
// express a second Cursor account, an unofficial OpenAI-compatible endpoint
// alongside the official one, an Anthropic HTTP call at all, or any other agent
// CLI.
//
// The candidates arrive as numbered environment variables rather than JSON,
// because the credential half already travels through a 0600 --env-file (see
// writeModelEnvFile in spawn_container.go): keeping key and configuration in the
// same transport means they cannot arrive out of step.

// endpoint kinds, mirroring OAM's endpoint_kinds.go. Duplicated deliberately
// rather than shared: OPM must keep running against an OAM that has learned a new
// kind, and a kind this build does not know is skipped (see candidateRunnable)
// instead of failing the job.
const (
	kindAPIOpenAI     = "api_openai"
	kindAPIAnthropic  = "api_anthropic"
	kindCLICursor     = "cli_cursor"
	kindCLIClaudeCode = "cli_claude_code"
	kindCLIGeneric    = "cli_generic"
)

type candidate struct {
	Index      int
	EndpointID string
	Kind       string
	Transport  string
	Provider   string
	BaseURL    string
	Model      string
	CLICommand string
	CLIArgs    []string
	APIKey     string
}

func (c candidate) name() string {
	if c.EndpointID != "" {
		return c.EndpointID
	}
	return "(unregistered " + c.Provider + ")"
}

// candidateRunnable reports whether this build knows how to drive the endpoint.
//
// An unknown kind is skipped rather than guessed at. Guessing is the dangerous
// option: falling back to the Cursor CLI for an unrecognised kind would hand a
// Claude Code credential to the Cursor binary, which is both a failed job and a
// credential sent somewhere it was not issued for.
func candidateRunnable(c candidate) bool {
	switch c.Kind {
	case kindAPIOpenAI, kindAPIAnthropic, kindCLICursor, kindCLIClaudeCode, kindCLIGeneric:
		return true
	case "":
		// No kind at all means an older OAM that only sent `provider`. Fall back
		// to the provider name, which is the pre-registry contract.
		return c.Provider != ""
	default:
		return false
	}
}

// loadCandidates reads the numbered candidate env vars.
//
// OPM_MODEL_CANDIDATE_COUNT bounds the loop, and each index i carries
// OPM_MODEL_<i>_KIND / _PROVIDER / _MODEL / _BASE_URL / _API_KEY / _ENDPOINT_ID /
// _CLI_COMMAND / _CLI_ARGS.
//
// With no candidate variables set, one candidate is synthesised from the legacy
// OPM_MODEL* / CURSOR_API_KEY environment — so a runner image deployed against an
// older control plane, or a deployment with PEER_OAM_URL unset, behaves exactly as
// it did before.
func loadCandidates() []candidate {
	n, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("OPM_MODEL_CANDIDATE_COUNT")))
	out := make([]candidate, 0, n)
	for i := 1; i <= n; i++ {
		p := fmt.Sprintf("OPM_MODEL_%d_", i)
		key := strings.TrimSpace(os.Getenv(p + "API_KEY"))
		if key == "" {
			// A candidate with no credential cannot be tried. OAM already drops
			// these, so reaching here means the env file was truncated.
			continue
		}
		c := candidate{
			Index:      i,
			EndpointID: strings.TrimSpace(os.Getenv(p + "ENDPOINT_ID")),
			Kind:       strings.ToLower(strings.TrimSpace(os.Getenv(p + "KIND"))),
			Transport:  strings.ToLower(strings.TrimSpace(os.Getenv(p + "TRANSPORT"))),
			Provider:   strings.ToLower(strings.TrimSpace(os.Getenv(p + "PROVIDER"))),
			BaseURL:    strings.TrimRight(strings.TrimSpace(os.Getenv(p+"BASE_URL")), "/"),
			Model:      strings.TrimSpace(os.Getenv(p + "MODEL")),
			CLICommand: strings.TrimSpace(os.Getenv(p + "CLI_COMMAND")),
			APIKey:     key,
		}
		if raw := strings.TrimSpace(os.Getenv(p + "CLI_ARGS")); raw != "" {
			c.CLIArgs = strings.Fields(raw)
		}
		out = append(out, c)
	}
	if len(out) > 0 {
		return out
	}
	return legacyCandidates()
}

// legacyCandidates rebuilds the single pre-registry candidate.
func legacyCandidates() []candidate {
	key := modelAPIKey()
	if key == "" {
		return nil
	}
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("OPM_MODEL_PROVIDER")))
	kind := kindCLICursor
	if provider == "openai" {
		kind = kindAPIOpenAI
	} else if provider == "anthropic" {
		kind = kindAPIAnthropic
	}
	return []candidate{{
		Index:    1,
		Kind:     kind,
		Provider: provider,
		BaseURL:  strings.TrimRight(strings.TrimSpace(os.Getenv("OPM_MODEL_BASE_URL")), "/"),
		Model:    envOr("OPM_MODEL", "auto"),
		APIKey:   key,
	}}
}

// retryable classifies a failure as "try the next endpoint" or "stop".
//
// Erring toward retryable is the right default here: the cost of moving to the
// next endpoint is one more attempt, while the cost of stopping on a transient
// fault is a failed job. The exceptions are failures that will recur identically
// on every endpoint — a malformed prompt, a cancelled context — where walking the
// whole list just multiplies the same error.
//
// Quota and rate limits are the case this feature exists for, so they are matched
// on message text as well as status: a 200 response whose body says "quota
// exceeded" is a real thing that upstream proxies do.
func retryable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, stop := range []string{
		"context canceled",
		"context deadline exceeded: prompt",
		"invalid prompt",
	} {
		if strings.Contains(msg, stop) {
			return false
		}
	}
	return true
}

// quotaLike reports whether a failure looks like exhausted quota or throttling,
// so the log line says which of the two kinds of failover happened. An operator
// reading "endpoint 1 was over quota" and one reading "endpoint 1 was unreachable"
// take different actions.
func quotaLike(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, sig := range []string{
		"429", "quota", "rate limit", "rate_limit",
		"insufficient_quota", "too many requests", "billing",
	} {
		if strings.Contains(msg, sig) {
			return true
		}
	}
	return false
}

// candidateCaller is the dispatch seam. Production points at callCandidate; tests
// substitute a stub so the *walk* — advance on retryable, stop otherwise, redact
// the key, name every attempt — can be asserted without a network or a CLI
// binary. The walk is the part worth testing: whether it advances correctly is
// what decides if a job survives a 429.
var candidateCaller = callCandidate

// stubCandidateCaller swaps the dispatch and returns a restore func.
func stubCandidateCaller(fn func(candidate, string, string) (string, error)) func() {
	prev := candidateCaller
	candidateCaller = fn
	return func() { candidateCaller = prev }
}

// callWithFailover walks the candidates in order and returns the first success.
//
// The returned error, on total failure, names every attempt. A single wrapped
// error would hide the shape of the problem: "all three endpoints refused" and
// "the first two were over quota and the third has a bad key" call for very
// different fixes.
func callWithFailover(cands []candidate, system, user string) (string, candidate, error) {
	if len(cands) == 0 {
		return "", candidate{}, fmt.Errorf("no model candidate available (no credential resolved)")
	}
	var attempts []string
	for _, c := range cands {
		if !candidateRunnable(c) {
			attempts = append(attempts, fmt.Sprintf("%s: unsupported kind %q by this runner", c.name(), c.Kind))
			continue
		}
		out, err := candidateCaller(c, system, user)
		if err == nil {
			return out, c, nil
		}
		// The candidate's own key must never reach a log line or the error chain.
		safe := strings.ReplaceAll(err.Error(), c.APIKey, "***")
		why := "failed"
		if quotaLike(err) {
			why = "over quota / throttled"
		}
		attempts = append(attempts, fmt.Sprintf("%s (%s): %s — %s", c.name(), c.Kind, why, truncate(safe, 200)))
		if !retryable(err) {
			return "", c, fmt.Errorf("model call failed and is not retryable:\n  %s", strings.Join(attempts, "\n  "))
		}
		fmt.Fprintf(os.Stderr, "opm-runner: candidate %d/%d %s %s, trying the next endpoint\n",
			c.Index, len(cands), c.name(), why)
	}
	return "", candidate{}, fmt.Errorf("all %d model endpoints failed:\n  %s",
		len(cands), strings.Join(attempts, "\n  "))
}

// callCandidate drives one endpoint, dispatching on its kind.
func callCandidate(c candidate, system, user string) (string, error) {
	switch c.Kind {
	case kindAPIOpenAI:
		base := c.BaseURL
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		return callChat(base, c.APIKey, c.Model, system, user)
	case kindAPIAnthropic:
		base := c.BaseURL
		if base == "" {
			base = "https://api.anthropic.com"
		}
		return callAnthropic(base, c.APIKey, c.Model, system, user)
	case kindCLIClaudeCode:
		return callClaudeCode(c, system, user)
	case kindCLIGeneric:
		return callGenericCLI(c, system, user)
	case kindCLICursor:
		return callCursorAgent(c.APIKey, c.Model, system, user)
	default:
		// Kind absent: an older OAM sent only `provider`.
		if c.Provider == "openai" {
			base := c.BaseURL
			if base == "" {
				base = "https://api.openai.com/v1"
			}
			return callChat(base, c.APIKey, c.Model, system, user)
		}
		return callCursorAgent(c.APIKey, c.Model, system, user)
	}
}
