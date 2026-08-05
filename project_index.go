package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ProjectIndex matches the planner schema used by AutoCursor-near-parity agents:
// project_type, services, infrastructure, conventions.
type ProjectIndex struct {
	ProjectType    string                     `json:"project_type"`
	Services       map[string]ServiceIndex    `json:"services"`
	Infrastructure InfrastructureIndex        `json:"infrastructure"`
	Conventions    ConventionsIndex           `json:"conventions"`
}

type ServiceIndex struct {
	Path        string   `json:"path"`
	TechStack   []string `json:"tech_stack"`
	DevCommand  string   `json:"dev_command,omitempty"`
	TestCommand string   `json:"test_command,omitempty"`
}

type InfrastructureIndex struct {
	Docker   bool    `json:"docker"`
	Database *string `json:"database"`
}

type ConventionsIndex struct {
	Linter    *string `json:"linter"`
	Formatter *string `json:"formatter"`
	Testing   *string `json:"testing"`
}

// populateProjectIndex derives an index from repo manifests and layout only.
// Extends AutoCursor's Node/Python heuristics with go.mod and Cargo.toml.
func populateProjectIndex(projectDir string) ProjectIndex {
	projectDir = filepath.Clean(projectDir)
	services := inferServices(projectDir)
	return ProjectIndex{
		ProjectType:    inferProjectType(projectDir),
		Services:       services,
		Infrastructure: inferInfrastructure(projectDir),
		Conventions:    inferConventions(projectDir),
	}
}

// ensureProjectIndex refreshes the index when a real clone is available.
// Writes JSON under outPath (typically runner-in/project_index.json), never
// under a durable customer .auto-cursor tree.
func ensureProjectIndex(projectDir, outPath string) (ProjectIndex, error) {
	idx := populateProjectIndex(projectDir)
	if outPath == "" {
		return idx, nil
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return idx, err
	}
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return idx, err
	}
	tmp := outPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return idx, err
	}
	if err := os.Rename(tmp, outPath); err != nil {
		_ = os.Remove(tmp)
		return idx, err
	}
	return idx, nil
}

func inferProjectType(projectDir string) string {
	serviceLike := map[string]bool{
		"backend": true, "frontend": true, "api": true, "web": true,
		"app": true, "services": true, "packages": true,
	}
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return "single"
	}
	found := 0
	for _, e := range entries {
		if e.IsDir() && serviceLike[e.Name()] {
			found++
		}
	}
	if found >= 2 {
		return "monorepo"
	}
	return "single"
}

func inferServices(projectDir string) map[string]ServiceIndex {
	services := map[string]ServiceIndex{}

	addRoot := func(name string, tech []string, dev, test string) {
		if name == "" || name == "root" {
			name = "app"
		}
		services[name] = ServiceIndex{
			Path: ".", TechStack: uniqueStrings(tech), DevCommand: dev, TestCommand: test,
		}
	}

	rootPkg := readJSONMap(filepath.Join(projectDir, "package.json"))
	rootPy := readPyProjectHints(filepath.Join(projectDir, "pyproject.toml"))
	hasGo := fileExists(filepath.Join(projectDir, "go.mod"))
	hasCargo := fileExists(filepath.Join(projectDir, "Cargo.toml"))
	hasAndroid := fileExists(filepath.Join(projectDir, "build.gradle")) ||
		fileExists(filepath.Join(projectDir, "build.gradle.kts")) ||
		fileExists(filepath.Join(projectDir, "settings.gradle")) ||
		fileExists(filepath.Join(projectDir, "settings.gradle.kts")) ||
		dirExists(filepath.Join(projectDir, "app")) && (fileExists(filepath.Join(projectDir, "app", "build.gradle")) ||
			fileExists(filepath.Join(projectDir, "app", "build.gradle.kts")))

	if rootPkg != nil || rootPy != nil || hasGo || hasCargo || hasAndroid {
		tech := []string{}
		dev, test := "", ""
		if rootPkg != nil {
			tech = append(tech, "node")
			scripts, _ := rootPkg["scripts"].(map[string]any)
			dev = firstScript(scripts, "dev", "start")
			if dev == "" {
				dev = "npm run dev"
			}
			test = firstScript(scripts, "test")
			if test == "" {
				test = "npm test"
			}
		}
		if rootPy != nil {
			tech = append(tech, "python")
			if dev == "" {
				if fileExists(filepath.Join(projectDir, "main.py")) {
					dev = "uvicorn main:app --reload"
				} else {
					dev = "python -m pytest"
				}
			}
			if test == "" {
				test = "pytest"
			}
		}
		if hasGo {
			tech = append(tech, "go")
			if dev == "" {
				dev = "go run ."
			}
			if test == "" {
				test = "go test ./..."
			}
		}
		if hasCargo {
			tech = append(tech, "rust")
			if dev == "" {
				dev = "cargo run"
			}
			if test == "" {
				test = "cargo test"
			}
		}
		if hasAndroid {
			tech = append(tech, "android", "kotlin")
			if dev == "" {
				dev = "./gradlew assembleDebug"
			}
			if test == "" {
				test = "./gradlew test"
			}
		}
		name := "app"
		switch {
		case len(tech) >= 1 && tech[0] == "android":
			name = "android"
		case len(tech) == 1 && tech[0] == "node":
			name = "node"
		case len(tech) == 1 && tech[0] == "python":
			name = "python"
		case len(tech) == 1 && tech[0] == "go":
			name = "go"
		case len(tech) == 1 && tech[0] == "rust":
			name = "rust"
		case containsString(tech, "android"):
			name = "android"
		}
		addRoot(name, tech, dev, test)
	}

	entries, _ := os.ReadDir(projectDir)
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		sub := filepath.Join(projectDir, e.Name())
		spkg := readJSONMap(filepath.Join(sub, "package.json"))
		spy := readPyProjectHints(filepath.Join(sub, "pyproject.toml"))
		sGo := fileExists(filepath.Join(sub, "go.mod"))
		sCargo := fileExists(filepath.Join(sub, "Cargo.toml"))
		if spkg == nil && spy == nil && !sGo && !sCargo {
			continue
		}
		tech := []string{}
		dev, test := "", ""
		if spkg != nil {
			tech = append(tech, "node")
			scripts, _ := spkg["scripts"].(map[string]any)
			dev = firstScript(scripts, "dev", "start")
			if dev == "" {
				dev = "npm run dev"
			}
			test = firstScript(scripts, "test")
			if test == "" {
				test = "npm test"
			}
		}
		if spy != nil {
			tech = append(tech, "python")
			if dev == "" {
				dev = "python -m pytest"
			}
			if test == "" {
				test = "pytest"
			}
		}
		if sGo {
			tech = append(tech, "go")
			if test == "" {
				test = "go test ./..."
			}
		}
		if sCargo {
			tech = append(tech, "rust")
			if test == "" {
				test = "cargo test"
			}
		}
		key := e.Name()
		if _, ok := services[key]; !ok {
			services[key] = ServiceIndex{
				Path: key, TechStack: uniqueStrings(tech), DevCommand: dev, TestCommand: test,
			}
		}
	}

	if len(services) == 0 {
		services["app"] = ServiceIndex{Path: ".", TechStack: []string{"unknown"}}
	}
	return services
}

func inferInfrastructure(projectDir string) InfrastructureIndex {
	out := InfrastructureIndex{Docker: false, Database: nil}
	for _, name := range []string{
		"Dockerfile", "docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml",
	} {
		if fileExists(filepath.Join(projectDir, name)) {
			out.Docker = true
			break
		}
	}
	// Lightweight DB heuristic: scan a few shallow text files for common drivers.
	db := detectDatabaseHint(projectDir)
	if db != "" {
		out.Database = &db
	}
	return out
}

func detectDatabaseHint(projectDir string) string {
	candidates := []string{
		"go.mod", "package.json", "pyproject.toml", "Cargo.toml",
		"requirements.txt", "composer.json",
	}
	for _, name := range candidates {
		b, err := os.ReadFile(filepath.Join(projectDir, name))
		if err != nil {
			continue
		}
		t := strings.ToLower(string(b))
		switch {
		case strings.Contains(t, "postgresql") || strings.Contains(t, "psycopg") || strings.Contains(t, "lib/pq"):
			return "postgresql"
		case strings.Contains(t, "sqlite"):
			return "sqlite"
		case strings.Contains(t, "mysql") || strings.Contains(t, "pymysql"):
			return "mysql"
		case strings.Contains(t, "mongodb") || strings.Contains(t, "mongo-driver"):
			return "mongodb"
		case strings.Contains(t, "redis"):
			return "redis"
		case strings.Contains(t, "clickhouse"):
			return "clickhouse"
		}
	}
	return ""
}

func inferConventions(projectDir string) ConventionsIndex {
	out := ConventionsIndex{}
	py := readPyProjectHints(filepath.Join(projectDir, "pyproject.toml"))
	if py != nil {
		if _, ok := py["ruff"]; ok {
			s := "ruff"
			out.Linter = &s
		}
		if _, ok := py["black"]; ok {
			s := "black"
			out.Formatter = &s
		}
		if _, ok := py["pytest"]; ok {
			s := "pytest"
			out.Testing = &s
		}
	}
	if pkg := readJSONMap(filepath.Join(projectDir, "package.json")); pkg != nil {
		dev, _ := pkg["devDependencies"].(map[string]any)
		if dev == nil {
			dev, _ = pkg["dependencies"].(map[string]any)
		}
		if dev != nil {
			if _, ok := dev["eslint"]; ok && out.Linter == nil {
				s := "eslint"
				out.Linter = &s
			}
			if _, ok := dev["prettier"]; ok && out.Formatter == nil {
				s := "prettier"
				out.Formatter = &s
			}
			if _, ok := dev["vitest"]; ok && out.Testing == nil {
				s := "vitest"
				out.Testing = &s
			} else if _, ok := dev["jest"]; ok && out.Testing == nil {
				s := "jest"
				out.Testing = &s
			}
		}
	}
	if fileExists(filepath.Join(projectDir, "go.mod")) && out.Testing == nil {
		s := "go test"
		out.Testing = &s
	}
	if fileExists(filepath.Join(projectDir, ".golangci.yml")) || fileExists(filepath.Join(projectDir, ".golangci.yaml")) {
		s := "golangci-lint"
		out.Linter = &s
	}
	return out
}

func readJSONMap(path string) map[string]any {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

// readPyProjectHints returns a lightweight tool map without a full TOML parser.
func readPyProjectHints(path string) map[string]any {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	text := string(b)
	tools := map[string]any{}
	if strings.Contains(text, "[tool.ruff]") {
		tools["ruff"] = map[string]any{}
	}
	if strings.Contains(text, "[tool.black]") {
		tools["black"] = map[string]any{}
	}
	if strings.Contains(text, "[tool.pytest") {
		tools["pytest"] = map[string]any{"ini_options": map[string]any{}}
	}
	if len(tools) == 0 {
		return nil
	}
	return tools
}

func firstScript(scripts map[string]any, keys ...string) string {
	if scripts == nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := scripts[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st != nil && !st.IsDir()
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st != nil && st.IsDir()
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return []string{"unknown"}
	}
	return out
}

// readCursorMd returns truncated CURSOR.md / Claude.md content for prompt prepend.
func readCursorMd(projectDir string, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = 8000
	}
	var parts []string
	for _, name := range []string{"CURSOR.md", "Claude.md"} {
		b, err := os.ReadFile(filepath.Join(projectDir, name))
		if err != nil {
			continue
		}
		s := strings.TrimSpace(string(b))
		if s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	joined := strings.Join(parts, "\n\n---\n\n")
	if len(joined) > maxBytes {
		return joined[:maxBytes] + "…"
	}
	return joined
}

// readRepoDocExcerpt reads a small markdown/doc file from the clone when present.
func readRepoDocExcerpt(projectDir, rel string, maxBytes int) string {
	rel = strings.TrimSpace(strings.TrimPrefix(filepath.ToSlash(rel), "./"))
	if rel == "" || strings.Contains(rel, "..") || filepath.IsAbs(rel) {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(projectDir, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if maxBytes > 0 && len(s) > maxBytes {
		return s[:maxBytes] + "…"
	}
	return s
}
