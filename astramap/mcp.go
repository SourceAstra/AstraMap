package astramap

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jmoiron/sqlx"
)

// ===== MCP JSON-RPC 消息格式 =====

type JsonRpcRequest struct {
	JsonRpc string           `json:"jsonrpc"`
	Method  string           `json:"method"`
	Params  *json.RawMessage `json:"params,omitempty"`
	ID      interface{}      `json:"id,omitempty"`
}

type JsonRpcResponse struct {
	JsonRpc string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *JsonRpcErr `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

type JsonRpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type ToolsListResult struct {
	Tools []McpTool `json:"tools"`
}

type McpTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
}

type InputSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties"`
	Required   []string               `json:"required,omitempty"`
}

type ToolCallParams struct {
	Name      string           `json:"name"`
	Arguments *json.RawMessage `json:"arguments,omitempty"`
}

type ToolCallResult struct {
	Content []McpContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

type McpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

const ServerInstructions = `
# AstraMap MCP Steering Rules

You are Antigravity/Claude Code programming agent, analyzing the current project with high-precision semantic code map. Please follow these rules:

1. Explore First, Read Never:
   - When you need to understand how function X calls function Y, do not grep or view_file recursively.
   - You must call 'astramap_explore' first. Pass the symbol names, it will find the trace paths with code snippets.

2. Handling Overloads:
   - If a symbol has multiple definitions (e.g. overloads, same method name in different classes), call 'astramap_node'.
   - It returns all candidates in one turn to avoid roundtrips.
`

// RunMcpServer starts the stdio MCP protocol loop
func RunMcpServer(db *sqlx.DB, projectRoot string) {
	logInfo("AstraMap: Starting stdio MCP Server loop, working directory: %s", projectRoot)

	go func() {
		if err := SyncAllFilesAstraMap(db, projectRoot); err != nil {
			logError("AstraMap: Background sync failed: %v", err)
		}
	}()

	reader := bufio.NewReader(os.Stdin)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			fmt.Fprintf(os.Stderr, "Failed to read request: %v\n", err)
			continue
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var req JsonRpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			sendError(req.ID, -32700, "Parse error: "+err.Error())
			continue
		}

		handleMcpMessage(db, projectRoot, req)
	}
}

func handleMcpMessage(db *sqlx.DB, projectRoot string, req JsonRpcRequest) {
	switch req.Method {
	case "initialize":
		res := map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"serverInfo": map[string]string{
				"name":    "astramap",
				"version": "1.0.0",
			},
			"instructions": ServerInstructions,
		}
		sendResult(req.ID, res)

	case "notifications/initialized":
		return

	case "tools/list":
		tools := []McpTool{
			{
				Name:        "astramap_search",
				Description: "Quickly search symbol definitions by name, returning symbol name, kind, and location, supporting fuzzy matching. Used for first-level quick location. Trigger: when user asks 'where is X defined' or 'find function Y', prioritize this over grep.",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"query":  map[string]string{"type": "string", "description": "Fuzzy search symbol keyword"},
						"kind":   map[string]string{"type": "string", "description": "Symbol kind (function, struct, class, interface, etc.)"},
						"limit":  map[string]string{"type": "integer", "description": "Return limit, default 20"},
						"offset": map[string]string{"type": "integer", "description": "Pagination offset, default 0"},
					},
					Required: []string{"query"},
				},
			},
			{
				Name:        "astramap_explore",
				Description: "Explore regional code flows, returning relevant source context and topological call relationships based on given business terms, a set of symbols, or natural language task descriptions. Clients should prefer this command to compress context and build logical entry points quickly. Trigger: preferred when user describes a business workflow or asks 'how are X and Y related'.",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"query":    map[string]string{"type": "string", "description": "Business flow symbol set (separated by space) or natural language task description"},
						"maxFiles": map[string]string{"type": "integer", "description": "Adaptive maximum file range returned"},
					},
					Required: []string{"query"},
				},
			},
			{
				Name:        "astramap_node",
				Description: "Resolve symbol entity details. Retrieves the underlying code implementation, docstring, and dependencies for a single symbol; in case of overload ambiguity, merges and returns all candidates in a single turn to avoid roundtrips. Trigger: used when user asks 'what is the source of X' or 'signature and doc of X'.",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"symbol":      map[string]string{"type": "string", "description": "Fully qualified name of target method/class"},
						"file":        map[string]string{"type": "string", "description": "Specify file name"},
						"includeCode": map[string]string{"type": "boolean", "description": "Whether to attach full source code (default true)"},
					},
				},
			},
			{
				Name:        "astramap_callers",
				Description: "Trace direct upstream callers of a specified symbol. Trigger: used when user asks 'who calls X' or 'where is X referenced'.",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"symbol": map[string]string{"type": "string", "description": "Target symbol ID"},
						"limit":  map[string]string{"type": "integer", "description": "Return limit, default 100"},
					},
					Required: []string{"symbol"},
				},
			},
			{
				Name:        "astramap_callees",
				Description: "Trace direct downstream callees of a specified symbol. Trigger: used when user asks 'what does X depend on' or 'what does X call internally'.",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"symbol": map[string]string{"type": "string", "description": "Target symbol ID"},
					},
					Required: []string{"symbol"},
				},
			},
			{
				Name:        "astramap_impact",
				Description: "Reverse dependency impact evaluation. Input the symbol ID to change, performs deep/broad traversal to return upstream nodes affected and risk values. Trigger: used when user asks 'what is affected if I change X'.",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"symbol": map[string]string{"type": "string", "description": "Modified source symbol ID"},
						"depth":  map[string]string{"type": "integer", "description": "Diffusion depth limit"},
					},
					Required: []string{"symbol"},
				},
			},
			{
				Name:        "astramap_status",
				Description: "Query current index coverage, dirty file list, supported languages, and incremental parsing status. Trigger: used when user asks 'is indexing done' or 'what is the map status'.",
				InputSchema: InputSchema{
					Type:       "object",
					Properties: map[string]interface{}{},
				},
			},
			{
				Name:        "astramap_trace",
				Description: "Trace call path from starting symbol A to target symbol B. Trigger: used when user asks 'what is the call chain from A to B' or 'how does execution flow reach Y'.",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"from": map[string]string{"type": "string", "description": "Fully qualified name of starting method/class"},
						"to":   map[string]string{"type": "string", "description": "Fully qualified name of target method/class"},
					},
					Required: []string{"from", "to"},
				},
			},
			{
				Name:        "astramap_files",
				Description: "List all indexed source files in the current project, supporting prefix path filtering and suffix/pattern matching. Trigger: used when user asks 'what files are in the project' or 'what source code is under directory X'.",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"path":    map[string]string{"type": "string", "description": "Path prefix filter"},
						"pattern": map[string]string{"type": "string", "description": "Filename regex or pattern match (e.g. *.go)"},
						"limit":   map[string]string{"type": "integer", "description": "Return limit, default 100"},
						"offset":  map[string]string{"type": "integer", "description": "Pagination offset, default 0"},
					},
				},
			},
		}
		sendResult(req.ID, ToolsListResult{Tools: tools})

	case "tools/call":
		if req.Params == nil {
			sendError(req.ID, -32602, "Missing params")
			return
		}
		var call ToolCallParams
		if err := json.Unmarshal(*req.Params, &call); err != nil {
			sendError(req.ID, -32602, "Invalid params: "+err.Error())
			return
		}

		handleMcpToolCall(db, projectRoot, req.ID, call)

	default:
		if req.ID == nil {
			return
		}
		sendError(req.ID, -32601, "Method not found: "+req.Method)
	}
}

func handleMcpToolCall(db *sqlx.DB, projectRoot string, id interface{}, call ToolCallParams) {
	argsMap := make(map[string]interface{})
	if call.Arguments != nil {
		_ = json.Unmarshal(*call.Arguments, &argsMap)
	}

	var content string
	var err error
	isErr := false

	switch call.Name {
	case "astramap_search":
		query, _ := argsMap["query"].(string)
		kind, _ := argsMap["kind"].(string)
		limitVal, _ := argsMap["limit"].(float64)
		offsetVal, _ := argsMap["offset"].(float64)
		limit := int(limitVal)
		offset := int(offsetVal)

		nodes, err2 := QuerySearchPaged(db, query, kind, limit, offset)
		err = err2
		if err == nil {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("Found %d matches for \"%s\"", len(nodes), query))
			if offset > 0 {
				sb.WriteString(fmt.Sprintf(" (offset %d)", offset))
			}
			sb.WriteString(":\n\n")
			for i, n := range nodes {
				sb.WriteString(fmt.Sprintf("%d. %s (%s) — %s:%d\n", offset+i+1, n.QualifiedName, n.Kind, n.FilePath, n.StartLine))
				if n.Signature != "" {
					sb.WriteString(fmt.Sprintf("   sig: %s\n", n.Signature))
				}
			}
			content = sb.String()
		}

	case "astramap_explore":
		query, _ := argsMap["query"].(string)
		maxFilesVal, _ := argsMap["maxFiles"].(float64)
		maxFiles := int(maxFilesVal)
		if maxFiles <= 0 {
			maxFiles = 3
		}

		result, err2 := QueryExplore(db, query, projectRoot, maxFiles)
		err = err2
		if err == nil {
			const outputBudget = 60000
			truncated := false
			var sb strings.Builder
			sb.WriteString("### ── AstraMap 调用路径探寻与源码上下文 ──\n\n")
			for _, fr := range result.Files {
				for _, n := range fr.Symbols {
					if sb.Len() >= outputBudget {
						truncated = true
						break
					}
					sb.WriteString(fmt.Sprintf("#### Symbol: %s (%s)\n", n.QualifiedName, n.Kind))
					sb.WriteString(fmt.Sprintf("*Location*: `%s:%d-%d`\n", n.FilePath, n.StartLine, n.EndLine))
					code, _ := ReadSourceRange(projectRoot, n.FilePath, n.StartLine, n.EndLine)
					if code != "" {
						if len(code) > 12000 {
							code = code[:12000] + "\n/* ... truncated ... */"
							truncated = true
						}
						sb.WriteString(fmt.Sprintf("```%s\n%s\n```\n", n.Language, code))
					}
				}
				if truncated {
					break
				}
			}
			if len(result.Relationships) > 0 {
				sb.WriteString("*Relationships*:\n")
				for _, r := range result.Relationships {
					if sb.Len() >= outputBudget {
						truncated = true
						break
					}
					sb.WriteString(fmt.Sprintf("  - %s\n", r))
				}
			}
			if truncated {
				sb.WriteString(fmt.Sprintf("\n_Result truncated. Pass a smaller `maxFiles` or a narrower query. Current maxFiles=%d._\n", maxFiles))
			}
			content = sb.String()
		}

	case "astramap_node":
		symbol, _ := argsMap["symbol"].(string)
		file, _ := argsMap["file"].(string)
		includeCode, ok := argsMap["includeCode"].(bool)
		if !ok {
			includeCode = true
		}

		candidates, err2 := QueryNodeBySymbol(db, symbol, file)
		err = err2
		if err == nil {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("### ── AstraMap 符号还原 (发现 %d 个实体) ──\n\n", len(candidates)))
			for i, n := range candidates {
				sb.WriteString(fmt.Sprintf("#### Candidate %d: %s (%s)\n", i+1, n.QualifiedName, n.Kind))
				sb.WriteString(fmt.Sprintf("*位置*: `%s:%d-%d`\n", n.FilePath, n.StartLine, n.EndLine))
				if n.Docstring != "" {
					sb.WriteString(fmt.Sprintf("*文档*: %s\n", n.Docstring))
				}
				if includeCode {
					code, _ := ReadSourceRange(projectRoot, n.FilePath, n.StartLine, n.EndLine)
					if code != "" {
						sb.WriteString(fmt.Sprintf("```%s\n%s\n```\n", n.Language, code))
					}
				}
				sb.WriteString("\n")
			}
			content = sb.String()
		}

	case "astramap_callers":
		symbol, _ := argsMap["symbol"].(string)
		limitVal, _ := argsMap["limit"].(float64)
		limit := int(limitVal)
		if limit <= 0 {
			limit = 100
		}
		ids, resolveErr := ResolveSymbolToIDs(db, symbol)
		if resolveErr != nil || len(ids) == 0 {
			content = fmt.Sprintf("### Callers of %s:\n\nSymbol not found.\n", symbol)
			break
		}
		var allEdges []*AstraMapEdge
		seenEdges := make(map[string]bool)
		for _, id := range ids {
			canonicalID := resolveCanonicalTraceStart(db, id)
			callers, err2 := GetCallersLimited(db, canonicalID, limit+1)
			if err2 == nil {
				for _, c := range callers {
					edgeKey := fmt.Sprintf("%s->%s:%d", c.Source, c.Target, c.Line)
					if !seenEdges[edgeKey] {
						seenEdges[edgeKey] = true
						allEdges = append(allEdges, c)
					}
				}
			}
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("### Callers of %s:\n\n", symbol))
		truncated := len(allEdges) > limit
		if truncated {
			allEdges = allEdges[:limit]
		}
		for _, c := range allEdges {
			sb.WriteString(fmt.Sprintf("- %s → %s (Line %d)\n",
				CanonicalSymbolIDForNodeID(db, c.Source),
				CanonicalSymbolIDForNodeID(db, c.Target),
				c.Line))
			if c.Metadata != "" {
				sb.WriteString(fmt.Sprintf("  - metadata: %s\n", c.Metadata))
			}
		}
		if truncated {
			sb.WriteString(fmt.Sprintf("\n_Result truncated to %d callers. Pass a higher `limit` to retrieve more._\n", limit))
		}
		content = sb.String()

	case "astramap_callees":
		symbol, _ := argsMap["symbol"].(string)
		ids, resolveErr := ResolveSymbolToIDs(db, symbol)
		if resolveErr != nil || len(ids) == 0 {
			content = fmt.Sprintf("### Callees of %s:\n\nSymbol not found.\n", symbol)
			break
		}
		var allEdges []*AstraMapEdge
		seenEdges := make(map[string]bool)
		for _, id := range ids {
			canonicalID := resolveCanonicalTraceStart(db, id)
			callees, err2 := GetCallees(db, canonicalID)
			if err2 == nil {
				for _, c := range callees {
					edgeKey := fmt.Sprintf("%s->%s:%d", c.Source, c.Target, c.Line)
					if !seenEdges[edgeKey] {
						seenEdges[edgeKey] = true
						allEdges = append(allEdges, c)
					}
				}
			}
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("### Callees of %s:\n\n", symbol))
		for _, c := range allEdges {
			sb.WriteString(fmt.Sprintf("- %s → %s (Line %d)\n",
				CanonicalSymbolIDForNodeID(db, c.Source),
				CanonicalSymbolIDForNodeID(db, c.Target),
				c.Line))
			if c.Metadata != "" {
				sb.WriteString(fmt.Sprintf("  - metadata: %s\n", c.Metadata))
			}
		}
		content = sb.String()

	case "astramap_impact":
		symbol, _ := argsMap["symbol"].(string)
		depth := 3
		if depthVal, ok := argsMap["depth"].(float64); ok {
			depth = normalizeImpactDepth(int(depthVal))
		}
		ids, resolveErr := ResolveSymbolToIDs(db, symbol)
		if resolveErr != nil || len(ids) == 0 {
			content = fmt.Sprintf("Symbol not found: %s", symbol)
			isErr = true
			break
		}
		var allAffected []AffectedNodeSummary
		seenNodes := make(map[string]bool)
		var lastErr error
		for _, id := range ids {
			canonicalID := resolveCanonicalTraceStart(db, id)
			res, err2 := AnalyzeImpact(db, canonicalID, depth)
			if err2 != nil {
				lastErr = err2
				continue
			}
			for _, node := range res.AffectedNodes {
				nodeKey := node.SymbolID
				if !seenNodes[nodeKey] {
					seenNodes[nodeKey] = true
					allAffected = append(allAffected, node)
				}
			}
		}
		if len(allAffected) > 0 || lastErr == nil {
			err = nil
			resCombined := &ImpactResult{
				RootSymbolID:  symbol,
				AffectedNodes: allAffected,
			}
			if len(ids) > 0 {
				resCombined.RootSymbolID = CanonicalSymbolIDForNodeID(db, ids[0])
			}
			data, _ := json.MarshalIndent(resCombined, "", "  ")
			content = string(data)
		} else {
			err = lastErr
		}

	case "astramap_status":
		status, err2 := QueryStatusWithProjectRoot(db, projectRoot)
		err = err2
		if err == nil {
			statusStr := "ready"
			if status.NodeCount == 0 {
				statusStr = "indexing"
			}
			filter, _ := LoadIndexFilter(projectRoot)
			languageRuntime := LanguageRuntimeForProject(projectRoot)
			res := map[string]interface{}{
				"status":                        statusStr,
				"database":                      "SQLite (modernc-sqlite-adapter)",
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
				"effectiveLanguageCapabilities": EffectiveLanguageCapabilitiesForProject(db, projectRoot),
				"semanticProviders":             SemanticProviderSpecsForProject(projectRoot),
				"projectUnits":                  DetectProjectUnits(projectRoot, SupportedLanguageIDsForProject(projectRoot), filter),
				"syntaxOverlays":                languageRuntime.SyntaxOverlays,
				"languagePackageDiagnostics":    languageRuntime.Diagnostics,
			}
			data, _ := json.MarshalIndent(res, "", "  ")
			content = string(data)
		}

	case "astramap_trace":
		from, _ := argsMap["from"].(string)
		to, _ := argsMap["to"].(string)

		fromIDs, resolveErr := ResolveSymbolToIDs(db, from)
		if resolveErr != nil || len(fromIDs) == 0 {
			content = fmt.Sprintf("From symbol not found: %s", from)
			isErr = true
			break
		}
		toIDs, resolveErr := ResolveSymbolToIDs(db, to)
		if resolveErr != nil || len(toIDs) == 0 {
			content = fmt.Sprintf("To symbol not found: %s", to)
			isErr = true
			break
		}

		paths, err2 := TracePath(db, fromIDs, toIDs)

		err = err2
		if err == nil {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("### Trace Path from %s to %s:\n\n", from, to))
			if len(paths) == 0 {
				sb.WriteString("No call path found.\n")
			} else {
				for i, path := range paths {
					displayPath := make([]string, 0, len(path))
					for _, nodeID := range path {
						displayPath = append(displayPath, CanonicalSymbolIDForNodeID(db, nodeID))
					}
					sb.WriteString(fmt.Sprintf("Path %d:\n  %s\n", i+1, strings.Join(displayPath, " ──► ")))
				}
			}
			content = sb.String()
		}

	case "astramap_files":
		pathFilter, _ := argsMap["path"].(string)
		pattern, _ := argsMap["pattern"].(string)
		limitVal, _ := argsMap["limit"].(float64)
		offsetVal, _ := argsMap["offset"].(float64)
		limit := int(limitVal)
		offset := int(offsetVal)
		files, err2 := QueryFilesPaged(db, pathFilter, pattern, limit, offset)
		err = err2
		if err == nil {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("### ── AstraMap 已索引源文件树 (返回 %d 个文件", len(files)))
			if offset > 0 {
				sb.WriteString(fmt.Sprintf(", offset %d", offset))
			}
			sb.WriteString(") ──\n\n")
			for _, f := range files {
				sb.WriteString(fmt.Sprintf("- `%s` (语言: %s, 节点数: %d, 大小: %d 字节)\n", f.Path, f.Language, f.NodeCount, f.Size))
			}
			content = sb.String()
		}

	default:
		sendError(id, -32601, "Tool not found")
		return
	}

	if err != nil {
		content = fmt.Sprintf("Error executing tool %s: %v", call.Name, err)
		isErr = true
	}

	res := ToolCallResult{
		Content: []McpContent{
			{
				Type: "text",
				Text: content,
			},
		},
		IsError: isErr,
	}
	sendResult(id, res)
}

func sendResult(id interface{}, result interface{}) {
	if id == nil {
		return
	}
	res := JsonRpcResponse{
		JsonRpc: "2.0",
		Result:  result,
		ID:      id,
	}
	data, _ := json.Marshal(res)
	fmt.Println(string(data))
}

func sendError(id interface{}, code int, message string) {
	if id == nil {
		return
	}
	res := JsonRpcResponse{
		JsonRpc: "2.0",
		Error: &JsonRpcErr{
			Code:    code,
			Message: message,
		},
		ID: id,
	}
	data, _ := json.Marshal(res)
	fmt.Println(string(data))
}
