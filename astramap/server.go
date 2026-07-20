package astramap

import (
	"bufio"
	"compress/gzip"
	"embed"
	"encoding/json"
	"fmt"
	"io"
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

// StartStandaloneServer starts the standalone Web service with built-in Dashboard, supporting HTTP requests and Graph API.
func StartStandaloneServer(db *sqlx.DB, projectRoot, host string, port int) error {
	// Read-write separation: readDB is used for Dashboard queries
	var readDB *sqlx.DB
	readDBPath := filepath.Join(projectRoot, ".astramap", "astramap.db")
	if rdb, err := sqlx.Open("sqlite", readDBPath+"?mode=ro"); err == nil {
		_, _ = rdb.Exec("PRAGMA journal_mode=WAL")
		_, _ = rdb.Exec("PRAGMA synchronous=NORMAL")
		_, _ = rdb.Exec("PRAGMA mmap_size=268435456")
		_, _ = rdb.Exec("PRAGMA cache_size=-65536")
		_, _ = rdb.Exec("PRAGMA temp_store=MEMORY")
		_, _ = rdb.Exec("PRAGMA busy_timeout=5000")
		_, _ = rdb.Exec("PRAGMA query_only=ON")
		rdb.SetMaxOpenConns(8)
		readDB = rdb
		defer rdb.Close()
	} else {
		readDB = db
	}

	mux := http.NewServeMux()

	// 1. JSON APIs matching standalone index.html calls (mock-free, no auth)
	mux.HandleFunc("/api/astramap/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status, err := QueryStatusWithProjectRoot(readDB, projectRoot)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		statusStr := "ready"
		if status.NodeCount == 0 {
			statusStr = "indexing"
		}
		filter, _ := LoadIndexFilter(projectRoot)
		languageRuntime := LanguageRuntimeForProject(projectRoot)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":                        statusStr,
			"database":                      "SQLite",
			"totalFiles":                    status.FileCount,
			"indexedNodes":                  status.NodeCount,
			"indexedEdges":                  status.EdgeCount,
			"dirtyCount":                    status.DirtyCount,
			"dirtyFiles":                    status.DirtyFiles,
			"semanticDirtyCount":            status.SemanticDirtyCount,
			"semanticDirtyFiles":            status.SemanticDirtyFiles,
			"supportedLanguages":            SupportedLanguageIDsForProject(projectRoot),
			"languageCapabilities":          SupportedLanguageCapabilitiesForProject(projectRoot),
			"declaredLanguageCapabilities":  SupportedLanguageCapabilitiesForProject(projectRoot),
			"effectiveLanguageCapabilities": EffectiveLanguageCapabilitiesForProject(readDB, projectRoot),
			"semanticProviders":             SemanticProviderSpecsForProject(projectRoot),
			"projectUnits":                  DetectProjectUnits(projectRoot, SupportedLanguageIDsForProject(projectRoot), filter),
			"syntaxOverlays":                languageRuntime.SyntaxOverlays,
			"languagePackageDiagnostics":    languageRuntime.Diagnostics,
		})
	})

	mux.HandleFunc("/api/astramap/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("q")
		kind := r.URL.Query().Get("kind")
		nodes, err := QuerySearch(readDB, q, kind, 50)
		if err != nil {
			if validateSearchKind(kind) != nil {
				w.WriteHeader(http.StatusBadRequest)
			} else {
				w.WriteHeader(http.StatusInternalServerError)
			}
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(nodes)
	})

	mux.HandleFunc("/api/astramap/overview", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		data, err := QueryProjectedGraph(readDB)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(data)
	})

	mux.HandleFunc("/api/astramap/functions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		nodes, err := QueryFunctionList(readDB)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(nodes)
	})

	mux.HandleFunc("/api/graph/module", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		data, err := QueryModuleGraph(readDB, r.URL.Query().Get("id"))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(data)
	})

	mux.HandleFunc("/api/astramap/data", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		data, err := QueryGraphData(readDB)
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
		if resolvedID, resolveErr := resolvePrimarySymbolID(readDB, id); resolveErr == nil && resolvedID != "" {
			id = resolvedID
		}
		var node AstraMapNode
		err := readDB.Get(&node, "SELECT * FROM astramap_nodes WHERE id = ?", id)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "node not found"})
			return
		}
		node.ID = CanonicalSymbolID(&node)
		json.NewEncoder(w).Encode(node)
	})

	mux.HandleFunc("/api/astramap/callers/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := strings.TrimPrefix(r.URL.Path, "/api/astramap/callers/")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		resolvedID, resolveErr := resolvePrimarySymbolID(readDB, id)
		if resolveErr != nil || resolvedID == "" {
			json.NewEncoder(w).Encode([]struct{}{})
			return
		}
		limit := 100
		if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
			limit = v
		}
		callers, err := GetCallersLimited(readDB, resolvedID, limit)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		nodeIDs := make([]string, 0, len(callers)*2)
		for _, edge := range callers {
			nodeIDs = append(nodeIDs, edge.Source, edge.Target)
		}
		canonicalMap, batchErr := BatchCanonicalSymbolIDs(readDB, nodeIDs)
		if batchErr == nil {
			for _, edge := range callers {
				if val, ok := canonicalMap[edge.Source]; ok {
					edge.Source = val
				}
				if val, ok := canonicalMap[edge.Target]; ok {
					edge.Target = val
				}
			}
		} else {
			for _, edge := range callers {
				edge.Source = CanonicalSymbolIDForNodeID(readDB, edge.Source)
				edge.Target = CanonicalSymbolIDForNodeID(readDB, edge.Target)
			}
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
		resolvedID, resolveErr := resolvePrimarySymbolID(readDB, id)
		if resolveErr != nil || resolvedID == "" {
			json.NewEncoder(w).Encode([]struct{}{})
			return
		}
		callees, err := GetCallees(readDB, resolvedID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		nodeIDs := make([]string, 0, len(callees)*2)
		for _, edge := range callees {
			nodeIDs = append(nodeIDs, edge.Source, edge.Target)
		}
		canonicalMap, batchErr := BatchCanonicalSymbolIDs(readDB, nodeIDs)
		if batchErr == nil {
			for _, edge := range callees {
				if val, ok := canonicalMap[edge.Source]; ok {
					edge.Source = val
				}
				if val, ok := canonicalMap[edge.Target]; ok {
					edge.Target = val
				}
			}
		} else {
			for _, edge := range callees {
				edge.Source = CanonicalSymbolIDForNodeID(readDB, edge.Source)
				edge.Target = CanonicalSymbolIDForNodeID(readDB, edge.Target)
			}
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
			if v, err := strconv.Atoi(d); err == nil {
				depth = normalizeImpactDepth(v)
			}
		}
		resolvedID, resolveErr := resolvePrimarySymbolID(readDB, id)
		if resolveErr != nil || resolvedID == "" {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "symbol not found"})
			return
		}
		res, err := AnalyzeImpact(readDB, resolvedID, depth)
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
		result, err := QueryExplore(readDB, q, projectRoot, maxFiles)
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
		fromIDs, resolveErr := ResolveSymbolToIDs(readDB, from)
		if resolveErr != nil || len(fromIDs) == 0 {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "from symbol not found"})
			return
		}
		toIDs, resolveErr := ResolveSymbolToIDs(readDB, to)
		if resolveErr != nil || len(toIDs) == 0 {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "to symbol not found"})
			return
		}
		paths, err := TracePath(readDB, fromIDs, toIDs)

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

		if resolvedID, resolveErr := resolvePrimarySymbolID(readDB, nodeID); resolveErr == nil && resolvedID != "" {
			nodeID = resolvedID
		}

		nodes, edges, err := QueryTraceCTE(readDB, projectRoot, nodeID, depth)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// 必须转换为 trace.js 预期的 from/to 边格式与含有 file 属性的节点格式
		type ResponseEdge struct {
			From     string `json:"from"`
			To       string `json:"to"`
			Metadata string `json:"metadata,omitempty"`
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
				ID:            CanonicalSymbolID(n),
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

		// 利用已有的 nodes 构建 canonical ID 映射，消除 BatchCanonicalSymbolIDs 的重复 SELECT *
		canonicalIDs := make(map[string]string, len(nodes))
		for _, n := range nodes {
			canonicalIDs[n.ID] = CanonicalSymbolID(n)
		}

		respEdges := make([]ResponseEdge, 0, len(edges))
		for _, e := range edges {
			fromID := canonicalIDs[e.Source]
			if fromID == "" {
				fromID = e.Source
			}
			toID := canonicalIDs[e.Target]
			if toID == "" {
				toID = e.Target
			}
			respEdges = append(respEdges, ResponseEdge{
				From:     fromID,
				To:       toID,
				Metadata: e.Metadata,
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
		doc := synthesizeUnderstandingDoc(readDB, projectRoot, req.Type, req.Key)
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
			FilePath  string   `json:"file_path"`
			SymbolID  string   `json:"symbol_id"`
			SymbolIDs []string `json:"symbol_ids"`
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
		metrics, err := calculateComplexityMetrics(readDB, projectRoot, req.FilePath, req.SymbolID, req.SymbolIDs)
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
	staticHandler := http.FileServer(http.FS(subFS))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setStaticCacheHeaders(w, r.URL.Path)
		staticHandler.ServeHTTP(w, r)
	}))

	addr := fmt.Sprintf("%s:%d", host, port)
	fmt.Fprintf(os.Stderr, "[INFO] AstraMap Dashboard started at http://%s\n", addr)

	srv := &http.Server{
		Addr:         addr,
		Handler:      gzipMiddleware(loggingMiddleware(mux)),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return srv.ListenAndServe()
}

type gzipResponseWriter struct {
	http.ResponseWriter
	io.Writer
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	return w.Writer.Write(b)
}

func setStaticCacheHeaders(w http.ResponseWriter, path string) {
	switch {
	case strings.HasSuffix(path, ".js"), strings.HasSuffix(path, ".css"), strings.HasSuffix(path, ".min.js"):
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	case strings.HasSuffix(path, ".html"), path == "/":
		w.Header().Set("Cache-Control", "no-cache")
	}
}

func shouldGzipResponse(r *http.Request, header http.Header, path string) bool {
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		return false
	}
	if header.Get("Content-Encoding") != "" {
		return false
	}
	if strings.HasPrefix(path, "/api/") {
		return true
	}
	contentType := header.Get("Content-Type")
	if strings.HasPrefix(contentType, "text/") ||
		strings.Contains(contentType, "application/json") ||
		strings.Contains(contentType, "application/javascript") {
		return true
	}
	return strings.HasSuffix(path, ".js") ||
		strings.HasSuffix(path, ".css") ||
		strings.HasSuffix(path, ".html") ||
		strings.HasSuffix(path, ".json") ||
		path == "/"
}

func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !shouldGzipResponse(r, w.Header(), r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Accept-Encoding")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Del("Content-Length")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		next.ServeHTTP(&gzipResponseWriter{ResponseWriter: w, Writer: gz}, r)
	})
}

func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
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
		"RequestHandler": 0, "BusinessService": 0, "DataAccess": 0,
		"DataModel": 0, "Utility": 0, "Configuration": 0,
		"Middleware": 0, "TestHelper": 0,
	}
	keywordMap := map[string][]string{
		"RequestHandler":  {"handler", "controller", "router", "route", "serve", "endpoint", "action"},
		"BusinessService": {"service", "query", "process", "execute", "compute", "analyze", "transform"},
		"DataAccess":      {"repo", "store", "db", "sql", "dal", "database", "persist", "cache", "dao"},
		"DataModel":       {"model", "entity", "dto", "vo", "schema", "struct", "type", "message"},
		"Utility":         {"util", "helper", "common", "shared", "format", "convert", "parse", "marshal"},
		"Configuration":   {"config", "setting", "option", "flag", "env", "init", "setup"},
		"Middleware":      {"middleware", "interceptor", "filter", "guard", "auth", "logging"},
		"TestHelper":      {"test", "mock", "stub", "fake", "fixture"},
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
		return "Unknown"
	}
	exportedRatio := float64(exportedCount) / float64(len(nodes))
	if exportedRatio > 0.7 {
		return "PublicInterface"
	}
	if exportedRatio < 0.3 {
		return "InternalImplementation"
	}
	return "MixedModule"
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
		return "This file defines several symbols; further analysis of call relationships is needed to determine its specific responsibilities."
	}
	return strings.Join(summaries, "; ") + "."
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

func calculateComplexityMetrics(db *sqlx.DB, projectRoot, filePath, symbolID string, symbolIDs []string) ([]complexityMetric, error) {
	var nodes []*AstraMapNode
	if len(symbolIDs) > 0 {
		query, args, err := sqlx.In("SELECT * FROM astramap_nodes WHERE id IN (?)", symbolIDs)
		if err != nil {
			return nil, err
		}
		query = db.Rebind(query)
		if err := db.Select(&nodes, query, args...); err != nil {
			return nil, err
		}
	} else if symbolID != "" {
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
		reasons = append(reasons, fmt.Sprintf("Extremely high cyclomatic complexity: %d independent paths", cc))
	} else if cc > 10 {
		reasons = append(reasons, fmt.Sprintf("High cyclomatic complexity: %d independent paths", cc))
	}
	if loc > 120 {
		reasons = append(reasons, fmt.Sprintf("Excessively long function body: %d lines of effective code", loc))
	}
	if depth > 4 {
		reasons = append(reasons, fmt.Sprintf("Deep nesting: maximum depth %d", depth))
	}
	if returns > 5 {
		reasons = append(reasons, fmt.Sprintf("Multiple return points: %d return statements", returns))
	}
	if fanIn > 10 || fanOut > 10 {
		reasons = append(reasons, fmt.Sprintf("Large call surface: fan-in %d, fan-out %d", fanIn, fanOut))
	}
	if cross > 0 {
		reasons = append(reasons, fmt.Sprintf("Cross-directory coupling: %d cross-directory calls", cross))
	}
	if public {
		reasons = append(reasons, "Public interface changes have broader impact")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "Complexity, length, and call surface are all in low-risk range")
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
	fmt.Fprintf(b, "| Function | Risk Score | Cyclomatic Complexity | LOC | Nesting | Fan-in | Fan-out | Main Reason |\n|------|--------|----------|-----|------|------|------|----------|\n")
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
	fmt.Fprintf(b, "\n## Function Pointer and Dynamic Dispatch Signals\n\n")
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
				risks = append(risks, fmt.Sprintf("Found `%s` semantic but no corresponding `%s`: example `%s`", pair[0], pair[1], leftName))
			} else {
				risks = append(risks, fmt.Sprintf("Found `%s` semantic but no corresponding `%s`: example `%s`", pair[1], pair[0], rightName))
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
		return fmt.Sprintf("# File Understanding Document\n\nTarget: `%s`\n\nThis file has not been indexed or contains no symbol data.\n", filePath)
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
	fmt.Fprintf(&b, "# File Understanding Document: `%s`\n\n", filePath)

	// Responsibility
	fmt.Fprintf(&b, "## Responsibility\n\n")
	fmt.Fprintf(&b, "**%s** — %s\n\n", role, inferBriefSummary(exported))

	// Overview Statistics
	fmt.Fprintf(&b, "## Overview Statistics\n\n")
	fmt.Fprintf(&b, "| Metric | Value |\n|------|----|\n")
	fmt.Fprintf(&b, "| Language | %s |\n", lang)
	fmt.Fprintf(&b, "| Total Symbols | %d |\n", len(nodes))
	fmt.Fprintf(&b, "| Public Interfaces | %d |\n", len(exported))
	fmt.Fprintf(&b, "| Internal Functions | %d |\n", len(internal))
	fmt.Fprintf(&b, "| Data Structures | %d |\n", len(dataStructs))
	fmt.Fprintf(&b, "| External Dependencies | %d |\n", len(extDepFiles))
	fmt.Fprintf(&b, "| Dependent By | %d |\n", len(incomingDepFiles))
	if len(fileRisk) > 0 {
		fmt.Fprintf(&b, "| Highest Risk Function | `%s` (%.1f) |\n", fileRisk[0].Name, fileRisk[0].RiskScore)
	}

	renderRiskTable(&b, "High Complexity and High Risk Functions", fileRisk)

	if risks := symmetryRisks(nodes); len(risks) > 0 {
		fmt.Fprintf(&b, "\n## Resource and State Symmetry Risks\n\n")
		for _, risk := range risks {
			fmt.Fprintf(&b, "- %s\n", risk)
		}
	}

	renderDynamicDispatchSection(&b, fileRisk)

	// Public Interface Details
	if len(exportedInfo) > 0 {
		fmt.Fprintf(&b, "\n## Public Interface Details\n\n")
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
			fmt.Fprintf(&b, "- **Signature**: `%s`\n", sig)
			if n.ReturnType != "" {
				fmt.Fprintf(&b, "- **Return Type**: `%s`\n", n.ReturnType)
			}
			if n.Docstring != "" {
				ds := n.Docstring
				if len(ds) > 200 {
					ds = ds[:197] + "..."
				}
				fmt.Fprintf(&b, "- **Docstring**: %s\n", ds)
			}
			fmt.Fprintf(&b, "- **Fan-in**: %d", info.fanIn)
			if len(info.callerNames) > 0 {
				fmt.Fprintf(&b, " — Callers: %s", strings.Join(info.callerNames, ", "))
			}
			fmt.Fprintf(&b, "\n")

			chains := extractCallChains(db, n.ID, 2, 8)
			if len(chains) > 0 {
				fmt.Fprintf(&b, "- **Call Chains**:\n")
				for _, ch := range chains {
					fmt.Fprintf(&b, "  - `%s`\n", ch)
				}
			}
			fmt.Fprintf(&b, "\n")
		}
	}

	// Data Structure Definitions
	if len(dataStructs) > 0 {
		fmt.Fprintf(&b, "## Data Structure Definitions\n\n")
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

	// Internal Functions
	if len(internal) > 0 {
		fmt.Fprintf(&b, "## Internal Functions\n\n")
		fmt.Fprintf(&b, "| Function | Line | Brief Signature |\n|------|------|----------|\n")
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

	// Dependency Diagram (mermaid)
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
		mermaid := buildMermaidDepGraph(deps, nodeShort, "Dependencies")
		if mermaid != "" {
			fmt.Fprintf(&b, "\n## Dependency Diagram\n\n%s\n", mermaid)
		}
	}

	// External Dependencies
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
		fmt.Fprintf(&b, "\n## External Dependencies\n\n")
		fmt.Fprintf(&b, "| File | Call Count | Main Callers |\n|------|----------|------------|\n")
		for _, d := range deps {
			topCallers := d.callers
			if len(topCallers) > 3 {
				topCallers = topCallers[:3]
			}
			fmt.Fprintf(&b, "| `%s` | %d | %s |\n", filepath.Base(d.file), d.count, strings.Join(topCallers, ", "))
		}
	}

	// Reading Path
	fmt.Fprintf(&b, "\n## Reading Path\n\n")
	step := 1
	if len(fileRisk) > 0 {
		top := fileRisk[0]
		fmt.Fprintf(&b, "%d. Read **`%s`** first — highest risk score (%.1f), reasons: %s\n", step, top.Name, top.RiskScore, strings.Join(top.ComplexityReasons, "; "))
		step++
	}
	if len(exportedInfo) > 0 {
		top := exportedInfo[0].node
		fmt.Fprintf(&b, "%d. Start with **`%s`** — this function has the highest fan-in (%d), making it the core entry point of this file\n", step, top.Name, exportedInfo[0].fanIn)
		step++
		if len(exportedInfo) > 1 {
			fmt.Fprintf(&b, "%d. Focus on **`%s`** — second highest fan-in interface, understand secondary responsibilities\n", step, exportedInfo[1].node.Name)
			step++
		}
	}
	if len(dataStructs) > 0 {
		fmt.Fprintf(&b, "%d. Understand **`%s`** — core data structure, master the data model\n", step, dataStructs[0].Name)
		step++
	}
	if len(extDepFiles) > 0 {
		fmt.Fprintf(&b, "%d. Check external coupling points in the dependency graph — depends on %d external files\n", step, len(extDepFiles))
	}

	return b.String()
}

func synthesizeModuleDoc(db *sqlx.DB, projectRoot, dirPath string) string {
	prefix := dirPath + "/"
	var nodes []*AstraMapNode
	if err := db.Select(&nodes, "SELECT * FROM astramap_nodes WHERE file_path LIKE ? ORDER BY file_path, start_line", prefix+"%"); err != nil || len(nodes) == 0 {
		return fmt.Sprintf("# Directory Understanding Document\n\nTarget: `%s`\n\nThis directory has not been indexed or contains no symbol data.\n", dirPath)
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
	fmt.Fprintf(&b, "# Directory Understanding Document: `%s`\n\n", dirPath)

	// Responsibility
	fmt.Fprintf(&b, "## Responsibility\n\n")
	fmt.Fprintf(&b, "**%s** — contains %d files, %d symbols, %d public interfaces\n\n", role, len(fileMap), len(nodes), countExported(nodes))

	// Overview Statistics
	fmt.Fprintf(&b, "## Overview Statistics\n\n")
	fmt.Fprintf(&b, "| Metric | Value |\n|------|----|\n")
	fmt.Fprintf(&b, "| File Count | %d |\n", len(fileMap))
	fmt.Fprintf(&b, "| Total Symbols | %d |\n", len(nodes))
	fmt.Fprintf(&b, "| Public Interfaces | %d |\n", countExported(nodes))
	fmt.Fprintf(&b, "| External Fan-in | %d |\n", totalFanIn)
	fmt.Fprintf(&b, "| External Fan-out | %d |\n", totalFanOut)
	instabilityDenominator := totalFanIn + totalFanOut
	instability := 0.0
	if instabilityDenominator > 0 {
		instability = float64(totalFanOut) / float64(instabilityDenominator)
	}
	fmt.Fprintf(&b, "| Instability I=Ce/(Ca+Ce) | %.2f |\n", instability)
	if len(moduleRisk) > 0 {
		fmt.Fprintf(&b, "| Highest Risk Function | `%s` (%.1f) |\n", moduleRisk[0].Name, moduleRisk[0].RiskScore)
	}

	renderRiskTable(&b, "High Complexity and High Risk Functions", moduleRisk)

	if risks := symmetryRisks(nodes); len(risks) > 0 {
		fmt.Fprintf(&b, "\n## Resource and State Symmetry Risks\n\n")
		for _, risk := range risks {
			fmt.Fprintf(&b, "- %s\n", risk)
		}
	}

	renderDynamicDispatchSection(&b, moduleRisk)

	// Core Files
	if len(fileOrder) > 0 {
		fmt.Fprintf(&b, "\n## Core Files\n\n")
		fmt.Fprintf(&b, "| File | Symbols | Public Interfaces | Called By | Responsibility |\n|------|--------|----------|--------|------|\n")
		for _, fp := range fileOrder {
			fi := fileMap[fp]
			fmt.Fprintf(&b, "| `%s` | %d | %d | %d | %s |\n", filepath.Base(fp), fi.symbolCnt, fi.exportedCnt, fi.callerCnt, fi.role)
		}
	}

	// External Interfaces (External Callers)
	if len(externalInterfaces) > 0 {
		fmt.Fprintf(&b, "\n## External Interfaces (External Callers)\n\n")
		fmt.Fprintf(&b, "| Interface Function | Caller Directory | Caller Symbols |\n|----------|-----------|------------|\n")
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
			fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", e.name, e.callerDir, strings.Join(names, ", "))
		}
	}

	// Cross-directory call chains (mermaid)
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
		mermaid := buildMermaidDepGraph(deps, nodeShort, "Cross-directory Dependencies")
		if mermaid != "" {
			fmt.Fprintf(&b, "\n## Cross-directory Call Chains\n\n%s\n", mermaid)
		}
	}

	// External Dependencies
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
		fmt.Fprintf(&b, "\n## External Dependencies\n\n")
		fmt.Fprintf(&b, "| Dependency Directory | Call Count | Main Callers |\n|----------|----------|------------|\n")
		for _, d := range deps {
			topCallers := d.callers
			if len(topCallers) > 3 {
				topCallers = topCallers[:3]
			}
			fmt.Fprintf(&b, "| `%s` | %d | %s |\n", d.dir, d.count, strings.Join(topCallers, ", "))
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
		fmt.Fprintf(&b, "\n## Strongest Coupled Directories\n\n")
		fmt.Fprintf(&b, "| Directory | Ca External Fan-in | Ce External Fan-out | Total |\n|------|--------------|--------------|------|\n")
		limit := len(couplings)
		if limit > 10 {
			limit = 10
		}
		for i := 0; i < limit; i++ {
			c := couplings[i]
			fmt.Fprintf(&b, "| `%s` | %d | %d | %d |\n", c.dir, c.ca, c.ce, c.total)
		}
	}

	// Internal file dependencies (mermaid)
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
		mermaid := buildMermaidDepGraph(deps, nodeShort, "Internal Dependencies")
		if mermaid != "" {
			fmt.Fprintf(&b, "\n## Internal File Dependencies\n\n%s\n", mermaid)
		}
	}

	// Reading Path
	fmt.Fprintf(&b, "\n## Reading Path\n\n")
	step := 1
	if len(moduleRisk) > 0 {
		top := moduleRisk[0]
		fmt.Fprintf(&b, "%d. Read **`%s`** first — highest risk score within module (%.1f), reasons: %s\n", step, top.Name, top.RiskScore, strings.Join(top.ComplexityReasons, "; "))
		step++
	}
	if len(fileOrder) > 0 {
		fi := fileMap[fileOrder[0]]
		fmt.Fprintf(&b, "%d. Understand module main entry from **`%s`** — %s, called %d times\n", step, filepath.Base(fi.path), fi.role, fi.callerCnt)
		step++
	}
	if len(fileOrder) > 1 {
		fi := fileMap[fileOrder[1]]
		fmt.Fprintf(&b, "%d. Read **`%s`** to understand core logic — %s\n", step, filepath.Base(fi.path), fi.role)
		step++
	}
	if len(externalInterfaces) > 0 {
		fmt.Fprintf(&b, "%d. Reference **`%s`** to understand external contracts — called by external modules like `%s`\n", step, externalInterfaces[0].name, externalInterfaces[0].callerDir)
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
		return "# Project Understanding Document\n\nProject has not been indexed or contains no symbol data.\n"
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
		langParts = append(langParts, fmt.Sprintf("%s: %d files", lang, cnt))
	}
	sort.Strings(langParts)
	langStr := strings.Join(langParts, ", ")
	if langStr == "" {
		langStr = "Unknown"
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
	fmt.Fprintf(&b, "# Project Understanding Document\n\n")

	// Project Overview
	fmt.Fprintf(&b, "## Project Overview\n\n")
	fmt.Fprintf(&b, "| Metric | Value |\n|------|----|\n")
	if status != nil {
		fmt.Fprintf(&b, "| Total Nodes | %d |\n", status.NodeCount)
		fmt.Fprintf(&b, "| Total Edges | %d |\n", status.EdgeCount)
		fmt.Fprintf(&b, "| File Count | %d |\n", status.FileCount)
	}
	fmt.Fprintf(&b, "| Directory Count | %d |\n", len(dirStats))
	fmt.Fprintf(&b, "| Language Distribution | %s |\n", langStr)
	if len(projectRisk) > 0 {
		fmt.Fprintf(&b, "| Highest Risk Function | `%s` (%.1f) |\n", projectRisk[0].Name, projectRisk[0].RiskScore)
	}

	renderRiskTable(&b, "Project-level High Complexity and High Risk Functions", projectRisk)

	if risks := symmetryRisks(nodes); len(risks) > 0 {
		fmt.Fprintf(&b, "\n## Global Resource and State Symmetry Risks\n\n")
		for _, risk := range risks {
			fmt.Fprintf(&b, "- %s\n", risk)
		}
	}

	renderDynamicDispatchSection(&b, projectRisk)

	// Architecture Layering
	fmt.Fprintf(&b, "\n## Architecture Layering\n\n")
	printLayerTable := func(title string, entries []layerEntry) {
		if len(entries) == 0 {
			return
		}
		fmt.Fprintf(&b, "### %s\n\n", title)
		fmt.Fprintf(&b, "| Directory | Responsibility | Symbols | Public Interfaces | External Fan-in | External Fan-out |\n|------|------|--------|----------|----------|----------|\n")
		for _, e := range entries {
			fmt.Fprintf(&b, "| `%s` | %s | %d | %d | %d | %d |\n", e.dir, e.role, e.st.symbols, e.st.exported, e.st.fanIn, e.st.fanOut)
		}
		fmt.Fprintf(&b, "\n")
	}
	printLayerTable("Entry Layer (Fan-in=0, provides project entry points)", entryLayer)
	printLayerTable("Business Layer (Core business logic)", bizLayer)
	printLayerTable("Infrastructure Layer (High fan-out, provides common capabilities)", infraLayer)

	// Module Overview
	fmt.Fprintf(&b, "## Module Overview\n\n")
	fmt.Fprintf(&b, "| Directory | Symbols | Public Interfaces | Ca External Fan-in | Ce External Fan-out | I | Responsibility |\n|------|--------|----------|--------------|--------------|---|------|\n")
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
		fmt.Fprintf(&b, "\n## Architecture Boundary Violations\n\n")
		for _, violation := range violations {
			fmt.Fprintf(&b, "- %s\n", violation)
		}
	} else {
		fmt.Fprintf(&b, "\n## Architecture Boundary Violations\n\nNo obvious high-level direct penetration to low-level or low-level reverse calls to high-level detected.\n")
	}

	// Module Dependency Topology (mermaid)
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
		mermaid := buildMermaidDepGraph(deps, nodeShort, "Module Dependencies")
		if mermaid != "" {
			fmt.Fprintf(&b, "\n## Module Dependency Topology\n\n%s\n", mermaid)
		}
	}

	// Key Call Chains
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
		fmt.Fprintf(&b, "\n## Key Call Chains\n\n")
		if len(keyChains) > 5 {
			keyChains = keyChains[:5]
		}
		for _, ch := range keyChains {
			fmt.Fprintf(&b, "- `%s`\n", ch)
		}
	}

	// Cycle Detection
	cycles, err := FindCycles(db, "package")
	fmt.Fprintf(&b, "\n## Cycle Detection\n\n")
	if err != nil || len(cycles) == 0 {
		fmt.Fprintf(&b, "No package-level cyclic dependencies detected ✓\n")
	} else {
		fmt.Fprintf(&b, "Detected %d package-level cyclic dependencies:\n\n", len(cycles))
		limit := len(cycles)
		if limit > 10 {
			limit = 10
		}
		for i := 0; i < limit; i++ {
			fmt.Fprintf(&b, "- `%s`\n", strings.Join(cycles[i], " → "))
		}
	}

	// Reading Path
	fmt.Fprintf(&b, "\n## Reading Path\n\n")
	step := 1
	if len(projectRisk) > 0 {
		top := projectRisk[0]
		fmt.Fprintf(&b, "%d. Read **`%s`** first — highest project risk score (%.1f), reasons: %s\n", step, top.Name, top.RiskScore, strings.Join(top.ComplexityReasons, "; "))
		step++
	}
	if len(entryLayer) > 0 {
		e := entryLayer[0]
		mainFunc := findMainFunc(db, e.dir)
		if mainFunc != "" {
			fmt.Fprintf(&b, "%d. Entry: Understand request entry from **`%s`**'s **`%s`**\n", step, e.dir, mainFunc)
		} else {
			fmt.Fprintf(&b, "%d. Entry: Understand project entry from **`%s`** — %s\n", step, e.dir, e.role)
		}
		step++
	}
	if len(keyChains) > 0 {
		fmt.Fprintf(&b, "%d. Main flow: Trace **`%s`** call chain to understand core business logic\n", step, keyChains[0])
		step++
	}
	if len(infraLayer) > 0 {
		fmt.Fprintf(&b, "%d. Infrastructure: **`%s`** provides common capabilities, consult as needed\n", step, infraLayer[0].dir)
		step++
	}
	if len(cycles) > 0 {
		fmt.Fprintf(&b, "%d. Note: %d cyclic dependencies exist, prioritize decoupling\n", step, len(cycles))
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
					text:  fmt.Sprintf("High-level `%s` directly calls low-level `%s` (%d times), suggest converging boundaries through business/port layers.", src, tgt, count),
					count: count,
				})
			}
			if srcRank == 3 && tgtRank == 1 {
				violations = append(violations, boundaryViolation{
					text:  fmt.Sprintf("Low-level `%s` reversely calls high-level `%s` (%d times), dependency direction reversal risk detected.", src, tgt, count),
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
	if strings.Contains(lower, "handler") || strings.Contains(lower, "controller") || strings.Contains(lower, "route") || strings.Contains(lower, "cmd") || strings.Contains(role, "RequestHandler") || strings.Contains(role, "Entry") || strings.Contains(role, "PublicInterface") {
		return 1
	}
	if strings.Contains(lower, "driver") || strings.Contains(lower, "adapter") || strings.Contains(lower, "infra") || strings.Contains(lower, "storage") || strings.Contains(lower, "db") || strings.Contains(lower, "repo") || strings.Contains(role, "DataAccess") || strings.Contains(role, "Infrastructure") {
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
		path := r.URL.Path
		if path == "/" || strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".css") || strings.HasSuffix(path, ".html") || strings.HasSuffix(path, ".min.js") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: 200}
		next.ServeHTTP(lrw, r)
		fmt.Fprintf(os.Stderr, "[HTTP] %s %s %d %s %s\n", r.RemoteAddr, r.Method, lrw.statusCode, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

func watchProjectFiles(db *sqlx.DB, projectRoot string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WATCH] File watcher initialization failed: %v\n", err)
		return
	}
	defer watcher.Close()

	filter, _ := LoadIndexFilter(projectRoot)
	if filter == nil {
		filter = &IndexFilter{}
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
			if !e.IsDir() {
				continue
			}
			childPath := filepath.Join(path, e.Name())
			relPath, _ := filepath.Rel(projectRoot, childPath)
			if !filter.AllowsDir(relPath, StageSyntax) {
				continue
			}
			addDir(childPath)
		}
	}
	addDir(projectRoot)
	fmt.Fprintf(os.Stderr, "[WATCH] File watcher started: %s\n", projectRoot)

	debounce := make(map[string]time.Time)
	mu := sync.Mutex{}

	debounceTmr := time.NewTimer(2 * time.Second)
	resetTimer(debounceTmr, 24*time.Hour)
	debounceTmr.Stop()

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) && !event.Has(fsnotify.Remove) && !event.Has(fsnotify.Rename) {
				continue
			}
			if !IsPotentialSupportedPathForProject(projectRoot, event.Name) && event.Name != "" {
				// Also handle Remove/Rename of directories
				if !event.Has(fsnotify.Remove) && !event.Has(fsnotify.Rename) {
					continue
				}
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					continue
				}
			}
			mu.Lock()
			debounce[event.Name] = time.Now()
			mu.Unlock()
			resetTimer(debounceTmr, 2*time.Second)
		case <-debounceTmr.C:
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

			anyChanged := false
			profile := BuildProjectProfile(projectRoot, filter, StageSyntax)
			for _, name := range pending {
				relPath, _ := filepath.Rel(projectRoot, name)
				changed, err := SyncDirtySyntaxOverlayWithProfile(db, profile, name)
				if err != nil {
					fmt.Fprintf(os.Stderr, "[WATCH] Overlay refresh failed %s: %v\n", relPath, err)
					continue
				}
				if changed {
					fmt.Fprintf(os.Stderr, "[WATCH] Refreshed syntax overlay %s\n", relPath)
					anyChanged = true
				}
			}
			if anyChanged {
				InvalidateOverviewCache()
				fmt.Fprintf(os.Stderr, "[WATCH] Syntax overlay update complete\n")
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "[WATCH] Watch error: %v\n", err)
		}
	}
}
