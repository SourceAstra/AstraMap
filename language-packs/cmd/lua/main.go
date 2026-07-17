package main

import (
	"fmt"
	"os"
	"strings"

	"astramap-language-packs/internal/sdk"
	lua "github.com/tree-sitter-grammars/tree-sitter-lua/bindings/go"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

func main() {
	if err := sdk.Run(sdk.Spec{
		Manifest: sdk.Manifest("lua", "Lua", "lua", ".", sdk.SyntaxCapabilities()),
		Grammar:  func(string) *sitter.Language { return sitter.NewLanguage(lua.Language()) },
		Definitions: map[string]sdk.DefinitionRule{
			"function_declaration": sdk.NamedRule("function", "name", true, true),
			"variable_declaration": {Normalizer: sdk.AssignedFunctionDefinition},
		},
		Calls: map[string][]string{"function_call": {"name"}}, Imports: map[string]bool{"function_call": true},
		NormalizeCall: sdk.TextualCallee, CallMetadata: sdk.DynamicCallMetadata, ImportPaths: luaImports,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func luaImports(source string) []string {
	if !strings.HasPrefix(strings.TrimSpace(source), "require") {
		return nil
	}
	return sdk.QuotedPaths(source)
}
