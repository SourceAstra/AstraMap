package main

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	"astramap-language-packs/internal/sdk"
	sitter "github.com/tree-sitter/go-tree-sitter"
	ruby "github.com/tree-sitter/tree-sitter-ruby/bindings/go"
)

func main() {
	manifest := sdk.Manifest("ruby", "Ruby", "rb", "::", sdk.SemanticCapabilities(true))
	manifest.Aliases = []string{"rb"}
	if err := sdk.Run(sdk.Spec{
		Manifest: manifest, Grammar: grammar(ruby.Language),
		Definitions: map[string]sdk.DefinitionRule{
			"class": sdk.NamedRule("class", "name", true), "module": sdk.NamedRule("module", "name", true),
			"method":           sdk.NamedRule("function", "name", true, true),
			"singleton_method": {Normalizer: singletonMethod}, "singleton_class": sdk.FixedRule("singleton", "class", true),
		},
		Calls: map[string][]string{"call": {"method"}}, Imports: map[string]bool{"call": true},
		NormalizeCall: sdk.TextualCallee, CallMetadata: sdk.DynamicCallMetadata, ImportPaths: rubyImports,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func singletonMethod(ctx sdk.DefinitionContext, _ sdk.DefinitionRule) (sdk.DefinitionRecord, bool) {
	name, container := "", ""
	if node := ctx.Node.ChildByFieldName("name"); node != nil {
		name = sdk.NodeText(node, ctx.Code)
	}
	if node := ctx.Node.ChildByFieldName("object"); node != nil {
		container = sdk.NodeText(node, ctx.Code)
	}
	return sdk.DefinitionRecord{Name: name, Kind: "method", Container: container, Scope: true, Callable: true}, name != ""
}

func rubyImports(source string) []string {
	trimmed := strings.TrimSpace(source)
	if !strings.HasPrefix(trimmed, "require ") && !strings.HasPrefix(trimmed, "require_relative ") {
		return nil
	}
	return sdk.QuotedPaths(trimmed)
}

func grammar(factory func() unsafe.Pointer) func(string) *sitter.Language {
	return func(string) *sitter.Language { return sitter.NewLanguage(factory()) }
}
