package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The change set is the only thing that can turn into a commit.
//
// A runner returns files[] plus a commitMessage; the control plane records that
// verbatim as specs/<specId>/changes.json and never invents entries. A run that
// produced nothing records nothing, so `deliver` can answer "no changes
// produced" instead of pushing an empty branch and calling it a delivery.

// changeSetFileName is the recorded change set inside the spec directory.
const changeSetFileName = "changes.json"

// FileChange is one file the runner wants written. Exactly one of Contents,
// Patch or Delete carries the change:
//
//	Contents — the full new file body (create or overwrite)
//	Patch    — a unified diff applied with `git apply`
//	Delete   — remove the path
type FileChange struct {
	Path     string  `json:"path"`
	Contents *string `json:"contents,omitempty"`
	Patch    string  `json:"patch,omitempty"`
	Delete   bool    `json:"delete,omitempty"`
}

// mode describes how the change will be applied, for logs and UI summaries.
func (f FileChange) mode() string {
	switch {
	case f.Delete:
		return "delete"
	case f.Patch != "":
		return "patch"
	case f.Contents != nil:
		return "write"
	}
	return "empty"
}

// ChangeSet is the recorded output of one automation run.
type ChangeSet struct {
	RunID         string       `json:"runId,omitempty"`
	Source        string       `json:"source"` // model | builtin
	Model         string       `json:"model,omitempty"`
	CommitMessage string       `json:"commitMessage,omitempty"`
	RecordedAt    time.Time    `json:"recordedAt"`
	Files         []FileChange `json:"files"`
	// Note explains an empty change set instead of leaving it ambiguous.
	Note string `json:"note,omitempty"`
}

func (cs ChangeSet) paths() []string {
	out := make([]string, 0, len(cs.Files))
	for _, f := range cs.Files {
		out = append(out, f.Path)
	}
	return out
}

// changeSetLimits caps a recorded change set so a runaway runner cannot commit an
// unbounded diff. Both are operator-tunable.
type changeSetLimits struct {
	MaxFiles int
	MaxBytes int
}

func deliveryLimits() changeSetLimits {
	return changeSetLimits{
		MaxFiles: atoiOr(envOr("OPM_DELIVERY_MAX_FILES", "50"), 50),
		MaxBytes: atoiOr(envOr("OPM_DELIVERY_MAX_BYTES", "2097152"), 2<<20),
	}
}

func atoiOr(s string, def int) int {
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			return def
		}
		n = n*10 + int(r-'0')
		if n > 1<<30 {
			return def
		}
	}
	if n == 0 {
		return def
	}
	return n
}

// normalizeChangeSet drops entries that carry no change and reports why. It does
// not validate paths — that happens against a concrete workspace in applyChangeSet,
// where the real filesystem is available.
func normalizeChangeSet(files []FileChange) (kept []FileChange, dropped []string) {
	seen := map[string]bool{}
	for _, f := range files {
		f.Path = strings.TrimSpace(f.Path)
		if f.Path == "" {
			dropped = append(dropped, "(blank path)")
			continue
		}
		if f.mode() == "empty" {
			dropped = append(dropped, f.Path+" (no contents, patch or delete)")
			continue
		}
		if seen[f.Path] {
			dropped = append(dropped, f.Path+" (duplicate)")
			continue
		}
		seen[f.Path] = true
		kept = append(kept, f)
	}
	return kept, dropped
}

// changeSetPath is the on-disk location of the recorded change set.
func (s *Store) changeSetPath(p Project, specID string) string {
	return filepath.Join(s.projectDir(p), "specs", specID, changeSetFileName)
}

// MergeChangeSet merges delta files into base by path (delta wins).
func MergeChangeSet(base, delta ChangeSet) ChangeSet {
	byPath := map[string]FileChange{}
	order := []string{}
	for _, f := range base.Files {
		byPath[f.Path] = f
		order = append(order, f.Path)
	}
	for _, f := range delta.Files {
		if _, ok := byPath[f.Path]; !ok {
			order = append(order, f.Path)
		}
		byPath[f.Path] = f
	}
	out := base
	if delta.RunID != "" {
		out.RunID = delta.RunID
	}
	if delta.Source != "" {
		out.Source = delta.Source
	}
	if delta.Model != "" {
		out.Model = delta.Model
	}
	if strings.TrimSpace(delta.CommitMessage) != "" {
		out.CommitMessage = delta.CommitMessage
	}
	out.RecordedAt = delta.RecordedAt
	if out.RecordedAt.IsZero() {
		out.RecordedAt = nowUTC()
	}
	out.Files = make([]FileChange, 0, len(order))
	for _, p := range order {
		out.Files = append(out.Files, byPath[p])
	}
	return out
}

// PutChangeSetMerged reads existing changes, merges, and writes.
func (s *Store) PutChangeSetMerged(projectID, specID string, delta ChangeSet) error {
	base, err := s.GetChangeSet(projectID, specID)
	if err != nil {
		return err
	}
	return s.PutChangeSet(projectID, specID, MergeChangeSet(base, delta))
}

// PutChangeSet records the change set produced by a run.
func (s *Store) PutChangeSet(projectID, specID string, cs ChangeSet) error {
	if cs.RecordedAt.IsZero() {
		cs.RecordedAt = nowUTC()
	}
	if cs.Files == nil {
		cs.Files = []FileChange{}
	}
	return s.withProject(projectID, func(p Project) error {
		return s.writeJSON(s.changeSetPath(p, specID), cs)
	})
}

// GetChangeSet reads the recorded change set. A missing file is an empty set, not
// an error: "nothing was produced yet" is a normal state.
func (s *Store) GetChangeSet(projectID, specID string) (ChangeSet, error) {
	var cs ChangeSet
	err := s.withProject(projectID, func(p Project) error {
		if err := s.readJSON(s.changeSetPath(p, specID), &cs); err != nil {
			if os.IsNotExist(err) {
				cs = ChangeSet{Files: []FileChange{}}
				return nil
			}
			return err
		}
		if cs.Files == nil {
			cs.Files = []FileChange{}
		}
		return nil
	})
	return cs, err
}

// ClearChangeSet removes the recorded change set after it has been committed, so
// a second deliver cannot silently re-commit the same work.
func (s *Store) ClearChangeSet(projectID, specID string) error {
	return s.withProject(projectID, func(p Project) error {
		err := os.Remove(s.changeSetPath(p, specID))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	})
}

// pathRejection explains why a path may not be written, or "" when it is safe.
// Rejections are deliberate and fail closed:
//   - absolute paths and .. escape the workspace
//   - .git/ would rewrite repository metadata rather than source
//   - .github/workflows/ is refused because the delivery credential is
//     Contents-write only and never carries workflows; GitHub would reject the
//     push, so it is better to say so before committing
func pathRejection(rel string) string {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "blank path"
	}
	if strings.ContainsAny(rel, "\x00") {
		return "path contains a null byte"
	}
	if strings.Contains(rel, `\`) {
		return "path must use forward slashes"
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return "absolute paths are not allowed"
	}
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean == "." || clean == ".." {
		return "path resolves to the workspace root"
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." {
			return "path escapes the workspace"
		}
	}
	if clean == ".git" || strings.HasPrefix(clean, ".git/") {
		return "writing repository metadata (.git) is not allowed"
	}
	if strings.HasPrefix(clean, ".github/workflows/") {
		return "workflow files are out of scope — the delivery credential has no workflows permission"
	}
	return ""
}

// applyOutcome is the honest result of applying a change set to a workspace.
type applyOutcome struct {
	Applied []string
	Skipped []string
	// Status is set when the apply failed as a whole.
	Status string
}

// applyChangeSet writes the change set into workDir. It validates every path
// against the workspace before touching the filesystem, so a malformed or
// hostile path is refused rather than partially applied.
func applyChangeSet(workDir string, cs ChangeSet, limits changeSetLimits) (applyOutcome, error) {
	var out applyOutcome
	if len(cs.Files) == 0 {
		out.Status = deliveryStatusNoChanges
		return out, fmt.Errorf("the change set contains no files")
	}
	if limits.MaxFiles > 0 && len(cs.Files) > limits.MaxFiles {
		out.Status = deliveryStatusChangeSetTooLarge
		return out, fmt.Errorf("change set has %d files, limit is %d (OPM_DELIVERY_MAX_FILES)",
			len(cs.Files), limits.MaxFiles)
	}

	root, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		out.Status = deliveryStatusWorkspaceUnavailable
		return out, fmt.Errorf("workspace not usable: %w", err)
	}

	total := 0
	for _, f := range cs.Files {
		if reason := pathRejection(f.Path); reason != "" {
			out.Status = deliveryStatusUnsafePath
			return out, fmt.Errorf("refusing %q: %s", f.Path, reason)
		}
		total += len(f.Patch)
		if f.Contents != nil {
			total += len(*f.Contents)
		}
	}
	if limits.MaxBytes > 0 && total > limits.MaxBytes {
		out.Status = deliveryStatusChangeSetTooLarge
		return out, fmt.Errorf("change set is %d bytes, limit is %d (OPM_DELIVERY_MAX_BYTES)",
			total, limits.MaxBytes)
	}

	for _, f := range cs.Files {
		rel := filepath.ToSlash(filepath.Clean(f.Path))
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if !withinRoot(root, abs) {
			out.Status = deliveryStatusUnsafePath
			return out, fmt.Errorf("refusing %q: resolved outside the workspace", f.Path)
		}
		switch f.mode() {
		case "delete":
			if err := os.Remove(abs); err != nil {
				if os.IsNotExist(err) {
					out.Skipped = append(out.Skipped, rel+" (already absent)")
					continue
				}
				out.Status = deliveryStatusApplyFailed
				return out, fmt.Errorf("delete %s: %w", rel, err)
			}
			out.Applied = append(out.Applied, rel)
		case "write":
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				out.Status = deliveryStatusApplyFailed
				return out, fmt.Errorf("create directory for %s: %w", rel, err)
			}
			if err := os.WriteFile(abs, []byte(*f.Contents), 0o644); err != nil {
				out.Status = deliveryStatusApplyFailed
				return out, fmt.Errorf("write %s: %w", rel, err)
			}
			out.Applied = append(out.Applied, rel)
		case "patch":
			if err := applyUnifiedPatch(root, rel, f.Patch); err != nil {
				out.Status = deliveryStatusApplyFailed
				return out, err
			}
			out.Applied = append(out.Applied, rel)
		}
	}
	if len(out.Applied) == 0 {
		out.Status = deliveryStatusNoChanges
		return out, fmt.Errorf("no file in the change set could be applied")
	}
	return out, nil
}

// withinRoot reports whether abs stays inside root after cleaning.
func withinRoot(root, abs string) bool {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	if rel == "." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}

// applyUnifiedPatch runs `git apply` on a single-file unified diff. git's own
// context checking is the point: a patch that does not match the real source
// fails loudly instead of producing a mangled file.
func applyUnifiedPatch(root, rel, patch string) error {
	if !strings.HasSuffix(patch, "\n") {
		patch += "\n"
	}
	tmp, err := os.CreateTemp("", "opm-patch-*.diff")
	if err != nil {
		return fmt.Errorf("patch %s: %w", rel, err)
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	if _, err := tmp.WriteString(patch); err != nil {
		return fmt.Errorf("patch %s: %w", rel, err)
	}
	cmd := exec.Command("git", "apply", "--whitespace=nowarn", tmp.Name())
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("patch %s did not apply: %v (%s)", rel, err,
			strings.TrimSpace(truncateBytes(out, 240)))
	}
	return nil
}

// changeSetFromRunner builds the recorded change set from a runner result.
func changeSetFromRunner(runID string, rr RunnerResult) ChangeSet {
	kept, dropped := normalizeChangeSet(rr.Files)
	cs := ChangeSet{
		RunID:         runID,
		Source:        "model",
		Model:         rr.Model,
		CommitMessage: strings.TrimSpace(rr.CommitMessage),
		RecordedAt:    nowUTC(),
		Files:         kept,
	}
	if len(dropped) > 0 {
		cs.Note = "dropped unusable entries: " + strings.Join(dropped, "; ")
	}
	if len(kept) == 0 && cs.Note == "" {
		cs.Note = "the runner returned no file changes"
	}
	return cs
}

// changeSetSummary is the UI-facing view: paths and modes, never file bodies.
func changeSetSummary(cs ChangeSet) map[string]interface{} {
	files := make([]map[string]interface{}, 0, len(cs.Files))
	for _, f := range cs.Files {
		entry := map[string]interface{}{"path": f.Path, "mode": f.mode()}
		if f.Contents != nil {
			entry["bytes"] = len(*f.Contents)
		} else if f.Patch != "" {
			entry["bytes"] = len(f.Patch)
		}
		files = append(files, entry)
	}
	out := map[string]interface{}{
		"files":         files,
		"fileCount":     len(cs.Files),
		"commitMessage": cs.CommitMessage,
		"source":        cs.Source,
	}
	if cs.Model != "" {
		out["model"] = cs.Model
	}
	if cs.RunID != "" {
		out["runId"] = cs.RunID
	}
	if !cs.RecordedAt.IsZero() {
		out["recordedAt"] = cs.RecordedAt.Format(time.RFC3339)
	}
	if cs.Note != "" {
		out["note"] = cs.Note
	}
	return out
}

// runnerFilesFromJSON decodes the files[] array the runner emits.
func runnerFilesFromJSON(raw json.RawMessage) []FileChange {
	if len(raw) == 0 {
		return nil
	}
	var files []FileChange
	if err := json.Unmarshal(raw, &files); err != nil {
		return nil
	}
	return files
}
