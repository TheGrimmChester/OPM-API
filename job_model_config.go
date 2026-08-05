package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
)

// The model configuration one job runs with.
//
// Two sources, and which one applies is decided by a single switch —
// PEER_OAM_URL:
//
//   - OAM configured: the control plane resolves the model AND the key for this
//     job's agent phase, scoped to the acting user's org (and their personal
//     override, if any). No environment variable participates.
//   - OAM not configured: the pre-OAM environment path, byte-for-byte. A
//     deployment that has not adopted OAM behaves exactly as it did.
//
// There is deliberately no third path where a resolve failure silently falls back
// to the environment. Once a deployment has adopted OAM, running a job with the
// wrong credentials is worse than not running it — the env key belongs to the
// deployment, not to the user who asked.
type jobModelConfig struct {
	Provider string
	Model    string
	BaseURL  string
	APIKey   string

	// Where the model came from (OAM's layer name) and which credential scope
	// won. Recorded on the job; never the key.
	ModelSource string
	KeyScope    string

	// FromOAM distinguishes a resolved config from the legacy env path.
	FromOAM bool

	// Candidates is the ordered endpoint list the runner walks. Empty means the
	// single {Provider, Model, BaseURL, APIKey} above is all there is, which is the
	// legacy path and what an older OAM returns.
	Candidates []resolvedEndpointCandidate
}

// hasKey reports whether a model-backed run is possible at all.
func (c jobModelConfig) hasKey() bool { return strings.TrimSpace(c.APIKey) != "" }

// resolveJobModelConfig produces the model config for a job.
//
// A returned error means the job must not proceed in model mode: the caller
// surfaces it rather than degrading to a different credential.
func resolveJobModelConfig(ctx context.Context, j Job) (jobModelConfig, error) {
	if !oamConfigured() {
		return legacyEnvModelConfig(j.Action), nil
	}

	agentKey := strings.TrimSpace(j.AgentKey)
	if agentKey == "" {
		agentKey = agentKeyForAction(j.Action)
	}
	if agentKey == "" {
		// An action with no AI phase (delivery, sync). Nothing to resolve, and
		// nothing should be handed a credential.
		return jobModelConfig{}, nil
	}

	override := j.ModelOverride
	// A job carrying a pinned binding re-sends it as the override, so the chain
	// keeps one model across the coding subtasks of a task even if org config
	// changes underneath. The KEY is always fetched fresh — it is never persisted
	// on the job, and a rotation must take effect immediately.
	if j.ResolvedAgentKey == agentKey && strings.TrimSpace(j.ResolvedModel) != "" {
		override = &modelOverride{Provider: j.ResolvedProvider, Model: j.ResolvedModel}
	}

	b, err := resolveAgentFromOAM(ctx, j.OrganizationID, j.ProjectID, j.ActorUsername, agentKey, override)
	if err != nil {
		return jobModelConfig{}, err
	}
	return jobModelConfig{
		Provider:    b.Provider,
		Model:       b.Model,
		BaseURL:     b.BaseURL,
		APIKey:      b.APIKey,
		ModelSource: b.ModelSource,
		KeyScope:    b.KeyScope,
		FromOAM:     true,
		Candidates:  b.Candidates,
	}, nil
}

// legacyEnvModelConfig is the pre-OAM path: one deployment-wide key and the
// OPM_MODEL_<PHASE> environment variables.
//
// Retained only so a deployment without PEER_OAM_URL is unchanged. It is the thing
// OAM replaces: every org, project and user shares this one key and this one model
// set, which is why nobody could choose their own.
//
// It resolves the model through modelForAction, NOT a bare OPM_MODEL read. That
// matters: a deployment with OPM_MODEL_CODING set to something other than
// OPM_MODEL would otherwise silently lose its per-phase choice the moment this
// code path replaced the old one — a regression in the very behaviour this work is
// supposed to extend.
func legacyEnvModelConfig(action string) jobModelConfig {
	key := strings.TrimSpace(os.Getenv("OPM_MODEL_API_KEY"))
	if key == "" {
		key = strings.TrimSpace(os.Getenv("CURSOR_API_KEY"))
	}
	return jobModelConfig{
		Provider:    envOr("OPM_MODEL_PROVIDER", ""),
		Model:       modelForAction(action),
		BaseURL:     envOr("OPM_MODEL_BASE_URL", ""),
		APIKey:      key,
		ModelSource: "env",
	}
}

// recordJobResolution persists what the job actually resolved, so "which model
// ran this, as whom" is answerable later and so the next job in the chain can
// inherit the pin. The API key is never written.
func recordJobResolution(store *Store, j Job, cfg jobModelConfig, agentKey string) Job {
	if !cfg.FromOAM {
		return j
	}
	j.ResolvedProvider = cfg.Provider
	j.ResolvedModel = cfg.Model
	j.ResolvedModelSource = cfg.ModelSource
	j.ResolvedKeyScope = cfg.KeyScope
	j.ResolvedAgentKey = agentKey
	if store != nil {
		if err := store.UpdateJob(j.ProjectID, j); err != nil {
			log.Printf("job %s: could not record resolution: %v", j.RunID, err)
		}
	}
	return j
}

// modelEnvForJob is the environment a runner receives for its model call.
//
// Only ONE model is passed. The pre-OAM code handed the runner all six
// OPM_MODEL_<PHASE> variables and let it pick, which meant the runner re-derived
// a decision the control plane had already made — and meant every phase's model
// travelled into every container. The control plane decides; the runner obeys.
func modelEnvForJob(cfg jobModelConfig) map[string]string {
	env := map[string]string{}
	if m := strings.TrimSpace(cfg.Model); m != "" {
		env["OPM_MODEL"] = m
	}
	if p := strings.TrimSpace(cfg.Provider); p != "" {
		env["OPM_MODEL_PROVIDER"] = p
	}
	if u := strings.TrimSpace(cfg.BaseURL); u != "" {
		env["OPM_MODEL_BASE_URL"] = u
	}
	return env
}

// candidateEnvLines renders the resolved endpoint list as numbered environment
// variables for the runner's --env-file.
//
// Numbered variables rather than a JSON blob, because the credentials have to
// travel through the env file anyway (ScrubEnv strips API_KEY from -e flags, and
// an env file stays out of the process table). Splitting configuration into JSON
// and keys into env would let the two arrive out of step — a candidate list
// pointing at endpoints whose keys were not written.
//
// With no candidates this returns nothing and the runner falls back to the single
// OPM_MODEL_API_KEY / OPM_MODEL* pair, which is the legacy contract.
func candidateEnvLines(cfg jobModelConfig) []string {
	if len(cfg.Candidates) == 0 {
		return nil
	}
	out := []string{fmt.Sprintf("OPM_MODEL_CANDIDATE_COUNT=%d", len(cfg.Candidates))}
	for i, c := range cfg.Candidates {
		p := fmt.Sprintf("OPM_MODEL_%d_", i+1)
		add := func(suffix, v string) {
			if strings.TrimSpace(v) == "" {
				return
			}
			// A newline in any of these would forge additional env entries in the
			// file — including overwriting a credential. The values come from
			// OAM's validated endpoint rows, so this is belt and braces rather
			// than a known hole, but the file format has no escaping at all.
			out = append(out, p+suffix+"="+sanitizeEnvValue(v))
		}
		add("ENDPOINT_ID", c.EndpointID)
		add("KIND", c.Kind)
		add("TRANSPORT", c.Transport)
		add("PROVIDER", c.Provider)
		add("BASE_URL", c.BaseURL)
		add("MODEL", c.Model)
		add("CLI_COMMAND", c.CLICommand)
		add("CLI_ARGS", strings.Join(c.CLIArgs, " "))
		add("API_KEY", c.APIKey)
	}
	return out
}

// sanitizeEnvValue strips the characters that would break out of one env-file line.
func sanitizeEnvValue(v string) string {
	v = strings.ReplaceAll(v, "\r", "")
	v = strings.ReplaceAll(v, "\n", "")
	return v
}
