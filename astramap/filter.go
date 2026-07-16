package astramap

import (
	"bufio"
	"bytes"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type IndexStage string

const (
	StageDetect     IndexStage = "detect"
	StageScip       IndexStage = "scip"
	StageTreeSitter IndexStage = "tree-sitter"
	StageHeuristic  IndexStage = "heuristic"
)

// ===== Exclude Rule Types =====

type ExcludeKind string

const (
	ExcludeVCSMetadata     ExcludeKind = "VCS_METADATA"
	ExcludeHiddenPath      ExcludeKind = "HIDDEN_PATH"
	ExcludeDependency      ExcludeKind = "DEPENDENCY"
	ExcludeBuildArtifact   ExcludeKind = "BUILD_ARTIFACT"
	ExcludeGeneratedSource ExcludeKind = "GENERATED_SOURCE"
	ExcludeCache           ExcludeKind = "CACHE"
	ExcludeMinified        ExcludeKind = "MINIFIED"
	ExcludeBinary          ExcludeKind = "BINARY"
	ExcludeUserConfigured  ExcludeKind = "USER_CONFIGURED"
)

// ExcludeRule describes a built-in exclude rule
type ExcludeRule struct {
	ID          string
	Description string
	Ecosystem   string   // "" = universal
	Match       []string // glob patterns
	Kind        ExcludeKind
	Confidence  int  // 0-100
	Overridable bool // whether it can be overridden by forceInclude
}

// FileIndexPlan describes the index evaluation result of a single file
type FileIndexPlan struct {
	Path       string
	Indexed    bool
	RuleID     string
	Kind       ExcludeKind
	Reason     string
	Overridden bool
}

// ===== Built-in Universal Rule Set =====

var builtInRules = []ExcludeRule{
	// --- VCS Metadata (Non-overridable) ---
	{ID: "vcs.git", Description: "Git repository metadata", Match: []string{".git/**"}, Kind: ExcludeVCSMetadata, Confidence: 100, Overridable: false},
	{ID: "vcs.svn", Description: "SVN repository metadata", Match: []string{".svn/**"}, Kind: ExcludeVCSMetadata, Confidence: 100, Overridable: false},
	{ID: "vcs.hg", Description: "Mercurial repository metadata", Match: []string{".hg/**"}, Kind: ExcludeVCSMetadata, Confidence: 100, Overridable: false},
	{ID: "vcs.astramap", Description: "AstraMap index data", Match: []string{".astramap/**"}, Kind: ExcludeVCSMetadata, Confidence: 100, Overridable: false},

	// --- Third-party Dependencies ---
	{ID: "dep.node_modules", Description: "Node.js dependencies", Match: []string{"**/node_modules/**"}, Kind: ExcludeDependency, Confidence: 100, Overridable: true},
	{ID: "dep.bower_components", Description: "Bower dependencies", Match: []string{"**/bower_components/**"}, Kind: ExcludeDependency, Confidence: 100, Overridable: true},
	{ID: "dep.pods", Description: "CocoaPods dependencies", Match: []string{"**/Pods/**"}, Kind: ExcludeDependency, Confidence: 100, Overridable: true},
	{ID: "dep.vendor", Description: "Vendor dependencies", Match: []string{"vendor/**"}, Kind: ExcludeDependency, Confidence: 95, Overridable: true},
	{ID: "dep.third_party", Description: "Third-party code", Match: []string{"third_party/**"}, Kind: ExcludeDependency, Confidence: 90, Overridable: true},

	// --- Caches ---
	{ID: "cache.pycache", Description: "Python cache", Match: []string{"**/__pycache__/**", "**/*.pyc", "**/*.pyo"}, Kind: ExcludeCache, Confidence: 100, Overridable: true},
	{ID: "cache.pytest", Description: "pytest cache", Match: []string{"**/.pytest_cache/**"}, Kind: ExcludeCache, Confidence: 100, Overridable: true},
	{ID: "cache.mypy", Description: "mypy cache", Match: []string{"**/.mypy_cache/**"}, Kind: ExcludeCache, Confidence: 100, Overridable: true},
	{ID: "cache.ruff", Description: "ruff cache", Match: []string{"**/.ruff_cache/**"}, Kind: ExcludeCache, Confidence: 100, Overridable: true},
	{ID: "cache.generic", Description: "Generic cache", Match: []string{"**/.cache/**", "**/.sass-cache/**"}, Kind: ExcludeCache, Confidence: 95, Overridable: true},

	// --- OS/Editor Junk ---
	{ID: "os.ds_store", Description: "macOS .DS_Store", Match: []string{"**/.DS_Store"}, Kind: ExcludeCache, Confidence: 100, Overridable: true},
	{ID: "os.thumbs", Description: "Windows Thumbs.db", Match: []string{"**/Thumbs.db"}, Kind: ExcludeCache, Confidence: 100, Overridable: true},
	{ID: "os.swap", Description: "Editor swap files", Match: []string{"**/*.swp", "**/*.swo", "**/*~"}, Kind: ExcludeCache, Confidence: 100, Overridable: true},
	{ID: "os.tmp", Description: "Temporary files", Match: []string{"**/*.tmp", "**/*.temp"}, Kind: ExcludeCache, Confidence: 100, Overridable: true},

	// --- Minified/Source Maps ---
	{ID: "minified.js", Description: "Minified JS/CSS", Match: []string{"**/*.min.js", "**/*.min.mjs", "**/*.min.cjs", "**/*.min.css"}, Kind: ExcludeMinified, Confidence: 100, Overridable: true},
	{ID: "sourcemap", Description: "Source Map files", Match: []string{"**/*.js.map", "**/*.css.map", "**/*.d.ts.map"}, Kind: ExcludeMinified, Confidence: 100, Overridable: true},

	// --- Generated Sources: Deterministic Filenames ---
	{ID: "gen.go.protobuf", Description: "Go protobuf generated files", Ecosystem: "go", Match: []string{"**/*.pb.go", "**/*.grpc.pb.go"}, Kind: ExcludeGeneratedSource, Confidence: 100, Overridable: true},
	{ID: "gen.py.protobuf", Description: "Python protobuf generated files", Ecosystem: "python", Match: []string{"**/*_pb2.py", "**/*_pb2.pyi", "**/*_pb2_grpc.py"}, Kind: ExcludeGeneratedSource, Confidence: 100, Overridable: true},
	{ID: "gen.cc.protobuf", Description: "C++ protobuf generated files", Ecosystem: "cpp", Match: []string{"**/*.pb.cc", "**/*.pb.h", "**/*.grpc.pb.cc", "**/*.grpc.pb.h"}, Kind: ExcludeGeneratedSource, Confidence: 100, Overridable: true},
	{ID: "gen.cc.flatbuffers", Description: "FlatBuffers generated files", Ecosystem: "cpp", Match: []string{"**/*_generated.h"}, Kind: ExcludeGeneratedSource, Confidence: 100, Overridable: true},
	{ID: "gen.dart.all", Description: "Dart generated files", Ecosystem: "dart", Match: []string{"**/*.g.dart", "**/*.freezed.dart", "**/*.gr.dart", "**/*.mocks.dart", "**/*.pb.dart"}, Kind: ExcludeGeneratedSource, Confidence: 100, Overridable: true},
	{ID: "gen.rb.protobuf", Description: "Ruby protobuf generated files", Ecosystem: "ruby", Match: []string{"**/*_pb.rb"}, Kind: ExcludeGeneratedSource, Confidence: 100, Overridable: true},

	// --- Binary Files (Non-overridable) ---
	{ID: "binary.object", Description: "Compiled object files", Match: []string{"**/*.o", "**/*.obj", "**/*.a", "**/*.lib", "**/*.so", "**/*.dylib", "**/*.dll", "**/*.exe"}, Kind: ExcludeBinary, Confidence: 100, Overridable: false},
	{ID: "binary.java", Description: "Java archives", Match: []string{"**/*.class", "**/*.jar", "**/*.war", "**/*.ear"}, Kind: ExcludeBinary, Confidence: 100, Overridable: false},
	{ID: "binary.wasm", Description: "WebAssembly", Match: []string{"**/*.wasm"}, Kind: ExcludeBinary, Confidence: 100, Overridable: false},
	{ID: "binary.archive", Description: "Compressed archives", Match: []string{"**/*.zip", "**/*.tar", "**/*.gz", "**/*.7z"}, Kind: ExcludeBinary, Confidence: 100, Overridable: false},
	{ID: "binary.image", Description: "Image files", Match: []string{"**/*.png", "**/*.jpg", "**/*.jpeg", "**/*.gif", "**/*.pdf", "**/*.ico", "**/*.svg", "**/*.webp"}, Kind: ExcludeBinary, Confidence: 100, Overridable: false},
	{ID: "binary.model", Description: "Model files", Match: []string{"**/*.tflite", "**/*.onnx", "**/*.pt"}, Kind: ExcludeBinary, Confidence: 100, Overridable: false},
	{ID: "binary.font", Description: "Font files", Match: []string{"**/*.woff", "**/*.woff2", "**/*.ttf", "**/*.eot"}, Kind: ExcludeBinary, Confidence: 100, Overridable: false},
}

// ===== Generated File Header Detection =====

var generatedHeaderPatterns = []string{
	`(?i)^// Code generated .* DO NOT EDIT\.?$`,
	`(?i)^// Generated by .* DO NOT EDIT\.?$`,
	`(?i)This file (was )?automatically generated`,
	`(?i)This file is auto-generated`,
	`(?i)DO NOT EDIT THIS FILE`,
	`(?i)<auto-generated>`,
}

var compiledGenHeaderPats = compileGenHeaderPats()

func compileGenHeaderPats() []matchPattern {
	pats := make([]matchPattern, len(generatedHeaderPatterns))
	for i, p := range generatedHeaderPatterns {
		pats[i] = matchPattern{pattern: p, re: mustCompileRegex(p)}
	}
	return pats
}

type matchPattern struct {
	pattern string
	re      interface{ MatchString(string) bool }
}

// IsGeneratedByHeader scans the first 8KB / 100 lines of the file to detect generated file headers
func IsGeneratedByHeader(content []byte) bool {
	const maxScan = 8 * 1024
	if len(content) > maxScan {
		content = content[:maxScan]
	}
	scanner := bufio.NewScanner(bytes.NewReader(content))
	lineCount := 0
	for scanner.Scan() {
		lineCount++
		if lineCount > 100 {
			break
		}
		line := scanner.Text()
		for _, pat := range compiledGenHeaderPats {
			if pat.re.MatchString(line) {
				return true
			}
		}
	}
	return false
}

// ===== IndexFilter =====

type IndexFilter struct {
	Include      []string
	Exclude      []string
	ForceInclude []string // advanced.forceInclude — overrides overridable built-in rules

	// Ecosystem-aware dynamic rules (generated by DetectProjectRoots)
	ecosystemRules []ExcludeRule
}

type IndexFilterMatchReport struct {
	Excluded []IndexFilterExcludedEntry
}

type IndexFilterExcludedEntry struct {
	Path   string
	RuleID string
	Kind   ExcludeKind
	Reason string
}

// Evaluate evaluates whether a file should be indexed, returning a FileIndexPlan
func (f *IndexFilter) Evaluate(relPath string) FileIndexPlan {
	relPath = normalizeFilterPath(relPath)
	if relPath == "" || relPath == "." {
		return FileIndexPlan{Path: relPath, Indexed: true}
	}

	// 1. Built-in rules (including ecosystem-aware rules)
	allRules := builtInRules
	if len(f.ecosystemRules) > 0 {
		allRules = append(allRules, f.ecosystemRules...)
	}
	for _, rule := range allRules {
		if matchesAnyPattern(relPath, rule.Match) {
			// forceInclude can override Overridable rules
			if rule.Overridable && len(f.ForceInclude) > 0 && matchesAnyPattern(relPath, f.ForceInclude) {
				return FileIndexPlan{Path: relPath, Indexed: true, RuleID: rule.ID, Kind: rule.Kind, Reason: rule.Description, Overridden: true}
			}
			return FileIndexPlan{Path: relPath, Indexed: false, RuleID: rule.ID, Kind: rule.Kind, Reason: rule.Description}
		}
	}

	// 2. Hidden paths
	if hasHiddenSegment(relPath) {
		if len(f.ForceInclude) > 0 && matchesAnyPattern(relPath, f.ForceInclude) {
			return FileIndexPlan{Path: relPath, Indexed: true, Kind: ExcludeHiddenPath, Overridden: true}
		}
		return FileIndexPlan{Path: relPath, Indexed: false, RuleID: "hidden.dotdir", Kind: ExcludeHiddenPath, Reason: "Hidden path"}
	}

	// 3. User Include
	if len(f.Include) > 0 && !matchesAnyPattern(relPath, f.Include) {
		return FileIndexPlan{Path: relPath, Indexed: false, RuleID: "user.include", Kind: ExcludeUserConfigured, Reason: "Not in include range"}
	}

	// 4. User Exclude
	if matchesAnyPattern(relPath, f.Exclude) {
		return FileIndexPlan{Path: relPath, Indexed: false, RuleID: "user.exclude", Kind: ExcludeUserConfigured, Reason: "User configured exclusion"}
	}

	return FileIndexPlan{Path: relPath, Indexed: true}
}

// EvaluateDir evaluates whether a directory should be traversed
func (f *IndexFilter) EvaluateDir(relPath string) FileIndexPlan {
	relPath = normalizeFilterPath(relPath)
	if relPath == "" || relPath == "." {
		return FileIndexPlan{Path: relPath, Indexed: true}
	}

	// Check the directory itself first
	plan := f.Evaluate(relPath)
	if !plan.Indexed && !plan.Overridden {
		return plan
	}

	// Then check directory match with trailing /
	planSlash := f.Evaluate(relPath + "/")
	if !planSlash.Indexed && !planSlash.Overridden {
		return planSlash
	}

	return FileIndexPlan{Path: relPath, Indexed: true}
}

// Allows maintains backward compatibility
func (f *IndexFilter) Allows(relPath string, stage IndexStage) bool {
	return f.Evaluate(relPath).Indexed
}

// AllowsDir maintains backward compatibility
func (f *IndexFilter) AllowsDir(relPath string, stage IndexStage) bool {
	return f.EvaluateDir(relPath).Indexed
}

// ===== Ecosystem-aware Project Root Detection =====

type ProjectRoot struct {
	Path      string // Path relative to the project root
	Ecosystem string // "go", "node", "rust", "maven", "gradle", "dotnet", "cmake", "python", "swift", "bazel"
}

var rootMarkers = map[string]string{
	"go.mod":           "go",
	"go.work":          "go",
	"package.json":     "node",
	"Cargo.toml":       "rust",
	"pom.xml":          "maven",
	"build.gradle":     "gradle",
	"build.gradle.kts": "gradle",
	"settings.gradle":  "gradle",
	"CMakeLists.txt":   "cmake",
	"pyproject.toml":   "python",
	"setup.py":         "python",
	"Package.swift":    "swift",
	"WORKSPACE":        "bazel",
	"WORKSPACE.bazel":  "bazel",
	"MODULE.bazel":     "bazel",
}

// DetectProjectRoots scans the project directory to establish project root mappings
func DetectProjectRoots(projectRoot string) []ProjectRoot {
	var roots []ProjectRoot
	_ = filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		relPath, _ := filepath.Rel(projectRoot, path)
		relPath = filepath.ToSlash(relPath)

		if info.IsDir() {
			// Skip hidden directories and common non-source directories
			name := info.Name()
			if strings.HasPrefix(name, ".") && name != "." && name != ".." {
				return filepath.SkipDir
			}
			switch name {
			case "node_modules", "vendor", "third_party", "Pods", "bower_components":
				return filepath.SkipDir
			}
			return nil
		}

		if ecosystem, ok := rootMarkers[info.Name()]; ok {
			dir := filepath.Dir(relPath)
			if dir == "." {
				dir = ""
			}
			dir = filepath.ToSlash(dir)
			roots = append(roots, ProjectRoot{Path: dir, Ecosystem: ecosystem})
		}
		return nil
	})
	return roots
}

// BuildEcosystemRules generates ecosystem-aware exclude rules based on project roots
func BuildEcosystemRules(roots []ProjectRoot) []ExcludeRule {
	var rules []ExcludeRule
	seen := make(map[string]bool)
	for _, root := range roots {
		prefix := root.Path
		if prefix != "" {
			prefix += "/"
		}
		key := func(id string) string { return id + "@" + root.Path }
		switch root.Ecosystem {
		case "node":
			if !seen[key("build.node.dist")] {
				rules = append(rules, ExcludeRule{ID: "build.node.dist", Description: "Node.js build output", Match: []string{prefix + "dist/**", prefix + "coverage/**"}, Kind: ExcludeBuildArtifact, Confidence: 100, Overridable: true})
				seen[key("build.node.dist")] = true
			}
		case "rust":
			if !seen[key("build.rust.target")] {
				rules = append(rules, ExcludeRule{ID: "build.rust.target", Description: "Rust build output", Match: []string{prefix + "target/**"}, Kind: ExcludeBuildArtifact, Confidence: 100, Overridable: true})
				seen[key("build.rust.target")] = true
			}
		case "maven":
			if !seen[key("build.maven.target")] {
				rules = append(rules, ExcludeRule{ID: "build.maven.target", Description: "Maven build output", Match: []string{prefix + "target/**"}, Kind: ExcludeBuildArtifact, Confidence: 100, Overridable: true})
				seen[key("build.maven.target")] = true
			}
		case "gradle":
			if !seen[key("build.gradle.output")] {
				rules = append(rules, ExcludeRule{ID: "build.gradle.output", Description: "Gradle build output", Match: []string{prefix + "build/**"}, Kind: ExcludeBuildArtifact, Confidence: 100, Overridable: true})
				seen[key("build.gradle.output")] = true
			}
		case "cmake":
			if !seen[key("build.cmake.output")] {
				rules = append(rules, ExcludeRule{ID: "build.cmake.output", Description: "CMake build output", Match: []string{prefix + "cmake-build-*/**"}, Kind: ExcludeBuildArtifact, Confidence: 100, Overridable: true})
				seen[key("build.cmake.output")] = true
			}
		case "bazel":
			if !seen[key("build.bazel.output")] {
				rules = append(rules, ExcludeRule{ID: "build.bazel.output", Description: "Bazel build output", Match: []string{prefix + "bazel-*/**"}, Kind: ExcludeBuildArtifact, Confidence: 100, Overridable: true})
				seen[key("build.bazel.output")] = true
			}
		case "swift":
			if !seen[key("build.swift.output")] {
				rules = append(rules, ExcludeRule{ID: "build.swift.output", Description: "Swift build output", Match: []string{prefix + ".build/**"}, Kind: ExcludeBuildArtifact, Confidence: 100, Overridable: true})
				seen[key("build.swift.output")] = true
			}
		}
	}
	return rules
}

// ===== Configuration Loading =====

func EnsureIndexConfigExample(projectRoot string) (string, bool, error) {
	configPath := filepath.Join(projectRoot, ".astramap", "config.yaml")
	if _, err := os.Stat(configPath); err == nil {
		return configPath, false, nil
	} else if !os.IsNotExist(err) {
		return "", false, err
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return "", false, err
	}
	if err := os.WriteFile(configPath, []byte(indexConfigExample), 0644); err != nil {
		return "", false, err
	}
	return configPath, true, nil
}

func LoadIndexFilter(projectRoot string) (*IndexFilter, error) {
	filter := &IndexFilter{}
	configPath := filepath.Join(projectRoot, ".astramap", "config.yaml")
	file, err := os.Open(configPath)
	if os.IsNotExist(err) {
		return filter, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	inIndex := false
	inAdvanced := false
	var current string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		rawLine := scanner.Text()
		line := stripConfigComment(rawLine)

		// Key tracking in comments
		if line != rawLine {
			commentPart := strings.TrimSpace(strings.SplitN(rawLine, "#", 2)[1])
			if strings.Contains(commentPart, ":") {
				commentKey, _, ok := strings.Cut(commentPart, ":")
				if ok {
					normalized := normalizeIndexConfigKey(commentKey)
					if isValidConfigKey(normalized) {
						current = normalized
					}
				}
			}
		}

		if strings.TrimSpace(line) == "" {
			continue
		}

		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent == 0 {
			inIndex = trimmed == "index:"
			inAdvanced = false
			current = ""
			continue
		}
		if !inIndex {
			continue
		}

		// advanced: nested keys
		if indent > 0 && !inAdvanced {
			advKey, _, advOk := strings.Cut(trimmed, ":")
			if advOk && normalizeIndexConfigKey(advKey) == "advanced" {
				inAdvanced = true
				current = ""
				continue
			}
		}

		if inAdvanced && indent <= 2 {
			inAdvanced = false
		}

		if strings.HasPrefix(trimmed, "-") {
			if current == "" {
				continue
			}
			addIndexPattern(filter, current, parseConfigValue(strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))))
			continue
		}

		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		current = normalizeIndexConfigKey(key)
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		for _, item := range parseConfigList(value) {
			addIndexPattern(filter, current, item)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Backward compatibility: scipExclude / treeSitterExclude in old config are merged into Exclude
	// (These fields are written directly to Exclude in addIndexPattern)

	// Load .gitignore
	gitIgnorePath := filepath.Join(projectRoot, ".gitignore")
	if gitIgnoreFile, err := os.Open(gitIgnorePath); err == nil {
		defer gitIgnoreFile.Close()
		gitIgnoreScanner := bufio.NewScanner(gitIgnoreFile)
		for gitIgnoreScanner.Scan() {
			line := strings.TrimSpace(gitIgnoreScanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			pattern := convertGitIgnoreToGlob(line)
			if pattern != "" {
				filter.Exclude = append(filter.Exclude, normalizeFilterPath(pattern))
			}
		}
	}

	// Ecosystem-aware
	roots := DetectProjectRoots(projectRoot)
	filter.ecosystemRules = BuildEcosystemRules(roots)

	return filter, nil
}

func IndexConfigExample() string {
	return indexConfigExample
}

const indexConfigExample = `# AstraMap index filter config.
# Built-in rules automatically exclude: hidden paths, VCS metadata,
# dependencies, build artifacts, generated code, caches, and binaries.
# You only need to configure project-specific patterns.
#
# index:
#   languages:
#     - go
#     - typescript
#   # include:
#   #   - "src/**"
#   # exclude:
#   #   - "examples/legacy/**"
#   # advanced:
#   #   forceInclude:
#   #     - "src/.domain/**"

index:
  # languages:
  #   - go
  #   - typescript
  # include:
  #   - "src/**"
  # exclude:
  #   - "examples/legacy/**"
  # advanced:
  #   forceInclude:
  #     - "src/.domain/**"
`

// ===== Reporting =====

func BuildIndexFilterMatchReport(projectRoot string, filter *IndexFilter) (*IndexFilterMatchReport, error) {
	report := &IndexFilterMatchReport{}
	if filter == nil {
		return report, nil
	}

	err := filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		relPath, err := filepath.Rel(projectRoot, path)
		if err != nil {
			return nil
		}
		relPath = normalizeFilterPath(relPath)
		if relPath == "" {
			return nil
		}

		if info.IsDir() {
			plan := filter.EvaluateDir(relPath)
			if !plan.Indexed && !plan.Overridden {
				report.Excluded = append(report.Excluded, IndexFilterExcludedEntry{
					Path:   relPath + "/",
					RuleID: plan.RuleID,
					Kind:   plan.Kind,
					Reason: plan.Reason,
				})
				return filepath.SkipDir
			}
			return nil
		}

		plan := filter.Evaluate(relPath)
		if !plan.Indexed && !plan.Overridden {
			report.Excluded = append(report.Excluded, IndexFilterExcludedEntry{
				Path:   relPath,
				RuleID: plan.RuleID,
				Kind:   plan.Kind,
				Reason: plan.Reason,
			})
		}
		return nil
	})
	sort.Slice(report.Excluded, func(i, j int) bool {
		return report.Excluded[i].Path < report.Excluded[j].Path
	})
	return report, err
}

// ===== Internal Helper Functions =====

func isValidConfigKey(normalized string) bool {
	return normalized == "include" || normalized == "exclude" ||
		normalized == "scipexclude" || normalized == "treesitterexclude" ||
		normalized == "forceinclude"
}

func stripConfigComment(line string) string {
	inQuote := rune(0)
	for i, r := range line {
		switch r {
		case '\'', '"':
			if inQuote == 0 {
				inQuote = r
			} else if inQuote == r {
				inQuote = 0
			}
		case '#':
			if inQuote == 0 {
				return line[:i]
			}
		}
	}
	return line
}

func normalizeIndexConfigKey(key string) string {
	key = strings.TrimSpace(key)
	key = strings.ReplaceAll(key, "-", "")
	key = strings.ReplaceAll(key, "_", "")
	return strings.ToLower(key)
}

func addIndexPattern(filter *IndexFilter, key, value string) {
	value = parseConfigValue(value)
	if value == "" {
		return
	}
	switch key {
	case "include":
		filter.Include = append(filter.Include, normalizeFilterPath(value))
	case "exclude":
		filter.Exclude = append(filter.Exclude, normalizeFilterPath(value))
	case "scipexclude", "treesitterexclude":
		// 向后兼容：合并到 Exclude
		filter.Exclude = append(filter.Exclude, normalizeFilterPath(value))
	case "forceinclude":
		filter.ForceInclude = append(filter.ForceInclude, normalizeFilterPath(value))
	}
}

func parseConfigList(value string) []string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
		return []string{parseConfigValue(value)}
	}
	value = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "["), "]"))
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		if item := parseConfigValue(part); item != "" {
			items = append(items, item)
		}
	}
	return items
}

func parseConfigValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	return value
}

func normalizeFilterPath(value string) string {
	raw := filepath.ToSlash(strings.TrimSpace(value))
	hasTrailingSlash := strings.HasSuffix(raw, "/")
	for strings.HasPrefix(raw, "../") {
		raw = strings.TrimPrefix(raw, "../")
	}
	if raw == ".." {
		raw = ""
	}
	raw = strings.TrimPrefix(raw, "./")
	raw = strings.TrimPrefix(raw, "/")
	value = path.Clean(raw)
	if value == "." || value == "/" {
		return ""
	}
	if hasTrailingSlash {
		return strings.TrimSuffix(value, "/") + "/"
	}
	return value
}

func matchesAnyPattern(relPath string, patterns []string) bool {
	for _, pattern := range patterns {
		if matchIndexPattern(pattern, relPath) {
			return true
		}
	}
	return false
}

func matchIndexPattern(pattern, relPath string) bool {
	pattern = normalizeFilterPath(pattern)
	relPath = normalizeFilterPath(relPath)
	if pattern == "" || relPath == "" {
		return false
	}
	if strings.HasSuffix(pattern, "/") {
		pattern += "**"
	}
	if !strings.ContainsAny(pattern, "*?[") {
		return relPath == pattern || strings.HasPrefix(relPath, pattern+"/")
	}
	if ok := matchPathSegments(strings.Split(pattern, "/"), strings.Split(relPath, "/")); ok {
		return true
	}
	if !strings.Contains(pattern, "/") {
		for _, segment := range strings.Split(relPath, "/") {
			if ok, _ := path.Match(pattern, segment); ok {
				return true
			}
		}
	}
	return false
}

func matchPathSegments(patterns, segments []string) bool {
	if len(patterns) == 0 {
		return len(segments) == 0
	}
	if patterns[0] == "**" {
		if matchPathSegments(patterns[1:], segments) {
			return true
		}
		for i := range segments {
			if matchPathSegments(patterns[1:], segments[i+1:]) {
				return true
			}
		}
		return false
	}
	if len(segments) == 0 {
		return false
	}
	ok, err := path.Match(patterns[0], segments[0])
	if err != nil || !ok {
		return false
	}
	return matchPathSegments(patterns[1:], segments[1:])
}

func hasHiddenSegment(relPath string) bool {
	for _, seg := range strings.Split(relPath, "/") {
		if strings.HasPrefix(seg, ".") && seg != "." && seg != ".." {
			return true
		}
	}
	return false
}

func convertGitIgnoreToGlob(line string) string {
	line = filepath.ToSlash(strings.TrimSpace(line))
	if line == "" {
		return ""
	}
	if strings.HasPrefix(line, "!") {
		return ""
	}
	isDir := strings.HasSuffix(line, "/")
	line = strings.TrimSuffix(line, "/")
	isRootOnly := strings.HasPrefix(line, "/")
	line = strings.TrimPrefix(line, "/")

	pattern := line
	if isDir {
		pattern += "/**"
	}
	if !isRootOnly && !strings.Contains(line, "/") {
		if isDir {
			return "**/" + pattern
		}
		return "**/" + pattern
	}
	return pattern
}

// mustCompileRegex compiles a regular expression, panic on failure
func mustCompileRegex(pattern string) interface{ MatchString(string) bool } {
	return regexp.MustCompile(pattern)
}
