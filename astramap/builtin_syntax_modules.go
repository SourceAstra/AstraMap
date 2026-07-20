package astramap

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unsafe"

	"astramap-standalone/languageprotocol"
	kotlin "github.com/fwcd/tree-sitter-kotlin/bindings/go"
	sitter "github.com/tree-sitter/go-tree-sitter"
	csharp "github.com/tree-sitter/tree-sitter-c-sharp/bindings/go"
	c "github.com/tree-sitter/tree-sitter-c/bindings/go"
	cpp "github.com/tree-sitter/tree-sitter-cpp/bindings/go"
	golang "github.com/tree-sitter/tree-sitter-go/bindings/go"
	java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	ruby "github.com/tree-sitter/tree-sitter-ruby/bindings/go"
	rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	scala "github.com/tree-sitter/tree-sitter-scala/bindings/go"
	typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

type builtinTreeSitterModule struct {
	spec       treeSitterSpec
	mu         sync.Mutex
	trees      map[string]cachedSyntaxTree
	cacheOrder []string
}

type cachedSyntaxTree struct {
	dialect string
	source  []byte
	tree    *sitter.Tree
}

func (m *builtinTreeSitterModule) Manifest() languageprotocol.Manifest {
	return m.spec.Manifest
}

func (m *builtinTreeSitterModule) Parse(request languageprotocol.ParseRequest) (languageprotocol.FileFacts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := request.ProjectRoot + "\x00" + filepath.ToSlash(request.RelativePath)
	previous, reusable := m.trees[key]
	if reusable && previous.dialect != request.Dialect {
		previous.tree.Close()
		delete(m.trees, key)
		reusable = false
	}
	var old *cachedSyntaxTree
	if reusable {
		old = &previous
	}
	facts, tree, err := parseTreeSitter(m.spec, request, old)
	if reusable {
		previous.tree.Close()
	}
	if err != nil {
		delete(m.trees, key)
		m.removeCacheKey(key)
		return languageprotocol.FileFacts{}, err
	}
	if m.trees == nil {
		m.trees = make(map[string]cachedSyntaxTree)
	}
	m.trees[key] = cachedSyntaxTree{dialect: request.Dialect, source: append([]byte(nil), request.Source...), tree: tree}
	m.touchCacheKey(key)
	return facts, nil
}

func (m *builtinTreeSitterModule) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, entry := range m.trees {
		entry.tree.Close()
		delete(m.trees, key)
	}
	m.cacheOrder = nil
	return nil
}

func (m *builtinTreeSitterModule) touchCacheKey(key string) {
	m.removeCacheKey(key)
	m.cacheOrder = append(m.cacheOrder, key)
	const maxCachedTrees = 64
	if len(m.cacheOrder) <= maxCachedTrees {
		return
	}
	evicted := m.cacheOrder[0]
	m.cacheOrder = m.cacheOrder[1:]
	if entry, ok := m.trees[evicted]; ok {
		entry.tree.Close()
		delete(m.trees, evicted)
	}
}

func (m *builtinTreeSitterModule) removeCacheKey(key string) {
	for index, cached := range m.cacheOrder {
		if cached == key {
			m.cacheOrder = append(m.cacheOrder[:index], m.cacheOrder[index+1:]...)
			return
		}
	}
}

func attachBuiltinSyntaxModules() {
	for index := range builtinLanguages {
		spec := &builtinLanguages[index]
		module := builtinSyntaxModule(spec.ID, spec.DisplayName, spec.IDPrefix, spec.QualifiedSeparator)
		if module == nil {
			panic("missing built-in Tree-sitter module: " + spec.ID)
		}
		spec.module = module
		spec.Capabilities.Containers = true
		spec.Capabilities.LocalCalls = true
		spec.Capabilities.Imports = true
		spec.Capabilities.IncrementalSyntax = true
	}
}

func builtinSyntaxModule(id, displayName, prefix, separator string) languageModule {
	spec, ok := builtinTreeSitterSpec(id)
	if !ok {
		return nil
	}
	spec.Manifest = languageprotocol.Manifest{
		Schema: 1, ID: id, Version: "builtin-1", ProtocolMin: languageprotocol.Version,
		ProtocolMax: languageprotocol.Version, DisplayName: displayName, IDPrefix: prefix,
		QualifiedSeparator: separator,
		Capabilities: languageprotocol.Capabilities{
			Definitions: true, Containers: true, LocalCalls: true, Imports: true, IncrementalSyntax: true,
		},
	}
	return &builtinTreeSitterModule{spec: spec}
}

func builtinTreeSitterSpec(id string) (treeSitterSpec, bool) {
	switch id {
	case "go":
		return goSyntaxSpec(), true
	case "typescript", "javascript":
		return scriptTreeSitterSpec(), true
	case "python":
		return pythonSyntaxSpec(), true
	case "java":
		return javaSyntaxSpec(), true
	case "c":
		spec := cFamilyTreeSitterSpec()
		spec.Grammar = syntaxGrammar(c.Language)
		return spec, true
	case "cpp":
		spec := cFamilyTreeSitterSpec()
		spec.Grammar = syntaxGrammar(cpp.Language)
		return spec, true
	case "rust":
		return rustSyntaxSpec(), true
	case "csharp":
		return csharpSyntaxSpec(), true
	case "kotlin":
		return kotlinSyntaxSpec(), true
	case "ruby":
		return rubySyntaxSpec(), true
	case "scala":
		return scalaSyntaxSpec(), true
	default:
		return treeSitterSpec{}, false
	}
}

func syntaxGrammar(factory func() unsafe.Pointer) func(string) *sitter.Language {
	return func(string) *sitter.Language { return sitter.NewLanguage(factory()) }
}

func goSyntaxSpec() treeSitterSpec {
	return treeSitterSpec{
		Grammar: syntaxGrammar(golang.Language),
		Definitions: map[string]syntaxDefinitionRule{
			"function_declaration": {Kind: "function", NameField: "name", Scope: true, Callable: true},
			"method_declaration":   {Kind: "method", NameField: "name", Scope: true, Callable: true, Normalizer: normalizeGoSyntaxMethod},
			"type_spec":            {NameField: "name", Scope: true, Normalizer: normalizeGoSyntaxType},
		},
		Calls:   map[string][]string{"call_expression": {"function", "expression"}},
		Imports: map[string]bool{"import_spec": true}, ImportPaths: syntaxQuotedPaths,
		InitialScope: goSyntaxInitialScope,
	}
}

func scriptTreeSitterSpec() treeSitterSpec {
	return treeSitterSpec{
		Grammar: typescriptSyntaxGrammar,
		Definitions: map[string]syntaxDefinitionRule{
			"class_declaration":      syntaxNamedRule("class", "name", true),
			"interface_declaration":  syntaxNamedRule("interface", "name", true),
			"function_declaration":   syntaxNamedRule("function", "name", true, true),
			"method_definition":      syntaxNamedRule("method", "name", true, true),
			"type_alias_declaration": syntaxNamedRule("type", "name", false),
			"enum_declaration":       syntaxNamedRule("enum", "name", true),
			"internal_module":        syntaxNamedRule("namespace", "name", true),
			"variable_declarator":    {Normalizer: normalizeScriptSyntaxLexical},
		},
		Calls:       map[string][]string{"call_expression": {"function", "expression"}},
		Imports:     map[string]bool{"import_statement": true, "export_statement": true},
		ImportPaths: syntaxQuotedPaths,
	}
}

func typescriptSyntaxGrammar(dialect string) *sitter.Language {
	if dialect == "tsx" || dialect == "jsx" {
		return sitter.NewLanguage(typescript.LanguageTSX())
	}
	return sitter.NewLanguage(typescript.LanguageTypescript())
}

func pythonSyntaxSpec() treeSitterSpec {
	return treeSitterSpec{
		Grammar: syntaxGrammar(python.Language),
		Definitions: map[string]syntaxDefinitionRule{
			"class_definition":    syntaxNamedRule("class", "name", true),
			"function_definition": {Kind: "function", NameField: "name", Scope: true, Callable: true, Normalizer: normalizePythonSyntaxFunction},
		},
		Calls:       map[string][]string{"call": {"function"}},
		Imports:     map[string]bool{"import_statement": true, "import_from_statement": true},
		ImportPaths: normalizePythonSyntaxImports, InitialScope: moduleSyntaxInitialScope,
	}
}

func javaSyntaxSpec() treeSitterSpec {
	return treeSitterSpec{
		Grammar: syntaxGrammar(java.Language),
		Definitions: map[string]syntaxDefinitionRule{
			"class_declaration":               syntaxNamedRule("class", "name", true),
			"interface_declaration":           syntaxNamedRule("interface", "name", true),
			"enum_declaration":                syntaxNamedRule("enum", "name", true),
			"record_declaration":              syntaxNamedRule("class", "name", true),
			"annotation_type_declaration":     syntaxNamedRule("interface", "name", true),
			"method_declaration":              syntaxNamedRule("method", "name", true, true),
			"constructor_declaration":         syntaxNamedRule("method", "name", true, true),
			"compact_constructor_declaration": syntaxNamedRule("method", "name", true, true),
		},
		Calls:       map[string][]string{"method_invocation": {"name"}, "object_creation_expression": {"type"}},
		Imports:     map[string]bool{"import_declaration": true},
		ImportPaths: syntaxKeywordPaths("import"), InitialScope: javaSyntaxInitialScope,
	}
}

func cFamilyTreeSitterSpec() treeSitterSpec {
	return treeSitterSpec{
		Definitions: map[string]syntaxDefinitionRule{
			"type_definition":      {Normalizer: normalizeCSyntaxType},
			"class_specifier":      {Kind: "class", NameField: "name", Scope: true, Normalizer: normalizeStandaloneCSyntaxType},
			"struct_specifier":     {Kind: "struct", NameField: "name", Scope: true, Normalizer: normalizeStandaloneCSyntaxType},
			"enum_specifier":       {Kind: "enum", NameField: "name", Scope: true, Normalizer: normalizeStandaloneCSyntaxType},
			"namespace_definition": syntaxNamedRule("namespace", "name", true),
			"function_definition":  {Kind: "function", Scope: true, Callable: true, Normalizer: normalizeCSyntaxFunction},
			"preproc_def":          syntaxNamedRule("macro", "name", false),
			"preproc_function_def": syntaxNamedRule("macro", "name", false),
		},
		Calls:   map[string][]string{"call_expression": {"function", "expression"}},
		Imports: map[string]bool{"preproc_include": true}, ImportPaths: syntaxQuotedPaths,
		Supplement: supplementCSyntaxMacros,
	}
}

func rustSyntaxSpec() treeSitterSpec {
	return treeSitterSpec{
		Grammar: syntaxGrammar(rust.Language),
		Definitions: map[string]syntaxDefinitionRule{
			"function_item":    {Normalizer: normalizeRustSyntaxCallable},
			"struct_item":      syntaxNamedRule("struct", "name", true),
			"enum_item":        syntaxNamedRule("enum", "name", true),
			"trait_item":       syntaxNamedRule("interface", "name", true),
			"type_item":        syntaxNamedRule("type", "name", false),
			"mod_item":         syntaxNamedRule("module", "name", true),
			"macro_definition": syntaxNamedRule("macro", "name", false),
		},
		Calls:       map[string][]string{"call_expression": {"function"}},
		Imports:     map[string]bool{"use_declaration": true, "extern_crate_declaration": true},
		ImportPaths: normalizeRustSyntaxImports, InitialScope: rustSyntaxInitialScope,
		NormalizeCall: syntaxTextualCallee,
	}
}

func csharpSyntaxSpec() treeSitterSpec {
	return treeSitterSpec{
		Grammar: syntaxGrammar(csharp.Language),
		Definitions: syntaxNamedDefinitions(
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
		),
		Calls:   map[string][]string{"invocation_expression": {"function"}, "object_creation_expression": {"type"}},
		Imports: map[string]bool{"using_directive": true}, ImportPaths: normalizeCSharpSyntaxImports,
		NormalizeCall: syntaxTextualCallee,
	}
}

func kotlinSyntaxSpec() treeSitterSpec {
	return treeSitterSpec{
		Grammar: syntaxGrammar(kotlin.Language),
		Definitions: map[string]syntaxDefinitionRule{
			"class_declaration":     {Normalizer: normalizeKotlinSyntaxType},
			"object_declaration":    syntaxDescendantRule("class", true, "type_identifier"),
			"companion_object":      syntaxFixedRule("companion", "class", true),
			"function_declaration":  {Normalizer: normalizeKotlinSyntaxCallable},
			"secondary_constructor": syntaxFixedRule("constructor", "method", true),
			"primary_constructor":   syntaxFixedRule("constructor", "method", true),
			"type_alias":            syntaxDescendantRule("type", false, "type_identifier", "simple_identifier"),
		},
		Calls:   map[string][]string{"call_expression": {"$self"}, "constructor_invocation": {"$self"}},
		Imports: map[string]bool{"import_header": true}, ImportPaths: syntaxKeywordPaths("import"),
		NormalizeCall: syntaxTextualCallee,
	}
}

func rubySyntaxSpec() treeSitterSpec {
	return treeSitterSpec{
		Grammar: syntaxGrammar(ruby.Language),
		Definitions: map[string]syntaxDefinitionRule{
			"class": syntaxNamedRule("class", "name", true), "module": syntaxNamedRule("module", "name", true),
			"method":           syntaxNamedRule("function", "name", true, true),
			"singleton_method": {Normalizer: normalizeRubySingletonMethod},
			"singleton_class":  syntaxFixedRule("singleton", "class", true),
		},
		Calls: map[string][]string{"call": {"method"}}, Imports: map[string]bool{"call": true},
		NormalizeCall: syntaxTextualCallee, CallMetadata: syntaxDynamicCallMetadata,
		ImportPaths: normalizeRubySyntaxImports,
	}
}

func scalaSyntaxSpec() treeSitterSpec {
	return treeSitterSpec{
		Grammar: syntaxGrammar(scala.Language),
		Definitions: map[string]syntaxDefinitionRule{
			"class_definition":     syntaxNamedRule("class", "name", true),
			"trait_definition":     syntaxNamedRule("interface", "name", true),
			"object_definition":    syntaxNamedRule("class", "name", true),
			"enum_definition":      syntaxNamedRule("enum", "name", true),
			"package_clause":       syntaxNamedRule("namespace", "name", true),
			"function_definition":  syntaxNamedRule("function", "name", true, true),
			"function_declaration": syntaxNamedRule("function", "name", true, true),
			"extension_definition": syntaxFixedRule("extension", "class", true),
			"given_definition":     syntaxDescendantRule("type", true, "identifier"),
			"type_definition":      syntaxNamedRule("type", "name", false),
		},
		Calls:       map[string][]string{"call_expression": {"function"}},
		Imports:     map[string]bool{"import_declaration": true, "export_declaration": true},
		ImportPaths: syntaxKeywordPaths("import", "export"), NormalizeCall: syntaxTextualCallee,
	}
}

func syntaxNamedDefinitions(types, callables map[string]string) map[string]syntaxDefinitionRule {
	result := make(map[string]syntaxDefinitionRule, len(types)+len(callables))
	for node, kind := range types {
		result[node] = syntaxNamedRule(kind, "name", true)
	}
	for node, kind := range callables {
		result[node] = syntaxNamedRule(kind, "name", true, true)
	}
	return result
}

func normalizeGoSyntaxMethod(ctx syntaxDefinitionContext, rule syntaxDefinitionRule) (syntaxDefinitionRecord, bool) {
	record, ok := normalizeDefinition(ctx, syntaxDefinitionRule{Kind: rule.Kind, NameField: rule.NameField, Scope: rule.Scope, Callable: rule.Callable})
	if !ok {
		return syntaxDefinitionRecord{}, false
	}
	if receiver := ctx.Node.ChildByFieldName("receiver"); receiver != nil {
		parts := strings.Fields(strings.Trim(syntaxNodeText(receiver, ctx.Code), "()"))
		if len(parts) > 1 {
			record.Container = strings.TrimPrefix(parts[len(parts)-1], "*")
			if ctx.ScopeName != "" {
				record.Container = ctx.ScopeName + "." + record.Container
			}
		}
	}
	return record, true
}

func normalizeGoSyntaxType(ctx syntaxDefinitionContext, _ syntaxDefinitionRule) (syntaxDefinitionRecord, bool) {
	name, value := ctx.Node.ChildByFieldName("name"), ctx.Node.ChildByFieldName("type")
	if name == nil || value == nil {
		return syntaxDefinitionRecord{}, false
	}
	kind, scope := "type", false
	if value.Kind() == "struct_type" {
		kind, scope = "struct", true
	} else if value.Kind() == "interface_type" {
		kind, scope = "interface", true
	}
	return syntaxDefinitionRecord{Name: syntaxNodeText(name, ctx.Code), Kind: kind, Scope: scope}, true
}

func normalizePythonSyntaxFunction(ctx syntaxDefinitionContext, rule syntaxDefinitionRule) (syntaxDefinitionRecord, bool) {
	record, ok := normalizeDefinition(ctx, syntaxDefinitionRule{Kind: "function", NameField: rule.NameField, Scope: true, Callable: true})
	if ok && ctx.ScopeKind == "class" {
		record.Kind = "method"
	}
	return record, ok
}

func normalizeScriptSyntaxLexical(ctx syntaxDefinitionContext, _ syntaxDefinitionRule) (syntaxDefinitionRecord, bool) {
	value, name := ctx.Node.ChildByFieldName("value"), ctx.Node.ChildByFieldName("name")
	if value == nil || name == nil || (value.Kind() != "arrow_function" && value.Kind() != "function_expression") {
		return syntaxDefinitionRecord{}, false
	}
	kind := "function"
	if ctx.ScopeKind == "class" {
		kind = "method"
	}
	return syntaxDefinitionRecord{Name: syntaxNodeText(name, ctx.Code), Kind: kind, Scope: true, Callable: true}, true
}

func normalizeCSyntaxType(ctx syntaxDefinitionContext, _ syntaxDefinitionRule) (syntaxDefinitionRecord, bool) {
	declarator := ctx.Node.ChildByFieldName("declarator")
	name := syntaxDeclaratorIdentifier(declarator, ctx.Code)
	return syntaxDefinitionRecord{Name: name, Kind: "type"}, name != ""
}

func normalizeStandaloneCSyntaxType(ctx syntaxDefinitionContext, rule syntaxDefinitionRule) (syntaxDefinitionRecord, bool) {
	if parent := ctx.Node.Parent(); parent != nil && parent.Kind() == "type_definition" {
		return syntaxDefinitionRecord{}, false
	}
	return normalizeDefinition(ctx, syntaxDefinitionRule{Kind: rule.Kind, NameField: rule.NameField, Scope: true})
}

func normalizeCSyntaxFunction(ctx syntaxDefinitionContext, rule syntaxDefinitionRule) (syntaxDefinitionRecord, bool) {
	declarator := ctx.Node.ChildByFieldName("declarator")
	if declarator == nil || syntaxFindDescendant(declarator, "parameter_list") == nil {
		return syntaxDefinitionRecord{}, false
	}
	name, container := syntaxCFunctionName(declarator, ctx.Code)
	if name == "" {
		return syntaxDefinitionRecord{}, false
	}
	kind := rule.Kind
	if container != "" || ctx.ScopeKind == "class" || ctx.ScopeKind == "struct" {
		kind = "method"
	}
	if container == "" {
		container = ctx.ScopeName
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(syntaxNodeText(declarator, ctx.Code))))
	return syntaxDefinitionRecord{Name: name, Kind: kind, Container: container, Scope: true, Callable: true, IdentitySuffix: fmt.Sprintf("~%x", sum[:6])}, true
}

func syntaxCFunctionName(node *sitter.Node, code []byte) (string, string) {
	if node == nil {
		return "", ""
	}
	if node.Kind() == "qualified_identifier" {
		scope, name := node.ChildByFieldName("scope"), node.ChildByFieldName("name")
		if scope != nil && name != nil {
			return syntaxNodeText(name, code), syntaxNodeText(scope, code)
		}
	}
	if node.Kind() == "function_declarator" {
		return syntaxCFunctionName(node.ChildByFieldName("declarator"), code)
	}
	if node.Kind() == "pointer_declarator" {
		for index := uint(0); index < node.ChildCount(); index++ {
			child := node.Child(index)
			if child.Kind() == "function_declarator" || child.Kind() == "qualified_identifier" || child.Kind() == "field_identifier" || child.Kind() == "identifier" {
				return syntaxCFunctionName(child, code)
			}
		}
	}
	return strings.TrimSpace(syntaxNodeText(node, code)), ""
}

func syntaxDeclaratorIdentifier(node *sitter.Node, code []byte) string {
	if node == nil {
		return ""
	}
	if node.Kind() == "identifier" || node.Kind() == "type_identifier" {
		return syntaxNodeText(node, code)
	}
	if name := node.ChildByFieldName("name"); name != nil {
		if value := syntaxDeclaratorIdentifier(name, code); value != "" {
			return value
		}
	}
	if declarator := node.ChildByFieldName("declarator"); declarator != nil {
		if value := syntaxDeclaratorIdentifier(declarator, code); value != "" {
			return value
		}
	}
	for index := node.ChildCount(); index > 0; index-- {
		if value := syntaxDeclaratorIdentifier(node.Child(index-1), code); value != "" {
			return value
		}
	}
	return ""
}

func normalizeRustSyntaxCallable(ctx syntaxDefinitionContext, _ syntaxDefinitionRule) (syntaxDefinitionRecord, bool) {
	name := syntaxDescendantText(ctx.Node, ctx.Code, "identifier")
	if name == "" {
		return syntaxDefinitionRecord{}, false
	}
	record := syntaxDefinitionRecord{Name: name, Kind: "function", Scope: true, Callable: true}
	for parent := ctx.Node.Parent(); parent != nil; parent = parent.Parent() {
		if parent.Kind() != "impl_item" {
			continue
		}
		record.Kind = "method"
		if target := parent.ChildByFieldName("type"); target != nil {
			record.Container = syntaxNodeText(target, ctx.Code)
		}
		break
	}
	return record, true
}

func normalizeKotlinSyntaxType(ctx syntaxDefinitionContext, _ syntaxDefinitionRule) (syntaxDefinitionRecord, bool) {
	name := syntaxDescendantText(ctx.Node, ctx.Code, "type_identifier")
	if name == "" {
		return syntaxDefinitionRecord{}, false
	}
	source, kind := syntaxNodeText(ctx.Node, ctx.Code), "class"
	if strings.Contains(source, "interface "+name) {
		kind = "interface"
	} else if strings.Contains(source, "enum class "+name) {
		kind = "enum"
	}
	return syntaxDefinitionRecord{Name: name, Kind: kind, Scope: true}, true
}

func normalizeKotlinSyntaxCallable(ctx syntaxDefinitionContext, _ syntaxDefinitionRule) (syntaxDefinitionRecord, bool) {
	record, ok := syntaxDescendantCallable("simple_identifier")(ctx, syntaxDefinitionRule{})
	if ok {
		if receiver := ctx.Node.ChildByFieldName("receiver"); receiver != nil {
			record.Kind, record.Container = "method", syntaxNodeText(receiver, ctx.Code)
		}
	}
	return record, ok
}

func normalizeRubySingletonMethod(ctx syntaxDefinitionContext, _ syntaxDefinitionRule) (syntaxDefinitionRecord, bool) {
	name, container := ctx.Node.ChildByFieldName("name"), ctx.Node.ChildByFieldName("object")
	if name == nil {
		return syntaxDefinitionRecord{}, false
	}
	record := syntaxDefinitionRecord{Name: syntaxNodeText(name, ctx.Code), Kind: "method", Scope: true, Callable: true}
	if container != nil {
		record.Container = syntaxNodeText(container, ctx.Code)
	}
	return record, true
}

func goSyntaxInitialScope(_ string, root *sitter.Node, code []byte) (string, string) {
	if root != nil {
		for index := uint(0); index < root.ChildCount(); index++ {
			child := root.Child(index)
			if child.Kind() == "package_clause" {
				return "module", strings.TrimSpace(strings.TrimPrefix(syntaxNodeText(child, code), "package "))
			}
		}
	}
	return "", ""
}

func moduleSyntaxInitialScope(filePath string, _ *sitter.Node, _ []byte) (string, string) {
	base := filepath.Base(filePath)
	return "module", strings.TrimSuffix(base, filepath.Ext(base))
}

func javaSyntaxInitialScope(_ string, root *sitter.Node, code []byte) (string, string) {
	if root != nil {
		for index := uint(0); index < root.ChildCount(); index++ {
			child := root.Child(index)
			if child.Kind() == "package_declaration" {
				value := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(syntaxNodeText(child, code)), "package"), ";"))
				return "module", value
			}
		}
	}
	return "", ""
}

func rustSyntaxInitialScope(filePath string, _ *sitter.Node, _ []byte) (string, string) {
	base := filepath.Base(filePath)
	if base == "main.rs" || base == "lib.rs" || base == "mod.rs" {
		return "module", filepath.Base(filepath.Dir(filePath))
	}
	return "module", strings.TrimSuffix(base, filepath.Ext(base))
}

func normalizePythonSyntaxImports(source string) []string {
	value := strings.TrimSpace(source)
	value = strings.TrimSpace(strings.TrimPrefix(value, "from"))
	value = strings.TrimSpace(strings.TrimPrefix(value, "import"))
	if index := strings.Index(value, " import "); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	if index := strings.Index(value, " as "); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	if value == "" {
		return nil
	}
	return []string{value}
}

func normalizeRustSyntaxImports(source string) []string {
	value := strings.TrimSpace(strings.TrimSuffix(source, ";"))
	value = strings.TrimSpace(strings.TrimPrefix(value, "use"))
	value = strings.TrimSpace(strings.TrimPrefix(value, "extern crate"))
	if value == "" {
		return nil
	}
	return []string{value}
}

func normalizeCSharpSyntaxImports(source string) []string {
	value := strings.TrimSpace(strings.TrimSuffix(source, ";"))
	value = strings.TrimSpace(strings.TrimPrefix(value, "global"))
	value = strings.TrimSpace(strings.TrimPrefix(value, "using"))
	if index := strings.Index(value, "="); index >= 0 {
		value = strings.TrimSpace(value[index+1:])
	}
	value = strings.TrimSpace(strings.TrimPrefix(value, "static"))
	if value == "" {
		return nil
	}
	return []string{value}
}

func normalizeRubySyntaxImports(source string) []string {
	trimmed := strings.TrimSpace(source)
	if !strings.HasPrefix(trimmed, "require ") && !strings.HasPrefix(trimmed, "require_relative ") {
		return nil
	}
	return syntaxQuotedPaths(trimmed)
}

var cSyntaxMacroPattern = regexp.MustCompile(`\b((?:(?:DECLARE|DEF|CREATE|IMPLEMENT)_[A-Z0-9_]+)|(?:[A-Z0-9_]+_(?:INIT|FUNC|REGISTER|ENTRY|HANDLER|CALLBACK)))\s*\(\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\)`)

func supplementCSyntaxMacros(_ []byte, lines []string) []syntaxSupplementDefinition {
	result := make([]syntaxSupplementDefinition, 0)
	inBlockComment := false
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "/*") {
			inBlockComment = true
		}
		if inBlockComment {
			if strings.Contains(trimmed, "*/") {
				inBlockComment = false
			}
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		match := cSyntaxMacroPattern.FindStringSubmatch(line)
		if len(match) != 3 {
			continue
		}
		result = append(result, syntaxSupplementDefinition{
			Name: match[2], Kind: "function", Signature: trimmed,
			Docstring: "Generated by macro " + match[1], StartLine: index + 1, EndLine: index + 1,
		})
	}
	return result
}
