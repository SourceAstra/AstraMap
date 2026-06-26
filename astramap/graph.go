package astramap

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"
)

// ===== 图查询数据结构 =====

type ImpactResult struct {
	RootSymbolID  string                `json:"rootSymbolId"`
	AffectedNodes []AffectedNodeSummary `json:"affectedNodes"`
}

type AffectedNodeSummary struct {
	SymbolID    string `json:"symbolId"`
	ImpactLevel string `json:"impactLevel"` // HIGH, MEDIUM, LOW
	Reason      string `json:"reason"`
}

type CouplingMetrics struct {
	Ca int `json:"ca"` // 输入耦合度 (Afferent Coupling)
	Ce int `json:"ce"` // 输出耦合度 (Efferent Coupling)
}

type CodeOwner struct {
	Author      string  `json:"author"`
	CommitCount int     `json:"commitCount"`
	Percent     float64 `json:"percent"`
}

// ===== 图遍历引擎 =====

// GetCallers 查找指定符号的直接上游调用者
func GetCallers(db *sqlx.DB, symbolID string) ([]*AstraMapEdge, error) {
	var edges []*AstraMapEdge
	err := db.Select(&edges, "SELECT * FROM astramap_edges WHERE target = ? AND kind = 'calls'", symbolID)
	return edges, err
}

// GetCallees 查找指定符号的直接下游被调用者
func GetCallees(db *sqlx.DB, symbolID string) ([]*AstraMapEdge, error) {
	var edges []*AstraMapEdge
	err := db.Select(&edges, "SELECT * FROM astramap_edges WHERE source = ? AND kind = 'calls'", symbolID)
	return edges, err
}

// AnalyzeImpact 变更影响分析：递归追溯所有上游调用者并计算受损系数
func AnalyzeImpact(db *sqlx.DB, symbolID string, maxDepth int) (*ImpactResult, error) {
	if maxDepth <= 0 {
		maxDepth = 3
	}

	visited := make(map[string]int) // symbolID -> depth
	queue := []string{symbolID}
	visited[symbolID] = 0

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		currDepth := visited[curr]
		if currDepth >= maxDepth {
			continue
		}

		callers, err := GetCallers(db, curr)
		if err != nil {
			return nil, err
		}

		for _, edge := range callers {
			if _, ok := visited[edge.Source]; !ok {
				visited[edge.Source] = currDepth + 1
				queue = append(queue, edge.Source)
			}
		}
	}

	result := &ImpactResult{
		RootSymbolID:  symbolID,
		AffectedNodes: []AffectedNodeSummary{},
	}

	for sym, depth := range visited {
		if sym == symbolID {
			continue
		}
		level := "LOW"
		if depth == 1 {
			level = "HIGH"
		} else if depth == 2 {
			level = "MEDIUM"
		}

		result.AffectedNodes = append(result.AffectedNodes, AffectedNodeSummary{
			SymbolID:    sym,
			ImpactLevel: level,
			Reason:      fmt.Sprintf("在图层 %d 中通过调用链路受到影响", depth),
		})
	}

	return result, nil
}

// TracePath 查找从起始符号 A 到目标符号 B 的最短调用路径 (单向 BFS, 正向 callees 搜索)
func TracePath(db *sqlx.DB, fromID, toID string) ([][]string, error) {
	type PathNode struct {
		curr string
		path []string
	}

	var results [][]string
	visited := make(map[string]bool)
	queue := []PathNode{{curr: fromID, path: []string{fromID}}}
	visited[fromID] = true

	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]

		if node.curr == toID {
			results = append(results, node.path)
			return results, nil
		}

		callees, err := GetCallees(db, node.curr)
		if err != nil {
			return nil, err
		}

		for _, edge := range callees {
			if !visited[edge.Target] {
				visited[edge.Target] = true
				newPath := append([]string{}, node.path...)
				newPath = append(newPath, edge.Target)
				queue = append(queue, PathNode{curr: edge.Target, path: newPath})
			}
		}
	}

	return results, nil
}

// FindDeadCode 死代码分析：基于已知入口点对整个符号图进行可达性扫描
func FindDeadCode(db *sqlx.DB, entryPoints []string) ([]*AstraMapNode, error) {
	if len(entryPoints) == 0 {
		var mains []string
		_ = db.Select(&mains, "SELECT id FROM astramap_nodes WHERE name = 'main' OR kind = 'route'")
		entryPoints = mains
	}

	alive := make(map[string]bool)
	queue := append([]string{}, entryPoints...)
	for _, ep := range entryPoints {
		alive[ep] = true
	}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		callees, err := GetCallees(db, curr)
		if err != nil {
			return nil, err
		}

		for _, edge := range callees {
			if !alive[edge.Target] {
				alive[edge.Target] = true
				queue = append(queue, edge.Target)
			}
		}
	}

	var allFuncs []*AstraMapNode
	err := db.Select(&allFuncs, "SELECT * FROM astramap_nodes WHERE kind IN ('function', 'method')")
	if err != nil {
		return nil, err
	}

	var dead []*AstraMapNode
	for _, f := range allFuncs {
		if !alive[f.ID] {
			dead = append(dead, f)
		}
	}

	return dead, nil
}

// FindCycles 循环依赖检测：查找导入（imports）引用之间的环路。
// level 控制检测粒度：
//   - "package": 按 filepath.Dir 分组，检测目录（包）级循环依赖
//   - "file" (默认): 按文件路径检测文件级循环依赖
func FindCycles(db *sqlx.DB, level string) ([][]string, error) {
	if level == "" {
		level = "file"
	}

	var edges []struct {
		Source string `db:"source"`
		Target string `db:"target"`
	}

	err := db.Select(&edges, "SELECT source, target FROM astramap_edges WHERE kind = 'imports'")
	if err != nil {
		return nil, err
	}

	// Build adjacency list with optional directory-level grouping
	adj := make(map[string]map[string]bool)
	nodeKey := func(raw string) string {
		if level == "package" {
			return filepath.Dir(raw)
		}
		return raw
	}

	for _, edge := range edges {
		src := nodeKey(edge.Source)
		tgt := nodeKey(edge.Target)
		if src == tgt {
			continue // skip self-loops after grouping
		}
		if adj[src] == nil {
			adj[src] = make(map[string]bool)
		}
		adj[src][tgt] = true
	}

	// Flatten adjacency set to slice for iteration
	adjList := make(map[string][]string, len(adj))
	for src, targets := range adj {
		for tgt := range targets {
			adjList[src] = append(adjList[src], tgt)
		}
	}

	var cycles [][]string
	visited := make(map[string]bool)
	recStack := make(map[string]bool)
	var path []string

	var dfs func(node string)
	dfs = func(node string) {
		visited[node] = true
		recStack[node] = true
		path = append(path, node)

		for _, neighbor := range adjList[node] {
			if !visited[neighbor] {
				dfs(neighbor)
			} else if recStack[neighbor] {
				cycleStart := -1
				for i, p := range path {
					if p == neighbor {
						cycleStart = i
						break
					}
				}
				if cycleStart != -1 {
					cycle := append([]string{}, path[cycleStart:]...)
					cycle = append(cycle, neighbor)
					cycles = append(cycles, cycle)
				}
			}
		}

		recStack[node] = false
		path = path[:len(path)-1]
	}

	for node := range adjList {
		if !visited[node] {
			dfs(node)
		}
	}

	return cycles, nil
}

// GetCouplingMetrics 架构内聚耦合度分析
func GetCouplingMetrics(db *sqlx.DB, pathPrefix string) (*CouplingMetrics, error) {
	var caCount, ceCount int

	if pathPrefix == "" {
		_ = db.Get(&caCount, "SELECT COUNT(DISTINCT source) FROM astramap_edges WHERE kind = 'imports'")
		_ = db.Get(&ceCount, "SELECT COUNT(DISTINCT target) FROM astramap_edges WHERE kind = 'imports'")
	} else {
		prefix := pathPrefix + "%"
		_ = db.Get(&caCount, `
			SELECT COUNT(DISTINCT source) FROM astramap_edges 
			WHERE kind = 'imports' AND target LIKE ? AND source NOT LIKE ?`, prefix, prefix)

		_ = db.Get(&ceCount, `
			SELECT COUNT(DISTINCT target) FROM astramap_edges 
			WHERE kind = 'imports' AND source LIKE ? AND target NOT LIKE ?`, prefix, prefix)
	}

	return &CouplingMetrics{Ca: caCount, Ce: ceCount}, nil
}

// GetCodeOwners 代码所有权分析：结合 git log 反查历史作者
func GetCodeOwners(db *sqlx.DB, symbolID string, projectRoot string) ([]CodeOwner, error) {
	var filePath string
	err := db.Get(&filePath, "SELECT file_path FROM astramap_nodes WHERE id = ?", symbolID)
	if err != nil || filePath == "" {
		return nil, fmt.Errorf("找不到符号对应文件: %v", err)
	}

	absPath := filepath.Join(projectRoot, filePath)

	cmd := exec.Command("git", "log", "--pretty=format:%an", "--", absPath)
	cmd.Dir = projectRoot
	out, err := cmd.Output()
	if err != nil {
		return []CodeOwner{{Author: "SourceAstra-LLM", CommitCount: 1, Percent: 100.0}}, nil
	}

	authors := strings.Split(string(out), "\n")
	counts := make(map[string]int)
	total := 0
	for _, author := range authors {
		author = strings.TrimSpace(author)
		if author != "" {
			counts[author]++
			total++
		}
	}

	if total == 0 {
		return []CodeOwner{{Author: "SourceAstra-LLM", CommitCount: 1, Percent: 100.0}}, nil
	}

	var owners []CodeOwner
	for name, count := range counts {
		percent := (float64(count) / float64(total)) * 100.0
		owners = append(owners, CodeOwner{
			Author:      name,
			CommitCount: count,
			Percent:     percent,
		})
	}

	sort.Slice(owners, func(i, j int) bool {
		return owners[i].CommitCount > owners[j].CommitCount
	})

	return owners, nil
}
