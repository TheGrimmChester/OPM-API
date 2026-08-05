package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// clearCandidateEnv removes every candidate variable so one test's fixture cannot
// leak into the next.
func clearCandidateEnv(t *testing.T) {
	t.Helper()
	for _, e := range os.Environ() {
		k := strings.SplitN(e, "=", 2)[0]
		if strings.HasPrefix(k, "OPM_MODEL_") || k == "CURSOR_API_KEY" {
			t.Setenv(k, "")
		}
	}
}

func TestLoadCandidatesReadsTheNumberedList(t *testing.T) {
	clearCandidateEnv(t)
	t.Setenv("OPM_MODEL_CANDIDATE_COUNT", "2")
	t.Setenv("OPM_MODEL_1_ENDPOINT_ID", "cursor-personal")
	t.Setenv("OPM_MODEL_1_KIND", "cli_cursor")
	t.Setenv("OPM_MODEL_1_MODEL", "auto")
	t.Setenv("OPM_MODEL_1_API_KEY", "crsr_one")
	t.Setenv("OPM_MODEL_2_ENDPOINT_ID", "openrouter")
	t.Setenv("OPM_MODEL_2_KIND", "api_openai")
	t.Setenv("OPM_MODEL_2_BASE_URL", "https://openrouter.ai/api/v1/")
	t.Setenv("OPM_MODEL_2_MODEL", "gpt-4o")
	t.Setenv("OPM_MODEL_2_API_KEY", "sk-or-two")

	got := loadCandidates()
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2", len(got))
	}
	if got[0].EndpointID != "cursor-personal" || got[0].APIKey != "crsr_one" {
		t.Fatalf("candidate 1 = %+v", got[0])
	}
	if got[1].Kind != "api_openai" || got[1].Model != "gpt-4o" {
		t.Fatalf("candidate 2 = %+v", got[1])
	}
	// The trailing slash is trimmed here rather than at every call site, so
	// base+"/v1/messages" cannot become a double slash.
	if got[1].BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("base url not trimmed: %q", got[1].BaseURL)
	}
	if got[0].Index != 1 || got[1].Index != 2 {
		t.Fatalf("indices = %d,%d — the log line names the wrong attempt", got[0].Index, got[1].Index)
	}
}

// A runner image deployed against a control plane that does not send candidates —
// or a deployment with PEER_OAM_URL unset — must behave exactly as it did before.
func TestNoCandidateVarsFallsBackToTheLegacyEnv(t *testing.T) {
	clearCandidateEnv(t)
	t.Setenv("CURSOR_API_KEY", "crsr_legacy")
	t.Setenv("OPM_MODEL", "auto")

	got := loadCandidates()
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want the single legacy one", len(got))
	}
	if got[0].Kind != kindCLICursor || got[0].APIKey != "crsr_legacy" || got[0].Model != "auto" {
		t.Fatalf("legacy candidate = %+v", got[0])
	}
}

func TestLegacyOpenAIProviderMapsToTheOpenAIKind(t *testing.T) {
	clearCandidateEnv(t)
	t.Setenv("OPM_MODEL_API_KEY", "sk-legacy")
	t.Setenv("OPM_MODEL_PROVIDER", "openai")
	t.Setenv("OPM_MODEL_BASE_URL", "https://api.example.com/v1")
	t.Setenv("OPM_MODEL", "gpt-4o")

	got := loadCandidates()
	if len(got) != 1 || got[0].Kind != kindAPIOpenAI {
		t.Fatalf("candidates = %+v", got)
	}
	if got[0].BaseURL != "https://api.example.com/v1" {
		t.Fatalf("base url = %q", got[0].BaseURL)
	}
}

// No credential anywhere is not a failover case — it is the fallback-to-builtin
// case, and the runner must be able to tell them apart.
func TestNoCredentialAtAllYieldsNoCandidates(t *testing.T) {
	clearCandidateEnv(t)
	if got := loadCandidates(); len(got) != 0 {
		t.Fatalf("got %d candidates with no credential set: %+v", len(got), got)
	}
}

// A truncated env file would otherwise produce a candidate the runner tries with
// an empty key, which fails as an auth error rather than being skipped.
func TestCandidateWithoutAKeyIsSkipped(t *testing.T) {
	clearCandidateEnv(t)
	t.Setenv("OPM_MODEL_CANDIDATE_COUNT", "2")
	t.Setenv("OPM_MODEL_1_KIND", "cli_cursor")
	// candidate 1 has no API key
	t.Setenv("OPM_MODEL_2_KIND", "cli_cursor")
	t.Setenv("OPM_MODEL_2_API_KEY", "crsr_two")

	got := loadCandidates()
	if len(got) != 1 || got[0].APIKey != "crsr_two" {
		t.Fatalf("candidates = %+v", got)
	}
}

// Guessing at an unknown kind is the dangerous option: defaulting to the Cursor
// CLI would hand a Claude Code credential to the Cursor binary.
func TestUnknownKindIsNotRunnable(t *testing.T) {
	if candidateRunnable(candidate{Kind: "quantum_api", APIKey: "x"}) {
		t.Fatal("an unrecognised kind must not be attempted")
	}
	for _, k := range []string{kindAPIOpenAI, kindAPIAnthropic, kindCLICursor, kindCLIClaudeCode, kindCLIGeneric} {
		if !candidateRunnable(candidate{Kind: k, APIKey: "x"}) {
			t.Fatalf("kind %q should be runnable", k)
		}
	}
	// An older OAM sends only `provider`.
	if !candidateRunnable(candidate{Provider: "openai", APIKey: "x"}) {
		t.Fatal("a provider-only candidate must still be runnable")
	}
	if candidateRunnable(candidate{APIKey: "x"}) {
		t.Fatal("a candidate with neither kind nor provider is not runnable")
	}
}

func TestQuotaLikeRecognisesThrottling(t *testing.T) {
	for _, msg := range []string{
		"HTTP 429: Too Many Requests",
		"insufficient_quota: you exceeded your current quota",
		"rate limit reached for gpt-4o",
		"billing hard limit reached",
	} {
		if !quotaLike(errors.New(msg)) {
			t.Fatalf("not recognised as quota/throttling: %q", msg)
		}
	}
	if quotaLike(errors.New("HTTP 500: internal error")) {
		t.Fatal("a 500 is not a quota problem — the log line would mislead")
	}
	if quotaLike(nil) {
		t.Fatal("nil is not a quota failure")
	}
}

// Erring toward retryable is the right default: the cost of trying the next
// endpoint is one attempt, while the cost of stopping on a transient fault is a
// failed job. The exceptions are failures that recur identically everywhere.
func TestRetryableClassification(t *testing.T) {
	for _, msg := range []string{
		"HTTP 429: rate limited",
		"HTTP 503: upstream unavailable",
		"dial tcp: connection refused",
		"HTTP 401: invalid api key",
		"HTTP 404: model not found",
	} {
		if !retryable(errors.New(msg)) {
			t.Fatalf("should move to the next endpoint: %q", msg)
		}
	}
	for _, msg := range []string{
		"context canceled",
		"invalid prompt: too long",
	} {
		if retryable(errors.New(msg)) {
			t.Fatalf("walking the list just repeats this error: %q", msg)
		}
	}
	if retryable(nil) {
		t.Fatal("nil is not a failure")
	}
}

// The failover loop itself: first success wins, and the attempt log names every
// endpoint tried.
func TestCallWithFailoverAdvancesPastAFailure(t *testing.T) {
	var tried []string
	restore := stubCandidateCaller(func(c candidate, system, user string) (string, error) {
		tried = append(tried, c.EndpointID)
		if c.EndpointID == "first" {
			return "", errors.New("HTTP 429: quota exceeded")
		}
		return "ok from " + c.EndpointID, nil
	})
	defer restore()

	cands := []candidate{
		{Index: 1, EndpointID: "first", Kind: kindCLICursor, APIKey: "a"},
		{Index: 2, EndpointID: "second", Kind: kindCLICursor, APIKey: "b"},
	}
	out, used, err := callWithFailover(cands, "sys", "usr")
	if err != nil {
		t.Fatalf("callWithFailover: %v", err)
	}
	if out != "ok from second" || used.EndpointID != "second" {
		t.Fatalf("out=%q used=%q", out, used.EndpointID)
	}
	if strings.Join(tried, ",") != "first,second" {
		t.Fatalf("tried = %v, want first then second", tried)
	}
}

// Total failure must name every attempt: "all three refused" and "two were over
// quota and the third has a bad key" call for different fixes.
func TestCallWithFailoverReportsEveryAttempt(t *testing.T) {
	restore := stubCandidateCaller(func(c candidate, system, user string) (string, error) {
		return "", fmt.Errorf("HTTP 429: %s is out of quota", c.EndpointID)
	})
	defer restore()

	cands := []candidate{
		{Index: 1, EndpointID: "one", Kind: kindCLICursor, APIKey: "a"},
		{Index: 2, EndpointID: "two", Kind: kindCLICursor, APIKey: "b"},
	}
	_, _, err := callWithFailover(cands, "sys", "usr")
	if err == nil {
		t.Fatal("expected an error when every endpoint fails")
	}
	for _, want := range []string{"one", "two", "over quota"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error does not mention %q: %v", want, err)
		}
	}
}

// A credential must never reach the error chain, which is logged and persisted as
// a job failure reason.
func TestFailoverErrorNeverCarriesACredential(t *testing.T) {
	restore := stubCandidateCaller(func(c candidate, system, user string) (string, error) {
		// A provider echoing the key back in its error message is a real thing.
		return "", fmt.Errorf("HTTP 401: invalid key %s", c.APIKey)
	})
	defer restore()

	cands := []candidate{{Index: 1, EndpointID: "one", Kind: kindCLICursor, APIKey: "crsr_super_secret"}}
	_, _, err := callWithFailover(cands, "sys", "usr")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "crsr_super_secret") {
		t.Fatalf("the credential leaked into the error: %v", err)
	}
	if !strings.Contains(err.Error(), "***") {
		t.Fatalf("expected the key to be redacted: %v", err)
	}
}

// A non-retryable failure stops immediately rather than repeating the same error
// against every endpoint.
func TestNonRetryableFailureStopsTheWalk(t *testing.T) {
	calls := 0
	restore := stubCandidateCaller(func(c candidate, system, user string) (string, error) {
		calls++
		return "", errors.New("invalid prompt: too long")
	})
	defer restore()

	cands := []candidate{
		{Index: 1, EndpointID: "one", Kind: kindCLICursor, APIKey: "a"},
		{Index: 2, EndpointID: "two", Kind: kindCLICursor, APIKey: "b"},
	}
	if _, _, err := callWithFailover(cands, "s", "u"); err == nil {
		t.Fatal("expected an error")
	}
	if calls != 1 {
		t.Fatalf("made %d attempts; a non-retryable error must stop after the first", calls)
	}
}

// An unsupported kind is skipped and the walk continues, so a newer OAM adding a
// kind cannot break jobs that also have a runnable endpoint configured.
func TestUnsupportedKindIsSkippedNotFatal(t *testing.T) {
	restore := stubCandidateCaller(func(c candidate, system, user string) (string, error) {
		return "ok from " + c.EndpointID, nil
	})
	defer restore()

	cands := []candidate{
		{Index: 1, EndpointID: "future", Kind: "quantum_api", APIKey: "a"},
		{Index: 2, EndpointID: "known", Kind: kindCLICursor, APIKey: "b"},
	}
	out, used, err := callWithFailover(cands, "s", "u")
	if err != nil {
		t.Fatalf("callWithFailover: %v", err)
	}
	if used.EndpointID != "known" || out != "ok from known" {
		t.Fatalf("used=%q out=%q", used.EndpointID, out)
	}
}

func TestNoCandidatesIsADistinctError(t *testing.T) {
	_, _, err := callWithFailover(nil, "s", "u")
	if err == nil || !strings.Contains(err.Error(), "no model candidate") {
		t.Fatalf("err = %v", err)
	}
}
