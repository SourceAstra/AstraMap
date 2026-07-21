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
		lang := normalizeDocLang(r.URL.Query().Get("lang"))
		items, err := listStoredDocs(projectRoot, docType, key, lang)
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
		lang := normalizeDocLang(r.URL.Query().Get("lang"))
		doc, err := getStoredDoc(projectRoot, docType, key, timestamp, lang)
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
			Lang    string `json:"lang"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		doc, err := saveStoredDoc(projectRoot, req.Type, req.Key, req.Content, req.Lang)
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
			Lang string `json:"lang"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		lang := normalizeDocLang(req.Lang)
		doc := synthesizeUnderstandingDoc(readDB, projectRoot, req.Type, req.Key, lang)
		if stored, err := saveStoredDoc(projectRoot, req.Type, req.Key, doc, lang); err == nil {
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
			Lang      string   `json:"lang"`
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
		if req.Lang == "" {
			req.Lang = r.URL.Query().Get("lang")
		}
		lang := normalizeDocLang(req.Lang)
		metrics, err := calculateComplexityMetrics(readDB, projectRoot, req.FilePath, req.SymbolID, req.SymbolIDs, lang)
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
	Lang      string `json:"lang,omitempty"`
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

// docLang returns the stored document's language; legacy docs without a
// language field are treated as English.
func docLang(doc storedDoc) string {
	if doc.Lang == "" {
		return "en"
	}
	return doc.Lang
}

func listStoredDocs(projectRoot, docType, key, lang string) ([]storedDoc, error) {
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
		if json.Unmarshal(data, &doc) != nil {
			continue
		}
		if docLang(doc) != lang {
			continue
		}
		docs = append(docs, doc)
	}
	for i, j := 0, len(docs)-1; i < j; i, j = i+1, j-1 {
		docs[i], docs[j] = docs[j], docs[i]
	}
	return docs, nil
}

func getStoredDoc(projectRoot, docType, key, timestamp, lang string) (storedDoc, error) {
	docs, err := listStoredDocs(projectRoot, docType, key, lang)
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

func saveStoredDoc(projectRoot, docType, key, content, lang string) (storedDoc, error) {
	timestamp := time.Now().Format("20060102T150405.000000000")
	doc := storedDoc{
		Type:      docType,
		Key:       key,
		Content:   content,
		Timestamp: timestamp,
		Lang:      normalizeDocLang(lang),
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

// docPhrases stores every fixed string emitted by understanding documents.
// Column 0 is English, column 1 is Simplified Chinese; docT selects by locale.
var docPhrases = map[string][2]string{
	// File document
	"file_doc_empty":         {"# File Understanding Document\n\nTarget: `%s`\n\nThis file has not been indexed or contains no symbol data.\n", "# 文件理解文档\n\n目标：`%s`\n\n该文件尚未被索引或不包含符号数据。\n"},
	"file_doc_title":         {"# File Understanding Document: `%s`\n\n", "# 文件理解文档：`%s`\n\n"},
	"brief_summary_fallback": {"This file defines several symbols; further analysis of call relationships is needed to determine its specific responsibilities.", "该文件定义了若干符号，需进一步分析调用关系才能确定其具体职责。"},

	// Module document
	"module_doc_empty":      {"# Directory Understanding Document\n\nTarget: `%s`\n\nThis directory has not been indexed or contains no symbol data.\n", "# 目录理解文档\n\n目标：`%s`\n\n该目录尚未被索引或不包含符号数据。\n"},
	"module_doc_title":      {"# Directory Understanding Document: `%s`\n\n", "# 目录理解文档：`%s`\n\n"},
	"module_responsibility": {"**%s** — contains %d files, %d symbols, %d public interfaces\n\n", "**%s** —— 包含 %d 个文件、%d 个符号、%d 个公共接口\n\n"},

	// Project document
	"project_doc_empty": {"# Project Understanding Document\n\nProject has not been indexed or contains no symbol data.\n", "# 项目理解文档\n\n项目尚未被索引或不包含符号数据。\n"},
	"project_doc_title": {"# Project Understanding Document\n\n", "# 项目理解文档\n\n"},

	// Shared sections
	"sec_responsibility":       {"## Responsibility\n\n", "## 职责定位\n\n"},
	"sec_overview":             {"## Overview Statistics\n\n", "## 概览统计\n\n"},
	"table_metric_value":       {"| Metric | Value |\n|------|----|\n", "| 指标 | 数值 |\n|------|----|\n"},
	"row_language":             {"| Language | %s |\n", "| 语言 | %s |\n"},
	"row_total_symbols":        {"| Total Symbols | %d |\n", "| 符号总数 | %d |\n"},
	"row_public_interfaces":    {"| Public Interfaces | %d |\n", "| 公共接口 | %d |\n"},
	"row_internal_functions":   {"| Internal Functions | %d |\n", "| 内部函数 | %d |\n"},
	"row_data_structures":      {"| Data Structures | %d |\n", "| 数据结构 | %d |\n"},
	"row_external_deps":        {"| External Dependencies | %d |\n", "| 外部依赖 | %d |\n"},
	"row_dependent_by":         {"| Dependent By | %d |\n", "| 被依赖方 | %d |\n"},
	"row_highest_risk":         {"| Highest Risk Function | `%s` (%.1f) |\n", "| 最高风险函数 | `%s` (%.1f) |\n"},
	"row_file_count":           {"| File Count | %d |\n", "| 文件数 | %d |\n"},
	"row_ext_fanin":            {"| External Fan-in | %d |\n", "| 外部扇入 | %d |\n"},
	"row_ext_fanout":           {"| External Fan-out | %d |\n", "| 外部扇出 | %d |\n"},
	"row_instability":          {"| Instability I=Ce/(Ca+Ce) | %.2f |\n", "| 不稳定性 I=Ce/(Ca+Ce) | %.2f |\n"},
	"row_total_nodes":          {"| Total Nodes | %d |\n", "| 节点总数 | %d |\n"},
	"row_total_edges":          {"| Total Edges | %d |\n", "| 边总数 | %d |\n"},
	"row_dir_count":            {"| Directory Count | %d |\n", "| 目录数 | %d |\n"},
	"row_lang_dist":            {"| Language Distribution | %s |\n", "| 语言分布 | %s |\n"},
	"lang_files":               {"%s: %d files", "%s：%d 个文件"},
	"lang_unknown":             {"Unknown", "未知"},
	"sec_symmetry":             {"\n## Resource and State Symmetry Risks\n\n", "\n## 资源与状态对称性风险\n\n"},
	"sec_global_symmetry":      {"\n## Global Resource and State Symmetry Risks\n\n", "\n## 全局资源与状态对称性风险\n\n"},
	"dyn_dispatch_title":       {"\n## Function Pointer and Dynamic Dispatch Signals\n\n", "\n## 函数指针与动态派发信号\n\n"},
	"risk_table_title":         {"High Complexity and High Risk Functions", "高复杂度与高风险函数"},
	"risk_table_title_project": {"Project-level High Complexity and High Risk Functions", "项目级高复杂度与高风险函数"},
	"risk_table_header":        {"| Function | Risk Score | Cyclomatic Complexity | LOC | Nesting | Fan-in | Fan-out | Main Reason |\n|------|--------|----------|-----|------|------|------|----------|\n", "| 函数 | 风险分 | 圈复杂度 | 代码行 | 嵌套深度 | 扇入 | 扇出 | 主因 |\n|------|--------|----------|-----|------|------|------|----------|\n"},
	"sec_reading_path":         {"\n## Reading Path\n\n", "\n## 阅读路径\n\n"},

	// File document sections
	"sec_public_details":   {"\n## Public Interface Details\n\n", "\n## 公共接口详情\n\n"},
	"label_signature":      {"- **Signature**: `%s`\n", "- **签名**：`%s`\n"},
	"label_return_type":    {"- **Return Type**: `%s`\n", "- **返回类型**：`%s`\n"},
	"label_docstring":      {"- **Docstring**: %s\n", "- **文档注释**：%s\n"},
	"label_fanin":          {"- **Fan-in**: %d", "- **扇入**：%d"},
	"label_callers":        {" — Callers: %s", " — 调用方：%s"},
	"label_call_chains":    {"- **Call Chains**:\n", "- **调用链**：\n"},
	"sec_data_structs":     {"## Data Structure Definitions\n\n", "## 数据结构定义\n\n"},
	"sec_internal_funcs":   {"## Internal Functions\n\n", "## 内部函数\n\n"},
	"internal_func_header": {"| Function | Line | Brief Signature |\n|------|------|----------|\n", "| 函数 | 行号 | 简要签名 |\n|------|------|----------|\n"},
	"mermaid_title_deps":   {"Dependencies", "依赖关系"},
	"sec_dep_diagram":      {"\n## Dependency Diagram\n\n%s\n", "\n## 依赖关系图\n\n%s\n"},
	"sec_external_deps":    {"\n## External Dependencies\n\n", "\n## 外部依赖\n\n"},
	"ext_dep_file_header":  {"| File | Call Count | Main Callers |\n|------|----------|------------|\n", "| 文件 | 调用次数 | 主要调用方 |\n|------|----------|------------|\n"},
	"read_risk_file":       {"%d. Read **`%s`** first — highest risk score (%.1f), reasons: %s\n", "%d. 优先阅读 **`%s`** —— 风险分最高（%.1f），原因：%s\n"},
	"read_fanin_top":       {"%d. Start with **`%s`** — this function has the highest fan-in (%d), making it the core entry point of this file\n", "%d. 从 **`%s`** 开始 —— 该函数扇入最高（%d），是本文件的核心入口\n"},
	"read_fanin_second":    {"%d. Focus on **`%s`** — second highest fan-in interface, understand secondary responsibilities\n", "%d. 关注 **`%s`** —— 扇入次高的接口，理解次要职责\n"},
	"read_struct":          {"%d. Understand **`%s`** — core data structure, master the data model\n", "%d. 理解 **`%s`** —— 核心数据结构，掌握数据模型\n"},
	"read_ext_couple":      {"%d. Check external coupling points in the dependency graph — depends on %d external files\n", "%d. 在依赖图中检查外部耦合点 —— 依赖 %d 个外部文件\n"},

	// Module document sections
	"sec_core_files":         {"\n## Core Files\n\n", "\n## 核心文件\n\n"},
	"core_files_header":      {"| File | Symbols | Public Interfaces | Called By | Responsibility |\n|------|--------|----------|--------|------|\n", "| 文件 | 符号数 | 公共接口 | 被调用次数 | 职责 |\n|------|--------|----------|--------|------|\n"},
	"sec_ext_interfaces":     {"\n## External Interfaces (External Callers)\n\n", "\n## 外部接口（外部调用方）\n\n"},
	"ext_iface_header":       {"| Interface Function | Caller Directory | Caller Symbols |\n|----------|-----------|------------|\n", "| 接口函数 | 调用方目录 | 调用方符号 |\n|----------|-----------|------------|\n"},
	"mermaid_title_crossdir": {"Cross-directory Dependencies", "跨目录依赖"},
	"sec_crossdir_chains":    {"\n## Cross-directory Call Chains\n\n%s\n", "\n## 跨目录调用链\n\n%s\n"},
	"ext_dep_dir_header":     {"| Dependency Directory | Call Count | Main Callers |\n|----------|----------|------------|\n", "| 依赖目录 | 调用次数 | 主要调用方 |\n|----------|----------|------------|\n"},
	"sec_strong_coupled":     {"\n## Strongest Coupled Directories\n\n", "\n## 耦合最强的目录\n\n"},
	"coupled_header":         {"| Directory | Ca External Fan-in | Ce External Fan-out | Total |\n|------|--------------|--------------|------|\n", "| 目录 | Ca 外部扇入 | Ce 外部扇出 | 合计 |\n|------|--------------|--------------|------|\n"},
	"mermaid_title_internal": {"Internal Dependencies", "内部依赖"},
	"sec_internal_deps":      {"\n## Internal File Dependencies\n\n%s\n", "\n## 内部文件依赖\n\n%s\n"},
	"read_risk_module":       {"%d. Read **`%s`** first — highest risk score within module (%.1f), reasons: %s\n", "%d. 优先阅读 **`%s`** —— 模块内风险分最高（%.1f），原因：%s\n"},
	"read_entry_file":        {"%d. Understand module main entry from **`%s`** — %s, called %d times\n", "%d. 从 **`%s`** 理解模块主入口 —— %s，被调用 %d 次\n"},
	"read_core_file":         {"%d. Read **`%s`** to understand core logic — %s\n", "%d. 阅读 **`%s`** 以理解核心逻辑 —— %s\n"},
	"read_ext_contract":      {"%d. Reference **`%s`** to understand external contracts — called by external modules like `%s`\n", "%d. 参考 **`%s`** 以理解对外契约 —— 被 `%s` 等外部模块调用\n"},

	// Project document sections
	"sec_project_overview":    {"## Project Overview\n\n", "## 项目概览\n\n"},
	"sec_arch_layering":       {"\n## Architecture Layering\n\n", "\n## 架构分层\n\n"},
	"layer_table_header":      {"| Directory | Responsibility | Symbols | Public Interfaces | External Fan-in | External Fan-out |\n|------|------|--------|----------|----------|----------|\n", "| 目录 | 职责 | 符号数 | 公共接口 | 外部扇入 | 外部扇出 |\n|------|------|--------|----------|----------|----------|\n"},
	"layer_entry":             {"Entry Layer (Fan-in=0, provides project entry points)", "入口层（Fan-in=0，提供项目入口）"},
	"layer_business":          {"Business Layer (Core business logic)", "业务层（核心业务逻辑）"},
	"layer_infra":             {"Infrastructure Layer (High fan-out, provides common capabilities)", "基础设施层（高扇出，提供通用能力）"},
	"sec_module_overview":     {"## Module Overview\n\n", "## 模块概览\n\n"},
	"module_overview_header":  {"| Directory | Symbols | Public Interfaces | Ca External Fan-in | Ce External Fan-out | I | Responsibility |\n|------|--------|----------|--------------|--------------|---|------|\n", "| 目录 | 符号数 | 公共接口 | Ca 外部扇入 | Ce 外部扇出 | I | 职责 |\n|------|--------|----------|--------------|--------------|---|------|\n"},
	"sec_boundary_violations": {"\n## Architecture Boundary Violations\n\n", "\n## 架构边界违规\n\n"},
	"boundary_none":           {"No obvious high-level direct penetration to low-level or low-level reverse calls to high-level detected.\n", "未发现明显的高层直接穿透至低层或低层反向调用高层的情况。\n"},
	"violation_high_to_low":   {"High-level `%s` directly calls low-level `%s` (%d times), suggest converging boundaries through business/port layers.", "高层 `%s` 直接调用低层 `%s`（%d 次），建议通过业务/端口层收敛边界。"},
	"violation_low_to_high":   {"Low-level `%s` reversely calls high-level `%s` (%d times), dependency direction reversal risk detected.", "低层 `%s` 反向调用高层 `%s`（%d 次），存在依赖方向倒置风险。"},
	"mermaid_title_modules":   {"Module Dependencies", "模块依赖"},
	"sec_module_topology":     {"\n## Module Dependency Topology\n\n%s\n", "\n## 模块依赖拓扑\n\n%s\n"},
	"sec_key_chains":          {"\n## Key Call Chains\n\n", "\n## 关键调用链\n\n"},
	"sec_cycle_detection":     {"\n## Cycle Detection\n\n", "\n## 循环依赖检测\n\n"},
	"cycle_none":              {"No package-level cyclic dependencies detected ✓\n", "未检测到包级循环依赖 ✓\n"},
	"cycle_found":             {"Detected %d package-level cyclic dependencies:\n\n", "检测到 %d 处包级循环依赖：\n\n"},
	"read_risk_project":       {"%d. Read **`%s`** first — highest project risk score (%.1f), reasons: %s\n", "%d. 优先阅读 **`%s`** —— 项目风险分最高（%.1f），原因：%s\n"},
	"read_entry_main":         {"%d. Entry: Understand request entry from **`%s`**'s **`%s`**\n", "%d. 入口：从 **`%s`** 的 **`%s`** 理解请求入口\n"},
	"read_entry_dir":          {"%d. Entry: Understand project entry from **`%s`** — %s\n", "%d. 入口：从 **`%s`** 理解项目入口 —— %s\n"},
	"read_main_flow":          {"%d. Main flow: Trace **`%s`** call chain to understand core business logic\n", "%d. 主流程：追踪 **`%s`** 调用链以理解核心业务逻辑\n"},
	"read_infra":              {"%d. Infrastructure: **`%s`** provides common capabilities, consult as needed\n", "%d. 基础设施：**`%s`** 提供通用能力，按需查阅\n"},
	"read_cycles":             {"%d. Note: %d cyclic dependencies exist, prioritize decoupling\n", "%d. 注意：存在 %d 处循环依赖，优先解耦\n"},

	// Complexity reasons and symmetry risks
	"reason_cc_extreme": {"Extremely high cyclomatic complexity: %d independent paths", "圈复杂度极高：%d 条独立路径"},
	"reason_cc_high":    {"High cyclomatic complexity: %d independent paths", "圈复杂度较高：%d 条独立路径"},
	"reason_loc":        {"Excessively long function body: %d lines of effective code", "函数体过长：%d 行有效代码"},
	"reason_depth":      {"Deep nesting: maximum depth %d", "嵌套过深：最大深度 %d"},
	"reason_returns":    {"Multiple return points: %d return statements", "返回点过多：%d 处 return"},
	"reason_fan":        {"Large call surface: fan-in %d, fan-out %d", "调用面过大：扇入 %d，扇出 %d"},
	"reason_cross":      {"Cross-directory coupling: %d cross-directory calls", "跨目录耦合：%d 处跨目录调用"},
	"reason_public":     {"Public interface changes have broader impact", "公共接口变更影响面更广"},
	"reason_low_risk":   {"Complexity, length, and call surface are all in low-risk range", "复杂度、长度与调用面均处于低风险区间"},
	"symmetry_risk":     {"Found `%s` semantic but no corresponding `%s`: example `%s`", "发现 `%s` 语义但缺少对应的 `%s`：例如 `%s`"},
}

// rolePhrases maps inferred role tokens to localized display names.
var rolePhrases = map[string][2]string{
	"RequestHandler":         {"RequestHandler", "请求处理器"},
	"BusinessService":        {"BusinessService", "业务服务"},
	"DataAccess":             {"DataAccess", "数据访问"},
	"DataModel":              {"DataModel", "数据模型"},
	"Utility":                {"Utility", "工具集"},
	"Configuration":          {"Configuration", "配置"},
	"Middleware":             {"Middleware", "中间件"},
	"TestHelper":             {"TestHelper", "测试辅助"},
	"PublicInterface":        {"PublicInterface", "公共接口"},
	"InternalImplementation": {"InternalImplementation", "内部实现"},
	"MixedModule":            {"MixedModule", "混合模块"},
	"Unknown":                {"Unknown", "未知"},
}

// normalizeDocLang collapses any locale tag to the supported set: "zh" or "en".
func normalizeDocLang(lang string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(lang)), "zh") {
		return "zh"
	}
	return "en"
}

// docT resolves a document phrase for the given locale, falling back to the key itself.
func docT(lang, key string) string {
	p, ok := docPhrases[key]
	if !ok {
		return key
	}
	if normalizeDocLang(lang) == "zh" {
		return p[1]
	}
	return p[0]
}

// roleT resolves a localized display name for an inferred role token.
func roleT(lang, role string) string {
	p, ok := rolePhrases[role]
	if !ok {
		return role
	}
	if normalizeDocLang(lang) == "zh" {
		return p[1]
	}
	return p[0]
}

func synthesizeUnderstandingDoc(db *sqlx.DB, projectRoot, docType, key, lang string) string {
	switch docType {
	case "file":
		return synthesizeFileDoc(db, projectRoot, key, lang)
	case "module":
		return synthesizeModuleDoc(db, projectRoot, key, lang)
	case "project":
		return synthesizeProjectDoc(db, projectRoot, lang)
	default:
		return synthesizeProjectDoc(db, projectRoot, lang)
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
func inferBriefSummary(nodes []*AstraMapNode, lang string) string {
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
		return docT(lang, "brief_summary_fallback")
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

func calculateComplexityMetrics(db *sqlx.DB, projectRoot, filePath, symbolID string, symbolIDs []string, lang string) ([]complexityMetric, error) {
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
		m := calculateNodeComplexity(db, projectRoot, n, lang)
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

func calculateNodeComplexity(db *sqlx.DB, projectRoot string, n *AstraMapNode, lang string) complexityMetric {
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
		ComplexityReasons:      complexityReasons(lang, 1+branchCount, loc, depth, returnCount, fanIn, fanOut, cross, public),
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

func complexityReasons(lang string, cc, loc, depth, returns, fanIn, fanOut, cross int, public bool) []string {
	reasons := make([]string, 0, 6)
	if cc > 20 {
		reasons = append(reasons, fmt.Sprintf(docT(lang, "reason_cc_extreme"), cc))
	} else if cc > 10 {
		reasons = append(reasons, fmt.Sprintf(docT(lang, "reason_cc_high"), cc))
	}
	if loc > 120 {
		reasons = append(reasons, fmt.Sprintf(docT(lang, "reason_loc"), loc))
	}
	if depth > 4 {
		reasons = append(reasons, fmt.Sprintf(docT(lang, "reason_depth"), depth))
	}
	if returns > 5 {
		reasons = append(reasons, fmt.Sprintf(docT(lang, "reason_returns"), returns))
	}
	if fanIn > 10 || fanOut > 10 {
		reasons = append(reasons, fmt.Sprintf(docT(lang, "reason_fan"), fanIn, fanOut))
	}
	if cross > 0 {
		reasons = append(reasons, fmt.Sprintf(docT(lang, "reason_cross"), cross))
	}
	if public {
		reasons = append(reasons, docT(lang, "reason_public"))
	}
	if len(reasons) == 0 {
		reasons = append(reasons, docT(lang, "reason_low_risk"))
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

func topComplexityMetrics(db *sqlx.DB, projectRoot string, nodes []*AstraMapNode, limit int, lang string) []complexityMetric {
	metrics := make([]complexityMetric, 0, len(nodes))
	for _, n := range nodes {
		if n.Kind != "function" && n.Kind != "method" {
			continue
		}
		metrics = append(metrics, calculateNodeComplexity(db, projectRoot, n, lang))
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

func renderRiskTable(b *strings.Builder, lang, title string, metrics []complexityMetric) {
	if len(metrics) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## %s\n\n", title)
	b.WriteString(docT(lang, "risk_table_header"))
	for _, m := range metrics {
		reason := ""
		if len(m.ComplexityReasons) > 0 {
			reason = m.ComplexityReasons[0]
		}
		fmt.Fprintf(b, "| `%s` | %.1f | %d | %d | %d | %d | %d | %s |\n", m.Name, m.RiskScore, m.CyclomaticComplexity, m.LinesOfCode, m.NestingDepth, m.FanIn, m.FanOut, reason)
	}
}

func renderDynamicDispatchSection(b *strings.Builder, lang string, metrics []complexityMetric) {
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
	b.WriteString(docT(lang, "dyn_dispatch_title"))
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

func symmetryRisks(nodes []*AstraMapNode, lang string) []string {
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
				risks = append(risks, fmt.Sprintf(docT(lang, "symmetry_risk"), pair[0], pair[1], leftName))
			} else {
				risks = append(risks, fmt.Sprintf(docT(lang, "symmetry_risk"), pair[1], pair[0], rightName))
			}
		}
	}
	sort.Strings(risks)
	if len(risks) > 12 {
		return risks[:12]
	}
	return risks
}

func synthesizeFileDoc(db *sqlx.DB, projectRoot, filePath, lang string) string {
	var nodes []*AstraMapNode
	if err := db.Select(&nodes, "SELECT * FROM astramap_nodes WHERE file_path = ? ORDER BY start_line", filePath); err != nil || len(nodes) == 0 {
		return fmt.Sprintf(docT(lang, "file_doc_empty"), filePath)
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

	nodeLang := ""
	if len(nodes) > 0 {
		nodeLang = nodes[0].Language
	}
	role := inferRole(nodes)
	fileRisk := topComplexityMetrics(db, projectRoot, nodes, 10, lang)

	var b strings.Builder
	fmt.Fprintf(&b, docT(lang, "file_doc_title"), filePath)

	// Responsibility
	b.WriteString(docT(lang, "sec_responsibility"))
	fmt.Fprintf(&b, "**%s** — %s\n\n", roleT(lang, role), inferBriefSummary(exported, lang))

	// Overview Statistics
	b.WriteString(docT(lang, "sec_overview"))
	b.WriteString(docT(lang, "table_metric_value"))
	fmt.Fprintf(&b, docT(lang, "row_language"), nodeLang)
	fmt.Fprintf(&b, docT(lang, "row_total_symbols"), len(nodes))
	fmt.Fprintf(&b, docT(lang, "row_public_interfaces"), len(exported))
	fmt.Fprintf(&b, docT(lang, "row_internal_functions"), len(internal))
	fmt.Fprintf(&b, docT(lang, "row_data_structures"), len(dataStructs))
	fmt.Fprintf(&b, docT(lang, "row_external_deps"), len(extDepFiles))
	fmt.Fprintf(&b, docT(lang, "row_dependent_by"), len(incomingDepFiles))
	if len(fileRisk) > 0 {
		fmt.Fprintf(&b, docT(lang, "row_highest_risk"), fileRisk[0].Name, fileRisk[0].RiskScore)
	}

	renderRiskTable(&b, lang, docT(lang, "risk_table_title"), fileRisk)

	if risks := symmetryRisks(nodes, lang); len(risks) > 0 {
		b.WriteString(docT(lang, "sec_symmetry"))
		for _, risk := range risks {
			fmt.Fprintf(&b, "- %s\n", risk)
		}
	}

	renderDynamicDispatchSection(&b, lang, fileRisk)

	// Public Interface Details
	if len(exportedInfo) > 0 {
		b.WriteString(docT(lang, "sec_public_details"))
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
			fmt.Fprintf(&b, docT(lang, "label_signature"), sig)
			if n.ReturnType != "" {
				fmt.Fprintf(&b, docT(lang, "label_return_type"), n.ReturnType)
			}
			if n.Docstring != "" {
				ds := n.Docstring
				if len(ds) > 200 {
					ds = ds[:197] + "..."
				}
				fmt.Fprintf(&b, docT(lang, "label_docstring"), ds)
			}
			fmt.Fprintf(&b, docT(lang, "label_fanin"), info.fanIn)
			if len(info.callerNames) > 0 {
				fmt.Fprintf(&b, docT(lang, "label_callers"), strings.Join(info.callerNames, ", "))
			}
			fmt.Fprintf(&b, "\n")

			chains := extractCallChains(db, n.ID, 2, 8)
			if len(chains) > 0 {
				b.WriteString(docT(lang, "label_call_chains"))
				for _, ch := range chains {
					fmt.Fprintf(&b, "  - `%s`\n", ch)
				}
			}
			fmt.Fprintf(&b, "\n")
		}
	}

	// Data Structure Definitions
	if len(dataStructs) > 0 {
		b.WriteString(docT(lang, "sec_data_structs"))
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
					fmt.Fprintf(&b, "```%s\n%s\n```\n\n", nodeLang, src)
				}
			}
			if n.Docstring != "" {
				fmt.Fprintf(&b, "%s\n\n", n.Docstring)
			}
		}
	}

	// Internal Functions
	if len(internal) > 0 {
		b.WriteString(docT(lang, "sec_internal_funcs"))
		b.WriteString(docT(lang, "internal_func_header"))
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
		mermaid := buildMermaidDepGraph(deps, nodeShort, docT(lang, "mermaid_title_deps"))
		if mermaid != "" {
			fmt.Fprintf(&b, docT(lang, "sec_dep_diagram"), mermaid)
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
		b.WriteString(docT(lang, "sec_external_deps"))
		b.WriteString(docT(lang, "ext_dep_file_header"))
		for _, d := range deps {
			topCallers := d.callers
			if len(topCallers) > 3 {
				topCallers = topCallers[:3]
			}
			fmt.Fprintf(&b, "| `%s` | %d | %s |\n", filepath.Base(d.file), d.count, strings.Join(topCallers, ", "))
		}
	}

	// Reading Path
	b.WriteString(docT(lang, "sec_reading_path"))
	step := 1
	if len(fileRisk) > 0 {
		top := fileRisk[0]
		fmt.Fprintf(&b, docT(lang, "read_risk_file"), step, top.Name, top.RiskScore, strings.Join(top.ComplexityReasons, "; "))
		step++
	}
	if len(exportedInfo) > 0 {
		top := exportedInfo[0].node
		fmt.Fprintf(&b, docT(lang, "read_fanin_top"), step, top.Name, exportedInfo[0].fanIn)
		step++
		if len(exportedInfo) > 1 {
			fmt.Fprintf(&b, docT(lang, "read_fanin_second"), step, exportedInfo[1].node.Name)
			step++
		}
	}
	if len(dataStructs) > 0 {
		fmt.Fprintf(&b, docT(lang, "read_struct"), step, dataStructs[0].Name)
		step++
	}
	if len(extDepFiles) > 0 {
		fmt.Fprintf(&b, docT(lang, "read_ext_couple"), step, len(extDepFiles))
	}

	return b.String()
}

func synthesizeModuleDoc(db *sqlx.DB, projectRoot, dirPath, lang string) string {
	prefix := dirPath + "/"
	var nodes []*AstraMapNode
	if err := db.Select(&nodes, "SELECT * FROM astramap_nodes WHERE file_path LIKE ? ORDER BY file_path, start_line", prefix+"%"); err != nil || len(nodes) == 0 {
		return fmt.Sprintf(docT(lang, "module_doc_empty"), dirPath)
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
	moduleRisk := topComplexityMetrics(db, projectRoot, nodes, 15, lang)

	var b strings.Builder
	fmt.Fprintf(&b, docT(lang, "module_doc_title"), dirPath)

	// Responsibility
	b.WriteString(docT(lang, "sec_responsibility"))
	fmt.Fprintf(&b, docT(lang, "module_responsibility"), roleT(lang, role), len(fileMap), len(nodes), countExported(nodes))

	// Overview Statistics
	b.WriteString(docT(lang, "sec_overview"))
	b.WriteString(docT(lang, "table_metric_value"))
	fmt.Fprintf(&b, docT(lang, "row_file_count"), len(fileMap))
	fmt.Fprintf(&b, docT(lang, "row_total_symbols"), len(nodes))
	fmt.Fprintf(&b, docT(lang, "row_public_interfaces"), countExported(nodes))
	fmt.Fprintf(&b, docT(lang, "row_ext_fanin"), totalFanIn)
	fmt.Fprintf(&b, docT(lang, "row_ext_fanout"), totalFanOut)
	instabilityDenominator := totalFanIn + totalFanOut
	instability := 0.0
	if instabilityDenominator > 0 {
		instability = float64(totalFanOut) / float64(instabilityDenominator)
	}
	fmt.Fprintf(&b, docT(lang, "row_instability"), instability)
	if len(moduleRisk) > 0 {
		fmt.Fprintf(&b, docT(lang, "row_highest_risk"), moduleRisk[0].Name, moduleRisk[0].RiskScore)
	}

	renderRiskTable(&b, lang, docT(lang, "risk_table_title"), moduleRisk)

	if risks := symmetryRisks(nodes, lang); len(risks) > 0 {
		b.WriteString(docT(lang, "sec_symmetry"))
		for _, risk := range risks {
			fmt.Fprintf(&b, "- %s\n", risk)
		}
	}

	renderDynamicDispatchSection(&b, lang, moduleRisk)

	// Core Files
	if len(fileOrder) > 0 {
		b.WriteString(docT(lang, "sec_core_files"))
		b.WriteString(docT(lang, "core_files_header"))
		for _, fp := range fileOrder {
			fi := fileMap[fp]
			fmt.Fprintf(&b, "| `%s` | %d | %d | %d | %s |\n", filepath.Base(fp), fi.symbolCnt, fi.exportedCnt, fi.callerCnt, roleT(lang, fi.role))
		}
	}

	// External Interfaces (External Callers)
	if len(externalInterfaces) > 0 {
		b.WriteString(docT(lang, "sec_ext_interfaces"))
		b.WriteString(docT(lang, "ext_iface_header"))
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
		mermaid := buildMermaidDepGraph(deps, nodeShort, docT(lang, "mermaid_title_crossdir"))
		if mermaid != "" {
			fmt.Fprintf(&b, docT(lang, "sec_crossdir_chains"), mermaid)
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
		b.WriteString(docT(lang, "sec_external_deps"))
		b.WriteString(docT(lang, "ext_dep_dir_header"))
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
		b.WriteString(docT(lang, "sec_strong_coupled"))
		b.WriteString(docT(lang, "coupled_header"))
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
		mermaid := buildMermaidDepGraph(deps, nodeShort, docT(lang, "mermaid_title_internal"))
		if mermaid != "" {
			fmt.Fprintf(&b, docT(lang, "sec_internal_deps"), mermaid)
		}
	}

	// Reading Path
	b.WriteString(docT(lang, "sec_reading_path"))
	step := 1
	if len(moduleRisk) > 0 {
		top := moduleRisk[0]
		fmt.Fprintf(&b, docT(lang, "read_risk_module"), step, top.Name, top.RiskScore, strings.Join(top.ComplexityReasons, "; "))
		step++
	}
	if len(fileOrder) > 0 {
		fi := fileMap[fileOrder[0]]
		fmt.Fprintf(&b, docT(lang, "read_entry_file"), step, filepath.Base(fi.path), roleT(lang, fi.role), fi.callerCnt)
		step++
	}
	if len(fileOrder) > 1 {
		fi := fileMap[fileOrder[1]]
		fmt.Fprintf(&b, docT(lang, "read_core_file"), step, filepath.Base(fi.path), roleT(lang, fi.role))
		step++
	}
	if len(externalInterfaces) > 0 {
		fmt.Fprintf(&b, docT(lang, "read_ext_contract"), step, externalInterfaces[0].name, externalInterfaces[0].callerDir)
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

func synthesizeProjectDoc(db *sqlx.DB, projectRoot, lang string) string {
	status, _ := QueryStatus(db)
	var nodes []*AstraMapNode
	if err := db.Select(&nodes, "SELECT * FROM astramap_nodes ORDER BY file_path, start_line"); err != nil || len(nodes) == 0 {
		return docT(lang, "project_doc_empty")
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
	projectRisk := topComplexityMetrics(db, projectRoot, nodes, 20, lang)

	// Language distribution
	langCount := make(map[string]int)
	var files []*AstraMapFile
	if err := db.Select(&files, "SELECT * FROM astramap_files"); err == nil {
		for _, f := range files {
			langCount[f.Language]++
		}
	}
	langParts := make([]string, 0, len(langCount))
	for codeLang, cnt := range langCount {
		langParts = append(langParts, fmt.Sprintf(docT(lang, "lang_files"), codeLang, cnt))
	}
	sort.Strings(langParts)
	langStr := strings.Join(langParts, ", ")
	if langStr == "" {
		langStr = docT(lang, "lang_unknown")
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
	b.WriteString(docT(lang, "project_doc_title"))

	// Project Overview
	b.WriteString(docT(lang, "sec_project_overview"))
	b.WriteString(docT(lang, "table_metric_value"))
	if status != nil {
		fmt.Fprintf(&b, docT(lang, "row_total_nodes"), status.NodeCount)
		fmt.Fprintf(&b, docT(lang, "row_total_edges"), status.EdgeCount)
		fmt.Fprintf(&b, docT(lang, "row_file_count"), status.FileCount)
	}
	fmt.Fprintf(&b, docT(lang, "row_dir_count"), len(dirStats))
	fmt.Fprintf(&b, docT(lang, "row_lang_dist"), langStr)
	if len(projectRisk) > 0 {
		fmt.Fprintf(&b, docT(lang, "row_highest_risk"), projectRisk[0].Name, projectRisk[0].RiskScore)
	}

	renderRiskTable(&b, lang, docT(lang, "risk_table_title_project"), projectRisk)

	if risks := symmetryRisks(nodes, lang); len(risks) > 0 {
		b.WriteString(docT(lang, "sec_global_symmetry"))
		for _, risk := range risks {
			fmt.Fprintf(&b, "- %s\n", risk)
		}
	}

	renderDynamicDispatchSection(&b, lang, projectRisk)

	// Architecture Layering
	b.WriteString(docT(lang, "sec_arch_layering"))
	printLayerTable := func(title string, entries []layerEntry) {
		if len(entries) == 0 {
			return
		}
		fmt.Fprintf(&b, "### %s\n\n", title)
		b.WriteString(docT(lang, "layer_table_header"))
		for _, e := range entries {
			fmt.Fprintf(&b, "| `%s` | %s | %d | %d | %d | %d |\n", e.dir, roleT(lang, e.role), e.st.symbols, e.st.exported, e.st.fanIn, e.st.fanOut)
		}
		fmt.Fprintf(&b, "\n")
	}
	printLayerTable(docT(lang, "layer_entry"), entryLayer)
	printLayerTable(docT(lang, "layer_business"), bizLayer)
	printLayerTable(docT(lang, "layer_infra"), infraLayer)

	// Module Overview
	b.WriteString(docT(lang, "sec_module_overview"))
	b.WriteString(docT(lang, "module_overview_header"))
	for _, d := range dirs {
		st := dirStats[d]
		denominator := st.fanIn + st.fanOut
		instability := 0.0
		if denominator > 0 {
			instability = float64(st.fanOut) / float64(denominator)
		}
		fmt.Fprintf(&b, "| `%s` | %d | %d | %d | %d | %.2f | %s |\n", d, st.symbols, st.exported, st.fanIn, st.fanOut, instability, roleT(lang, st.role))
	}

	violations := architectureBoundaryViolations(dirDeps, dirStats, lang)
	if len(violations) > 0 {
		b.WriteString(docT(lang, "sec_boundary_violations"))
		for _, violation := range violations {
			fmt.Fprintf(&b, "- %s\n", violation)
		}
	} else {
		b.WriteString(docT(lang, "sec_boundary_violations"))
		b.WriteString(docT(lang, "boundary_none"))
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
		mermaid := buildMermaidDepGraph(deps, nodeShort, docT(lang, "mermaid_title_modules"))
		if mermaid != "" {
			fmt.Fprintf(&b, docT(lang, "sec_module_topology"), mermaid)
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
		b.WriteString(docT(lang, "sec_key_chains"))
		if len(keyChains) > 5 {
			keyChains = keyChains[:5]
		}
		for _, ch := range keyChains {
			fmt.Fprintf(&b, "- `%s`\n", ch)
		}
	}

	// Cycle Detection
	cycles, err := FindCycles(db, "package")
	b.WriteString(docT(lang, "sec_cycle_detection"))
	if err != nil || len(cycles) == 0 {
		b.WriteString(docT(lang, "cycle_none"))
	} else {
		fmt.Fprintf(&b, docT(lang, "cycle_found"), len(cycles))
		limit := len(cycles)
		if limit > 10 {
			limit = 10
		}
		for i := 0; i < limit; i++ {
			fmt.Fprintf(&b, "- `%s`\n", strings.Join(cycles[i], " → "))
		}
	}

	// Reading Path
	b.WriteString(docT(lang, "sec_reading_path"))
	step := 1
	if len(projectRisk) > 0 {
		top := projectRisk[0]
		fmt.Fprintf(&b, docT(lang, "read_risk_project"), step, top.Name, top.RiskScore, strings.Join(top.ComplexityReasons, "; "))
		step++
	}
	if len(entryLayer) > 0 {
		e := entryLayer[0]
		mainFunc := findMainFunc(db, e.dir)
		if mainFunc != "" {
			fmt.Fprintf(&b, docT(lang, "read_entry_main"), step, e.dir, mainFunc)
		} else {
			fmt.Fprintf(&b, docT(lang, "read_entry_dir"), step, e.dir, roleT(lang, e.role))
		}
		step++
	}
	if len(keyChains) > 0 {
		fmt.Fprintf(&b, docT(lang, "read_main_flow"), step, keyChains[0])
		step++
	}
	if len(infraLayer) > 0 {
		fmt.Fprintf(&b, docT(lang, "read_infra"), step, infraLayer[0].dir)
		step++
	}
	if len(cycles) > 0 {
		fmt.Fprintf(&b, docT(lang, "read_cycles"), step, len(cycles))
	}

	return b.String()
}

func architectureBoundaryViolations(dirDeps map[string]map[string]int, dirStats map[string]*docDirStat, lang string) []string {
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
					text:  fmt.Sprintf(docT(lang, "violation_high_to_low"), src, tgt, count),
					count: count,
				})
			}
			if srcRank == 3 && tgtRank == 1 {
				violations = append(violations, boundaryViolation{
					text:  fmt.Sprintf(docT(lang, "violation_low_to_high"), src, tgt, count),
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
