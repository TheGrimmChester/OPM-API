package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	openauth "github.com/TheGrimmChester/open-auth-go"
)

// handleOAMProjects proxies GET OAM /api/projects for the family project switcher.
// Board registry remains on /api/projects — this path is directory-only.
//
// Fail-closed hook (jobs): before enqueueing work for a concrete OAM directory
// id, call the same upstream with ?product=opm and reject when the id is absent
// from projects[]. Skip when PEER_OAM_URL is unset, the project header is
// empty/"all", or the id is an OPM board registry UUID (not an OAM directory
// id) — nested /api/projects/{uuid}/… board/jobs paths are intentionally not
// checked (see requireEnabledOAMProject on POST /api/projects link only).
// Enablement writes stay on OAM only.
func handleOAMProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	base := oamPeerURL()
	if base == "" {
		writeJSON(w, map[string]interface{}{
			"projects":         []interface{}{},
			"peer_unavailable": true,
			"peer":             "oam-api",
			"note":             "Set PEER_OAM_URL to discover OAM directory projects.",
		})
		return
	}
	target := oamProjectsTarget(base, r.URL.Query())
	raw, status, err := proxyOAMProjectsGET(r.Context(), target, r)
	if err != nil {
		writeJSON(w, map[string]interface{}{
			"projects": []interface{}{},
			"error":    err.Error(),
			"peer":     "oam-api",
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(aliasDirectoryIDs(raw, "projects", "project_id"))
}

func oamProjectsTarget(base string, q url.Values) string {
	target := strings.TrimRight(base, "/") + "/api/projects"
	vals := url.Values{}
	if org := strings.TrimSpace(q.Get("organization_id")); org != "" && !strings.EqualFold(org, "all") {
		vals.Set("organization_id", org)
	}
	if product := strings.TrimSpace(q.Get("product")); product != "" {
		vals.Set("product", product)
	}
	if enc := vals.Encode(); enc != "" {
		target += "?" + enc
	}
	return target
}

func aliasDirectoryIDs(raw []byte, listKey, aliasKey string) []byte {
	var payload map[string]interface{}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return raw
	}
	list, ok := payload[listKey].([]interface{})
	if !ok {
		return raw
	}
	for _, item := range list {
		row, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if _, exists := row[aliasKey]; exists {
			continue
		}
		if id, ok := row["id"].(string); ok && id != "" {
			row[aliasKey] = id
		}
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return out
}

// oamDirectoryProject is the subset of an OAM directory row needed to
// provision an OPM board registry entry keyed by the directory id.
type oamDirectoryProject struct {
	ID            string   `json:"id"`
	ProjectID     string   `json:"project_id"`
	Name          string   `json:"name"`
	OrganizationID string  `json:"organization_id"`
	ExternalKey   string   `json:"external_key"`
	ExternalID    string   `json:"external_id"`
	HTMLURL       string   `json:"html_url"`
	DefaultBranch string   `json:"default_branch"`
	ConnectorIDs  []string `json:"connector_ids"`
}

func (p oamDirectoryProject) directoryID() string {
	id := strings.TrimSpace(p.ID)
	if id == "" {
		id = strings.TrimSpace(p.ProjectID)
	}
	return id
}

// lookupOAMDirectoryProject returns the OAM directory row for projectID, or
// (nil, nil) when the id is absent from the product-filtered list.
func lookupOAMDirectoryProject(ctx context.Context, r *http.Request, product, projectID string) (*oamDirectoryProject, error) {
	base := oamPeerURL()
	if base == "" {
		return nil, fmt.Errorf("PEER_OAM_URL unset")
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, fmt.Errorf("project id required")
	}
	ok, err := oamDirectoryHasProject(ctx, r, base, product, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	q := url.Values{}
	q.Set("product", product)
	if org := strings.TrimSpace(r.Header.Get("X-Organization-ID")); org != "" && !strings.EqualFold(org, "all") {
		q.Set("organization_id", org)
	}
	raw, status, err := proxyOAMProjectsGET(ctx, oamProjectsTarget(base, q), r)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("oam returned %d", status)
	}
	var payload struct {
		Projects []oamDirectoryProject `json:"projects"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	for i := range payload.Projects {
		row := &payload.Projects[i]
		if row.directoryID() == projectID {
			return row, nil
		}
	}
	return nil, nil
}

// fillProjectSCMFromOAM copies connector_ids[0] / external_key (and empty
// metadata) from the OAM directory when the client omitted them. Fail closed
// when PEER_OAM_URL is set and the concrete project has no connector or repo.
func fillProjectSCMFromOAM(r *http.Request, in Project) (Project, int, string) {
	needConnector := strings.TrimSpace(in.ConnectorID) == ""
	needOwner := strings.TrimSpace(normalizeOwnerRepo(in.OwnerRepo)) == ""
	if !needConnector && !needOwner {
		return in, 0, ""
	}
	if oamPeerURL() == "" {
		if needConnector || needOwner {
			return in, 400, "connectorId and ownerRepo required when PEER_OAM_URL is unset"
		}
		return in, 0, ""
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = strings.TrimSpace(r.Header.Get("X-Project-ID"))
	}
	if id == "" || strings.EqualFold(id, "all") {
		return in, 400, "oam project id required to resolve connector from directory"
	}
	dir, err := lookupOAMDirectoryProject(r.Context(), r, "opm", id)
	if err != nil {
		return in, 503, "could not load OAM directory project: " + err.Error()
	}
	if dir == nil {
		return in, 404, "project not found in OAM directory"
	}
	filled := boardProjectFromOAM(dir, in.OrganizationID)
	if needConnector {
		in.ConnectorID = filled.ConnectorID
	}
	if needOwner {
		in.OwnerRepo = filled.OwnerRepo
	}
	if strings.TrimSpace(in.Name) == "" {
		in.Name = filled.Name
	}
	if strings.TrimSpace(in.GithubRepoID) == "" {
		in.GithubRepoID = filled.GithubRepoID
	}
	if strings.TrimSpace(in.HTMLURL) == "" {
		in.HTMLURL = filled.HTMLURL
	}
	if strings.TrimSpace(in.DefaultBranch) == "" {
		in.DefaultBranch = filled.DefaultBranch
	}
	if strings.TrimSpace(in.ConnectorID) == "" {
		return in, 400, "project has no connector_ids; attach a connector in Account Manager"
	}
	if strings.TrimSpace(normalizeOwnerRepo(in.OwnerRepo)) == "" {
		return in, 400, "project has no external_key (owner/repo); import a GitHub project in Account Manager"
	}
	return in, 0, ""
}

func boardProjectFromOAM(dir *oamDirectoryProject, requestOrg string) Project {
	id := dir.directoryID()
	name := strings.TrimSpace(dir.Name)
	if name == "" {
		name = id
	}
	ownerRepo := normalizeOwnerRepo(dir.ExternalKey)
	connectorID := ""
	if len(dir.ConnectorIDs) > 0 {
		connectorID = strings.TrimSpace(dir.ConnectorIDs[0])
	}
	// Stamp the caller's org (personal → empty). Do not copy the directory's
	// organization_id — personal JWTs would otherwise create default-org rows
	// that ListProjectsForOrg hides.
	return Project{
		ID:             id,
		Name:           name,
		OwnerRepo:      ownerRepo,
		GithubRepoID:   strings.TrimSpace(dir.ExternalID),
		ConnectorID:    connectorID,
		OrganizationID: normalizeWriteOrg(requestOrg),
		HTMLURL:        strings.TrimSpace(dir.HTMLURL),
		DefaultBranch:  strings.TrimSpace(dir.DefaultBranch),
	}
}

func proxyOAMProjectsGET(ctx context.Context, target string, r *http.Request) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, err
	}
	if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	} else if c, err := r.Cookie(openauth.CookieName); err == nil && c.Value != "" {
		req.Header.Set("Authorization", "Bearer "+c.Value)
	}
	for _, h := range []string{"X-Organization-ID", "X-Project-ID", "X-Tenant-User-ID"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return raw, resp.StatusCode, nil
}
