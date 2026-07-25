// Copyright 2026 AstraMap Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the original license at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package astramap

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ScipRecipe string

const (
	ScipRecipeGo      ScipRecipe = "go"
	ScipRecipeNode    ScipRecipe = "node"
	ScipRecipePython  ScipRecipe = "python"
	ScipRecipeClang   ScipRecipe = "clang"
	ScipRecipeJVM     ScipRecipe = "jvm"
	ScipRecipeRust    ScipRecipe = "rust"
	ScipRecipeDotNet  ScipRecipe = "dotnet"
	ScipRecipePackage ScipRecipe = "package"
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
	Filenames    []string      `json:"filenames,omitempty"`
	Capabilities CapabilitySet `json:"capabilities"`
	ProviderID   string        `json:"semanticProvider,omitempty"`
	AutoSemantic bool          `json:"autoSemantic"`
}

type CapabilityState struct {
	Language       string `json:"language"`
	DeclaredLevel  string `json:"declaredLevel"`
	EffectiveLevel string `json:"effectiveLevel"`
	SyntaxStatus   string `json:"syntaxStatus"`
	ProviderStatus string `json:"providerStatus"`
	Artifacts      int    `json:"artifacts"`
	LastError      string `json:"lastError,omitempty"`
}

type ToolchainRequirement struct {
	Label             string
	Commands          []string
	InstallHint       string
	Installer         *DependencyInstaller
	WhenAnyFiles      []string
	ProjectExecutable string
}

type DependencyInstaller struct {
	Command string
	Args    []string
}

type SemanticNodeNormalizer func(node *AstraMapNode, symbol string, sourceLines []string)
type DetectionResolver func(ProjectProfile, string, string) bool
type IdentityNormalizer func(string) string

type ShebangRule struct {
	Contains []string
	Dialect  string
}

type DetectionSpec struct {
	Extensions map[string]string
	Filenames  map[string]string
	Shebangs   []ShebangRule
	Resolvers  []DetectionResolver
}

type SemanticBinding struct {
	ProviderID string
}

type SemanticProviderSpec struct {
	ID               string
	Tool             string
	InstallHint      string
	Recipe           ScipRecipe
	AutoGenerateScip bool
	Args             []string
	Artifact         string
}

type LanguageSpec struct {
	ID                 string
	DisplayName        string
	Aliases            []string
	IDPrefix           string
	QualifiedSeparator string
	Extensions         []string
	Filenames          []string
	Detection          DetectionSpec
	Semantic           *SemanticBinding
	Capabilities       CapabilitySet
	Toolchain          []ToolchainRequirement
	NormalizeSemantic  SemanticNodeNormalizer
	NormalizeIdentity  IdentityNormalizer
	module             languageModule
	source             string
	projectManifests   []string
}

type ProjectProfile struct {
	ProjectRoot     string
	ExtensionCounts map[string]int
	LanguageCounts  map[string]int
	registry        *languageRegistrySnapshot
}

type LanguageSelection struct {
	ID      string
	Dialect string
	Module  languageModule
	Spec    *LanguageSpec
}

var semanticProviders = map[string]SemanticProviderSpec{
	"go": {
		ID: "go", Tool: "scip-go",
		InstallHint: "go install github.com/scip-code/scip-go/cmd/scip-go@latest",
		Recipe:      ScipRecipeGo, AutoGenerateScip: true,
	},
	"typescript": {
		ID: "typescript", Tool: "scip-typescript",
		InstallHint: "npm install -g @sourcegraph/scip-typescript",
		Recipe:      ScipRecipeNode, AutoGenerateScip: true,
	},
	"python": {
		ID: "python", Tool: "scip-python",
		InstallHint: "pip install scip-python",
		Recipe:      ScipRecipePython, AutoGenerateScip: true,
	},
	"java": {
		ID: "java", Tool: "scip-java",
		InstallHint: "See https://github.com/sourcegraph/scip-java",
		Recipe:      ScipRecipeJVM, AutoGenerateScip: true,
	},
	"rust": {
		ID: "rust", Tool: "rust-analyzer",
		InstallHint: "rustup component add rust-analyzer",
		Recipe:      ScipRecipeRust, AutoGenerateScip: true,
	},
	"dotnet": {
		ID: "dotnet", Tool: "scip-dotnet",
		InstallHint: "dotnet tool install --global scip-dotnet",
		Recipe:      ScipRecipeDotNet, AutoGenerateScip: true,
	},
	"ruby": {
		ID: "ruby", Tool: "scip-ruby",
		InstallHint: "gem install scip-ruby",
		Recipe:      ScipRecipePackage, AutoGenerateScip: true,
		Args: []string{"index"}, Artifact: "index.scip",
	},
	"clang": {
		ID: "clang", Tool: "scip-clang",
		InstallHint: "See https://github.com/sourcegraph/scip-clang",
		Recipe:      ScipRecipeClang, AutoGenerateScip: true,
	},
}

var semanticCapabilities = CapabilitySet{
	Definitions: true, CrossFileCalls: true, OverloadResolve: true, Implementations: true,
}

var builtinLanguages = []LanguageSpec{
	{
		ID: "go", DisplayName: "Go", IDPrefix: "go", QualifiedSeparator: ".", Extensions: []string{".go"},
		Semantic:          &SemanticBinding{ProviderID: "go"},
		Capabilities:      semanticCapabilities,
		NormalizeSemantic: normalizeGoScipNode,
		Toolchain: []ToolchainRequirement{
			{Label: "Go compiler", Commands: []string{"go"}, InstallHint: "https://go.dev/doc/install"},
		},
	},
	{
		ID: "typescript", DisplayName: "TypeScript", Aliases: []string{"ts"}, IDPrefix: "ts", QualifiedSeparator: ".",
		Extensions: []string{".ts", ".tsx"}, Semantic: &SemanticBinding{ProviderID: "typescript"},
		Capabilities: semanticCapabilities,
		Toolchain: []ToolchainRequirement{
			{Label: "Node.js", Commands: []string{"node"}, InstallHint: "Ubuntu/Debian: sudo apt install nodejs npm | macOS: brew install node"},
			{Label: "Package manager", Commands: []string{"pnpm", "yarn", "npm"}, InstallHint: "Ubuntu/Debian: sudo apt install npm | macOS: brew install node"},
			{
				Label: "TypeScript compiler", Commands: []string{"tsc"}, InstallHint: "npm install -g typescript",
				Installer:         &DependencyInstaller{Command: "npm", Args: []string{"install", "-g", "typescript"}},
				WhenAnyFiles:      []string{"tsconfig.json"},
				ProjectExecutable: filepath.Join("node_modules", ".bin", "tsc"),
			},
		},
	},
	{
		ID: "javascript", DisplayName: "JavaScript", Aliases: []string{"js"}, IDPrefix: "js", QualifiedSeparator: ".",
		Extensions: []string{".js", ".jsx", ".mjs", ".cjs"}, Semantic: &SemanticBinding{ProviderID: "typescript"},
		Capabilities: semanticCapabilities,
		Toolchain: []ToolchainRequirement{
			{Label: "Node.js", Commands: []string{"node"}, InstallHint: "Ubuntu/Debian: sudo apt install nodejs npm | macOS: brew install node"},
			{Label: "Package manager", Commands: []string{"pnpm", "yarn", "npm"}, InstallHint: "Ubuntu/Debian: sudo apt install npm | macOS: brew install node"},
		},
	},
	{
		ID: "python", DisplayName: "Python", IDPrefix: "py", QualifiedSeparator: ".", Extensions: []string{".py"},
		Semantic:     &SemanticBinding{ProviderID: "python"},
		Capabilities: semanticCapabilities,
		Toolchain: []ToolchainRequirement{
			{Label: "Python interpreter", Commands: []string{"python3", "python"}, InstallHint: "Ubuntu/Debian: sudo apt install python3 python3-pip | macOS: brew install python"},
			{Label: "pip", Commands: []string{"pip3", "pip"}, InstallHint: "Ubuntu/Debian: sudo apt install python3-pip | macOS: python3 -m ensurepip"},
		},
	},
	{
		ID: "java", DisplayName: "Java", IDPrefix: "java", QualifiedSeparator: ".", Extensions: []string{".java"},
		Semantic:     &SemanticBinding{ProviderID: "java"},
		Capabilities: semanticCapabilities,
		Toolchain: []ToolchainRequirement{
			{Label: "Java runtime", Commands: []string{"java"}, InstallHint: "Ubuntu/Debian: sudo apt install default-jdk | macOS: brew install openjdk"},
			{Label: "Java compiler", Commands: []string{"javac"}, InstallHint: "Ubuntu/Debian: sudo apt install default-jdk | macOS: brew install openjdk"},
			{Label: "Maven", Commands: []string{"mvn"}, InstallHint: "Ubuntu/Debian: sudo apt install maven | macOS: brew install maven", WhenAnyFiles: []string{"pom.xml"}},
			{Label: "Gradle", Commands: []string{"gradle"}, InstallHint: "Ubuntu/Debian: sudo apt install gradle | macOS: brew install gradle", WhenAnyFiles: []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", "gradlew"}, ProjectExecutable: "gradlew"},
		},
	},
	{
		ID: "c", DisplayName: "C", IDPrefix: "c", QualifiedSeparator: ".", Extensions: []string{".c", ".h"},
		Semantic:          &SemanticBinding{ProviderID: "clang"},
		Capabilities:      semanticCapabilities,
		NormalizeSemantic: normalizeCScipNode,
	},
	{
		ID: "cpp", DisplayName: "C++", Aliases: []string{"c++", "cplusplus"}, IDPrefix: "cxx", QualifiedSeparator: "::",
		Extensions:        []string{".cc", ".cpp", ".cxx", ".hpp", ".hxx"},
		Semantic:          &SemanticBinding{ProviderID: "clang"},
		Capabilities:      semanticCapabilities,
		NormalizeSemantic: normalizeCScipNode,
	},
	{
		ID: "rust", DisplayName: "Rust", Aliases: []string{"rs"}, IDPrefix: "rs", QualifiedSeparator: "::",
		Extensions: []string{".rs"}, Semantic: &SemanticBinding{ProviderID: "rust"}, Capabilities: semanticCapabilities,
		Toolchain: []ToolchainRequirement{{Label: "Rust toolchain", Commands: []string{"cargo", "rustc"}, InstallHint: "https://rustup.rs"}},
	},
	{
		ID: "csharp", DisplayName: "C#", Aliases: []string{"cs", "c#"}, IDPrefix: "cs", QualifiedSeparator: ".",
		Extensions: []string{".cs"}, Semantic: &SemanticBinding{ProviderID: "dotnet"}, Capabilities: semanticCapabilities,
		Toolchain: []ToolchainRequirement{{Label: ".NET SDK", Commands: []string{"dotnet"}, InstallHint: "https://dotnet.microsoft.com/download"}},
	},
	{
		ID: "kotlin", DisplayName: "Kotlin", Aliases: []string{"kt"}, IDPrefix: "kt", QualifiedSeparator: ".",
		Extensions: []string{".kt", ".kts"}, Detection: DetectionSpec{Extensions: map[string]string{".kt": "kotlin", ".kts": "kotlin-script"}},
		Semantic: &SemanticBinding{ProviderID: "java"}, Capabilities: semanticCapabilities,
		Toolchain: []ToolchainRequirement{
			{Label: "JDK", Commands: []string{"java", "javac"}, InstallHint: "https://adoptium.net"},
			{Label: "Gradle", Commands: []string{"gradle"}, InstallHint: "https://gradle.org/install", WhenAnyFiles: []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"}, ProjectExecutable: "gradlew"},
		},
	},
	{
		ID: "ruby", DisplayName: "Ruby", Aliases: []string{"rb"}, IDPrefix: "rb", QualifiedSeparator: "::",
		Extensions: []string{".rb", ".rake"}, Filenames: []string{"Gemfile", "Rakefile", "Guardfile"},
		Detection: DetectionSpec{Shebangs: []ShebangRule{{Contains: []string{"ruby"}, Dialect: "ruby"}}},
		Semantic:  &SemanticBinding{ProviderID: "ruby"}, Capabilities: semanticCapabilities,
		Toolchain:        []ToolchainRequirement{{Label: "Ruby", Commands: []string{"ruby"}, InstallHint: "https://www.ruby-lang.org/en/downloads/"}},
		projectManifests: []string{"Gemfile"},
	},
	{
		ID: "scala", DisplayName: "Scala", IDPrefix: "scala", QualifiedSeparator: ".",
		Extensions: []string{".scala", ".sc"}, Semantic: &SemanticBinding{ProviderID: "java"}, Capabilities: semanticCapabilities,
		Toolchain: []ToolchainRequirement{
			{Label: "JDK", Commands: []string{"java", "javac"}, InstallHint: "https://adoptium.net"},
			{Label: "sbt", Commands: []string{"sbt"}, InstallHint: "https://www.scala-sbt.org/download", WhenAnyFiles: []string{"build.sbt"}},
			{Label: "Gradle", Commands: []string{"gradle"}, InstallHint: "https://gradle.org/install", WhenAnyFiles: []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"}, ProjectExecutable: "gradlew"},
		},
		projectManifests: []string{"build.sbt"},
	},
}

var (
	languageByID  map[string]*LanguageSpec
	extensionToID map[string]string
	filenameToID  map[string]string
)

func init() {
	attachBuiltinSyntaxModules()
	languageByID = make(map[string]*LanguageSpec, len(builtinLanguages))
	extensionToID = make(map[string]string)
	filenameToID = make(map[string]string)
	prefixes := make(map[string]string)
	for i := range builtinLanguages {
		spec := &builtinLanguages[i]
		if spec.ID == "" || spec.DisplayName == "" || spec.IDPrefix == "" || spec.Semantic == nil {
			panic("incomplete language spec: " + spec.ID)
		}
		if _, exists := languageByID[spec.ID]; exists {
			panic("duplicate language id: " + spec.ID)
		}
		if owner, exists := prefixes[spec.IDPrefix]; exists {
			panic("duplicate language id prefix: " + spec.IDPrefix + " (" + owner + ", " + spec.ID + ")")
		}
		if _, ok := semanticProviderForSpec(spec); !ok {
			panic("unknown semantic provider: " + spec.Semantic.ProviderID)
		}
		normalizeDetectionSpec(spec)
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
		for _, filename := range spec.Filenames {
			key := strings.ToLower(filename)
			if owner, exists := filenameToID[key]; exists {
				panic("ambiguous filename without resolver: " + filename + " (" + owner + ", " + spec.ID + ")")
			}
			filenameToID[key] = spec.ID
		}
	}
}

func LanguageSpecs() []LanguageSpec {
	result := make([]LanguageSpec, len(builtinLanguages))
	copy(result, builtinLanguages)
	for i := range result {
		result[i].Extensions = append([]string(nil), result[i].Extensions...)
		result[i].Filenames = append([]string(nil), result[i].Filenames...)
		result[i].Aliases = append([]string(nil), result[i].Aliases...)
		result[i].Toolchain = cloneToolchain(result[i].Toolchain)
	}
	return result
}

func LanguageSpecsForProject(projectRoot string) []LanguageSpec {
	registry := languageRegistryForProject(projectRoot)
	ids := make([]string, 0, len(registry.languages))
	for id := range registry.languages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]LanguageSpec, 0, len(ids))
	for _, id := range ids {
		result = append(result, *cloneLanguageSpec(registry.languages[id]))
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
	result.Filenames = append([]string(nil), spec.Filenames...)
	result.Aliases = append([]string(nil), spec.Aliases...)
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
		if source[i].Installer != nil {
			installer := *source[i].Installer
			installer.Args = append([]string(nil), source[i].Installer.Args...)
			result[i].Installer = &installer
		}
	}
	return result
}

func normalizeLanguageID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, spec := range builtinLanguages {
		for _, alias := range spec.Aliases {
			if id == strings.ToLower(alias) {
				return spec.ID
			}
		}
	}
	return id
}

func IsKnownLanguage(id string) bool {
	_, ok := languageByID[normalizeLanguageID(id)]
	return ok
}

func IsKnownLanguageForProject(projectRoot, id string) bool {
	registry := languageRegistryForProject(projectRoot)
	id = strings.ToLower(strings.TrimSpace(id))
	_, language := registry.languages[id]
	_, alias := registry.aliases[id]
	return language || alias
}

func NormalizeLanguageIDForProject(projectRoot, id string) (string, bool) {
	registry := languageRegistryForProject(projectRoot)
	id = strings.ToLower(strings.TrimSpace(id))
	if _, ok := registry.languages[id]; ok {
		return id, true
	}
	if owner := registry.aliases[id]; owner != "" {
		return owner, true
	}
	return "", false
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

func SupportedLanguageIDsForProject(projectRoot string) []string {
	registry := languageRegistryForProject(projectRoot)
	result := make([]string, 0, len(registry.languages))
	for id := range registry.languages {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func SupportedLanguageCapabilities() []LanguageCapabilityInfo {
	result := make([]LanguageCapabilityInfo, 0, len(builtinLanguages))
	for i := range builtinLanguages {
		spec := &builtinLanguages[i]
		providerID, autoSemantic := "", false
		if spec.Semantic != nil {
			provider := semanticProviders[spec.Semantic.ProviderID]
			providerID, autoSemantic = provider.ID, provider.AutoGenerateScip
		}
		result = append(result, LanguageCapabilityInfo{
			ID: spec.ID, DisplayName: spec.DisplayName, Level: capabilityLevel(spec.Capabilities),
			Extensions: append([]string(nil), spec.Extensions...), Filenames: append([]string(nil), spec.Filenames...),
			Capabilities: spec.Capabilities, ProviderID: providerID, AutoSemantic: autoSemantic,
		})
	}
	return result
}

func SupportedLanguageCapabilitiesForProject(projectRoot string) []LanguageCapabilityInfo {
	registry := languageRegistryForProject(projectRoot)
	ids := make([]string, 0, len(registry.languages))
	for id := range registry.languages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]LanguageCapabilityInfo, 0, len(ids))
	for _, id := range ids {
		spec := registry.languages[id]
		providerID, autoSemantic := "", false
		if provider, ok := semanticProviderForSpec(spec); ok {
			providerID, autoSemantic = provider.ID, provider.AutoGenerateScip
		}
		result = append(result, LanguageCapabilityInfo{
			ID: spec.ID, DisplayName: spec.DisplayName, Level: capabilityLevel(spec.Capabilities),
			Extensions: append([]string(nil), spec.Extensions...), Filenames: append([]string(nil), spec.Filenames...),
			Capabilities: spec.Capabilities, ProviderID: providerID, AutoSemantic: autoSemantic,
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
	default:
		return "unavailable"
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

func LanguageIDForFilename(name string) (string, bool) {
	id, ok := filenameToID[strings.ToLower(filepath.Base(name))]
	return id, ok
}

func IsSupportedExtension(ext string) bool {
	_, ok := LanguageIDForExtension(ext)
	return ok
}

func IsPotentialSupportedPath(path string) bool {
	if IsSupportedExtension(strings.ToLower(filepath.Ext(path))) {
		return true
	}
	return isPotentialNamedOrScriptFile(path)
}

func IsPotentialSupportedPathForProject(projectRoot, path string) bool {
	registry := languageRegistryForProject(projectRoot)
	if registry.supportsExtension(strings.ToLower(filepath.Ext(path))) {
		return true
	}
	return registry.isPotentialNamedOrScript(path)
}

func IsSupportedFile(profile ProjectProfile, filePath string) bool {
	_, ok := ResolveLanguageWithProfile(profile, filePath)
	return ok
}

func IsLanguageFile(profile ProjectProfile, filePath string, languages map[string]bool) bool {
	selection, ok := ResolveLanguageWithProfile(profile, filePath)
	return ok && (len(languages) == 0 || languages[selection.ID])
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

func languageIDPrefixForProfile(profile ProjectProfile, id string) string {
	registry := profile.registry
	if registry == nil {
		registry = languageRegistryForProject(profile.ProjectRoot)
	}
	if spec := registry.specForID(id); spec != nil {
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

func LanguageIdentity(id, value string) string {
	spec := languageByID[normalizeLanguageID(id)]
	if spec != nil && spec.NormalizeIdentity != nil {
		return spec.NormalizeIdentity(value)
	}
	return value
}

func SemanticProviderForLanguage(id string) (SemanticProviderSpec, bool) {
	spec := languageByID[normalizeLanguageID(id)]
	if spec == nil || spec.Semantic == nil {
		return SemanticProviderSpec{}, false
	}
	provider, ok := semanticProviders[spec.Semantic.ProviderID]
	return provider, ok
}

func SemanticProviderForProjectLanguage(projectRoot, id string) (SemanticProviderSpec, bool) {
	registry := languageRegistryForProject(projectRoot)
	spec := registry.specForID(id)
	return semanticProviderForSpec(spec)
}

func SemanticProviderForProjectByID(projectRoot, id string) (SemanticProviderSpec, bool) {
	registry := languageRegistryForProject(projectRoot)
	for _, spec := range registry.languages {
		provider, ok := semanticProviderForSpec(spec)
		if ok && provider.ID == id {
			return provider, true
		}
	}
	return SemanticProviderSpec{}, false
}

func semanticProviderForSpec(spec *LanguageSpec) (SemanticProviderSpec, bool) {
	if spec == nil || spec.Semantic == nil {
		return SemanticProviderSpec{}, false
	}
	if provider, ok := semanticProviders[spec.Semantic.ProviderID]; ok {
		return provider, true
	}
	return SemanticProviderSpec{}, false
}

func LanguageToolchainRequirementsForProject(projectRoot, id string) []ToolchainRequirement {
	registry := languageRegistryForProject(projectRoot)
	if spec := registry.specForID(id); spec != nil {
		return cloneToolchain(spec.Toolchain)
	}
	return nil
}

func LanguageDisplayNameForProject(projectRoot, id string) string {
	registry := languageRegistryForProject(projectRoot)
	if spec := registry.specForID(id); spec != nil {
		return spec.DisplayName
	}
	return id
}

func SemanticProviderByID(id string) (SemanticProviderSpec, bool) {
	provider, ok := semanticProviders[strings.ToLower(strings.TrimSpace(id))]
	return provider, ok
}

func SemanticProviderSpecs() []SemanticProviderSpec {
	result := make([]SemanticProviderSpec, 0, len(semanticProviders))
	for _, provider := range semanticProviders {
		result = append(result, provider)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func SemanticProviderSpecsForProject(projectRoot string) []SemanticProviderSpec {
	registry := languageRegistryForProject(projectRoot)
	providers := make(map[string]SemanticProviderSpec)
	for _, spec := range registry.languages {
		if provider, ok := semanticProviderForSpec(spec); ok {
			providers[provider.ID] = provider
		}
	}
	result := make([]SemanticProviderSpec, 0, len(providers))
	for _, provider := range providers {
		result = append(result, provider)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
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
	return ok && provider.AutoGenerateScip && provider.Tool != "" && provider.Recipe != ""
}

func ScipToolNameForProject(projectRoot, id string) string {
	provider, _ := SemanticProviderForProjectLanguage(projectRoot, id)
	return provider.Tool
}

func ScipInstallHintForProject(projectRoot, id string) string {
	provider, _ := SemanticProviderForProjectLanguage(projectRoot, id)
	return provider.InstallHint
}

func CanAutoGenerateScipForProject(projectRoot, id string) bool {
	provider, ok := SemanticProviderForProjectLanguage(projectRoot, id)
	return ok && provider.AutoGenerateScip && provider.Tool != "" && provider.Recipe != ""
}

func normalizeSemanticNode(profile ProjectProfile, language string, node *AstraMapNode, symbol string, sourceLines []string) {
	registry := profile.registry
	if registry == nil {
		registry = languageRegistryForProject(profile.ProjectRoot)
	}
	spec := registry.specForID(language)
	if spec != nil && spec.NormalizeSemantic != nil {
		spec.NormalizeSemantic(node, symbol, sourceLines)
	}
}

func BuildProjectProfile(projectRoot string, filter *IndexFilter, stage IndexStage) ProjectProfile {
	profile := ProjectProfile{
		ProjectRoot: projectRoot, ExtensionCounts: make(map[string]int), LanguageCounts: make(map[string]int),
		registry: languageRegistryForProject(projectRoot),
	}
	var files []string
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
		if (profile.registry.supportsExtension(ext) || profile.registry.isPotentialNamedOrScript(path)) && (filter == nil || filter.Allows(relPath, stage)) {
			profile.ExtensionCounts[ext]++
			files = append(files, path)
		}
		return nil
	})
	for _, path := range files {
		if selection, ok := ResolveLanguageWithProfile(profile, path); ok {
			profile.LanguageCounts[selection.ID]++
		}
	}
	return profile
}

func ResolveLanguageWithProfile(profile ProjectProfile, filePath string) (LanguageSelection, bool) {
	registry := profile.registry
	if registry == nil {
		registry = languageRegistryForProject(profile.ProjectRoot)
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	id, ok := registry.languageForFilename(filepath.Base(filePath))
	dialect := ""
	if ok {
		dialect = registry.languages[id].Detection.Filenames[strings.ToLower(filepath.Base(filePath))]
	} else {
		id, ok = registry.languageForExtension(ext)
		if ok {
			dialect = registry.languages[id].Detection.Extensions[ext]
		}
	}
	if !ok && ext == "" {
		id, dialect, ok = registry.languageFromShebang(filePath)
	}
	if !ok {
		return LanguageSelection{}, false
	}
	if ext == ".h" && hasCxxProfile(profile) {
		id = "cpp"
	}
	spec := registry.languages[id]
	if spec == nil || spec.Semantic == nil {
		return LanguageSelection{}, false
	}
	if dialect == "" {
		dialect = dialectForExtension(id, ext)
	}
	for _, resolver := range spec.Detection.Resolvers {
		if !resolver(profile, filePath, dialect) {
			return LanguageSelection{}, false
		}
	}
	return LanguageSelection{ID: id, Dialect: dialect, Module: spec.module, Spec: spec}, true
}

func ProjectLanguageCounts(profile ProjectProfile) map[string]int {
	if len(profile.LanguageCounts) > 0 {
		result := make(map[string]int, len(profile.LanguageCounts))
		for lang, count := range profile.LanguageCounts {
			result[lang] = count
		}
		return result
	}
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

func normalizeDetectionSpec(spec *LanguageSpec) {
	if spec.Detection.Extensions == nil {
		spec.Detection.Extensions = make(map[string]string, len(spec.Extensions))
	}
	for _, ext := range spec.Extensions {
		if _, exists := spec.Detection.Extensions[ext]; !exists {
			spec.Detection.Extensions[ext] = strings.TrimPrefix(ext, ".")
		}
	}
	if len(spec.Extensions) == 0 {
		for ext := range spec.Detection.Extensions {
			spec.Extensions = append(spec.Extensions, ext)
		}
		sort.Strings(spec.Extensions)
	}
	if spec.Detection.Filenames == nil {
		spec.Detection.Filenames = make(map[string]string, len(spec.Filenames))
	}
	for _, filename := range spec.Filenames {
		key := strings.ToLower(filename)
		if _, exists := spec.Detection.Filenames[key]; !exists {
			spec.Detection.Filenames[key] = key
		}
	}
	if len(spec.Filenames) == 0 {
		for filename := range spec.Detection.Filenames {
			spec.Filenames = append(spec.Filenames, filename)
		}
		sort.Strings(spec.Filenames)
	}
}

func isPotentialNamedOrScriptFile(path string) bool {
	if _, ok := LanguageIDForFilename(filepath.Base(path)); ok {
		return true
	}
	return filepath.Ext(path) == ""
}

func languageFromShebang(filePath string) (string, string, bool) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", "", false
	}
	defer file.Close()
	buffer := make([]byte, 256)
	count, _ := file.Read(buffer)
	firstLine := strings.SplitN(string(buffer[:count]), "\n", 2)[0]
	if !strings.HasPrefix(firstLine, "#!") {
		return "", "", false
	}
	lower := strings.ToLower(firstLine)
	for i := range builtinLanguages {
		for _, rule := range builtinLanguages[i].Detection.Shebangs {
			for _, marker := range rule.Contains {
				if strings.Contains(lower, strings.ToLower(marker)) {
					return builtinLanguages[i].ID, rule.Dialect, true
				}
			}
		}
	}
	return "", "", false
}
