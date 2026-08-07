// Copyright 2026 AstraMap Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the original license at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package astramap

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"
)

var (
	callRe              = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)\s*\(`)
	functionPointerInit = regexp.MustCompile(`\.\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*=\s*&?\s*([a-zA-Z_][a-zA-Z0-9_]*)`)
	macroReturnCallRe   = regexp.MustCompile(`\b[A-Z][A-Z0-9_]*RETURN\s*\(([^,\)]+)`)

	// Cross-language command execution patterns
	// Go: exec.Command("python3", "path/to/script.py")
	goExecCommandRe = regexp.MustCompile(`exec\.Command\s*\(\s*["']python3?["']\s*,\s*["']([^"']+\.py)["']`)
	// Python: subprocess.run(["python3", "script.py"])
	pySubprocessRunRe = regexp.MustCompile(`subprocess\.(?:run|Popen)\s*\(\s*\[\s*["']python3?["']\s*,\s*["']([^"']+\.py)["']`)
	// Python: os.system("python3 script.py")
	pyOsSystemRe = regexp.MustCompile(`os\.system\s*\(\s*["']python3?\s+([^"']+\.py)["']`)
	// Python: exec(open("file.py").read())
	pyExecOpenRe = regexp.MustCompile(`exec\s*\(\s*open\s*\(\s*["']([^"']+\.py)["']\s*\)\.read\s*\(\s*\)\s*\)`)
)

func isKeyword(name string) bool {
	keywords := map[string]bool{
		"if": true, "else": true, "while": true, "for": true,
		"switch": true, "return": true, "sizeof": true, "typeof": true,
		"break": true, "continue": true, "goto": true, "do": true,
		"case": true, "default": true, "typedef": true, "struct": true,
		"union": true, "enum": true, "static": true, "inline": true,
		"extern": true, "const": true, "void": true, "int": true,
		"char": true, "short": true, "long": true, "float": true,
		"double": true, "unsigned": true, "signed": true, "super": true, "this": true,
	}
	return keywords[name]
}

func isInsideQuotes(s string) bool {
	inDouble := false
	inSingle := false
	inBacktick := false
	escaped := false
	for _, r := range s {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '"' && !inSingle && !inBacktick {
			inDouble = !inDouble
		} else if r == '\'' && !inDouble && !inBacktick {
			inSingle = !inSingle
		} else if r == '`' && !inDouble && !inSingle {
			inBacktick = !inBacktick
		}
	}
	return inDouble || inSingle || inBacktick
}

type funcNode struct {
	ID        string `db:"id"`
	StartLine int    `db:"start_line"`
	EndLine   int    `db:"end_line"`
}

type callableInterval struct {
	node   *AstraMapNode
	maxEnd int
	left   *callableInterval
	right  *callableInterval
}

type callableScopeIndex struct {
	root *callableInterval
}

func newCallableScopeIndex(nodes []*AstraMapNode) callableScopeIndex {
	callables := make([]*AstraMapNode, 0, len(nodes))
	for _, node := range nodes {
		if node.Kind == "function" || node.Kind == "method" {
			callables = append(callables, node)
		}
	}
	sort.Slice(callables, func(i, j int) bool {
		if callables[i].StartLine == callables[j].StartLine {
			return callables[i].EndLine > callables[j].EndLine
		}
		return callables[i].StartLine < callables[j].StartLine
	})
	var build func([]*AstraMapNode) *callableInterval
	build = func(items []*AstraMapNode) *callableInterval {
		if len(items) == 0 {
			return nil
		}
		mid := len(items) / 2
		root := &callableInterval{node: items[mid], maxEnd: items[mid].EndLine}
		root.left = build(items[:mid])
		root.right = build(items[mid+1:])
		if root.left != nil && root.left.maxEnd > root.maxEnd {
			root.maxEnd = root.left.maxEnd
		}
		if root.right != nil && root.right.maxEnd > root.maxEnd {
			root.maxEnd = root.right.maxEnd
		}
		return root
	}
	return callableScopeIndex{root: build(callables)}
}

func (idx callableScopeIndex) Enclosing(line int) *AstraMapNode {
	var matched *AstraMapNode
	var visit func(*callableInterval)
	visit = func(current *callableInterval) {
		if current == nil || current.maxEnd < line {
			return
		}
		if current.left != nil && current.left.maxEnd >= line {
			visit(current.left)
		}
		node := current.node
		if node.StartLine <= line && line <= node.EndLine {
			if matched == nil || node.EndLine-node.StartLine < matched.EndLine-matched.StartLine {
				matched = node
			}
		}
		if node.StartLine <= line {
			visit(current.right)
		}
	}
	visit(idx.root)
	return matched
}

// ResolveCrossFileCalls scans all indexed source files and resolves
// function call references against the global symbol registry in DB.
// This fills in cross-file 'calls' edges that single-file parsing misses.
func ResolveCrossFileCalls(db *sqlx.DB, projectRoot string) error {
	return resolveCrossFileCalls(db, projectRoot, nil)
}

func ResolveCrossFileCallsForFiles(db *sqlx.DB, projectRoot string, files []string) error {
	if len(files) == 0 {
		return nil
	}
	return resolveCrossFileCalls(db, projectRoot, files)
}

func resolveCrossFileCalls(db *sqlx.DB, projectRoot string, changedFiles []string) error {
	filter, err := LoadIndexFilter(projectRoot)
	if err != nil {
		return fmt.Errorf("读取 AstraMap 配置失败: %w", err)
	}

	type globalNode struct {
		ID            string `db:"id"`
		Name          string `db:"name"`
		QualifiedName string `db:"qualified_name"`
		FilePath      string `db:"file_path"`
	}
	var allFuncs []globalNode
	err = db.Select(&allFuncs, "SELECT id, name, qualified_name, file_path FROM astramap_nodes WHERE kind IN ('function', 'method')")
	if err != nil {
		return fmt.Errorf("query global registry failed: %w", err)
	}

	shortMap := make(map[string][]string)
	qualifiedMap := make(map[string][]string)
	for _, fn := range allFuncs {
		shortMap[fn.Name] = append(shortMap[fn.Name], fn.ID)
		qualifiedMap[fn.QualifiedName] = append(qualifiedMap[fn.QualifiedName], fn.ID)
	}
	fieldFunctionMap := buildFunctionPointerFieldMap(projectRoot, shortMap)

	var files []string
	if len(changedFiles) == 0 {
		err = db.Select(&files, "SELECT path FROM astramap_files")
		if err != nil {
			return fmt.Errorf("query files failed: %w", err)
		}
	} else {
		seen := make(map[string]bool, len(changedFiles))
		for _, filePath := range changedFiles {
			if filePath == "" {
				continue
			}
			relPath := filePath
			if filepath.IsAbs(relPath) {
				if rel, relErr := filepath.Rel(projectRoot, relPath); relErr == nil {
					relPath = rel
				}
			}
			relPath = filepath.ToSlash(relPath)
			if seen[relPath] {
				continue
			}
			seen[relPath] = true
			files = append(files, relPath)
		}
	}

	type preparedFile struct {
		path  string
		lines []string
		funcs []funcNode
	}
	prepared := make([]preparedFile, 0, len(files))
	for _, fp := range files {
		if !filter.Allows(fp, StageSyntax) {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(projectRoot, fp))
		if readErr != nil {
			continue
		}
		var localFuncs []funcNode
		if selectErr := db.Select(&localFuncs, "SELECT id, start_line, end_line FROM astramap_nodes WHERE file_path = ? AND kind IN ('function', 'method')", fp); selectErr != nil {
			return fmt.Errorf("query callable ranges for %s: %w", fp, selectErr)
		}
		prepared = append(prepared, preparedFile{
			path:  fp,
			lines: strings.Split(string(data), "\n"),
			funcs: localFuncs,
		})
	}

	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if len(changedFiles) == 0 {
		_, err = tx.Exec("DELETE FROM astramap_edges WHERE provenance = 'heuristic' AND kind = 'calls'")
		if err != nil {
			return fmt.Errorf("failed to clear old heuristic calls: %w", err)
		}
	}

	insertStmt, err := tx.Preparex(`
		INSERT OR IGNORE INTO astramap_edges (source, target, kind, provenance, line, col, metadata)
		VALUES (?, ?, 'calls', 'heuristic', ?, ?, '')
	`)
	if err != nil {
		return err
	}
	defer insertStmt.Close()

	for _, preparedFile := range prepared {
		fp := preparedFile.path
		if len(changedFiles) > 0 {
			_, err = tx.Exec(`
				DELETE FROM astramap_edges
				WHERE provenance = 'heuristic'
				  AND kind = 'calls'
				  AND source IN (
				    SELECT id FROM astramap_nodes
				    WHERE file_path = ? AND kind IN ('function', 'method')
				  )
			`, fp)
			if err != nil {
				return fmt.Errorf("failed to clear heuristic calls for %s: %w", fp, err)
			}
		}

		inMultiLineComment := false
		for i, line := range preparedFile.lines {
			lineNum := i + 1
			trimmed := strings.TrimSpace(line)

			if !inMultiLineComment {
				if strings.HasPrefix(trimmed, "/*") {
					inMultiLineComment = true
					if strings.Contains(trimmed, "*/") {
						inMultiLineComment = false
					}
					continue
				}
			} else {
				if strings.Contains(trimmed, "*/") {
					inMultiLineComment = false
				}
				continue
			}

			if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
				continue
			}

			matches := callRe.FindAllStringSubmatchIndex(line, -1)
			if len(matches) == 0 {
				continue
			}

			var callerID string
			callerSpan := 0
			for _, lf := range preparedFile.funcs {
				if lineNum >= lf.StartLine && lineNum <= lf.EndLine {
					span := lf.EndLine - lf.StartLine
					if callerID == "" || span < callerSpan {
						callerID = lf.ID
						callerSpan = span
					}
				}
			}
			if callerID == "" {
				continue
			}

			for _, macroTarget := range macroReturnCallTargets(line, shortMap, fieldFunctionMap) {
				if macroTarget == callerID {
					continue
				}
				_, _ = insertStmt.Exec(callerID, macroTarget, lineNum, 1)
			}

			for _, m := range matches {
				if len(m) < 4 {
					continue
				}
				calleeName := line[m[2]:m[3]]
				if isKeyword(calleeName) || calleeName == "init" {
					continue
				}

				if isInsideQuotes(line[:m[0]]) {
					continue
				}

				targets := shortMap[calleeName]

				beforeCallee := line[:m[2]]
				sepIndex := -1
				lastDot := strings.LastIndex(beforeCallee, ".")
				lastArrow := strings.LastIndex(beforeCallee, "->")
				lastColon := strings.LastIndex(beforeCallee, "::")

				if lastDot > sepIndex {
					sepIndex = lastDot
				}
				if lastArrow > sepIndex {
					sepIndex = lastArrow
				}
				if lastColon > sepIndex {
					sepIndex = lastColon
				}

				if sepIndex != -1 {
					if fieldTargets := fieldFunctionMap[calleeName]; len(fieldTargets) > 0 {
						targets = fieldTargets
					}
				}
				if calleeName == "main" && sepIndex == -1 {
					var filtered []string
					for _, tID := range targets {
						if strings.Contains(tID, ":"+fp+"::") {
							filtered = append(filtered, tID)
						}
					}
					targets = filtered
				}
				hasExplicitQualifiedMatch := false
				if sepIndex != -1 {
					leftBound := sepIndex
					for leftBound > 0 {
						c := beforeCallee[leftBound-1]
						if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
							leftBound--
						} else {
							break
						}
					}
					prefix := beforeCallee[leftBound:sepIndex]
					if prefix != "" {
						possibleQualified1 := prefix + "." + calleeName
						possibleQualified2 := prefix + "::" + calleeName
						if qTargets, exists := qualifiedMap[possibleQualified1]; exists {
							targets = qTargets
							hasExplicitQualifiedMatch = true
						} else if qTargets, exists := qualifiedMap[possibleQualified2]; exists {
							targets = qTargets
							hasExplicitQualifiedMatch = true
						}
					}
				}
				// 防御漏洞 B：对于包含成员/包选择符（如 obj.Method()）的调用，
				// 如果未能通过 QualifiedName 精确匹配到定义，且当前候选目标有多个不同类型的同名方法，
				// 严禁退化为盲目全连，直接跳过以防止污染调用图。
				if sepIndex != -1 && !hasExplicitQualifiedMatch && len(targets) > 1 {
					continue
				}
				// 启发式消歧逻辑：当存在多个候选 target 时，通过路径/包就近原则进行过滤
				if len(targets) > 1 {
					callerFile := fp
					callerDir := filepath.ToSlash(filepath.Dir(callerFile))

					var refinedTargets []string
					for _, targetID := range targets {
						// 从 targetID 或 qualifiedMap 的对应节点获取被调用者的 file_path。
						// 比较 callerDir 与 targetID 的所属目录，优先匹配同包（相同目录）下的定义。
						// Go 格式 targetID 类似 'scip:core-go/cmd/mint/main.go::main' 或包含路径
						targetPath := ""
						if strings.HasPrefix(targetID, "scip:") {
							// scip:path::symbol
							parts := strings.Split(strings.TrimPrefix(targetID, "scip:"), "::")
							if len(parts) > 0 {
								targetPath = parts[0]
							}
						} else if strings.Contains(targetID, "::") {
							// heuristic/syntax ID format: lang:path/to/file::symbol or file:path/to/file::symbol
							parts := strings.Split(targetID, "::")
							if len(parts) > 0 {
								rawPath := parts[0]
								if idx := strings.Index(rawPath, ":"); idx >= 0 {
									rawPath = rawPath[idx+1:]
								}
								targetPath = rawPath
							}
						}

						if targetPath != "" {
							targetDir := filepath.ToSlash(filepath.Dir(targetPath))
							if targetDir == callerDir {
								refinedTargets = append(refinedTargets, targetID)
							}
						}
					}
					// 如果就近原则筛选出了唯一的或更少的目标，采用精细化后的结果；否则回退保留原有匹配
					if len(refinedTargets) > 0 {
						targets = refinedTargets
					}
				}

				if isAmbiguousHeuristicCall(targets) {
					continue
				}

				for _, targetID := range targets {
					if targetID == callerID {
						continue
					}
					_, _ = insertStmt.Exec(callerID, targetID, lineNum, m[0]+1)
				}
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	var callEdges int
	_ = db.Get(&callEdges, `SELECT COUNT(*) FROM astramap_edges WHERE provenance = 'heuristic' AND kind = 'calls' AND source NOT LIKE 'route:%'`)
	logInfo("ResolveCrossFileCalls: resolved %d heuristic call edges", callEdges)
	return nil
}

func buildFunctionPointerFieldMap(projectRoot string, shortMap map[string][]string) map[string][]string {
	fieldMap := make(map[string][]string)
	seen := make(map[string]map[string]struct{})

	structFieldsMap := make(map[string][]string)
	typedefMap := make(map[string]string)

	structRe := regexp.MustCompile(`struct\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*\{([\s\S]*?)\}`)
	funcPtrFieldRe := regexp.MustCompile(`\(\s*\*\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\)`)
	typedefRe := regexp.MustCompile(`typedef\s+struct\s+([a-zA-Z_][a-zA-Z0-9_]*)\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*;`)

	// Pass 1: Parse all structs & typedefs
	_ = filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if hasHiddenSegment(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".c" && ext != ".h" && ext != ".cpp" && ext != ".cc" && ext != ".cxx" && ext != ".hpp" && ext != ".hxx" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)

		structMatches := structRe.FindAllStringSubmatch(content, -1)
		for _, m := range structMatches {
			structName := m[1]
			body := m[2]
			var fields []string
			lines := strings.Split(body, "\n")
			for _, line := range lines {
				fSub := funcPtrFieldRe.FindStringSubmatch(line)
				if len(fSub) > 1 {
					fields = append(fields, fSub[1])
				}
			}
			if len(fields) > 0 {
				structFieldsMap[structName] = fields
			}
		}

		typedefMatches := typedefRe.FindAllStringSubmatch(content, -1)
		for _, m := range typedefMatches {
			typedefMap[m[2]] = m[1]
		}
		return nil
	})

	for alias, realName := range typedefMap {
		if fields, ok := structFieldsMap[realName]; ok {
			structFieldsMap[alias] = fields
		}
	}

	// Pass 2: Extract assignments
	_ = filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if hasHiddenSegment(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".c" && ext != ".h" && ext != ".cpp" && ext != ".cc" && ext != ".cxx" && ext != ".hpp" && ext != ".hxx" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)

		// 2.1 Designated initializer mode (.init = func)
		matches := functionPointerInit.FindAllStringSubmatch(content, -1)
		for _, match := range matches {
			if len(match) < 3 {
				continue
			}
			fieldName := match[1]
			funcName := match[2]
			for _, targetID := range shortMap[funcName] {
				if seen[fieldName] == nil {
					seen[fieldName] = make(map[string]struct{})
				}
				if _, exists := seen[fieldName][targetID]; exists {
					continue
				}
				seen[fieldName][targetID] = struct{}{}
				fieldMap[fieldName] = append(fieldMap[fieldName], targetID)
			}
		}

		// 2.2 Sequential initialization mode (fireflys_api_t api = { func1, func2 })
		initVarRe := regexp.MustCompile(`(?:const\s+)?([a-zA-Z_][a-zA-Z0-9_]*)\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*=\s*\{([\s\S]*?)\}`)
		initVarMatches := initVarRe.FindAllStringSubmatch(content, -1)
		for _, m := range initVarMatches {
			typeName := m[1]
			fields, hasFields := structFieldsMap[typeName]
			if !hasFields {
				if realType, ok := typedefMap[typeName]; ok {
					fields, hasFields = structFieldsMap[realType]
				}
			}

			if hasFields && len(fields) > 0 {
				body := m[3]
				bodyClean := removeCComments(body)
				items := splitCInitList(bodyClean)
				for idx, item := range items {
					if idx >= len(fields) {
						break
					}
					fieldName := fields[idx]
					funcName := strings.TrimSpace(item)
					funcName = strings.TrimPrefix(funcName, "&")
					funcName = strings.TrimSpace(funcName)
					if funcName == "" || funcName == "NULL" || funcName == "0" || funcName == "nullptr" {
						continue
					}
					if !isValidCIdentifier(funcName) {
						continue
					}
					for _, targetID := range shortMap[funcName] {
						if seen[fieldName] == nil {
							seen[fieldName] = make(map[string]struct{})
						}
						if _, exists := seen[fieldName][targetID]; exists {
							continue
						}
						seen[fieldName][targetID] = struct{}{}
						fieldMap[fieldName] = append(fieldMap[fieldName], targetID)
					}
				}
			}
		}
		return nil
	})
	return fieldMap
}

func removeCComments(s string) string {
	mlRe := regexp.MustCompile(`/\*[\s\S]*?\*/`)
	s = mlRe.ReplaceAllString(s, "")
	slRe := regexp.MustCompile(`//.*`)
	s = slRe.ReplaceAllString(s, "")
	return s
}

func splitCInitList(s string) []string {
	var items []string
	var current strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '{' {
			depth++
			current.WriteByte(c)
		} else if c == '}' {
			depth--
			current.WriteByte(c)
		} else if c == ',' && depth == 0 {
			items = append(items, strings.TrimSpace(current.String()))
			current.Reset()
		} else {
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		items = append(items, strings.TrimSpace(current.String()))
	}
	return items
}

func isValidCIdentifier(s string) bool {
	if len(s) == 0 {
		return false
	}
	first := s[0]
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || first == '_') {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

func macroReturnCallTargets(line string, shortMap, fieldFunctionMap map[string][]string) []string {
	var targets []string
	seen := make(map[string]struct{})
	matches := macroReturnCallRe.FindAllStringSubmatch(line, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		expr := strings.TrimSpace(match[1])
		for _, targetID := range targetsForCallExpression(expr, shortMap, fieldFunctionMap) {
			if _, exists := seen[targetID]; exists {
				continue
			}
			seen[targetID] = struct{}{}
			targets = append(targets, targetID)
		}
	}
	return targets
}

func targetsForCallExpression(expr string, shortMap, fieldFunctionMap map[string][]string) []string {
	name := trailingIdentifier(expr)
	if name == "" {
		return nil
	}
	if strings.Contains(expr, "->") || strings.Contains(expr, ".") {
		if targets := fieldFunctionMap[name]; len(targets) > 0 {
			return targets
		}
	}
	return shortMap[name]
}

func trailingIdentifier(expr string) string {
	expr = strings.TrimSpace(strings.TrimPrefix(expr, "&"))
	end := len(expr)
	for end > 0 {
		c := expr[end-1]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			break
		}
		end--
	}
	start := end
	for start > 0 {
		c := expr[start-1]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			start--
		} else {
			break
		}
	}
	if start == end {
		return ""
	}
	return expr[start:end]
}

func isAmbiguousHeuristicCall(targets []string) bool {
	return len(targets) > 1
}

// ===== Cross-Language Command Execution Resolver =====
//
// SCIP is a single-language protocol. When Go code calls exec.Command to
// spawn a Python script, or Python uses subprocess.run to invoke another Python
// file, no cross-language call edge is emitted by the language servers.
// This resolver bridges that gap by pattern-matching common command execution
// APIs and creating synthetic 'calls' edges from the caller function to the
// target script's entry point.

// crossLangCmdMatch captures one detected cross-language command invocation.
type crossLangCmdMatch struct {
	callerID string // USN of the calling function
	targetID string // USN of the target script's entry function
	line     int
	col      int
}

// ResolveCrossLanguageCalls scans all indexed source files for patterns that
// invoke external scripts via command execution APIs (exec.Command,
// subprocess.run, os.system, etc.) and creates synthetic call edges from the
// enclosing function to the target script's entry point.
func ResolveCrossLanguageCalls(db *sqlx.DB, projectRoot string) error {
	filter, err := LoadIndexFilter(projectRoot)
	if err != nil {
		return fmt.Errorf("load project filter: %w", err)
	}

	// Build a lookup of Python file paths -> entry function node ID
	type entryNode struct {
		ID        string `db:"id"`
		FilePath  string `db:"file_path"`
		StartLine int    `db:"start_line"`
	}
	var entries []entryNode
	// Heuristic: the first function/method in a .py file is treated as the
	// script entry point. In practice this is often `main()` or the top-level
	// block, but SCIP indexes functions, not top-level statements.
	err = db.Select(&entries, `
		SELECT id, file_path, start_line FROM astramap_nodes
		WHERE language = 'python' AND kind IN ('function', 'method')
		GROUP BY file_path
		HAVING start_line = MIN(start_line)
	`)
	if err != nil {
		return fmt.Errorf("query python entry nodes: %w", err)
	}
	pyFileToEntry := make(map[string]string)
	for _, e := range entries {
		pyFileToEntry[e.FilePath] = e.ID
	}
	if len(pyFileToEntry) == 0 {
		logInfo("ResolveCrossLanguageCalls: no Python entry points found, skipping")
		return nil
	}

	var files []string
	err = db.Select(&files, "SELECT path FROM astramap_files")
	if err != nil {
		return fmt.Errorf("query files: %w", err)
	}

	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Remove stale synthetic edges
	_, _ = tx.Exec("DELETE FROM astramap_edges WHERE provenance = 'cross-lang' AND kind = 'calls'")

	insertStmt, err := tx.Preparex(`
		INSERT OR IGNORE INTO astramap_edges (source, target, kind, provenance, line, col, metadata)
		VALUES (?, ?, 'calls', 'cross-lang', ?, ?, '')
	`)
	if err != nil {
		return err
	}
	defer insertStmt.Close()

	for _, fp := range files {
		if !filter.Allows(fp, StageSyntax) {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(projectRoot, fp))
		if readErr != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		var funcs []funcNode
		if selectErr := db.Select(&funcs,
			"SELECT id, start_line, end_line FROM astramap_nodes WHERE file_path = ? AND kind IN ('function', 'method')",
			fp); selectErr != nil {
			continue
		}

		for i, line := range lines {
			lineNum := i + 1
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
				continue
			}

			// Determine caller function for this line
			var callerID string
			callerSpan := 0
			for _, lf := range funcs {
				if lineNum >= lf.StartLine && lineNum <= lf.EndLine {
					span := lf.EndLine - lf.StartLine
					if callerID == "" || span < callerSpan {
						callerID = lf.ID
						callerSpan = span
					}
				}
			}
			if callerID == "" {
				continue
			}

			// Try each pattern
			for _, m := range extractCrossLanguageTargets(line, pyFileToEntry) {
				if m.callerID == "" {
					m.callerID = callerID
				}
				if m.callerID == m.targetID {
					continue
				}
				_, _ = insertStmt.Exec(m.callerID, m.targetID, lineNum, m.col)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	var count int
	_ = db.Get(&count, "SELECT COUNT(*) FROM astramap_edges WHERE provenance = 'cross-lang' AND kind = 'calls'")
	logInfo("ResolveCrossLanguageCalls: resolved %d cross-language call edges", count)
	return nil
}

// extractCrossLanguageTargets applies all cross-language command patterns to a
// single source line and returns any matches.
func extractCrossLanguageTargets(line string, pyFileToEntry map[string]string) []crossLangCmdMatch {
	var matches []crossLangCmdMatch

	// Go: exec.Command("python3", "path/to/script.py")
	for _, m := range goExecCommandRe.FindAllStringSubmatchIndex(line, -1) {
		if len(m) >= 4 {
			scriptPath := line[m[2]:m[3]]
			if target := pyFileToEntry[scriptPath]; target != "" {
				matches = append(matches, crossLangCmdMatch{targetID: target, col: m[0] + 1})
			}
		}
	}

	// Python: subprocess.run(["python3", "script.py"])
	for _, m := range pySubprocessRunRe.FindAllStringSubmatchIndex(line, -1) {
		if len(m) >= 4 {
			scriptPath := line[m[2]:m[3]]
			if target := pyFileToEntry[scriptPath]; target != "" {
				matches = append(matches, crossLangCmdMatch{targetID: target, col: m[0] + 1})
			}
		}
	}

	// Python: os.system("python3 script.py")
	for _, m := range pyOsSystemRe.FindAllStringSubmatchIndex(line, -1) {
		if len(m) >= 4 {
			scriptPath := line[m[2]:m[3]]
			if target := pyFileToEntry[scriptPath]; target != "" {
				matches = append(matches, crossLangCmdMatch{targetID: target, col: m[0] + 1})
			}
		}
	}

	// Python: exec(open("file.py").read())
	for _, m := range pyExecOpenRe.FindAllStringSubmatchIndex(line, -1) {
		if len(m) >= 4 {
			scriptPath := line[m[2]:m[3]]
			if target := pyFileToEntry[scriptPath]; target != "" {
				matches = append(matches, crossLangCmdMatch{targetID: target, col: m[0] + 1})
			}
		}
	}

	return matches
}
