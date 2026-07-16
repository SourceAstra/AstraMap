package astramap

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	c "github.com/tree-sitter/tree-sitter-c/bindings/go"
	cpp "github.com/tree-sitter/tree-sitter-cpp/bindings/go"
	golang "github.com/tree-sitter/tree-sitter-go/bindings/go"
	java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

type ScipDriver string

const (
	ScipDriverGo     ScipDriver = "go"
	ScipDriverNode   ScipDriver = "node"
	ScipDriverPython ScipDriver = "python"
	ScipDriverClang  ScipDriver = "clang"
)

type CapabilitySet struct {
	Definitions       bool `json:"definitions"`
	Containers        bool `json:"containers"`
	LocalCalls        bool `json:"localCalls"`
	Imports           bool `json:"imports"`
	CrossFileCalls    bool `json:"crossFileCalls"`
	OverloadResolve   bool `json:"overloadResolve"`
	Implementations   bool `json:"implementations"`
	IncrementalSyntax bool `json:"incrementalSyntax"`
}

type LanguageCapabilityInfo struct {
	ID           string        `json:"id"`
	DisplayName  string        `json:"displayName"`
	Level        string        `json:"level"`
	Extensions   []string      `json:"extensions"`
	Capabilities CapabilitySet `json:"capabilities"`
	ProviderID   string        `json:"semanticProvider,omitempty"`
	AutoSemantic bool          `json:"autoSemantic"`
}

type ToolchainRequirement struct {
	Label             string
	Commands          []string
	InstallHint       string
	WhenAnyFiles      []string
	ProjectExecutable string
}

type DefinitionRule struct {
	Kind       string
	NameField  string
	Scope      bool
	Callable   bool
	Normalizer DefinitionNormalizer
}

type DefinitionContext struct {
	Node      *sitter.Node
	Code      []byte
	ScopeKind string
	ScopeName string
}

type DefinitionRecord struct {
	Name           string
	Kind           string
	Container      string
	Scope          bool
	Callable       bool
	IdentitySuffix string
}

type DefinitionNormalizer func(DefinitionContext, DefinitionRule) (DefinitionRecord, bool)
type InitialScopeResolver func(filePath string, root *sitter.Node, code []byte) (kind, name string)
type SupplementDefinitions func(code []byte, lines []string) []SupplementDefinition
type SemanticNodeNormalizer func(node *AstraMapNode, symbol string, sourceLines []string)

type SupplementDefinition struct {
	Name      string
	Kind      string
	Signature string
	Docstring string
	StartLine int
	EndLine   int
}

type SyntaxSpec struct {
	Definitions   map[string]DefinitionRule
	Calls         map[string][]string
	Imports       map[string]bool
	NormalizeCall func(*sitter.Node, []byte) string
	ImportPaths   func(string) []string
	InitialScope  InitialScopeResolver
	Supplement    SupplementDefinitions
}

type SemanticProviderSpec struct {
	ID               string
	Tool             string
	InstallHint      string
	Driver           ScipDriver
	AutoGenerateScip bool
}

type LanguageSpec struct {
	ID                 string
	DisplayName        string
	IDPrefix           string
	QualifiedSeparator string
	Extensions         []string
	ProviderID         string
	Capabilities       CapabilitySet
	Toolchain          []ToolchainRequirement
	NormalizeSemantic  SemanticNodeNormalizer
	grammar            func(ext string) *sitter.Language
	syntax             SyntaxSpec
}

type ProjectProfile struct {
	ProjectRoot     string
	ExtensionCounts map[string]int
}

type LanguageSelection struct {
	ID      string
	Dialect string
	Grammar *sitter.Language
}

var semanticProviders = map[string]SemanticProviderSpec{
	"go": {
		ID: "go", Tool: "scip-go",
		InstallHint: "go install github.com/sourcegraph/scip-go/cmd/scip-go@latest",
		Driver:      ScipDriverGo, AutoGenerateScip: true,
	},
	"typescript": {
		ID: "typescript", Tool: "scip-typescript",
		InstallHint: "npm install -g @sourcegraph/scip-typescript",
		Driver:      ScipDriverNode, AutoGenerateScip: true,
	},
	"python": {
		ID: "python", Tool: "scip-python",
		InstallHint: "pip install scip-python",
		Driver:      ScipDriverPython, AutoGenerateScip: true,
	},
	"java": {
		ID: "java", Tool: "scip-java",
		InstallHint: "See https://github.com/sourcegraph/scip-java",
	},
	"clang": {
		ID: "clang", Tool: "scip-clang",
		InstallHint: "See https://github.com/sourcegraph/scip-clang",
		Driver:      ScipDriverClang, AutoGenerateScip: true,
	},
}

var syntaxCapabilities = CapabilitySet{
	Definitions: true, Containers: true, LocalCalls: true, Imports: true, IncrementalSyntax: true,
}

var semanticCapabilities = CapabilitySet{
	Definitions: true, Containers: true, LocalCalls: true, Imports: true,
	CrossFileCalls: true, OverloadResolve: true, IncrementalSyntax: true,
}

var builtinLanguages = []LanguageSpec{
	{
		ID: "go", DisplayName: "Go", IDPrefix: "go", QualifiedSeparator: ".", Extensions: []string{".go"}, ProviderID: "go",
		Capabilities:      semanticCapabilities,
		NormalizeSemantic: normalizeGoScipNode,
		Toolchain: []ToolchainRequirement{
			{Label: "Go compiler", Commands: []string{"go"}, InstallHint: "https://go.dev/doc/install"},
		},
		grammar: func(string) *sitter.Language { return sitter.NewLanguage(golang.Language()) },
		syntax: SyntaxSpec{
			Definitions: map[string]DefinitionRule{
				"function_declaration": {Kind: "function", NameField: "name", Scope: true, Callable: true},
				"method_declaration":   {Kind: "method", NameField: "name", Scope: true, Callable: true, Normalizer: normalizeGoMethodDefinition},
				"type_spec":            {NameField: "name", Scope: true, Normalizer: normalizeGoTypeDefinition},
			},
			Calls:        map[string][]string{"call_expression": {"function", "expression"}},
			Imports:      map[string]bool{"import_spec": true},
			ImportPaths:  quotedImportPaths,
			InitialScope: goInitialScope,
		},
	},
	{
		ID: "typescript", DisplayName: "TypeScript", IDPrefix: "ts", QualifiedSeparator: ".",
		Extensions: []string{".ts", ".tsx"}, ProviderID: "typescript",
		Capabilities: semanticCapabilities,
		Toolchain: []ToolchainRequirement{
			{Label: "Node.js", Commands: []string{"node"}, InstallHint: "Ubuntu/Debian: sudo apt install nodejs npm | macOS: brew install node"},
			{Label: "Package manager", Commands: []string{"pnpm", "yarn", "npm"}, InstallHint: "Ubuntu/Debian: sudo apt install npm | macOS: brew install node"},
			{Label: "TypeScript compiler", Commands: []string{"tsc"}, InstallHint: "npm install -g typescript", WhenAnyFiles: []string{"tsconfig.json"}},
		},
		grammar: typescriptGrammar,
		syntax:  scriptSyntaxSpec(),
	},
	{
		ID: "javascript", DisplayName: "JavaScript", IDPrefix: "js", QualifiedSeparator: ".",
		Extensions: []string{".js", ".jsx", ".mjs", ".cjs"}, ProviderID: "typescript",
		Capabilities: semanticCapabilities,
		Toolchain: []ToolchainRequirement{
			{Label: "Node.js", Commands: []string{"node"}, InstallHint: "Ubuntu/Debian: sudo apt install nodejs npm | macOS: brew install node"},
			{Label: "Package manager", Commands: []string{"pnpm", "yarn", "npm"}, InstallHint: "Ubuntu/Debian: sudo apt install npm | macOS: brew install node"},
		},
		grammar: typescriptGrammar,
		syntax:  scriptSyntaxSpec(),
	},
	{
		ID: "python", DisplayName: "Python", IDPrefix: "py", QualifiedSeparator: ".", Extensions: []string{".py"}, ProviderID: "python",
		Capabilities: semanticCapabilities,
		Toolchain: []ToolchainRequirement{
			{Label: "Python interpreter", Commands: []string{"python3", "python"}, InstallHint: "Ubuntu/Debian: sudo apt install python3 python3-pip | macOS: brew install python"},
			{Label: "pip", Commands: []string{"pip3", "pip"}, InstallHint: "Ubuntu/Debian: sudo apt install python3-pip | macOS: python3 -m ensurepip"},
		},
		grammar: func(string) *sitter.Language { return sitter.NewLanguage(python.Language()) },
		syntax: SyntaxSpec{
			Definitions: map[string]DefinitionRule{
				"class_definition":    {Kind: "class", NameField: "name", Scope: true},
				"function_definition": {Kind: "function", NameField: "name", Scope: true, Callable: true, Normalizer: normalizePythonFunctionDefinition},
			},
			Calls:        map[string][]string{"call": {"function"}},
			Imports:      map[string]bool{"import_statement": true, "import_from_statement": true},
			ImportPaths:  normalizePythonImports,
			InitialScope: moduleInitialScope,
		},
	},
	{
		ID: "java", DisplayName: "Java", IDPrefix: "java", QualifiedSeparator: ".", Extensions: []string{".java"}, ProviderID: "java",
		Capabilities: syntaxCapabilities,
		Toolchain: []ToolchainRequirement{
			{Label: "Java runtime", Commands: []string{"java"}, InstallHint: "Ubuntu/Debian: sudo apt install default-jdk | macOS: brew install openjdk"},
			{Label: "Java compiler", Commands: []string{"javac"}, InstallHint: "Ubuntu/Debian: sudo apt install default-jdk | macOS: brew install openjdk"},
			{Label: "Maven", Commands: []string{"mvn"}, InstallHint: "Ubuntu/Debian: sudo apt install maven | macOS: brew install maven", WhenAnyFiles: []string{"pom.xml"}},
			{Label: "Gradle", Commands: []string{"gradle"}, InstallHint: "Ubuntu/Debian: sudo apt install gradle | macOS: brew install gradle", WhenAnyFiles: []string{"build.gradle", "build.gradle.kts", "gradlew"}, ProjectExecutable: "gradlew"},
		},
		grammar: func(string) *sitter.Language { return sitter.NewLanguage(java.Language()) },
		syntax: SyntaxSpec{
			Definitions: map[string]DefinitionRule{
				"class_declaration":               {Kind: "class", NameField: "name", Scope: true},
				"interface_declaration":           {Kind: "interface", NameField: "name", Scope: true},
				"enum_declaration":                {Kind: "enum", NameField: "name", Scope: true},
				"record_declaration":              {Kind: "class", NameField: "name", Scope: true},
				"annotation_type_declaration":     {Kind: "interface", NameField: "name", Scope: true},
				"method_declaration":              {Kind: "method", NameField: "name", Scope: true, Callable: true},
				"constructor_declaration":         {Kind: "method", NameField: "name", Scope: true, Callable: true},
				"compact_constructor_declaration": {Kind: "method", NameField: "name", Scope: true, Callable: true},
			},
			Calls:        map[string][]string{"method_invocation": {"name"}, "object_creation_expression": {"type"}},
			Imports:      map[string]bool{"import_declaration": true},
			ImportPaths:  normalizeJavaImports,
			InitialScope: javaInitialScope,
		},
	},
	{
		ID: "c", DisplayName: "C", IDPrefix: "c", QualifiedSeparator: ".", Extensions: []string{".c", ".h"}, ProviderID: "clang",
		Capabilities:      semanticCapabilities,
		NormalizeSemantic: normalizeCScipNode,
		grammar:           func(string) *sitter.Language { return sitter.NewLanguage(c.Language()) },
		syntax:            cFamilySyntaxSpec(),
	},
	{
		ID: "cpp", DisplayName: "C++", IDPrefix: "cxx", QualifiedSeparator: "::",
		Extensions: []string{".cc", ".cpp", ".cxx", ".hpp", ".hxx"}, ProviderID: "clang",
		Capabilities:      semanticCapabilities,
		NormalizeSemantic: normalizeCScipNode,
		grammar:           func(string) *sitter.Language { return sitter.NewLanguage(cpp.Language()) },
		syntax:            cFamilySyntaxSpec(),
	},
}

func scriptSyntaxSpec() SyntaxSpec {
	return SyntaxSpec{
		Definitions: map[string]DefinitionRule{
			"class_declaration":      {Kind: "class", NameField: "name", Scope: true},
			"interface_declaration":  {Kind: "interface", NameField: "name", Scope: true},
			"function_declaration":   {Kind: "function", NameField: "name", Scope: true, Callable: true},
			"method_definition":      {Kind: "method", NameField: "name", Scope: true, Callable: true},
			"type_alias_declaration": {Kind: "type", NameField: "name"},
			"enum_declaration":       {Kind: "enum", NameField: "name", Scope: true},
			"internal_module":        {Kind: "namespace", NameField: "name", Scope: true},
			"variable_declarator":    {Normalizer: normalizeScriptLexicalDefinition},
		},
		Calls:       map[string][]string{"call_expression": {"function", "expression"}},
		Imports:     map[string]bool{"import_statement": true, "export_statement": true},
		ImportPaths: quotedImportPaths,
	}
}

func cFamilySyntaxSpec() SyntaxSpec {
	return SyntaxSpec{
		Definitions: map[string]DefinitionRule{
			"type_definition":      {Normalizer: normalizeCTypeDefinition},
			"class_specifier":      {Kind: "class", NameField: "name", Scope: true, Normalizer: normalizeStandaloneCTypeDefinition},
			"struct_specifier":     {Kind: "struct", NameField: "name", Scope: true, Normalizer: normalizeStandaloneCTypeDefinition},
			"enum_specifier":       {Kind: "enum", NameField: "name", Scope: true, Normalizer: normalizeStandaloneCTypeDefinition},
			"namespace_definition": {Kind: "namespace", NameField: "name", Scope: true},
			"function_definition":  {Kind: "function", Scope: true, Callable: true, Normalizer: normalizeCFunctionDefinition},
			"preproc_def":          {Kind: "macro", NameField: "name"},
			"preproc_function_def": {Kind: "macro", NameField: "name"},
		},
		Calls:       map[string][]string{"call_expression": {"function", "expression"}},
		Imports:     map[string]bool{"preproc_include": true},
		ImportPaths: quotedImportPaths,
		Supplement:  supplementCMacroDefinitions,
	}
}

var (
	languageByID  map[string]*LanguageSpec
	extensionToID map[string]string
)

func init() {
	languageByID = make(map[string]*LanguageSpec, len(builtinLanguages))
	extensionToID = make(map[string]string)
	prefixes := make(map[string]string)
	for i := range builtinLanguages {
		spec := &builtinLanguages[i]
		if spec.ID == "" || spec.DisplayName == "" || spec.IDPrefix == "" || spec.grammar == nil {
			panic("incomplete language spec: " + spec.ID)
		}
		if _, exists := languageByID[spec.ID]; exists {
			panic("duplicate language id: " + spec.ID)
		}
		if owner, exists := prefixes[spec.IDPrefix]; exists {
			panic("duplicate language id prefix: " + spec.IDPrefix + " (" + owner + ", " + spec.ID + ")")
		}
		if _, ok := semanticProviders[spec.ProviderID]; !ok {
			panic("unknown semantic provider: " + spec.ProviderID)
		}
		languageByID[spec.ID] = spec
		prefixes[spec.IDPrefix] = spec.ID
		for _, ext := range spec.Extensions {
			if ext == "" || ext[0] != '.' || ext != strings.ToLower(ext) {
				panic("invalid extension " + ext + " for language " + spec.ID)
			}
			if owner, exists := extensionToID[ext]; exists {
				panic("ambiguous extension without resolver: " + ext + " (" + owner + ", " + spec.ID + ")")
			}
			extensionToID[ext] = spec.ID
		}
		for nodeKind, fields := range spec.syntax.Calls {
			if nodeKind == "" || len(fields) == 0 {
				panic("call expression requires node kind and callee fields: " + spec.ID)
			}
		}
		if len(spec.syntax.Imports) > 0 && spec.syntax.ImportPaths == nil {
			panic("import nodes require a path normalizer: " + spec.ID)
		}
	}
}

func LanguageSpecs() []LanguageSpec {
	result := make([]LanguageSpec, len(builtinLanguages))
	copy(result, builtinLanguages)
	for i := range result {
		result[i].Extensions = append([]string(nil), result[i].Extensions...)
		result[i].Toolchain = cloneToolchain(result[i].Toolchain)
	}
	return result
}

func LanguageSpecByID(id string) (LanguageSpec, bool) {
	spec, ok := languageByID[normalizeLanguageID(id)]
	if !ok {
		return LanguageSpec{}, false
	}
	result := *spec
	result.Extensions = append([]string(nil), spec.Extensions...)
	result.Toolchain = cloneToolchain(spec.Toolchain)
	return result, true
}

func LanguageToolchainRequirements(id string) []ToolchainRequirement {
	spec := languageByID[normalizeLanguageID(id)]
	if spec == nil {
		return nil
	}
	return cloneToolchain(spec.Toolchain)
}

func cloneToolchain(source []ToolchainRequirement) []ToolchainRequirement {
	result := make([]ToolchainRequirement, len(source))
	copy(result, source)
	for i := range result {
		result[i].Commands = append([]string(nil), result[i].Commands...)
		result[i].WhenAnyFiles = append([]string(nil), result[i].WhenAnyFiles...)
	}
	return result
}

func normalizeLanguageID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "ts" {
		return "typescript"
	}
	if id == "js" {
		return "javascript"
	}
	return id
}

func IsKnownLanguage(id string) bool {
	_, ok := languageByID[normalizeLanguageID(id)]
	return ok
}

func LanguageExtensions(id string) []string {
	spec := languageByID[normalizeLanguageID(id)]
	if spec == nil {
		return nil
	}
	return append([]string(nil), spec.Extensions...)
}

func SupportedLanguageIDs() []string {
	result := make([]string, 0, len(builtinLanguages))
	for i := range builtinLanguages {
		result = append(result, builtinLanguages[i].ID)
	}
	return result
}

func SupportedLanguageCapabilities() []LanguageCapabilityInfo {
	result := make([]LanguageCapabilityInfo, 0, len(builtinLanguages))
	for i := range builtinLanguages {
		spec := &builtinLanguages[i]
		provider := semanticProviders[spec.ProviderID]
		result = append(result, LanguageCapabilityInfo{
			ID: spec.ID, DisplayName: spec.DisplayName, Level: capabilityLevel(spec.Capabilities),
			Extensions: append([]string(nil), spec.Extensions...), Capabilities: spec.Capabilities,
			ProviderID: provider.ID, AutoSemantic: provider.AutoGenerateScip,
		})
	}
	return result
}

func capabilityLevel(c CapabilitySet) string {
	switch {
	case c.CrossFileCalls && c.OverloadResolve && c.Implementations:
		return "full"
	case c.CrossFileCalls:
		return "semantic"
	case c.LocalCalls:
		return "local-graph"
	default:
		return "syntax"
	}
}

func SupportedExtensions() []string {
	result := make([]string, 0, len(extensionToID))
	for ext := range extensionToID {
		result = append(result, ext)
	}
	sort.Strings(result)
	return result
}

func LanguageIDForExtension(ext string) (string, bool) {
	id, ok := extensionToID[strings.ToLower(ext)]
	return id, ok
}

func IsSupportedExtension(ext string) bool {
	_, ok := LanguageIDForExtension(ext)
	return ok
}

func LanguageDisplayName(id string) string {
	if spec := languageByID[normalizeLanguageID(id)]; spec != nil {
		return spec.DisplayName
	}
	return id
}

func LanguageIDPrefix(id string) string {
	if spec := languageByID[normalizeLanguageID(id)]; spec != nil {
		return spec.IDPrefix
	}
	return "unknown"
}

func LanguageQualifiedSeparator(id string) string {
	if spec := languageByID[normalizeLanguageID(id)]; spec != nil && spec.QualifiedSeparator != "" {
		return spec.QualifiedSeparator
	}
	return "."
}

func SemanticProviderForLanguage(id string) (SemanticProviderSpec, bool) {
	spec := languageByID[normalizeLanguageID(id)]
	if spec == nil {
		return SemanticProviderSpec{}, false
	}
	provider, ok := semanticProviders[spec.ProviderID]
	return provider, ok
}

func SemanticProviderByID(id string) (SemanticProviderSpec, bool) {
	provider, ok := semanticProviders[strings.ToLower(strings.TrimSpace(id))]
	return provider, ok
}

func ScipToolName(id string) string {
	provider, _ := SemanticProviderForLanguage(id)
	return provider.Tool
}

func ScipInstallHint(id string) string {
	provider, _ := SemanticProviderForLanguage(id)
	return provider.InstallHint
}

func CanAutoGenerateScip(id string) bool {
	provider, ok := SemanticProviderForLanguage(id)
	return ok && provider.AutoGenerateScip && provider.Tool != "" && provider.Driver != ""
}

func ScipDriverForLanguage(id string) ScipDriver {
	provider, _ := SemanticProviderForLanguage(id)
	return provider.Driver
}

func normalizeSemanticNode(language string, node *AstraMapNode, symbol string, sourceLines []string) {
	spec := languageByID[normalizeLanguageID(language)]
	if spec != nil && spec.NormalizeSemantic != nil {
		spec.NormalizeSemantic(node, symbol, sourceLines)
	}
}

func BuildProjectProfile(projectRoot string, filter *IndexFilter, stage IndexStage) ProjectProfile {
	profile := ProjectProfile{ProjectRoot: projectRoot, ExtensionCounts: make(map[string]int)}
	_ = filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		relPath, _ := filepath.Rel(projectRoot, path)
		if info.IsDir() {
			if path != projectRoot && hasHiddenSegment(info.Name()) {
				return filepath.SkipDir
			}
			if filter != nil && !filter.AllowsDir(relPath, stage) {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if IsSupportedExtension(ext) && (filter == nil || filter.Allows(relPath, stage)) {
			profile.ExtensionCounts[ext]++
		}
		return nil
	})
	return profile
}

func ResolveLanguageWithProfile(profile ProjectProfile, filePath string) (LanguageSelection, bool) {
	ext := strings.ToLower(filepath.Ext(filePath))
	id, ok := LanguageIDForExtension(ext)
	if !ok {
		return LanguageSelection{}, false
	}
	if ext == ".h" && hasCxxProfile(profile) {
		id = "cpp"
	}
	spec := languageByID[id]
	if spec == nil || spec.grammar == nil {
		return LanguageSelection{}, false
	}
	return LanguageSelection{ID: id, Dialect: dialectForExtension(id, ext), Grammar: spec.grammar(ext)}, true
}

func ProjectLanguageCounts(profile ProjectProfile) map[string]int {
	result := make(map[string]int, len(builtinLanguages))
	for _, spec := range builtinLanguages {
		for _, ext := range spec.Extensions {
			result[spec.ID] += profile.ExtensionCounts[ext]
		}
	}
	if hasCxxProfile(profile) {
		result["cpp"] += profile.ExtensionCounts[".h"]
		result["c"] -= profile.ExtensionCounts[".h"]
	}
	return result
}

func ResolveLanguage(projectRoot, filePath string) (string, *sitter.Language, bool) {
	profile := BuildProjectProfile(projectRoot, nil, StageTreeSitter)
	selection, ok := ResolveLanguageWithProfile(profile, filePath)
	return selection.ID, selection.Grammar, ok
}

func hasCxxProfile(profile ProjectProfile) bool {
	for _, ext := range []string{".cc", ".cpp", ".cxx", ".hpp", ".hxx"} {
		if profile.ExtensionCounts[ext] > 0 {
			return true
		}
	}
	return false
}

func dialectForExtension(language, ext string) string {
	switch ext {
	case ".tsx":
		return "tsx"
	case ".jsx":
		return "jsx"
	case ".mjs":
		return "mjs"
	case ".cjs":
		return "cjs"
	case ".h":
		return language + "-header"
	default:
		return strings.TrimPrefix(ext, ".")
	}
}

func languageSyntax(id string) (SyntaxSpec, bool) {
	spec := languageByID[normalizeLanguageID(id)]
	if spec == nil {
		return SyntaxSpec{}, false
	}
	return spec.syntax, true
}

func definitionRule(language, nodeKind string) (DefinitionRule, bool) {
	syntax, ok := languageSyntax(language)
	if !ok {
		return DefinitionRule{}, false
	}
	rule, ok := syntax.Definitions[nodeKind]
	return rule, ok
}

func callExpressionFields(language, nodeKind string) ([]string, bool) {
	syntax, ok := languageSyntax(language)
	if !ok {
		return nil, false
	}
	fields, ok := syntax.Calls[nodeKind]
	return fields, ok
}

func normalizeCallee(language string, node *sitter.Node, code []byte) string {
	syntax, ok := languageSyntax(language)
	if ok && syntax.NormalizeCall != nil {
		return syntax.NormalizeCall(node, code)
	}
	return extractCalleeShortName(node, code)
}

func importPaths(language, nodeKind, source string) []string {
	syntax, ok := languageSyntax(language)
	if !ok || !syntax.Imports[nodeKind] || syntax.ImportPaths == nil {
		return nil
	}
	return syntax.ImportPaths(source)
}

func isImportNode(language, nodeKind string) bool {
	syntax, ok := languageSyntax(language)
	return ok && syntax.Imports[nodeKind]
}

func initialLanguageScope(language, filePath string, root *sitter.Node, code []byte) (string, string) {
	syntax, ok := languageSyntax(language)
	if !ok || syntax.InitialScope == nil {
		return "", ""
	}
	return syntax.InitialScope(filePath, root, code)
}

func supplementalDefinitions(language string, code []byte, lines []string) []SupplementDefinition {
	syntax, ok := languageSyntax(language)
	if !ok || syntax.Supplement == nil {
		return nil
	}
	return syntax.Supplement(code, lines)
}

func typescriptGrammar(ext string) *sitter.Language {
	if ext == ".tsx" || ext == ".jsx" {
		return sitter.NewLanguage(typescript.LanguageTSX())
	}
	return sitter.NewLanguage(typescript.LanguageTypescript())
}

func quotedImportPaths(source string) []string {
	source = strings.TrimSpace(source)
	for _, pair := range [][2]byte{{'"', '"'}, {'\'', '\''}, {'`', '`'}, {'<', '>'}} {
		start := strings.IndexByte(source, pair[0])
		if start < 0 {
			continue
		}
		end := strings.IndexByte(source[start+1:], pair[1])
		if end >= 0 {
			return []string{strings.TrimSpace(source[start+1 : start+1+end])}
		}
	}
	return nil
}

func normalizePythonImports(source string) []string {
	source = strings.TrimSpace(source)
	if strings.HasPrefix(source, "from ") {
		fields := strings.Fields(strings.TrimPrefix(source, "from "))
		if len(fields) > 0 {
			return []string{fields[0]}
		}
		return nil
	}
	source = strings.TrimSpace(strings.TrimPrefix(source, "import "))
	var result []string
	for _, item := range strings.Split(source, ",") {
		fields := strings.Fields(strings.TrimSpace(item))
		if len(fields) > 0 {
			result = append(result, fields[0])
		}
	}
	return result
}

func normalizeJavaImports(source string) []string {
	source = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(source), "import"))
	source = strings.TrimSpace(strings.TrimPrefix(source, "static"))
	source = strings.TrimSpace(strings.TrimSuffix(source, ";"))
	if source == "" {
		return nil
	}
	return []string{source}
}
