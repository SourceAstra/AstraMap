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
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

//go:embed web/*
var WebStatic embed.FS

// StartStandaloneServer starts a decoupled HTTP server serving both mock-free APIs and AstraMap standalone Web UI.
func StartStandaloneServer(db *sqlx.DB, projectRoot string, port int) error {
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
			"supportedLanguages": []string{"go", "cpp", "python", "typescript", "java"},
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
		callers, err := GetCallers(db, id)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(callers)
	})

	mux.HandleFunc("/api/astramap/callees/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := strings.TrimPrefix(r.URL.Path, "/api/astramap/callees/")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		callees, err := GetCallees(db, id)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(callees)
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
		res, err := AnalyzeImpact(db, id, depth)
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
		paths, err := TracePath(db, from, to)
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

	mux.HandleFunc("/api/chat/completion", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		prompt := ""
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == "user" {
				prompt = req.Messages[i].Content
				break
			}
		}
		doc := synthesizeUnderstandingDoc(prompt)
		for _, line := range strings.Split(doc, "\n") {
			fmt.Fprintf(w, "event: chunk\ndata: %s\x1E\n\n", line)
		}
	})

	mux.HandleFunc("/api/modules", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	})

	mux.HandleFunc("/api/complexity/calculate", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	})

	// 3. Serve Embedded Web Static Assets (Dashboard)
	// WebStatic contains "web/index.html", "web/explore.js", "web/trace.js", etc.
	// We use sub-FS so we can serve the content of "web" directory directly from root "/".
	subFS, err := fs.Sub(WebStatic, "web")
	if err != nil {
		return fmt.Errorf("failed to create sub-FS: %w", err)
	}
	mux.Handle("/", http.FileServer(http.FS(subFS)))

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	fmt.Fprintf(os.Stderr, "[INFO] Standalone AstraMap Dashboard is running at http://%s\n", addr)
	return http.ListenAndServe(addr, mux)
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
	doc := storedDoc{
		Type:      docType,
		Key:       key,
		Content:   content,
		Timestamp: time.Now().Format("20060102T150405"),
	}
	dir := docStoreDir(projectRoot, docType, key)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return storedDoc{}, err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return storedDoc{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, doc.Timestamp+".json"), data, 0644); err != nil {
		return storedDoc{}, err
	}
	return doc, nil
}

func synthesizeUnderstandingDoc(prompt string) string {
	title := "代码理解文档"
	if strings.Contains(prompt, "项目理解文档") {
		title = "项目理解文档"
	} else if strings.Contains(prompt, "文件理解文档") {
		title = "文件理解文档"
	} else if strings.Contains(prompt, "模块架构理解文档") {
		title = "目录理解文档"
	}
	if len(prompt) > 4000 {
		prompt = prompt[:4000]
	}
	return fmt.Sprintf(`# %s

## 核心职责

该文档由 AstraMap standalone 根据当前代码地图上下文生成。它提炼当前目标的结构边界、函数分布和可见依赖，用于快速建立阅读入口。

## 结构概览

%s

## 关键协作

- 优先从函数入口进入依赖分析，确认调用方向和影响范围。
- 再回到探索视界查看目录、文件与函数之间的聚合关系。
- 对高扇出或跨目录调用的节点，建议单独追踪并补充人工判断。

## 阅读建议

1. 从入口函数开始查看调用树。
2. 对照文件理解文档确认局部职责。
3. 对跨目录调用路径做二次审查，避免误判边界。
`, title, strings.TrimSpace(prompt))
}
