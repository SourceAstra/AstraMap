package astramap

import (
	"crypto/sha256"
	"encoding/hex"
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

// ===== AstraMap Log Helpers (Output to Stderr so as not to pollute stdio MCP channel) =====

var quietLogging bool

func SetQuietLogging(quiet bool) {
	quietLogging = quiet
}

func logInfo(format string, v ...interface{}) {
	if quietLogging {
		return
	}
	fmt.Fprintf(os.Stderr, "[INFO] "+format+"\n", v...)
}

func logError(format string, v ...interface{}) {
	fmt.Fprintf(os.Stderr, "[ERROR] "+format+"\n", v...)
}

// ===== AstraMap Core Data Model =====

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
	Provenance    string `db:"provenance" json:"provenance,omitempty"`
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
	Path         string `db:"path" json:"path"`
	ContentHash  string `db:"content_hash" json:"contentHash"`
	Language     string `db:"language" json:"language"`
	Size         int64  `db:"size" json:"size"`
	ModifiedAt   int64  `db:"modified_at" json:"modifiedAt"`
	ModifiedAtNS int64  `db:"modified_at_ns" json:"modifiedAtNs"`
	IndexedAt    int64  `db:"indexed_at" json:"indexedAt"`
	NodeCount    int    `db:"node_count" json:"nodeCount"`
	Errors       string `db:"errors" json:"errors,omitempty"`
}

// ===== SCIP Index Importer =====

func ValidateScipIndexFile(scipPath string) error {
	_, err := readScipIndexFile(scipPath)
	return err
}

func ScipIndexLanguages(scipPath string) ([]string, error) {
	index, err := readScipIndexFile(scipPath)
	if err != nil {
		return nil, err
	}
	languages := make(map[string]bool)
	for _, document := range index.Documents {
		if language := strings.ToLower(strings.TrimSpace(document.Language)); language != "" {
			languages[language] = true
		}
	}
	result := make([]string, 0, len(languages))
	for language := range languages {
		result = append(result, language)
	}
	sort.Strings(result)
	return result, nil
}

func readScipIndexFile(scipPath string) (scip.Index, error) {
	data, err := os.ReadFile(scipPath)
	if err != nil {
		return scip.Index{}, fmt.Errorf("failed to read SCIP index file: %w", err)
	}
	var index scip.Index
	if err := proto.Unmarshal(data, &index); err != nil {
		return scip.Index{}, fmt.Errorf("SCIP Protobuf deserialization failed: %w", err)
	}
	if len(index.Documents) == 0 {
		return scip.Index{}, fmt.Errorf("SCIP index contains no documents")
	}
	return index, nil
}

func ImportScipIndexToAstraMap(db *sqlx.DB, scipPath, projectRoot string) error {
	return ImportScipIndexesToAstraMap(db, []string{scipPath}, projectRoot)
}

// ImportScipIndexesToAstraMap parses every provider output first, then replaces
// the SCIP provenance in one transaction. No provider can erase another one.
func ImportScipIndexesToAstraMap(db *sqlx.DB, scipPaths []string, projectRoot string) error {
	if len(scipPaths) == 0 {
		return nil
	}
	logInfo("ImportScipIndexesToAstraMap: Starting batch import of %d SCIP indexes", len(scipPaths))
	filter, err := LoadIndexFilter(projectRoot)
	if err != nil {
		return fmt.Errorf("failed to read AstraMap config: %w", err)
	}
	profile := BuildProjectProfile(projectRoot, filter, StageScip)
	var index scip.Index
	for _, scipPath := range scipPaths {
		data, readErr := os.ReadFile(scipPath)
		if readErr != nil {
			return fmt.Errorf("failed to read SCIP index file %s: %w", scipPath, readErr)
		}
		var providerIndex scip.Index
		if unmarshalErr := proto.Unmarshal(data, &providerIndex); unmarshalErr != nil {
			return fmt.Errorf("SCIP Protobuf deserialization failed %s: %w", scipPath, unmarshalErr)
		}
		index.Documents = append(index.Documents, providerIndex.Documents...)
		index.ExternalSymbols = append(index.ExternalSymbols, providerIndex.ExternalSymbols...)
	}

	// 1. Cache SymbolInformation for rich text enrichment
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
		if strings.HasPrefix(doc.RelativePath, "..") || filepath.IsAbs(doc.RelativePath) {
			continue
		}
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

	// 2. Traverse Documents to extract nodes and edges
	for _, doc := range index.Documents {
		relPath := doc.RelativePath
		if strings.HasPrefix(relPath, "..") || filepath.IsAbs(relPath) {
			continue
		}
		if !filter.Allows(relPath, StageScip) {
			continue
		}
		docLang := normalizeLanguage(profile, doc.Language, relPath)
		fileLanguages[relPath] = docLang

		// Sort occurrences to compute precise function end_line
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

		// First pass: Create nodes
		scipToUsn := make(map[string]string)
		var documentNodes []*AstraMapNode
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
			endLine := startLine + 4 // Default estimate

			// Get next definition start line in the file as endLine
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

			// Get rich text information
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

			if docstring == "" && len(sourceLines) > 0 {
				docstring = findLeadingComments(sourceLines, startLine)
			}

			// Format QualifiedName
			qname := info.name
			parts := strings.Split(occ.Symbol, " ")
			if len(parts) > 0 {
				lastPart := parts[len(parts)-1]
				lastPart = strings.TrimSuffix(lastPart, ".")
				qname = strings.ReplaceAll(lastPart, "/", ".")
				qname = strings.ReplaceAll(qname, "#", ".")
				qname = strings.ReplaceAll(qname, ":", ".")
				qname = strings.ReplaceAll(qname, "`", "")
			}

			// Shorten long ID to ensure physical primary key stability
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
			normalizeSemanticNode(profile, docLang, node, occ.Symbol, sourceLines)
			nodes = append(nodes, node)
			documentNodes = append(documentNodes, node)
			fileNodeCounts[relPath]++
			scipToUsn[occ.Symbol] = usn
			globalScipToUsn[occ.Symbol] = usn
		}

		documentScopes := newCallableScopeIndex(documentNodes)

		// Second pass: Create edges (call edges)
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

			callerNode := documentScopes.Enclosing(occLine)
			if callerNode == nil {
				continue
			}
			callerUSN := callerNode.ID

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
	logInfo("ImportScipIndexToAstraMap: Writing index, please wait...")

	// 3. Perform database batch write (Transaction)
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Replace symmetrically by provenance, leaving recoverable Tree-sitter nodes untouched.
	_, _ = tx.Exec("DELETE FROM astramap_edges WHERE provenance = 'scip'")
	_, _ = tx.Exec("DELETE FROM astramap_nodes WHERE provenance = 'scip' AND kind != 'external'")

	// Batch insert Nodes
	nodeStmt, err := tx.Preparex(`
		INSERT INTO astramap_nodes (
			id, kind, name, qualified_name, file_path, language,
			start_line, end_line, start_column, end_column,
			signature, docstring, visibility, return_type, is_exported, provenance, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			kind=excluded.kind,
			name=excluded.name,
			qualified_name=excluded.qualified_name,
			start_line=excluded.start_line,
			end_line=excluded.end_line,
			signature=excluded.signature,
			docstring=excluded.docstring,
			provenance=excluded.provenance,
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
			n.Signature, n.Docstring, n.Visibility, n.ReturnType, n.IsExported, "scip", n.UpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("failed to insert node (%s): %w", n.ID, err)
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

	// Insert external: placeholder nodes (FK constraint requires edge targets to exist in nodes table)
	externalSeen := make(map[string]bool)
	for _, e := range edges {
		if strings.HasPrefix(e.Target, "external:") && !externalSeen[e.Target] {
			externalSeen[e.Target] = true
			name := e.Target[len("external:"):]
			_, _ = tx.Exec(`INSERT OR IGNORE INTO astramap_nodes
				(id, kind, name, qualified_name, file_path, language, is_exported, provenance, updated_at)
				VALUES (?, 'external', ?, ?, '', '', 0, 'scip', ?)`,
				e.Target, name, name, time.Now().Unix())
		}
	}

	// Batch insert Edges
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
			return fmt.Errorf("failed to insert edge (%s -> %s): %w", e.Source, e.Target, err)
		}
	}
	_, _ = tx.Exec(`
		DELETE FROM astramap_nodes
		WHERE kind = 'external'
		  AND provenance = 'scip'
		  AND id NOT IN (SELECT source FROM astramap_edges)
		  AND id NOT IN (SELECT target FROM astramap_edges)
	`)

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return err
	}

	// 4. Trigger heuristic edge resolution
	_ = ResolveGoInterfaces(db)
	_ = ResolveWebRoutes(db, projectRoot)

	if err := ResolveCrossFileCalls(db, projectRoot); err != nil {
		logError("ResolveCrossFileCalls failed: %v", err)
	}

	InvalidateOverviewCache()
	logInfo("ImportScipIndexesToAstraMap: Successfully imported %d nodes, %d call edges", len(nodes), len(edges))
	return nil
}

// ===== Watcher & Incremental Sync =====

// SyncFileAstraMap incrementally syncs a single file, filtering via hash dirty states, and provides transactional batch writes
func SyncFileAstraMap(db *sqlx.DB, projectRoot, filePath string) (bool, error) {
	profile := BuildProjectProfile(projectRoot, nil, StageTreeSitter)
	changed, err := syncFileAstraMapWithProfile(db, profile, filePath)
	if err != nil || !changed {
		return changed, err
	}
	relPath := filePath
	if filepath.IsAbs(relPath) {
		relPath, _ = filepath.Rel(projectRoot, relPath)
	}
	relPath = filepath.ToSlash(relPath)
	if err := ResolveCrossFileCallsForFiles(db, projectRoot, []string{relPath}); err != nil {
		return true, fmt.Errorf("resolve cross-file calls for %s: %w", relPath, err)
	}
	return true, nil
}

func syncFileAstraMapWithProfile(db *sqlx.DB, profile ProjectProfile, filePath string) (bool, error) {
	projectRoot := profile.ProjectRoot
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
		_, _ = db.Exec("DELETE FROM astramap_edges WHERE source IN (SELECT id FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%') OR target IN (SELECT id FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%')", relPath, relPath)
		_, _ = db.Exec("DELETE FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%'", relPath)
		_, _ = db.Exec("DELETE FROM astramap_files WHERE path = ?", relPath)
		InvalidateQueryHelperCacheForFile(relPath)
		return true, nil
	}

	mtimeNS := stat.ModTime().UnixNano()
	var existing fileIndexFingerprint
	if err := db.Get(&existing, "SELECT content_hash, size, modified_at_ns FROM astramap_files WHERE path = ?", relPath); err == nil {
		if existing.ModifiedAtNS > 0 && existing.Size == stat.Size() && existing.ModifiedAtNS == mtimeNS {
			return false, nil
		}
	}

	contentHash, err := hashFile(absPath)
	if err != nil {
		return false, err
	}
	if existing.ContentHash == contentHash {
		overlayChanged, overlayErr := ensureTreeSitterOverlay(db, profile, relPath, absPath)
		if overlayErr != nil {
			return false, overlayErr
		}
		if existing.ModifiedAtNS == 0 || existing.Size != stat.Size() || existing.ModifiedAtNS != mtimeNS {
			_, _ = db.Exec("UPDATE astramap_files SET size = ?, modified_at = ?, modified_at_ns = ?, indexed_at = ? WHERE path = ?",
				stat.Size(), stat.ModTime().Unix(), mtimeNS, time.Now().Unix(), relPath)
		}
		return overlayChanged, nil
	}

	nodes, edges, _, err := ParseFileIncrementalWithProfile(profile, relPath)
	if err != nil {
		return false, err
	}
	if err := reuseExistingIncrementalIDs(db, relPath, nodes, edges); err != nil {
		return false, err
	}

	tx, err := db.Beginx()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	// Find nodes about to be deleted to update incoming calls to external placeholders
	var deletedNodes []struct {
		ID       string `db:"id"`
		Name     string `db:"name"`
		Language string `db:"language"`
		Kind     string `db:"kind"`
	}
	_ = tx.Select(&deletedNodes, "SELECT id, name, language, kind FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%'", relPath)
	for _, dn := range deletedNodes {
		if dn.Kind == "function" || dn.Kind == "method" {
			prefix := languageIDPrefixForProfile(profile, dn.Language)
			extID := fmt.Sprintf("external:%s . . $ %s.", prefix, dn.Name)
			_, _ = tx.Exec("UPDATE astramap_edges SET target = ? WHERE target = ? AND provenance != 'heuristic'", extID, dn.ID)
		}
	}

	_, _ = tx.Exec("DELETE FROM astramap_edges WHERE source IN (SELECT id FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%') OR target IN (SELECT id FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%')", relPath, relPath)
	_, _ = tx.Exec("DELETE FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%'", relPath)

	nodeStmt, err := tx.Preparex(`
		INSERT INTO astramap_nodes (
			id, kind, name, qualified_name, file_path, language,
			start_line, end_line, start_column, end_column,
			signature, docstring, visibility, return_type, is_exported, provenance, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			kind=excluded.kind,
			name=excluded.name,
			qualified_name=excluded.qualified_name,
			start_line=excluded.start_line,
			end_line=excluded.end_line,
			signature=excluded.signature,
			docstring=excluded.docstring,
			provenance=excluded.provenance,
			updated_at=excluded.updated_at
	`)
	if err != nil {
		return false, err
	}
	defer nodeStmt.Close()

	for _, n := range nodes {
		_, err = nodeStmt.Exec(
			n.ID, n.Kind, n.Name, n.QualifiedName, n.FilePath, n.Language,
			n.StartLine, n.EndLine, n.StartColumn, n.EndColumn,
			n.Signature, n.Docstring, n.Visibility, n.ReturnType, n.IsExported, "tree-sitter", n.UpdatedAt,
		)
		if err != nil {
			return false, err
		}
	}

	externalSeen := make(map[string]bool)
	for _, e := range edges {
		if !strings.HasPrefix(e.Target, "external:") || externalSeen[e.Target] {
			continue
		}
		externalSeen[e.Target] = true
		name := externalSymbolName(e.Target)
		_, _ = tx.Exec(`INSERT OR IGNORE INTO astramap_nodes
			(id, kind, name, qualified_name, file_path, language, is_exported, provenance, updated_at)
			VALUES (?, 'external', ?, ?, '', '', 0, 'tree-sitter', ?)`,
			e.Target, name, name, time.Now().Unix())
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

	selection, _ := ResolveLanguageWithProfile(profile, absPath)
	language := selection.ID
	if len(nodes) > 0 {
		language = nodes[0].Language
	}
	if language == "" {
		language = "unknown"
	}
	_, _ = tx.Exec(`
			INSERT INTO astramap_files (path, content_hash, language, size, modified_at, modified_at_ns, indexed_at, node_count, errors)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(path) DO UPDATE SET
				content_hash=excluded.content_hash,
				language=excluded.language,
				size=excluded.size,
				modified_at=excluded.modified_at,
				modified_at_ns=excluded.modified_at_ns,
				indexed_at=excluded.indexed_at,
				node_count=excluded.node_count
		`, relPath, contentHash, language, stat.Size(), stat.ModTime().Unix(), mtimeNS, time.Now().Unix(), len(nodes), "")

	if err := tx.Commit(); err != nil {
		return false, err
	}

	// Bind existing external calls to the newly indexed functions/methods
	for _, n := range nodes {
		if n.Kind == "function" || n.Kind == "method" {
			prefix := languageIDPrefixForProfile(profile, n.Language)
			extID := fmt.Sprintf("external:%s . . $ %s.", prefix, n.Name)
			_, _ = db.Exec("UPDATE astramap_edges SET target = ? WHERE target = ? AND kind = 'calls'", n.ID, extID)
		}
	}

	InvalidateQueryHelperCacheForFile(relPath)
	return true, nil
}

func reuseExistingIncrementalIDs(db *sqlx.DB, relPath string, nodes []*AstraMapNode, edges []*AstraMapEdge) error {
	if len(nodes) == 0 {
		return nil
	}
	var existing []*AstraMapNode
	if err := db.Select(&existing, `
		SELECT *
		FROM astramap_nodes
		WHERE file_path = ?
		ORDER BY
			CASE
				WHEN id LIKE 'cxx . . $%' OR id LIKE 'scip:%' OR id LIKE 'go:%' THEN 0
				WHEN id NOT LIKE '%:%::%' THEN 1
				ELSE 2
			END,
			start_line,
			id
	`, relPath); err != nil {
		return err
	}
	if len(existing) == 0 {
		return reuseExistingExternalIDs(db, edges)
	}

	byKey := make(map[string]string, len(existing))
	for _, n := range existing {
		for _, key := range nodeMatchKeys(n) {
			if _, exists := byKey[key]; !exists {
				byKey[key] = n.ID
			}
		}
	}

	remap := make(map[string]string)
	for _, n := range nodes {
		for _, key := range nodeMatchKeys(n) {
			if id, exists := byKey[key]; exists {
				remap[n.ID] = id
				n.ID = id
				break
			}
		}
	}
	for _, e := range edges {
		if id, exists := remap[e.Source]; exists {
			e.Source = id
		}
		if id, exists := remap[e.Target]; exists {
			e.Target = id
		}
	}
	return reuseExistingExternalIDs(db, edges)
}

func nodeMatchKeys(n *AstraMapNode) []string {
	if n == nil {
		return nil
	}
	kind := n.Kind
	if kind == "method" {
		kind = "function"
	}
	qname := strings.TrimSpace(n.QualifiedName)
	name := strings.TrimSpace(n.Name)
	keys := []string{}
	if qname != "" {
		keys = append(keys, kind+"\x00"+qname)
	}
	if name != "" {
		keys = append(keys, kind+"\x00"+name)
	}
	return keys
}

func reuseExistingExternalIDs(db *sqlx.DB, edges []*AstraMapEdge) error {
	if len(edges) == 0 {
		return nil
	}
	names := make(map[string]string)
	for _, e := range edges {
		if strings.HasPrefix(e.Target, "external:") {
			name := externalSymbolName(e.Target)
			if name != "" {
				names[name] = e.Target
			}
		}
	}
	if len(names) == 0 {
		return nil
	}

	for name, currentID := range names {
		var existingID string
		err := db.Get(&existingID, `
			SELECT id
			FROM astramap_nodes
			WHERE (kind = 'external' OR id LIKE 'external:%')
			  AND (name = ? OR qualified_name = ? OR id LIKE ?)
			ORDER BY
				CASE WHEN id LIKE '%(%).%' THEN 0 ELSE 1 END,
				id
			LIMIT 1
		`, name, name, "%$ "+name+"%")
		if err != nil || existingID == "" || existingID == currentID {
			continue
		}
		for _, e := range edges {
			if e.Target == currentID {
				e.Target = existingID
			}
		}
	}
	return nil
}

func ensureTreeSitterOverlay(db *sqlx.DB, profile ProjectProfile, relPath, absPath string) (bool, error) {
	selection, _ := ResolveLanguageWithProfile(profile, absPath)
	lang := selection.ID
	prefix := languageIDPrefixForProfile(profile, lang)
	if prefix == "unknown" {
		return false, nil
	}
	var existingTS int
	if err := db.Get(&existingTS, "SELECT COUNT(*) FROM astramap_nodes WHERE file_path = ? AND id LIKE ?", relPath, prefix+":"+relPath+"::%"); err != nil {
		return false, err
	}
	if existingTS > 0 {
		return false, nil
	}

	nodes, _, _, err := ParseFileIncrementalWithProfile(profile, relPath)
	if err != nil {
		return false, err
	}
	if len(nodes) == 0 {
		return false, nil
	}

	var existingNames []string
	if err := db.Select(&existingNames, "SELECT DISTINCT name FROM astramap_nodes WHERE file_path = ?", relPath); err != nil {
		return false, err
	}
	conflicts := make(map[string]bool, len(existingNames))
	for _, name := range existingNames {
		conflicts[name] = true
	}

	tx, err := db.Beginx()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	nodeStmt, err := tx.Preparex(`
		INSERT OR IGNORE INTO astramap_nodes (
			id, kind, name, qualified_name, file_path, language,
			start_line, end_line, start_column, end_column,
			signature, docstring, visibility, return_type, is_exported, provenance, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return false, err
	}
	defer nodeStmt.Close()

	inserted := 0
	for _, n := range nodes {
		if conflicts[n.Name] {
			continue
		}
		res, err := nodeStmt.Exec(
			n.ID, n.Kind, n.Name, n.QualifiedName, n.FilePath, n.Language,
			n.StartLine, n.EndLine, n.StartColumn, n.EndColumn,
			n.Signature, n.Docstring, n.Visibility, n.ReturnType, n.IsExported, "tree-sitter", n.UpdatedAt,
		)
		if err != nil {
			return false, err
		}
		if rows, rowErr := res.RowsAffected(); rowErr == nil && rows > 0 {
			inserted++
		}
	}
	if inserted == 0 {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

type fileIndexFingerprint struct {
	ContentHash  string `db:"content_hash"`
	Size         int64  `db:"size"`
	ModifiedAtNS int64  `db:"modified_at_ns"`
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

// SyncAllFilesAstraMap scans the project directory, incrementally syncing all dirty files
type SyncAllFilesResult struct {
	Scanned       int
	Updated       int
	UpdatedFiles  []string
	Pruned        bool
	PrunedDeleted int
}

func SyncAllFilesAstraMap(db *sqlx.DB, projectRoot string, langFilter ...string) error {
	_, err := SyncAllFilesAstraMapResult(db, projectRoot, langFilter...)
	return err
}

func SyncAllFilesAstraMapResult(db *sqlx.DB, projectRoot string, langFilter ...string) (SyncAllFilesResult, error) {
	logInfo("SyncAllFilesAstraMap: Incremental scan %s", projectRoot)
	result := SyncAllFilesResult{}
	filter, err := LoadIndexFilter(projectRoot)
	if err != nil {
		return result, fmt.Errorf("failed to read AstraMap config: %w", err)
	}
	profile := BuildProjectProfile(projectRoot, filter, StageTreeSitter)
	pruned, err := PruneExcludedFiles(db, filter)
	if err != nil {
		return result, err
	}
	result.Pruned = pruned

	prunedDeleted, err := PruneDeletedFiles(db, projectRoot)
	if err != nil {
		logError("PruneDeletedFiles failed: %v", err)
	} else if prunedDeleted > 0 {
		result.PrunedDeleted = prunedDeleted
		logInfo("PruneDeletedFiles: Cleaned up residual records for %d deleted files", prunedDeleted)
	}

	languages := languageFilterSet(profile, langFilter)

	scanned := 0
	updated := 0
	updatedFiles := make([]string, 0)
	err = filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		relPath, _ := filepath.Rel(projectRoot, path)
		if info.IsDir() {
			if !filter.AllowsDir(relPath, StageTreeSitter) {
				return filepath.SkipDir
			}
			return nil
		}

		if !IsLanguageFile(profile, path, languages) {
			return nil
		}
		if !filter.Allows(relPath, StageTreeSitter) {
			return nil
		}

		scanned++

		changed, syncErr := syncFileAstraMapWithProfile(db, profile, path)
		if syncErr != nil {
			return syncErr
		}
		if changed {
			updated++
			updatedFiles = append(updatedFiles, relPath)
		}
		return nil
	})
	result.Scanned = scanned
	result.Updated = updated
	result.UpdatedFiles = updatedFiles
	if err != nil {
		return result, fmt.Errorf("sync files: %w", err)
	}

	logInfo("SyncAllFilesAstraMap: Scan complete, %d files, %d updated, resolving call relationships", scanned, updated)
	if updated == 0 && !pruned && prunedDeleted == 0 {
		logInfo("SyncAllFilesAstraMap: No file changes, refreshing heuristic call relationships")
	}

	// Trigger cross-file call resolution
	_ = ResolveGoInterfaces(db)
	_ = ResolveWebRoutesForFiles(db, projectRoot, result.UpdatedFiles)
	if err := ResolveCrossFileCallsForFiles(db, projectRoot, updatedFiles); err != nil {
		return result, fmt.Errorf("resolve cross-file calls: %w", err)
	}
	InvalidateOverviewCache()

	logInfo("SyncAllFilesAstraMap: Ready, %d files, %d updated", scanned, updated)
	return result, err
}

func SyncChangedFilesAstraMapResult(db *sqlx.DB, projectRoot string, paths []string, langFilter ...string) (SyncAllFilesResult, error) {
	result := SyncAllFilesResult{}
	if len(paths) == 0 {
		return result, nil
	}

	filter, err := LoadIndexFilter(projectRoot)
	if err != nil {
		return result, fmt.Errorf("failed to read AstraMap config: %w", err)
	}
	profile := BuildProjectProfile(projectRoot, filter, StageTreeSitter)

	languages := languageFilterSet(profile, langFilter)
	seen := make(map[string]bool)
	for _, rawPath := range paths {
		if rawPath == "" {
			continue
		}
		absPath := rawPath
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(projectRoot, rawPath)
		}
		relPath, relErr := filepath.Rel(projectRoot, absPath)
		if relErr != nil {
			continue
		}
		relPath = filepath.ToSlash(relPath)
		if seen[relPath] {
			continue
		}
		seen[relPath] = true

		info, statErr := os.Stat(absPath)
		if statErr == nil && info.IsDir() {
			if !filter.AllowsDir(relPath, StageTreeSitter) {
				continue
			}
			result.PrunedDeleted += pruneDeletedUnderPath(db, projectRoot, relPath)
			continue
		}

		if statErr != nil {
			result.PrunedDeleted += pruneDeletedUnderPath(db, projectRoot, relPath)
			continue
		}
		if !IsLanguageFile(profile, absPath, languages) {
			continue
		}

		if statErr == nil && !filter.Allows(relPath, StageTreeSitter) {
			if removed, removeErr := removeIndexedFile(db, relPath); removeErr != nil {
				return result, removeErr
			} else if removed {
				result.Pruned = true
			}
			continue
		}

		result.Scanned++
		changed, err := syncFileAstraMapWithProfile(db, profile, absPath)
		if err != nil {
			return result, err
		}
		if changed {
			result.Updated++
			result.UpdatedFiles = append(result.UpdatedFiles, relPath)
		}
	}

	if result.Updated == 0 && !result.Pruned && result.PrunedDeleted == 0 {
		return result, nil
	}

	_ = ResolveGoInterfaces(db)
	_ = ResolveWebRoutesForFiles(db, projectRoot, result.UpdatedFiles)
	if err := ResolveCrossFileCallsForFiles(db, projectRoot, result.UpdatedFiles); err != nil {
		return result, fmt.Errorf("resolve cross-file calls: %w", err)
	}
	InvalidateOverviewCache()
	return result, nil
}

func languageFilterSet(profile ProjectProfile, filters []string) map[string]bool {
	result := make(map[string]bool, len(filters))
	registry := profile.registry
	if registry == nil {
		registry = languageRegistryForProject(profile.ProjectRoot)
	}
	for _, filter := range filters {
		if spec := registry.specForID(filter); spec != nil {
			result[spec.ID] = true
		}
	}
	return result
}

func pruneDeletedUnderPath(db *sqlx.DB, projectRoot, relPath string) int {
	var files []string
	if err := db.Select(&files, "SELECT path FROM astramap_files WHERE path = ? OR path LIKE ?", relPath, strings.TrimRight(relPath, "/")+"/%"); err != nil {
		return 0
	}
	pruned := 0
	for _, filePath := range files {
		if _, err := os.Stat(filepath.Join(projectRoot, filePath)); err == nil {
			continue
		}
		if removed, err := removeIndexedFile(db, filePath); err == nil && removed {
			pruned++
		}
	}
	return pruned
}

func removeIndexedFile(db *sqlx.DB, relPath string) (bool, error) {
	res, err := db.Exec("DELETE FROM astramap_edges WHERE source IN (SELECT id FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%') OR target IN (SELECT id FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%')", relPath, relPath)
	if err != nil {
		return false, err
	}
	affected := int64(0)
	if res != nil {
		affected, _ = res.RowsAffected()
	}
	res, err = db.Exec("DELETE FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%'", relPath)
	if err != nil {
		return false, err
	}
	if res != nil {
		if rows, rowErr := res.RowsAffected(); rowErr == nil {
			affected += rows
		}
	}
	res, err = db.Exec("DELETE FROM astramap_files WHERE path = ?", relPath)
	if err != nil {
		return false, err
	}
	if res != nil {
		if rows, rowErr := res.RowsAffected(); rowErr == nil {
			affected += rows
		}
	}
	InvalidateQueryHelperCacheForFile(relPath)
	return affected > 0, nil
}

func PruneExcludedFiles(db *sqlx.DB, filter *IndexFilter) (bool, error) {
	var files []string
	if err := db.Select(&files, "SELECT path FROM astramap_files"); err != nil {
		return false, fmt.Errorf("query indexed files failed: %w", err)
	}

	tx, err := db.Beginx()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	pruned := false
	for _, filePath := range files {
		if filter.Allows(filePath, StageTreeSitter) {
			continue
		}

		if _, err := tx.Exec(
			"DELETE FROM astramap_edges WHERE source IN (SELECT id FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%') OR target IN (SELECT id FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%')",
			filePath, filePath,
		); err != nil {
			return false, fmt.Errorf("delete edges for excluded file %s failed: %w", filePath, err)
		}

		if _, err := tx.Exec("DELETE FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%'", filePath); err != nil {
			return false, fmt.Errorf("delete nodes for excluded file %s failed: %w", filePath, err)
		}
		if _, err := tx.Exec("DELETE FROM astramap_files WHERE path = ?", filePath); err != nil {
			return false, fmt.Errorf("delete indexed file %s failed: %w", filePath, err)
		}
		pruned = true
	}

	return pruned, tx.Commit()
}

// PruneDeletedFiles removes DB records for files that no longer exist on disk.
func PruneDeletedFiles(db *sqlx.DB, projectRoot string) (int, error) {
	var files []string
	if err := db.Select(&files, "SELECT path FROM astramap_files"); err != nil {
		return 0, fmt.Errorf("query indexed files failed: %w", err)
	}

	tx, err := db.Beginx()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	pruned := 0
	for _, filePath := range files {
		absPath := filepath.Join(projectRoot, filePath)
		if _, err := os.Stat(absPath); err != nil && os.IsNotExist(err) {
			if _, err := tx.Exec(
				"DELETE FROM astramap_edges WHERE source IN (SELECT id FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%') OR target IN (SELECT id FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%')",
				filePath, filePath,
			); err != nil {
				return pruned, fmt.Errorf("delete edges for deleted file %s failed: %w", filePath, err)
			}
			if _, err := tx.Exec("DELETE FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%'", filePath); err != nil {
				return pruned, fmt.Errorf("delete nodes for deleted file %s failed: %w", filePath, err)
			}
			if _, err := tx.Exec("DELETE FROM astramap_files WHERE path = ?", filePath); err != nil {
				return pruned, fmt.Errorf("delete file record %s failed: %w", filePath, err)
			}
			pruned++
		}
	}

	if pruned > 0 {
		return pruned, tx.Commit()
	}
	return 0, nil
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

func EffectiveLanguageCapabilities(db *sqlx.DB) []CapabilityState {
	return EffectiveLanguageCapabilitiesForProject(db, "")
}

func EffectiveLanguageCapabilitiesForProject(db *sqlx.DB, projectRoot string) []CapabilityState {
	registry := languageRegistryForProject(projectRoot)
	ids := make([]string, 0, len(registry.languages))
	for id := range registry.languages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]CapabilityState, 0, len(ids))
	for _, id := range ids {
		spec := registry.languages[id]
		state := CapabilityState{
			Language: spec.ID, DeclaredLevel: capabilityLevel(spec.Capabilities),
			EffectiveLevel: capabilityLevel(syntaxCapabilities), GrammarStatus: "ready",
			ProviderStatus: "not-applicable",
		}
		if spec.Semantic != nil {
			state.ProviderStatus = "not-configured"
			_ = db.Get(&state.Artifacts, "SELECT CASE WHEN EXISTS (SELECT 1 FROM astramap_nodes WHERE language = ? AND provenance = 'scip') THEN 1 ELSE 0 END", spec.ID)
			if state.Artifacts > 0 {
				state.EffectiveLevel = state.DeclaredLevel
				state.ProviderStatus = "imported"
			}
		}
		result = append(result, state)
	}
	return result
}

// ===== Heuristic Resolvers =====

// ResolveGoInterfaces Go implicit interface resolver: establishes implements edges when struct method set fully covers interface method set
func ResolveGoInterfaces(db *sqlx.DB) error {
	type idName struct {
		ID   string `db:"id"`
		Name string `db:"name"`
	}

	// 1. Query all interface and struct nodes
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

	// 2. Build contains edge query: get the set of method names associated with interface/struct via contains edges
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

	// Group method names by source
	ownerMethods := make(map[string]map[string]struct{})
	for _, row := range containsEdges {
		if ownerMethods[row.Source] == nil {
			ownerMethods[row.Source] = make(map[string]struct{})
		}
		ownerMethods[row.Source][row.MethodName] = struct{}{}
	}

	// 3. Transaction: clean up old heuristic implements edges and rebuild
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

	// 4. For each interface, check if each struct fully covers its method set
	for _, iface := range interfaces {
		ifaceMethods := ownerMethods[iface.ID]
		if len(ifaceMethods) == 0 {
			continue // Empty interfaces do not establish implements edges
		}

		for _, st := range structs {
			structMethods := ownerMethods[st.ID]
			if len(structMethods) < len(ifaceMethods) {
				continue
			}

			// Check full coverage
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

// ResolveWebRoutes Web route reflection handler: scans route bindings and establishes edges from routes to controller Handlers
func ResolveWebRoutes(db *sqlx.DB, projectRoot string) error {
	return resolveWebRoutes(db, projectRoot, nil)
}

func ResolveWebRoutesForFiles(db *sqlx.DB, projectRoot string, files []string) error {
	if len(files) == 0 {
		return nil
	}
	return resolveWebRoutes(db, projectRoot, files)
}

func resolveWebRoutes(db *sqlx.DB, projectRoot string, files []string) error {
	var handlers []struct {
		ID            string `db:"id"`
		Name          string `db:"name"`
		FilePath      string `db:"file_path"`
		QualifiedName string `db:"qualified_name"`
	}

	if len(files) == 0 {
		if err := db.Select(&handlers, "SELECT id, name, file_path, qualified_name FROM astramap_nodes WHERE kind IN ('function', 'method')"); err != nil {
			return err
		}
	} else {
		seen := make(map[string]bool, len(files))
		var relFiles []string
		for _, filePath := range files {
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
			relFiles = append(relFiles, relPath)
		}
		if len(relFiles) == 0 {
			return nil
		}
		query, args, err := sqlx.In("SELECT id, name, file_path, qualified_name FROM astramap_nodes WHERE kind IN ('function', 'method') AND file_path IN (?)", relFiles)
		if err != nil {
			return err
		}
		query = db.Rebind(query)
		if err := db.Select(&handlers, query, args...); err != nil {
			return err
		}
	}

	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if len(files) == 0 {
		_, _ = tx.Exec("DELETE FROM astramap_edges WHERE provenance = 'heuristic' AND kind = 'route'")
		_, _ = tx.Exec("DELETE FROM astramap_nodes WHERE kind = 'route'")
	} else {
		for _, filePath := range files {
			relPath := filePath
			if filepath.IsAbs(relPath) {
				if rel, relErr := filepath.Rel(projectRoot, relPath); relErr == nil {
					relPath = rel
				}
			}
			relPath = filepath.ToSlash(relPath)
			_, _ = tx.Exec("DELETE FROM astramap_edges WHERE provenance = 'heuristic' AND kind = 'route' AND source IN (SELECT id FROM astramap_nodes WHERE kind = 'route' AND file_path = ?)", relPath)
			_, _ = tx.Exec("DELETE FROM astramap_nodes WHERE kind = 'route' AND file_path = ?", relPath)
		}
	}

	routeRe := regexp.MustCompile(`(?:\.GET|\.POST|\.PUT|\.DELETE|@app\.[a-z]+)\(\s*["']([^"']+)["']\s*,\s*([a-zA-Z0-9_]+)`)

	handlersByFile := make(map[string]map[string][]string)
	for _, handler := range handlers {
		if handlersByFile[handler.FilePath] == nil {
			handlersByFile[handler.FilePath] = make(map[string][]string)
		}
		handlersByFile[handler.FilePath][handler.Name] = append(handlersByFile[handler.FilePath][handler.Name], handler.ID)
	}

	for filePath, handlersByName := range handlersByFile {
		absPath := filepath.Join(projectRoot, filePath)
		content, err := os.ReadFile(absPath)
		if err != nil {
			continue
		}

		matches := routeRe.FindAllStringSubmatch(string(content), -1)
		for _, m := range matches {
			routePath := m[1]
			handlerName := m[2]

			for _, handlerID := range handlersByName[handlerName] {
				routeUSN := fmt.Sprintf("route:%s", routePath)
				_, _ = tx.Exec(`
					INSERT INTO astramap_nodes (id, kind, name, qualified_name, file_path, language, start_line, end_line, provenance, updated_at)
					VALUES (?, 'route', ?, ?, ?, 'http', 0, 0, 'heuristic', ?)
					ON CONFLICT(id) DO NOTHING
				`, routeUSN, routePath, routePath, filePath, time.Now().Unix())

				_, _ = tx.Exec(`
					INSERT OR IGNORE INTO astramap_edges (source, target, kind, provenance, metadata)
					VALUES (?, ?, 'calls', 'heuristic', '')
				`, routeUSN, handlerID)
			}
		}
	}

	return tx.Commit()
}

// ===== SCIP Helper Functions =====

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
		symType = refineCSymbolType(sym, name, symType)
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
		symType = refineCSymbolType(sym, lastDesc.Name, symType)
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
	case scip.SymbolInformation_Class:
		return "class", false
	case scip.SymbolInformation_Enum:
		return "enum", false
	case scip.SymbolInformation_Struct:
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
		if strings.HasPrefix(sym, "scip-go") {
			return "struct", false
		}
		return "class", false
	case scip.Descriptor_Macro:
		return "macro", false
	case scip.Descriptor_Parameter, scip.Descriptor_TypeParameter:
		return "variable", false
	default:
		return "variable", false
	}
}

func refineCSymbolType(sym, name, symType string) string {
	if !strings.HasPrefix(sym, "cxx ") && !strings.HasPrefix(sym, "c ") {
		return symType
	}
	if symType != "class" && symType != "type" {
		return symType
	}
	lowerName := strings.ToLower(strings.TrimSuffix(name, "#"))
	if strings.HasSuffix(lowerName, "_e") || strings.Contains(lowerName, "enum") {
		return "enum"
	}
	return "struct"
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

func normalizeLanguage(profile ProjectProfile, lang, filePath string) string {
	if selection, ok := ResolveLanguageWithProfile(profile, filePath); ok {
		return selection.ID
	}
	registry := profile.registry
	if registry == nil {
		registry = languageRegistryForProject(profile.ProjectRoot)
	}
	if spec := registry.specForID(lang); spec != nil {
		return spec.ID
	}
	return "unknown"
}

func normalizeGoScipNode(node *AstraMapNode, _ string, _ []string) {
	if node == nil || node.Name == "" {
		return
	}
	r, _ := utf8.DecodeRuneInString(node.Name)
	if r != utf8.RuneError && unicode.IsUpper(r) {
		node.IsExported = 1
	}
}

func normalizeCScipNode(node *AstraMapNode, _ string, sourceLines []string) {
	if node == nil {
		return
	}
	if node.Signature == "" && node.StartLine > 0 && len(sourceLines) >= node.StartLine {
		node.Signature = strings.TrimSpace(sourceLines[node.StartLine-1])
	}
	node.Kind = normalizeCDeclarationKind(node.Kind, node.Signature)
}

func normalizeCDeclarationKind(kind, signature string) string {
	signature = strings.TrimSpace(signature)
	switch {
	case strings.HasPrefix(signature, "typedef "):
		return "type"
	case strings.HasPrefix(signature, "enum "):
		return "enum"
	case strings.HasPrefix(signature, "struct "), strings.HasPrefix(signature, "union "):
		return "struct"
	default:
		return kind
	}
}

func findLeadingComments(lines []string, startLine int) string {
	if len(lines) == 0 || startLine <= 1 || startLine > len(lines) {
		return ""
	}
	var commentLines []string
	inBlock := false
	emptyLineCount := 0
	for i := startLine - 2; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			if inBlock {
				commentLines = append([]string{line}, commentLines...)
				continue
			}
			emptyLineCount++
			if emptyLineCount > 2 { // Allow at most 2 empty lines
				break
			}
			continue
		}
		if !inBlock && !strings.HasPrefix(line, "//") && !strings.HasSuffix(line, "*/") && !strings.HasPrefix(line, "/*") {
			break
		}

		if strings.HasPrefix(line, "//") {
			if inBlock {
				break
			}
			commentLines = append([]string{strings.TrimPrefix(line, "//")}, commentLines...)
			emptyLineCount = 0
			continue
		}
		if strings.HasSuffix(line, "*/") {
			inBlock = true
			lineContent := strings.TrimSuffix(line, "*/")
			if strings.HasPrefix(lineContent, "/*") {
				inBlock = false
				lineContent = strings.TrimPrefix(lineContent, "/*")
				commentLines = append([]string{lineContent}, commentLines...)
				break
			}
			commentLines = append([]string{lineContent}, commentLines...)
			emptyLineCount = 0
			continue
		}
		if strings.HasPrefix(line, "/*") {
			if !inBlock {
				break
			}
			inBlock = false
			lineContent := strings.TrimPrefix(line, "/*")
			commentLines = append([]string{lineContent}, commentLines...)
			break
		}
		if inBlock {
			lineContent := line
			if strings.HasPrefix(line, "*") {
				lineContent = strings.TrimPrefix(line, "*")
			}
			commentLines = append([]string{lineContent}, commentLines...)
			emptyLineCount = 0
			continue
		}
		break
	}
	var cleaned []string
	for _, l := range commentLines {
		t := strings.TrimSpace(l)
		cleaned = append(cleaned, t)
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}
