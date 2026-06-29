package astramap

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
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

type IndexFilter struct {
	Include           []string
	Exclude           []string
	ScipExclude       []string
	TreeSitterExclude []string
}

type IndexFilterMatchReport struct {
	Exclude           []string
	ScipExclude       []string
	TreeSitterExclude []string
}

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
	var current string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		rawLine := scanner.Text()
		line := stripConfigComment(rawLine)

		// If line was stripped (contained #), check if the comment part
		// contains a valid key like "scipExclude:" so that uncommented
		// items below it still get assigned to the right key.
		if line != rawLine {
			commentPart := strings.TrimSpace(strings.SplitN(rawLine, "#", 2)[1])
			if strings.Contains(commentPart, ":") {
				commentKey, _, ok := strings.Cut(commentPart, ":")
				if ok {
					normalized := normalizeIndexConfigKey(commentKey)
					if normalized == "include" || normalized == "exclude" || normalized == "scipexclude" || normalized == "treesitterexclude" {
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
			current = ""
			continue
		}
		if !inIndex {
			continue
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
	return filter, scanner.Err()
}

const indexConfigExample = `# AstraMap index filter config.
# All paths are relative to the project root and use / as the separator.
#
# Usage:
# 1. Uncomment or add patterns below.
# 2. Run: amap index
# 3. Excluded files are skipped by language detection, SCIP import,
#    Tree-sitter parsing, and cross-file heuristic analysis.
#
# Pattern examples:
# - "docs/**" excludes a directory.
# - "**/*.pb.go" excludes generated files by suffix.
# - "examples/**" can be excluded only from SCIP with scipExclude.
# - "testdata/**" can be excluded only from Tree-sitter with treeSitterExclude.

index:
  # include:
  #   - "**/*.go"
  #   - "**/*.ts"
  # exclude:
  #   - "docs/**"
  #   - "vendor/**"
  #   - "**/*.pb.go"
  #   - "**/*.min.js"
  # scipExclude:
  #   - "examples/**"
  # treeSitterExclude:
  #   - "testdata/**"
`

func (f *IndexFilter) Allows(relPath string, stage IndexStage) bool {
	relPath = normalizeFilterPath(relPath)
	if relPath == "" || relPath == "." {
		return true
	}

	if matchesAnyPattern(relPath, builtInIndexExcludes) {
		return false
	}
	if len(f.Include) > 0 && !matchesAnyPattern(relPath, f.Include) {
		return false
	}
	if matchesAnyPattern(relPath, f.Exclude) {
		return false
	}
	switch stage {
	case StageScip:
		return !matchesAnyPattern(relPath, f.ScipExclude)
	case StageTreeSitter:
		return !matchesAnyPattern(relPath, f.TreeSitterExclude)
	default:
		return true
	}
}

func (f *IndexFilter) AllowsDir(relPath string, stage IndexStage) bool {
	relPath = normalizeFilterPath(relPath)
	if relPath == "" || relPath == "." {
		return true
	}
	if matchesAnyPattern(relPath, builtInIndexExcludes) || matchesAnyPattern(relPath+"/", builtInIndexExcludes) {
		return false
	}
	if matchesAnyPattern(relPath, f.Exclude) || matchesAnyPattern(relPath+"/", f.Exclude) {
		return false
	}
	switch stage {
	case StageScip:
		return !matchesAnyPattern(relPath, f.ScipExclude) && !matchesAnyPattern(relPath+"/", f.ScipExclude)
	case StageTreeSitter:
		return !matchesAnyPattern(relPath, f.TreeSitterExclude) && !matchesAnyPattern(relPath+"/", f.TreeSitterExclude)
	default:
		return true
	}
}

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

		reportPath := relPath
		if info.IsDir() {
			reportPath += "/"
		}

		excluded := matchesAnyPattern(relPath, filter.Exclude)
		scipExcluded := matchesAnyPattern(relPath, filter.ScipExclude)
		treeSitterExcluded := matchesAnyPattern(relPath, filter.TreeSitterExclude)
		if info.IsDir() {
			excluded = excluded || matchesAnyPattern(reportPath, filter.Exclude)
			scipExcluded = scipExcluded || matchesAnyPattern(reportPath, filter.ScipExclude)
			treeSitterExcluded = treeSitterExcluded || matchesAnyPattern(reportPath, filter.TreeSitterExclude)
		}

		if excluded {
			report.Exclude = append(report.Exclude, reportPath)
		}
		if scipExcluded {
			report.ScipExclude = append(report.ScipExclude, reportPath)
		}
		if treeSitterExcluded {
			report.TreeSitterExclude = append(report.TreeSitterExclude, reportPath)
		}

		if info.IsDir() && (excluded || matchesAnyPattern(relPath, builtInIndexExcludes) || matchesAnyPattern(reportPath, builtInIndexExcludes)) {
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(report.Exclude)
	sort.Strings(report.ScipExclude)
	sort.Strings(report.TreeSitterExclude)
	return report, err
}

var builtInIndexExcludes = []string{
	".git/**",
	".astramap/**",
	".understand-anything/**",
	".cache/**",
	".idea/**",
	".vscode/**",
	"node_modules/**",
	"build/**",
	"dist/**",
	"vendor/**",
	"target/**",
	"out/**",
	"tmp/**",
	"temp/**",
	".trash*/**",
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
	case "scipexclude":
		filter.ScipExclude = append(filter.ScipExclude, normalizeFilterPath(value))
	case "treesitterexclude":
		filter.TreeSitterExclude = append(filter.TreeSitterExclude, normalizeFilterPath(value))
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
