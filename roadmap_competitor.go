package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The competitor-analysis stage, and roadmap schema validation.
//
// Both come from ORA's roadmap.go, which is being retired. What did NOT come
// across is ORA's discovery and features pipeline: OPM already has those as
// model-backed job kinds (`run-roadmap-discovery`, `run-roadmap-features`, with
// real prompts in cmd/opm-runner/prompts.go and repo-aware context packs in
// runner_context.go). Porting ORA's versions would have duplicated two working
// stages with a weaker pair — ORA's read a throwaway `prepareSCMWorktree`
// checkout, OPM's read the task workspace and project index.
//
// So this file carries only the two things ORA genuinely had and OPM did not:
// a competitor stage that feeds discovery, and a schema check on the roadmap the
// model returns.

// competitorAnalysis is the artifact this stage produces. It is stored on the
// roadmap's metadata rather than in its own document, because it is an *input* to
// discovery and features rather than a thing the board renders on its own.
type competitorAnalysis struct {
	Competitors     []competitorEntry `json:"competitors"`
	MarketGaps      []string          `json:"market_gaps,omitempty"`
	Differentiators []string          `json:"differentiators,omitempty"`
	// Honesty carries the reason a heuristic stood in for the model, so an
	// operator reading the roadmap can tell analysis from placeholder. ORA set the
	// same field for the same reason.
	Honesty string `json:"honesty,omitempty"`
}

type competitorEntry struct {
	Name       string   `json:"name"`
	Strengths  []string `json:"strengths,omitempty"`
	Weaknesses []string `json:"weaknesses,omitempty"`
	Gaps       []string `json:"gaps,omitempty"`
}

// roadmapMetaCompetitor is the metadata key the analysis is stored under.
const roadmapMetaCompetitor = "competitor_analysis"

// heuristicCompetitorAnalysis is the degraded output when no model is reachable.
//
// Ported from ORA with its shape intact, so a deployment migrating from ORA sees
// the same fields. It exists for the same reason as every other builtin here: a
// missing credential must degrade rather than fail, and the `honesty` field is
// what stops the degraded output being mistaken for analysis.
func heuristicCompetitorAnalysis(projectName string, competitors []string) competitorAnalysis {
	out := competitorAnalysis{}
	for _, c := range competitors {
		if name := strings.TrimSpace(c); name != "" {
			out.Competitors = append(out.Competitors, competitorEntry{Name: name})
		}
	}
	if len(out.Competitors) == 0 {
		out.Competitors = []competitorEntry{{
			Name: "generic alternatives",
			Gaps: []string{"deeper automation"},
		}}
	}
	out.MarketGaps = []string{"autonomous task planning with approval gates"}
	out.Differentiators = []string{nz(strings.TrimSpace(projectName), "this project") + " board-driven delivery"}
	return out
}

// parseCompetitorAnalysis reads the model's output, falling back to the heuristic.
//
// A model that returns an empty competitor list has not done the work, so that
// counts as a miss rather than as "no competitors exist" — otherwise a silent
// empty analysis would flow into discovery as though it were a finding.
func parseCompetitorAnalysis(raw json.RawMessage, projectName string, competitors []string) (competitorAnalysis, bool) {
	if len(raw) == 0 {
		return heuristicCompetitorAnalysis(projectName, competitors), false
	}
	var got competitorAnalysis
	if err := json.Unmarshal(raw, &got); err != nil {
		fb := heuristicCompetitorAnalysis(projectName, competitors)
		fb.Honesty = "model returned unparseable competitor JSON: " + truncateRunes(err.Error(), 120)
		return fb, false
	}
	if len(got.Competitors) == 0 {
		fb := heuristicCompetitorAnalysis(projectName, competitors)
		fb.Honesty = "model returned no competitors"
		return fb, false
	}
	return got, true
}

// validateRoadmapJSON is ORA's check, kept because it catches the one failure that
// silently produces a useless roadmap: a model that returns prose or a features
// list with no phases. OPM's board renders phases, so a phase-less roadmap looks
// like an empty board rather than like a failed job.
//
// It is intentionally shallow. A strict schema would reject the model's harmless
// variation (extra keys, a missing optional field) and turn a usable roadmap into
// a failure, which is worse than a roadmap with a thin phase.
func validateRoadmapJSON(rm Roadmap) error {
	if len(rm.Phases) == 0 {
		return fmt.Errorf("roadmap has no phases; the board would render as empty")
	}
	for i, p := range rm.Phases {
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("phase %d has no name", i+1)
		}
	}
	// A feature referencing a phase that does not exist is a dangling row: it
	// never renders in a column, so it is invisible on the board while still
	// counting in the totals.
	known := map[string]bool{}
	for _, p := range rm.Phases {
		if id := strings.TrimSpace(p.ID); id != "" {
			known[id] = true
		}
	}
	var dangling []string
	for _, f := range rm.Features {
		pid := strings.TrimSpace(f.PhaseID)
		if pid != "" && !known[pid] {
			dangling = append(dangling, nz(f.Title, f.ID)+"→"+pid)
		}
	}
	if len(dangling) > 0 {
		return fmt.Errorf("%d feature(s) reference an unknown phase: %s",
			len(dangling), truncateRunes(strings.Join(dangling, ", "), 160))
	}
	return nil
}

// builtinRoadmapCompetitor is the control-plane side of the competitor stage.
//
// Like every other builtin here it runs when the model produced nothing, and it
// writes the heuristic analysis so the next stage has an input rather than an
// error.
func builtinRoadmapCompetitor(store *Store, j Job) (string, error) {
	rm, err := store.GetRoadmap(j.ProjectID)
	if err != nil {
		// No roadmap yet is normal: competitor analysis can legitimately run
		// first, so start a document rather than failing.
		rm = Roadmap{ID: "roadmap-1"}
	}
	p, perr := store.GetProject(j.ProjectID)
	name := rm.ProjectName
	if perr == nil {
		name = nz(name, p.Name)
	}
	analysis := heuristicCompetitorAnalysis(name, j.Competitors)
	analysis.Honesty = "generated by the builtin heuristic; no model output was available"

	if err := storeCompetitorAnalysis(store, j.ProjectID, rm, analysis); err != nil {
		return "Failed to write competitor analysis", err
	}
	_ = store.AppendProjectLog(j.ProjectID, "run-roadmap-competitor",
		fmt.Sprintf("[%s] builtin competitor analysis for %d named competitor(s)\n", j.RunID, len(j.Competitors)))
	return fmt.Sprintf("Wrote heuristic competitor analysis (%d entries). Configure a model binding for real analysis.",
		len(analysis.Competitors)), nil
}

// storeCompetitorAnalysis merges the analysis into the roadmap's metadata.
func storeCompetitorAnalysis(store *Store, projectID string, rm Roadmap, analysis competitorAnalysis) error {
	if rm.Metadata == nil {
		rm.Metadata = map[string]any{}
	}
	rm.Metadata[roadmapMetaCompetitor] = analysis
	if strings.TrimSpace(rm.ID) == "" {
		rm.ID = "roadmap-1"
	}
	return store.PutRoadmap(projectID, rm)
}

// competitorAnalysisFor reads back a stored analysis so discovery and features can
// use it. A missing or malformed value returns nothing rather than an error: the
// downstream stages are designed to run without it.
func competitorAnalysisFor(store *Store, projectID string) (competitorAnalysis, bool) {
	rm, err := store.GetRoadmap(projectID)
	if err != nil || rm.Metadata == nil {
		return competitorAnalysis{}, false
	}
	raw, ok := rm.Metadata[roadmapMetaCompetitor]
	if !ok {
		return competitorAnalysis{}, false
	}
	// Round-trip through JSON: metadata is map[string]any, so a stored struct
	// comes back as a map after a read from disk.
	blob, err := json.Marshal(raw)
	if err != nil {
		return competitorAnalysis{}, false
	}
	var out competitorAnalysis
	if err := json.Unmarshal(blob, &out); err != nil {
		return competitorAnalysis{}, false
	}
	return out, len(out.Competitors) > 0
}
