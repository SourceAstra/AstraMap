package astramap

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/jmoiron/sqlx"
)

var (
	overviewCache     *ProjectedGraphResult
	overviewCacheLock sync.RWMutex
)

// InvalidateOverviewCache clears the cached ProjectedGraphResult.
// This should be called whenever the graph data (nodes/edges) is mutated.
func InvalidateOverviewCache() {
	overviewCacheLock.Lock()
	defer overviewCacheLock.Unlock()
	overviewCache = nil
	InvalidateQueryHelperCache()
}

var stopWords = map[string]bool{
	"a": true, "about": true, "above": true, "after": true, "again": true, "against": true,
	"all": true, "am": true, "an": true, "and": true, "any": true, "are": true, "as": true,
	"at": true, "be": true, "because": true, "been": true, "before": true, "being": true,
	"below": true, "between": true, "both": true, "but": true, "by": true, "can": true,
	"did": true, "do": true, "does": true, "doing": true, "down": true, "during": true,
	"each": true, "few": true, "for": true, "from": true, "further": true, "had": true,
	"has": true, "have": true, "having": true, "he": true, "her": true, "here": true,
	"hers": true, "herself": true, "him": true, "himself": true, "his": true, "how": true,
	"if": true, "in": true, "into": true, "is": true, "it": true, "its": true, "itself": true,
	"me": true, "more": true, "most": true, "my": true, "myself": true, "no": true,
	"nor": true, "not": true, "of": true, "off": true, "on": true, "once": true, "only": true,
	"or": true, "other": true, "our": true, "ours": true, "ourselves": true, "out": true,
	"over": true, "own": true, "same": true, "she": true, "should": true, "so": true,
	"some": true, "such": true, "than": true, "that": true, "the": true, "their": true,
	"theirs": true, "them": true, "themselves": true, "then": true, "there": true, "these": true,
	"they": true, "this": true, "those": true, "through": true, "to": true, "too": true,
	"under": true, "until": true, "up": true, "very": true, "was": true, "we": true,
	"were": true, "what": true, "when": true, "where": true, "which": true, "while": true,
	"who": true, "whom": true, "why": true, "with": true, "you": true, "your": true,
	"yours": true, "yourself": true, "yourselves": true,
}

var chStopWords = map[string]bool{}

func cleanQueryTerms(query string) []string {
	re := regexp.MustCompile(`[a-zA-Z0-9_]+|[\p{Han}]`)
	matches := re.FindAllString(query, -1)

	var clean []string
	seen := make(map[string]bool)
	for _, term := range matches {
		termLower := strings.ToLower(term)
		if stopWords[termLower] || chStopWords[term] {
			continue
		}
		if seen[termLower] {
			continue
		}
		seen[termLower] = true
		clean = append(clean, term)
	}
	return clean
}

// ===== Shared Query Service Layer =====
// The single implementation of all query logic, accessed by both MCP handlers and REST handlers.

// IndexStatus holds index health metrics.
type IndexStatus struct {
	NodeCount          int      `json:"node_count" db:"node_count"`
	EdgeCount          int      `json:"edge_count" db:"edge_count"`
	FileCount          int      `json:"file_count" db:"file_count"`
	DirtyCount         int      `json:"dirty_count"`
	DirtyFiles         []string `json:"dirty_files"`
	SemanticDirtyCount int      `json:"semantic_dirty_count"`
	SemanticDirtyFiles []string `json:"semantic_dirty_files"`
}

// ExploreFileResult groups symbols and source code for a single file.
type ExploreFileResult struct {
	FilePath string
	Symbols  []*AstraMapNode
	Source   string // source code with line numbers
}

// ExploreResult is the structured return of QueryExplore.
type ExploreResult struct {
	Files         []ExploreFileResult
	Relationships []string
}

type GraphDataResult struct {
	Nodes []*AstraMapNode `json:"nodes"`
	Edges []*AstraMapEdge `json:"edges"`
	Files []*AstraMapFile `json:"files"`
}

type ProjectedGraphResult struct {
	Nodes []ProjectedNode `json:"nodes"`
	Links []ProjectedLink `json:"links"`
}

type ProjectedNode struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Weight int    `json:"weight"`
}

type ProjectedLink struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Weight int    `json:"weight"`
}

type ModuleGraphResult struct {
	Nodes []ModuleGraphNode `json:"nodes"`
	Edges []ModuleGraphEdge `json:"edges"`
}

type ModuleGraphNode struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Kind     string `json:"kind"`
	File     string `json:"file"`
	FilePath string `json:"filePath"`
	Line     int    `json:"line"`
}

type ModuleGraphEdge struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Kind       string `json:"kind"`
	Provenance string `json:"provenance,omitempty"`
	Line       int    `json:"line,omitempty"`
	Col        int    `json:"col,omitempty"`
}

const nonSyntheticAnonymousNodeSQL = `
	AND name NOT LIKE '$anonymous_type_%'
	AND qualified_name NOT LIKE '%$anonymous_type_%'
	AND id NOT LIKE '%$anonymous_type_%'
`

func QueryGraphData(db *sqlx.DB) (*GraphDataResult, error) {
	var nodes []*AstraMapNode
	if err := db.Select(&nodes, `
		SELECT *
		FROM astramap_nodes
		WHERE kind IN ('function', 'method', 'route', 'external')
		  AND file_path NOT LIKE '%.h' AND file_path NOT LIKE '%.hpp' AND file_path NOT LIKE '%.hh'
		`+nonSyntheticAnonymousNodeSQL+`
		ORDER BY file_path, start_line, name
	`); err != nil {
		return nil, err
	}

	var edges []*AstraMapEdge
	if err := db.Select(&edges, `
		SELECT id, source, target, kind, provenance, line, col, COALESCE(metadata, '') AS metadata
		FROM astramap_edges
		WHERE kind IN ('calls', 'imports', 'implements', 'route')
		ORDER BY id
	`); err != nil {
		return nil, err
	}

	nodes, edges = canonicalizeDuplicateFunctionNodes(nodes, edges)
	edges = filterEdgesToNodes(edges, nodes)
	canonicalizeOutputGraphIDs(nodes, edges)

	var files []*AstraMapFile
	if err := db.Select(&files, "SELECT * FROM astramap_files ORDER BY path"); err != nil {
		return nil, err
	}

	return &GraphDataResult{Nodes: nodes, Edges: edges, Files: files}, nil
}

func QueryProjectedGraph(db *sqlx.DB) (*ProjectedGraphResult, error) {
	overviewCacheLock.RLock()
	cache := overviewCache
	overviewCacheLock.RUnlock()
	if cache != nil {
		return cache, nil
	}

	var nodeRows []struct {
		ID       string `db:"id"`
		Name     string `db:"name"`
		FilePath string `db:"file_path"`
	}
	if err := db.Select(&nodeRows, `
		SELECT id, name, file_path
		FROM astramap_nodes
		WHERE kind IN ('function', 'method', 'route')
		  AND file_path NOT LIKE '%.h' AND file_path NOT LIKE '%.hpp' AND file_path NOT LIKE '%.hh'
		`+nonSyntheticAnonymousNodeSQL+`
	`); err != nil {
		return nil, err
	}

	nodeModule := make(map[string]string, len(nodeRows))
	moduleWeight := make(map[string]int)
	for _, row := range nodeRows {
		module := moduleFromFilePath(row.FilePath)
		nodeModule[row.ID] = module
		moduleWeight[module]++
	}

	var edgeRows []struct {
		SourceFile string `db:"source_file"`
		TargetFile string `db:"target_file"`
		Weight     int    `db:"weight"`
	}
	if err := db.Select(&edgeRows, `
		SELECT n1.file_path AS source_file, n2.file_path AS target_file, COUNT(*) AS weight
		FROM astramap_edges e
		JOIN astramap_nodes n1 ON e.source = n1.id
		JOIN astramap_nodes n2 ON e.target = n2.id
		WHERE e.kind IN ('calls','imports','implements','route')
		  AND n1.kind IN ('function','method','route')
		  AND n2.kind IN ('function','method','route')
		  AND n1.file_path NOT LIKE '%.h' AND n1.file_path NOT LIKE '%.hpp' AND n1.file_path NOT LIKE '%.hh'
		  AND n2.file_path NOT LIKE '%.h' AND n2.file_path NOT LIKE '%.hpp' AND n2.file_path NOT LIKE '%.hh'
		GROUP BY n1.file_path, n2.file_path
	`); err != nil {
		return nil, err
	}

	linkWeight := make(map[string]int)
	for _, row := range edgeRows {
		sourceModule := moduleFromFilePath(row.SourceFile)
		targetModule := moduleFromFilePath(row.TargetFile)
		if sourceModule == "" || targetModule == "" || sourceModule == targetModule {
			continue
		}
		linkWeight[sourceModule+"\x00"+targetModule] += row.Weight
	}

	nodes := make([]ProjectedNode, 0, len(moduleWeight))
	for module, weight := range moduleWeight {
		nodes = append(nodes, ProjectedNode{ID: module, Name: module, Weight: weight})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	links := make([]ProjectedLink, 0, len(linkWeight))
	for key, weight := range linkWeight {
		parts := strings.SplitN(key, "\x00", 2)
		if len(parts) != 2 {
			continue
		}
		links = append(links, ProjectedLink{Source: parts[0], Target: parts[1], Weight: weight})
	}
	sort.Slice(links, func(i, j int) bool {
		if links[i].Source != links[j].Source {
			return links[i].Source < links[j].Source
		}
		return links[i].Target < links[j].Target
	})

	result := &ProjectedGraphResult{Nodes: nodes, Links: links}
	overviewCacheLock.Lock()
	overviewCache = result
	overviewCacheLock.Unlock()

	return result, nil
}

func QueryFunctionList(db *sqlx.DB) ([]ModuleGraphNode, error) {
	var nodes []*AstraMapNode
	if err := db.Select(&nodes, `
		SELECT id, kind, name, file_path, start_line
		FROM astramap_nodes
		WHERE kind IN ('function', 'method')
		  AND file_path NOT LIKE '%.h' AND file_path NOT LIKE '%.hpp' AND file_path NOT LIKE '%.hh'
		`+nonSyntheticAnonymousNodeSQL+`
		ORDER BY file_path, start_line, name
	`); err != nil {
		return nil, err
	}
	return moduleGraphNodes(nodes), nil
}

func QueryModuleGraph(db *sqlx.DB, moduleID string) (*ModuleGraphResult, error) {
	if moduleID == "" {
		moduleID = "(root)"
	}

	var nodes []*AstraMapNode
	nodeQuery := `
		SELECT id, kind, name, qualified_name, file_path, language, start_line, end_line,
		       start_column, end_column, signature, docstring, visibility, return_type,
		       is_exported, updated_at
		FROM astramap_nodes
		WHERE kind IN ('function', 'method', 'route')
		  AND file_path NOT LIKE '%.h' AND file_path NOT LIKE '%.hpp' AND file_path NOT LIKE '%.hh'
		` + nonSyntheticAnonymousNodeSQL
	var nodeArgs []interface{}
	if moduleID == "(root)" {
		nodeQuery += " AND file_path NOT LIKE ?"
		nodeArgs = append(nodeArgs, "%/%")
	} else {
		nodeQuery += " AND file_path LIKE ?"
		nodeArgs = append(nodeArgs, moduleID+"/%")
	}
	nodeQuery += " ORDER BY file_path, start_line, name"
	if err := db.Select(&nodes, nodeQuery, nodeArgs...); err != nil {
		return nil, err
	}

	nodeIDs := make([]string, 0, len(nodes))
	for _, node := range nodes {
		nodeIDs = append(nodeIDs, node.ID)
	}
	if len(nodes) == 0 {
		return &ModuleGraphResult{Nodes: []ModuleGraphNode{}, Edges: []ModuleGraphEdge{}}, nil
	}

	var edges []*AstraMapEdge
	edgeQuery, edgeArgs, err := sqlx.In(`
		SELECT id, source, target, kind, provenance, line, col, COALESCE(metadata, '') AS metadata
		FROM astramap_edges
		WHERE kind IN ('calls', 'imports', 'implements', 'route') AND source IN (?) AND target IN (?)
		ORDER BY id
	`, nodeIDs, nodeIDs)
	if err != nil {
		return nil, err
	}
	edgeQuery = db.Rebind(edgeQuery)
	if err := db.Select(&edges, edgeQuery, edgeArgs...); err != nil {
		return nil, err
	}

	resultEdges := make([]ModuleGraphEdge, 0, len(edges))
	idMap := canonicalNodeIDMap(nodes)
	for _, edge := range edges {
		resultEdges = append(resultEdges, ModuleGraphEdge{
			From:       canonicalEdgeEndpoint(idMap, edge.Source),
			To:         canonicalEdgeEndpoint(idMap, edge.Target),
			Kind:       edge.Kind,
			Provenance: edge.Provenance,
			Line:       edge.Line,
			Col:        edge.Col,
		})
	}

	return &ModuleGraphResult{Nodes: moduleGraphNodes(nodes), Edges: resultEdges}, nil
}

func canonicalNodeIDMap(nodes []*AstraMapNode) map[string]string {
	idMap := make(map[string]string, len(nodes))
	for _, node := range nodes {
		idMap[node.ID] = CanonicalSymbolID(node)
	}
	return idMap
}

func canonicalEdgeEndpoint(idMap map[string]string, id string) string {
	if mapped := idMap[id]; mapped != "" {
		return mapped
	}
	if strings.HasPrefix(id, "external:") {
		return "external:" + externalSymbolName(id)
	}
	if strings.Contains(id, "::") {
		parts := strings.SplitN(id, ":", 2)
		if len(parts) == 2 && !strings.Contains(parts[0], "/") && !strings.Contains(parts[0], "\\") {
			return parts[1]
		}
	}
	return id
}

func canonicalizeOutputGraphIDs(nodes []*AstraMapNode, edges []*AstraMapEdge) {
	idMap := canonicalNodeIDMap(nodes)
	for _, node := range nodes {
		node.ID = CanonicalSymbolID(node)
	}
	for _, edge := range edges {
		edge.Source = canonicalEdgeEndpoint(idMap, edge.Source)
		edge.Target = canonicalEdgeEndpoint(idMap, edge.Target)
	}
}

func canonicalizeDuplicateFunctionNodes(nodes []*AstraMapNode, edges []*AstraMapEdge) ([]*AstraMapNode, []*AstraMapEdge) {
	degree := make(map[string]int)
	for _, edge := range edges {
		if edge.Kind != "calls" {
			continue
		}
		degree[edge.Source]++
		degree[edge.Target]++
	}

	groups := make(map[string][]*AstraMapNode)
	for _, node := range nodes {
		if node.Kind != "function" && node.Kind != "method" {
			continue
		}
		key := node.Kind + "\x00" + node.FilePath + "\x00" + node.Name
		groups[key] = append(groups[key], node)
	}

	alias := make(map[string]string)
	dropped := make(map[string]bool)
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		best := group[0]
		for _, node := range group[1:] {
			if degree[node.ID] > degree[best.ID] {
				best = node
			}
		}
		for _, node := range group {
			alias[node.ID] = best.ID
			if node.ID != best.ID {
				dropped[node.ID] = true
			}
		}
	}

	canonicalID := func(id string) string {
		if mapped := alias[id]; mapped != "" {
			return mapped
		}
		return id
	}

	filteredNodes := make([]*AstraMapNode, 0, len(nodes))
	for _, node := range nodes {
		if !dropped[node.ID] {
			filteredNodes = append(filteredNodes, node)
		}
	}

	seenEdges := make(map[string]bool)
	filteredEdges := make([]*AstraMapEdge, 0, len(edges))
	for _, edge := range edges {
		source := canonicalID(edge.Source)
		target := canonicalID(edge.Target)
		if source == target && edge.Source != edge.Target {
			continue
		}
		key := source + "\x00" + target + "\x00" + edge.Kind
		if seenEdges[key] {
			continue
		}
		seenEdges[key] = true
		copied := *edge
		copied.Source = source
		copied.Target = target
		filteredEdges = append(filteredEdges, &copied)
	}

	return filteredNodes, filteredEdges
}

func filterEdgesToNodes(edges []*AstraMapEdge, nodes []*AstraMapNode) []*AstraMapEdge {
	visible := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		visible[node.ID] = true
	}
	filtered := make([]*AstraMapEdge, 0, len(edges))
	for _, edge := range edges {
		if visible[edge.Source] && visible[edge.Target] {
			filtered = append(filtered, edge)
		}
	}
	return filtered
}

func moduleFromFilePath(filePath string) string {
	filePath = filepath.ToSlash(strings.TrimSpace(filePath))
	filePath = strings.Trim(filePath, "/")
	if filePath == "" {
		return "(root)"
	}
	parts := strings.Split(filePath, "/")
	if len(parts) <= 1 || parts[0] == "" {
		return "(root)"
	}
	return parts[0]
}

func moduleGraphNodes(nodes []*AstraMapNode) []ModuleGraphNode {
	result := make([]ModuleGraphNode, 0, len(nodes))
	for _, node := range nodes {
		if isSyntheticAnonymousSymbol(node.ID, node.Name) {
			continue
		}
		result = append(result, ModuleGraphNode{
			ID:       CanonicalSymbolID(node),
			Name:     node.Name,
			Type:     node.Kind,
			Kind:     node.Kind,
			File:     node.FilePath,
			FilePath: node.FilePath,
			Line:     node.StartLine,
		})
	}
	return result
}

// QuerySearch performs fuzzy symbol search with parameterized queries.
func QuerySearch(db *sqlx.DB, query, kind string, limit int) ([]*AstraMapNode, error) {
	return QuerySearchPaged(db, query, kind, limit, 0)
}

func QuerySearchPaged(db *sqlx.DB, query, kind string, limit, offset int) ([]*AstraMapNode, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("empty query is not allowed")
	}
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	if err := validateSearchKind(kind); err != nil {
		return nil, err
	}
	var nodes []*AstraMapNode
	q := "SELECT * FROM astramap_nodes WHERE (name LIKE ? OR qualified_name LIKE ?) " + nonSyntheticAnonymousNodeSQL
	params := []interface{}{"%" + query + "%", "%" + query + "%"}
	if kind != "" {
		if kind == "struct" {
			q += "AND ((kind = 'struct' AND signature NOT LIKE 'enum %' AND signature NOT LIKE 'typedef %') OR (kind = 'class' AND language IN ('c', 'cpp') AND signature NOT LIKE 'enum %' AND signature NOT LIKE 'typedef %' AND name NOT LIKE '%_e')) "
		} else if kind == "enum" {
			q += "AND (kind = 'enum' OR (kind IN ('class', 'struct') AND language IN ('c', 'cpp') AND (signature LIKE 'enum %' OR signature LIKE 'typedef enum%' OR name LIKE '%_e' OR name LIKE '%enum%')) OR (kind IN ('typedef', 'type') AND language IN ('c', 'cpp') AND signature LIKE 'typedef enum%')) "
		} else {
			if kind == "typedef" || kind == "type" {
				q += "AND (kind IN ('typedef', 'type') OR (language IN ('c', 'cpp') AND signature LIKE 'typedef %') OR (language = 'go' AND kind = 'struct')) "
			} else {
				q += "AND kind = ? "
				params = append(params, kind)
			}
		}
	}
	q += " ORDER BY file_path, start_line, name LIMIT ? OFFSET ?"
	params = append(params, limit, offset)
	err := db.Select(&nodes, q, params...)
	if err == nil {
		for _, node := range nodes {
			normalizeSearchNodeKind(node, kind)
			node.ID = CanonicalSymbolID(node)
		}
	}
	return nodes, err
}

func normalizeSearchNodeKind(node *AstraMapNode, requestedKind string) {
	normalizeTypedefNodeKind(node)
	if node == nil || (node.Language != "c" && node.Language != "cpp") {
		return
	}
	signature := strings.TrimSpace(node.Signature)
	switch requestedKind {
	case "typedef":
		if strings.HasPrefix(signature, "typedef ") {
			node.Kind = "typedef"
		}
	case "type":
		if strings.HasPrefix(signature, "typedef ") {
			node.Kind = "type"
		}
	case "enum":
		if strings.HasPrefix(signature, "enum ") || strings.HasPrefix(signature, "typedef enum ") {
			node.Kind = "enum"
		}
	case "struct":
		if strings.HasPrefix(signature, "struct ") {
			node.Kind = "struct"
		}
	}
}

func normalizeTypedefNodeKind(node *AstraMapNode) {
	if node == nil {
		return
	}
	if node.Language != "c" && node.Language != "cpp" {
		return
	}
	signature := strings.TrimSpace(node.Signature)
	if strings.HasPrefix(signature, "typedef ") {
		node.Kind = "typedef"
	}
}

func CanonicalSymbolID(n *AstraMapNode) string {
	if n == nil {
		return ""
	}
	if n.Kind == "external" || strings.HasPrefix(n.ID, "external:") {
		name := externalSymbolName(n.ID)
		if name == "" {
			name = strings.TrimSpace(n.Name)
		}
		if name == "" {
			name = strings.TrimPrefix(n.ID, "external:")
		}
		return "external:" + name
	}
	qname := strings.TrimSpace(n.QualifiedName)
	name := strings.TrimSpace(n.Name)
	if name != "" && (n.Language == "c" || n.Language == "cpp" || strings.HasPrefix(qname, name+"(")) {
		qname = name
	}
	if qname == "" {
		qname = name
	}
	if n.FilePath == "" {
		return qname
	}
	return n.FilePath + "::" + qname
}

func CanonicalSymbolIDForNodeID(db *sqlx.DB, id string) string {
	var node AstraMapNode
	if err := db.Get(&node, "SELECT * FROM astramap_nodes WHERE id = ? LIMIT 1", id); err == nil {
		return CanonicalSymbolID(&node)
	}
	if strings.HasPrefix(id, "external:") {
		return "external:" + externalSymbolName(id)
	}
	return id
}

// BatchCanonicalSymbolIDs batches resolving of multiple node IDs to their canonical symbol IDs.
func BatchCanonicalSymbolIDs(db *sqlx.DB, ids []string) (map[string]string, error) {
	if len(ids) == 0 {
		return map[string]string{}, nil
	}
	uniqMap := make(map[string]struct{})
	for _, id := range ids {
		if id != "" {
			uniqMap[id] = struct{}{}
		}
	}
	if len(uniqMap) == 0 {
		return map[string]string{}, nil
	}
	deduped := make([]string, 0, len(uniqMap))
	for id := range uniqMap {
		deduped = append(deduped, id)
	}
	result := make(map[string]string, len(ids))
	const batchSize = 500
	for i := 0; i < len(deduped); i += batchSize {
		end := i + batchSize
		if end > len(deduped) {
			end = len(deduped)
		}
		batch := deduped[i:end]
		query, args, err := sqlx.In("SELECT * FROM astramap_nodes WHERE id IN (?)", batch)
		if err != nil {
			return nil, err
		}
		query = db.Rebind(query)
		var nodes []AstraMapNode
		if err := db.Select(&nodes, query, args...); err != nil {
			return nil, err
		}
		for _, node := range nodes {
			result[node.ID] = CanonicalSymbolID(&node)
		}
	}
	for _, id := range ids {
		if _, ok := result[id]; !ok {
			if strings.HasPrefix(id, "external:") {
				result[id] = "external:" + externalSymbolName(id)
			} else {
				result[id] = id
			}
		}
	}
	return result, nil
}

// ResolveSymbolToIDs resolves a bare symbol name or partial ID to a list of full node IDs.
// Tries exact id match first, then name/qualified_name matching.
func ResolveSymbolToIDs(db *sqlx.DB, symbol string) ([]string, error) {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return nil, nil
	}

	var ids []string
	err := db.Select(&ids, "SELECT id FROM astramap_nodes WHERE id = ? "+nonSyntheticAnonymousNodeSQL, symbol)
	if err == nil && len(ids) > 0 {
		return ids, nil
	}
	if strings.HasPrefix(symbol, "external:") {
		if ids, err = ResolveExternalSymbolToIDs(db, strings.TrimPrefix(symbol, "external:")); err != nil || len(ids) > 0 {
			return ids, err
		}
	}
	if filePart, qnamePart, ok := splitCanonicalSymbolID(symbol); ok {
		err = db.Select(&ids, `
			SELECT id
			FROM astramap_nodes
			WHERE file_path = ?
			  AND (qualified_name = ? OR name = ?)
			`+nonSyntheticAnonymousNodeSQL+`
			ORDER BY
				CASE
					WHEN id LIKE 'cxx . . $%' OR id LIKE 'scip:%' OR id LIKE 'go:%' THEN 0
					ELSE 1
				END,
				start_line,
				id
			LIMIT 20
		`, filePart, qnamePart, qnamePart)
		if err != nil {
			return nil, err
		}
		if len(ids) > 0 {
			return ids, nil
		}
	}
	err = db.Select(&ids, `
		SELECT id
		FROM astramap_nodes
		WHERE (name = ? OR qualified_name = ? OR qualified_name LIKE ?)
		`+nonSyntheticAnonymousNodeSQL+`
		ORDER BY
			CASE
				WHEN name = ? AND kind IN ('function', 'method') THEN 0
				WHEN name = ? THEN 1
				WHEN qualified_name = ? AND kind IN ('function', 'method') THEN 2
				WHEN qualified_name = ? THEN 3
				WHEN qualified_name LIKE ? AND kind IN ('function', 'method') THEN 4
				ELSE 5
			END,
			CASE
				WHEN id LIKE 'cxx . . $%' OR id LIKE 'scip:%' OR id LIKE 'go:%' THEN 0
				ELSE 1
			END,
			file_path,
			start_line,
			id
		LIMIT 20
	`, symbol, symbol, "%"+symbol+"%", symbol, symbol, symbol, symbol, "%"+symbol+"%")
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		return ids, nil
	}
	return ResolveExternalSymbolToIDs(db, symbol)
}

func splitCanonicalSymbolID(symbol string) (filePart, qnamePart string, ok bool) {
	if strings.HasPrefix(symbol, "external:") {
		return "", "", false
	}
	idx := strings.LastIndex(symbol, "::")
	if idx <= 0 || idx+2 >= len(symbol) {
		return "", "", false
	}
	filePart = strings.TrimSpace(symbol[:idx])
	qnamePart = strings.TrimSpace(symbol[idx+2:])
	if filePart == "" || qnamePart == "" {
		return "", "", false
	}
	return filePart, qnamePart, true
}

func ResolveExternalSymbolToIDs(db *sqlx.DB, symbol string) ([]string, error) {
	var candidates []string
	like := "%" + symbol + "%"
	err := db.Select(&candidates, `
		SELECT DISTINCT id
		FROM astramap_nodes
		WHERE (kind = 'external' OR id LIKE 'external:%')
		  AND (id = ? OR name = ? OR qualified_name = ? OR id LIKE ? OR name LIKE ? OR qualified_name LIKE ?)
		LIMIT 20
	`, symbol, symbol, symbol, like, like, like)
	if err != nil {
		return nil, err
	}

	if len(candidates) < 20 {
		var edgeTargets []string
		err = db.Select(&edgeTargets, `
			SELECT DISTINCT target
			FROM astramap_edges
			WHERE target LIKE 'external:%' AND target LIKE ?
			LIMIT 50
		`, like)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, edgeTargets...)
	}

	seen := make(map[string]bool)
	ids := make([]string, 0, len(candidates))
	for _, id := range candidates {
		if seen[id] || !externalSymbolMatches(id, symbol) {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
		if len(ids) >= 20 {
			break
		}
	}
	return ids, nil
}

func externalSymbolMatches(id, symbol string) bool {
	if id == symbol {
		return true
	}
	name := externalSymbolName(id)
	return name == symbol || strings.Contains(id, "$ "+symbol+"(") || strings.Contains(id, "$ "+symbol+".")
}

func externalSymbolName(id string) string {
	id = strings.TrimPrefix(id, "external:")
	if idx := strings.LastIndex(id, "$ "); idx >= 0 {
		id = id[idx+2:]
	}
	id = strings.TrimSpace(id)
	if idx := strings.IndexAny(id, "(. "); idx >= 0 {
		id = id[:idx]
	}
	return id
}

// QueryExplore performs FTS5 full-text search + source code + relationships.
// Handles both symbol queries and natural language task descriptions.
func QueryExplore(db *sqlx.DB, query, projectRoot string, maxFiles int) (*ExploreResult, error) {
	if maxFiles <= 0 {
		maxFiles = 3
	}

	terms := cleanQueryTerms(query)
	var matchedNodes []*AstraMapNode
	var err error
	if len(terms) == 0 {
		// Empty query: select top nodes to populate G_DATA skeleton
		err = db.Select(&matchedNodes, "SELECT * FROM astramap_nodes WHERE 1=1 "+nonSyntheticAnonymousNodeSQL+" ORDER BY file_path, start_line LIMIT ?", maxFiles)
	} else {
		ftsQuery := strings.Join(terms, " OR ")
		err = db.Select(&matchedNodes,
			"SELECT n.* FROM astramap_nodes n "+
				"JOIN (SELECT astramap_fts.id AS id, bm25(astramap_fts) as rank FROM astramap_fts WHERE astramap_fts MATCH ? ORDER BY rank LIMIT ?) f "+
				"ON n.id = f.id WHERE 1=1 "+
				"AND n.name NOT LIKE '$anonymous_type_%' "+
				"AND n.qualified_name NOT LIKE '%$anonymous_type_%' "+
				"AND n.id NOT LIKE '%$anonymous_type_%' "+
				"ORDER BY f.rank",
			ftsQuery, maxFiles)
	}
	if err != nil {
		return nil, err
	}
	for _, n := range matchedNodes {
		normalizeTypedefNodeKind(n)
	}

	// Group by file
	fileMap := make(map[string]*ExploreFileResult)
	var fileOrder []string
	for _, n := range matchedNodes {
		fr, ok := fileMap[n.FilePath]
		if !ok {
			fr = &ExploreFileResult{FilePath: n.FilePath}
			fileMap[n.FilePath] = fr
			fileOrder = append(fileOrder, n.FilePath)
		}
		fr.Symbols = append(fr.Symbols, n)
		// Read source for this symbol
		if projectRoot != "" {
			code, _ := ReadSourceRange(projectRoot, n.FilePath, n.StartLine, n.EndLine)
			if code != "" && fr.Source == "" {
				fr.Source = code
			}
		}
	}

	result := &ExploreResult{}
	for _, fp := range fileOrder {
		result.Files = append(result.Files, *fileMap[fp])
	}

	// Collect caller relationships for all matched nodes
	for _, n := range matchedNodes {
		callers, _ := GetCallers(db, n.ID)
		for _, c := range callers {
			result.Relationships = append(result.Relationships,
				CanonicalSymbolIDForNodeID(db, c.Source)+" → "+CanonicalSymbolID(n))
		}
		if n.Kind == "struct" || n.Kind == "class" || n.Kind == "interface" {
			var structEdges []struct {
				Source string `db:"source"`
				Target string `db:"target"`
				Kind   string `db:"kind"`
			}
			err := db.Select(&structEdges, "SELECT source, target, kind FROM astramap_edges WHERE (source = ? OR target = ?) AND kind IN ('contains', 'implements')", n.ID, n.ID)
			if err == nil {
				for _, e := range structEdges {
					src := CanonicalSymbolIDForNodeID(db, e.Source)
					tgt := CanonicalSymbolIDForNodeID(db, e.Target)
					if src != "" && tgt != "" {
						result.Relationships = append(result.Relationships,
							src+" "+e.Kind+" → "+tgt)
					}
				}
			}
		}
	}
	for _, n := range matchedNodes {
		n.ID = CanonicalSymbolID(n)
	}

	return result, nil
}

// QueryNodeBySymbol finds nodes by symbol name or file path.
func QueryNodeBySymbol(db *sqlx.DB, symbol, file string) ([]*AstraMapNode, error) {
	var nodes []*AstraMapNode
	file = normalizeNodeFileFilter(file)
	if symbol != "" {
		q := "SELECT * FROM astramap_nodes WHERE (qualified_name LIKE ? OR name = ?) " + nonSyntheticAnonymousNodeSQL
		params := []interface{}{"%" + symbol + "%", symbol}
		if file != "" {
			q += " AND " + nodeFileFilterSQL()
			params = append(params, nodeFileFilterParams(file)...)
		}
		q += " ORDER BY file_path, start_line, name"
		err := db.Select(&nodes, q, params...)
		if err == nil {
			for _, node := range nodes {
				normalizeTypedefNodeKind(node)
			}
		}
		return nodes, err
	}
	if file != "" {
		q := "SELECT * FROM astramap_nodes WHERE " + nodeFileFilterSQL() + nonSyntheticAnonymousNodeSQL + " LIMIT 10"
		err := db.Select(&nodes, q, nodeFileFilterParams(file)...)
		if err == nil {
			for _, node := range nodes {
				normalizeTypedefNodeKind(node)
			}
		}
		return nodes, err
	}
	return nodes, nil
}

func normalizeNodeFileFilter(file string) string {
	file = filepath.ToSlash(strings.TrimSpace(file))
	file = strings.TrimPrefix(file, "./")
	file = strings.TrimLeft(file, "/")
	if file == "." {
		return ""
	}
	return file
}

func nodeFileFilterSQL() string {
	return "(file_path = ? OR file_path LIKE ?)"
}

func nodeFileFilterParams(file string) []interface{} {
	return []interface{}{file, "%/" + file}
}

// QueryStatus returns index health metrics.
func QueryStatus(db *sqlx.DB) (*IndexStatus, error) {
	return QueryStatusWithProjectRoot(db, "")
}

func QueryStatusWithProjectRoot(db *sqlx.DB, projectRoot string) (*IndexStatus, error) {
	s := &IndexStatus{}
	if err := db.Get(&s.NodeCount, "SELECT COUNT(*) FROM astramap_nodes"); err != nil {
		return nil, err
	}
	if err := db.Get(&s.EdgeCount, "SELECT COUNT(*) FROM astramap_edges"); err != nil {
		return nil, err
	}
	if err := db.Get(&s.FileCount, "SELECT COUNT(*) FROM astramap_files"); err != nil {
		return nil, err
	}
	if projectRoot != "" {
		dirty, dirtyCount, err := QueryDirtyFilesWithCount(db, projectRoot, 100)
		if err != nil {
			return nil, err
		}
		s.DirtyFiles = dirty
		s.DirtyCount = dirtyCount
	}
	if err := db.Get(&s.SemanticDirtyCount, "SELECT COUNT(*) FROM astramap_files WHERE syntax_hash != '' AND semantic_hash != syntax_hash"); err != nil {
		return nil, err
	}
	if err := db.Select(&s.SemanticDirtyFiles, "SELECT path FROM astramap_files WHERE syntax_hash != '' AND semantic_hash != syntax_hash ORDER BY path LIMIT 100"); err != nil {
		return nil, err
	}
	return s, nil
}

func QueryDirtyFiles(db *sqlx.DB, projectRoot string, limit int) ([]string, error) {
	dirty, _, err := QueryDirtyFilesWithCount(db, projectRoot, limit)
	return dirty, err
}

func QueryDirtyFilesWithCount(db *sqlx.DB, projectRoot string, limit int) ([]string, int, error) {
	if limit <= 0 {
		limit = 100
	}
	var files []*AstraMapFile
	if err := db.Select(&files, "SELECT * FROM astramap_files ORDER BY path"); err != nil {
		return nil, 0, err
	}
	indexed := make(map[string]*AstraMapFile, len(files))
	for _, file := range files {
		if file != nil && file.Path != "" {
			indexed[filepath.ToSlash(file.Path)] = file
		}
	}
	dirty := make([]string, 0)
	dirtyCount := 0
	markDirty := func(path string) {
		dirtyCount++
		dirty = append(dirty, path)
	}
	filter, err := LoadIndexFilter(projectRoot)
	if err != nil {
		return nil, 0, err
	}
	profile := BuildProjectProfile(projectRoot, filter, StageSyntax)
	seen := make(map[string]bool, len(indexed))
	err = filepath.Walk(projectRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info == nil {
			return nil
		}
		relPath, relErr := filepath.Rel(projectRoot, path)
		if relErr != nil {
			return nil
		}
		relPath = filepath.ToSlash(relPath)
		if info.IsDir() {
			if path != projectRoot && !filter.AllowsDir(relPath, StageSyntax) {
				return filepath.SkipDir
			}
			return nil
		}
		selection, supported := ResolveLanguageWithProfile(profile, path)
		if !supported || selection.Module == nil || !filter.Allows(relPath, StageSyntax) {
			return nil
		}
		seen[relPath] = true
		file := indexed[relPath]
		if file == nil || file.SyntaxHash == "" {
			markDirty(relPath)
			return nil
		}
		if info.Size() == file.Size && info.ModTime().UnixNano() == file.ModifiedAtNS {
			return nil
		}
		hash, hashErr := fileContentHash(path)
		if hashErr != nil || hash != file.SyntaxHash {
			markDirty(relPath)
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	for path := range indexed {
		if !seen[path] {
			markDirty(path)
		}
	}
	sort.Strings(dirty)
	if len(dirty) > limit {
		dirty = dirty[:limit]
	}
	return dirty, dirtyCount, nil
}

func fileContentHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// QueryFiles lists indexed files, optionally filtered by path prefix and glob pattern.
func QueryFiles(db *sqlx.DB, pathPrefix, pattern string) ([]*AstraMapFile, error) {
	return QueryFilesPaged(db, pathPrefix, pattern, 100, 0)
}

func QueryFilesPaged(db *sqlx.DB, pathPrefix, pattern string, limit, offset int) ([]*AstraMapFile, error) {
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	q := "SELECT * FROM astramap_files "
	var conditions []string
	var params []interface{}
	pathPrefix = normalizeFilePathPrefix(pathPrefix)
	if pathPrefix != "" {
		conditions = append(conditions, "path LIKE ?")
		params = append(params, pathPrefix+"%")
	}
	if pattern != "" {
		pattern = strings.ReplaceAll(pattern, "*", "%")
		conditions = append(conditions, "path LIKE ?")
		params = append(params, "%"+pattern)
	}
	if len(conditions) > 0 {
		q += "WHERE " + strings.Join(conditions, " AND ")
	}
	q += " ORDER BY path ASC LIMIT ? OFFSET ?"
	params = append(params, limit, offset)

	var files []*AstraMapFile
	err := db.Select(&files, q, params...)
	return files, err
}

func normalizeFilePathPrefix(pathPrefix string) string {
	pathPrefix = filepath.ToSlash(strings.TrimSpace(pathPrefix))
	if pathPrefix == "" || pathPrefix == "." || pathPrefix == "./" {
		return ""
	}
	pathPrefix = strings.TrimPrefix(pathPrefix, "./")
	pathPrefix = strings.TrimLeft(pathPrefix, "/")
	pathPrefix = filepath.ToSlash(filepath.Clean(pathPrefix))
	if pathPrefix == "." {
		return ""
	}
	return pathPrefix
}

// ReadSourceRange reads source file lines [startLine, endLine] with 1-based line numbers.
func ReadSourceRange(projectRoot, filePath string, startLine, endLine int) (string, error) {
	absPath := filepath.Join(projectRoot, filePath)
	file, err := os.Open(absPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var matched []string
	scanner := bufio.NewScanner(file)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum >= startLine && lineNum <= endLine {
			matched = append(matched, scanner.Text())
		}
		if lineNum > endLine {
			break
		}
	}
	return strings.Join(matched, "\n"), scanner.Err()
}

// QueryTraceCTE returns the direct call star centered on startNodeID.
// The dashboard keeps depth for UI compatibility; this query is intentionally
// one-hop to keep click-to-trace latency bounded.
func QueryTraceCTE(db *sqlx.DB, projectRoot string, startNodeID string, maxDepth int) ([]*AstraMapNode, []*AstraMapEdge, error) {
	_ = projectRoot
	_ = maxDepth

	var startID string
	err := db.Get(&startID, "SELECT id FROM astramap_nodes WHERE id = ? LIMIT 1", startNodeID)
	if err != nil {
		err = db.Get(&startID, "SELECT id FROM astramap_nodes WHERE name = ? ORDER BY file_path, start_line LIMIT 1", startNodeID)
		if err != nil {
			return nil, nil, fmt.Errorf("start node not found: %s", startNodeID)
		}
	}
	startID = resolveCanonicalTraceStart(db, startID)

	var rawEdges []*AstraMapEdge
	if err := db.Select(&rawEdges, `
		SELECT id, source, target, kind, provenance, line, col, COALESCE(metadata, '') AS metadata
		FROM astramap_edges
		WHERE kind = 'calls' AND (source = ? OR target = ?)
		ORDER BY source, target, line, id
	`, startID, startID); err != nil {
		return nil, nil, err
	}

	nodeSet := make(map[string]bool)
	nodeSet[startID] = true
	edgeMap := make(map[string]*AstraMapEdge, len(rawEdges))
	for _, edge := range rawEdges {
		if edge == nil || edge.Source == "" || edge.Target == "" || edge.Source == edge.Target {
			continue
		}
		key := edge.Source + "\x00" + edge.Target
		if _, exists := edgeMap[key]; exists {
			continue
		}
		edgeMap[key] = edge
		nodeSet[edge.Source] = true
		nodeSet[edge.Target] = true
	}

	nodeIDs := make([]string, 0, len(nodeSet))
	for id := range nodeSet {
		nodeIDs = append(nodeIDs, id)
	}

	if len(nodeIDs) == 0 {
		return nil, nil, nil
	}

	// 3. Query matched symbols (nodes)
	query, args, err := sqlx.In("SELECT * FROM astramap_nodes WHERE id IN (?)", nodeIDs)
	if err != nil {
		return nil, nil, err
	}
	query = db.Rebind(query)

	var nodes []*AstraMapNode
	if err := db.Select(&nodes, query, args...); err != nil {
		return nil, nil, err
	}

	filteredEdges := make([]*AstraMapEdge, 0, len(edgeMap))
	for _, edge := range edgeMap {
		filteredEdges = append(filteredEdges, edge)
	}

	nodes, filteredEdges = canonicalizeDuplicateFunctionNodes(nodes, filteredEdges)

	sort.Slice(filteredEdges, func(i, j int) bool {
		if filteredEdges[i].Source != filteredEdges[j].Source {
			return filteredEdges[i].Source < filteredEdges[j].Source
		}
		if filteredEdges[i].Target != filteredEdges[j].Target {
			return filteredEdges[i].Target < filteredEdges[j].Target
		}
		if filteredEdges[i].Line != filteredEdges[j].Line {
			return filteredEdges[i].Line < filteredEdges[j].Line
		}
		return filteredEdges[i].ID < filteredEdges[j].ID
	})

	return nodes, filteredEdges, nil
}

func resolveCanonicalTraceStart(db *sqlx.DB, nodeID string) string {
	var node AstraMapNode
	if err := db.Get(&node, "SELECT id, kind, name, file_path FROM astramap_nodes WHERE id = ? LIMIT 1", nodeID); err != nil {
		return nodeID
	}
	if node.Kind != "function" && node.Kind != "method" {
		return nodeID
	}

	// Use covering index to quickly find candidate symbols with the same name and file, using the one with the most edges as the canonical start
	var candidates []string
	if err := db.Select(&candidates, `
		SELECT id FROM astramap_nodes
		WHERE kind = ? AND file_path = ? AND name = ?
		ORDER BY id
	`, node.Kind, node.FilePath, node.Name); err != nil || len(candidates) <= 1 {
		return nodeID
	}

	// Calculate incoming degree (callers) only when multiple candidates with the same name exist
	type idDegree struct {
		ID     string `db:"id"`
		Degree int    `db:"degree"`
	}
	var ranked []idDegree
	q, args, err := sqlx.In(`
		SELECT n.id,
		       (SELECT COUNT(*) FROM astramap_edges e WHERE e.kind = 'calls' AND e.target = n.id) AS degree
		FROM astramap_nodes n
		WHERE n.id IN (?)
		ORDER BY degree DESC, n.id
	`, candidates)
	if err != nil {
		return nodeID
	}
	q = db.Rebind(q)
	if err := db.Select(&ranked, q, args...); err != nil || len(ranked) == 0 || ranked[0].Degree == 0 {
		return nodeID
	}
	return ranked[0].ID
}
