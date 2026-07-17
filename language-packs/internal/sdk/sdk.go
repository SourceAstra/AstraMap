package sdk

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"astramap-standalone/languageprotocol"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

type DefinitionRecord struct {
	Name           string
	Kind           string
	Container      string
	IdentitySuffix string
	Scope          bool
	Callable       bool
}

type DefinitionContext struct {
	Node      *sitter.Node
	Code      []byte
	ScopeKind string
	ScopeName string
}

type DefinitionNormalizer func(DefinitionContext, DefinitionRule) (DefinitionRecord, bool)

type DefinitionRule struct {
	Kind       string
	NameField  string
	Scope      bool
	Callable   bool
	Normalizer DefinitionNormalizer
}

type Spec struct {
	Manifest      languageprotocol.Manifest
	Grammar       func(string) *sitter.Language
	Definitions   map[string]DefinitionRule
	Calls         map[string][]string
	Imports       map[string]bool
	NormalizeCall func(*sitter.Node, []byte) string
	CallMetadata  func(*sitter.Node, []byte) string
	ImportPaths   func(string) []string
	InitialScope  func(string, *sitter.Node, []byte) (string, string)
}

// WorkerVersion is injected from language.json by the package build script.
var WorkerVersion = "1.0.0"

func Manifest(id, displayName, prefix, separator string, capabilities languageprotocol.Capabilities) languageprotocol.Manifest {
	return languageprotocol.Manifest{
		Schema: 1, ID: id, Version: WorkerVersion, ProtocolMin: languageprotocol.Version,
		ProtocolMax: languageprotocol.Version, DisplayName: displayName, IDPrefix: prefix,
		QualifiedSeparator: separator, Capabilities: capabilities,
	}
}

func SyntaxCapabilities() languageprotocol.Capabilities {
	return languageprotocol.Capabilities{
		Definitions: true, Containers: true, LocalCalls: true, Imports: true, IncrementalSyntax: true,
	}
}

func SemanticCapabilities(full bool) languageprotocol.Capabilities {
	capabilities := SyntaxCapabilities()
	capabilities.CrossFileCalls = true
	capabilities.OverloadResolve = true
	capabilities.Implementations = full
	return capabilities
}

func Run(spec Spec) error {
	if spec.Manifest.ID == "" || spec.Grammar == nil {
		return fmt.Errorf("incomplete language worker specification")
	}
	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	for {
		var request languageprotocol.Request
		if err := languageprotocol.ReadFrame(reader, &request); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		response := languageprotocol.Response{ID: request.ID, Version: languageprotocol.Version}
		switch request.Method {
		case "handshake":
			if request.Handshake == nil || request.Handshake.CoreMin > languageprotocol.Version || request.Handshake.CoreMax < languageprotocol.Version {
				response.Error = "incompatible language protocol"
			} else {
				response.Handshake = &languageprotocol.HandshakeResponse{
					ModuleID: spec.Manifest.ID, Version: spec.Manifest.Version,
					Protocol: languageprotocol.Version, Capability: spec.Manifest.Capabilities,
				}
			}
		case "parse":
			if request.Parse == nil {
				response.Error = "missing parse request"
			} else {
				facts, err := Parse(spec, *request.Parse)
				if err != nil {
					response.Error = err.Error()
				} else {
					response.Parse = &facts
				}
			}
		case "shutdown":
			if err := languageprotocol.WriteFrame(writer, response); err != nil {
				return err
			}
			return writer.Flush()
		default:
			response.Error = "unknown language worker method"
		}
		if err := languageprotocol.WriteFrame(writer, response); err != nil {
			return err
		}
		if err := writer.Flush(); err != nil {
			return err
		}
	}
}

func Parse(spec Spec, request languageprotocol.ParseRequest) (languageprotocol.FileFacts, error) {
	if request.Language != spec.Manifest.ID {
		return languageprotocol.FileFacts{}, fmt.Errorf("worker %s cannot parse %s", spec.Manifest.ID, request.Language)
	}
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(spec.Grammar(request.Dialect)); err != nil {
		return languageprotocol.FileFacts{}, err
	}
	tree := parser.Parse(request.Source, nil)
	if tree == nil {
		return languageprotocol.FileFacts{}, fmt.Errorf("tree-sitter parse failed")
	}
	defer tree.Close()
	root := tree.RootNode()
	lines := strings.Split(string(request.Source), "\n")
	type scope struct {
		kind, name, localID string
	}
	initial := scope{}
	if spec.InitialScope != nil {
		initial.kind, initial.name = spec.InitialScope(request.RelativePath, root, request.Source)
	}
	facts := languageprotocol.FileFacts{Language: request.Language, Dialect: request.Dialect}
	definitionsByName := make(map[string][]string)
	var calls []*sitter.Node
	var imports []*sitter.Node
	var walk func(*sitter.Node, scope)
	walk = func(node *sitter.Node, current scope) {
		if node == nil {
			return
		}
		next := current
		if _, ok := spec.Calls[node.Kind()]; ok {
			calls = append(calls, node)
		}
		if spec.Imports[node.Kind()] {
			imports = append(imports, node)
		}
		if rule, ok := spec.Definitions[node.Kind()]; ok {
			record, valid := normalizeDefinition(DefinitionContext{
				Node: node, Code: request.Source, ScopeKind: current.kind, ScopeName: current.name,
			}, rule)
			if valid && record.Name != "" && record.Kind != "" {
				container := record.Container
				if container == "" {
					container = current.name
				}
				qualified := record.Name
				if container != "" {
					qualified = container + separator(spec.Manifest) + record.Name
				}
				localID := fmt.Sprintf("d:%d:%d:%s", node.StartPosition().Row+1, node.StartPosition().Column+1, qualified)
				fact := languageprotocol.DefinitionFact{
					LocalID: localID, ParentLocalID: current.localID, Kind: record.Kind, Name: record.Name,
					QualifiedName: qualified, StartLine: int(node.StartPosition().Row) + 1,
					EndLine: int(node.EndPosition().Row) + 1, Signature: firstSignatureLine(node, request.Source),
					Docstring:      leadingComments(lines, int(node.StartPosition().Row)+1),
					IdentitySuffix: record.IdentitySuffix, Callable: record.Callable,
				}
				facts.Definitions = append(facts.Definitions, fact)
				definitionsByName[identity(spec.Manifest, record.Name)] = append(definitionsByName[identity(spec.Manifest, record.Name)], localID)
				if record.Scope {
					next = scope{kind: record.Kind, name: qualified, localID: localID}
				}
			}
		}
		for i := uint(0); i < node.ChildCount(); i++ {
			walk(node.Child(i), next)
		}
	}
	walk(root, initial)
	callables := callableFacts(facts.Definitions)
	for _, node := range calls {
		fields := spec.Calls[node.Kind()]
		callee := callNode(node, fields)
		if callee == nil {
			continue
		}
		name := normalizeCallee(spec, callee, request.Source)
		if name == "" {
			continue
		}
		line := int(node.StartPosition().Row) + 1
		caller := enclosingCallable(callables, line)
		if caller == "" {
			continue
		}
		fact := languageprotocol.CallFact{
			CallerLocalID: caller, CalleeName: name, Line: line, Column: int(node.StartPosition().Column) + 1,
		}
		if matches := definitionsByName[identity(spec.Manifest, name)]; len(matches) == 1 {
			fact.CalleeLocalID = matches[0]
		}
		if spec.CallMetadata != nil {
			fact.Metadata = spec.CallMetadata(node, request.Source)
		}
		facts.Calls = append(facts.Calls, fact)
	}
	for _, node := range imports {
		if spec.ImportPaths == nil {
			continue
		}
		for _, path := range spec.ImportPaths(NodeText(node, request.Source)) {
			if path != "" {
				facts.Imports = append(facts.Imports, languageprotocol.ImportFact{Path: path, Line: int(node.StartPosition().Row) + 1})
			}
		}
	}
	return facts, nil
}

func NamedRule(kind, field string, scope bool, callable ...bool) DefinitionRule {
	return DefinitionRule{Kind: kind, NameField: field, Scope: scope, Callable: len(callable) > 0 && callable[0]}
}

func FixedRule(name, kind string, scope bool) DefinitionRule {
	return DefinitionRule{Normalizer: func(DefinitionContext, DefinitionRule) (DefinitionRecord, bool) {
		return DefinitionRecord{Name: name, Kind: kind, Scope: scope, Callable: kind == "function" || kind == "method"}, true
	}}
}

func DescendantRule(kind string, scope bool, names ...string) DefinitionRule {
	return DefinitionRule{Normalizer: func(ctx DefinitionContext, _ DefinitionRule) (DefinitionRecord, bool) {
		name := DescendantText(ctx.Node, ctx.Code, names...)
		return DefinitionRecord{Name: name, Kind: kind, Scope: scope}, name != ""
	}}
}

func DescendantCallable(names ...string) DefinitionNormalizer {
	return func(ctx DefinitionContext, _ DefinitionRule) (DefinitionRecord, bool) {
		name := DescendantText(ctx.Node, ctx.Code, names...)
		kind := "function"
		if isTypeScope(ctx.ScopeKind) {
			kind = "method"
		}
		return DefinitionRecord{Name: name, Kind: kind, Scope: true, Callable: true}, name != ""
	}
}

func AssignedFunctionDefinition(ctx DefinitionContext, _ DefinitionRule) (DefinitionRecord, bool) {
	if FindDescendant(ctx.Node, "function_definition") == nil {
		return DefinitionRecord{}, false
	}
	name := DescendantText(ctx.Node, ctx.Code, "identifier")
	return DefinitionRecord{Name: name, Kind: "function", Scope: true, Callable: true}, name != ""
}

func AssignedTypeDefinition(kind string) DefinitionNormalizer {
	return func(ctx DefinitionContext, _ DefinitionRule) (DefinitionRecord, bool) {
		parent := ctx.Node.Parent()
		if parent == nil || parent.Kind() != "variable_declaration" {
			return DefinitionRecord{}, false
		}
		name := DescendantText(parent, ctx.Code, "identifier")
		return DefinitionRecord{Name: name, Kind: kind, Scope: true}, name != ""
	}
}

func DescendantText(node *sitter.Node, code []byte, kinds ...string) string {
	for _, kind := range kinds {
		if child := FindDescendant(node, kind); child != nil {
			return strings.TrimSpace(NodeText(child, code))
		}
	}
	return ""
}

func FindDescendant(node *sitter.Node, kind string) *sitter.Node {
	if node == nil {
		return nil
	}
	for i := uint(0); i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child.Kind() == kind {
			return child
		}
		if found := FindDescendant(child, kind); found != nil {
			return found
		}
	}
	return nil
}

func NodeText(node *sitter.Node, code []byte) string {
	if node == nil {
		return ""
	}
	start, end := node.StartByte(), node.EndByte()
	if start > end || end > uint(len(code)) {
		return ""
	}
	return string(code[start:end])
}

var calleePattern = regexp.MustCompile(`(?:\.|::|\\)?([[:alpha:]_][[:alnum:]_!?]*)\s*(?:\(|$)`)

func TextualCallee(node *sitter.Node, code []byte) string {
	text := strings.TrimSpace(NodeText(node, code))
	matches := calleePattern.FindAllStringSubmatch(text, -1)
	if len(matches) > 0 {
		return matches[0][1]
	}
	return shortName(text)
}

func DynamicCallMetadata(node *sitter.Node, code []byte) string {
	if strings.ContainsAny(NodeText(node, code), "$#") {
		return "confidence=dynamic"
	}
	return "confidence=heuristic"
}

func KeywordPaths(keywords ...string) func(string) []string {
	return func(source string) []string {
		value := strings.TrimSpace(strings.TrimSuffix(source, ";"))
		lower := strings.ToLower(value)
		for _, keyword := range keywords {
			if strings.HasPrefix(lower, strings.ToLower(keyword)+" ") {
				value = strings.TrimSpace(value[len(keyword):])
				break
			}
		}
		if index := strings.Index(value, " as "); index >= 0 {
			value = strings.TrimSpace(value[:index])
		}
		if value == "" {
			return nil
		}
		return []string{value}
	}
}

func QuotedPaths(source string) []string {
	var result []string
	for _, quote := range []byte{'\'', '"'} {
		for start := 0; start < len(source); {
			left := strings.IndexByte(source[start:], quote)
			if left < 0 {
				break
			}
			left += start
			right := strings.IndexByte(source[left+1:], quote)
			if right < 0 {
				break
			}
			right += left + 1
			if value := strings.TrimSpace(source[left+1 : right]); value != "" {
				result = append(result, value)
			}
			start = right + 1
		}
	}
	return result
}

func normalizeDefinition(ctx DefinitionContext, rule DefinitionRule) (DefinitionRecord, bool) {
	if rule.Normalizer != nil {
		return rule.Normalizer(ctx, rule)
	}
	name := ctx.Node.ChildByFieldName(rule.NameField)
	if name == nil {
		return DefinitionRecord{}, false
	}
	value := strings.TrimSpace(NodeText(name, ctx.Code))
	return DefinitionRecord{Name: value, Kind: rule.Kind, Scope: rule.Scope, Callable: rule.Callable}, value != "" && rule.Kind != ""
}

func callNode(node *sitter.Node, fields []string) *sitter.Node {
	for _, field := range fields {
		switch field {
		case "$self":
			return node
		case "$first":
			return node.NamedChild(0)
		default:
			if child := node.ChildByFieldName(field); child != nil {
				return child
			}
		}
	}
	return nil
}

func normalizeCallee(spec Spec, node *sitter.Node, code []byte) string {
	if spec.NormalizeCall != nil {
		return spec.NormalizeCall(node, code)
	}
	return shortName(NodeText(node, code))
}

func shortName(value string) string {
	value = strings.TrimSpace(value)
	for _, separator := range []string{"::", "->", ".", "\\"} {
		if index := strings.LastIndex(value, separator); index >= 0 {
			value = value[index+len(separator):]
		}
	}
	return strings.TrimSpace(strings.Trim(value, "(){}[];"))
}

func separator(manifest languageprotocol.Manifest) string {
	if manifest.QualifiedSeparator != "" {
		return manifest.QualifiedSeparator
	}
	return "."
}

func identity(manifest languageprotocol.Manifest, value string) string {
	if manifest.CaseInsensitive {
		return strings.ToLower(value)
	}
	return value
}

type callableFact struct {
	id         string
	start, end int
}

func callableFacts(definitions []languageprotocol.DefinitionFact) []callableFact {
	var result []callableFact
	for _, fact := range definitions {
		if fact.Callable || fact.Kind == "function" || fact.Kind == "method" {
			result = append(result, callableFact{id: fact.LocalID, start: fact.StartLine, end: fact.EndLine})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].start == result[j].start {
			return result[i].end < result[j].end
		}
		return result[i].start > result[j].start
	})
	return result
}

func enclosingCallable(callables []callableFact, line int) string {
	matched := ""
	width := int(^uint(0) >> 1)
	for _, callable := range callables {
		if callable.start <= line && line <= callable.end && callable.end-callable.start < width {
			matched, width = callable.id, callable.end-callable.start
		}
	}
	return matched
}

func firstSignatureLine(node *sitter.Node, code []byte) string {
	line, _, _ := strings.Cut(NodeText(node, code), "\n")
	return strings.TrimSpace(line)
}

func leadingComments(lines []string, line int) string {
	var comments []string
	for index := line - 2; index >= 0; index-- {
		value := strings.TrimSpace(lines[index])
		if value == "" && len(comments) == 0 {
			continue
		}
		if strings.HasPrefix(value, "//") || strings.HasPrefix(value, "#") || strings.HasPrefix(value, "*") {
			comments = append(comments, strings.TrimSpace(strings.TrimLeft(value, "/*# ")))
			continue
		}
		break
	}
	for left, right := 0, len(comments)-1; left < right; left, right = left+1, right-1 {
		comments[left], comments[right] = comments[right], comments[left]
	}
	return strings.Join(comments, "\n")
}

func isTypeScope(kind string) bool {
	switch kind {
	case "class", "struct", "interface", "enum", "module", "namespace", "union":
		return true
	default:
		return false
	}
}
