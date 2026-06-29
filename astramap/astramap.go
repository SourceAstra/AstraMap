package astramap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jmoiron/sqlx"
	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

// ===== AstraMap 日志辅助 (输出到 Stderr 不污染 stdio MCP 通道) =====

func logInfo(format string, v ...interface{}) {
	fmt.Fprintf(os.Stderr, "[INFO] "+format+"\n", v...)
}

func logError(format string, v ...interface{}) {
	fmt.Fprintf(os.Stderr, "[ERROR] "+format+"\n", v...)
}

// ===== AstraMap 核心数据模型 =====

type AstraMapNode struct {
	ID            string `db:"id" json:"id"`
	Kind          string `db:"kind" json:"kind"`
	Name          string `db:"name" json:"name"`
	QualifiedName string `db:"qualified_name" json:"qualifiedName"`
	FilePath      string `db:"file_path" json:"filePath"`
	Language      string `db:"language" json:"language"`
	StartLine     int    `db:"start_line" json:"startLine"`
	EndLine       int    `db:"end_line" json:"endLine"`
	StartColumn   int    `db:"start_column" json:"startColumn"`
	EndColumn     int    `db:"end_column" json:"endColumn"`
	Signature     string `db:"signature" json:"signature,omitempty"`
	Docstring     string `db:"docstring" json:"docstring,omitempty"`
	Visibility    string `db:"visibility" json:"visibility,omitempty"`
	ReturnType    string `db:"return_type" json:"returnType,omitempty"`
	IsExported    int    `db:"is_exported" json:"isExported"`
	UpdatedAt     int64  `db:"updated_at" json:"updatedAt"`
}

type AstraMapEdge struct {
	ID         int64  `db:"id" json:"id"`
	Source     string `db:"source" json:"source"`
	Target     string `db:"target" json:"target"`
	Kind       string `db:"kind" json:"kind"`
	Provenance string `db:"provenance" json:"provenance"`
	Line       int    `db:"line" json:"line"`
	Col        int    `db:"col" json:"col"`
	Metadata   string `db:"metadata" json:"metadata,omitempty"`
}

type AstraMapFile struct {
	Path        string `db:"path" json:"path"`
	ContentHash string `db:"content_hash" json:"contentHash"`
	Language    string `db:"language" json:"language"`
	Size        int64  `db:"size" json:"size"`
	ModifiedAt  int64  `db:"modified_at" json:"modifiedAt"`
	IndexedAt   int64  `db:"indexed_at" json:"indexedAt"`
	NodeCount   int    `db:"node_count" json:"nodeCount"`
	Errors      string `db:"errors" json:"errors,omitempty"`
}

type AstraMapVerdict struct {
	ID              int64  `db:"id" json:"id"`
	SymbolID        string `db:"symbol_id" json:"symbolId"`
	HasActiveDefect int    `db:"has_active_defect" json:"hasActiveDefect"`
	Stage           string `db:"stage" json:"stage"`
	Decision        string `db:"decision" json:"decision"`
	RuleID          string `db:"rule_id" json:"ruleId"`
	Description     string `db:"description" json:"description"`
	Suggestion      string `db:"suggestion" json:"suggestion"`
	Operator        string `db:"operator" json:"operator"`
	UpdatedAt       int64  `db:"updated_at" json:"updatedAt"`
}

// ===== SCIP 索引导入器 =====

// ImportScipIndexToAstraMap 解析 SCIP 索引并全量同步到 SQLite 的 AstraMap 表中
func ImportScipIndexToAstraMap(db *sqlx.DB, scipPath, projectRoot string) error {
	logInfo("ImportScipIndexToAstraMap: 开始导入 SCIP 索引: %s", scipPath)
	logInfo("ImportScipIndexToAstraMap: 正在载入中，请稍后......")
	filter, err := LoadIndexFilter(projectRoot)
	if err != nil {
		return fmt.Errorf("读取 AstraMap 配置失败: %w", err)
	}
	data, err := os.ReadFile(scipPath)
	if err != nil {
		return fmt.Errorf("读取 SCIP 索引文件失败: %w", err)
	}

	var index scip.Index
	if err := proto.Unmarshal(data, &index); err != nil {
		return fmt.Errorf("Protobuf 反序列化失败: %w", err)
	}

	// 1. 缓存 SymbolInformation 以进行富文本填充
	scipSymMap := make(map[string]*scip.SymbolInformation)
	for _, extSym := range index.ExternalSymbols {
		scipSymMap[extSym.Symbol] = extSym
	}
	for _, doc := range index.Documents {
		for _, symInfo := range doc.Symbols {
			scipSymMap[symInfo.Symbol] = symInfo
		}
	}

	var nodes []*AstraMapNode
	var edges []*AstraMapEdge
	now := time.Now().Unix()
	globalScipToUsn := make(map[string]string)
	fileNodeCounts := make(map[string]int)
	fileLanguages := make(map[string]string)

	for _, doc := range index.Documents {
		if !filter.Allows(doc.RelativePath, StageScip) {
			continue
		}
		for _, occ := range doc.Occurrences {
			if (occ.SymbolRoles&int32(scip.SymbolRole_Definition)) == 0 || occ.Symbol == "" {
				continue
			}
			info := extractSymbolInfo(occ.Symbol, scipSymMap)
			if info.name == "" || len(info.name) <= 1 {
				continue
			}
			if isSyntheticAnonymousSymbol(occ.Symbol, info.name) {
				continue
			}
			usn := occ.Symbol
			if len(usn) > 200 {
				usn = fmt.Sprintf("scip:%s::%s", doc.RelativePath, info.name)
			}
			globalScipToUsn[occ.Symbol] = usn
		}
	}

	// 2. 遍历 Documents 提取节点和边
	for _, doc := range index.Documents {
		relPath := doc.RelativePath
		if !filter.Allows(relPath, StageScip) {
			continue
		}
		docLang := normalizeLanguage(doc.Language, relPath)
		fileLanguages[relPath] = docLang

		// 排序 occurrences 从而计算精确的函数 end_line
		type defInfo struct {
			occ       *scip.Occurrence
			startLine int
		}
		var defs []defInfo

		for _, occ := range doc.Occurrences {
			if (occ.SymbolRoles&int32(scip.SymbolRole_Definition)) == 0 || occ.Symbol == "" {
				continue
			}
			startLine := 1
			if len(occ.Range) > 0 {
				startLine = int(occ.Range[0]) + 1
			}
			defs = append(defs, defInfo{occ: occ, startLine: startLine})
		}

		sort.Slice(defs, func(i, j int) bool {
			return defs[i].startLine < defs[j].startLine
		})

		// 第一遍：创建节点
		scipToUsn := make(map[string]string)
		sourceLines := readSourceLinesBestEffort(projectRoot, relPath)
		for idx, d := range defs {
			occ := d.occ
			info := extractSymbolInfo(occ.Symbol, scipSymMap)
			if info.name == "" || len(info.name) <= 1 {
				continue
			}
			if isSyntheticAnonymousSymbol(occ.Symbol, info.name) {
				continue
			}

			startLine := d.startLine
			endLine := startLine + 4 // 默认估算值

			// 获取本文件下一个定义起始行作为 endLine
			if idx+1 < len(defs) {
				endLine = defs[idx+1].startLine - 1
			}
			if info.isFunc {
				endLine = estimateFunctionEndLine(sourceLines, startLine)
			}
			if endLine < startLine {
				endLine = startLine
			}

			startCol := 1
			endCol := 1
			if len(occ.Range) > 1 {
				startCol = int(occ.Range[1]) + 1
			}
			if len(occ.Range) > 3 {
				endCol = int(occ.Range[3]) + 1
			}

			// 获取富文本信息
			docstring := ""
			signature := ""
			visibility := "public"
			returnType := ""
			isExported := 0

			if symInfo, exists := scipSymMap[occ.Symbol]; exists {
				if len(symInfo.Documentation) > 0 {
					docstring = strings.Join(symInfo.Documentation, "\n")
				}
				signature = symInfo.SignatureDocumentation.GetText()
				if strings.Contains(occ.Symbol, "private") {
					visibility = "private"
				} else if strings.Contains(occ.Symbol, "protected") {
					visibility = "protected"
				}
			}

			// isExported: Go 语言按首字母大写判断; 其他语言默认 0
			if docLang == "go" {
				r, _ := utf8.DecodeRuneInString(info.name)
				if r != utf8.RuneError && unicode.IsUpper(r) {
					isExported = 1
				}
			}

			// 格式化 QualifiedName
			qname := info.name
			parts := strings.Split(occ.Symbol, " ")
			if len(parts) > 0 {
				lastPart := parts[len(parts)-1]
				lastPart = strings.TrimSuffix(lastPart, ".")
				qname = strings.ReplaceAll(lastPart, "/", ".")
				qname = strings.ReplaceAll(qname, "#", ".")
				qname = strings.ReplaceAll(qname, ":", ".")
			}

			// 缩短超长 ID 保证物理主键稳定性
			usn := occ.Symbol
			if len(usn) > 200 {
				usn = fmt.Sprintf("scip:%s::%s", relPath, info.name)
			}

			node := &AstraMapNode{
				ID:            usn,
				Kind:          info.symType,
				Name:          info.name,
				QualifiedName: qname,
				FilePath:      relPath,
				Language:      docLang,
				StartLine:     startLine,
				EndLine:       endLine,
				StartColumn:   startCol,
				EndColumn:     endCol,
				Signature:     signature,
				Docstring:     docstring,
				Visibility:    visibility,
				ReturnType:    returnType,
				IsExported:    isExported,
				UpdatedAt:     now,
			}
			nodes = append(nodes, node)
			fileNodeCounts[relPath]++
			scipToUsn[occ.Symbol] = usn
			globalScipToUsn[occ.Symbol] = usn
		}

		// 第二遍：创建边 (调用边)
		for _, occ := range doc.Occurrences {
			if occ.Symbol == "" || len(occ.Range) == 0 {
				continue
			}
			if (occ.SymbolRoles & int32(scip.SymbolRole_Definition)) != 0 {
				continue
			}

			occLine := int(occ.Range[0]) + 1
			occCol := 1
			if len(occ.Range) > 1 {
				occCol = int(occ.Range[1]) + 1
			}

			info := extractSymbolInfo(occ.Symbol, scipSymMap)
			if !info.isFunc || info.name == "" {
				continue
			}

			// 找到包围该 Occurrence 的 Caller 函数
			callerUSN := ""
			for _, node := range nodes {
				if node.FilePath == relPath && (node.Kind == "function" || node.Kind == "method") &&
					occLine >= node.StartLine && occLine <= node.EndLine {
					callerUSN = node.ID
					break
				}
			}

			if callerUSN == "" {
				continue
			}

			targetUSN := scipToUsn[occ.Symbol]
			if targetUSN == "" {
				targetUSN = globalScipToUsn[occ.Symbol]
			}
			if targetUSN == "" {
				targetUSN = fmt.Sprintf("external:%s", occ.Symbol)
			}

			if callerUSN == targetUSN {
				continue
			}

			edges = append(edges, &AstraMapEdge{
				Source:     callerUSN,
				Target:     targetUSN,
				Kind:       "calls",
				Provenance: "scip",
				Line:       occLine,
				Col:        occCol,
			})
		}
	}
	logInfo("ImportScipIndexToAstraMap: 正在写入索引，请稍后......")

	// 3. 执行数据库批量写入 (Transaction)
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 清理该项目下旧的 SCIP 数据
	_, _ = tx.Exec("DELETE FROM astramap_edges WHERE provenance = 'scip'")
	_, _ = tx.Exec("DELETE FROM astramap_nodes WHERE id LIKE 'scip:%' OR id LIKE 'go:%' OR id LIKE 'cxx:%'")

	// 批量插入 Nodes
	nodeStmt, err := tx.Preparex(`
		INSERT INTO astramap_nodes (
			id, kind, name, qualified_name, file_path, language,
			start_line, end_line, start_column, end_column,
			signature, docstring, visibility, return_type, is_exported, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			kind=excluded.kind,
			name=excluded.name,
			qualified_name=excluded.qualified_name,
			start_line=excluded.start_line,
			end_line=excluded.end_line,
			signature=excluded.signature,
			docstring=excluded.docstring,
			updated_at=excluded.updated_at
	`)
	if err != nil {
		return err
	}
	defer nodeStmt.Close()

	for _, n := range nodes {
		_, err = nodeStmt.Exec(
			n.ID, n.Kind, n.Name, n.QualifiedName, n.FilePath, n.Language,
			n.StartLine, n.EndLine, n.StartColumn, n.EndColumn,
			n.Signature, n.Docstring, n.Visibility, n.ReturnType, n.IsExported, n.UpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("插入节点失败 (%s): %w", n.ID, err)
		}
	}

	fileStmt, err := tx.Preparex(`
		INSERT INTO astramap_files (path, content_hash, language, size, modified_at, indexed_at, node_count, errors)
		VALUES (?, ?, ?, ?, ?, ?, ?, '')
		ON CONFLICT(path) DO UPDATE SET
			content_hash=excluded.content_hash,
			language=excluded.language,
			size=excluded.size,
			modified_at=excluded.modified_at,
			indexed_at=excluded.indexed_at,
			node_count=excluded.node_count,
			errors=''
	`)
	if err != nil {
		return err
	}
	defer fileStmt.Close()

	for relPath, lang := range fileLanguages {
		absPath := filepath.Join(projectRoot, relPath)
		contentHash, _ := hashFile(absPath)
		var size int64
		var modifiedAt int64
		if stat, statErr := os.Stat(absPath); statErr == nil {
			size = stat.Size()
			modifiedAt = stat.ModTime().Unix()
		}
		_, _ = fileStmt.Exec(relPath, contentHash, lang, size, modifiedAt, now, fileNodeCounts[relPath])
	}

	// 插入 external: 占位节点（FK 约束要求边目标必须存在于 nodes 表）
	externalSeen := make(map[string]bool)
	for _, e := range edges {
		if strings.HasPrefix(e.Target, "external:") && !externalSeen[e.Target] {
			externalSeen[e.Target] = true
			name := e.Target[len("external:"):]
			_, _ = tx.Exec(`INSERT OR IGNORE INTO astramap_nodes
				(id, kind, name, qualified_name, file_path, language, is_exported, updated_at)
				VALUES (?, 'external', ?, ?, '', '', 0, ?)`,
				e.Target, name, name, time.Now().Unix())
		}
	}

	// 批量插入 Edges
	edgeStmt, err := tx.Preparex(`
		INSERT OR IGNORE INTO astramap_edges (source, target, kind, provenance, line, col, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer edgeStmt.Close()

	for _, e := range edges {
		_, err = edgeStmt.Exec(e.Source, e.Target, e.Kind, e.Provenance, e.Line, e.Col, e.Metadata)
		if err != nil {
			return fmt.Errorf("插入边失败 (%s -> %s): %w", e.Source, e.Target, err)
		}
	}

	// 同步并解析相关的 verdicts
	_ = syncVerdictsFromProject(tx, projectRoot)

	// 提交事务
	if err := tx.Commit(); err != nil {
		return err
	}

	// 4. 触发启发式边粘合
	_ = ResolveGoInterfaces(db)
	_ = ResolveWebRoutes(db, projectRoot)

	if err := ResolveCrossFileCalls(db, projectRoot); err != nil {
		logError("ResolveCrossFileCalls failed: %v", err)
	}

	logInfo("ImportScipIndexToAstraMap: 成功导入 %d 节点, %d 调用边", len(nodes), len(edges))
	return nil
}

// ===== 增量文件侦听同步 (Watcher & Incremental Sync) =====

// SyncFileAstraMap 增量同步单文件，计算 Hash 脏状态进行过滤，并提供事务合并写入
func SyncFileAstraMap(db *sqlx.DB, projectRoot, filePath string) (bool, error) {
	absPath := filePath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(projectRoot, filePath)
	}

	relPath, err := filepath.Rel(projectRoot, absPath)
	if err != nil {
		relPath = filePath
	}

	stat, err := os.Stat(absPath)
	if err != nil {
		_, _ = db.Exec("DELETE FROM astramap_nodes WHERE file_path = ?", relPath)
		_, _ = db.Exec("DELETE FROM astramap_files WHERE path = ?", relPath)
		return true, nil
	}

	contentHash, err := hashFile(absPath)
	if err != nil {
		return false, err
	}
	var existingHash string
	_ = db.Get(&existingHash, "SELECT content_hash FROM astramap_files WHERE path = ?", relPath)
	if existingHash == contentHash {
		return false, nil
	}

	nodes, edges, _, err := ParseFileIncremental(projectRoot, relPath)
	if err != nil {
		return false, err
	}

	tx, err := db.Beginx()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	_, _ = tx.Exec("DELETE FROM astramap_nodes WHERE file_path = ?", relPath)

	nodeStmt, err := tx.Preparex(`
		INSERT INTO astramap_nodes (
			id, kind, name, qualified_name, file_path, language,
			start_line, end_line, start_column, end_column,
			signature, docstring, visibility, return_type, is_exported, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return false, err
	}
	defer nodeStmt.Close()

	for _, n := range nodes {
		_, err = nodeStmt.Exec(
			n.ID, n.Kind, n.Name, n.QualifiedName, n.FilePath, n.Language,
			n.StartLine, n.EndLine, n.StartColumn, n.EndColumn,
			n.Signature, n.Docstring, n.Visibility, n.ReturnType, n.IsExported, n.UpdatedAt,
		)
		if err != nil {
			return false, err
		}
	}

	edgeStmt, err := tx.Preparex(`
		INSERT OR IGNORE INTO astramap_edges (source, target, kind, provenance, line, col, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return false, err
	}
	defer edgeStmt.Close()

	for _, e := range edges {
		_, err = edgeStmt.Exec(e.Source, e.Target, e.Kind, e.Provenance, e.Line, e.Col, e.Metadata)
		if err != nil {
			return false, err
		}
	}

	if len(nodes) > 0 {
		_, _ = tx.Exec(`
			INSERT INTO astramap_files (path, content_hash, language, size, modified_at, indexed_at, node_count, errors)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(path) DO UPDATE SET
				content_hash=excluded.content_hash,
				size=excluded.size,
				modified_at=excluded.modified_at,
				indexed_at=excluded.indexed_at,
				node_count=excluded.node_count
		`, relPath, contentHash, nodes[0].Language, stat.Size(), stat.ModTime().Unix(), time.Now().Unix(), len(nodes), "")
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}

	return true, nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func readSourceLinesBestEffort(projectRoot, relPath string) []string {
	data, err := os.ReadFile(filepath.Join(projectRoot, relPath))
	if err != nil {
		return nil
	}
	return strings.Split(string(data), "\n")
}

func estimateFunctionEndLine(lines []string, startLine int) int {
	if len(lines) == 0 || startLine <= 0 || startLine > len(lines) {
		return startLine
	}

	braceDepth := 0
	seenOpenBrace := false
	for lineNo := startLine; lineNo <= len(lines); lineNo++ {
		line := stripLineForBraceScan(lines[lineNo-1])
		for _, r := range line {
			switch r {
			case '{':
				braceDepth++
				seenOpenBrace = true
			case '}':
				if braceDepth > 0 {
					braceDepth--
				}
				if seenOpenBrace && braceDepth == 0 {
					return lineNo
				}
			}
		}
	}
	return startLine
}

func stripLineForBraceScan(line string) string {
	if idx := strings.Index(line, "//"); idx >= 0 {
		line = line[:idx]
	}
	var b strings.Builder
	inString := false
	inChar := false
	escaped := false
	for _, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '"' && !inChar {
			inString = !inString
			continue
		}
		if r == '\'' && !inString {
			inChar = !inChar
			continue
		}
		if !inString && !inChar {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SyncAllFilesAstraMap 扫描项目目录，增量同步所有脏文件
// allIndexExts is the full set of extensions SyncAllFilesAstraMap will index when no filter is provided.
var allIndexExts = map[string]bool{
	".go": true, ".py": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".c": true, ".cpp": true, ".cc": true, ".cxx": true, ".h": true, ".hpp": true, ".hxx": true, ".java": true,
}

// LangExts maps language name to its source file extensions (exported for CLI use).
var LangExts = map[string][]string{
	"go":         {".go"},
	"typescript": {".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"},
	"python":     {".py"},
	"java":       {".java"},
	"c":          {".c", ".h"},
	"cpp":        {".cc", ".cpp", ".cxx", ".hpp", ".hxx"},
}

// ExtToLang maps file extension back to its language name.
var ExtToLang = func() map[string]string {
	m := make(map[string]string)
	for lang, exts := range LangExts {
		for _, ext := range exts {
			m[ext] = lang
		}
	}
	return m
}()

func SyncAllFilesAstraMap(db *sqlx.DB, projectRoot string, langFilter ...string) error {
	logInfo("SyncAllFilesAstraMap: 增量扫描 %s", projectRoot)
	filter, err := LoadIndexFilter(projectRoot)
	if err != nil {
		return fmt.Errorf("读取 AstraMap 配置失败: %w", err)
	}
	if err := PruneExcludedFiles(db, filter); err != nil {
		return err
	}

	extensions := make(map[string]bool)
	if len(langFilter) > 0 {
		for _, lang := range langFilter {
			for _, ext := range LangExts[lang] {
				extensions[ext] = true
			}
		}
	}
	if len(extensions) == 0 {
		for ext := range allIndexExts {
			extensions[ext] = true
		}
	}

	scanned := 0
	updated := 0
	err = filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		relPath, _ := filepath.Rel(projectRoot, path)
		if info.IsDir() {
			name := info.Name()
			if shouldSkipIndexDir(name) || !filter.AllowsDir(relPath, StageTreeSitter) {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if !extensions[ext] {
			return nil
		}
		if !filter.Allows(relPath, StageTreeSitter) {
			return nil
		}

		scanned++

		changed, err := SyncFileAstraMap(db, projectRoot, path)
		if err == nil && changed {
			updated++
		}
		return nil
	})

	// 触发跨文件调用解析
	logInfo("SyncAllFilesAstraMap: 扫描完成 %d 文件, %d 更新, 解析全局关系", scanned, updated)
	_ = ResolveGoInterfaces(db)
	_ = ResolveWebRoutes(db, projectRoot)
	if err2 := ResolveCrossFileCalls(db, projectRoot); err2 != nil {
		logError("ResolveCrossFileCalls failed: %v", err2)
	}

	logInfo("SyncAllFilesAstraMap: 就绪, %d 文件, %d 更新", scanned, updated)
	return err
}

func PruneExcludedFiles(db *sqlx.DB, filter *IndexFilter) error {
	var files []string
	if err := db.Select(&files, "SELECT path FROM astramap_files"); err != nil {
		return fmt.Errorf("query indexed files failed: %w", err)
	}

	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, filePath := range files {
		if filter.Allows(filePath, StageTreeSitter) || filter.Allows(filePath, StageScip) {
			continue
		}

		if _, err := tx.Exec(
			"DELETE FROM astramap_edges WHERE source IN (SELECT id FROM astramap_nodes WHERE file_path = ?) OR target IN (SELECT id FROM astramap_nodes WHERE file_path = ?)",
			filePath, filePath,
		); err != nil {
			return fmt.Errorf("delete edges for excluded file %s failed: %w", filePath, err)
		}

		if _, err := tx.Exec("DELETE FROM astramap_nodes WHERE file_path = ?", filePath); err != nil {
			return fmt.Errorf("delete nodes for excluded file %s failed: %w", filePath, err)
		}
		if _, err := tx.Exec("DELETE FROM astramap_files WHERE path = ?", filePath); err != nil {
			return fmt.Errorf("delete indexed file %s failed: %w", filePath, err)
		}
	}

	return tx.Commit()
}

// ProvenanceStats returns node counts by language and edge counts by provenance.
func ProvenanceStats(db *sqlx.DB) (map[string]int, map[string]int, error) {
	nodeStats := make(map[string]int)
	edgeStats := make(map[string]int)

	type row struct {
		Key   string `db:"key"`
		Count int    `db:"cnt"`
	}
	var rows []row
	if err := db.Select(&rows, "SELECT language AS key, COUNT(*) AS cnt FROM astramap_nodes GROUP BY language"); err == nil {
		for _, r := range rows {
			nodeStats[r.Key] = r.Count
		}
	}
	rows = nil
	if err := db.Select(&rows, "SELECT provenance AS key, COUNT(*) AS cnt FROM astramap_edges GROUP BY provenance"); err == nil {
		for _, r := range rows {
			edgeStats[r.Key] = r.Count
		}
	}
	return nodeStats, edgeStats, nil
}

func shouldSkipIndexDir(name string) bool {
	switch name {
	case ".git", ".astramap", ".understand-anything", ".cache", ".idea", ".vscode",
		"node_modules", "build", "dist", "vendor", "target", "out", "tmp", "temp":
		return true
	default:
		return strings.HasPrefix(name, ".trash")
	}
}

// ===== Heuristic Resolvers (启发式粘合解析器) =====

// ResolveGoInterfaces Go 隐式接口解析器：struct 方法集完全覆盖 interface 方法集则建立 implements 边
func ResolveGoInterfaces(db *sqlx.DB) error {
	type idName struct {
		ID   string `db:"id"`
		Name string `db:"name"`
	}

	// 1. 查出所有 interface 和 struct 节点
	var interfaces []idName
	if err := db.Select(&interfaces, "SELECT id, name FROM astramap_nodes WHERE kind = 'interface'"); err != nil {
		return err
	}
	if len(interfaces) == 0 {
		return nil
	}

	var structs []idName
	if err := db.Select(&structs, "SELECT id, name FROM astramap_nodes WHERE kind = 'struct'"); err != nil {
		return err
	}

	// 2. 构建 contains 边查询: 获取 interface/struct 通过 contains 边关联的方法名集合
	type containsRow struct {
		Source     string `db:"source"`
		MethodName string `db:"name"`
	}
	var containsEdges []containsRow
	if err := db.Select(&containsEdges, `
		SELECT e.source, n.name
		FROM astramap_edges e
		JOIN astramap_nodes n ON n.id = e.target
		WHERE e.kind = 'contains'
		  AND n.kind = 'method'
	`); err != nil {
		return err
	}

	// 按 source 分组方法名
	ownerMethods := make(map[string]map[string]struct{})
	for _, row := range containsEdges {
		if ownerMethods[row.Source] == nil {
			ownerMethods[row.Source] = make(map[string]struct{})
		}
		ownerMethods[row.Source][row.MethodName] = struct{}{}
	}

	// 3. 事务: 清理旧 heuristic implements 边并重建
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, _ = tx.Exec("DELETE FROM astramap_edges WHERE provenance = 'heuristic' AND kind = 'implements'")

	edgeStmt, err := tx.Preparex(`
		INSERT OR IGNORE INTO astramap_edges (source, target, kind, provenance, metadata)
		VALUES (?, ?, 'implements', 'heuristic', '')
	`)
	if err != nil {
		return err
	}
	defer edgeStmt.Close()

	// 4. 对每个 interface，检查每个 struct 是否完全覆盖其方法集
	for _, iface := range interfaces {
		ifaceMethods := ownerMethods[iface.ID]
		if len(ifaceMethods) == 0 {
			continue // 空接口不建立 implements 边
		}

		for _, st := range structs {
			structMethods := ownerMethods[st.ID]
			if len(structMethods) < len(ifaceMethods) {
				continue
			}

			// 检查完全覆盖
			covers := true
			for mName := range ifaceMethods {
				if _, ok := structMethods[mName]; !ok {
					covers = false
					break
				}
			}
			if covers {
				_, _ = edgeStmt.Exec(st.ID, iface.ID)
			}
		}
	}

	return tx.Commit()
}

// ResolveWebRoutes Web 路由反射处理器：扫描路由绑定并建立从路由到控制层 Handler 的边
func ResolveWebRoutes(db *sqlx.DB, projectRoot string) error {
	var handlers []struct {
		ID            string `db:"id"`
		Name          string `db:"name"`
		FilePath      string `db:"file_path"`
		QualifiedName string `db:"qualified_name"`
	}

	err := db.Select(&handlers, "SELECT id, name, file_path, qualified_name FROM astramap_nodes WHERE kind IN ('function', 'method')")
	if err != nil {
		return err
	}

	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, _ = tx.Exec("DELETE FROM astramap_edges WHERE provenance = 'heuristic' AND kind = 'route'")
	_, _ = tx.Exec("DELETE FROM astramap_nodes WHERE kind = 'route'")

	routeRe := regexp.MustCompile(`(?:\.GET|\.POST|\.PUT|\.DELETE|@app\.[a-z]+)\(\s*["']([^"']+)["']\s*,\s*([a-zA-Z0-9_]+)`)

	for _, handler := range handlers {
		absPath := filepath.Join(projectRoot, handler.FilePath)
		content, err := os.ReadFile(absPath)
		if err != nil {
			continue
		}

		matches := routeRe.FindAllStringSubmatch(string(content), -1)
		for _, m := range matches {
			routePath := m[1]
			handlerName := m[2]

			if handler.Name == handlerName {
				routeUSN := fmt.Sprintf("route:%s", routePath)
				_, _ = tx.Exec(`
					INSERT INTO astramap_nodes (id, kind, name, qualified_name, file_path, language, start_line, end_line, updated_at)
					VALUES (?, 'route', ?, ?, ?, 'http', 0, 0, ?)
					ON CONFLICT(id) DO NOTHING
				`, routeUSN, routePath, routePath, handler.FilePath, time.Now().Unix())

				_, _ = tx.Exec(`
					INSERT OR IGNORE INTO astramap_edges (source, target, kind, provenance, metadata)
					VALUES (?, ?, 'calls', 'heuristic', '')
				`, routeUSN, handler.ID)
			}
		}
	}

	return tx.Commit()
}

// ===== Verdict (审计缺陷) 缓存与入库 =====

func syncVerdictsFromProject(tx *sqlx.Tx, projectRoot string) error {
	verdictPath := filepath.Join(projectRoot, "verdict.json")
	if _, err := os.Stat(verdictPath); os.IsNotExist(err) {
		jobsDir := filepath.Join(projectRoot, "jobs")
		_ = filepath.Walk(jobsDir, func(path string, info os.FileInfo, err error) error {
			if err == nil && info.Name() == "verdict.json" {
				verdictPath = path
			}
			return nil
		})
	}

	data, err := os.ReadFile(verdictPath)
	if err != nil {
		return nil
	}

	type verdictItem struct {
		RuleID      string `json:"rule_id"`
		SymbolID    string `json:"symbol_id"`
		Decision    string `json:"decision"`
		Description string `json:"description"`
		Suggestion  string `json:"suggestion"`
		Operator    string `json:"operator"`
	}

	var items []verdictItem
	if err := json.Unmarshal(data, &items); err != nil {
		var dictMap map[string]interface{}
		if err2 := json.Unmarshal(data, &dictMap); err2 == nil {
			for k, v := range dictMap {
				if m, ok := v.(map[string]interface{}); ok {
					dec, _ := m["decision"].(string)
					desc, _ := m["description"].(string)
					sug, _ := m["suggestion"].(string)
					op, _ := m["operator"].(string)
					rule, _ := m["rule_id"].(string)
					items = append(items, verdictItem{
						SymbolID:    k,
						RuleID:      rule,
						Decision:    dec,
						Description: desc,
						Suggestion:  sug,
						Operator:    op,
					})
				}
			}
		} else {
			return err
		}
	}

	_, _ = tx.Exec("DELETE FROM astramap_verdicts")

	verdictStmt, err := tx.Preparex(`
		INSERT INTO astramap_verdicts (symbol_id, has_active_defect, stage, decision, rule_id, description, suggestion, operator, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer verdictStmt.Close()

	now := time.Now().Unix()
	for _, item := range items {
		hasActive := 0
		if item.Decision == "REJECT" {
			hasActive = 1
		}
		_, _ = verdictStmt.Exec(
			item.SymbolID, hasActive, "final_verdict", item.Decision,
			item.RuleID, item.Description, item.Suggestion, item.Operator, now,
		)
	}

	return nil
}

// ===== SCIP 辅组函数移接 =====

type scipSymbolInfo struct {
	name    string
	symType string
	isFunc  bool
}

func extractSymbolInfo(sym string, infoMap map[string]*scip.SymbolInformation) scipSymbolInfo {
	if strings.HasPrefix(sym, "local ") {
		return scipSymbolInfo{name: sym, symType: "local"}
	}

	if symInfo, ok := infoMap[sym]; ok {
		name := symInfo.DisplayName
		if name == "" {
			name = parseSymbolNameFallback(sym)
		}
		symType, isFunc := getSymbolTypeFromKind(symInfo.Kind)
		kind := symInfo.Kind
		if kind == scip.SymbolInformation_UnspecifiedKind {
			parsed, err := scip.ParseSymbol(sym)
			if err == nil && len(parsed.Descriptors) > 0 {
				symType, isFunc = getSymbolTypeFromSuffix(parsed.Descriptors[len(parsed.Descriptors)-1].Suffix, sym)
			}
		}
		return scipSymbolInfo{
			name:    name,
			symType: symType,
			isFunc:  isFunc,
		}
	}

	parsed, err := scip.ParseSymbol(sym)
	if err == nil && len(parsed.Descriptors) > 0 {
		lastDesc := parsed.Descriptors[len(parsed.Descriptors)-1]
		symType, isFunc := getSymbolTypeFromSuffix(lastDesc.Suffix, sym)
		return scipSymbolInfo{
			name:    lastDesc.Name,
			symType: symType,
			isFunc:  isFunc,
		}
	}

	name := parseSymbolNameFallback(sym)
	return scipSymbolInfo{name: name, symType: "variable"}
}

func isSyntheticAnonymousSymbol(sym, name string) bool {
	return strings.HasPrefix(name, "$anonymous_type_") ||
		strings.HasPrefix(name, "$anon") ||
		strings.Contains(sym, "$anonymous_type_") ||
		strings.Contains(sym, "#$anonymous_type_")
}

func getSymbolTypeFromKind(kind scip.SymbolInformation_Kind) (string, bool) {
	switch kind {
	case scip.SymbolInformation_Interface:
		return "interface", false
	case scip.SymbolInformation_Class, scip.SymbolInformation_Struct:
		return "struct", false
	case scip.SymbolInformation_Method:
		return "method", true
	case scip.SymbolInformation_Function, scip.SymbolInformation_Constructor:
		return "function", true
	case scip.SymbolInformation_Macro:
		return "macro", false
	case scip.SymbolInformation_Parameter, scip.SymbolInformation_Variable, scip.SymbolInformation_Field:
		return "variable", false
	default:
		return "variable", false
	}
}

func getSymbolTypeFromSuffix(suffix scip.Descriptor_Suffix, sym string) (string, bool) {
	switch suffix {
	case scip.Descriptor_Method:
		return "function", true
	case scip.Descriptor_Term:
		if strings.Contains(sym, "(") {
			return "function", true
		}
		return "variable", false
	case scip.Descriptor_Type:
		return "struct", false
	case scip.Descriptor_Macro:
		return "macro", false
	case scip.Descriptor_Parameter, scip.Descriptor_TypeParameter:
		return "variable", false
	default:
		return "variable", false
	}
}

func parseSymbolNameFallback(sym string) string {
	parts := strings.Split(sym, " ")
	rest := parts[len(parts)-1]
	idx := strings.LastIndex(rest, "/")
	if idx >= 0 {
		rest = rest[idx+1:]
	}
	rest = strings.TrimSuffix(rest, ".")
	if strings.Contains(rest, "(") {
		rest = rest[:strings.Index(rest, "(")]
	}
	rest = strings.TrimSuffix(rest, "#")
	return rest
}

func normalizeLanguage(lang, filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".ts", ".tsx", ".js", ".jsx":
		return "typescript"
	case ".c", ".h":
		return "c"
	case ".cpp", ".cc", ".cxx", ".hpp", ".hxx":
		return "cpp"
	case ".java":
		return "java"
	}
	return "cxx"
}
