package main

import (
	"fmt"
	"os"
	"strings"

	"astramap-language-packs/internal/sdk"
	zig "github.com/tree-sitter-grammars/tree-sitter-zig/bindings/go"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

func main() {
	if err := sdk.Run(sdk.Spec{
		Manifest: sdk.Manifest("zig", "Zig", "zig", ".", sdk.SyntaxCapabilities()),
		Grammar:  func(string) *sitter.Language { return sitter.NewLanguage(zig.Language()) },
		Definitions: map[string]sdk.DefinitionRule{
			"function_declaration": sdk.NamedRule("function", "name", true, true),
			"struct_declaration":   {Normalizer: sdk.AssignedTypeDefinition("struct")},
			"enum_declaration":     {Normalizer: sdk.AssignedTypeDefinition("enum")},
			"union_declaration":    {Normalizer: sdk.AssignedTypeDefinition("union")},
			"opaque_declaration":   {Normalizer: sdk.AssignedTypeDefinition("type")},
		},
		Calls: map[string][]string{"call_expression": {"function"}}, Imports: map[string]bool{"builtin_function": true},
		NormalizeCall: sdk.TextualCallee, ImportPaths: zigImports,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func zigImports(source string) []string {
	if !strings.HasPrefix(strings.TrimSpace(source), "@import") {
		return nil
	}
	return sdk.QuotedPaths(source)
}
