package main

import (
	"fmt"
	"os"

	"astramap-language-packs/internal/sdk"
	sitter "github.com/tree-sitter/go-tree-sitter"
	scala "github.com/tree-sitter/tree-sitter-scala/bindings/go"
)

func main() {
	if err := sdk.Run(sdk.Spec{
		Manifest: sdk.Manifest("scala", "Scala", "scala", ".", sdk.SemanticCapabilities(true)),
		Grammar:  func(string) *sitter.Language { return sitter.NewLanguage(scala.Language()) },
		Definitions: map[string]sdk.DefinitionRule{
			"class_definition":     sdk.NamedRule("class", "name", true),
			"trait_definition":     sdk.NamedRule("interface", "name", true),
			"object_definition":    sdk.NamedRule("class", "name", true),
			"enum_definition":      sdk.NamedRule("enum", "name", true),
			"package_clause":       sdk.NamedRule("namespace", "name", true),
			"function_definition":  sdk.NamedRule("function", "name", true, true),
			"function_declaration": sdk.NamedRule("function", "name", true, true),
			"extension_definition": sdk.FixedRule("extension", "class", true),
			"given_definition":     sdk.DescendantRule("type", true, "identifier"),
			"type_definition":      sdk.NamedRule("type", "name", false),
		},
		Calls:       map[string][]string{"call_expression": {"function"}},
		Imports:     map[string]bool{"import_declaration": true, "export_declaration": true},
		ImportPaths: sdk.KeywordPaths("import", "export"), NormalizeCall: sdk.TextualCallee,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
