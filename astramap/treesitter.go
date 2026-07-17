package astramap

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"astramap-standalone/languageprotocol"
	"github.com/jmoiron/sqlx"
	sitter "github.com/tree-sitter/go-tree-sitter"
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
	profile := BuildProjectProfile(projectRoot, nil, StageTreeSitter)
	return ParseFileIncrementalWithProfile(profile, filePath)
}

func ParseFileIncrementalWithProfile(profile ProjectProfile, filePath string) ([]*AstraMapNode, []*AstraMapEdge, string, error) {
	projectRoot := profile.ProjectRoot
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

	selection, supported := ResolveLanguageWithProfile(profile, filePath)
	if !supported {
		return nil, nil, contentHash, nil
	}
	lang := selection.ID
	now := time.Now().Unix()
	if selection.Module != nil {
		facts, parseErr := selection.Module.Parse(languageprotocol.ParseRequest{
			Language: lang, Dialect: selection.Dialect, ProjectRoot: projectRoot,
			RelativePath: relPath, ContentHash: contentHash, Source: codeBytes,
		})
		if parseErr != nil {
			return nil, nil, contentHash, parseErr
		}
		nodes, edges, graphErr := languageFactsToGraph(selection, facts, relPath, now)
		return nodes, edges, contentHash, graphErr
	}

	parser := sitter.NewParser()
	parser.SetLanguage(selection.Grammar)
	defer parser.Close()

	tree := parser.Parse(codeBytes, nil)
	if tree == nil {
		return nil, nil, contentHash, fmt.Errorf("tree-sitter parse failed")
	}
	defer tree.Close()

	rootNode := tree.RootNode()
	var nodes []*AstraMapNode
	var edges []*AstraMapEdge
	definedByName := make(map[string][]*AstraMapNode)
	definedByQName := make(map[string][]*AstraMapNode)
	definedByID := make(map[string]*AstraMapNode)
	var callCaptures []*sitter.Node
	var importCaptures []*sitter.Node

	resolveLocalDefinition := func(name string) (*AstraMapNode, bool) {
		candidates := definedByName[LanguageIdentity(lang, name)]
		if len(candidates) != 1 {
			return nil, false
		}
		return candidates[0], true
	}

	resolveContainer := func(name string) (*AstraMapNode, bool) {
		if candidates := definedByQName[LanguageIdentity(lang, name)]; len(candidates) == 1 {
			return candidates[0], true
		}
		return resolveLocalDefinition(name)
	}

	resolveCallDefinition := func(caller *AstraMapNode, name string) (*AstraMapNode, bool) {
		if caller != nil {
			qname := caller.QualifiedName
			separator := LanguageQualifiedSeparator(lang)
			if idx := strings.LastIndex(qname, separator); idx >= 0 {
				key := LanguageIdentity(lang, qname[:idx+len(separator)]+name)
				if candidates := definedByQName[key]; len(candidates) == 1 {
					return candidates[0], true
				}
			}
		}
		return resolveLocalDefinition(name)
	}

	type scopeFrame struct {
		Kind  string
		QName string
	}
	separator := LanguageQualifiedSeparator(lang)

	var collect func(n *sitter.Node, scope scopeFrame)
	collect = func(n *sitter.Node, scope scopeFrame) {
		if n == nil {
			return
		}

		nextScope := scope
		if _, isCall := callExpressionFields(lang, n.Kind()); isCall {
			callCaptures = append(callCaptures, n)
		}
		if isImportNode(lang, n.Kind()) {
			importCaptures = append(importCaptures, n)
		}
		rule, isDefinition := definitionRule(lang, n.Kind())
		if isDefinition {
			record, ok := normalizeDefinition(DefinitionContext{
				Node: n, Code: codeBytes, ScopeKind: scope.Kind, ScopeName: scope.QName,
			}, rule)
			if ok && record.Name != "" && record.Kind != "" {
				container := record.Container
				if container == "" {
					container = scope.QName
				}
				qname := record.Name
				if container != "" {
					qname = container + separator + record.Name
				}

				sig := firstSignatureLine(n, codeBytes)
				identity := LanguageIdentity(lang, qname)
				usn := fmt.Sprintf("%s:%s::%s%s", getLangPrefix(lang), relPath, identity, record.IdentitySuffix)
				if _, exists := definedByID[usn]; exists {
					usn = fmt.Sprintf("%s@%d", usn, int(n.StartPosition().Row)+1)
				}

				amNode := &AstraMapNode{
					ID: usn, Kind: record.Kind, Name: record.Name, QualifiedName: qname,
					FilePath: relPath, Language: lang,
					StartLine: int(n.StartPosition().Row) + 1, EndLine: int(n.EndPosition().Row) + 1,
					Signature: sig, Docstring: findLeadingComments(sourceLines, int(n.StartPosition().Row)+1),
					Provenance: "tree-sitter", UpdatedAt: now,
				}

				nodes = append(nodes, amNode)
				definedByID[usn] = amNode
				nameKey := LanguageIdentity(lang, record.Name)
				qnameKey := LanguageIdentity(lang, qname)
				definedByName[nameKey] = append(definedByName[nameKey], amNode)
				definedByQName[qnameKey] = append(definedByQName[qnameKey], amNode)

				parentID := fmt.Sprintf("file:%s", relPath)
				if container != "" {
					if parentNode, exists := resolveContainer(container); exists {
						parentID = parentNode.ID
					}
				}
				edges = append(edges, &AstraMapEdge{
					Source: parentID, Target: usn, Kind: "contains", Provenance: "tree-sitter",
				})

				if record.Scope {
					nextScope = scopeFrame{Kind: record.Kind, QName: qname}
				}
			}
		}

		for i := uint(0); i < n.ChildCount(); i++ {
			collect(n.Child(i), nextScope)
		}
	}

	initialKind, initialName := initialLanguageScope(lang, filePath, rootNode, codeBytes)
	collect(rootNode, scopeFrame{Kind: initialKind, QName: initialName})

	for _, extra := range supplementalDefinitions(lang, codeBytes, sourceLines) {
		nameKey := LanguageIdentity(lang, extra.Name)
		if len(definedByName[nameKey]) > 0 {
			continue
		}
		usn := fmt.Sprintf("%s:%s::%s", getLangPrefix(lang), relPath, extra.Name)
		amNode := &AstraMapNode{
			ID: usn, Kind: extra.Kind, Name: extra.Name, QualifiedName: extra.Name,
			FilePath: relPath, Language: lang, StartLine: extra.StartLine, EndLine: extra.EndLine,
			Signature: extra.Signature, Docstring: extra.Docstring, Provenance: "tree-sitter", UpdatedAt: now,
		}
		nodes = append(nodes, amNode)
		definedByID[usn] = amNode
		definedByName[nameKey] = append(definedByName[nameKey], amNode)
		definedByQName[LanguageIdentity(lang, extra.Name)] = append(definedByQName[LanguageIdentity(lang, extra.Name)], amNode)
		edges = append(edges, &AstraMapEdge{
			Source: fmt.Sprintf("file:%s", relPath), Target: usn, Kind: "contains", Provenance: "tree-sitter",
		})
	}

	scopes := newCallableScopeIndex(nodes)

	for _, callNode := range callCaptures {
		fields, _ := callExpressionFields(lang, callNode.Kind())
		var calleeNode *sitter.Node
		for _, field := range fields {
			if field == "$self" {
				calleeNode = callNode
				break
			}
			if field == "$first" {
				calleeNode = callNode.NamedChild(0)
				break
			}
			if calleeNode = callNode.ChildByFieldName(field); calleeNode != nil {
				break
			}
		}

		if calleeNode != nil {
			calleeName := normalizeCallee(lang, calleeNode, codeBytes)
			lineNum := int(callNode.StartPosition().Row) + 1
			if calleeName != "" && !isKeyword(calleeName) {
				if callerNode := scopes.Enclosing(lineNum); callerNode != nil {
					targetID := externalCallTargetID(lang, calleeName)
					if targetNode, exists := resolveCallDefinition(callerNode, calleeName); exists && targetNode.ID != callerNode.ID {
						targetID = targetNode.ID
					}
					if targetID != callerNode.ID {
						edges = append(edges, &AstraMapEdge{
							Source: callerNode.ID, Target: targetID, Kind: "calls", Provenance: "tree-sitter",
							Line: lineNum, Col: int(callNode.StartPosition().Column) + 1,
							Metadata: callMetadata(lang, callNode, codeBytes),
						})
					}
				}
			}
		}
	}

	for _, importNode := range importCaptures {
		for _, impPath := range importPaths(lang, importNode.Kind(), nodeText(importNode, codeBytes)) {
			if impPath == "" {
				continue
			}
			edges = append(edges, &AstraMapEdge{
				Source: fmt.Sprintf("file:%s", relPath), Target: fmt.Sprintf("import:%s", impPath),
				Kind: "imports", Provenance: "tree-sitter",
			})
		}
	}

	return nodes, edges, contentHash, nil
}

type callableInterval struct {
	node   *AstraMapNode
	maxEnd int
	left   *callableInterval
	right  *callableInterval
}

type callableScopeIndex struct {
	root *callableInterval
}

func newCallableScopeIndex(nodes []*AstraMapNode) callableScopeIndex {
	var callables []*AstraMapNode
	for _, node := range nodes {
		if node.Kind == "function" || node.Kind == "method" {
			callables = append(callables, node)
		}
	}
	sort.Slice(callables, func(i, j int) bool {
		if callables[i].StartLine == callables[j].StartLine {
			return callables[i].EndLine > callables[j].EndLine
		}
		return callables[i].StartLine < callables[j].StartLine
	})
	var build func([]*AstraMapNode) *callableInterval
	build = func(items []*AstraMapNode) *callableInterval {
		if len(items) == 0 {
			return nil
		}
		mid := len(items) / 2
		root := &callableInterval{node: items[mid], maxEnd: items[mid].EndLine}
		root.left = build(items[:mid])
		root.right = build(items[mid+1:])
		if root.left != nil && root.left.maxEnd > root.maxEnd {
			root.maxEnd = root.left.maxEnd
		}
		if root.right != nil && root.right.maxEnd > root.maxEnd {
			root.maxEnd = root.right.maxEnd
		}
		return root
	}
	return callableScopeIndex{root: build(callables)}
}

func (idx callableScopeIndex) Enclosing(line int) *AstraMapNode {
	var matched *AstraMapNode
	var visit func(*callableInterval)
	visit = func(current *callableInterval) {
		if current == nil || current.maxEnd < line {
			return
		}
		if current.left != nil && current.left.maxEnd >= line {
			visit(current.left)
		}
		node := current.node
		if node.StartLine <= line && line <= node.EndLine {
			if matched == nil || node.EndLine-node.StartLine < matched.EndLine-matched.StartLine {
				matched = node
			}
		}
		if node.StartLine <= line {
			visit(current.right)
		}
	}
	visit(idx.root)
	return matched
}

// ===== Tree-sitter Helper Functions =====

func normalizeDefinition(ctx DefinitionContext, rule DefinitionRule) (DefinitionRecord, bool) {
	if rule.Normalizer != nil {
		return rule.Normalizer(ctx, rule)
	}
	if ctx.Node == nil {
		return DefinitionRecord{}, false
	}
	nameNode := ctx.Node.ChildByFieldName(rule.NameField)
	if nameNode == nil {
		return DefinitionRecord{}, false
	}
	name := strings.TrimSpace(nodeText(nameNode, ctx.Code))
	return DefinitionRecord{
		Name: name, Kind: rule.Kind, Scope: rule.Scope, Callable: rule.Callable,
	}, name != "" && rule.Kind != ""
}

func firstSignatureLine(node *sitter.Node, code []byte) string {
	if node == nil {
		return ""
	}
	lines := strings.Split(nodeText(node, code), "\n")
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[0])
}

func normalizeGoMethodDefinition(ctx DefinitionContext, rule DefinitionRule) (DefinitionRecord, bool) {
	record, ok := normalizeDefinition(ctx, DefinitionRule{
		Kind: rule.Kind, NameField: rule.NameField, Scope: rule.Scope, Callable: rule.Callable,
	})
	if !ok {
		return DefinitionRecord{}, false
	}
	if receiverNode := ctx.Node.ChildByFieldName("receiver"); receiverNode != nil {
		receiver := extractGoReceiverStruct(nodeText(receiverNode, ctx.Code))
		if receiver != "" {
			record.Container = receiver
			if ctx.ScopeName != "" {
				record.Container = ctx.ScopeName + "." + receiver
			}
		}
	}
	return record, true
}

func normalizeGoTypeDefinition(ctx DefinitionContext, rule DefinitionRule) (DefinitionRecord, bool) {
	nameNode := ctx.Node.ChildByFieldName("name")
	typeNode := ctx.Node.ChildByFieldName("type")
	if nameNode == nil || typeNode == nil {
		return DefinitionRecord{}, false
	}
	kind := "type"
	scope := false
	switch typeNode.Kind() {
	case "struct_type":
		kind, scope = "struct", true
	case "interface_type":
		kind, scope = "interface", true
	}
	return DefinitionRecord{
		Name: strings.TrimSpace(nodeText(nameNode, ctx.Code)), Kind: kind, Scope: scope,
	}, true
}

func normalizePythonFunctionDefinition(ctx DefinitionContext, rule DefinitionRule) (DefinitionRecord, bool) {
	record, ok := normalizeDefinition(ctx, DefinitionRule{
		Kind: "function", NameField: rule.NameField, Scope: true, Callable: true,
	})
	if ok && ctx.ScopeKind == "class" {
		record.Kind = "method"
	}
	return record, ok
}

func normalizeScriptLexicalDefinition(ctx DefinitionContext, _ DefinitionRule) (DefinitionRecord, bool) {
	declarator := findDescendantByKind(ctx.Node, "variable_declarator")
	if declarator == nil {
		return DefinitionRecord{}, false
	}
	value := declarator.ChildByFieldName("value")
	if value == nil || (value.Kind() != "arrow_function" && value.Kind() != "function_expression") {
		return DefinitionRecord{}, false
	}
	nameNode := declarator.ChildByFieldName("name")
	if nameNode == nil {
		return DefinitionRecord{}, false
	}
	kind := "function"
	if ctx.ScopeKind == "class" {
		kind = "method"
	}
	return DefinitionRecord{
		Name: strings.TrimSpace(nodeText(nameNode, ctx.Code)), Kind: kind, Scope: true, Callable: true,
	}, true
}

func normalizeCTypeDefinition(ctx DefinitionContext, _ DefinitionRule) (DefinitionRecord, bool) {
	declarator := ctx.Node.ChildByFieldName("declarator")
	if declarator == nil {
		return DefinitionRecord{}, false
	}
	return DefinitionRecord{
		Name: extractDeclaratorIdentifier(declarator, ctx.Code), Kind: "type",
	}, true
}

func normalizeStandaloneCTypeDefinition(ctx DefinitionContext, rule DefinitionRule) (DefinitionRecord, bool) {
	if parent := ctx.Node.Parent(); parent != nil && parent.Kind() == "type_definition" {
		return DefinitionRecord{}, false
	}
	return normalizeDefinition(ctx, DefinitionRule{
		Kind: rule.Kind, NameField: rule.NameField, Scope: true,
	})
}

func normalizeCFunctionDefinition(ctx DefinitionContext, rule DefinitionRule) (DefinitionRecord, bool) {
	declarator := ctx.Node.ChildByFieldName("declarator")
	if declarator == nil || !containsNodeKind(declarator, "parameter_list") {
		return DefinitionRecord{}, false
	}
	name, container := extractCppFuncNameAndContainer(declarator, ctx.Code)
	if name == "" {
		return DefinitionRecord{}, false
	}
	kind := rule.Kind
	if container != "" || ctx.ScopeKind == "class" || ctx.ScopeKind == "struct" {
		kind = "method"
	}
	if container == "" {
		container = ctx.ScopeName
	} else if ctx.ScopeKind == "namespace" && ctx.ScopeName != "" && !strings.HasPrefix(container, ctx.ScopeName+"::") {
		container = ctx.ScopeName + "::" + container
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(nodeText(declarator, ctx.Code))))
	return DefinitionRecord{
		Name: name, Kind: kind, Container: container, Scope: true, Callable: true,
		IdentitySuffix: fmt.Sprintf("~%x", sum[:6]),
	}, true
}

func goInitialScope(_ string, root *sitter.Node, code []byte) (string, string) {
	if root == nil {
		return "", ""
	}
	for i := uint(0); i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child.Kind() != "package_clause" {
			continue
		}
		value := strings.TrimSpace(nodeText(child, code))
		return "module", strings.TrimSpace(strings.TrimPrefix(value, "package "))
	}
	return "", ""
}

func moduleInitialScope(filePath string, _ *sitter.Node, _ []byte) (string, string) {
	base := filepath.Base(filePath)
	return "module", strings.TrimSuffix(base, filepath.Ext(base))
}

func javaInitialScope(_ string, root *sitter.Node, code []byte) (string, string) {
	if root == nil {
		return "", ""
	}
	for i := uint(0); i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child.Kind() != "package_declaration" {
			continue
		}
		value := strings.TrimSpace(nodeText(child, code))
		value = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "package"), ";"))
		return "module", value
	}
	return "", ""
}

func supplementCMacroDefinitions(_ []byte, lines []string) []SupplementDefinition {
	pattern := regexp.MustCompile(`\b((?:(?:DECLARE|DEF|CREATE|IMPLEMENT)_[A-Z0-9_]+)|(?:[A-Z0-9_]+_(?:INIT|FUNC|REGISTER|ENTRY|HANDLER|CALLBACK)))\s*\(\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\)`)
	var result []SupplementDefinition
	inBlockComment := false
	for lineIndex, line := range lines {
		trimmed := strings.TrimSpace(line)
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
			continue
		}
		match := pattern.FindStringSubmatch(line)
		if len(match) != 3 {
			continue
		}
		if matchIndex := strings.Index(line, match[0]); matchIndex > 0 {
			prefix := line[:matchIndex]
			if strings.Contains(prefix, "//") || strings.Contains(prefix, "/*") {
				continue
			}
		}
		result = append(result, SupplementDefinition{
			Name: match[2], Kind: "function", Signature: trimmed,
			Docstring: fmt.Sprintf("由宏 %s 隐式生成的函数定义", match[1]),
			StartLine: lineIndex + 1, EndLine: lineIndex + 1,
		})
	}
	return result
}

func findDescendantByKind(node *sitter.Node, kind string) *sitter.Node {
	if node == nil {
		return nil
	}
	if node.Kind() == kind {
		return node
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		if found := findDescendantByKind(node.Child(i), kind); found != nil {
			return found
		}
	}
	return nil
}

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
	return LanguageIDPrefix(lang)
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

	type preparedFile struct {
		path  string
		lines []string
		funcs []funcNode
	}
	prepared := make([]preparedFile, 0, len(files))
	for _, fp := range files {
		if !filter.Allows(fp, StageTreeSitter) {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(projectRoot, fp))
		if readErr != nil {
			continue
		}
		var localFuncs []funcNode
		if selectErr := db.Select(&localFuncs, "SELECT id, start_line, end_line FROM astramap_nodes WHERE file_path = ? AND kind IN ('function', 'method')", fp); selectErr != nil {
			return fmt.Errorf("query callable ranges for %s: %w", fp, selectErr)
		}
		prepared = append(prepared, preparedFile{
			path:  fp,
			lines: strings.Split(string(data), "\n"),
			funcs: localFuncs,
		})
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

	for _, preparedFile := range prepared {
		fp := preparedFile.path
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

		inMultiLineComment := false
		for i, line := range preparedFile.lines {
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
			for _, lf := range preparedFile.funcs {
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
			if hasHiddenSegment(info.Name()) {
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
			if hasHiddenSegment(info.Name()) {
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
