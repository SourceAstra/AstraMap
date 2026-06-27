package astramap

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"
)

// ===== 共享查询服务层 =====
// 所有查询逻辑的唯一实现，MCP handler 和 REST handler 均通过此层访问数据。

// IndexStatus holds index health metrics.
type IndexStatus struct {
	NodeCount int `json:"node_count" db:"node_count"`
	EdgeCount int `json:"edge_count" db:"edge_count"`
	FileCount int `json:"file_count" db:"file_count"`
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

func QueryGraphData(db *sqlx.DB) (*GraphDataResult, error) {
	var nodes []*AstraMapNode
	if err := db.Select(&nodes, `
		SELECT *
		FROM astramap_nodes
		WHERE kind IN ('function', 'method', 'class', 'struct', 'interface', 'route', 'external')
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

	var files []*AstraMapFile
	if err := db.Select(&files, "SELECT * FROM astramap_files ORDER BY path"); err != nil {
		return nil, err
	}

	return &GraphDataResult{Nodes: nodes, Edges: edges, Files: files}, nil
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

// QuerySearch performs fuzzy symbol search with parameterized queries.
func QuerySearch(db *sqlx.DB, query, kind string, limit int) ([]*AstraMapNode, error) {
	if limit <= 0 {
		limit = 20
	}
	var nodes []*AstraMapNode
	q := "SELECT * FROM astramap_nodes WHERE (name LIKE ? OR qualified_name LIKE ?) "
	params := []interface{}{"%" + query + "%", "%" + query + "%"}
	if kind != "" {
		q += "AND kind = ? "
		params = append(params, kind)
	}
	q += "LIMIT ?"
	params = append(params, limit)
	err := db.Select(&nodes, q, params...)
	return nodes, err
}

// ResolveSymbolToIDs resolves a bare symbol name or partial ID to a list of full node IDs.
// Tries exact id match first, then name/qualified_name matching.
func ResolveSymbolToIDs(db *sqlx.DB, symbol string) ([]string, error) {
	var ids []string
	err := db.Select(&ids, "SELECT id FROM astramap_nodes WHERE id = ?", symbol)
	if err == nil && len(ids) > 0 {
		return ids, nil
	}
	err = db.Select(&ids,
		"SELECT id FROM astramap_nodes WHERE name = ? OR qualified_name LIKE ? LIMIT 20",
		symbol, "%"+symbol+"%")
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// QueryExplore performs FTS5 full-text search + source code + relationships.
// Handles both symbol queries and natural language task descriptions.
func QueryExplore(db *sqlx.DB, query, projectRoot string, maxFiles int) (*ExploreResult, error) {
	if maxFiles <= 0 {
		maxFiles = 10
	}

	terms := strings.Fields(query)
	var matchedNodes []*AstraMapNode
	var err error
	if len(terms) == 0 {
		// Empty query: select top nodes to populate G_DATA skeleton
		err = db.Select(&matchedNodes, "SELECT * FROM astramap_nodes ORDER BY file_path, start_line LIMIT ?", maxFiles)
	} else {
		ftsQuery := strings.Join(terms, " OR ")
		err = db.Select(&matchedNodes, "SELECT * FROM astramap_nodes WHERE id IN (SELECT id FROM astramap_fts WHERE astramap_fts MATCH ?) LIMIT ?", ftsQuery, maxFiles)
	}
	if err != nil {
		return nil, err
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
			result.Relationships = append(result.Relationships, c.Source+" → "+n.ID)
		}
	}

	return result, nil
}

// QueryNodeBySymbol finds nodes by symbol name or file path.
func QueryNodeBySymbol(db *sqlx.DB, symbol, file string) ([]*AstraMapNode, error) {
	var nodes []*AstraMapNode
	if symbol != "" {
		err := db.Select(&nodes, "SELECT * FROM astramap_nodes WHERE qualified_name LIKE ? OR name = ?", "%"+symbol+"%", symbol)
		return nodes, err
	}
	if file != "" {
		err := db.Select(&nodes, "SELECT * FROM astramap_nodes WHERE file_path = ? LIMIT 10", file)
		return nodes, err
	}
	return nodes, nil
}

// QueryStatus returns index health metrics.
func QueryStatus(db *sqlx.DB) (*IndexStatus, error) {
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
	return s, nil
}

// QueryFiles lists indexed files, optionally filtered by path prefix and glob pattern.
func QueryFiles(db *sqlx.DB, pathPrefix, pattern string) ([]*AstraMapFile, error) {
	q := "SELECT * FROM astramap_files "
	var conditions []string
	var params []interface{}
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
	q += " ORDER BY path ASC LIMIT 100"

	var files []*AstraMapFile
	err := db.Select(&files, q, params...)
	return files, err
}

// QueryVerdicts returns governance verdicts for a symbol.
func QueryVerdicts(db *sqlx.DB, symbolID string) ([]*AstraMapVerdict, error) {
	var verdicts []*AstraMapVerdict
	err := db.Select(&verdicts, "SELECT * FROM astramap_verdicts WHERE symbol_id = ?", symbolID)
	return verdicts, err
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

// QueryTraceCTE returns the path subgraph centered on startNodeID.
// It keeps only edges that are on an upstream path into the root or a
// downstream path out of the root. It intentionally avoids the old induced
// subgraph behavior because that pulled in every side-call between visited
// nodes and made common utilities dominate the view.
func QueryTraceCTE(db *sqlx.DB, startNodeID string, maxDepth int) ([]*AstraMapNode, []*AstraMapEdge, error) {
	var startID string
	err := db.Get(&startID, "SELECT id FROM astramap_nodes WHERE id = ? LIMIT 1", startNodeID)
	if err != nil {
		err = db.Get(&startID, "SELECT id FROM astramap_nodes WHERE name = ? ORDER BY file_path, start_line LIMIT 1", startNodeID)
		if err != nil {
			return nil, nil, fmt.Errorf("起始节点未找到: %s", startNodeID)
		}
	}
	startID = resolveCanonicalTraceStart(db, startID)
	const maxNodes = 180
	const maxEdges = 500
	const perNodeFanout = 32
	if maxDepth <= 0 {
		maxDepth = 3
	}

	visited := map[string]int{startID: 0}
	edgeMap := make(map[string]*AstraMapEdge)
	addEdge := func(e *AstraMapEdge) {
		if e == nil || e.Source == "" || e.Target == "" || e.Source == e.Target {
			return
		}
		key := e.Source + "\x00" + e.Target
		if _, exists := edgeMap[key]; !exists {
			edgeMap[key] = e
		}
	}

	type item struct {
		id    string
		depth int
	}
	walk := func(direction string) error {
		queue := []item{{id: startID, depth: 0}}
		seen := map[string]bool{startID: true}
		for len(queue) > 0 && len(visited) < maxNodes && len(edgeMap) < maxEdges {
			curr := queue[0]
			queue = queue[1:]
			if curr.depth >= maxDepth {
				continue
			}

			var nextEdges []*AstraMapEdge
			var err error
			if direction == "up" {
				err = db.Select(&nextEdges, `
					SELECT id, source, target, kind, provenance, line, col, COALESCE(metadata, '') AS metadata
					FROM astramap_edges
					WHERE kind = 'calls' AND target = ?
					ORDER BY provenance = 'scip' DESC, line, id
					LIMIT ?
				`, curr.id, perNodeFanout)
			} else {
				err = db.Select(&nextEdges, `
					SELECT id, source, target, kind, provenance, line, col, COALESCE(metadata, '') AS metadata
					FROM astramap_edges
					WHERE kind = 'calls' AND source = ?
					ORDER BY provenance = 'scip' DESC, line, id
					LIMIT ?
				`, curr.id, perNodeFanout)
			}
			if err != nil {
				return err
			}

			for _, edge := range nextEdges {
				nextID := edge.Target
				if direction == "up" {
					nextID = edge.Source
				}
				if nextID == "" || shouldPruneTraceUtility(db, startID, edge, direction, curr.depth) {
					continue
				}
				addEdge(edge)
				if !seen[nextID] && len(visited) < maxNodes {
					seen[nextID] = true
					visited[nextID] = curr.depth + 1
					queue = append(queue, item{id: nextID, depth: curr.depth + 1})
				}
			}
		}
		return nil
	}

	if err := walk("up"); err != nil {
		return nil, nil, err
	}
	if err := walk("down"); err != nil {
		return nil, nil, err
	}

	nodeSet := make(map[string]bool)
	nodeSet[startID] = true
	for _, edge := range edgeMap {
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

	// 3. 查询命中的 symbols (nodes)
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

func shouldPruneTraceUtility(db *sqlx.DB, rootID string, edge *AstraMapEdge, direction string, currDepth int) bool {
	if edge == nil || direction != "down" || currDepth == 0 {
		return false
	}
	var node AstraMapNode
	if err := db.Get(&node, "SELECT * FROM astramap_nodes WHERE id = ? LIMIT 1", edge.Target); err != nil {
		return false
	}
	name := strings.ToLower(node.Name)
	noisyName := strings.Contains(name, "free") ||
		strings.Contains(name, "malloc") ||
		strings.Contains(name, "memset") ||
		strings.Contains(name, "memcpy") ||
		strings.Contains(name, "lock") ||
		strings.Contains(name, "unlock") ||
		strings.Contains(name, "dbg") ||
		strings.Contains(name, "debug") ||
		strings.Contains(name, "log")
	if !noisyName {
		return false
	}
	var degree int
	_ = db.Get(&degree, "SELECT COUNT(*) FROM astramap_edges WHERE kind = 'calls' AND target = ?", edge.Target)
	return degree >= 8 && edge.Source != rootID
}

func resolveCanonicalTraceStart(db *sqlx.DB, nodeID string) string {
	var node AstraMapNode
	if err := db.Get(&node, "SELECT * FROM astramap_nodes WHERE id = ? LIMIT 1", nodeID); err != nil {
		return nodeID
	}
	if node.Kind != "function" && node.Kind != "method" {
		return nodeID
	}

	var candidates []struct {
		ID     string `db:"id"`
		Degree int    `db:"degree"`
	}
	if err := db.Select(&candidates, `
		SELECT n.id,
		       (
		         SELECT COUNT(*)
		         FROM astramap_edges e
		         WHERE e.kind = 'calls' AND (e.source = n.id OR e.target = n.id)
		       ) AS degree
		FROM astramap_nodes n
		WHERE n.kind = ? AND n.file_path = ? AND n.name = ?
		ORDER BY degree DESC, n.id
	`, node.Kind, node.FilePath, node.Name); err != nil {
		return nodeID
	}
	if len(candidates) == 0 || candidates[0].Degree == 0 {
		return nodeID
	}
	return candidates[0].ID
}
