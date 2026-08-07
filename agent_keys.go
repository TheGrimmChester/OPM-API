package main

import "strings"

// Action → agent key. This mapping is OPM's and lives here on purpose.
//
// OAM stores per-agent model bindings under a canonical agent key and refuses to
// derive that key from a job action, because the derivation is not a string
// transform: `run-implementation` is the **coding** phase, and both
// `run-planning` and `run-followup-planning` are **planning**. A prefix-stripping
// rule in the config service would look for an "implementation" binding, miss the
// stored "coding" one, and silently fall back to a default — the wrong model
// chosen quietly, which is exactly what per-agent configuration exists to stop.
//
// The keys below match the AI phases OPM still owns (planning, coding, ideation,
// roadmap). Deep review / QA-fix live in ORA — they are not OPM agent keys.
const (
	agentKeyPlanning          = "planning"
	agentKeyCoding            = "coding"
	agentKeyIdeation          = "ideation"
	agentKeyRoadmapDiscovery  = "roadmap_discovery"
	agentKeyRoadmapFeatures  = "roadmap_features"
	agentKeyRoadmapCompetitor = "roadmap_competitor"
)

// agentKeyForAction returns the canonical agent key for a job action, or "" when
// the action has no AI phase.
//
// An empty result means "do not resolve a model for this job" — the job runs as
// it always has. That keeps actions nobody has classified (delivery, sync) on
// their current path instead of silently acquiring a model binding.
func agentKeyForAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "run-planning", "run-followup-planning":
		return agentKeyPlanning
	case "run-implementation":
		return agentKeyCoding
	case "run-ideation":
		return agentKeyIdeation
	case "run-roadmap-discovery":
		return agentKeyRoadmapDiscovery
	case "run-roadmap-features":
		return agentKeyRoadmapFeatures
	case "run-roadmap-competitor":
		return agentKeyRoadmapCompetitor
	// run-roadmap-publish is deliberately absent: publishing projects already
	// approved features onto GitHub involves no judgement, so it has no model and
	// therefore no agent key. Returning one would put a configurable model on the
	// Agents & Models page for an action that never calls one.
	default:
		return ""
	}
}

// agentCatalogEntry is one row OPM publishes to OAM so the console can render a
// model picker per agent without OAM hardcoding OPM's action list.
type agentCatalogEntry struct {
	AgentKey string `json:"agent_key"`
	Label    string `json:"label"`
	TierHint string `json:"tier_hint"`
}

// agentCatalog is what OPM declares about itself.
//
// tier_hint is only a hint for the console's default suggestion — it does not
// select a model. The family default is provider cli_cursor with model `auto`,
// and `auto` stays correct for every one of these until someone deliberately
// overrides it.
func agentCatalog() []agentCatalogEntry {
	return []agentCatalogEntry{
		{AgentKey: agentKeyPlanning, Label: "Planning", TierHint: "light"},
		{AgentKey: agentKeyCoding, Label: "Implementation", TierHint: "strong"},
		{AgentKey: agentKeyIdeation, Label: "Ideation", TierHint: "light"},
		{AgentKey: agentKeyRoadmapDiscovery, Label: "Roadmap discovery", TierHint: "light"},
		{AgentKey: agentKeyRoadmapFeatures, Label: "Roadmap features", TierHint: "strong"},
		{AgentKey: agentKeyRoadmapCompetitor, Label: "Roadmap competitor analysis", TierHint: "light"},
	}
}
