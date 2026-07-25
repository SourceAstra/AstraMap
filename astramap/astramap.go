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

func logWarn(format string, v ...interface{}) {
	fmt.Fprintf(os.Stderr, "[WARN] "+format+"\n", v...)
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
	SemanticHash string `db:"semantic_hash" json:"semanticHash"`
	SyntaxHash   string `db:"syntax_hash" json:"syntaxHash"`
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

// ScipIndexProjectLanguages resolves provider-level SCIP language labels to
// AstraMap language IDs from each document path. This is required for providers
// such as scip-clang, which labels both C and C++ documents as "cpp".
func ScipIndexProjectLanguages(scipPath, projectRoot string) ([]string, error) {
	index, err := readScipIndexFile(scipPath)
	if err != nil {
		return nil, err
	}
	filter, err := LoadIndexFilter(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("load project filter: %w", err)
	}
	profile := BuildProjectProfile(projectRoot, filter, StageScip)
	languages := make(map[string]bool)
	for _, document := range index.Documents {
		if !filter.Allows(document.RelativePath, StageScip) {
			continue
		}
		language := normalizeLanguage(profile, document.Language, document.RelativePath)
		if language == "unknown" {
			return nil, fmt.Errorf("SCIP document %s declares an unsupported language %q", document.RelativePath, document.Language)
		}
		languages[language] = true
	}
	result := make([]string, 0, len(languages))
	for language := range languages {
		result = append(result, language)
	}
	sort.Strings(result)
	return result, nil
}

func readScipIndexFile(scipPath string) (*scip.Index, error) {
	data, err := os.ReadFile(scipPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read SCIP index file: %w", err)
	}
	var index scip.Index
	if err := proto.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("SCIP Protobuf deserialization failed: %w", err)
	}
	if len(index.Documents) == 0 {
		return nil, fmt.Errorf("SCIP index contains no documents")
	}
	return &index, nil
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
		if docLang == "unknown" {
			return fmt.Errorf("SCIP document %s declares an unsupported language %q", relPath, doc.Language)
		}
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

	// A new semantic baseline invalidates every prior Syntax Overlay. The caller
	// may rebuild overlays only after this transaction commits.
	_, _ = tx.Exec("DELETE FROM astramap_edges WHERE provenance = 'syntax-package'")
	_, _ = tx.Exec("DELETE FROM astramap_nodes WHERE provenance = 'syntax-package'")
	_, _ = tx.Exec("UPDATE astramap_files SET semantic_hash = '', syntax_hash = ''")

	// Replace the semantic layer symmetrically by provenance.
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
		INSERT INTO astramap_files (path, content_hash, semantic_hash, syntax_hash, language, size, modified_at, modified_at_ns, indexed_at, node_count, errors)
		VALUES (?, ?, ?, '', ?, ?, ?, 0, ?, ?, '')
		ON CONFLICT(path) DO UPDATE SET
			content_hash=excluded.content_hash,
			semantic_hash=excluded.semantic_hash,
			syntax_hash='',
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
		_, _ = fileStmt.Exec(relPath, contentHash, contentHash, lang, size, modifiedAt, now, fileNodeCounts[relPath])
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
	langFiles := make(map[string]int)
	for _, lang := range fileLanguages {
		langFiles[lang]++
	}
	logInfo("ImportScipIndexesToAstraMap: Imported %d files, %d nodes, %d call edges", len(fileLanguages), len(nodes), len(edges))
	logInfo("ImportScipIndexesToAstraMap:   by language: %s", formatLanguageCounts(langFiles))
	return nil
}

// formatLanguageCounts renders a language->count map as a stable, sorted "lang=n" list.
func formatLanguageCounts(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, counts[k]))
	}
	return strings.Join(parts, ", ")
}

// ===== Watcher & Incremental Sync =====

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
	relPath = filepath.ToSlash(relPath)

	stat, err := os.Stat(absPath)
	if err != nil {
		return removeFileAstraMapRecords(db, relPath)
	}
	selection, supported := ResolveLanguageWithProfile(profile, absPath)
	if !supported || selection.Module == nil {
		return false, nil
	}

	mtimeNS := stat.ModTime().UnixNano()
	var existing fileIndexFingerprint
	hasExisting := db.Get(&existing, "SELECT content_hash, semantic_hash, syntax_hash, size, modified_at_ns FROM astramap_files WHERE path = ?", relPath) == nil
	if hasExisting && existing.SyntaxHash != "" && existing.ModifiedAtNS > 0 && existing.Size == stat.Size() && existing.ModifiedAtNS == mtimeNS {
		return false, nil
	}

	contentHash, err := hashFile(absPath)
	if err != nil {
		return false, err
	}
	if hasExisting && existing.SyntaxHash == contentHash {
		_, _ = db.Exec("UPDATE astramap_files SET size = ?, modified_at = ?, modified_at_ns = ?, indexed_at = ? WHERE path = ?",
			stat.Size(), stat.ModTime().Unix(), mtimeNS, time.Now().Unix(), relPath)
		return false, nil
	}
	return SyncDirtySyntaxOverlayWithProfile(db, profile, absPath)
}

// SyncDirtySyntaxOverlayWithProfile refreshes one file's realtime syntax layer
// and invalidates SCIP facts when their source hash no longer matches.
func SyncDirtySyntaxOverlayWithProfile(db *sqlx.DB, profile ProjectProfile, filePath string) (bool, error) {
	projectRoot := profile.ProjectRoot
	absPath := filePath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(projectRoot, filePath)
	}

	relPath, err := filepath.Rel(projectRoot, absPath)
	if err != nil {
		relPath = filePath
	}
	relPath = filepath.ToSlash(relPath)

	stat, err := os.Stat(absPath)
	if err != nil {
		return removeFileAstraMapRecords(db, relPath)
	}

	selection, supported := ResolveLanguageWithProfile(profile, absPath)
	if !supported || selection.Module == nil {
		return false, nil
	}

	nodes, edges, contentHash, err := ParseFileIncrementalWithProfile(profile, relPath)
	if err != nil {
		return false, err
	}
	var semanticHash string
	hasSemantic := db.Get(&semanticHash, "SELECT semantic_hash FROM astramap_files WHERE path = ?", relPath) == nil
	invalidateSemantic := hasSemantic && semanticHash != "" && semanticHash != contentHash

	tx, err := db.Beginx()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	if err := replaceSyntaxOverlayTx(tx, relPath, contentHash, stat, selection.ID, nodes, edges, invalidateSemantic); err != nil {
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}

	InvalidateQueryHelperCacheForFile(relPath)
	InvalidateOverviewCache()
	return true, nil
}

func removeFileAstraMapRecords(db *sqlx.DB, relPath string) (bool, error) {
	tx, err := db.Beginx()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		"DELETE FROM astramap_edges WHERE source IN (SELECT id FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%') OR target IN (SELECT id FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%') OR source = ? OR target = ?",
		relPath, relPath, "file:"+relPath, "file:"+relPath,
	); err != nil {
		return false, err
	}
	if _, err := tx.Exec("DELETE FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%'", relPath); err != nil {
		return false, err
	}
	if _, err := tx.Exec("DELETE FROM astramap_files WHERE path = ?", relPath); err != nil {
		return false, err
	}
	if err := cleanupOrphanExternalNodesTx(tx); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}

	InvalidateQueryHelperCacheForFile(relPath)
	InvalidateOverviewCache()
	return true, nil
}

func replaceSyntaxOverlayTx(tx *sqlx.Tx, relPath, contentHash string, stat os.FileInfo, language string, nodes []*AstraMapNode, edges []*AstraMapEdge, invalidateSemantic bool) error {
	fileNodeID := "file:" + relPath
	if invalidateSemantic {
		if _, err := tx.Exec(
			"DELETE FROM astramap_edges WHERE source IN (SELECT id FROM astramap_nodes WHERE file_path = ? AND provenance = 'scip') OR target IN (SELECT id FROM astramap_nodes WHERE file_path = ? AND provenance = 'scip')",
			relPath, relPath,
		); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM astramap_nodes WHERE file_path = ? AND provenance = 'scip'", relPath); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(
		"DELETE FROM astramap_edges WHERE provenance = 'syntax-package' AND (source = ? OR target = ? OR source IN (SELECT id FROM astramap_nodes WHERE file_path = ?) OR target IN (SELECT id FROM astramap_nodes WHERE file_path = ?))",
		fileNodeID, fileNodeID, relPath, relPath,
	); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM astramap_nodes WHERE provenance = 'syntax-package' AND file_path = ?", relPath); err != nil {
		return err
	}

	type semanticNode struct {
		ID        string `db:"id"`
		Kind      string `db:"kind"`
		Name      string `db:"name"`
		StartLine int    `db:"start_line"`
	}
	var semanticNodes []semanticNode
	if err := tx.Select(&semanticNodes, "SELECT id, kind, name, start_line FROM astramap_nodes WHERE file_path = ? AND provenance = 'scip'", relPath); err != nil {
		return err
	}
	semanticByLocation := make(map[string][]semanticNode, len(semanticNodes))
	for _, node := range semanticNodes {
		key := fmt.Sprintf("%s\x00%s\x00%d", node.Kind, node.Name, node.StartLine)
		semanticByLocation[key] = append(semanticByLocation[key], node)
	}
	remappedIDs := make(map[string]string)

	nodeStmt, err := tx.Preparex(`
		INSERT OR IGNORE INTO astramap_nodes (
			id, kind, name, qualified_name, file_path, language,
			start_line, end_line, start_column, end_column,
			signature, docstring, visibility, return_type, is_exported, provenance, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer nodeStmt.Close()

	for _, n := range nodes {
		key := fmt.Sprintf("%s\x00%s\x00%d", n.Kind, n.Name, n.StartLine)
		if matches := semanticByLocation[key]; len(matches) == 1 {
			remappedIDs[n.ID] = matches[0].ID
			if _, err := tx.Exec(`
				UPDATE astramap_nodes
				SET signature = CASE WHEN signature = '' THEN ? ELSE signature END,
					docstring = CASE WHEN docstring = '' THEN ? ELSE docstring END,
					end_line = CASE WHEN end_line < ? THEN ? ELSE end_line END
				WHERE id = ?`, n.Signature, n.Docstring, n.EndLine, n.EndLine, matches[0].ID); err != nil {
				return err
			}
			continue
		}
		if _, err := nodeStmt.Exec(
			n.ID, n.Kind, n.Name, n.QualifiedName, n.FilePath, n.Language,
			n.StartLine, n.EndLine, n.StartColumn, n.EndColumn,
			n.Signature, n.Docstring, n.Visibility, n.ReturnType, n.IsExported, "syntax-package", n.UpdatedAt,
		); err != nil {
			return err
		}
	}

	edgeStmt, err := tx.Preparex(`
		INSERT OR IGNORE INTO astramap_edges (source, target, kind, provenance, line, col, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer edgeStmt.Close()

	for _, e := range edges {
		source, target := e.Source, e.Target
		if mapped := remappedIDs[source]; mapped != "" {
			source = mapped
		}
		if mapped := remappedIDs[target]; mapped != "" {
			target = mapped
		}
		if _, err := edgeStmt.Exec(source, target, e.Kind, e.Provenance, e.Line, e.Col, e.Metadata); err != nil {
			return err
		}
	}
	if err := cleanupOrphanExternalNodesTx(tx); err != nil {
		return err
	}

	fileStmt, err := tx.Preparex(`
		INSERT INTO astramap_files (path, content_hash, semantic_hash, syntax_hash, language, size, modified_at, modified_at_ns, indexed_at, node_count, errors)
		VALUES (?, ?, '', ?, ?, ?, ?, ?, ?, ?, '')
		ON CONFLICT(path) DO UPDATE SET
			content_hash=excluded.content_hash,
			syntax_hash=excluded.syntax_hash,
			language=excluded.language,
			size=excluded.size,
			modified_at=excluded.modified_at,
			modified_at_ns=excluded.modified_at_ns,
			indexed_at=excluded.indexed_at,
			node_count=excluded.node_count,
			errors=''
	`)
	if err != nil {
		return err
	}
	defer fileStmt.Close()

	_, err = fileStmt.Exec(relPath, contentHash, contentHash, language, stat.Size(), stat.ModTime().Unix(), stat.ModTime().UnixNano(), time.Now().Unix(), len(nodes))
	return err
}

func cleanupOrphanExternalNodesTx(tx *sqlx.Tx) error {
	_, err := tx.Exec(`
		DELETE FROM astramap_nodes
		WHERE kind = 'external'
		  AND id NOT IN (SELECT source FROM astramap_edges)
		  AND id NOT IN (SELECT target FROM astramap_edges)
	`)
	return err
}

type fileIndexFingerprint struct {
	ContentHash  string `db:"content_hash"`
	SemanticHash string `db:"semantic_hash"`
	SyntaxHash   string `db:"syntax_hash"`
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
	profile := BuildProjectProfile(projectRoot, filter, StageSyntax)
	pruned, err := PruneExcludedFiles(db, filter)
	if err != nil {
		return result, err
	}
	result.Pruned = pruned

	prunedDeleted, pruneDeletedErr := PruneDeletedFiles(db, projectRoot)
	if pruneDeletedErr != nil {
		logError("PruneDeletedFiles failed: %v", pruneDeletedErr)
	} else if prunedDeleted > 0 {
		result.PrunedDeleted = prunedDeleted
		logInfo("PruneDeletedFiles: Cleaned up residual records for %d deleted files", prunedDeleted)
	}

	languages := languageFilterSet(profile, langFilter)

	scanned := 0
	updated := 0
	updatedFiles := make([]string, 0)
	if hasSyntaxOverlay(profile, languages) {
		err = filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			relPath, _ := filepath.Rel(projectRoot, path)
			if info.IsDir() {
				if !filter.AllowsDir(relPath, StageSyntax) {
					return filepath.SkipDir
				}
				return nil
			}

			selection, supported := ResolveLanguageWithProfile(profile, path)
			if !supported || selection.Module == nil || (len(languages) > 0 && !languages[selection.ID]) {
				return nil
			}
			if !filter.Allows(relPath, StageSyntax) {
				return nil
			}

			scanned++

			changed, syncErr := syncFileAstraMapWithProfile(db, profile, path)
			if syncErr != nil {
				logWarn("skip file due to parse error: %s: %v", relPath, syncErr)
				return nil
			}
			if changed {
				updated++
				updatedFiles = append(updatedFiles, relPath)
			}
			return nil
		})
	}
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

func hasSyntaxOverlay(profile ProjectProfile, languages map[string]bool) bool {
	registry := profile.registry
	if registry == nil {
		registry = languageRegistryForProject(profile.ProjectRoot)
	}
	for id, spec := range registry.languages {
		if spec.module != nil && (len(languages) == 0 || languages[id]) {
			return true
		}
	}
	return false
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
		if filter.Allows(filePath, StageSyntax) {
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
			EffectiveLevel: "unavailable", SyntaxStatus: "missing",
			ProviderStatus: "not-configured",
		}
		if spec.module != nil {
			state.SyntaxStatus = "ready"
			state.EffectiveLevel = "syntax"
		}
		_ = db.Get(&state.Artifacts, "SELECT CASE WHEN EXISTS (SELECT 1 FROM astramap_nodes WHERE language = ? AND provenance = 'scip') THEN 1 ELSE 0 END", spec.ID)
		if state.Artifacts > 0 {
			state.EffectiveLevel = state.DeclaredLevel
			state.ProviderStatus = "imported"
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

	if err := tx.Commit(); err != nil {
		return err
	}
	var implementsEdges int
	_ = db.Get(&implementsEdges, `SELECT COUNT(*) FROM astramap_edges WHERE provenance = 'heuristic' AND kind = 'implements'`)
	logInfo("ResolveGoInterfaces: resolved %d heuristic implements edges", implementsEdges)
	return nil
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

	if err := tx.Commit(); err != nil {
		return err
	}
	var routeNodes int
	_ = db.Get(&routeNodes, `SELECT COUNT(*) FROM astramap_nodes WHERE kind = 'route'`)
	logInfo("ResolveWebRoutes: resolved %d heuristic route nodes", routeNodes)
	return nil
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
	if node.Kind == "macro" {
		node.Name = extractCMacroName(node.Name, node.Signature)
		node.QualifiedName = node.Name
	}
	node.Kind = normalizeCDeclarationKind(node.Kind, node.Signature)
}

// extractCMacroName recovers the real macro identifier from a #define signature.
// scip-clang emits position-based names like "file.h:3:10`!" which are useless
// as public identifiers. The actual macro name is the first token after #define.
func extractCMacroName(syntheticName, signature string) string {
	if !strings.ContainsRune(syntheticName, '`') && !strings.ContainsRune(syntheticName, ':') {
		return syntheticName
	}
	sig := strings.TrimSpace(signature)
	if !strings.HasPrefix(sig, "#define") {
		return syntheticName
	}
	rest := strings.TrimSpace(strings.TrimPrefix(sig, "#define"))
	// The macro name is the first identifier token; stop at '(' for function-like macros.
	i := 0
	for i < len(rest) && (rest[i] == '_' || (rest[i] >= 'a' && rest[i] <= 'z') || (rest[i] >= 'A' && rest[i] <= 'Z') || (rest[i] >= '0' && rest[i] <= '9')) {
		i++
	}
	if i == 0 {
		return syntheticName
	}
	return rest[:i]
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
		blockEnd := strings.HasSuffix(line, "*/")
		if blockEnd {
			if blockStart := strings.LastIndex(line, "/*"); blockStart > 0 && strings.TrimSpace(line[:blockStart]) != "" {
				break
			}
		}
		if !inBlock && !strings.HasPrefix(line, "//") && !blockEnd && !strings.HasPrefix(line, "/*") {
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
		if blockEnd {
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
			if blockStart := strings.Index(line, "/*"); blockStart > 0 && strings.TrimSpace(line[:blockStart]) != "" {
				commentLines = nil
				break
			}
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
