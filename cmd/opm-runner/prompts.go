package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Condensed AutoCursor-near-parity system contracts. Output is always OPM JSON
// keys returned to the control plane (never write .auto-cursor into the repo).

func promptsFor(in runnerInput) (system, user string) {
	switch in.Action {
	case "run-roadmap-discovery":
		return discoveryPrompts(in)
	case "run-roadmap-features":
		return featuresPrompts(in)
	case "run-ideation":
		return ideationPrompts(in)
	default:
		return defaultTaskPrompts(in)
	}
}

func defaultTaskPrompts(in runnerInput) (system, user string) {
	system = `You are the OPM task-automation agent. Reply with a single JSON object only (no markdown fences).
Keys by action:
- run-planning / run-followup-planning: specMarkdown (string), plan (object: feature, workflow_type, phases[{phase,name,type,subtasks[{id,description,status,verification,files_to_modify}]}])
- run-implementation: files (array), commitMessage (string), implementationNotes (string), completedSubtaskId (string, optional)
- run-review: reviewMarkdown (string), reviewPass (boolean)

For run-implementation, "files" is the actual source change and the only thing that becomes a commit:
  [{"path":"relative/path.go","contents":"<complete new file body>"}]
  [{"path":"relative/path.go","patch":"<unified diff with correct context>"}]
  [{"path":"relative/path.go","delete":true}]
Rules for files:
- paths are repository-relative, forward slashes, never absolute and never containing ".."
- never touch .git/ or .github/workflows/
- prefer "contents" for new or small files; use "patch" only when the diff context is exact
- return an empty files array when you cannot make a correct change — do not invent one
- commitMessage is a plain subject line, optionally a blank line and a short body

Keep plans concrete and small (3 phases: planning, coding, review). Status values: pending|completed.`

	user = baseUserHeader(in)
	user += appendRepoForCoding(in)
	return system, user
}

func discoveryPrompts(in runnerInput) (system, user string) {
	system = `You are a roadmap discovery agent running inside OPM job automation.
Your subject is the LINKED CUSTOMER REPOSITORY described in the user message (projectName / repository / README / projectIndex / CURSOR.md).
Do NOT describe OPM, the Open family control plane, or this automation harness — those are the host, not the product under analysis.

Reply with a single JSON object only (no markdown fences).
Required JSON keys:
- vision (string): product_vision.one_liner for the linked repository
- targetAudience (string): compact primary persona + usage context for that product
- phases (array): [{id, name, description, order, status}] — typically 3–5 strategic phases; status "planned"
- discovery (object, optional but preferred): richer blob with project_type, tech_stack, target_audience object,
  product_vision {one_liner, problem_statement, value_proposition, success_metrics},
  current_state, constraints, created_at

Do NOT include ideation idea bodies or a full MoSCoW feature list.
Ground vision and phases in the real repository purpose (README, manifests, projectIndex, Claude.md/CURSOR.md).
If existingVision/existingPhases are provided, refine rather than invent unrelated replacements; preserve phase names when still valid.`

	user = baseUserHeader(in)
	user += appendContextPack(in, true, false)
	user += appendRepoForDiscovery(in)
	return system, user
}

func featuresPrompts(in runnerInput) (system, user string) {
	system = `You are a roadmap features agent running inside OPM job automation.
Your subject is the LINKED CUSTOMER REPOSITORY (projectName / repository / discovery snapshot / projectIndex).
Do NOT invent features for OPM or the Open family control plane unless that repository IS OPM.

Reply with a single JSON object only (no markdown fences).
Required JSON keys:
- features (array): [{id, title, description, rationale, priority, complexity, impact, phase_id,
  status, acceptance_criteria, user_stories}]
  priority: must|should|could|wont (MoSCoW)
  complexity: xs|s|m|l|xl (or small|medium|large)
  impact: high|medium|low (or free text)
  status: planned|idea
- If needsDiscovery is true or phases are empty, ALSO include vision, targetAudience, phases, and optional discovery
  (same shape as roadmap discovery) so the control plane can bootstrap — still about the linked repository only.

Do NOT include ideation idea bodies.
Exclude titles listed as implemented. Prefer net-new features grounded in discovery + projectIndex.
Attach each feature to a real phase_id from the discovery snapshot.`

	user = baseUserHeader(in)
	user += appendContextPack(in, false, true)
	user += appendRepoForDiscovery(in)
	return system, user
}

func ideationPrompts(in runnerInput) (system, user string) {
	typ := strings.TrimSpace(in.IdeationType)
	system = ideationSystemForType(typ)

	user = baseUserHeader(in)
	if typ != "" {
		user += "ideationType=" + typ + " (only this type)\n"
	} else {
		user += "ideationType=all\n"
	}
	user += appendContextPack(in, false, false)
	user += appendRepoForIdeation(in, typ)
	return system, user
}

func ideationSystemForType(typ string) string {
	base := `You are an ideation agent running inside OPM job automation.
Analyze the LINKED CUSTOMER REPOSITORY (projectName / repository / ideation_context / projectIndex / mounted files).
Do NOT propose ideas about OPM/Open-family control-plane internals unless that repository is OPM.

Reply with a single JSON object only (no markdown fences).
Output key: ideas — object keyed ONLY by these OPM type keys:
  code_improvements, security, performance, documentation, ui_ux, code_quality
Each idea: {title, description, rationale, estimated_effort, affected_files, implementation_approach}
Optional extras (severity, CWE, category) may be folded into description/rationale text.
Propose 1–2 concrete, novel ideas per requested type. Cite real paths from the repo when mounted.
Do not repeat existing or implemented titles. Do not rewrite roadmap discovery or generate MoSCoW feature lists.
`

	switch typ {
	case "security":
		return base + `Focus: authn/authz, injection, secrets, insecure defaults, dependency risk. Prefer exploitable issues with remediation.`
	case "performance":
		return base + `Focus: hot paths, N+1 queries, unbounded allocations, missing caching, slow I/O. Cite files/functions.`
	case "documentation":
		return base + `Focus: missing operator docs, stale README sections, API contract gaps, runbooks.`
	case "ui_ux":
		return base + `Focus: operator clarity, empty/error states, navigation, messaging — no screenshot tooling required.`
	case "code_quality":
		return base + `Focus: duplication, unclear boundaries, test gaps, lint debt, brittle modules.`
	case "code_improvements":
		return base + `Focus: pattern extensions, shared helpers, config opportunities, architecture enablement.`
	default:
		return base + `Cover each requested type with type-appropriate ideas.`
	}
}

func baseUserHeader(in runnerInput) string {
	user := fmt.Sprintf("action=%s\nrunId=%s\nspecId=%s\ntitle=%s\ndescription=%s\n",
		in.Action, in.RunID, in.SpecID, in.TaskTitle, in.TaskDesc)
	if in.ProjectName != "" {
		user += "projectName=" + in.ProjectName + "\n"
	}
	if in.OwnerRepo != "" {
		base := strings.TrimSpace(in.DefaultBranch)
		if base == "" {
			base = "main"
		}
		user += fmt.Sprintf("repository=%s\nbaseBranch=%s\n", in.OwnerRepo, base)
	}
	if in.CloneHonestyNote != "" {
		user += "\n--- clone honesty ---\n" + in.CloneHonestyNote + "\n"
	}
	if in.SpecExcerpt != "" {
		user += "\n--- existing spec ---\n" + truncate(in.SpecExcerpt, 4000) + "\n"
	}
	if in.PlanJSON != "" {
		user += "\n--- existing plan json ---\n" + truncate(in.PlanJSON, 6000) + "\n"
	}
	return user
}

func appendContextPack(in runnerInput, discovery, features bool) string {
	var b strings.Builder
	ctx := in.Context
	if len(ctx) == 0 {
		// Flat fallbacks from older control planes.
		if in.ExistingIdeaTitles != "" {
			b.WriteString("\n--- existing idea titles (do not repeat) ---\n")
			b.WriteString(truncate(in.ExistingIdeaTitles, 4000) + "\n")
		}
		if in.TaskTitles != "" {
			b.WriteString("\n--- board task titles ---\n")
			b.WriteString(truncate(in.TaskTitles, 2000) + "\n")
		}
		return b.String()
	}

	if md := ctxString(ctx, "cursorMd"); md != "" {
		b.WriteString("\n--- CURSOR.md / Claude.md ---\n")
		b.WriteString(truncate(md, 8000) + "\n")
	}
	if note := ctxString(ctx, "cloneHonestyNote"); note != "" && in.CloneHonestyNote == "" {
		b.WriteString("\n--- clone honesty ---\n" + note + "\n")
	}
	if pi, ok := ctx["projectIndex"]; ok {
		if raw, err := json.Marshal(pi); err == nil {
			b.WriteString("\n--- projectIndex ---\n" + truncate(string(raw), 4000) + "\n")
		}
	}
	if r := ctxString(ctx, "readmeExcerpt"); r != "" {
		b.WriteString("\n--- README excerpt ---\n" + truncate(r, 3000) + "\n")
	}
	if m, ok := ctx["manifestExcerpts"].(map[string]any); ok && len(m) > 0 {
		b.WriteString("\n--- manifests ---\n")
		for k, v := range m {
			if s, ok := v.(string); ok {
				b.WriteString("--- " + k + " ---\n" + truncate(s, 1200) + "\n")
			}
		}
	}

	if discovery || features {
		if v := ctxString(ctx, "existingVision"); v != "" {
			b.WriteString("\nexistingVision=" + truncate(v, 500) + "\n")
		}
		if ph, ok := ctx["existingPhases"]; ok {
			if raw, err := json.Marshal(ph); err == nil && string(raw) != "null" {
				b.WriteString("\n--- existing phases ---\n" + truncate(string(raw), 3000) + "\n")
			}
		}
	}
	if features {
		if nd, ok := ctx["needsDiscovery"].(bool); ok && nd {
			b.WriteString("\nneedsDiscovery=true — also return vision, targetAudience, phases, discovery.\n")
		}
		if snap, ok := ctx["discoverySnapshot"]; ok {
			if raw, err := json.Marshal(snap); err == nil {
				b.WriteString("\n--- discovery snapshot ---\n" + truncate(string(raw), 4000) + "\n")
			}
		}
		if titles := ctxStringSlice(ctx, "existingFeatureTitles"); len(titles) > 0 {
			b.WriteString("\n--- existing feature titles ---\n" + truncate(strings.Join(titles, "\n"), 2000) + "\n")
		}
		if note := ctxString(ctx, "implementedFeatureNote"); note != "" {
			b.WriteString("\n--- implemented features note ---\n" + truncate(note, 3000) + "\n")
		}
		if dc := ctxString(ctx, "designChoicesExcerpt"); dc != "" {
			b.WriteString("\n--- docs/APP_DESIGN_CHOICES.md ---\n" + truncate(dc, 1500) + "\n")
		}
	}

	if in.Action == "run-ideation" {
		if ic, ok := ctx["ideationContext"]; ok {
			if raw, err := json.Marshal(ic); err == nil {
				b.WriteString("\n--- ideation_context ---\n" + truncate(string(raw), 4000) + "\n")
			}
		}
		if titles := ctxStringSlice(ctx, "existingIdeaTitles"); len(titles) > 0 {
			b.WriteString("\n--- existing idea titles (do not repeat) ---\n")
			b.WriteString(truncate(strings.Join(titles, "\n"), 4000) + "\n")
		} else if in.ExistingIdeaTitles != "" {
			b.WriteString("\n--- existing idea titles (do not repeat) ---\n")
			b.WriteString(truncate(in.ExistingIdeaTitles, 4000) + "\n")
		}
		if note := ctxString(ctx, "implementedIdeationNote"); note != "" {
			b.WriteString("\n--- implemented ideation note ---\n" + truncate(note, 2000) + "\n")
		}
		if titles := ctxStringSlice(ctx, "boardTaskTitles"); len(titles) > 0 {
			b.WriteString("\n--- board task titles ---\n" + truncate(strings.Join(titles, "\n"), 2000) + "\n")
		}
	}
	return b.String()
}

func appendRepoForDiscovery(in runnerInput) string {
	if in.RepoDir == "" {
		return "\nNo repository mounted — infer only from project metadata; do not invent file paths.\n"
	}
	inv := repoInventory(in.RepoDir)
	if len(inv) == 0 {
		return "\nMounted repository at " + in.RepoDir + " has no readable source files.\n"
	}
	return "\n--- repository files (" + fmt.Sprint(len(inv)) + " shown) ---\n" +
		truncate(strings.Join(inv, "\n"), maxInventoryBytes) + "\n"
}

func appendRepoForIdeation(in runnerInput, typ string) string {
	if in.RepoDir == "" {
		return "\nNo repository mounted — keep ideas high-level; do not invent file paths.\n"
	}
	inv := repoInventory(in.RepoDir)
	if len(inv) == 0 {
		return "\nMounted repository at " + in.RepoDir + " has no readable source files.\n"
	}
	user := "\n--- repository files (" + fmt.Sprint(len(inv)) + " shown) ---\n" +
		truncate(strings.Join(inv, "\n"), maxInventoryBytes) + "\n"
	wanted := ideationHotspotPaths(inv, typ)
	for rel, body := range repoExcerpts(in.RepoDir, wanted) {
		user += "\n--- file " + rel + " ---\n" + body + "\n"
	}
	return user
}

func appendRepoForCoding(in runnerInput) string {
	if in.RepoDir == "" {
		return "\nNo repository was mounted for this run: you cannot see the source. " +
			"Return an empty files array and say so in implementationNotes.\n"
	}
	inv := repoInventory(in.RepoDir)
	if len(inv) == 0 {
		return "\nThe mounted repository at " + in.RepoDir + " contains no readable source files.\n"
	}
	user := "\n--- repository files (" + fmt.Sprint(len(inv)) + " shown) ---\n" +
		truncate(strings.Join(inv, "\n"), maxInventoryBytes) + "\n"
	wanted := planFilesToModify(in.PlanJSON)
	if len(wanted) == 0 {
		wanted = inv
	}
	for rel, body := range repoExcerpts(in.RepoDir, wanted) {
		user += "\n--- file " + rel + " ---\n" + body + "\n"
	}
	return user
}

func ideationHotspotPaths(inv []string, typ string) []string {
	keywords := []string{}
	switch typ {
	case "security":
		keywords = []string{"auth", "security", "token", "password", "middleware", "acl"}
	case "performance":
		keywords = []string{"cache", "pool", "query", "batch", "worker"}
	case "documentation":
		keywords = []string{"README", "docs/", ".md"}
	case "ui_ux":
		keywords = []string{".tsx", ".jsx", ".css", ".vue", ".kt", ".xml", "component", "compose", "layout"}
	case "code_quality", "code_improvements":
		keywords = []string{".go", ".kt", ".java", "handler", "store", "service", "ViewModel", "Repository"}
	default:
		keywords = []string{"README", "main.", "handler", "auth", "AndroidManifest", "build.gradle"}
	}
	var out []string
	for _, p := range inv {
		pl := strings.ToLower(p)
		for _, k := range keywords {
			if strings.Contains(pl, strings.ToLower(k)) {
				out = append(out, p)
				break
			}
		}
		if len(out) >= maxExcerptFiles {
			break
		}
	}
	if len(out) == 0 {
		n := maxExcerptFiles
		if len(inv) < n {
			n = len(inv)
		}
		return inv[:n]
	}
	return out
}

func ctxString(ctx map[string]any, key string) string {
	if ctx == nil {
		return ""
	}
	v, ok := ctx[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func ctxStringSlice(ctx map[string]any, key string) []string {
	if ctx == nil {
		return nil
	}
	v, ok := ctx[key]
	if !ok || v == nil {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		var out []string
		for _, x := range t {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
