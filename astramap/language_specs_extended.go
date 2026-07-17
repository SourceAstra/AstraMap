package astramap

import (
	"path/filepath"
	"regexp"
	"strings"
	"unsafe"

	kotlin "github.com/fwcd/tree-sitter-kotlin/bindings/go"
	sitter "github.com/tree-sitter/go-tree-sitter"
	bash "github.com/tree-sitter/tree-sitter-bash/bindings/go"
	csharp "github.com/tree-sitter/tree-sitter-c-sharp/bindings/go"
	php "github.com/tree-sitter/tree-sitter-php/bindings/go"
	rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
)

func extendedLanguages() []LanguageSpec {
	return []LanguageSpec{
		rustLanguage(),
		csharpLanguage(),
		kotlinLanguage(),
		phpLanguage(),
		bashLanguage(),
	}
}

func rustLanguage() LanguageSpec {
	return LanguageSpec{
		ID: "rust", DisplayName: "Rust", Aliases: []string{"rs"}, IDPrefix: "rs",
		QualifiedSeparator: "::", Extensions: []string{".rs"},
		Semantic: &SemanticBinding{ProviderID: "rust", Mode: "stable"}, Capabilities: semanticCapabilities,
		Toolchain: []ToolchainRequirement{
			{Label: "Rust toolchain", Commands: []string{"cargo", "rustc"}, InstallHint: "https://rustup.rs"},
		},
		grammar: grammarFactory(rust.Language),
		syntax: SyntaxSpec{
			Definitions: map[string]DefinitionRule{
				"function_item":    {Normalizer: rustCallableDefinition},
				"struct_item":      namedRule("struct", "name", true),
				"enum_item":        namedRule("enum", "name", true),
				"trait_item":       namedRule("interface", "name", true),
				"type_item":        namedRule("type", "name", false),
				"mod_item":         namedRule("module", "name", true),
				"macro_definition": namedRule("macro", "name", false),
			},
			Calls:         map[string][]string{"call_expression": {"function"}},
			Imports:       map[string]bool{"use_declaration": true, "extern_crate_declaration": true},
			ImportPaths:   rustImportPaths,
			InitialScope:  rustInitialScope,
			NormalizeCall: textualCallee,
		},
	}
}

func csharpLanguage() LanguageSpec {
	return LanguageSpec{
		ID: "csharp", DisplayName: "C#", Aliases: []string{"cs", "c#"}, IDPrefix: "cs",
		QualifiedSeparator: ".", Extensions: []string{".cs"},
		Semantic: &SemanticBinding{ProviderID: "dotnet", Mode: "stable"}, Capabilities: semanticCapabilities,
		Toolchain: []ToolchainRequirement{
			{Label: ".NET SDK", Commands: []string{"dotnet"}, InstallHint: "https://dotnet.microsoft.com/download"},
		},
		grammar: grammarFactory(csharp.Language),
		syntax: namedTypeSyntax(
			map[string]string{
				"class_declaration": "class", "record_declaration": "class", "struct_declaration": "struct",
				"interface_declaration": "interface", "enum_declaration": "enum",
				"namespace_declaration": "namespace", "file_scoped_namespace_declaration": "namespace",
			},
			map[string]string{
				"method_declaration": "method", "constructor_declaration": "method",
				"local_function_statement": "function", "delegate_declaration": "function",
				"operator_declaration": "method", "conversion_operator_declaration": "method",
			},
			map[string][]string{"invocation_expression": {"function"}, "object_creation_expression": {"type"}},
			map[string]bool{"using_directive": true}, normalizeUsingImports,
		),
	}
}

func kotlinLanguage() LanguageSpec {
	syntax := SyntaxSpec{
		Definitions: map[string]DefinitionRule{
			"class_declaration":  {Normalizer: kotlinTypeDefinition},
			"object_declaration": descendantRule("class", true, "type_identifier"),
			"companion_object":   fixedRule("companion", "class", true),
			"function_declaration": {
				Normalizer: kotlinCallableDefinition,
			},
			"secondary_constructor": fixedRule("constructor", "method", true),
			"primary_constructor":   fixedRule("constructor", "method", true),
			"type_alias":            descendantRule("type", false, "type_identifier", "simple_identifier"),
		},
		Calls:         map[string][]string{"call_expression": {"$self"}, "constructor_invocation": {"$self"}},
		Imports:       map[string]bool{"import_header": true},
		NormalizeCall: textualCallee,
		ImportPaths:   keywordPathNormalizer("import"),
	}
	return LanguageSpec{
		ID: "kotlin", DisplayName: "Kotlin", Aliases: []string{"kt"}, IDPrefix: "kt",
		QualifiedSeparator: ".", Extensions: []string{".kt", ".kts"},
		Detection: DetectionSpec{Extensions: map[string]string{".kt": "kotlin", ".kts": "kotlin-script"}},
		Semantic:  &SemanticBinding{ProviderID: "java", Mode: "stable"}, Capabilities: semanticCapabilities,
		Toolchain: []ToolchainRequirement{
			{Label: "JDK", Commands: []string{"java", "javac"}, InstallHint: "https://adoptium.net"},
		},
		grammar: grammarFactory(kotlin.Language), syntax: syntax,
	}
}

func phpLanguage() LanguageSpec {
	return LanguageSpec{
		ID: "php", DisplayName: "PHP", IDPrefix: "php", QualifiedSeparator: "\\",
		Extensions: []string{".php", ".phtml"},
		Detection:  DetectionSpec{Extensions: map[string]string{".php": "php-only", ".phtml": "php"}},
		Semantic:   &SemanticBinding{ProviderID: "php", Mode: "experimental"}, Capabilities: semanticCapabilities,
		Toolchain: []ToolchainRequirement{
			{Label: "PHP", Commands: []string{"php"}, InstallHint: "https://www.php.net/downloads"},
			{Label: "Composer", Commands: []string{"composer"}, InstallHint: "https://getcomposer.org/download/", WhenAnyFiles: []string{"composer.json"}},
		},
		grammar: phpGrammar,
		syntax: SyntaxSpec{
			Definitions: map[string]DefinitionRule{
				"namespace_definition":  namedRule("namespace", "name", true),
				"class_declaration":     namedRule("class", "name", true),
				"interface_declaration": namedRule("interface", "name", true),
				"trait_declaration":     namedRule("interface", "name", true),
				"enum_declaration":      namedRule("enum", "name", true),
				"function_definition":   namedRule("function", "name", true, true),
				"method_declaration":    namedRule("method", "name", true, true),
			},
			Calls: map[string][]string{
				"function_call_expression": {"function"}, "member_call_expression": {"name"},
				"nullsafe_member_call_expression": {"name"}, "scoped_call_expression": {"name"},
				"object_creation_expression": {"$self"},
			},
			Imports: map[string]bool{
				"namespace_use_declaration": true, "include_expression": true, "include_once_expression": true,
				"require_expression": true, "require_once_expression": true,
			},
			NormalizeCall: textualCallee, CallMetadata: dynamicCallMetadata, ImportPaths: phpImportPaths,
		},
	}
}

func bashLanguage() LanguageSpec {
	return LanguageSpec{
		ID: "bash", DisplayName: "Bash", Aliases: []string{"sh", "shell"}, IDPrefix: "sh",
		QualifiedSeparator: ".", Extensions: []string{".sh", ".bash"},
		Detection: DetectionSpec{
			Filenames: map[string]string{".bashrc": "bash", ".bash_profile": "bash", ".profile": "shell"},
			Shebangs: []ShebangRule{{
				Contains: []string{"/bash", "/sh", "env bash", "env sh"}, Dialect: "shell",
			}},
		},
		Capabilities: syntaxCapabilities, grammar: grammarFactory(bash.Language),
		syntax: SyntaxSpec{
			Definitions: map[string]DefinitionRule{"function_definition": namedRule("function", "name", true, true)},
			Calls:       map[string][]string{"command": {"name"}}, Imports: map[string]bool{"command": true},
			NormalizeCall: textualCallee, CallMetadata: shellCallMetadata, ImportPaths: shellImportPaths,
		},
	}
}

func grammarFactory(factory func() unsafe.Pointer) func(string) *sitter.Language {
	return func(string) *sitter.Language { return sitter.NewLanguage(factory()) }
}

func phpGrammar(dialect string) *sitter.Language {
	if dialect == "php" {
		return sitter.NewLanguage(php.LanguagePHP())
	}
	return sitter.NewLanguage(php.LanguagePHPOnly())
}

func namedRule(kind, field string, scope bool, callable ...bool) DefinitionRule {
	return DefinitionRule{Kind: kind, NameField: field, Scope: scope, Callable: len(callable) > 0 && callable[0]}
}

func fixedRule(name, kind string, scope bool) DefinitionRule {
	return DefinitionRule{Normalizer: func(DefinitionContext, DefinitionRule) (DefinitionRecord, bool) {
		return DefinitionRecord{Name: name, Kind: kind, Scope: scope, Callable: kind == "function" || kind == "method"}, true
	}}
}

func descendantRule(kind string, scope bool, names ...string) DefinitionRule {
	return DefinitionRule{Normalizer: func(ctx DefinitionContext, _ DefinitionRule) (DefinitionRecord, bool) {
		name := descendantText(ctx.Node, ctx.Code, names...)
		return DefinitionRecord{Name: name, Kind: kind, Scope: scope}, name != ""
	}}
}

func descendantCallable(names ...string) DefinitionNormalizer {
	return func(ctx DefinitionContext, _ DefinitionRule) (DefinitionRecord, bool) {
		name := descendantText(ctx.Node, ctx.Code, names...)
		if name == "" {
			return DefinitionRecord{}, false
		}
		kind := "function"
		if isTypeScope(ctx.ScopeKind) {
			kind = "method"
		}
		return DefinitionRecord{Name: name, Kind: kind, Scope: true, Callable: true}, true
	}
}

func namedTypeSyntax(
	types map[string]string,
	callables map[string]string,
	calls map[string][]string,
	imports map[string]bool,
	importPath func(string) []string,
) SyntaxSpec {
	definitions := make(map[string]DefinitionRule, len(types)+len(callables))
	for node, kind := range types {
		definitions[node] = namedRule(kind, "name", true)
	}
	for node, kind := range callables {
		definitions[node] = namedRule(kind, "name", true, true)
	}
	return SyntaxSpec{
		Definitions: definitions, Calls: calls, Imports: imports,
		NormalizeCall: textualCallee, ImportPaths: importPath,
	}
}

func descendantText(node *sitter.Node, code []byte, kinds ...string) string {
	for _, kind := range kinds {
		if child := findDescendantByKind(node, kind); child != nil && child != node {
			return strings.TrimSpace(nodeText(child, code))
		}
	}
	return ""
}

func isTypeScope(kind string) bool {
	switch kind {
	case "class", "struct", "interface", "enum", "module":
		return true
	default:
		return false
	}
}

func ancestorByKind(node *sitter.Node, kind string) *sitter.Node {
	for current := node.Parent(); current != nil; current = current.Parent() {
		if current.Kind() == kind {
			return current
		}
	}
	return nil
}

func rustCallableDefinition(ctx DefinitionContext, _ DefinitionRule) (DefinitionRecord, bool) {
	name := descendantText(ctx.Node, ctx.Code, "identifier")
	if name == "" {
		return DefinitionRecord{}, false
	}
	record := DefinitionRecord{Name: name, Kind: "function", Scope: true, Callable: true}
	if impl := ancestorByKind(ctx.Node, "impl_item"); impl != nil {
		record.Kind = "method"
		if target := impl.ChildByFieldName("type"); target != nil {
			record.Container = strings.TrimSpace(nodeText(target, ctx.Code))
		}
	}
	return record, true
}

func kotlinTypeDefinition(ctx DefinitionContext, _ DefinitionRule) (DefinitionRecord, bool) {
	name := descendantText(ctx.Node, ctx.Code, "type_identifier")
	if name == "" {
		return DefinitionRecord{}, false
	}
	source := strings.TrimSpace(nodeText(ctx.Node, ctx.Code))
	kind := "class"
	switch {
	case strings.Contains(source, "interface "+name):
		kind = "interface"
	case strings.Contains(source, "enum class "+name):
		kind = "enum"
	}
	return DefinitionRecord{Name: name, Kind: kind, Scope: true}, true
}

func kotlinCallableDefinition(ctx DefinitionContext, _ DefinitionRule) (DefinitionRecord, bool) {
	record, ok := descendantCallable("simple_identifier")(ctx, DefinitionRule{})
	if !ok {
		return record, false
	}
	if receiver := ctx.Node.ChildByFieldName("receiver"); receiver != nil {
		record.Kind = "method"
		record.Container = strings.TrimSpace(nodeText(receiver, ctx.Code))
	}
	return record, true
}

var calleeTextPattern = regexp.MustCompile(`(?:\.|::|\\)?([[:alpha:]_][[:alnum:]_!?]*)\s*(?:\(|$)`)

func textualCallee(node *sitter.Node, code []byte) string {
	text := strings.TrimSpace(nodeText(node, code))
	matches := calleeTextPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return extractCalleeShortName(node, code)
	}
	return matches[0][1]
}

func dynamicCallMetadata(node *sitter.Node, code []byte) string {
	text := nodeText(node, code)
	if strings.ContainsAny(text, "$#") {
		return "confidence=dynamic"
	}
	return "confidence=heuristic"
}

func shellCallMetadata(node *sitter.Node, code []byte) string {
	text := nodeText(node, code)
	if strings.ContainsAny(text, "$`") || strings.Contains(text, "$(") {
		return "confidence=dynamic"
	}
	return "confidence=heuristic"
}

func keywordPathNormalizer(keywords ...string) func(string) []string {
	return func(source string) []string {
		value := strings.TrimSpace(strings.TrimSuffix(source, ";"))
		lower := strings.ToLower(value)
		for _, keyword := range keywords {
			if strings.HasPrefix(lower, strings.ToLower(keyword)+" ") {
				value = strings.TrimSpace(value[len(keyword):])
				break
			}
		}
		if idx := strings.Index(value, " as "); idx >= 0 {
			value = strings.TrimSpace(value[:idx])
		}
		if value == "" {
			return nil
		}
		return []string{value}
	}
}

func normalizeUsingImports(source string) []string {
	value := strings.TrimSpace(strings.TrimSuffix(source, ";"))
	value = strings.TrimSpace(strings.TrimPrefix(value, "global"))
	value = strings.TrimSpace(strings.TrimPrefix(value, "using"))
	if idx := strings.Index(value, "="); idx >= 0 {
		value = strings.TrimSpace(value[idx+1:])
	}
	if strings.HasPrefix(value, "static ") {
		value = strings.TrimSpace(strings.TrimPrefix(value, "static"))
	}
	if value == "" {
		return nil
	}
	return []string{value}
}

func rustImportPaths(source string) []string {
	value := strings.TrimSpace(strings.TrimSuffix(source, ";"))
	value = strings.TrimSpace(strings.TrimPrefix(value, "use"))
	value = strings.TrimSpace(strings.TrimPrefix(value, "extern crate"))
	open, close := strings.Index(value, "{"), strings.LastIndex(value, "}")
	if open < 0 || close <= open {
		return nonEmptyPath(value)
	}
	prefix := strings.TrimSuffix(strings.TrimSpace(value[:open]), "::")
	var result []string
	for _, item := range strings.Split(value[open+1:close], ",") {
		item = strings.TrimSpace(strings.SplitN(item, " as ", 2)[0])
		if item != "" {
			result = append(result, strings.Trim(strings.Join([]string{prefix, item}, "::"), ":"))
		}
	}
	return result
}

func phpImportPaths(source string) []string {
	trimmed := strings.TrimSpace(source)
	if strings.HasPrefix(trimmed, "use ") {
		return keywordPathNormalizer("use")(trimmed)
	}
	return quotedImportPaths(trimmed)
}

func shellImportPaths(source string) []string {
	fields := strings.Fields(strings.TrimSpace(source))
	if len(fields) < 2 || (fields[0] != "source" && fields[0] != ".") {
		return nil
	}
	return nonEmptyPath(strings.Trim(fields[1], `"'`))
}

func nonEmptyPath(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return []string{value}
}

func rustInitialScope(filePath string, _ *sitter.Node, _ []byte) (string, string) {
	base := filepath.Base(filePath)
	switch base {
	case "main.rs", "lib.rs", "mod.rs":
		return "module", filepath.Base(filepath.Dir(filePath))
	default:
		return "module", strings.TrimSuffix(base, filepath.Ext(base))
	}
}
