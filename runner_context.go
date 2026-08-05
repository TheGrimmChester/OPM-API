package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// runnerContext is the nested context object written into runner input.json.
// Action-specific packs follow the AutoCursor-near-parity matrix: discovery,
// features, and ideation each get only what they need.
type runnerContext struct {
	ProjectIndex         *ProjectIndex     `json:"projectIndex,omitempty"`
	CursorMd             string            `json:"cursorMd,omitempty"`
	RepoDir              string            `json:"repoDir,omitempty"`
	CloneHonestyNote     string            `json:"cloneHonestyNote,omitempty"`
	ReadmeExcerpt        string            `json:"readmeExcerpt,omitempty"`
	ManifestExcerpts     map[string]string `json:"manifestExcerpts,omitempty"`
	FeaturesDocExcerpt   string            `json:"featuresDocExcerpt,omitempty"`
	DesignChoicesExcerpt string            `json:"designChoicesExcerpt,omitempty"`

	// Competitor pack — an input to discovery and features, produced by
	// run-roadmap-competitor.
	CompetitorAnalysis any `json:"competitorAnalysis,omitempty"`

	// Discovery pack
	ExistingVision string         `json:"existingVision,omitempty"`
	ExistingPhases []RoadmapPhase `json:"existingPhases,omitempty"`
	NeedsDiscovery bool           `json:"needsDiscovery,omitempty"`

	// Features pack
	DiscoverySnapshot      map[string]any `json:"discoverySnapshot,omitempty"`
	ExistingFeatureTitles  []string       `json:"existingFeatureTitles,omitempty"`
	ImplementedFeatures    []string       `json:"implementedFeatures,omitempty"`
	ImplementedFeatureNote string         `json:"implementedFeatureNote,omitempty"`

	// Ideation pack
	IdeationContext         map[string]any `json:"ideationContext,omitempty"`
	ExistingIdeaTitles      []string       `json:"existingIdeaTitles,omitempty"`
	ImplementedIdeaIDs      []string       `json:"implementedIdeaIds,omitempty"`
	ImplementedIdeaTitles   []string       `json:"implementedIdeaTitles,omitempty"`
	ImplementedIdeationNote string         `json:"implementedIdeationNote,omitempty"`
	BoardTaskTitles         []string       `json:"boardTaskTitles,omitempty"`
}

// buildRunnerContext assembles the per-action context pack for the runner.
// hostRepo is the host path of a real clone (with .git); empty when unavailable.
func buildRunnerContext(store *Store, j Job, hostRepo string, repoMounted bool) runnerContext {
	ctx := runnerContext{}
	if repoMounted {
		ctx.RepoDir = containerRepoPath
	}

	wantIndex := j.Action == "run-ideation" ||
		j.Action == "run-roadmap-discovery" ||
		j.Action == "run-roadmap-features" ||
		j.Action == "run-roadmap-competitor"

	if hostRepo != "" && wantIndex {
		idx := populateProjectIndex(hostRepo)
		ctx.ProjectIndex = &idx
		ctx.CursorMd = readCursorMd(hostRepo, 8000)
		ctx.ReadmeExcerpt = firstNonEmpty(
			readRepoDocExcerpt(hostRepo, "README.md", 3000),
			readRepoDocExcerpt(hostRepo, "README", 3000),
		)
		ctx.ManifestExcerpts = collectManifestExcerpts(hostRepo)
		ctx.FeaturesDocExcerpt = readRepoDocExcerpt(hostRepo, "docs/FEATURES.md", 2000)
		ctx.DesignChoicesExcerpt = readRepoDocExcerpt(hostRepo, "docs/APP_DESIGN_CHOICES.md", 2000)
	}

	if !repoMounted {
		p, _ := store.GetProject(j.ProjectID)
		if strings.TrimSpace(p.OwnerRepo) != "" && strings.TrimSpace(p.ConnectorID) != "" {
			ctx.CloneHonestyNote = fmt.Sprintf(
				"Linked repository %s has a connector but no usable git clone was mounted for this run "+
					"(clone credentials missing or clone failed). Ground suggestions in project metadata only; "+
					"do not invent repository file paths.",
				p.OwnerRepo,
			)
		} else if strings.TrimSpace(p.OwnerRepo) != "" {
			ctx.CloneHonestyNote = fmt.Sprintf(
				"Linked repository %s has no clone mounted (connector or clone credentials unavailable). "+
					"Do not invent repository file paths.",
				p.OwnerRepo,
			)
		} else {
			ctx.CloneHonestyNote = "No repository was mounted for this run."
		}
	}

	switch j.Action {
	case "run-roadmap-discovery":
		fillDiscoveryContext(store, j, &ctx)
	case "run-roadmap-features":
		fillFeaturesContext(store, j, &ctx)
	case "run-ideation":
		fillIdeationContext(store, j, &ctx)
	}
	return ctx
}

// cloneHonestyOperatorNote returns a short operator-facing sentence when a linked
// repo exists but no usable git clone was prepared for the job workspace.
func cloneHonestyOperatorNote(p Project, workDir string) string {
	if repoMountSource(workDir) != "" {
		return ""
	}
	ownerRepo := strings.TrimSpace(p.OwnerRepo)
	if ownerRepo == "" {
		return "No repository clone was mounted for this run."
	}
	if strings.TrimSpace(p.ConnectorID) != "" {
		return fmt.Sprintf("No git clone mounted for %s (connector present but clone unavailable); model/builtin output may not cite real paths.", ownerRepo)
	}
	return fmt.Sprintf("No git clone mounted for %s; model/builtin output may not cite real paths.", ownerRepo)
}

func fillDiscoveryContext(store *Store, j Job, ctx *runnerContext) {
	// The competitor analysis is an input here, not decoration: ORA's pipeline fed
	// it into the discovery prompt, and dropping that would make the competitor
	// stage write-only.
	if analysis, ok := competitorAnalysisFor(store, j.ProjectID); ok {
		ctx.CompetitorAnalysis = analysis
	}
	rm, err := store.GetRoadmap(j.ProjectID)
	if err != nil {
		return
	}
	ctx.ExistingVision = strings.TrimSpace(rm.Vision)
	if len(rm.Phases) > 0 {
		ctx.ExistingPhases = rm.Phases
	}
}

func fillFeaturesContext(store *Store, j Job, ctx *runnerContext) {
	if analysis, ok := competitorAnalysisFor(store, j.ProjectID); ok {
		ctx.CompetitorAnalysis = analysis
	}
	rm, err := store.GetRoadmap(j.ProjectID)
	if err != nil {
		ctx.NeedsDiscovery = true
		return
	}
	ctx.NeedsDiscovery = len(rm.Phases) == 0
	snap := map[string]any{
		"vision":          rm.Vision,
		"target_audience": rm.TargetAudience,
		"phases":          rm.Phases,
	}
	if rm.Metadata != nil {
		if d, ok := rm.Metadata["discovery"]; ok {
			snap["discovery"] = d
		}
	}
	ctx.DiscoverySnapshot = snap
	ctx.ExistingPhases = rm.Phases
	ctx.ExistingVision = strings.TrimSpace(rm.Vision)

	var titles, implemented []string
	for _, f := range rm.Features {
		t := strings.TrimSpace(f.Title)
		if t == "" {
			continue
		}
		titles = append(titles, t)
		if featureStatusImplemented(f.Status) {
			implemented = append(implemented, t)
		}
	}
	sort.Strings(titles)
	sort.Strings(implemented)
	ctx.ExistingFeatureTitles = titles
	ctx.ImplementedFeatures = implemented
	ctx.ImplementedFeatureNote = buildImplementedFeatureNote(implemented, ctx.FeaturesDocExcerpt)
}

func fillIdeationContext(store *Store, j Job, ctx *runnerContext) {
	ideas, _ := store.GetIdeation(j.ProjectID)
	if ideas == nil {
		ideas = emptyIdeation()
	}
	rm, _ := store.GetRoadmap(j.ProjectID)
	_, taskTitles, _ := projectContext(store, j.ProjectID)
	ctx.BoardTaskTitles = taskTitles

	types := IdeationTypes
	if t := strings.TrimSpace(j.IdeationType); t != "" {
		types = []string{t}
	}

	existingByType := map[string][]string{}
	var allTitles []string
	for _, typ := range types {
		var titles []string
		for _, idea := range ideas[typ] {
			if t := strings.TrimSpace(idea.Title); t != "" {
				titles = append(titles, t)
				allTitles = append(allTitles, t)
			}
		}
		sort.Strings(titles)
		existingByType[typ] = titles
	}
	sort.Strings(allTitles)
	ctx.ExistingIdeaTitles = allTitles

	implIDs, implTitles := implementedIdeationFromDone(store, j.ProjectID, j.IdeationType)
	ctx.ImplementedIdeaIDs = sortedSet(implIDs)
	ctx.ImplementedIdeaTitles = sortedSet(implTitles)
	ctx.ImplementedIdeationNote = buildImplementedIdeationNote(ctx.ImplementedIdeaIDs, ctx.ImplementedIdeaTitles)

	var featureTitles []string
	for _, f := range rm.Features {
		if t := strings.TrimSpace(f.Title); t != "" {
			featureTitles = append(featureTitles, t)
		}
	}
	sort.Strings(featureTitles)

	vision := strings.TrimSpace(rm.Vision)
	if len(vision) > 240 {
		vision = vision[:240] + "…"
	}
	ctx.IdeationContext = map[string]any{
		"existing_idea_titles_by_type": existingByType,
		"roadmap_feature_titles":       featureTitles,
		"board_task_titles":            taskTitles,
		"vision_one_liner":             vision,
		"ideation_type":                nz(j.IdeationType, "all"),
	}
}

func collectManifestExcerpts(projectDir string) map[string]string {
	out := map[string]string{}
	for _, name := range []string{"package.json", "pyproject.toml", "go.mod", "Cargo.toml", "composer.json",
		"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"} {
		ex := readRepoDocExcerpt(projectDir, name, 1500)
		if ex != "" {
			out[name] = ex
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func featureStatusImplemented(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "implemented", "done", "completed", "shipped":
		return true
	}
	return false
}

func buildImplementedFeatureNote(titles []string, featuresDoc string) string {
	var b strings.Builder
	b.WriteString("Exclude already-implemented features from new proposals. ")
	b.WriteString("Treat OPM store status in {implemented,done,completed,shipped} as authoritative; ")
	b.WriteString("also honor docs/FEATURES.md when present in the clone.\n")
	if len(titles) == 0 {
		b.WriteString("Implemented features (store): none.\n")
	} else {
		b.WriteString("Implemented features (store):\n")
		for _, t := range titles {
			b.WriteString("- " + t + "\n")
		}
	}
	if strings.TrimSpace(featuresDoc) != "" {
		b.WriteString("\n--- docs/FEATURES.md excerpt ---\n")
		b.WriteString(featuresDoc)
	}
	return b.String()
}

func buildImplementedIdeationNote(ids, titles []string) string {
	var b strings.Builder
	b.WriteString("Do not re-propose ideas already linked to Done board tasks (by ideationId or title).\n")
	if len(ids) == 0 && len(titles) == 0 {
		b.WriteString("Implemented ideation: none.\n")
		return b.String()
	}
	if len(ids) > 0 {
		b.WriteString("Implemented ideation ids: " + strings.Join(ids, ", ") + "\n")
	}
	if len(titles) > 0 {
		b.WriteString("Implemented idea titles:\n")
		for _, t := range titles {
			b.WriteString("- " + t + "\n")
		}
	}
	return b.String()
}

// implementedIdeationFromDone collects ideationIds and normalized titles from
// tasks in the Done column.
func implementedIdeationFromDone(store *Store, projectID, ideationType string) (ids, titles map[string]bool) {
	ids = map[string]bool{}
	titles = map[string]bool{}
	board, err := store.GetBoard(projectID)
	if err != nil {
		return ids, titles
	}
	for _, specID := range board["done"] {
		task, err := store.GetTask(projectID, specID)
		if err != nil {
			continue
		}
		taskType := strings.TrimSpace(task.IdeationTypeKey)
		if ideationType != "" && taskType != "" && taskType != ideationType {
			continue
		}
		if id := strings.TrimSpace(task.IdeationID); id != "" {
			ids[id] = true
		}
		if t := normalizeTitle(task.Title); t != "" {
			titles[t] = true
		}
	}
	return ids, titles
}

func normalizeTitle(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// writeRunnerInputJSON marshals the full runner input including nested context.
func writeRunnerInputJSON(store *Store, j Job, path string, repoMounted bool, hostRepo string) error {
	in := map[string]any{
		"action":    j.Action,
		"runId":     j.RunID,
		"specId":    j.SpecID,
		"projectId": j.ProjectID,
	}
	if repoMounted {
		in["repoDir"] = containerRepoPath
	}
	if p, err := store.GetProject(j.ProjectID); err == nil {
		in["ownerRepo"] = p.OwnerRepo
		in["defaultBranch"] = nz(p.DefaultBranch, "main")
		in["projectName"] = p.Name
	}
	if j.IdeationType != "" {
		in["ideationType"] = j.IdeationType
	}
	// The roadmap pipeline's per-run inputs. Named competitors are analysed exactly
	// as given, so they have to reach the prompt rather than being re-derived.
	if len(j.Competitors) > 0 {
		in["competitors"] = j.Competitors
	}
	if n := strings.TrimSpace(j.AudienceNotes); n != "" {
		in["audienceNotes"] = n
	}
	if j.SpecID != "" {
		if task, err := store.GetTask(j.ProjectID, j.SpecID); err == nil {
			in["taskTitle"] = task.Title
			in["taskDescription"] = task.Description
		}
		if md, err := store.GetSpecMarkdown(j.ProjectID, j.SpecID); err == nil {
			in["specExcerpt"] = truncateRunes(md, 4000)
		}
		if plan, err := store.GetPlan(j.ProjectID, j.SpecID); err == nil && len(plan.Phases) > 0 {
			if b, mErr := json.Marshal(plan); mErr == nil {
				in["planJson"] = string(b)
			}
		}
	}

	ctx := buildRunnerContext(store, j, hostRepo, repoMounted)
	in["context"] = ctx

	if len(ctx.ExistingIdeaTitles) > 0 {
		in["existingIdeaTitles"] = strings.Join(ctx.ExistingIdeaTitles, "\n")
	}
	if len(ctx.BoardTaskTitles) > 0 {
		in["taskTitles"] = strings.Join(ctx.BoardTaskTitles, "\n")
	}
	if ctx.CloneHonestyNote != "" {
		in["cloneHonestyNote"] = ctx.CloneHonestyNote
	}

	b, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
