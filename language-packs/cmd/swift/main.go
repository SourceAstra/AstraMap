package main

import (
	"fmt"
	"os"
	"strings"

	"astramap-language-packs/internal/sdk"
	swift "github.com/alex-pinkus/tree-sitter-swift/bindings/go"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

func main() {
	if err := sdk.Run(sdk.Spec{
		Manifest: sdk.Manifest("swift", "Swift", "swift", ".", sdk.SyntaxCapabilities()),
		Grammar:  func(string) *sitter.Language { return sitter.NewLanguage(swift.Language()) },
		Definitions: map[string]sdk.DefinitionRule{
			"class_declaration":             {Normalizer: swiftType},
			"protocol_declaration":          sdk.NamedRule("interface", "name", true),
			"function_declaration":          sdk.NamedRule("function", "name", true, true),
			"protocol_function_declaration": sdk.NamedRule("method", "name", true, true),
			"init_declaration":              sdk.FixedRule("init", "method", true),
			"deinit_declaration":            sdk.FixedRule("deinit", "method", true),
			"subscript_declaration":         sdk.FixedRule("subscript", "method", true),
			"typealias_declaration":         sdk.NamedRule("type", "name", false),
		},
		Calls:   map[string][]string{"call_expression": {"$self"}, "constructor_expression": {"$self"}},
		Imports: map[string]bool{"import_declaration": true}, ImportPaths: sdk.KeywordPaths("import"),
		NormalizeCall: sdk.TextualCallee,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func swiftType(ctx sdk.DefinitionContext, _ sdk.DefinitionRule) (sdk.DefinitionRecord, bool) {
	nameNode := ctx.Node.ChildByFieldName("name")
	kindNode := ctx.Node.ChildByFieldName("declaration_kind")
	if nameNode == nil || kindNode == nil {
		return sdk.DefinitionRecord{}, false
	}
	kind := strings.TrimSpace(sdk.NodeText(kindNode, ctx.Code))
	if kind == "extension" || kind == "actor" {
		kind = "class"
	}
	return sdk.DefinitionRecord{Name: sdk.NodeText(nameNode, ctx.Code), Kind: kind, Scope: true}, true
}
