package astramap

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	sitter "github.com/tree-sitter/go-tree-sitter"
	c "github.com/tree-sitter/tree-sitter-c/bindings/go"
	cpp "github.com/tree-sitter/tree-sitter-cpp/bindings/go"
	golang "github.com/tree-sitter/tree-sitter-go/bindings/go"
	java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

var (
	// callRe holds light regex pattern for cross-file call heuristics
	callRe              = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)\s*\(`)
	functionPointerInit = regexp.MustCompile(`\.\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*=\s*&?\s*([a-zA-Z_][a-zA-Z0-9_]*)`)
	macroReturnCallRe   = regexp.MustCompile(`\b[A-Z][A-Z0-9_]*RETURN\s*\(([^,\)]+)`)
)

// ParseFileIncremental parses a single file incrementally using Tree-sitter.
// It extracts node definitions, contains edges, local calls, and file imports.
func ParseFileIncremental(projectRoot, filePath string) ([]*AstraMapNode, []*AstraMapEdge, string, error) {
	absPath := filePath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(projectRoot, filePath)
	}

	relPath, err := filepath.Rel(projectRoot, absPath)
	if err != nil {
		relPath = filePath
	}

	file, err := os.Open(absPath)
	if err != nil {
		return nil, nil, "", err
	}
	defer file.Close()

	// 1. Calculate Content Hash and read source code
	hasher := sha256.New()
	tee := io.TeeReader(file, hasher)
	codeBytes, err := io.ReadAll(tee)
	if err != nil {
		return nil, nil, "", err
	}
	contentHash := hex.EncodeToString(hasher.Sum(nil))
	sourceLines := strings.Split(string(codeBytes), "\n")


	// 2. Identify Language and load corresponding Tree-sitter grammar
	ext := strings.ToLower(filepath.Ext(filePath))
	lang := "unknown"
	var langGrammar *sitter.Language
	switch ext {
	case ".go":
		lang = "go"
		langGrammar = sitter.NewLanguage(golang.Language())
	case ".py":
		lang = "python"
		langGrammar = sitter.NewLanguage(python.Language())
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		lang = "typescript"
		if ext == ".tsx" {
			langGrammar = sitter.NewLanguage(typescript.LanguageTSX())
		} else {
			langGrammar = sitter.NewLanguage(typescript.LanguageTypescript())
		}
	case ".c":
		lang = "c"
		langGrammar = sitter.NewLanguage(c.Language())
	case ".cpp", ".cc", ".cxx", ".hpp", ".hxx":
		lang = "cpp"
		langGrammar = sitter.NewLanguage(cpp.Language())
	case ".h":
		if hasProjectExtension(projectRoot, ".cpp", ".cc", ".cxx", ".hpp", ".hxx") {
			lang = "cpp"
			langGrammar = sitter.NewLanguage(cpp.Language())
		} else {
			lang = "c"
			langGrammar = sitter.NewLanguage(c.Language())
		}
	case ".java":
		lang = "java"
		langGrammar = sitter.NewLanguage(java.Language())
	}

	if lang == "unknown" || langGrammar == nil {
		return nil, nil, contentHash, nil
	}

	// 3. Tree-sitter parsing
	parser := sitter.NewParser()
	parser.SetLanguage(langGrammar)
	defer parser.Close()

	tree := parser.Parse(codeBytes, nil)
	if tree == nil {
		return nil, nil, contentHash, fmt.Errorf("tree-sitter parse failed")
	}
	defer tree.Close()

	rootNode := tree.RootNode()
	now := time.Now().Unix()

	// 4. Traverse syntax tree to collect node definitions and 'contains' hierarchy edges
	var nodes []*AstraMapNode
	var edges []*AstraMapEdge
	definedSymbols := make(map[string]*AstraMapNode)

	var collect func(n *sitter.Node, container string)
	collect = func(n *sitter.Node, container string) {
		if n == nil {
			return
		}

		nodeType := n.Kind()
		nodeName := ""
		nodeKind := ""
		sig := ""
		isDef := false

		switch lang {
		case "go":
			if nodeType == "function_declaration" {
				nodeKind = "function"
				if nameNode := n.ChildByFieldName("name"); nameNode != nil {
					nodeName = nodeText(nameNode, codeBytes)
				}
				isDef = true
			} else if nodeType == "method_declaration" {
				nodeKind = "method"
				if nameNode := n.ChildByFieldName("name"); nameNode != nil {
					nodeName = nodeText(nameNode, codeBytes)
				}
				receiver := ""
				if recvNode := n.ChildByFieldName("receiver"); recvNode != nil {
					recvText := nodeText(recvNode, codeBytes)
					receiver = extractGoReceiverStruct(recvText)
				}
				if receiver != "" {
					container = receiver
				}
				isDef = true
			} else if nodeType == "type_spec" {
				if nameNode := n.ChildByFieldName("name"); nameNode != nil {
					nodeName = nodeText(nameNode, codeBytes)
				}
				if typeNode := n.ChildByFieldName("type"); typeNode != nil {
					if typeNode.Kind() == "struct_type" {
						nodeKind = "struct"
						isDef = true
					} else if typeNode.Kind() == "interface_type" {
						nodeKind = "interface"
						isDef = true
					}
				}
			}

		case "python":
			if nodeType == "class_definition" {
				nodeKind = "class"
				if nameNode := n.ChildByFieldName("name"); nameNode != nil {
					nodeName = nodeText(nameNode, codeBytes)
				}
				isDef = true
			} else if nodeType == "function_definition" {
				if nameNode := n.ChildByFieldName("name"); nameNode != nil {
					nodeName = nodeText(nameNode, codeBytes)
				}
				if container != "" {
					nodeKind = "method"
				} else {
					nodeKind = "function"
				}
				isDef = true
			}

		case "typescript":
			if nodeType == "class_declaration" || nodeType == "interface_declaration" {
				if nodeType == "class_declaration" {
					nodeKind = "class"
				} else {
					nodeKind = "interface"
				}
				if nameNode := n.ChildByFieldName("name"); nameNode != nil {
					nodeName = nodeText(nameNode, codeBytes)
				}
				isDef = true
			} else if nodeType == "function_declaration" {
				nodeKind = "function"
				if nameNode := n.ChildByFieldName("name"); nameNode != nil {
					nodeName = nodeText(nameNode, codeBytes)
				}
				isDef = true
			} else if nodeType == "method_definition" {
				nodeKind = "method"
				if nameNode := n.ChildByFieldName("name"); nameNode != nil {
					nodeName = nodeText(nameNode, codeBytes)
				}
				isDef = true
			}

		case "c", "cpp":
			if nodeType == "type_definition" {
				if declNode := n.ChildByFieldName("declarator"); declNode != nil {
					nodeName = extractDeclaratorIdentifier(declNode, codeBytes)
				}
				typeNode := n.ChildByFieldName("type")
				switch {
				case containsNodeKind(typeNode, "class_specifier"):
					nodeKind = "type"
				case containsNodeKind(typeNode, "struct_specifier"):
					nodeKind = "type"
				case containsNodeKind(typeNode, "enum_specifier"):
					nodeKind = "type"
				default:
					nodeKind = "type"
				}
				isDef = nodeName != ""
			} else if nodeType == "class_specifier" || nodeType == "struct_specifier" || nodeType == "enum_specifier" {
				if n.Parent() != nil && n.Parent().Kind() == "type_definition" {
					isDef = false
				} else {
					if nodeType == "class_specifier" {
						nodeKind = "class"
					} else if nodeType == "enum_specifier" {
						nodeKind = "enum"
					} else {
						nodeKind = "struct"
					}
					if nameNode := n.ChildByFieldName("name"); nameNode != nil {
						nodeName = nodeText(nameNode, codeBytes)
					}
					isDef = true
				}
			} else if nodeType == "function_definition" || nodeType == "declaration" {
				declNode := n.ChildByFieldName("declarator")
				isFuncDecl := false
				if declNode != nil {
					if containsNodeKind(declNode, "parameter_list") {
						isFuncDecl = true
						nodeName, container = extractCppFuncNameAndContainer(declNode, codeBytes)
					}
				}
				if isFuncDecl {
					if lang == "cpp" && container != "" {
						nodeKind = "method"
					} else {
						nodeKind = "function"
					}
					isDef = true
				}
			} else if nodeType == "preproc_def" || nodeType == "preproc_function_def" {
				if nameNode := n.ChildByFieldName("name"); nameNode != nil {
					nodeName = nodeText(nameNode, codeBytes)
				}
				nodeKind = "macro"
				isDef = nodeName != ""
			}

		case "java":
			if nodeType == "class_declaration" || nodeType == "interface_declaration" {
				if nodeType == "class_declaration" {
					nodeKind = "class"
				} else {
					nodeKind = "interface"
				}
				if nameNode := n.ChildByFieldName("name"); nameNode != nil {
					nodeName = nodeText(nameNode, codeBytes)
				}
				isDef = true
			} else if nodeType == "method_declaration" {
				nodeKind = "method"
				if nameNode := n.ChildByFieldName("name"); nameNode != nil {
					nodeName = nodeText(nameNode, codeBytes)
				}
				isDef = true
			}
		}

		nextContainer := container
		if isDef && nodeName != "" && nodeKind != "" {
			qname := nodeName
			if container != "" {
				if lang == "cpp" {
					qname = container + "::" + nodeName
				} else {
					qname = container + "." + nodeName
				}
			}

			sigLines := strings.Split(nodeText(n, codeBytes), "\n")
			if len(sigLines) > 0 {
				sig = strings.TrimSpace(sigLines[0])
			}

			usn := fmt.Sprintf("%s:%s::%s", getLangPrefix(lang), relPath, qname)
			doc := ""
			if len(sourceLines) > 0 {
				doc = findLeadingComments(sourceLines, int(n.StartPosition().Row)+1)
			}
			amNode := &AstraMapNode{
				ID:            usn,
				Kind:          nodeKind,
				Name:          nodeName,
				QualifiedName: qname,
				FilePath:      relPath,
				Language:      lang,
				StartLine:     int(n.StartPosition().Row) + 1,
				EndLine:       int(n.EndPosition().Row) + 1,
				Signature:     sig,
				Docstring:     doc,
				UpdatedAt:     now,
			}

			nodes = append(nodes, amNode)
			definedSymbols[nodeName] = amNode

			var parentID string
			if container == "" {
				parentID = fmt.Sprintf("file:%s", relPath)
			} else {
				if parentNode, exists := definedSymbols[container]; exists {
					parentID = parentNode.ID
				} else {
					parentID = fmt.Sprintf("file:%s", relPath)
				}
			}
			edges = append(edges, &AstraMapEdge{
				Source:     parentID,
				Target:     usn,
				Kind:       "contains",
				Provenance: "tree-sitter",
			})

			nextContainer = qname
		}

		for i := uint(0); i < n.ChildCount(); i++ {
			collect(n.Child(i), nextContainer)
		}
	}

	initialContainer := ""
	if lang == "go" {
		for i := uint(0); i < rootNode.ChildCount(); i++ {
			child := rootNode.Child(i)
			if child.Kind() == "package_clause" {
				pkgText := strings.TrimSpace(nodeText(child, codeBytes))
				if strings.HasPrefix(pkgText, "package ") {
					initialContainer = strings.TrimSpace(strings.TrimPrefix(pkgText, "package "))
				}
				break
			}
		}
	} else if lang == "python" {
		base := filepath.Base(filePath)
		initialContainer = strings.TrimSuffix(base, filepath.Ext(base))
	}

	collect(rootNode, initialContainer)


	if lang == "c" || lang == "cpp" {
		// 仅捕获具有显式声明、定义、初始化或注册意图的大写宏
		heuristicMacroRegex := regexp.MustCompile(`\b((?:(?:DECLARE|DEF|CREATE|IMPLEMENT)_[A-Z0-9_]+)|(?:[A-Z0-9_]+_(?:INIT|FUNC|REGISTER|ENTRY|HANDLER|CALLBACK)))\s*\(\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\)`)
		inBlockComment := false
		for lineIdx, line := range sourceLines {
			trimmed := strings.TrimSpace(line)
			// 1. 跳过块注释的边界以及行内注释行
			if strings.HasPrefix(trimmed, "/*") {
				inBlockComment = true
			}
			if inBlockComment {
				if strings.Contains(trimmed, "*/") {
					inBlockComment = false
				}
				continue
			}
			if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "*") {
				continue // 过滤空行、单行注释、预处理宏定义本身以及块注释内部行
			}

			matches := heuristicMacroRegex.FindStringSubmatch(line)
			if len(matches) == 3 {
				macroName := matches[1]
				symName := matches[2]

				// 2. 检查匹配项左侧是否有单行注释或块注释标记
				matchIdx := strings.Index(line, matches[0])
				if matchIdx > 0 {
					prefix := line[:matchIdx]
					if strings.Contains(prefix, "//") || strings.Contains(prefix, "/*") {
						continue
					}
				}

				if _, exists := definedSymbols[symName]; !exists {
					usn := fmt.Sprintf("%s:%s::%s", getLangPrefix(lang), relPath, symName)
					amNode := &AstraMapNode{
						ID:            usn,
						Kind:          "function",
						Name:          symName,
						QualifiedName: symName,
						FilePath:      relPath,
						Language:      lang,
						StartLine:     lineIdx + 1,
						EndLine:       lineIdx + 1,
						Signature:     strings.TrimSpace(line),
						Docstring:     fmt.Sprintf("由宏 %s 隐式生成的函数定义", macroName),
						UpdatedAt:     now,
					}
					nodes = append(nodes, amNode)
					definedSymbols[symName] = amNode

					parentID := fmt.Sprintf("file:%s", relPath)
					edges = append(edges, &AstraMapEdge{
						Source:     parentID,
						Target:     usn,
						Kind:       "contains",
						Provenance: "tree-sitter",
					})
				}
			}
		}
	}




	// 5. Traverse AST to collect 'calls' inside the same file
	getEnclosingFunc := func(line int) *AstraMapNode {
		var matched *AstraMapNode
		for _, node := range nodes {
			if (node.Kind == "function" || node.Kind == "method") && line >= node.StartLine && line <= node.EndLine {
				if matched == nil || (node.EndLine-node.StartLine < matched.EndLine-matched.StartLine) {
					matched = node
				}
			}
		}
		return matched
	}

	var collectCalls func(n *sitter.Node)
	collectCalls = func(n *sitter.Node) {
		if n == nil {
			return
		}

		nodeType := n.Kind()
		isCall := false
		var calleeNode *sitter.Node

		switch lang {
		case "go", "typescript", "c", "cpp":
			if nodeType == "call_expression" {
				isCall = true
				calleeNode = n.ChildByFieldName("function")
				if calleeNode == nil {
					calleeNode = n.ChildByFieldName("expression")
				}
			}
		case "python":
			if nodeType == "call" {
				isCall = true
				calleeNode = n.ChildByFieldName("function")
			}
		case "java":
			if nodeType == "method_invocation" {
				isCall = true
				calleeNode = n.ChildByFieldName("name")
			}
		}

		if isCall && calleeNode != nil {
			calleeName := extractCalleeShortName(calleeNode, codeBytes)
			lineNum := int(n.StartPosition().Row) + 1

			if calleeName != "" && !isKeyword(calleeName) {
				callerNode := getEnclosingFunc(lineNum)
				if callerNode != nil {
					if targetNode, exists := definedSymbols[calleeName]; exists {
						if targetNode.ID != callerNode.ID {
							edges = append(edges, &AstraMapEdge{
								Source:     callerNode.ID,
								Target:     targetNode.ID,
								Kind:       "calls",
								Provenance: "tree-sitter",
								Line:       lineNum,
								Col:        int(n.StartPosition().Column) + 1,
							})
						}
					} else {
						edges = append(edges, &AstraMapEdge{
							Source:     callerNode.ID,
							Target:     externalCallTargetID(lang, calleeName),
							Kind:       "calls",
							Provenance: "tree-sitter",
							Line:       lineNum,
							Col:        int(n.StartPosition().Column) + 1,
						})
					}
				}
			}
		}

		for i := uint(0); i < n.ChildCount(); i++ {
			collectCalls(n.Child(i))
		}
	}

	collectCalls(rootNode)

	// 6. Collect file imports edges
	var collectImports func(n *sitter.Node)
	collectImports = func(n *sitter.Node) {
		if n == nil {
			return
		}
		nodeType := n.Kind()
		if nodeType == "import_spec" || nodeType == "import_statement" || nodeType == "import_from_statement" || nodeType == "preproc_include" {
			impPath := normalizeImportText(nodeText(n, codeBytes))
			if impPath != "" {
				targetUSN := fmt.Sprintf("import:%s", impPath)
				edges = append(edges, &AstraMapEdge{
					Source:     fmt.Sprintf("file:%s", relPath),
					Target:     targetUSN,
					Kind:       "imports",
					Provenance: "tree-sitter",
				})
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			collectImports(n.Child(i))
		}
	}
	collectImports(rootNode)

	return nodes, edges, contentHash, nil
}

// ===== Tree-sitter Helper Functions =====

func extractGoReceiverStruct(recv string) string {
	recv = strings.Trim(recv, "()")
	parts := strings.Fields(recv)
	if len(parts) < 2 {
		return ""
	}
	t := parts[len(parts)-1]
	t = strings.TrimPrefix(t, "*")
	return t
}

func extractCppFuncNameAndContainer(n *sitter.Node, code []byte) (name, container string) {
	if n == nil {
		return "", ""
	}
	if n.Kind() == "qualified_identifier" {
		scopeNode := n.ChildByFieldName("scope")
		nameNode := n.ChildByFieldName("name")
		if scopeNode != nil && nameNode != nil {
			return nodeText(nameNode, code), nodeText(scopeNode, code)
		}
	}
	if n.Kind() == "function_declarator" {
		declarator := n.ChildByFieldName("declarator")
		return extractCppFuncNameAndContainer(declarator, code)
	}
	if n.Kind() == "pointer_declarator" {
		for i := uint(0); i < n.ChildCount(); i++ {
			c := n.Child(i)
			if c.Kind() == "function_declarator" || c.Kind() == "qualified_identifier" || c.Kind() == "field_identifier" || c.Kind() == "identifier" {
				return extractCppFuncNameAndContainer(c, code)
			}
		}
	}
	return nodeText(n, code), ""
}

func extractCalleeShortName(n *sitter.Node, code []byte) string {
	if n == nil {
		return ""
	}
	nodeType := n.Kind()
	if nodeType == "identifier" || nodeType == "field_identifier" || nodeType == "type_identifier" {
		return nodeText(n, code)
	}
	if nodeType == "selector_expression" {
		field := n.ChildByFieldName("field")
		if field != nil {
			return nodeText(field, code)
		}
	}
	if nodeType == "attribute" {
		attribute := n.ChildByFieldName("attribute")
		if attribute != nil {
			return nodeText(attribute, code)
		}
	}
	if nodeType == "member_expression" {
		property := n.ChildByFieldName("property")
		if property != nil {
			return nodeText(property, code)
		}
	}
	return nodeText(n, code)
}

func extractDeclaratorIdentifier(n *sitter.Node, code []byte) string {
	if n == nil {
		return ""
	}
	if n.Kind() == "identifier" || n.Kind() == "type_identifier" {
		return nodeText(n, code)
	}
	if nameNode := n.ChildByFieldName("name"); nameNode != nil {
		if name := extractDeclaratorIdentifier(nameNode, code); name != "" {
			return name
		}
	}
	if declNode := n.ChildByFieldName("declarator"); declNode != nil {
		if name := extractDeclaratorIdentifier(declNode, code); name != "" {
			return name
		}
	}
	for i := n.ChildCount(); i > 0; i-- {
		if name := extractDeclaratorIdentifier(n.Child(i-1), code); name != "" {
			return name
		}
	}
	return ""
}

func externalCallTargetID(lang, name string) string {
	prefix := getLangPrefix(lang)
	if prefix == "cpp" {
		prefix = "cxx"
	}
	return fmt.Sprintf("external:%s . . $ %s.", prefix, name)
}

func containsNodeKind(n *sitter.Node, kind string) bool {
	if n == nil {
		return false
	}
	if n.Kind() == kind {
		return true
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		if containsNodeKind(n.Child(i), kind) {
			return true
		}
	}
	return false
}

func getLangPrefix(lang string) string {
	switch lang {
	case "c":
		return "c"
	case "go":
		return "go"
	case "python":
		return "py"
	case "typescript":
		return "ts"
	case "cpp":
		return "cxx"
	case "java":
		return "java"
	}
	return "unknown"
}

func normalizeImportText(s string) string {
	s = strings.TrimSpace(s)
	for _, prefix := range []string{"#include", "import", "from"} {
		s = strings.TrimSpace(strings.TrimPrefix(s, prefix))
	}
	s = strings.Trim(s, `"'<> `)
	return s
}

func hasProjectExtension(projectRoot string, extensions ...string) bool {
	wanted := make(map[string]struct{}, len(extensions))
	for _, ext := range extensions {
		wanted[ext] = struct{}{}
	}
	found := false
	_ = filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || found {
			return nil
		}
		if info.IsDir() {
			if shouldSkipIndexDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := wanted[strings.ToLower(filepath.Ext(path))]; ok {
			found = true
		}
		return nil
	})
	return found
}

func nodeText(n *sitter.Node, code []byte) string {
	if n == nil {
		return ""
	}
	start := n.StartByte()
	end := n.EndByte()
	if int(start) > len(code) || int(end) > len(code) || start > end {
		return ""
	}
	return string(code[start:end])
}

func isKeyword(name string) bool {
	keywords := map[string]bool{
		"if": true, "else": true, "while": true, "for": true,
		"switch": true, "return": true, "sizeof": true, "typeof": true,
		"break": true, "continue": true, "goto": true, "do": true,
		"case": true, "default": true, "typedef": true, "struct": true,
		"union": true, "enum": true, "static": true, "inline": true,
		"extern": true, "const": true, "void": true, "int": true,
		"char": true, "short": true, "long": true, "float": true,
		"double": true, "unsigned": true, "signed": true, "super": true, "this": true,
	}
	return keywords[name]
}

func isInsideQuotes(s string) bool {
	inDouble := false
	inSingle := false
	inBacktick := false
	escaped := false
	for _, r := range s {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '"' && !inSingle && !inBacktick {
			inDouble = !inDouble
		} else if r == '\'' && !inDouble && !inBacktick {
			inSingle = !inSingle
		} else if r == '`' && !inDouble && !inSingle {
			inBacktick = !inBacktick
		}
	}
	return inDouble || inSingle || inBacktick
}

type funcNode struct {
	ID        string `db:"id"`
	StartLine int    `db:"start_line"`
	EndLine   int    `db:"end_line"`
}

// ResolveCrossFileCalls scans all indexed source files and resolves
// function call references against the global symbol registry in DB.
// This fills in cross-file 'calls' edges that single-file parsing misses.
func ResolveCrossFileCalls(db *sqlx.DB, projectRoot string) error {
	return resolveCrossFileCalls(db, projectRoot, nil)
}

func ResolveCrossFileCallsForFiles(db *sqlx.DB, projectRoot string, files []string) error {
	if len(files) == 0 {
		return nil
	}
	return resolveCrossFileCalls(db, projectRoot, files)
}

func resolveCrossFileCalls(db *sqlx.DB, projectRoot string, changedFiles []string) error {
	filter, err := LoadIndexFilter(projectRoot)
	if err != nil {
		return fmt.Errorf("读取 AstraMap 配置失败: %w", err)
	}

	type globalNode struct {
		ID            string `db:"id"`
		Name          string `db:"name"`
		QualifiedName string `db:"qualified_name"`
		FilePath      string `db:"file_path"`
	}
	var allFuncs []globalNode
	err = db.Select(&allFuncs, "SELECT id, name, qualified_name, file_path FROM astramap_nodes WHERE kind IN ('function', 'method')")
	if err != nil {
		return fmt.Errorf("query global registry failed: %w", err)
	}

	shortMap := make(map[string][]string)
	qualifiedMap := make(map[string][]string)
	for _, fn := range allFuncs {
		shortMap[fn.Name] = append(shortMap[fn.Name], fn.ID)
		qualifiedMap[fn.QualifiedName] = append(qualifiedMap[fn.QualifiedName], fn.ID)
	}
	fieldFunctionMap := buildFunctionPointerFieldMap(projectRoot, shortMap)

	var files []string
	if len(changedFiles) == 0 {
		err = db.Select(&files, "SELECT path FROM astramap_files")
		if err != nil {
			return fmt.Errorf("query files failed: %w", err)
		}
	} else {
		seen := make(map[string]bool, len(changedFiles))
		for _, filePath := range changedFiles {
			if filePath == "" {
				continue
			}
			relPath := filePath
			if filepath.IsAbs(relPath) {
				if rel, relErr := filepath.Rel(projectRoot, relPath); relErr == nil {
					relPath = rel
				}
			}
			relPath = filepath.ToSlash(relPath)
			if seen[relPath] {
				continue
			}
			seen[relPath] = true
			files = append(files, relPath)
		}
	}

	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if len(changedFiles) == 0 {
		_, err = tx.Exec("DELETE FROM astramap_edges WHERE provenance = 'heuristic' AND kind = 'calls'")
		if err != nil {
			return fmt.Errorf("failed to clear old heuristic calls: %w", err)
		}
	}

	insertStmt, err := tx.Preparex(`
		INSERT OR IGNORE INTO astramap_edges (source, target, kind, provenance, line, col, metadata)
		VALUES (?, ?, 'calls', 'heuristic', ?, ?, '')
	`)
	if err != nil {
		return err
	}
	defer insertStmt.Close()

	for _, fp := range files {
		if !filter.Allows(fp, StageTreeSitter) {
			continue
		}
		if len(changedFiles) > 0 {
			_, err = tx.Exec(`
				DELETE FROM astramap_edges
				WHERE provenance = 'heuristic'
				  AND kind = 'calls'
				  AND source IN (
				    SELECT id FROM astramap_nodes
				    WHERE file_path = ? AND kind IN ('function', 'method')
				  )
			`, fp)
			if err != nil {
				return fmt.Errorf("failed to clear heuristic calls for %s: %w", fp, err)
			}
		}

		absPath := filepath.Join(projectRoot, fp)
		file, err := os.Open(absPath)
		if err != nil {
			continue
		}

		var lines []string
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		file.Close()

		var localFuncs []funcNode
		err = tx.Select(&localFuncs, "SELECT id, start_line, end_line FROM astramap_nodes WHERE file_path = ? AND kind IN ('function', 'method')", fp)
		if err != nil {
			continue
		}

		inMultiLineComment := false
		for i, line := range lines {
			lineNum := i + 1
			trimmed := strings.TrimSpace(line)

			if !inMultiLineComment {
				if strings.HasPrefix(trimmed, "/*") {
					inMultiLineComment = true
					if strings.Contains(trimmed, "*/") {
						inMultiLineComment = false
					}
					continue
				}
			} else {
				if strings.Contains(trimmed, "*/") {
					inMultiLineComment = false
				}
				continue
			}

			if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
				continue
			}

			matches := callRe.FindAllStringSubmatchIndex(line, -1)
			if len(matches) == 0 {
				continue
			}

			var callerID string
			callerSpan := 0
			for _, lf := range localFuncs {
				if lineNum >= lf.StartLine && lineNum <= lf.EndLine {
					span := lf.EndLine - lf.StartLine
					if callerID == "" || span < callerSpan {
						callerID = lf.ID
						callerSpan = span
					}
				}
			}
			if callerID == "" {
				continue
			}

			for _, macroTarget := range macroReturnCallTargets(line, shortMap, fieldFunctionMap) {
				if macroTarget == callerID {
					continue
				}
				_, _ = insertStmt.Exec(callerID, macroTarget, lineNum, 1)
			}

			for _, m := range matches {
				if len(m) < 4 {
					continue
				}
				calleeName := line[m[2]:m[3]]
				if isKeyword(calleeName) {
					continue
				}

				if isInsideQuotes(line[:m[0]]) {
					continue
				}

				targets := shortMap[calleeName]

				beforeCallee := line[:m[2]]
				sepIndex := -1
				lastDot := strings.LastIndex(beforeCallee, ".")
				lastArrow := strings.LastIndex(beforeCallee, "->")
				lastColon := strings.LastIndex(beforeCallee, "::")

				if lastDot > sepIndex {
					sepIndex = lastDot
				}
				if lastArrow > sepIndex {
					sepIndex = lastArrow
				}
				if lastColon > sepIndex {
					sepIndex = lastColon
				}

				if sepIndex != -1 {
					if fieldTargets := fieldFunctionMap[calleeName]; len(fieldTargets) > 0 {
						targets = fieldTargets
					}
				}
				if calleeName == "main" && sepIndex == -1 {
					var filtered []string
					for _, tID := range targets {
						if strings.Contains(tID, ":"+fp+"::") {
							filtered = append(filtered, tID)
						}
					}
					targets = filtered
				}
				if sepIndex != -1 {
					leftBound := sepIndex
					for leftBound > 0 {
						c := beforeCallee[leftBound-1]
						if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
							leftBound--
						} else {
							break
						}
					}
					prefix := beforeCallee[leftBound:sepIndex]
					if prefix != "" {
						possibleQualified1 := prefix + "." + calleeName
						possibleQualified2 := prefix + "::" + calleeName
						if qTargets, exists := qualifiedMap[possibleQualified1]; exists {
							targets = qTargets
						} else if qTargets, exists := qualifiedMap[possibleQualified2]; exists {
							targets = qTargets
						}
					}
				}
				if isAmbiguousHeuristicCall(targets) {
					continue
				}

				for _, targetID := range targets {
					if targetID == callerID {
						continue
					}
					_, _ = insertStmt.Exec(callerID, targetID, lineNum, m[0]+1)
				}
			}
		}
	}

	return tx.Commit()
}

func buildFunctionPointerFieldMap(projectRoot string, shortMap map[string][]string) map[string][]string {
	fieldMap := make(map[string][]string)
	seen := make(map[string]map[string]struct{})

	structFieldsMap := make(map[string][]string)
	typedefMap := make(map[string]string)

	structRe := regexp.MustCompile(`struct\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*\{([\s\S]*?)\}`)
	funcPtrFieldRe := regexp.MustCompile(`\(\s*\*\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\)`)
	typedefRe := regexp.MustCompile(`typedef\s+struct\s+([a-zA-Z_][a-zA-Z0-9_]*)\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*;`)

	// Pass 1: Parse all structs & typedefs
	_ = filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if shouldSkipIndexDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".c" && ext != ".h" && ext != ".cpp" && ext != ".cc" && ext != ".cxx" && ext != ".hpp" && ext != ".hxx" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)

		structMatches := structRe.FindAllStringSubmatch(content, -1)
		for _, m := range structMatches {
			structName := m[1]
			body := m[2]
			var fields []string
			lines := strings.Split(body, "\n")
			for _, line := range lines {
				fSub := funcPtrFieldRe.FindStringSubmatch(line)
				if len(fSub) > 1 {
					fields = append(fields, fSub[1])
				}
			}
			if len(fields) > 0 {
				structFieldsMap[structName] = fields
			}
		}

		typedefMatches := typedefRe.FindAllStringSubmatch(content, -1)
		for _, m := range typedefMatches {
			typedefMap[m[2]] = m[1]
		}
		return nil
	})

	for alias, realName := range typedefMap {
		if fields, ok := structFieldsMap[realName]; ok {
			structFieldsMap[alias] = fields
		}
	}

	// Pass 2: Extract assignments
	_ = filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if shouldSkipIndexDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".c" && ext != ".h" && ext != ".cpp" && ext != ".cc" && ext != ".cxx" && ext != ".hpp" && ext != ".hxx" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)

		// 2.1 Designated initializer mode (.init = func)
		matches := functionPointerInit.FindAllStringSubmatch(content, -1)
		for _, match := range matches {
			if len(match) < 3 {
				continue
			}
			fieldName := match[1]
			funcName := match[2]
			for _, targetID := range shortMap[funcName] {
				if seen[fieldName] == nil {
					seen[fieldName] = make(map[string]struct{})
				}
				if _, exists := seen[fieldName][targetID]; exists {
					continue
				}
				seen[fieldName][targetID] = struct{}{}
				fieldMap[fieldName] = append(fieldMap[fieldName], targetID)
			}
		}

		// 2.2 Sequential initialization mode (fireflys_api_t api = { func1, func2 })
		initVarRe := regexp.MustCompile(`(?:const\s+)?([a-zA-Z_][a-zA-Z0-9_]*)\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*=\s*\{([\s\S]*?)\}`)
		initVarMatches := initVarRe.FindAllStringSubmatch(content, -1)
		for _, m := range initVarMatches {
			typeName := m[1]
			fields, hasFields := structFieldsMap[typeName]
			if !hasFields {
				if realType, ok := typedefMap[typeName]; ok {
					fields, hasFields = structFieldsMap[realType]
				}
			}

			if hasFields && len(fields) > 0 {
				body := m[3]
				bodyClean := removeCComments(body)
				items := splitCInitList(bodyClean)
				for idx, item := range items {
					if idx >= len(fields) {
						break
					}
					fieldName := fields[idx]
					funcName := strings.TrimSpace(item)
					funcName = strings.TrimPrefix(funcName, "&")
					funcName = strings.TrimSpace(funcName)
					if funcName == "" || funcName == "NULL" || funcName == "0" || funcName == "nullptr" {
						continue
					}
					if !isValidCIdentifier(funcName) {
						continue
					}
					for _, targetID := range shortMap[funcName] {
						if seen[fieldName] == nil {
							seen[fieldName] = make(map[string]struct{})
						}
						if _, exists := seen[fieldName][targetID]; exists {
							continue
						}
						seen[fieldName][targetID] = struct{}{}
						fieldMap[fieldName] = append(fieldMap[fieldName], targetID)
					}
				}
			}
		}
		return nil
	})
	return fieldMap
}

func removeCComments(s string) string {
	mlRe := regexp.MustCompile(`/\*[\s\S]*?\*/`)
	s = mlRe.ReplaceAllString(s, "")
	slRe := regexp.MustCompile(`//.*`)
	s = slRe.ReplaceAllString(s, "")
	return s
}

func splitCInitList(s string) []string {
	var items []string
	var current strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '{' {
			depth++
			current.WriteByte(c)
		} else if c == '}' {
			depth--
			current.WriteByte(c)
		} else if c == ',' && depth == 0 {
			items = append(items, strings.TrimSpace(current.String()))
			current.Reset()
		} else {
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		items = append(items, strings.TrimSpace(current.String()))
	}
	return items
}

func isValidCIdentifier(s string) bool {
	if len(s) == 0 {
		return false
	}
	first := s[0]
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || first == '_') {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

func macroReturnCallTargets(line string, shortMap, fieldFunctionMap map[string][]string) []string {
	var targets []string
	seen := make(map[string]struct{})
	matches := macroReturnCallRe.FindAllStringSubmatch(line, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		expr := strings.TrimSpace(match[1])
		for _, targetID := range targetsForCallExpression(expr, shortMap, fieldFunctionMap) {
			if _, exists := seen[targetID]; exists {
				continue
			}
			seen[targetID] = struct{}{}
			targets = append(targets, targetID)
		}
	}
	return targets
}

func targetsForCallExpression(expr string, shortMap, fieldFunctionMap map[string][]string) []string {
	name := trailingIdentifier(expr)
	if name == "" {
		return nil
	}
	if strings.Contains(expr, "->") || strings.Contains(expr, ".") {
		if targets := fieldFunctionMap[name]; len(targets) > 0 {
			return targets
		}
	}
	return shortMap[name]
}

func trailingIdentifier(expr string) string {
	expr = strings.TrimSpace(strings.TrimPrefix(expr, "&"))
	end := len(expr)
	for end > 0 {
		c := expr[end-1]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			break
		}
		end--
	}
	start := end
	for start > 0 {
		c := expr[start-1]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			start--
		} else {
			break
		}
	}
	if start == end {
		return ""
	}
	return expr[start:end]
}

func isAmbiguousHeuristicCall(targets []string) bool {
	return len(targets) > 1
}
