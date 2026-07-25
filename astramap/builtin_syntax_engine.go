// Copyright 2026 AstraMap Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the original license at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package astramap

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"astramap-standalone/languageprotocol"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

type syntaxDefinitionRecord struct {
	Name           string
	Kind           string
	Container      string
	IdentitySuffix string
	Scope          bool
	Callable       bool
}

type syntaxDefinitionContext struct {
	Node      *sitter.Node
	Code      []byte
	ScopeKind string
	ScopeName string
}

type syntaxDefinitionNormalizer func(syntaxDefinitionContext, syntaxDefinitionRule) (syntaxDefinitionRecord, bool)

type syntaxDefinitionRule struct {
	Kind       string
	NameField  string
	Scope      bool
	Callable   bool
	Normalizer syntaxDefinitionNormalizer
}

type treeSitterSpec struct {
	Manifest      languageprotocol.Manifest
	Grammar       func(string) *sitter.Language
	Definitions   map[string]syntaxDefinitionRule
	Calls         map[string][]string
	Imports       map[string]bool
	NormalizeCall func(*sitter.Node, []byte) string
	CallMetadata  func(*sitter.Node, []byte) string
	ImportPaths   func(string) []string
	InitialScope  func(string, *sitter.Node, []byte) (string, string)
	Supplement    func([]byte, []string) []syntaxSupplementDefinition
}

type syntaxSupplementDefinition struct {
	Name, Kind, Signature, Docstring string
	StartLine, EndLine               int
}

func parseTreeSitter(spec treeSitterSpec, request languageprotocol.ParseRequest, previous *cachedSyntaxTree) (languageprotocol.FileFacts, *sitter.Tree, error) {
	if request.Language != spec.Manifest.ID {
		return languageprotocol.FileFacts{}, nil, fmt.Errorf("worker %s cannot parse %s", spec.Manifest.ID, request.Language)
	}
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(spec.Grammar(request.Dialect)); err != nil {
		return languageprotocol.FileFacts{}, nil, err
	}
	var oldTree *sitter.Tree
	if previous != nil && previous.tree != nil {
		oldTree = previous.tree
		oldTree.Edit(syntaxInputEdit(previous.source, request.Source))
	}
	tree := parser.Parse(request.Source, oldTree)
	if tree == nil {
		return languageprotocol.FileFacts{Language: request.Language, Dialect: request.Dialect}, nil, nil
	}
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
			record, valid := normalizeDefinition(syntaxDefinitionContext{
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
					Docstring:      findLeadingComments(lines, int(node.StartPosition().Row)+1),
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
	if spec.Supplement != nil {
		for _, extra := range spec.Supplement(request.Source, lines) {
			if extra.Name == "" || extra.Kind == "" || len(definitionsByName[identity(spec.Manifest, extra.Name)]) > 0 {
				continue
			}
			localID := fmt.Sprintf("s:%d:%s", extra.StartLine, extra.Name)
			facts.Definitions = append(facts.Definitions, languageprotocol.DefinitionFact{
				LocalID: localID, Kind: extra.Kind, Name: extra.Name, QualifiedName: extra.Name,
				StartLine: extra.StartLine, EndLine: extra.EndLine, Signature: extra.Signature,
				Docstring: extra.Docstring, Callable: extra.Kind == "function" || extra.Kind == "method",
			})
			definitionsByName[identity(spec.Manifest, extra.Name)] = append(definitionsByName[identity(spec.Manifest, extra.Name)], localID)
		}
	}
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
		for _, path := range spec.ImportPaths(syntaxNodeText(node, request.Source)) {
			if path != "" {
				facts.Imports = append(facts.Imports, languageprotocol.ImportFact{Path: path, Line: int(node.StartPosition().Row) + 1})
			}
		}
	}
	return facts, tree, nil
}

func syntaxInputEdit(oldSource, newSource []byte) *sitter.InputEdit {
	prefix := 0
	for prefix < len(oldSource) && prefix < len(newSource) && oldSource[prefix] == newSource[prefix] {
		prefix++
	}
	oldSuffix, newSuffix := len(oldSource), len(newSource)
	for oldSuffix > prefix && newSuffix > prefix && oldSource[oldSuffix-1] == newSource[newSuffix-1] {
		oldSuffix--
		newSuffix--
	}
	return &sitter.InputEdit{
		StartByte: uint(prefix), OldEndByte: uint(oldSuffix), NewEndByte: uint(newSuffix),
		StartPosition:  syntaxPointAt(oldSource, prefix),
		OldEndPosition: syntaxPointAt(oldSource, oldSuffix),
		NewEndPosition: syntaxPointAt(newSource, newSuffix),
	}
}

func syntaxPointAt(source []byte, offset int) sitter.Point {
	point := sitter.Point{}
	for index := 0; index < offset; index++ {
		if source[index] == '\n' {
			point.Row++
			point.Column = 0
		} else {
			point.Column++
		}
	}
	return point
}

func syntaxNamedRule(kind, field string, scope bool, callable ...bool) syntaxDefinitionRule {
	return syntaxDefinitionRule{Kind: kind, NameField: field, Scope: scope, Callable: len(callable) > 0 && callable[0]}
}

func syntaxFixedRule(name, kind string, scope bool) syntaxDefinitionRule {
	return syntaxDefinitionRule{Normalizer: func(syntaxDefinitionContext, syntaxDefinitionRule) (syntaxDefinitionRecord, bool) {
		return syntaxDefinitionRecord{Name: name, Kind: kind, Scope: scope, Callable: kind == "function" || kind == "method"}, true
	}}
}

func syntaxDescendantRule(kind string, scope bool, names ...string) syntaxDefinitionRule {
	return syntaxDefinitionRule{Normalizer: func(ctx syntaxDefinitionContext, _ syntaxDefinitionRule) (syntaxDefinitionRecord, bool) {
		name := syntaxDescendantText(ctx.Node, ctx.Code, names...)
		return syntaxDefinitionRecord{Name: name, Kind: kind, Scope: scope}, name != ""
	}}
}

func syntaxDescendantCallable(names ...string) syntaxDefinitionNormalizer {
	return func(ctx syntaxDefinitionContext, _ syntaxDefinitionRule) (syntaxDefinitionRecord, bool) {
		name := syntaxDescendantText(ctx.Node, ctx.Code, names...)
		kind := "function"
		if isTypeScope(ctx.ScopeKind) {
			kind = "method"
		}
		return syntaxDefinitionRecord{Name: name, Kind: kind, Scope: true, Callable: true}, name != ""
	}
}

func syntaxAssignedFunctionDefinition(ctx syntaxDefinitionContext, _ syntaxDefinitionRule) (syntaxDefinitionRecord, bool) {
	if syntaxFindDescendant(ctx.Node, "function_definition") == nil {
		return syntaxDefinitionRecord{}, false
	}
	name := syntaxDescendantText(ctx.Node, ctx.Code, "identifier")
	return syntaxDefinitionRecord{Name: name, Kind: "function", Scope: true, Callable: true}, name != ""
}

func syntaxAssignedTypeDefinition(kind string) syntaxDefinitionNormalizer {
	return func(ctx syntaxDefinitionContext, _ syntaxDefinitionRule) (syntaxDefinitionRecord, bool) {
		parent := ctx.Node.Parent()
		if parent == nil || parent.Kind() != "variable_declaration" {
			return syntaxDefinitionRecord{}, false
		}
		name := syntaxDescendantText(parent, ctx.Code, "identifier")
		return syntaxDefinitionRecord{Name: name, Kind: kind, Scope: true}, name != ""
	}
}

func syntaxDescendantText(node *sitter.Node, code []byte, kinds ...string) string {
	for _, kind := range kinds {
		if child := syntaxFindDescendant(node, kind); child != nil {
			return strings.TrimSpace(syntaxNodeText(child, code))
		}
	}
	return ""
}

func syntaxFindDescendant(node *sitter.Node, kind string) *sitter.Node {
	if node == nil {
		return nil
	}
	for i := uint(0); i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child.Kind() == kind {
			return child
		}
		if found := syntaxFindDescendant(child, kind); found != nil {
			return found
		}
	}
	return nil
}

func syntaxNodeText(node *sitter.Node, code []byte) string {
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

func syntaxTextualCallee(node *sitter.Node, code []byte) string {
	text := strings.TrimSpace(syntaxNodeText(node, code))
	matches := calleePattern.FindAllStringSubmatch(text, -1)
	if len(matches) > 0 {
		return matches[0][1]
	}
	return shortName(text)
}

func syntaxDynamicCallMetadata(node *sitter.Node, code []byte) string {
	if strings.ContainsAny(syntaxNodeText(node, code), "$#") {
		return "confidence=dynamic"
	}
	return "confidence=heuristic"
}

func syntaxKeywordPaths(keywords ...string) func(string) []string {
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

func syntaxQuotedPaths(source string) []string {
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

func normalizeDefinition(ctx syntaxDefinitionContext, rule syntaxDefinitionRule) (syntaxDefinitionRecord, bool) {
	if rule.Normalizer != nil {
		return rule.Normalizer(ctx, rule)
	}
	name := ctx.Node.ChildByFieldName(rule.NameField)
	if name == nil {
		return syntaxDefinitionRecord{}, false
	}
	value := strings.TrimSpace(syntaxNodeText(name, ctx.Code))
	return syntaxDefinitionRecord{Name: value, Kind: rule.Kind, Scope: rule.Scope, Callable: rule.Callable}, value != "" && rule.Kind != ""
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

func normalizeCallee(spec treeSitterSpec, node *sitter.Node, code []byte) string {
	if spec.NormalizeCall != nil {
		return spec.NormalizeCall(node, code)
	}
	return shortName(syntaxNodeText(node, code))
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
	line, _, _ := strings.Cut(syntaxNodeText(node, code), "\n")
	return strings.TrimSpace(line)
}

func isTypeScope(kind string) bool {
	switch kind {
	case "class", "struct", "interface", "enum", "module", "namespace", "union":
		return true
	default:
		return false
	}
}
