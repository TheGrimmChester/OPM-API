package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Publishing a roadmap to GitHub as issues.
//
// This is the last piece of ORA's roadmap feature to find a home. The *pipeline*
// moved here because OPM owns the roadmap document; the GitHub protocol work
// stayed in ORA because ORA owns connectors and issue writes. So this file
// composes: it reads OPM's roadmap and calls the ORA peer once per feature,
// exactly as OPM already does for milestones and Projects v2.
//
// Deliberately no GitHub client here. Adding one would give OPM a second
// implementation of authentication, rate limiting and error mapping that ORA
// already has, and would need its own connector credentials — which is precisely
// the duplication the family's ownership split exists to avoid.

// roadmapPublishLabel marks an issue as roadmap-created, so a republish can
// recognise its own output instead of duplicating it.
const roadmapPublishLabel = "roadmap"

// roadmapMetaPublished records what was published, keyed by feature id.
const roadmapMetaPublished = "published_issues"

type publishedIssue struct {
	FeatureID string `json:"feature_id"`
	Number    int    `json:"number"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	At        string `json:"at"`
}

// builtinRoadmapPublish creates one GitHub issue per unpublished roadmap feature.
//
// It is a "builtin" rather than a model action because there is no judgement in
// it: the features are already written, and publishing is a mechanical projection
// of them onto GitHub. Sending it through a model would introduce paraphrasing
// into text a human already approved.
func builtinRoadmapPublish(store *Store, j Job) (string, error) {
	if !peerORAConfigured() {
		// Fail rather than degrade. A silent no-op here reads as "published" on the
		// board, and the operator only learns otherwise by looking at GitHub.
		return "PEER_ORA_URL is not configured; roadmap publishing needs the ORA peer",
			fmt.Errorf("peer_ora_unconfigured")
	}
	p, err := store.GetProject(j.ProjectID)
	if err != nil {
		return "Project not found", err
	}
	if strings.TrimSpace(p.OwnerRepo) == "" || strings.TrimSpace(p.ConnectorID) == "" {
		return "This project has no GitHub repository linked; link one before publishing the roadmap",
			fmt.Errorf("project_not_linked")
	}
	rm, err := store.GetRoadmap(j.ProjectID)
	if err != nil {
		return "No roadmap to publish", err
	}
	if len(rm.Features) == 0 {
		return "The roadmap has no features to publish; run roadmap features first", nil
	}
	// Validate before writing anything outward. A dangling phase reference is
	// harmless on the board but produces issues nobody can trace back to a phase,
	// and issues are not cheap to undo.
	if verr := validateRoadmapJSON(rm); verr != nil {
		return "Roadmap is not publishable: " + verr.Error(), verr
	}

	already := publishedIssuesFor(rm)
	phaseName := map[string]string{}
	for _, ph := range rm.Phases {
		phaseName[ph.ID] = ph.Name
	}

	// Deterministic order, so a partial failure resumes predictably rather than
	// publishing a different subset on each attempt.
	features := append([]RoadmapFeature(nil), rm.Features...)
	sort.SliceStable(features, func(a, b int) bool {
		if features[a].PhaseID != features[b].PhaseID {
			return features[a].PhaseID < features[b].PhaseID
		}
		return features[a].ID < features[b].ID
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	created := 0
	skipped := 0
	var failures []string
	for _, f := range features {
		fid := strings.TrimSpace(f.ID)
		if fid == "" {
			skipped++
			continue
		}
		if _, done := already[fid]; done {
			// Idempotent: a republish after adding two features creates two issues,
			// not a whole duplicate roadmap.
			skipped++
			continue
		}
		title := strings.TrimSpace(f.Title)
		if title == "" {
			skipped++
			continue
		}
		body := formatRoadmapIssueBody(rm, f, phaseName[f.PhaseID])
		labels := []string{roadmapPublishLabel}
		if pr := strings.TrimSpace(f.Priority); pr != "" {
			labels = append(labels, "priority:"+pr)
		}
		res, cerr := peerCreateIssue(ctx, j.OrganizationID, j.ActorUsername, p.ConnectorID, p.OwnerRepo,
			title, body, labels, f.GithubMilestoneNumber)
		if cerr != nil {
			failures = append(failures, fid+": "+truncateRunes(cerr.Error(), 100))
			// Keep going. One repository-level rejection (a label that does not
			// exist, a closed milestone) should not strand the rest of the roadmap.
			continue
		}
		already[fid] = publishedIssue{
			FeatureID: fid,
			Number:    intFromAny(res["number"]),
			URL:       strFromAny(res["html_url"]),
			Title:     title,
			At:        nowUTC().Format(time.RFC3339),
		}
		created++
	}

	// Record what was published even on a partial failure, so a retry does not
	// duplicate the issues that did land.
	if rm.Metadata == nil {
		rm.Metadata = map[string]any{}
	}
	list := make([]publishedIssue, 0, len(already))
	for _, v := range already {
		list = append(list, v)
	}
	sort.SliceStable(list, func(a, b int) bool { return list[a].FeatureID < list[b].FeatureID })
	rm.Metadata[roadmapMetaPublished] = list
	if perr := store.PutRoadmap(j.ProjectID, rm); perr != nil {
		return fmt.Sprintf("Published %d issue(s) but failed to record them", created), perr
	}

	_ = store.AppendProjectLog(j.ProjectID, "run-roadmap-publish",
		fmt.Sprintf("[%s] published %d issue(s), skipped %d, %d failure(s)\n",
			j.RunID, created, skipped, len(failures)))

	msg := fmt.Sprintf("Published %d issue(s) to %s; %d already published or unpublishable",
		created, p.OwnerRepo, skipped)
	if len(failures) > 0 {
		// Naming the failures matters: "published 8 of 10" without saying which two
		// failed leaves an operator diffing GitHub against the board by hand.
		msg += fmt.Sprintf(". %d failed: %s", len(failures),
			truncateRunes(strings.Join(failures, "; "), 300))
		return msg, fmt.Errorf("%d feature(s) could not be published", len(failures))
	}
	return msg, nil
}

// publishedIssuesFor reads the record of what has already been published.
func publishedIssuesFor(rm Roadmap) map[string]publishedIssue {
	out := map[string]publishedIssue{}
	if rm.Metadata == nil {
		return out
	}
	raw, ok := rm.Metadata[roadmapMetaPublished]
	if !ok {
		return out
	}
	// Round-trips through any, because metadata read back from disk is generic.
	list, ok := raw.([]any)
	if !ok {
		if typed, ok := raw.([]publishedIssue); ok {
			for _, p := range typed {
				out[p.FeatureID] = p
			}
		}
		return out
	}
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		fid := strFromAny(m["feature_id"])
		if fid == "" {
			continue
		}
		out[fid] = publishedIssue{
			FeatureID: fid,
			Number:    intFromAny(m["number"]),
			URL:       strFromAny(m["url"]),
			Title:     strFromAny(m["title"]),
			At:        strFromAny(m["at"]),
		}
	}
	return out
}

// formatRoadmapIssueBody renders one feature as issue markdown.
//
// It carries the roadmap and feature ids so an issue can be traced back to the
// row that produced it — ORA's version did the same, and without it a
// roadmap-created issue is indistinguishable from a hand-written one.
func formatRoadmapIssueBody(rm Roadmap, f RoadmapFeature, phase string) string {
	var b strings.Builder
	if d := strings.TrimSpace(f.Description); d != "" {
		b.WriteString(d)
		b.WriteString("\n\n")
	}
	if r := strings.TrimSpace(f.Rationale); r != "" {
		b.WriteString("**Why:** ")
		b.WriteString(r)
		b.WriteString("\n\n")
	}
	if len(f.AcceptanceCriteria) > 0 {
		b.WriteString("### Acceptance criteria\n")
		for _, c := range f.AcceptanceCriteria {
			if c = strings.TrimSpace(c); c != "" {
				b.WriteString("- [ ] ")
				b.WriteString(c)
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
	}
	if len(f.UserStories) > 0 {
		b.WriteString("### User stories\n")
		for _, s := range f.UserStories {
			if s = strings.TrimSpace(s); s != "" {
				b.WriteString("- ")
				b.WriteString(s)
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("---\n")
	fmt.Fprintf(&b, "Roadmap `%s`", nz(rm.ID, "roadmap"))
	if phase != "" {
		fmt.Fprintf(&b, " · phase **%s**", phase)
	}
	fmt.Fprintf(&b, " · feature `%s`", f.ID)
	if pr := strings.TrimSpace(f.Priority); pr != "" {
		fmt.Fprintf(&b, " · priority %s", pr)
	}
	if cx := strings.TrimSpace(f.Complexity); cx != "" {
		fmt.Fprintf(&b, " · complexity %s", cx)
	}
	b.WriteString("\n")
	return b.String()
}
