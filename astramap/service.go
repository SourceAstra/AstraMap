package astramap

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
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
			result.Relationships = append(result.Relationships, c.Source+" → "+n.QualifiedName)
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

// QueryTraceCTE returns the call closure centered on startNodeID.
// Traversal expands both callers and callees transitively up to maxDepth,
// then keeps only edges whose endpoints are both inside the closure.
func QueryTraceCTE(db *sqlx.DB, startNodeID string, maxDepth int) ([]*AstraMapNode, []*AstraMapEdge, error) {
	var startID string
	err := db.Get(&startID, "SELECT id FROM astramap_nodes WHERE id = ? LIMIT 1", startNodeID)
	if err != nil {
		err = db.Get(&startID, "SELECT id FROM astramap_nodes WHERE name = ? ORDER BY file_path, start_line LIMIT 1", startNodeID)
		if err != nil {
			return nil, nil, fmt.Errorf("起始节点未找到: %s", startNodeID)
		}
	}
	const maxNodes = 180
	const maxEdges = 500
	if maxDepth <= 0 {
		maxDepth = 3
	}

	visited := map[string]int{startID: 0}
	queue := []string{startID}

	for len(queue) > 0 && len(visited) < maxNodes {
		curr := queue[0]
		queue = queue[1:]

		currDepth := visited[curr]
		if currDepth >= maxDepth {
			continue
		}

		var nextEdges []*AstraMapEdge
		if err := db.Select(&nextEdges, `
			SELECT id, source, target, kind, provenance, line, col, COALESCE(metadata, '') AS metadata
			FROM astramap_edges
			WHERE kind = 'calls' AND (source = ? OR target = ?)
			ORDER BY id
			LIMIT ?
		`, curr, curr, maxEdges); err != nil {
			return nil, nil, err
		}

		for _, e := range nextEdges {
			nextID := ""
			if e.Source == curr {
				nextID = e.Target
			} else if e.Target == curr {
				nextID = e.Source
			}
			if nextID == "" {
				continue
			}
			if _, ok := visited[nextID]; !ok && len(visited) < maxNodes {
				visited[nextID] = currDepth + 1
				queue = append(queue, nextID)
			}
		}
	}

	nodeIDs := make([]string, 0, len(visited))
	for id := range visited {
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

	allowed := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		allowed[n.ID] = true
	}

	queryEdges, argsEdges, err := sqlx.In(`
		SELECT id, source, target, kind, provenance, line, col, COALESCE(metadata, '') AS metadata
		FROM astramap_edges
		WHERE kind = 'calls' AND source IN (?) AND target IN (?)
		ORDER BY id
		LIMIT ?
	`, nodeIDs, nodeIDs, maxEdges)
	if err != nil {
		return nil, nil, err
	}
	queryEdges = db.Rebind(queryEdges)

	var edges []*AstraMapEdge
	if err := db.Select(&edges, queryEdges, argsEdges...); err != nil {
		return nil, nil, err
	}

	filteredEdges := make([]*AstraMapEdge, 0, len(edges))
	for _, e := range edges {
		if allowed[e.Source] && allowed[e.Target] {
			filteredEdges = append(filteredEdges, e)
		}
	}

	return nodes, filteredEdges, nil
}
