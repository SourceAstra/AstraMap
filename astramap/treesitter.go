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

	sitter "github.com/tree-sitter/go-tree-sitter"
	golang "github.com/tree-sitter/tree-sitter-go/bindings/go"
	python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
	cpp "github.com/tree-sitter/tree-sitter-cpp/bindings/go"
	java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	"github.com/jmoiron/sqlx"
)

var (
	// callRe holds light regex pattern for cross-file call heuristics
	callRe = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)\s*\(`)
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
	case ".c", ".cpp", ".cc", ".cxx", ".h", ".hpp", ".hxx":
		lang = "cpp"
		langGrammar = sitter.NewLanguage(cpp.Language())
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

		case "cpp":
			if nodeType == "class_specifier" || nodeType == "struct_specifier" {
				if nodeType == "class_specifier" {
					nodeKind = "class"
				} else {
					nodeKind = "struct"
				}
				if nameNode := n.ChildByFieldName("name"); nameNode != nil {
					nodeName = nodeText(nameNode, codeBytes)
				}
				isDef = true
			} else if nodeType == "function_definition" {
				declNode := n.ChildByFieldName("declarator")
				if declNode != nil {
					nodeName, container = extractCppFuncNameAndContainer(declNode, codeBytes)
				}
				if container != "" {
					nodeKind = "method"
				} else {
					nodeKind = "function"
				}
				isDef = true
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

	collect(rootNode, "")

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
		case "go", "typescript", "cpp":
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
		if nodeType == "import_spec" || nodeType == "import_statement" || nodeType == "import_from_statement" {
			impPath := strings.Trim(nodeText(n, codeBytes), `"' `)
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

func getLangPrefix(lang string) string {
	switch lang {
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
	type globalNode struct {
		ID            string `db:"id"`
		Name          string `db:"name"`
		QualifiedName string `db:"qualified_name"`
		FilePath      string `db:"file_path"`
	}
	var allFuncs []globalNode
	err := db.Select(&allFuncs, "SELECT id, name, qualified_name, file_path FROM astramap_nodes WHERE kind IN ('function', 'method')")
	if err != nil {
		return fmt.Errorf("query global registry failed: %w", err)
	}

	shortMap := make(map[string][]string)
	qualifiedMap := make(map[string][]string)
	nodeFileMap := make(map[string]string)
	for _, fn := range allFuncs {
		shortMap[fn.Name] = append(shortMap[fn.Name], fn.ID)
		qualifiedMap[fn.QualifiedName] = append(qualifiedMap[fn.QualifiedName], fn.ID)
		nodeFileMap[fn.ID] = fn.FilePath
	}

	var files []string
	err = db.Select(&files, "SELECT path FROM astramap_files")
	if err != nil {
		return fmt.Errorf("query files failed: %w", err)
	}

	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec("DELETE FROM astramap_edges WHERE provenance = 'heuristic' AND kind = 'calls'")
	if err != nil {
		return fmt.Errorf("failed to clear old heuristic calls: %w", err)
	}

	insertStmt, err := tx.Preparex(`
		INSERT OR IGNORE INTO astramap_edges (source, target, kind, provenance, line, col)
		VALUES (?, ?, 'calls', 'heuristic', ?, ?)
	`)
	if err != nil {
		return err
	}
	defer insertStmt.Close()

	for _, fp := range files {
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
			for _, lf := range localFuncs {
				if lineNum >= lf.StartLine && lineNum <= lf.EndLine {
					callerID = lf.ID
				}
			}
			if callerID == "" {
				continue
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
				dotIndex := strings.LastIndex(beforeCallee, ".")
				if dotIndex != -1 {
					leftBound := dotIndex
					for leftBound > 0 {
						c := beforeCallee[leftBound-1]
						if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
							leftBound--
						} else {
							break
						}
					}
					prefix := beforeCallee[leftBound:dotIndex]
					if prefix != "" {
						possibleQualified := prefix + "." + calleeName
						if qTargets, exists := qualifiedMap[possibleQualified]; exists {
							targets = qTargets
						}
					}
				}

				for _, targetID := range targets {
					if nodeFileMap[targetID] == fp {
						continue
					}
					_, _ = insertStmt.Exec(callerID, targetID, lineNum, m[0]+1)
				}
			}
		}
	}

	return tx.Commit()
}
