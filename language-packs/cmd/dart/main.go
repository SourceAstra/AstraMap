package main

import (
	"fmt"
	"os"
	"strings"

	"astramap-language-packs/internal/sdk"
	dart "github.com/UserNobody14/tree-sitter-dart/bindings/go"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

func main() {
	if err := sdk.Run(sdk.Spec{
		Manifest: sdk.Manifest("dart", "Dart", "dart", ".", sdk.SemanticCapabilities(false)),
		Grammar:  func(string) *sitter.Language { return sitter.NewLanguage(dart.Language()) },
		Definitions: map[string]sdk.DefinitionRule{
			"class_definition":           sdk.NamedRule("class", "name", true),
			"enum_declaration":           sdk.NamedRule("enum", "name", true),
			"extension_declaration":      sdk.NamedRule("class", "name", true),
			"extension_type_declaration": sdk.NamedRule("class", "name", true),
			"mixin_declaration":          sdk.DescendantRule("interface", true, "identifier"),
			"function_signature":         sdk.NamedRule("function", "name", true, true),
			"method_signature":           {Normalizer: sdk.DescendantCallable("identifier")},
			"constructor_signature":      {Normalizer: constructor},
		},
		Calls:         map[string][]string{"selector": {"$self"}, "constructor_invocation": {"$self"}},
		Imports:       map[string]bool{"import_or_export": true, "library_import": true},
		NormalizeCall: dartCallee, ImportPaths: sdk.QuotedPaths,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func constructor(ctx sdk.DefinitionContext, _ sdk.DefinitionRule) (sdk.DefinitionRecord, bool) {
	name := sdk.DescendantText(ctx.Node, ctx.Code, "identifier")
	if name == "" {
		name = "new"
	}
	return sdk.DefinitionRecord{Name: name, Kind: "method", Scope: true, Callable: true}, true
}

func dartCallee(node *sitter.Node, code []byte) string {
	if !strings.Contains(sdk.NodeText(node, code), "(") {
		return ""
	}
	return sdk.TextualCallee(node, code)
}
