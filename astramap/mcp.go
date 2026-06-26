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
# AstraMap MCP 使用指导规则 (Steering Rules)

You are Antigravity/Claude Code programming agent, analyzing the current project with high-precision semantic code map. Please follow these rules:

1. Explore First, Read Never:
   - When you need to understand how function X calls function Y, do not grep or view_file recursively.
   - You must call 'astramap_explore' first. Pass the symbol names, it will find the trace paths with code snippets.

2. Handling Overloads:
   - If a symbol has multiple definitions (e.g. overloads, same method name in different classes), call 'astramap_node'.
   - It returns all candidates in one turn to avoid roundtrips.

3. Observe Quality Redlines:
   - Before you refactor or modify core methods, call 'astramap_verdict'.
   - If there is an active REJECT verdict, review the Suggestion to fix it directly.
`

// RunMcpServer 启动 stdio MCP 协议循环
func RunMcpServer(db *sqlx.DB, projectRoot string) {
	logInfo("AstraMap: 启动 stdio MCP Server 循环, 工作目录: %s", projectRoot)

	go func() {
		if err := SyncAllFilesAstraMap(db, projectRoot); err != nil {
			logError("AstraMap: 后台同步失败: %v", err)
		}
	}()

	reader := bufio.NewReader(os.Stdin)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			fmt.Fprintf(os.Stderr, "读取请求失败: %v\n", err)
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
				Description: "按名称快速检索符号定义，返回符号名称、类型及位置，支持模糊匹配。常用于首层快速定位。触发场景：用户问「X 在哪定义」「找一下 Y 函数」时，优先调用此工具而非 grep。",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"query": map[string]string{"type": "string", "description": "模糊检索符号关键词"},
						"kind":  map[string]string{"type": "string", "description": "符号类型 (function, struct, class, interface 等)"},
					},
					Required: []string{"query"},
				},
			},
			{
				Name:        "astramap_explore",
				Description: "探索区域性代码流，根据给定的业务词汇、一组符号或自然语言任务描述返回相关的源码上下文及拓扑调用关系。AI 代理应首选该命令以快速压缩 Token 并构建逻辑上下文。触发场景：用户描述业务流程或问「X 和 Y 是怎么关联的」时首选此工具。",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"query":    map[string]string{"type": "string", "description": "业务流符号集（多符号用空格隔开）或自然语言任务描述"},
						"maxFiles": map[string]string{"type": "integer", "description": "自适应返回的最大文件范围"},
					},
					Required: []string{"query"},
				},
			},
			{
				Name:        "astramap_node",
				Description: "符号实体详情还原。获取单个符号对应的底层代码实现、文档注释及依赖；在有重载歧义时会在单个 Turn 中合并返回全部候选体以避免 AI 反复调用。触发场景：用户问「X 的源码是什么」「X 的签名和文档」时使用。",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"symbol":      map[string]string{"type": "string", "description": "目标方法/类完全限定名"},
						"file":        map[string]string{"type": "string", "description": "指定文件名"},
						"includeCode": map[string]string{"type": "boolean", "description": "是否附加完整源码内容（默认 true）"},
					},
				},
			},
			{
				Name:        "astramap_callers",
				Description: "向上追溯指定符号的直接上游调用源。触发场景：用户问「谁调用了 X」「X 被哪些地方引用」时使用。",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"symbol": map[string]string{"type": "string", "description": "目标符号ID"},
					},
					Required: []string{"symbol"},
				},
			},
			{
				Name:        "astramap_callees",
				Description: "向下追溯指定符号的直接被调用依赖。触发场景：用户问「X 依赖什么」「X 内部调用了什么」时使用。",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"symbol": map[string]string{"type": "string", "description": "目标符号ID"},
					},
					Required: []string{"symbol"},
				},
			},
			{
				Name:        "astramap_impact",
				Description: "逆向依赖波及评估。输入待变动的符号 ID，深度广度遍历返回波及受损的上游节点列表与风险值。触发场景：用户问「改了 X 会影响什么」时使用。",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"symbol": map[string]string{"type": "string", "description": "修改的源符号ID"},
						"depth":  map[string]string{"type": "integer", "description": "扩散深度限制"},
					},
					Required: []string{"symbol"},
				},
			},
			{
				Name:        "astramap_status",
				Description: "查询代码地图当前索引覆盖率、脏文件列表、系统支持语言以及增量解析状态。触发场景：用户问「索引好了吗」「地图状态如何」时使用。",
				InputSchema: InputSchema{
					Type:       "object",
					Properties: map[string]interface{}{},
				},
			},
			{
				Name:        "astramap_verdict",
				Description: "获取目标符号在 SourceAstra 中的质量审计裁决结论与人工修复建议，指导进行定向重构。触发场景：用户问「X 有没有代码质量问题」时使用。",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"symbolId": map[string]string{"type": "string", "description": "符号的USN编码"},
					},
					Required: []string{"symbolId"},
				},
			},
			{
				Name:        "astramap_trace",
				Description: "追踪从起始符号 A 到目标符号 B 的调用路径。触发场景：用户问「从 A 到 B 的调用链是什么」「执行流如何到达 Y」时使用。",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"from": map[string]string{"type": "string", "description": "起始方法/类完全限定名"},
						"to":   map[string]string{"type": "string", "description": "目标方法/类完全限定名"},
					},
					Required: []string{"from", "to"},
				},
			},
			{
				Name:        "astramap_files",
				Description: "列出当前项目中的所有已索引源文件，支持前缀路径过滤与后缀/模式匹配。触发场景：用户问「项目有哪些文件」「某目录下有哪些源码」时使用。",
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"path":    map[string]string{"type": "string", "description": "路径前缀过滤"},
						"pattern": map[string]string{"type": "string", "description": "文件名正则或模式匹配 (如 *.go)"},
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

		nodes, err2 := QuerySearch(db, query, kind, 20)
		err = err2
		if err == nil {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("Found %d matches for \"%s\":\n\n", len(nodes), query))
			for i, n := range nodes {
				sb.WriteString(fmt.Sprintf("%d. %s (%s) — %s:%d\n", i+1, n.QualifiedName, n.Kind, n.FilePath, n.StartLine))
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

		result, err2 := QueryExplore(db, query, projectRoot, maxFiles)
		err = err2
		if err == nil {
			var sb strings.Builder
			sb.WriteString("### ── AstraMap 调用路径探寻与源码上下文 ──\n\n")
			for _, fr := range result.Files {
				for _, n := range fr.Symbols {
					sb.WriteString(fmt.Sprintf("#### Symbol: %s (%s)\n", n.QualifiedName, n.Kind))
					sb.WriteString(fmt.Sprintf("*Location*: `%s:%d-%d`\n", n.FilePath, n.StartLine, n.EndLine))
					code, _ := ReadSourceRange(projectRoot, n.FilePath, n.StartLine, n.EndLine)
					if code != "" {
						sb.WriteString(fmt.Sprintf("```%s\n%s\n```\n", n.Language, code))
					}
				}
			}
			if len(result.Relationships) > 0 {
				sb.WriteString("*Relationships*:\n")
				for _, r := range result.Relationships {
					sb.WriteString(fmt.Sprintf("  - %s\n", r))
				}
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
		callers, err2 := GetCallers(db, symbol)
		err = err2
		if err == nil {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("### Callers of %s:\n\n", symbol))
			for _, c := range callers {
				sb.WriteString(fmt.Sprintf("- %s (Line %d)\n", c.Source, c.Line))
			}
			content = sb.String()
		}

	case "astramap_callees":
		symbol, _ := argsMap["symbol"].(string)
		callees, err2 := GetCallees(db, symbol)
		err = err2
		if err == nil {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("### Callees of %s:\n\n", symbol))
			for _, c := range callees {
				sb.WriteString(fmt.Sprintf("- %s (Line %d)\n", c.Target, c.Line))
			}
			content = sb.String()
		}

	case "astramap_impact":
		symbol, _ := argsMap["symbol"].(string)
		depthVal, _ := argsMap["depth"].(float64)
		depth := int(depthVal)
		if depth <= 0 {
			depth = 3
		}

		res, err2 := AnalyzeImpact(db, symbol, depth)
		err = err2
		if err == nil {
			data, _ := json.MarshalIndent(res, "", "  ")
			content = string(data)
		}

	case "astramap_status":
		status, err2 := QueryStatus(db)
		err = err2
		if err == nil {
			statusStr := "ready"
			if status.NodeCount == 0 {
				statusStr = "indexing"
			}
			res := map[string]interface{}{
				"status":             statusStr,
				"database":           "SQLite (modernc-sqlite-adapter)",
				"totalFiles":         status.FileCount,
				"indexedNodes":       status.NodeCount,
				"indexedEdges":       status.EdgeCount,
				"supportedLanguages": []string{"go", "cpp", "python", "typescript", "java"},
			}
			data, _ := json.MarshalIndent(res, "", "  ")
			content = string(data)
		}

	case "astramap_verdict":
		symbolId, _ := argsMap["symbolId"].(string)
		verdicts, err2 := QueryVerdicts(db, symbolId)
		err = err2
		if err == nil {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("### ── AstraMap 代码治理裁决 (%s) ──\n\n", symbolId))
			if len(verdicts) == 0 {
				sb.WriteString("✅ 暂无未修复的缺陷裁决。该符号状态良好！\n")
			} else {
				for _, v := range verdicts {
					badge := "⚠️ WARNING"
					if v.HasActiveDefect == 1 {
						badge = "❌ REJECTED"
					}
					sb.WriteString(fmt.Sprintf("#### [%s] Rule: %s (by %s)\n", badge, v.RuleID, v.Operator))
					sb.WriteString(fmt.Sprintf("*缺陷描述*: %s\n", v.Description))
					sb.WriteString(fmt.Sprintf("*修复建议*: **%s**\n\n", v.Suggestion))
				}
			}
			content = sb.String()
		}

	case "astramap_trace":
		from, _ := argsMap["from"].(string)
		to, _ := argsMap["to"].(string)
		paths, err2 := TracePath(db, from, to)
		err = err2
		if err == nil {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("### Trace Path from %s to %s:\n\n", from, to))
			if len(paths) == 0 {
				sb.WriteString("No call path found.\n")
			} else {
				for i, path := range paths {
					sb.WriteString(fmt.Sprintf("Path %d:\n  %s\n", i+1, strings.Join(path, " ──► ")))
				}
			}
			content = sb.String()
		}

	case "astramap_files":
		pathFilter, _ := argsMap["path"].(string)
		pattern, _ := argsMap["pattern"].(string)
		files, err2 := QueryFiles(db, pathFilter, pattern)
		err = err2
		if err == nil {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("### ── AstraMap 已索引源文件树 (共 %d 个文件) ──\n\n", len(files)))
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
