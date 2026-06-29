package astramap

import (
	"bufio"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jmoiron/sqlx"
)

//go:embed web/*
var WebStatic embed.FS

// StartStandaloneServer starts a decoupled HTTP server serving both mock-free APIs and AstraMap standalone Web UI.
func StartStandaloneServer(db *sqlx.DB, projectRoot, host string, port int) error {
	go watchProjectFiles(db, projectRoot)

	mux := http.NewServeMux()

	// 1. JSON APIs matching standalone index.html calls (mock-free, no auth)
	mux.HandleFunc("/api/astramap/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status, err := QueryStatus(db)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		statusStr := "ready"
		if status.NodeCount == 0 {
			statusStr = "indexing"
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":             statusStr,
			"database":           "SQLite",
			"totalFiles":         status.FileCount,
			"indexedNodes":       status.NodeCount,
			"indexedEdges":       status.EdgeCount,
			"supportedLanguages": []string{"go", "c", "cpp", "python", "typescript", "java"},
		})
	})

	mux.HandleFunc("/api/astramap/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("q")
		kind := r.URL.Query().Get("kind")
		nodes, err := QuerySearch(db, q, kind, 50)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(nodes)
	})

	mux.HandleFunc("/api/astramap/overview", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		data, err := QueryProjectedGraph(db)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(data)
	})

	mux.HandleFunc("/api/astramap/functions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		nodes, err := QueryFunctionList(db)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(nodes)
	})

	mux.HandleFunc("/api/graph/module", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		data, err := QueryModuleGraph(db, r.URL.Query().Get("id"))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(data)
	})

	mux.HandleFunc("/api/astramap/data", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		data, err := QueryGraphData(db)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(data)
	})

	mux.HandleFunc("/api/astramap/node/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := strings.TrimPrefix(r.URL.Path, "/api/astramap/node/")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var node AstraMapNode
		err := db.Get(&node, "SELECT * FROM astramap_nodes WHERE id = ?", id)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "node not found"})
			return
		}
		json.NewEncoder(w).Encode(node)
	})

	mux.HandleFunc("/api/astramap/callers/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := strings.TrimPrefix(r.URL.Path, "/api/astramap/callers/")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ids, resolveErr := ResolveSymbolToIDs(db, id)
		if resolveErr != nil || len(ids) == 0 {
			json.NewEncoder(w).Encode([]struct{}{})
			return
		}
		var allCallers []*AstraMapEdge
		for _, nid := range ids {
			callers, err := GetCallers(db, nid)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			allCallers = append(allCallers, callers...)
		}
		json.NewEncoder(w).Encode(allCallers)
	})

	mux.HandleFunc("/api/astramap/callees/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := strings.TrimPrefix(r.URL.Path, "/api/astramap/callees/")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ids, resolveErr := ResolveSymbolToIDs(db, id)
		if resolveErr != nil || len(ids) == 0 {
			json.NewEncoder(w).Encode([]struct{}{})
			return
		}
		var allCallees []*AstraMapEdge
		for _, nid := range ids {
			callees, err := GetCallees(db, nid)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			allCallees = append(allCallees, callees...)
		}
		json.NewEncoder(w).Encode(allCallees)
	})

	mux.HandleFunc("/api/astramap/impact/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := strings.TrimPrefix(r.URL.Path, "/api/astramap/impact/")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		depth := 3
		if d := r.URL.Query().Get("depth"); d != "" {
			if v, err := strconv.Atoi(d); err == nil && v > 0 {
				depth = v
			}
		}
		ids, resolveErr := ResolveSymbolToIDs(db, id)
		if resolveErr != nil || len(ids) == 0 {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "symbol not found"})
			return
		}
		res, err := AnalyzeImpact(db, ids[0], depth)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(res)
	})

	mux.HandleFunc("/api/astramap/explore", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("q")
		maxFiles := 15
		if m := r.URL.Query().Get("maxFiles"); m != "" {
			if v, err := strconv.Atoi(m); err == nil && v > 0 {
				maxFiles = v
			}
		}
		result, err := QueryExplore(db, q, projectRoot, maxFiles)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(result)
	})

	mux.HandleFunc("/api/astramap/trace", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		from := r.URL.Query().Get("from")
		to := r.URL.Query().Get("to")
		if from == "" || to == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "parameters from and to required"})
			return
		}
		fromIDs, resolveErr := ResolveSymbolToIDs(db, from)
		if resolveErr != nil || len(fromIDs) == 0 {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "from symbol not found"})
			return
		}
		toIDs, resolveErr := ResolveSymbolToIDs(db, to)
		if resolveErr != nil || len(toIDs) == 0 {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "to symbol not found"})
			return
		}
		paths, err := TracePath(db, fromIDs[0], toIDs[0])
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(paths)
	})

	// 2. Mock-free standard APIs to support trace.js calls directly
	mux.HandleFunc("/api/trace", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		nodeID := r.URL.Query().Get("node_id")
		depthStr := r.URL.Query().Get("depth")
		depth := 3
		if d, err := strconv.Atoi(depthStr); err == nil && d > 0 {
			depth = d
		}

		if nodeID == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "node_id required"})
			return
		}

		nodes, edges, err := QueryTraceCTE(db, nodeID, depth)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// 必须转换为 trace.js 预期的 from/to 边格式与含有 file 属性的节点格式
		type ResponseEdge struct {
			From string `json:"from"`
			To   string `json:"to"`
		}

		type ResponseNode struct {
			ID            string `json:"id"`
			Kind          string `json:"kind"`
			Type          string `json:"type"`
			Name          string `json:"name"`
			QualifiedName string `json:"qualifiedName"`
			File          string `json:"file"`
			FilePath      string `json:"filePath"`
			StartLine     int    `json:"startLine"`
			Line          int    `json:"line"`
			EndLine       int    `json:"endLine"`
			StartColumn   int    `json:"startColumn"`
			EndColumn     int    `json:"endColumn"`
			Signature     string `json:"signature,omitempty"`
			Docstring     string `json:"docstring,omitempty"`
			IsExported    int    `json:"isExported"`
		}

		respNodes := make([]ResponseNode, 0)
		for _, n := range nodes {
			respNodes = append(respNodes, ResponseNode{
				ID:            n.ID,
				Kind:          n.Kind,
				Type:          n.Kind,
				Name:          n.Name,
				QualifiedName: n.QualifiedName,
				File:          n.FilePath,
				FilePath:      n.FilePath,
				StartLine:     n.StartLine,
				Line:          n.StartLine,
				EndLine:       n.EndLine,
				StartColumn:   n.StartColumn,
				EndColumn:     n.EndColumn,
				Signature:     n.Signature,
				Docstring:     n.Docstring,
				IsExported:    n.IsExported,
			})
		}

		respEdges := make([]ResponseEdge, 0)
		for _, e := range edges {
			respEdges = append(respEdges, ResponseEdge{
				From: e.Source,
				To:   e.Target,
			})
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"nodes": respNodes,
			"links": respEdges,
		})
	})

	mux.HandleFunc("/api/snippet", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		relFile := strings.TrimSpace(r.URL.Query().Get("file"))
		if relFile == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "file required"})
			return
		}
		line := 1
		if v, err := strconv.Atoi(r.URL.Query().Get("line")); err == nil && v > 0 {
			line = v
		}
		count := 120
		if v, err := strconv.Atoi(r.URL.Query().Get("count")); err == nil && v > 0 {
			count = v
		}
		start := line - count/2
		if start < 1 {
			start = 1
		}
		snippet, err := readSnippetLines(projectRoot, relFile, start, count)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"file":    relFile,
			"line":    line,
			"start":   start,
			"snippet": snippet,
		})
	})

	mux.HandleFunc("/api/documents/list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		docType := r.URL.Query().Get("type")
		key := r.URL.Query().Get("key")
		items, err := listStoredDocs(projectRoot, docType, key)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(items)
	})

	mux.HandleFunc("/api/documents/get", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		docType := r.URL.Query().Get("type")
		key := r.URL.Query().Get("key")
		timestamp := r.URL.Query().Get("timestamp")
		doc, err := getStoredDoc(projectRoot, docType, key, timestamp)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(doc)
	})

	mux.HandleFunc("/api/documents/save", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var req struct {
			Type    string `json:"type"`
			Key     string `json:"key"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		doc, err := saveStoredDoc(projectRoot, req.Type, req.Key, req.Content)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(doc)
	})

	mux.HandleFunc("/api/documents/generate", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var req struct {
			Type string `json:"type"`
			Key  string `json:"key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		doc := synthesizeUnderstandingDoc(db, projectRoot, req.Type, req.Key)
		if stored, err := saveStoredDoc(projectRoot, req.Type, req.Key, doc); err == nil {
			json.NewEncoder(w).Encode(stored)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		}
	})

	mux.HandleFunc("/api/modules", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	})

	mux.HandleFunc("/api/complexity/calculate", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var req struct {
			FilePath string `json:"file_path"`
			SymbolID string `json:"symbol_id"`
		}
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
		}
		if req.FilePath == "" {
			req.FilePath = r.URL.Query().Get("file_path")
		}
		if req.SymbolID == "" {
			req.SymbolID = r.URL.Query().Get("symbol_id")
		}
		metrics, err := calculateComplexityMetrics(db, projectRoot, req.FilePath, req.SymbolID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(metrics)
	})

	// 3. Serve Embedded Web Static Assets (Dashboard)
	// WebStatic contains "web/index.html", "web/explore.js", "web/trace.js", etc.
	// We use sub-FS so we can serve the content of "web" directory directly from root "/".
	subFS, err := fs.Sub(WebStatic, "web")
	if err != nil {
		return fmt.Errorf("failed to create sub-FS: %w", err)
	}
	mux.Handle("/", http.FileServer(http.FS(subFS)))

	addr := fmt.Sprintf("%s:%d", host, port)
	fmt.Fprintf(os.Stderr, "[INFO] AstraMap Dashboard 启动 http://%s\n", addr)
	return http.ListenAndServe(addr, loggingMiddleware(mux))
}

func readSnippetLines(projectRoot, relFile string, start, count int) ([]string, error) {
	clean := filepath.Clean(strings.TrimPrefix(relFile, "/"))
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, err
	}
	absFile, err := filepath.Abs(filepath.Join(absRoot, clean))
	if err != nil {
		return nil, err
	}
	if absFile != absRoot && !strings.HasPrefix(absFile, absRoot+string(os.PathSeparator)) {
		return nil, fmt.Errorf("file outside project")
	}

	f, err := os.Open(absFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	lines := make([]string, 0, count)
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 8*1024*1024)
	lineNo := 0
	end := start + count - 1
	for scanner.Scan() {
		lineNo++
		if lineNo < start {
			continue
		}
		if lineNo > end {
			break
		}
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

type storedDoc struct {
	Type      string `json:"type"`
	Key       string `json:"key"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
}

func docStoreDir(projectRoot, docType, key string) string {
	safeType := sanitizeDocPathPart(docType)
	safeKey := sanitizeDocPathPart(key)
	return filepath.Join(projectRoot, ".astramap", "docs", safeType, safeKey)
}

func sanitizeDocPathPart(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "_"
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func listStoredDocs(projectRoot, docType, key string) ([]storedDoc, error) {
	dir := docStoreDir(projectRoot, docType, key)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []storedDoc{}, nil
	}
	if err != nil {
		return nil, err
	}
	docs := make([]storedDoc, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var doc storedDoc
		if json.Unmarshal(data, &doc) == nil {
			docs = append(docs, doc)
		}
	}
	for i, j := 0, len(docs)-1; i < j; i, j = i+1, j-1 {
		docs[i], docs[j] = docs[j], docs[i]
	}
	return docs, nil
}

func getStoredDoc(projectRoot, docType, key, timestamp string) (storedDoc, error) {
	docs, err := listStoredDocs(projectRoot, docType, key)
	if err != nil {
		return storedDoc{}, err
	}
	for _, doc := range docs {
		if timestamp == "" || doc.Timestamp == timestamp {
			return doc, nil
		}
	}
	return storedDoc{}, fmt.Errorf("document not found")
}

func saveStoredDoc(projectRoot, docType, key, content string) (storedDoc, error) {
	timestamp := time.Now().Format("20060102T150405.000000000")
	doc := storedDoc{
		Type:      docType,
		Key:       key,
		Content:   content,
		Timestamp: timestamp,
	}
	dir := docStoreDir(projectRoot, docType, key)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return storedDoc{}, err
	}
	path := filepath.Join(dir, doc.Timestamp+".json")
	for i := 1; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		doc.Timestamp = fmt.Sprintf("%s-%02d", timestamp, i)
		path = filepath.Join(dir, doc.Timestamp+".json")
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return storedDoc{}, err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return storedDoc{}, err
	}
	return doc, nil
}

func synthesizeUnderstandingDoc(db *sqlx.DB, projectRoot, docType, key string) string {
	switch docType {
	case "file":
		return synthesizeFileDoc(db, projectRoot, key)
	case "module":
		return synthesizeModuleDoc(db, projectRoot, key)
	case "project":
		return synthesizeProjectDoc(db, projectRoot)
	default:
		return synthesizeProjectDoc(db, projectRoot)
	}
}

// inferRole infers architectural role from symbol names, docstrings, and exported ratio.
func inferRole(nodes []*AstraMapNode) string {
	roles := map[string]int{
		"请求处理层": 0, "业务服务层": 0, "数据访问层": 0,
		"数据模型层": 0, "通用工具层": 0, "配置管理层": 0,
		"中间件层": 0, "测试辅助层": 0,
	}
	keywordMap := map[string][]string{
		"请求处理层": {"handler", "controller", "router", "route", "serve", "endpoint", "action"},
		"业务服务层": {"service", "query", "process", "execute", "compute", "analyze", "transform"},
		"数据访问层": {"repo", "store", "db", "sql", "dal", "database", "persist", "cache", "dao"},
		"数据模型层": {"model", "entity", "dto", "vo", "schema", "struct", "type", "message"},
		"通用工具层": {"util", "helper", "common", "shared", "format", "convert", "parse", "marshal"},
		"配置管理层": {"config", "setting", "option", "flag", "env", "init", "setup"},
		"中间件层":  {"middleware", "interceptor", "filter", "guard", "auth", "logging"},
		"测试辅助层": {"test", "mock", "stub", "fake", "fixture"},
	}

	exportedCount := 0
	for _, n := range nodes {
		if n.IsExported != 0 {
			exportedCount++
		}
		lower := strings.ToLower(n.Name)
		lowerQN := strings.ToLower(n.QualifiedName)
		lowerDS := strings.ToLower(n.Docstring)
		for role, keywords := range keywordMap {
			for _, kw := range keywords {
				if strings.Contains(lower, kw) || strings.Contains(lowerQN, kw) || strings.Contains(lowerDS, kw) {
					roles[role]++
				}
			}
		}
	}

	bestRole, bestScore := "", 0
	for role, score := range roles {
		if score > bestScore {
			bestScore = score
			bestRole = role
		}
	}
	if bestScore > 0 {
		return bestRole
	}
	if len(nodes) == 0 {
		return "未知"
	}
	exportedRatio := float64(exportedCount) / float64(len(nodes))
	if exportedRatio > 0.7 {
		return "对外接口层"
	}
	if exportedRatio < 0.3 {
		return "内部实现层"
	}
	return "混合模块"
}

// inferRoleForFile infers role for a single file's nodes.
func inferRoleForFile(nodes []*AstraMapNode) string {
	return inferRole(nodes)
}

// inferBriefSummary generates a 1-2 sentence summary from exported function names and docstrings.
func inferBriefSummary(nodes []*AstraMapNode) string {
	var summaries []string
	for _, n := range nodes {
		if n.IsExported == 0 || n.Kind != "function" && n.Kind != "method" {
			continue
		}
		if n.Docstring != "" {
			ds := n.Docstring
			if len(ds) > 60 {
				ds = ds[:57] + "..."
			}
			summaries = append(summaries, fmt.Sprintf("%s: %s", n.Name, ds))
		}
	}
	if len(summaries) > 3 {
		summaries = summaries[:3]
	}
	if len(summaries) == 0 {
		return "该文件定义了若干符号，具体职责需结合调用关系进一步分析。"
	}
	return strings.Join(summaries, "；") + "。"
}

// buildMermaidDepGraph builds a mermaid flowchart from dependency map.
// deps: source -> list of targets. nodeShort maps full path to display name.
func buildMermaidDepGraph(deps map[string][]string, nodeShort map[string]string, title string) string {
	if len(deps) == 0 {
		return ""
	}
	// Count edges per node for pruning
	degree := make(map[string]int)
	for src, tgts := range deps {
		degree[src]++
		for _, tgt := range tgts {
			degree[tgt]++
		}
	}

	// Prune to top-15 nodes by degree if too large
	allNodes := make([]string, 0, len(degree))
	for src := range deps {
		allNodes = append(allNodes, src)
		for _, tgt := range deps[src] {
			if _, ok := degree[tgt]; ok {
				continue // already counted
			}
			allNodes = append(allNodes, tgt)
		}
	}
	// Deduplicate
	seen := make(map[string]bool)
	uniqueNodes := make([]string, 0, len(allNodes))
	for _, n := range allNodes {
		if !seen[n] {
			seen[n] = true
			uniqueNodes = append(uniqueNodes, n)
		}
	}

	if len(uniqueNodes) > 15 {
		sort.Slice(uniqueNodes, func(i, j int) bool {
			return degree[uniqueNodes[i]] > degree[uniqueNodes[j]]
		})
		uniqueNodes = uniqueNodes[:15]
		keepSet := make(map[string]bool)
		for _, n := range uniqueNodes {
			keepSet[n] = true
		}
		prunedDeps := make(map[string][]string)
		for src, tgts := range deps {
			if !keepSet[src] {
				continue
			}
			filtered := make([]string, 0, len(tgts))
			for _, tgt := range tgts {
				if keepSet[tgt] {
					filtered = append(filtered, tgt)
				}
			}
			if len(filtered) > 0 {
				prunedDeps[src] = filtered
			}
		}
		deps = prunedDeps
	}

	var b strings.Builder
	fmt.Fprintf(&b, "```mermaid\nflowchart LR\n")
	for src, tgts := range deps {
		srcLabel := nodeShort[src]
		if srcLabel == "" {
			srcLabel = filepath.Base(src)
		}
		for _, tgt := range tgts {
			tgtLabel := nodeShort[tgt]
			if tgtLabel == "" {
				tgtLabel = filepath.Base(tgt)
			}
			// mermaid node IDs must be alphanumeric; use sanitized version
			srcID := sanitizeMermaidID(src)
			tgtID := sanitizeMermaidID(tgt)
			fmt.Fprintf(&b, "    %s[%s] --> %s[%s]\n", srcID, srcLabel, tgtID, tgtLabel)
		}
	}
	fmt.Fprintf(&b, "```\n")
	return b.String()
}

// sanitizeMermaidID converts a string to a valid mermaid node ID.
func sanitizeMermaidID(s string) string {
	// Use base name + hash of full path for uniqueness
	base := filepath.Base(s)
	base = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, base)
	if len(base) > 20 {
		base = base[:20]
	}
	// Add short hash suffix for uniqueness
	h := 0
	for _, c := range s {
		h = h*31 + int(c)
	}
	return base + "_" + strconv.Itoa(absInt(h%1000))
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// extractCallChains extracts call chains from a symbol using BFS (depth limited).
func extractCallChains(db *sqlx.DB, symbolID string, maxDepth int, maxPaths int) []string {
	if maxDepth <= 0 {
		maxDepth = 2
	}
	if maxPaths <= 0 {
		maxPaths = 8
	}

	type pathItem struct {
		id   string
		path []string
	}
	queue := []pathItem{{id: symbolID, path: []string{symbolID}}}
	visited := make(map[string]bool)
	visited[symbolID] = true
	var results []string

	for len(queue) > 0 && len(results) < maxPaths {
		curr := queue[0]
		queue = queue[1:]
		if len(curr.path) > maxDepth {
			continue
		}
		callees, err := GetCallees(db, curr.id)
		if err != nil {
			continue
		}
		for _, e := range callees {
			if strings.HasPrefix(e.Target, "external:") || e.Target == "" || visited[e.Target] {
				continue
			}
			visited[e.Target] = true
			newPath := append([]string{}, curr.path...)
			newPath = append(newPath, e.Target)
			if len(newPath) >= 2 {
				// Resolve names for display
				var names []string
				for _, id := range newPath {
					var node AstraMapNode
					if db.Get(&node, "SELECT name FROM astramap_nodes WHERE id = ? LIMIT 1", id) == nil {
						names = append(names, node.Name)
					} else {
						names = append(names, id)
					}
				}
				results = append(results, strings.Join(names, " → "))
			}
			if len(newPath) < maxDepth+1 {
				queue = append(queue, pathItem{id: e.Target, path: newPath})
			}
		}
	}
	return results
}

// readStructSource reads source code for a struct/class/interface node, truncated to 30 lines.
func readStructSource(projectRoot string, n *AstraMapNode) string {
	lineCount := n.EndLine - n.StartLine + 1
	endLine := n.EndLine
	truncated := false
	if lineCount > 30 {
		endLine = n.StartLine + 29
		truncated = true
	}
	src, err := ReadSourceRange(projectRoot, n.FilePath, n.StartLine, endLine)
	if err != nil || src == "" {
		return ""
	}
	if truncated {
		src += "\n// ..."
	}
	return src
}

type complexityMetric struct {
	SymbolID               string   `json:"symbol_id"`
	Name                   string   `json:"name"`
	QualifiedName          string   `json:"qualified_name"`
	FilePath               string   `json:"file_path"`
	Language               string   `json:"language"`
	StartLine              int      `json:"start_line"`
	EndLine                int      `json:"end_line"`
	CyclomaticComplexity   int      `json:"cyclomatic_complexity"`
	LinesOfCode            int      `json:"lines_of_code"`
	NestingDepth           int      `json:"nesting_depth"`
	ReturnCount            int      `json:"return_count"`
	BranchCount            int      `json:"branch_count"`
	FanIn                  int      `json:"fan_in"`
	FanOut                 int      `json:"fan_out"`
	CrossModuleCalls       int      `json:"cross_module_calls"`
	PublicInterface        bool     `json:"public_interface"`
	RiskScore              float64  `json:"risk_score"`
	ComplexityReasons      []string `json:"complexity_reasons"`
	DynamicDispatchSignals []string `json:"dynamic_dispatch_signals,omitempty"`
}

type docDirStat struct {
	symbols  int
	exported int
	fanIn    int
	fanOut   int
	role     string
	nodes    []*AstraMapNode
}

func calculateComplexityMetrics(db *sqlx.DB, projectRoot, filePath, symbolID string) ([]complexityMetric, error) {
	var nodes []*AstraMapNode
	if symbolID != "" {
		var n AstraMapNode
		if err := db.Get(&n, "SELECT * FROM astramap_nodes WHERE id = ? LIMIT 1", symbolID); err != nil {
			return nil, err
		}
		nodes = []*AstraMapNode{&n}
	} else if filePath != "" {
		if err := db.Select(&nodes, "SELECT * FROM astramap_nodes WHERE file_path = ? AND kind IN ('function', 'method') ORDER BY start_line", filePath); err != nil {
			return nil, err
		}
	} else {
		if err := db.Select(&nodes, "SELECT * FROM astramap_nodes WHERE kind IN ('function', 'method') ORDER BY file_path, start_line LIMIT 2000"); err != nil {
			return nil, err
		}
	}

	metrics := make([]complexityMetric, 0, len(nodes))
	for _, n := range nodes {
		m := calculateNodeComplexity(db, projectRoot, n)
		metrics = append(metrics, m)
	}
	sort.Slice(metrics, func(i, j int) bool {
		if metrics[i].RiskScore == metrics[j].RiskScore {
			return metrics[i].CyclomaticComplexity > metrics[j].CyclomaticComplexity
		}
		return metrics[i].RiskScore > metrics[j].RiskScore
	})
	return metrics, nil
}

func calculateNodeComplexity(db *sqlx.DB, projectRoot string, n *AstraMapNode) complexityMetric {
	endLine := n.EndLine
	if endLine < n.StartLine {
		endLine = n.StartLine
	}
	src, _ := ReadSourceRange(projectRoot, n.FilePath, n.StartLine, endLine)
	clean := stripCodeNoise(src)
	tokens := scanCodeTokens(clean)
	branchCount := 0
	returnCount := 0
	for _, tok := range tokens {
		switch tok {
		case "if", "elif", "elseif", "for", "foreach", "while", "case", "catch", "except", "when", "guard", "&&", "||", "?":
			branchCount++
		case "return":
			returnCount++
		}
	}
	loc := countEffectiveLOC(clean)
	depth := estimateNestingDepth(clean)
	fanIn, fanOut, cross := graphRiskInputs(db, n)
	public := n.IsExported != 0 || n.Visibility == "public"
	risk := float64(1+branchCount)*2 + float64(loc)/10 + float64(depth)*3 + float64(fanIn)*2 + float64(fanOut) + float64(cross)*2
	if public {
		risk += 5
	}
	return complexityMetric{
		SymbolID:               n.ID,
		Name:                   n.Name,
		QualifiedName:          n.QualifiedName,
		FilePath:               n.FilePath,
		Language:               n.Language,
		StartLine:              n.StartLine,
		EndLine:                n.EndLine,
		CyclomaticComplexity:   1 + branchCount,
		LinesOfCode:            loc,
		NestingDepth:           depth,
		ReturnCount:            returnCount,
		BranchCount:            branchCount,
		FanIn:                  fanIn,
		FanOut:                 fanOut,
		CrossModuleCalls:       cross,
		PublicInterface:        public,
		RiskScore:              risk,
		ComplexityReasons:      complexityReasons(1+branchCount, loc, depth, returnCount, fanIn, fanOut, cross, public),
		DynamicDispatchSignals: dynamicDispatchSignals(src),
	}
}

func stripCodeNoise(src string) string {
	var b strings.Builder
	inLineComment, inBlockComment, inString := false, false, rune(0)
	escaped := false
	runes := []rune(src)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		next := rune(0)
		if i+1 < len(runes) {
			next = runes[i+1]
		}
		if inLineComment {
			if r == '\n' {
				inLineComment = false
				b.WriteRune('\n')
			}
			continue
		}
		if inBlockComment {
			if r == '\n' {
				b.WriteRune('\n')
			}
			if r == '*' && next == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if inString != 0 {
			if r == '\n' {
				b.WriteRune('\n')
			} else {
				b.WriteRune(' ')
			}
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == inString {
				inString = 0
			}
			continue
		}
		if r == '/' && next == '/' {
			inLineComment = true
			i++
			continue
		}
		if r == '/' && next == '*' {
			inBlockComment = true
			i++
			continue
		}
		if r == '#' {
			inLineComment = true
			continue
		}
		if r == '"' || r == '\'' || r == '`' {
			inString = r
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func scanCodeTokens(src string) []string {
	tokens := make([]string, 0)
	var word strings.Builder
	flush := func() {
		if word.Len() == 0 {
			return
		}
		tokens = append(tokens, strings.ToLower(word.String()))
		word.Reset()
	}
	runes := []rune(src)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			word.WriteRune(r)
			continue
		}
		flush()
		if (r == '&' || r == '|') && i+1 < len(runes) && runes[i+1] == r {
			tokens = append(tokens, string([]rune{r, r}))
			i++
		} else if r == '?' {
			tokens = append(tokens, "?")
		}
	}
	flush()
	return tokens
}

func countEffectiveLOC(src string) int {
	count := 0
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "{" || trimmed == "}" {
			continue
		}
		count++
	}
	return count
}

func estimateNestingDepth(src string) int {
	maxDepth, depth := 0, 0
	indentBase := 0
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		for _, r := range trimmed {
			if r == '}' || r == ')' || r == ']' {
				if depth > 0 {
					depth--
				}
			}
		}
		leading := len(line) - len(strings.TrimLeft(line, " \t"))
		if strings.HasSuffix(trimmed, ":") && leading >= indentBase {
			indentBase = leading
			if depth+1 > maxDepth {
				maxDepth = depth + 1
			}
		}
		for _, r := range trimmed {
			if r == '{' {
				depth++
				if depth > maxDepth {
					maxDepth = depth
				}
			}
		}
	}
	return maxDepth
}

func graphRiskInputs(db *sqlx.DB, n *AstraMapNode) (int, int, int) {
	fanIn, fanOut, cross := 0, 0, 0
	dir := filepath.Dir(n.FilePath)
	if callers, err := GetCallers(db, n.ID); err == nil {
		fanIn = len(callers)
		for _, e := range callers {
			var src AstraMapNode
			if db.Get(&src, "SELECT file_path FROM astramap_nodes WHERE id = ? LIMIT 1", e.Source) == nil && filepath.Dir(src.FilePath) != dir {
				cross++
			}
		}
	}
	if callees, err := GetCallees(db, n.ID); err == nil {
		fanOut = len(callees)
		for _, e := range callees {
			var tgt AstraMapNode
			if db.Get(&tgt, "SELECT file_path FROM astramap_nodes WHERE id = ? LIMIT 1", e.Target) == nil && filepath.Dir(tgt.FilePath) != dir {
				cross++
			}
		}
	}
	return fanIn, fanOut, cross
}

func complexityReasons(cc, loc, depth, returns, fanIn, fanOut, cross int, public bool) []string {
	reasons := make([]string, 0, 6)
	if cc > 20 {
		reasons = append(reasons, fmt.Sprintf("圈复杂度极高：%d 个独立路径", cc))
	} else if cc > 10 {
		reasons = append(reasons, fmt.Sprintf("圈复杂度偏高：%d 个独立路径", cc))
	}
	if loc > 120 {
		reasons = append(reasons, fmt.Sprintf("函数体过长：%d 行有效代码", loc))
	}
	if depth > 4 {
		reasons = append(reasons, fmt.Sprintf("嵌套过深：最大深度 %d", depth))
	}
	if returns > 5 {
		reasons = append(reasons, fmt.Sprintf("返回点较多：%d 个 return", returns))
	}
	if fanIn > 10 || fanOut > 10 {
		reasons = append(reasons, fmt.Sprintf("调用面大：扇入 %d，扇出 %d", fanIn, fanOut))
	}
	if cross > 0 {
		reasons = append(reasons, fmt.Sprintf("跨目录耦合：%d 条跨目录调用", cross))
	}
	if public {
		reasons = append(reasons, "公共接口变更影响面更大")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "复杂度、长度和调用面均处于低风险区间")
	}
	return reasons
}

func dynamicDispatchSignals(src string) []string {
	var signals []string
	keywords := []string{"callback", "handler", "listener", "hook", "dispatch", "registry", "register", "vtbl", "vtable", "ops", "interface", "delegate", "lambda", "event"}
	for i, line := range strings.Split(src, "\n") {
		lower := strings.ToLower(line)
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				signals = append(signals, fmt.Sprintf("L%d: %s", i+1, strings.TrimSpace(line)))
				break
			}
		}
		if len(signals) >= 5 {
			break
		}
	}
	return signals
}

func topComplexityMetrics(db *sqlx.DB, projectRoot string, nodes []*AstraMapNode, limit int) []complexityMetric {
	metrics := make([]complexityMetric, 0, len(nodes))
	for _, n := range nodes {
		if n.Kind != "function" && n.Kind != "method" {
			continue
		}
		metrics = append(metrics, calculateNodeComplexity(db, projectRoot, n))
	}
	sort.Slice(metrics, func(i, j int) bool {
		if metrics[i].RiskScore == metrics[j].RiskScore {
			return metrics[i].CyclomaticComplexity > metrics[j].CyclomaticComplexity
		}
		return metrics[i].RiskScore > metrics[j].RiskScore
	})
	if limit > 0 && len(metrics) > limit {
		return metrics[:limit]
	}
	return metrics
}

func renderRiskTable(b *strings.Builder, title string, metrics []complexityMetric) {
	if len(metrics) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## %s\n\n", title)
	fmt.Fprintf(b, "| 函数 | 风险分 | 圈复杂度 | LOC | 嵌套 | 扇入 | 扇出 | 主要原因 |\n|------|--------|----------|-----|------|------|------|----------|\n")
	for _, m := range metrics {
		reason := ""
		if len(m.ComplexityReasons) > 0 {
			reason = m.ComplexityReasons[0]
		}
		fmt.Fprintf(b, "| `%s` | %.1f | %d | %d | %d | %d | %d | %s |\n", m.Name, m.RiskScore, m.CyclomaticComplexity, m.LinesOfCode, m.NestingDepth, m.FanIn, m.FanOut, reason)
	}
}

func renderDynamicDispatchSection(b *strings.Builder, metrics []complexityMetric) {
	type row struct {
		name    string
		signals []string
	}
	rows := make([]row, 0)
	for _, m := range metrics {
		if len(m.DynamicDispatchSignals) > 0 {
			rows = append(rows, row{name: m.Name, signals: m.DynamicDispatchSignals})
		}
	}
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## 函数指针与动态分发线索\n\n")
	limit := len(rows)
	if limit > 8 {
		limit = 8
	}
	for i := 0; i < limit; i++ {
		fmt.Fprintf(b, "### `%s`\n\n", rows[i].name)
		for _, signal := range rows[i].signals {
			fmt.Fprintf(b, "- `%s`\n", signal)
		}
		fmt.Fprintf(b, "\n")
	}
}

func symmetryRisks(nodes []*AstraMapNode) []string {
	pairs := [][2]string{
		{"init", "deinit"}, {"initialize", "shutdown"}, {"start", "stop"}, {"open", "close"},
		{"lock", "unlock"}, {"alloc", "free"}, {"malloc", "free"}, {"create", "destroy"},
		{"new", "delete"}, {"enable", "disable"}, {"register", "unregister"}, {"add", "remove"},
		{"push", "pop"}, {"enter", "exit"}, {"connect", "disconnect"}, {"subscribe", "unsubscribe"},
	}
	names := make(map[string]string)
	for _, n := range nodes {
		if n.Kind == "function" || n.Kind == "method" {
			names[strings.ToLower(n.Name)] = n.Name
		}
	}
	var risks []string
	for _, pair := range pairs {
		leftSeen, rightSeen := false, false
		var leftName, rightName string
		for lower, original := range names {
			if strings.Contains(lower, pair[0]) {
				leftSeen, leftName = true, original
			}
			if strings.Contains(lower, pair[1]) {
				rightSeen, rightName = true, original
			}
		}
		if leftSeen != rightSeen {
			if leftSeen {
				risks = append(risks, fmt.Sprintf("发现 `%s` 语义但未发现对应 `%s`：示例 `%s`", pair[0], pair[1], leftName))
			} else {
				risks = append(risks, fmt.Sprintf("发现 `%s` 语义但未发现对应 `%s`：示例 `%s`", pair[1], pair[0], rightName))
			}
		}
	}
	sort.Strings(risks)
	if len(risks) > 12 {
		return risks[:12]
	}
	return risks
}

func synthesizeFileDoc(db *sqlx.DB, projectRoot, filePath string) string {
	var nodes []*AstraMapNode
	if err := db.Select(&nodes, "SELECT * FROM astramap_nodes WHERE file_path = ? ORDER BY start_line", filePath); err != nil || len(nodes) == 0 {
		return fmt.Sprintf("# 文件理解文档\n\n目标：`%s`\n\n该文件尚未索引或无符号数据。\n", filePath)
	}

	var exported, internal []*AstraMapNode
	var dataStructs []*AstraMapNode
	structKinds := map[string]bool{"struct": true, "class": true, "interface": true, "type": true}
	funcKinds := map[string]bool{"function": true, "method": true}

	for _, n := range nodes {
		if structKinds[n.Kind] {
			dataStructs = append(dataStructs, n)
		}
		if funcKinds[n.Kind] {
			if n.IsExported != 0 {
				exported = append(exported, n)
			} else {
				internal = append(internal, n)
			}
		}
	}

	type symInfo struct {
		node        *AstraMapNode
		fanIn       int
		callerNames []string
		callees     []string
	}
	exportedInfo := make([]symInfo, 0, len(exported))
	extDepFiles := make(map[string]int)
	extDepCallers := make(map[string][]string) // dep file -> caller function names
	incomingDepFiles := make(map[string]int)

	for _, n := range exported {
		info := symInfo{node: n}
		if callers, err := GetCallers(db, n.ID); err == nil {
			info.fanIn = len(callers)
			for _, e := range callers {
				var src AstraMapNode
				if db.Get(&src, "SELECT id, name, file_path FROM astramap_nodes WHERE id = ? LIMIT 1", e.Source) == nil {
					info.callerNames = append(info.callerNames, src.Name)
					if src.FilePath != filePath && src.FilePath != "" {
						incomingDepFiles[src.FilePath]++
					}
				}
			}
			if len(info.callerNames) > 5 {
				info.callerNames = info.callerNames[:5]
			}
		}
		if callees, err := GetCallees(db, n.ID); err == nil {
			for _, e := range callees {
				if e.Target != "" && !strings.HasPrefix(e.Target, "external:") {
					var tgt AstraMapNode
					if db.Get(&tgt, "SELECT file_path, name FROM astramap_nodes WHERE id = ? LIMIT 1", e.Target) == nil {
						if tgt.FilePath != filePath {
							extDepFiles[tgt.FilePath]++
							extDepCallers[tgt.FilePath] = append(extDepCallers[tgt.FilePath], n.Name)
						}
						info.callees = append(info.callees, e.Target)
					}
				}
			}
		}
		exportedInfo = append(exportedInfo, info)
	}
	sort.Slice(exportedInfo, func(i, j int) bool { return exportedInfo[i].fanIn > exportedInfo[j].fanIn })

	lang := ""
	if len(nodes) > 0 {
		lang = nodes[0].Language
	}
	role := inferRole(nodes)
	fileRisk := topComplexityMetrics(db, projectRoot, nodes, 10)

	var b strings.Builder
	fmt.Fprintf(&b, "# 文件理解文档：`%s`\n\n", filePath)

	// 职责定位
	fmt.Fprintf(&b, "## 职责定位\n\n")
	fmt.Fprintf(&b, "**%s** — %s\n\n", role, inferBriefSummary(exported))

	// 概览统计
	fmt.Fprintf(&b, "## 概览统计\n\n")
	fmt.Fprintf(&b, "| 指标 | 值 |\n|------|----|\n")
	fmt.Fprintf(&b, "| 语言 | %s |\n", lang)
	fmt.Fprintf(&b, "| 符号总数 | %d |\n", len(nodes))
	fmt.Fprintf(&b, "| 公共接口 | %d |\n", len(exported))
	fmt.Fprintf(&b, "| 内部函数 | %d |\n", len(internal))
	fmt.Fprintf(&b, "| 数据结构 | %d |\n", len(dataStructs))
	fmt.Fprintf(&b, "| 对外依赖文件 | %d |\n", len(extDepFiles))
	fmt.Fprintf(&b, "| 被依赖文件 | %d |\n", len(incomingDepFiles))
	if len(fileRisk) > 0 {
		fmt.Fprintf(&b, "| 最高风险函数 | `%s`（%.1f） |\n", fileRisk[0].Name, fileRisk[0].RiskScore)
	}

	renderRiskTable(&b, "高复杂度与高风险函数", fileRisk)

	if risks := symmetryRisks(nodes); len(risks) > 0 {
		fmt.Fprintf(&b, "\n## 资源与状态对称性风险\n\n")
		for _, risk := range risks {
			fmt.Fprintf(&b, "- %s\n", risk)
		}
	}

	renderDynamicDispatchSection(&b, fileRisk)

	// 公共接口详解
	if len(exportedInfo) > 0 {
		fmt.Fprintf(&b, "\n## 公共接口详解\n\n")
		limit := len(exportedInfo)
		if limit > 15 {
			limit = 15
		}
		for i := 0; i < limit; i++ {
			info := exportedInfo[i]
			n := info.node
			fmt.Fprintf(&b, "### `%s`\n\n", n.Name)
			sig := n.Signature
			if sig == "" {
				sig = n.Name + "()"
			}
			fmt.Fprintf(&b, "- **签名**：`%s`\n", sig)
			if n.ReturnType != "" {
				fmt.Fprintf(&b, "- **返回类型**：`%s`\n", n.ReturnType)
			}
			if n.Docstring != "" {
				ds := n.Docstring
				if len(ds) > 200 {
					ds = ds[:197] + "..."
				}
				fmt.Fprintf(&b, "- **文档注释**：%s\n", ds)
			}
			fmt.Fprintf(&b, "- **扇入**：%d", info.fanIn)
			if len(info.callerNames) > 0 {
				fmt.Fprintf(&b, " — 调用方：%s", strings.Join(info.callerNames, "、"))
			}
			fmt.Fprintf(&b, "\n")

			chains := extractCallChains(db, n.ID, 2, 8)
			if len(chains) > 0 {
				fmt.Fprintf(&b, "- **调用链**：\n")
				for _, ch := range chains {
					fmt.Fprintf(&b, "  - `%s`\n", ch)
				}
			}
			fmt.Fprintf(&b, "\n")
		}
	}

	// 数据结构定义
	if len(dataStructs) > 0 {
		fmt.Fprintf(&b, "## 数据结构定义\n\n")
		limit := len(dataStructs)
		if limit > 8 {
			limit = 8
		}
		for i := 0; i < limit; i++ {
			n := dataStructs[i]
			fmt.Fprintf(&b, "### `%s` (%s)\n\n", n.Name, n.Kind)
			if projectRoot != "" {
				src := readStructSource(projectRoot, n)
				if src != "" {
					fmt.Fprintf(&b, "```%s\n%s\n```\n\n", lang, src)
				}
			}
			if n.Docstring != "" {
				fmt.Fprintf(&b, "%s\n\n", n.Docstring)
			}
		}
	}

	// 内部函数
	if len(internal) > 0 {
		fmt.Fprintf(&b, "## 内部函数\n\n")
		fmt.Fprintf(&b, "| 函数 | 行号 | 简要签名 |\n|------|------|----------|\n")
		for _, n := range internal {
			sig := n.Signature
			if sig == "" {
				sig = n.Name + "()"
			}
			if len(sig) > 60 {
				sig = sig[:57] + "..."
			}
			fmt.Fprintf(&b, "| `%s` | L%d | `%s` |\n", n.Name, n.StartLine, sig)
		}
	}

	// 依赖关系图 (mermaid)
	if len(extDepFiles) > 0 || len(incomingDepFiles) > 0 {
		deps := make(map[string][]string)
		nodeShort := make(map[string]string)
		nodeShort[filePath] = filepath.Base(filePath)
		for dep := range extDepFiles {
			deps[filePath] = append(deps[filePath], dep)
			nodeShort[dep] = filepath.Base(dep)
		}
		for inc := range incomingDepFiles {
			deps[inc] = append(deps[inc], filePath)
			nodeShort[inc] = filepath.Base(inc)
		}
		mermaid := buildMermaidDepGraph(deps, nodeShort, "依赖关系")
		if mermaid != "" {
			fmt.Fprintf(&b, "\n## 依赖关系图\n\n%s\n", mermaid)
		}
	}

	// 对外依赖
	if len(extDepFiles) > 0 {
		type depInfo struct {
			file    string
			count   int
			callers []string
		}
		deps := make([]depInfo, 0, len(extDepFiles))
		for f, c := range extDepFiles {
			callers := extDepCallers[f]
			deps = append(deps, depInfo{file: f, count: c, callers: callers})
		}
		sort.Slice(deps, func(i, j int) bool { return deps[i].count > deps[j].count })
		fmt.Fprintf(&b, "\n## 对外依赖\n\n")
		fmt.Fprintf(&b, "| 文件 | 调用次数 | 主要调用方 |\n|------|----------|------------|\n")
		for _, d := range deps {
			topCallers := d.callers
			if len(topCallers) > 3 {
				topCallers = topCallers[:3]
			}
			fmt.Fprintf(&b, "| `%s` | %d | %s |\n", filepath.Base(d.file), d.count, strings.Join(topCallers, "、"))
		}
	}

	// 阅读路径
	fmt.Fprintf(&b, "\n## 阅读路径\n\n")
	step := 1
	if len(fileRisk) > 0 {
		top := fileRisk[0]
		fmt.Fprintf(&b, "%d. 先读 **`%s`** — 风险分最高（%.1f），原因：%s\n", step, top.Name, top.RiskScore, strings.Join(top.ComplexityReasons, "；"))
		step++
	}
	if len(exportedInfo) > 0 {
		top := exportedInfo[0].node
		fmt.Fprintf(&b, "%d. 从 **`%s`** 开始 — 该函数扇入最高（%d），是本文件的核心入口\n", step, top.Name, exportedInfo[0].fanIn)
		step++
		if len(exportedInfo) > 1 {
			fmt.Fprintf(&b, "%d. 关注 **`%s`** — 第二高扇入接口，理解次要职责\n", step, exportedInfo[1].node.Name)
			step++
		}
	}
	if len(dataStructs) > 0 {
		fmt.Fprintf(&b, "%d. 理解 **`%s`** — 核心数据结构，掌握数据模型\n", step, dataStructs[0].Name)
		step++
	}
	if len(extDepFiles) > 0 {
		fmt.Fprintf(&b, "%d. 检查依赖图中的外部耦合点 — 共依赖 %d 个外部文件\n", step, len(extDepFiles))
	}

	return b.String()
}

func synthesizeModuleDoc(db *sqlx.DB, projectRoot, dirPath string) string {
	prefix := dirPath + "/"
	var nodes []*AstraMapNode
	if err := db.Select(&nodes, "SELECT * FROM astramap_nodes WHERE file_path LIKE ? ORDER BY file_path, start_line", prefix+"%"); err != nil || len(nodes) == 0 {
		return fmt.Sprintf("# 目录理解文档\n\n目标：`%s`\n\n该目录尚未索引或无符号数据。\n", dirPath)
	}

	type fileInfo struct {
		path        string
		symbolCnt   int
		exportedCnt int
		callerCnt   int
		role        string
	}
	fileMap := make(map[string]*fileInfo)
	var fileOrder []string
	fileNodes := make(map[string][]*AstraMapNode) // per-file nodes for role inference
	for _, n := range nodes {
		fi, ok := fileMap[n.FilePath]
		if !ok {
			fi = &fileInfo{path: n.FilePath}
			fileMap[n.FilePath] = fi
			fileOrder = append(fileOrder, n.FilePath)
		}
		fi.symbolCnt++
		fileNodes[n.FilePath] = append(fileNodes[n.FilePath], n)
		if n.IsExported != 0 {
			fi.exportedCnt++
		}
	}
	// Infer role per file
	for fp, fi := range fileMap {
		fi.role = inferRoleForFile(fileNodes[fp])
	}

	extDeps := make(map[string]int)
	extCallers := make(map[string]int)
	extCallerDetails := make(map[string][]string)   // external dir -> caller symbol names
	extDepDetails := make(map[string][]string)      // external dir -> callee symbol names
	internalDeps := make(map[string]map[string]int) // file -> dep file -> count

	// Track external interface: which exported symbols are called from outside
	type ifaceEntry struct {
		name        string
		callerDir   string
		callerNames []string
	}
	var externalInterfaces []ifaceEntry

	for _, n := range nodes {
		if n.IsExported == 0 {
			continue
		}
		if callees, err := GetCallees(db, n.ID); err == nil {
			for _, e := range callees {
				var tgt AstraMapNode
				if db.Get(&tgt, "SELECT file_path, name FROM astramap_nodes WHERE id = ? LIMIT 1", e.Target) == nil {
					if !strings.HasPrefix(tgt.FilePath, prefix) && tgt.FilePath != "" {
						dir := filepath.Dir(tgt.FilePath)
						extDeps[dir]++
						extDepDetails[dir] = append(extDepDetails[dir], n.Name)
					}
					if strings.HasPrefix(tgt.FilePath, prefix) && tgt.FilePath != n.FilePath {
						if internalDeps[n.FilePath] == nil {
							internalDeps[n.FilePath] = make(map[string]int)
						}
						internalDeps[n.FilePath][tgt.FilePath]++
					}
				}
			}
		}
		if callers, err := GetCallers(db, n.ID); err == nil {
			for _, e := range callers {
				var src AstraMapNode
				if db.Get(&src, "SELECT file_path, name FROM astramap_nodes WHERE id = ? LIMIT 1", e.Source) == nil {
					if !strings.HasPrefix(src.FilePath, prefix) && src.FilePath != "" {
						dir := filepath.Dir(src.FilePath)
						extCallers[dir]++
						extCallerDetails[dir] = append(extCallerDetails[dir], src.Name)
						externalInterfaces = append(externalInterfaces, ifaceEntry{
							name:        n.Name,
							callerDir:   dir,
							callerNames: []string{src.Name},
						})
					}
				}
			}
			fileMap[n.FilePath].callerCnt += len(callers)
		}
	}

	sort.Slice(fileOrder, func(i, j int) bool {
		return fileMap[fileOrder[i]].callerCnt > fileMap[fileOrder[j]].callerCnt
	})

	totalFanIn := 0
	for _, c := range extCallers {
		totalFanIn += c
	}
	totalFanOut := 0
	for _, c := range extDeps {
		totalFanOut += c
	}

	role := inferRole(nodes)
	moduleRisk := topComplexityMetrics(db, projectRoot, nodes, 15)

	var b strings.Builder
	fmt.Fprintf(&b, "# 目录理解文档：`%s`\n\n", dirPath)

	// 职责定位
	fmt.Fprintf(&b, "## 职责定位\n\n")
	fmt.Fprintf(&b, "**%s** — 包含 %d 个文件，%d 个符号，%d 个公共接口\n\n", role, len(fileMap), len(nodes), countExported(nodes))

	// 概览统计
	fmt.Fprintf(&b, "## 概览统计\n\n")
	fmt.Fprintf(&b, "| 指标 | 值 |\n|------|----|\n")
	fmt.Fprintf(&b, "| 文件数 | %d |\n", len(fileMap))
	fmt.Fprintf(&b, "| 符号总数 | %d |\n", len(nodes))
	fmt.Fprintf(&b, "| 公共接口 | %d |\n", countExported(nodes))
	fmt.Fprintf(&b, "| 外部扇入 | %d |\n", totalFanIn)
	fmt.Fprintf(&b, "| 外部扇出 | %d |\n", totalFanOut)
	instabilityDenominator := totalFanIn + totalFanOut
	instability := 0.0
	if instabilityDenominator > 0 {
		instability = float64(totalFanOut) / float64(instabilityDenominator)
	}
	fmt.Fprintf(&b, "| 不稳定度 I=Ce/(Ca+Ce) | %.2f |\n", instability)
	if len(moduleRisk) > 0 {
		fmt.Fprintf(&b, "| 最高风险函数 | `%s`（%.1f） |\n", moduleRisk[0].Name, moduleRisk[0].RiskScore)
	}

	renderRiskTable(&b, "高复杂度与高风险函数", moduleRisk)

	if risks := symmetryRisks(nodes); len(risks) > 0 {
		fmt.Fprintf(&b, "\n## 资源与状态对称性风险\n\n")
		for _, risk := range risks {
			fmt.Fprintf(&b, "- %s\n", risk)
		}
	}

	renderDynamicDispatchSection(&b, moduleRisk)

	// 核心文件
	if len(fileOrder) > 0 {
		fmt.Fprintf(&b, "\n## 核心文件\n\n")
		fmt.Fprintf(&b, "| 文件 | 符号数 | 公共接口 | 被调用 | 职责 |\n|------|--------|----------|--------|------|\n")
		for _, fp := range fileOrder {
			fi := fileMap[fp]
			fmt.Fprintf(&b, "| `%s` | %d | %d | %d | %s |\n", filepath.Base(fp), fi.symbolCnt, fi.exportedCnt, fi.callerCnt, fi.role)
		}
	}

	// 对外接口（外部调用方）
	if len(externalInterfaces) > 0 {
		fmt.Fprintf(&b, "\n## 对外接口（外部调用方）\n\n")
		fmt.Fprintf(&b, "| 接口函数 | 调用方目录 | 调用方符号 |\n|----------|-----------|------------|\n")
		limit := len(externalInterfaces)
		if limit > 15 {
			limit = 15
		}
		for i := 0; i < limit; i++ {
			e := externalInterfaces[i]
			names := e.callerNames
			if len(names) > 3 {
				names = names[:3]
			}
			fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", e.name, e.callerDir, strings.Join(names, "、"))
		}
	}

	// 跨目录调用链 (mermaid)
	if len(extCallers) > 0 || len(extDeps) > 0 {
		deps := make(map[string][]string)
		nodeShort := make(map[string]string)
		dirBase := filepath.Base(dirPath)
		nodeShort[dirPath] = dirBase
		for extDir := range extCallers {
			deps[extDir] = append(deps[extDir], dirPath)
			nodeShort[extDir] = filepath.Base(extDir)
		}
		for extDir := range extDeps {
			deps[dirPath] = append(deps[dirPath], extDir)
			nodeShort[extDir] = filepath.Base(extDir)
		}
		mermaid := buildMermaidDepGraph(deps, nodeShort, "跨目录依赖")
		if mermaid != "" {
			fmt.Fprintf(&b, "\n## 跨目录调用链\n\n%s\n", mermaid)
		}
	}

	// 外部依赖
	if len(extDeps) > 0 {
		type depInfo struct {
			dir     string
			count   int
			callers []string
		}
		deps := make([]depInfo, 0, len(extDeps))
		for d, c := range extDeps {
			callers := extDepDetails[d]
			deps = append(deps, depInfo{dir: d, count: c, callers: callers})
		}
		sort.Slice(deps, func(i, j int) bool { return deps[i].count > deps[j].count })
		fmt.Fprintf(&b, "\n## 外部依赖\n\n")
		fmt.Fprintf(&b, "| 依赖目录 | 调用次数 | 主要调用方 |\n|----------|----------|------------|\n")
		for _, d := range deps {
			topCallers := d.callers
			if len(topCallers) > 3 {
				topCallers = topCallers[:3]
			}
			fmt.Fprintf(&b, "| `%s` | %d | %s |\n", d.dir, d.count, strings.Join(topCallers, "、"))
		}
	}

	if len(extCallers) > 0 || len(extDeps) > 0 {
		type couplingInfo struct {
			dir   string
			ca    int
			ce    int
			total int
		}
		couplings := make([]couplingInfo, 0, len(extCallers)+len(extDeps))
		seen := make(map[string]bool)
		for d := range extCallers {
			seen[d] = true
		}
		for d := range extDeps {
			seen[d] = true
		}
		for d := range seen {
			ca, ce := extCallers[d], extDeps[d]
			couplings = append(couplings, couplingInfo{dir: d, ca: ca, ce: ce, total: ca + ce})
		}
		sort.Slice(couplings, func(i, j int) bool { return couplings[i].total > couplings[j].total })
		fmt.Fprintf(&b, "\n## 最强耦合目录\n\n")
		fmt.Fprintf(&b, "| 目录 | Ca 外部扇入 | Ce 外部扇出 | 合计 |\n|------|--------------|--------------|------|\n")
		limit := len(couplings)
		if limit > 10 {
			limit = 10
		}
		for i := 0; i < limit; i++ {
			c := couplings[i]
			fmt.Fprintf(&b, "| `%s` | %d | %d | %d |\n", c.dir, c.ca, c.ce, c.total)
		}
	}

	// 内部文件依赖 (mermaid)
	if len(internalDeps) > 0 {
		deps := make(map[string][]string)
		nodeShort := make(map[string]string)
		for src, tgts := range internalDeps {
			nodeShort[src] = filepath.Base(src)
			for tgt := range tgts {
				deps[src] = append(deps[src], tgt)
				nodeShort[tgt] = filepath.Base(tgt)
			}
		}
		mermaid := buildMermaidDepGraph(deps, nodeShort, "内部依赖")
		if mermaid != "" {
			fmt.Fprintf(&b, "\n## 内部文件依赖\n\n%s\n", mermaid)
		}
	}

	// 阅读路径
	fmt.Fprintf(&b, "\n## 阅读路径\n\n")
	step := 1
	if len(moduleRisk) > 0 {
		top := moduleRisk[0]
		fmt.Fprintf(&b, "%d. 先读 **`%s`** — 模块内风险分最高（%.1f），原因：%s\n", step, top.Name, top.RiskScore, strings.Join(top.ComplexityReasons, "；"))
		step++
	}
	if len(fileOrder) > 0 {
		fi := fileMap[fileOrder[0]]
		fmt.Fprintf(&b, "%d. 从 **`%s`** 理解模块主入口 — %s，被调用 %d 次\n", step, filepath.Base(fi.path), fi.role, fi.callerCnt)
		step++
	}
	if len(fileOrder) > 1 {
		fi := fileMap[fileOrder[1]]
		fmt.Fprintf(&b, "%d. 阅读 **`%s`** 理解核心逻辑 — %s\n", step, filepath.Base(fi.path), fi.role)
		step++
	}
	if len(externalInterfaces) > 0 {
		fmt.Fprintf(&b, "%d. 参考 **`%s`** 理解对外契约 — 被 `%s` 等外部模块调用\n", step, externalInterfaces[0].name, externalInterfaces[0].callerDir)
	}

	return b.String()
}

func countExported(nodes []*AstraMapNode) int {
	cnt := 0
	for _, n := range nodes {
		if n.IsExported != 0 {
			cnt++
		}
	}
	return cnt
}

func synthesizeProjectDoc(db *sqlx.DB, projectRoot string) string {
	status, _ := QueryStatus(db)
	var nodes []*AstraMapNode
	if err := db.Select(&nodes, "SELECT * FROM astramap_nodes ORDER BY file_path, start_line"); err != nil || len(nodes) == 0 {
		return "# 项目理解文档\n\n项目尚未索引或无符号数据。\n"
	}

	dirStats := make(map[string]*docDirStat)
	for _, n := range nodes {
		dir := filepath.Dir(n.FilePath)
		st, ok := dirStats[dir]
		if !ok {
			st = &docDirStat{}
			dirStats[dir] = st
		}
		st.symbols++
		st.nodes = append(st.nodes, n)
		if n.IsExported != 0 {
			st.exported++
		}
	}

	// Compute cross-directory fanIn/fanOut and inter-directory edges
	dirDeps := make(map[string]map[string]int) // sourceDir -> targetDir -> count
	for _, n := range nodes {
		if n.IsExported == 0 {
			continue
		}
		dir := filepath.Dir(n.FilePath)
		if callees, err := GetCallees(db, n.ID); err == nil {
			for _, e := range callees {
				var tgt AstraMapNode
				if db.Get(&tgt, "SELECT file_path FROM astramap_nodes WHERE id = ? LIMIT 1", e.Target) == nil {
					tgtDir := filepath.Dir(tgt.FilePath)
					if tgtDir != dir && tgtDir != "." && tgt.FilePath != "" {
						dirStats[dir].fanOut++
						if dirDeps[dir] == nil {
							dirDeps[dir] = make(map[string]int)
						}
						dirDeps[dir][tgtDir]++
					}
				}
			}
		}
		if callers, err := GetCallers(db, n.ID); err == nil {
			for _, e := range callers {
				var src AstraMapNode
				if db.Get(&src, "SELECT file_path FROM astramap_nodes WHERE id = ? LIMIT 1", e.Source) == nil {
					srcDir := filepath.Dir(src.FilePath)
					if srcDir != dir && srcDir != "." && src.FilePath != "" {
						dirStats[dir].fanIn++
					}
				}
			}
		}
	}

	// Infer roles per directory
	for _, st := range dirStats {
		st.role = inferRole(st.nodes)
	}
	projectRisk := topComplexityMetrics(db, projectRoot, nodes, 20)

	// Language distribution
	langCount := make(map[string]int)
	var files []*AstraMapFile
	if err := db.Select(&files, "SELECT * FROM astramap_files"); err == nil {
		for _, f := range files {
			langCount[f.Language]++
		}
	}
	langParts := make([]string, 0, len(langCount))
	for lang, cnt := range langCount {
		langParts = append(langParts, fmt.Sprintf("%s: %d 文件", lang, cnt))
	}
	sort.Strings(langParts)
	langStr := strings.Join(langParts, ", ")
	if langStr == "" {
		langStr = "未知"
	}

	dirs := make([]string, 0, len(dirStats))
	for d := range dirStats {
		dirs = append(dirs, d)
	}
	sort.Slice(dirs, func(i, j int) bool { return dirStats[dirs[i]].symbols > dirStats[dirs[j]].symbols })

	// Architecture layering
	type layerEntry struct {
		dir  string
		role string
		st   *docDirStat
	}
	var entryLayer, bizLayer, infraLayer []layerEntry
	for _, d := range dirs {
		st := dirStats[d]
		e := layerEntry{dir: d, role: st.role, st: st}
		if st.fanIn == 0 && st.exported > 0 {
			entryLayer = append(entryLayer, e)
		} else if st.fanOut > 5 {
			infraLayer = append(infraLayer, e)
		} else {
			bizLayer = append(bizLayer, e)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# 项目理解文档\n\n")

	// 项目概览
	fmt.Fprintf(&b, "## 项目概览\n\n")
	fmt.Fprintf(&b, "| 指标 | 值 |\n|------|----|\n")
	if status != nil {
		fmt.Fprintf(&b, "| 总节点数 | %d |\n", status.NodeCount)
		fmt.Fprintf(&b, "| 总边数 | %d |\n", status.EdgeCount)
		fmt.Fprintf(&b, "| 文件数 | %d |\n", status.FileCount)
	}
	fmt.Fprintf(&b, "| 目录数 | %d |\n", len(dirStats))
	fmt.Fprintf(&b, "| 语言分布 | %s |\n", langStr)
	if len(projectRisk) > 0 {
		fmt.Fprintf(&b, "| 最高风险函数 | `%s`（%.1f） |\n", projectRisk[0].Name, projectRisk[0].RiskScore)
	}

	renderRiskTable(&b, "项目级高复杂度与高风险函数", projectRisk)

	if risks := symmetryRisks(nodes); len(risks) > 0 {
		fmt.Fprintf(&b, "\n## 全局资源与状态对称性风险\n\n")
		for _, risk := range risks {
			fmt.Fprintf(&b, "- %s\n", risk)
		}
	}

	renderDynamicDispatchSection(&b, projectRisk)

	// 架构分层
	fmt.Fprintf(&b, "\n## 架构分层\n\n")
	printLayerTable := func(title string, entries []layerEntry) {
		if len(entries) == 0 {
			return
		}
		fmt.Fprintf(&b, "### %s\n\n", title)
		fmt.Fprintf(&b, "| 目录 | 职责 | 符号数 | 公共接口 | 外部扇入 | 外部扇出 |\n|------|------|--------|----------|----------|----------|\n")
		for _, e := range entries {
			fmt.Fprintf(&b, "| `%s` | %s | %d | %d | %d | %d |\n", e.dir, e.role, e.st.symbols, e.st.exported, e.st.fanIn, e.st.fanOut)
		}
		fmt.Fprintf(&b, "\n")
	}
	printLayerTable("入口层（扇入=0，提供项目入口）", entryLayer)
	printLayerTable("业务层（核心业务逻辑）", bizLayer)
	printLayerTable("基础设施层（高扇出，提供通用能力）", infraLayer)

	// 模块概览
	fmt.Fprintf(&b, "## 模块概览\n\n")
	fmt.Fprintf(&b, "| 目录 | 符号数 | 公共接口 | Ca 外部扇入 | Ce 外部扇出 | I | 职责 |\n|------|--------|----------|--------------|--------------|---|------|\n")
	for _, d := range dirs {
		st := dirStats[d]
		denominator := st.fanIn + st.fanOut
		instability := 0.0
		if denominator > 0 {
			instability = float64(st.fanOut) / float64(denominator)
		}
		fmt.Fprintf(&b, "| `%s` | %d | %d | %d | %d | %.2f | %s |\n", d, st.symbols, st.exported, st.fanIn, st.fanOut, instability, st.role)
	}

	violations := architectureBoundaryViolations(dirDeps, dirStats)
	if len(violations) > 0 {
		fmt.Fprintf(&b, "\n## 违反分层的调用\n\n")
		for _, violation := range violations {
			fmt.Fprintf(&b, "- %s\n", violation)
		}
	} else {
		fmt.Fprintf(&b, "\n## 违反分层的调用\n\n未发现明显的高层直接穿透底层或底层反向调用上层。\n")
	}

	// 模块依赖拓扑 (mermaid)
	if len(dirDeps) > 0 {
		deps := make(map[string][]string)
		nodeShort := make(map[string]string)
		for srcDir, tgts := range dirDeps {
			nodeShort[srcDir] = filepath.Base(srcDir)
			for tgtDir := range tgts {
				deps[srcDir] = append(deps[srcDir], tgtDir)
				nodeShort[tgtDir] = filepath.Base(tgtDir)
			}
		}
		mermaid := buildMermaidDepGraph(deps, nodeShort, "模块依赖")
		if mermaid != "" {
			fmt.Fprintf(&b, "\n## 模块依赖拓扑\n\n%s\n", mermaid)
		}
	}

	// 关键调用链路
	var keyChains []string
	for _, e := range entryLayer {
		for _, n := range e.st.nodes {
			if n.IsExported == 0 {
				continue
			}
			chains := extractCallChains(db, n.ID, 3, 5)
			keyChains = append(keyChains, chains...)
			if len(keyChains) >= 5 {
				break
			}
		}
		if len(keyChains) >= 5 {
			break
		}
	}
	if len(keyChains) > 0 {
		fmt.Fprintf(&b, "\n## 关键调用链路\n\n")
		if len(keyChains) > 5 {
			keyChains = keyChains[:5]
		}
		for _, ch := range keyChains {
			fmt.Fprintf(&b, "- `%s`\n", ch)
		}
	}

	// 循环依赖检测
	cycles, err := FindCycles(db, "package")
	fmt.Fprintf(&b, "\n## 循环依赖检测\n\n")
	if err != nil || len(cycles) == 0 {
		fmt.Fprintf(&b, "未检测到包级循环依赖 ✓\n")
	} else {
		fmt.Fprintf(&b, "检测到 %d 条包级循环依赖：\n\n", len(cycles))
		limit := len(cycles)
		if limit > 10 {
			limit = 10
		}
		for i := 0; i < limit; i++ {
			fmt.Fprintf(&b, "- `%s`\n", strings.Join(cycles[i], " → "))
		}
	}

	// 阅读路径
	fmt.Fprintf(&b, "\n## 阅读路径\n\n")
	step := 1
	if len(projectRisk) > 0 {
		top := projectRisk[0]
		fmt.Fprintf(&b, "%d. 先读 **`%s`** — 项目风险分最高（%.1f），原因：%s\n", step, top.Name, top.RiskScore, strings.Join(top.ComplexityReasons, "；"))
		step++
	}
	if len(entryLayer) > 0 {
		e := entryLayer[0]
		mainFunc := findMainFunc(db, e.dir)
		if mainFunc != "" {
			fmt.Fprintf(&b, "%d. 入口：从 **`%s`** 的 **`%s`** 理解请求入口\n", step, e.dir, mainFunc)
		} else {
			fmt.Fprintf(&b, "%d. 入口：从 **`%s`** 理解项目入口 — %s\n", step, e.dir, e.role)
		}
		step++
	}
	if len(keyChains) > 0 {
		fmt.Fprintf(&b, "%d. 主流程：追踪 **`%s`** 调用链，理解核心业务逻辑\n", step, keyChains[0])
		step++
	}
	if len(infraLayer) > 0 {
		fmt.Fprintf(&b, "%d. 基础设施：**`%s`** 提供通用能力，按需查阅\n", step, infraLayer[0].dir)
		step++
	}
	if len(cycles) > 0 {
		fmt.Fprintf(&b, "%d. 注意：存在 %d 条循环依赖，建议优先解耦\n", step, len(cycles))
	}

	return b.String()
}

func architectureBoundaryViolations(dirDeps map[string]map[string]int, dirStats map[string]*docDirStat) []string {
	type boundaryViolation struct {
		text  string
		count int
	}
	var violations []boundaryViolation
	for src, tgts := range dirDeps {
		srcStat := dirStats[src]
		if srcStat == nil {
			continue
		}
		srcRank := architectureLayerRank(src, srcStat.role)
		for tgt, count := range tgts {
			tgtStat := dirStats[tgt]
			if tgtStat == nil {
				continue
			}
			tgtRank := architectureLayerRank(tgt, tgtStat.role)
			if srcRank == 1 && tgtRank == 3 {
				violations = append(violations, boundaryViolation{
					text:  fmt.Sprintf("高层 `%s` 直接调用底层 `%s`（%d 次），建议经由业务/端口层收敛边界。", src, tgt, count),
					count: count,
				})
			}
			if srcRank == 3 && tgtRank == 1 {
				violations = append(violations, boundaryViolation{
					text:  fmt.Sprintf("底层 `%s` 反向调用高层 `%s`（%d 次），存在依赖方向反转风险。", src, tgt, count),
					count: count,
				})
			}
		}
	}
	sort.Slice(violations, func(i, j int) bool { return violations[i].count > violations[j].count })
	limit := len(violations)
	if limit > 12 {
		limit = 12
	}
	result := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		result = append(result, violations[i].text)
	}
	return result
}

func architectureLayerRank(dir, role string) int {
	lower := strings.ToLower(dir + " " + role)
	if strings.Contains(lower, "handler") || strings.Contains(lower, "controller") || strings.Contains(lower, "route") || strings.Contains(lower, "cmd") || strings.Contains(role, "请求") || strings.Contains(role, "入口") || strings.Contains(role, "接口") {
		return 1
	}
	if strings.Contains(lower, "driver") || strings.Contains(lower, "adapter") || strings.Contains(lower, "infra") || strings.Contains(lower, "storage") || strings.Contains(lower, "db") || strings.Contains(lower, "repo") || strings.Contains(role, "数据访问") || strings.Contains(role, "基础") {
		return 3
	}
	return 2
}

// findMainFunc finds a main/init function in a directory.
func findMainFunc(db *sqlx.DB, dir string) string {
	prefix := dir + "/"
	var node AstraMapNode
	if err := db.Get(&node, "SELECT name FROM astramap_nodes WHERE (name = 'main' OR name = 'init') AND file_path LIKE ? AND is_exported = 1 LIMIT 1", prefix+"%"); err == nil {
		return node.Name
	}
	return ""
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: 200}
		next.ServeHTTP(lrw, r)
		fmt.Fprintf(os.Stderr, "[HTTP] %s %s %d %s %s\n", r.RemoteAddr, r.Method, lrw.statusCode, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

func watchProjectFiles(db *sqlx.DB, projectRoot string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WATCH] 文件监控初始化失败: %v\n", err)
		return
	}
	defer watcher.Close()

	skipNames := map[string]bool{
		".git": true, ".astramap": true, "node_modules": true,
		"vendor": true, "build": true, "dist": true, "out": true,
	}
	watchExts := map[string]bool{
		".go": true, ".py": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
		".c": true, ".h": true, ".cpp": true, ".hpp": true, ".cc": true, ".cxx": true,
		".java": true,
	}

	var addDir func(path string)
	addDir = func(path string) {
		entries, err := os.ReadDir(path)
		if err != nil {
			return
		}
		if err := watcher.Add(path); err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() || skipNames[e.Name()] {
				continue
			}
			addDir(filepath.Join(path, e.Name()))
		}
	}
	addDir(projectRoot)
	fmt.Fprintf(os.Stderr, "[WATCH] 文件监控已启动: %s\n", projectRoot)

	debounce := make(map[string]time.Time)
	mu := sync.Mutex{}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Create) || event.Has(fsnotify.Rename) {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					if !skipNames[filepath.Base(event.Name)] {
						watcher.Add(event.Name)
					}
				}
			}
			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) && !event.Has(fsnotify.Remove) && !event.Has(fsnotify.Rename) {
				continue
			}
			ext := strings.ToLower(filepath.Ext(event.Name))
			if !watchExts[ext] && event.Name != "" {
				// Also handle Remove/Rename of directories
				if !event.Has(fsnotify.Remove) && !event.Has(fsnotify.Rename) {
					continue
				}
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					continue
				}
				if ext == "" && !event.Has(fsnotify.Remove) {
					continue
				}
			}
			mu.Lock()
			debounce[event.Name] = time.Now()
			mu.Unlock()
		case <-time.After(2 * time.Second):
			mu.Lock()
			now := time.Now()
			var pending []string
			for name, t := range debounce {
				if now.Sub(t) > 800*time.Millisecond {
					pending = append(pending, name)
				}
			}
			for _, name := range pending {
				delete(debounce, name)
			}
			mu.Unlock()

			for _, name := range pending {
				relPath, _ := filepath.Rel(projectRoot, name)
				changed, err := SyncFileAstraMap(db, projectRoot, name)
				if err != nil {
					fmt.Fprintf(os.Stderr, "[WATCH] 同步失败 %s: %v\n", relPath, err)
					continue
				}
				if changed {
					fmt.Fprintf(os.Stderr, "[WATCH] 已同步 %s\n", relPath)
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "[WATCH] 监控错误: %v\n", err)
		}
	}
}
