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

// ===== 排除规则类型 =====

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

// ExcludeRule 描述一条内置排除规则
type ExcludeRule struct {
	ID          string
	Description string
	Ecosystem   string   // "" = universal
	Match       []string // glob patterns
	Kind        ExcludeKind
	Confidence  int  // 0-100
	Overridable bool // 是否可被 forceInclude 覆盖
}

// FileIndexPlan 描述单个文件的索引判定结果
type FileIndexPlan struct {
	Path       string
	Indexed    bool
	RuleID     string
	Kind       ExcludeKind
	Reason     string
	Overridden bool
}

// ===== 内置通用规则集 =====

var builtInRules = []ExcludeRule{
	// --- VCS 元数据 (不可覆盖) ---
	{ID: "vcs.git", Description: "Git 仓库元数据", Match: []string{".git/**"}, Kind: ExcludeVCSMetadata, Confidence: 100, Overridable: false},
	{ID: "vcs.svn", Description: "SVN 仓库元数据", Match: []string{".svn/**"}, Kind: ExcludeVCSMetadata, Confidence: 100, Overridable: false},
	{ID: "vcs.hg", Description: "Mercurial 仓库元数据", Match: []string{".hg/**"}, Kind: ExcludeVCSMetadata, Confidence: 100, Overridable: false},
	{ID: "vcs.astramap", Description: "AstraMap 索引数据", Match: []string{".astramap/**"}, Kind: ExcludeVCSMetadata, Confidence: 100, Overridable: false},

	// --- 第三方依赖 ---
	{ID: "dep.node_modules", Description: "Node.js 依赖", Match: []string{"**/node_modules/**"}, Kind: ExcludeDependency, Confidence: 100, Overridable: true},
	{ID: "dep.bower_components", Description: "Bower 依赖", Match: []string{"**/bower_components/**"}, Kind: ExcludeDependency, Confidence: 100, Overridable: true},
	{ID: "dep.pods", Description: "CocoaPods 依赖", Match: []string{"**/Pods/**"}, Kind: ExcludeDependency, Confidence: 100, Overridable: true},
	{ID: "dep.vendor", Description: "Vendor 依赖", Match: []string{"vendor/**"}, Kind: ExcludeDependency, Confidence: 95, Overridable: true},
	{ID: "dep.third_party", Description: "第三方代码", Match: []string{"third_party/**"}, Kind: ExcludeDependency, Confidence: 90, Overridable: true},

	// --- 缓存 ---
	{ID: "cache.pycache", Description: "Python 缓存", Match: []string{"**/__pycache__/**", "**/*.pyc", "**/*.pyo"}, Kind: ExcludeCache, Confidence: 100, Overridable: true},
	{ID: "cache.pytest", Description: "pytest 缓存", Match: []string{"**/.pytest_cache/**"}, Kind: ExcludeCache, Confidence: 100, Overridable: true},
	{ID: "cache.mypy", Description: "mypy 缓存", Match: []string{"**/.mypy_cache/**"}, Kind: ExcludeCache, Confidence: 100, Overridable: true},
	{ID: "cache.ruff", Description: "ruff 缓存", Match: []string{"**/.ruff_cache/**"}, Kind: ExcludeCache, Confidence: 100, Overridable: true},
	{ID: "cache.generic", Description: "通用缓存", Match: []string{"**/.cache/**", "**/.sass-cache/**"}, Kind: ExcludeCache, Confidence: 95, Overridable: true},

	// --- OS/编辑器垃圾 ---
	{ID: "os.ds_store", Description: "macOS .DS_Store", Match: []string{"**/.DS_Store"}, Kind: ExcludeCache, Confidence: 100, Overridable: true},
	{ID: "os.thumbs", Description: "Windows Thumbs.db", Match: []string{"**/Thumbs.db"}, Kind: ExcludeCache, Confidence: 100, Overridable: true},
	{ID: "os.swap", Description: "编辑器 swap 文件", Match: []string{"**/*.swp", "**/*.swo", "**/*~"}, Kind: ExcludeCache, Confidence: 100, Overridable: true},
	{ID: "os.tmp", Description: "临时文件", Match: []string{"**/*.tmp", "**/*.temp"}, Kind: ExcludeCache, Confidence: 100, Overridable: true},

	// --- 压缩/Source Map ---
	{ID: "minified.js", Description: "压缩 JS/CSS", Match: []string{"**/*.min.js", "**/*.min.mjs", "**/*.min.cjs", "**/*.min.css"}, Kind: ExcludeMinified, Confidence: 100, Overridable: true},
	{ID: "sourcemap", Description: "Source Map 文件", Match: []string{"**/*.js.map", "**/*.css.map", "**/*.d.ts.map"}, Kind: ExcludeMinified, Confidence: 100, Overridable: true},

	// --- 生成源码：确定性文件名 ---
	{ID: "gen.go.protobuf", Description: "Go protobuf 生成文件", Ecosystem: "go", Match: []string{"**/*.pb.go", "**/*.grpc.pb.go"}, Kind: ExcludeGeneratedSource, Confidence: 100, Overridable: true},
	{ID: "gen.py.protobuf", Description: "Python protobuf 生成文件", Ecosystem: "python", Match: []string{"**/*_pb2.py", "**/*_pb2.pyi", "**/*_pb2_grpc.py"}, Kind: ExcludeGeneratedSource, Confidence: 100, Overridable: true},
	{ID: "gen.cc.protobuf", Description: "C++ protobuf 生成文件", Ecosystem: "cpp", Match: []string{"**/*.pb.cc", "**/*.pb.h", "**/*.grpc.pb.cc", "**/*.grpc.pb.h"}, Kind: ExcludeGeneratedSource, Confidence: 100, Overridable: true},
	{ID: "gen.cc.flatbuffers", Description: "FlatBuffers 生成文件", Ecosystem: "cpp", Match: []string{"**/*_generated.h"}, Kind: ExcludeGeneratedSource, Confidence: 100, Overridable: true},
	{ID: "gen.dart.all", Description: "Dart 生成文件", Ecosystem: "dart", Match: []string{"**/*.g.dart", "**/*.freezed.dart", "**/*.gr.dart", "**/*.mocks.dart", "**/*.pb.dart"}, Kind: ExcludeGeneratedSource, Confidence: 100, Overridable: true},
	{ID: "gen.rb.protobuf", Description: "Ruby protobuf 生成文件", Ecosystem: "ruby", Match: []string{"**/*_pb.rb"}, Kind: ExcludeGeneratedSource, Confidence: 100, Overridable: true},

	// --- 二进制文件 (不可覆盖) ---
	{ID: "binary.object", Description: "编译目标文件", Match: []string{"**/*.o", "**/*.obj", "**/*.a", "**/*.lib", "**/*.so", "**/*.dylib", "**/*.dll", "**/*.exe"}, Kind: ExcludeBinary, Confidence: 100, Overridable: false},
	{ID: "binary.java", Description: "Java 归档", Match: []string{"**/*.class", "**/*.jar", "**/*.war", "**/*.ear"}, Kind: ExcludeBinary, Confidence: 100, Overridable: false},
	{ID: "binary.wasm", Description: "WebAssembly", Match: []string{"**/*.wasm"}, Kind: ExcludeBinary, Confidence: 100, Overridable: false},
	{ID: "binary.archive", Description: "压缩归档", Match: []string{"**/*.zip", "**/*.tar", "**/*.gz", "**/*.7z"}, Kind: ExcludeBinary, Confidence: 100, Overridable: false},
	{ID: "binary.image", Description: "图片文件", Match: []string{"**/*.png", "**/*.jpg", "**/*.jpeg", "**/*.gif", "**/*.pdf", "**/*.ico", "**/*.svg", "**/*.webp"}, Kind: ExcludeBinary, Confidence: 100, Overridable: false},
	{ID: "binary.model", Description: "模型文件", Match: []string{"**/*.tflite", "**/*.onnx", "**/*.pt"}, Kind: ExcludeBinary, Confidence: 100, Overridable: false},
	{ID: "binary.font", Description: "字体文件", Match: []string{"**/*.woff", "**/*.woff2", "**/*.ttf", "**/*.eot"}, Kind: ExcludeBinary, Confidence: 100, Overridable: false},
}

// ===== 生成文件头检测 =====

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

// IsGeneratedByHeader 扫描文件前 8KB / 100 行检测生成文件头标记
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
	ForceInclude []string // advanced.forceInclude — 覆盖可覆盖的内置规则

	// 生态感知动态规则（由 DetectProjectRoots 生成）
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

// Evaluate 评估文件是否应被索引，返回 FileIndexPlan
func (f *IndexFilter) Evaluate(relPath string) FileIndexPlan {
	relPath = normalizeFilterPath(relPath)
	if relPath == "" || relPath == "." {
		return FileIndexPlan{Path: relPath, Indexed: true}
	}

	// 1. 内置规则（含生态感知规则）
	allRules := builtInRules
	if len(f.ecosystemRules) > 0 {
		allRules = append(allRules, f.ecosystemRules...)
	}
	for _, rule := range allRules {
		if matchesAnyPattern(relPath, rule.Match) {
			// forceInclude 可覆盖 Overridable 规则
			if rule.Overridable && len(f.ForceInclude) > 0 && matchesAnyPattern(relPath, f.ForceInclude) {
				return FileIndexPlan{Path: relPath, Indexed: true, RuleID: rule.ID, Kind: rule.Kind, Reason: rule.Description, Overridden: true}
			}
			return FileIndexPlan{Path: relPath, Indexed: false, RuleID: rule.ID, Kind: rule.Kind, Reason: rule.Description}
		}
	}

	// 2. 隐藏路径
	if hasHiddenSegment(relPath) {
		if len(f.ForceInclude) > 0 && matchesAnyPattern(relPath, f.ForceInclude) {
			return FileIndexPlan{Path: relPath, Indexed: true, Kind: ExcludeHiddenPath, Overridden: true}
		}
		return FileIndexPlan{Path: relPath, Indexed: false, RuleID: "hidden.dotdir", Kind: ExcludeHiddenPath, Reason: "隐藏路径"}
	}

	// 3. 用户 Include
	if len(f.Include) > 0 && !matchesAnyPattern(relPath, f.Include) {
		return FileIndexPlan{Path: relPath, Indexed: false, RuleID: "user.include", Kind: ExcludeUserConfigured, Reason: "不在 include 范围内"}
	}

	// 4. 用户 Exclude
	if matchesAnyPattern(relPath, f.Exclude) {
		return FileIndexPlan{Path: relPath, Indexed: false, RuleID: "user.exclude", Kind: ExcludeUserConfigured, Reason: "用户配置排除"}
	}

	return FileIndexPlan{Path: relPath, Indexed: true}
}

// EvaluateDir 评估目录是否应被遍历
func (f *IndexFilter) EvaluateDir(relPath string) FileIndexPlan {
	relPath = normalizeFilterPath(relPath)
	if relPath == "" || relPath == "." {
		return FileIndexPlan{Path: relPath, Indexed: true}
	}

	// 目录先检查自身
	plan := f.Evaluate(relPath)
	if !plan.Indexed && !plan.Overridden {
		return plan
	}

	// 再检查带 / 后缀的目录匹配
	planSlash := f.Evaluate(relPath + "/")
	if !planSlash.Indexed && !planSlash.Overridden {
		return planSlash
	}

	return FileIndexPlan{Path: relPath, Indexed: true}
}

// Allows 保持向后兼容：stage 参数被忽略，统一走 Evaluate
func (f *IndexFilter) Allows(relPath string, stage IndexStage) bool {
	return f.Evaluate(relPath).Indexed
}

// AllowsDir 保持向后兼容
func (f *IndexFilter) AllowsDir(relPath string, stage IndexStage) bool {
	return f.EvaluateDir(relPath).Indexed
}

// ===== 生态感知工程根识别 =====

type ProjectRoot struct {
	Path      string // 相对于项目根的路径
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

// DetectProjectRoots 扫描项目目录建立工程根映射
func DetectProjectRoots(projectRoot string) []ProjectRoot {
	var roots []ProjectRoot
	_ = filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		relPath, _ := filepath.Rel(projectRoot, path)
		relPath = filepath.ToSlash(relPath)

		if info.IsDir() {
			// 隐藏目录和常见非源码目录跳过
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

// BuildEcosystemRules 根据工程根生成生态感知排除规则
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
				rules = append(rules, ExcludeRule{ID: "build.node.dist", Description: "Node.js 构建输出", Match: []string{prefix + "dist/**", prefix + "coverage/**"}, Kind: ExcludeBuildArtifact, Confidence: 100, Overridable: true})
				seen[key("build.node.dist")] = true
			}
		case "rust":
			if !seen[key("build.rust.target")] {
				rules = append(rules, ExcludeRule{ID: "build.rust.target", Description: "Rust 构建输出", Match: []string{prefix + "target/**"}, Kind: ExcludeBuildArtifact, Confidence: 100, Overridable: true})
				seen[key("build.rust.target")] = true
			}
		case "maven":
			if !seen[key("build.maven.target")] {
				rules = append(rules, ExcludeRule{ID: "build.maven.target", Description: "Maven 构建输出", Match: []string{prefix + "target/**"}, Kind: ExcludeBuildArtifact, Confidence: 100, Overridable: true})
				seen[key("build.maven.target")] = true
			}
		case "gradle":
			if !seen[key("build.gradle.output")] {
				rules = append(rules, ExcludeRule{ID: "build.gradle.output", Description: "Gradle 构建输出", Match: []string{prefix + "build/**"}, Kind: ExcludeBuildArtifact, Confidence: 100, Overridable: true})
				seen[key("build.gradle.output")] = true
			}
		case "cmake":
			if !seen[key("build.cmake.output")] {
				rules = append(rules, ExcludeRule{ID: "build.cmake.output", Description: "CMake 构建输出", Match: []string{prefix + "cmake-build-*/**"}, Kind: ExcludeBuildArtifact, Confidence: 100, Overridable: true})
				seen[key("build.cmake.output")] = true
			}
		case "bazel":
			if !seen[key("build.bazel.output")] {
				rules = append(rules, ExcludeRule{ID: "build.bazel.output", Description: "Bazel 构建输出", Match: []string{prefix + "bazel-*/**"}, Kind: ExcludeBuildArtifact, Confidence: 100, Overridable: true})
				seen[key("build.bazel.output")] = true
			}
		case "swift":
			if !seen[key("build.swift.output")] {
				rules = append(rules, ExcludeRule{ID: "build.swift.output", Description: "Swift 构建输出", Match: []string{prefix + ".build/**"}, Kind: ExcludeBuildArtifact, Confidence: 100, Overridable: true})
				seen[key("build.swift.output")] = true
			}
		}
	}
	return rules
}

// ===== 配置加载 =====

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

		// 注释中的 key 追踪
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

		// advanced: 嵌套键
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

	// 向后兼容：旧配置中的 scipExclude / treeSitterExclude 合并到 Exclude
	// (这些字段在 addIndexPattern 中已直接写入 Exclude)

	// 加载 .gitignore
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

	// 生态感知
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

// ===== 报告 =====

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

// ===== 内部辅助函数 =====

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

// mustCompileRegex 编译正则表达式，编译失败 panic
func mustCompileRegex(pattern string) interface{ MatchString(string) bool } {
	return regexp.MustCompile(pattern)
}
