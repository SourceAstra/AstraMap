package main

import (
	"fmt"
	"os"

	"astramap-language-packs/internal/sdk"
	sitter "github.com/tree-sitter/go-tree-sitter"
	visualbasic "github.com/tree-sitter/tree-sitter-tree_sitter_vb_dotnet/bindings/go"
)

func main() {
	manifest := sdk.Manifest("visualbasic", "Visual Basic", "vb", ".", sdk.SemanticCapabilities(false))
	manifest.Aliases = []string{"vb", "visual-basic"}
	manifest.CaseInsensitive = true
	if err := sdk.Run(sdk.Spec{
		Manifest: manifest, Grammar: func(string) *sitter.Language { return sitter.NewLanguage(visualbasic.Language()) },
		Definitions: map[string]sdk.DefinitionRule{
			"namespace_block":         sdk.NamedRule("namespace", "name", true),
			"module_block":            sdk.NamedRule("module", "name", true),
			"class_block":             sdk.NamedRule("class", "name", true),
			"structure_block":         sdk.NamedRule("struct", "name", true),
			"interface_block":         sdk.NamedRule("interface", "name", true),
			"enum_block":              sdk.NamedRule("enum", "name", true),
			"method_declaration":      sdk.NamedRule("method", "name", true, true),
			"delegate_declaration":    sdk.NamedRule("function", "name", true, true),
			"property_declaration":    sdk.NamedRule("property", "name", true),
			"constructor_declaration": sdk.FixedRule("new", "method", true),
		},
		Calls:   map[string][]string{"invocation": {"target"}, "call_statement": {"$self"}},
		Imports: map[string]bool{"imports_statement": true}, ImportPaths: sdk.KeywordPaths("imports"),
		NormalizeCall: sdk.TextualCallee,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
