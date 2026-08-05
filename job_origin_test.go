package main

import "testing"

// What an autopilot chain inherits, and what it must not.
//
// Two different things travel down a chain and conflating them is a bug in both
// directions: the ACTOR is the identity the whole chain runs as, while the MODEL
// belongs to one agent phase. A chain crosses phases (coding → review → qa_fix).

func TestChainInheritsActorAcrossPhases(t *testing.T) {
	parent := Job{
		Action:         "run-implementation",
		OrganizationID: "org-a",
		ActorUsername:  "alice",
	}
	origin := jobOriginFrom(parent)
	if origin.OrganizationID != "org-a" || origin.ActorUsername != "alice" {
		t.Fatalf("actor lost: org=%q user=%q", origin.OrganizationID, origin.ActorUsername)
	}
}

// Same phase (coding subtask 2.1 → 2.2 on one shared workspace): the pinned model
// carries over, so an org config edit or a key rotation mid-task cannot switch
// models between subtasks.
func TestPinnedModelCarriesWithinAPhase(t *testing.T) {
	parent := Job{
		Action:              "run-implementation",
		ResolvedProvider:    "anthropic",
		ResolvedModel:       "claude-opus-5",
		ResolvedModelSource: "org",
		ResolvedAgentKey:    agentKeyCoding,
	}
	origin := jobOriginFrom(parent)
	if !origin.pinnedFor(agentKeyCoding) {
		t.Fatal("a coding pin must be reusable by the next coding subtask")
	}
}

// Different phase (coding → review): the pin must NOT carry, or every phase would
// run the model configured for the first one and per-agent models would be a lie.
func TestPinnedModelDoesNotCrossPhases(t *testing.T) {
	parent := Job{
		Action:           "run-implementation",
		ResolvedProvider: "anthropic",
		ResolvedModel:    "claude-opus-5",
		ResolvedAgentKey: agentKeyCoding,
	}
	origin := jobOriginFrom(parent)
	if origin.pinnedFor(agentKeyReview) {
		t.Fatal("a coding pin must not satisfy the review phase")
	}
	if origin.pinnedFor(agentKeyQAFix) {
		t.Fatal("a coding pin must not satisfy the qa_fix phase")
	}
	// An empty agent key can never be satisfied by a pin either — that would let
	// a non-AI action inherit a model.
	if origin.pinnedFor("") {
		t.Fatal("an empty agent key must not be considered pinned")
	}
}

func TestUnpinnedOriginIsNotPinned(t *testing.T) {
	origin := jobOriginFrom(Job{Action: "run-implementation", ActorUsername: "alice"})
	if origin.pinnedFor(agentKeyCoding) {
		t.Fatal("a fresh session carries no pin")
	}
	// A half-populated pin (model without provider) is not usable.
	half := jobOriginFrom(Job{ResolvedModel: "claude-opus-5", ResolvedAgentKey: agentKeyCoding})
	if half.pinnedFor(agentKeyCoding) {
		t.Fatal("a pin missing its provider must not count as pinned")
	}
}

// The Job's AgentKey is derived from its action at creation, so the resolve path
// does not have to re-derive it (and cannot disagree with it).
func TestJobOriginRoundTripKeepsOverride(t *testing.T) {
	override := &modelOverride{Provider: "anthropic", Model: "claude-haiku-4-5"}
	parent := Job{Action: "run-planning", ModelOverride: override, ActorUsername: "alice"}
	origin := jobOriginFrom(parent)
	if origin.ModelOverride == nil || origin.ModelOverride.Model != "claude-haiku-4-5" {
		t.Fatal("a per-task override must survive chaining")
	}
	if override.empty() {
		t.Fatal("a populated override must not read as empty")
	}
	var nilOverride *modelOverride
	if !nilOverride.empty() {
		t.Fatal("a nil override must read as empty")
	}
	if !(&modelOverride{}).empty() {
		t.Fatal("a zero override must read as empty")
	}
}
