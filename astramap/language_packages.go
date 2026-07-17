package astramap

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"astramap-standalone/languageprotocol"
)

const (
	languageManifestName       = "language.json"
	languageInstallReceiptName = "install-receipt.json"
)

type languageModule interface {
	Manifest() languageprotocol.Manifest
	Parse(languageprotocol.ParseRequest) (languageprotocol.FileFacts, error)
	Close() error
}

type languageRegistrySnapshot struct {
	languages   map[string]*LanguageSpec
	aliases     map[string]string
	extensions  map[string]string
	filenames   map[string]string
	activeCount int
	diagnostics []string
}

type LanguageRuntimeSummary struct {
	BuiltinLanguages   int      `json:"builtinLanguages"`
	InstalledLanguages int      `json:"installedLanguages"`
	EffectiveLanguages int      `json:"effectiveLanguages"`
	Diagnostics        []string `json:"diagnostics,omitempty"`
}

type activeLanguageSet struct {
	Schema   int               `json:"schema"`
	Packages map[string]string `json:"packages"`
}

var languageModulePool = struct {
	sync.Mutex
	modules map[string]*processLanguageModule
}{modules: make(map[string]*processLanguageModule)}

func languageRegistryForProject(projectRoot string) *languageRegistrySnapshot {
	registry := &languageRegistrySnapshot{
		languages:  make(map[string]*LanguageSpec, len(builtinLanguages)),
		aliases:    make(map[string]string),
		extensions: make(map[string]string),
		filenames:  make(map[string]string),
	}
	for i := range builtinLanguages {
		spec := cloneLanguageSpec(&builtinLanguages[i])
		if err := registry.add(spec); err != nil {
			registry.diagnostics = append(registry.diagnostics, err.Error())
		}
	}
	registry.loadActive(userLanguageRoot(), false)
	if projectRoot != "" {
		registry.loadActive(filepath.Join(projectRoot, ".astramap", "languages"), true)
	}
	return registry
}

func cloneLanguageSpec(source *LanguageSpec) *LanguageSpec {
	result := *source
	result.Extensions = append([]string(nil), source.Extensions...)
	result.Filenames = append([]string(nil), source.Filenames...)
	result.Aliases = append([]string(nil), source.Aliases...)
	result.Toolchain = cloneToolchain(source.Toolchain)
	result.projectManifests = append([]string(nil), source.projectManifests...)
	if source.packageSemantic != nil {
		semantic := *source.packageSemantic
		semantic.Args = append([]string(nil), source.packageSemantic.Args...)
		result.packageSemantic = &semantic
	}
	return &result
}

func (r *languageRegistrySnapshot) add(spec *LanguageSpec) error {
	if spec == nil || spec.ID == "" || spec.IDPrefix == "" || (spec.grammar == nil && spec.module == nil) {
		return fmt.Errorf("invalid language module specification")
	}
	if owner := r.languages[spec.ID]; owner != nil {
		return fmt.Errorf("language id conflict %q between %s and %s", spec.ID, owner.sourceName(), spec.sourceName())
	}
	if owner := r.aliases[spec.ID]; owner != "" {
		return fmt.Errorf("language id %q conflicts with alias owned by %s", spec.ID, owner)
	}
	for _, existing := range r.languages {
		if strings.EqualFold(existing.IDPrefix, spec.IDPrefix) {
			return fmt.Errorf("language prefix conflict %q between %s and %s", spec.IDPrefix, existing.ID, spec.ID)
		}
	}
	normalizeDetectionSpec(spec)
	for _, alias := range spec.Aliases {
		key := strings.ToLower(strings.TrimSpace(alias))
		if key == "" || key == spec.ID {
			continue
		}
		if owner := r.languages[key]; owner != nil {
			return fmt.Errorf("language alias %q for %s conflicts with language %s", alias, spec.ID, owner.ID)
		}
		if owner := r.aliases[key]; owner != "" {
			return fmt.Errorf("language alias conflict %q between %s and %s", alias, owner, spec.ID)
		}
	}
	for _, ext := range spec.Extensions {
		if owner := r.extensions[ext]; owner != "" {
			return fmt.Errorf("language extension conflict %q between %s and %s", ext, owner, spec.ID)
		}
	}
	for _, name := range spec.Filenames {
		key := strings.ToLower(name)
		if owner := r.filenames[key]; owner != "" {
			return fmt.Errorf("language filename conflict %q between %s and %s", name, owner, spec.ID)
		}
	}
	r.languages[spec.ID] = spec
	for _, alias := range spec.Aliases {
		key := strings.ToLower(strings.TrimSpace(alias))
		if key != "" && key != spec.ID {
			r.aliases[key] = spec.ID
		}
	}
	for _, ext := range spec.Extensions {
		r.extensions[ext] = spec.ID
	}
	for _, name := range spec.Filenames {
		r.filenames[strings.ToLower(name)] = spec.ID
	}
	return nil
}

func (r *languageRegistrySnapshot) remove(id string) {
	spec := r.languages[id]
	if spec == nil {
		return
	}
	delete(r.languages, id)
	for _, alias := range spec.Aliases {
		key := strings.ToLower(strings.TrimSpace(alias))
		if r.aliases[key] == id {
			delete(r.aliases, key)
		}
	}
	for _, ext := range spec.Extensions {
		if r.extensions[ext] == id {
			delete(r.extensions, ext)
		}
	}
	for _, name := range spec.Filenames {
		key := strings.ToLower(name)
		if r.filenames[key] == id {
			delete(r.filenames, key)
		}
	}
}

func (spec *LanguageSpec) sourceName() string {
	if spec.source != "" {
		return spec.source
	}
	return "builtin:" + spec.ID
}

func (r *languageRegistrySnapshot) supportsExtension(ext string) bool {
	_, ok := r.extensions[strings.ToLower(ext)]
	return ok
}

func (r *languageRegistrySnapshot) languageForExtension(ext string) (string, bool) {
	id, ok := r.extensions[strings.ToLower(ext)]
	return id, ok
}

func (r *languageRegistrySnapshot) languageForFilename(name string) (string, bool) {
	id, ok := r.filenames[strings.ToLower(filepath.Base(name))]
	return id, ok
}

func (r *languageRegistrySnapshot) specForID(id string) *LanguageSpec {
	key := normalizeLanguageID(id)
	if spec := r.languages[key]; spec != nil {
		return spec
	}
	if owner := r.aliases[strings.ToLower(strings.TrimSpace(id))]; owner != "" {
		return r.languages[owner]
	}
	return nil
}

func (r *languageRegistrySnapshot) isPotentialNamedOrScript(path string) bool {
	if _, ok := r.languageForFilename(path); ok {
		return true
	}
	return filepath.Ext(path) == ""
}

func (r *languageRegistrySnapshot) languageFromShebang(path string) (string, string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", false
	}
	defer file.Close()
	buffer := make([]byte, 512)
	n, _ := file.Read(buffer)
	line := strings.ToLower(strings.SplitN(string(buffer[:n]), "\n", 2)[0])
	if !strings.HasPrefix(line, "#!") {
		return "", "", false
	}
	ids := make([]string, 0, len(r.languages))
	for id := range r.languages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		for _, rule := range r.languages[id].Detection.Shebangs {
			for _, needle := range rule.Contains {
				if strings.Contains(line, strings.ToLower(needle)) {
					return id, rule.Dialect, true
				}
			}
		}
	}
	return "", "", false
}

func (r *languageRegistrySnapshot) loadActive(root string, overridePackage bool) {
	active, err := readActiveLanguageSet(root)
	if err != nil {
		if !os.IsNotExist(err) {
			r.diagnostics = append(r.diagnostics, err.Error())
		}
		return
	}
	ids := make([]string, 0, len(active.Packages))
	for id := range active.Packages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		r.activeCount++
		version := active.Packages[id]
		packageDir := filepath.Join(root, "packages", id, version)
		manifest, artifactPath, loadErr := loadInstalledManifest(packageDir)
		if loadErr != nil {
			r.diagnostics = append(r.diagnostics, loadErr.Error())
			continue
		}
		module := pooledProcessLanguageModule(manifest, artifactPath)
		spec := languageSpecFromManifest(manifest, module, packageDir)
		var replaced *LanguageSpec
		if existing := r.languages[spec.ID]; overridePackage && existing != nil && existing.module != nil {
			replaced = existing
			r.remove(spec.ID)
		}
		if addErr := r.add(spec); addErr != nil {
			if replaced != nil {
				_ = r.add(replaced)
			}
			r.diagnostics = append(r.diagnostics, addErr.Error())
		}
	}
}

func LanguageRuntimeForProject(projectRoot string) LanguageRuntimeSummary {
	registry := languageRegistryForProject(projectRoot)
	return LanguageRuntimeSummary{
		BuiltinLanguages: len(builtinLanguages), InstalledLanguages: registry.activeCount,
		EffectiveLanguages: len(registry.languages), Diagnostics: append([]string(nil), registry.diagnostics...),
	}
}

func languageSpecFromManifest(manifest languageprotocol.Manifest, module languageModule, source string) *LanguageSpec {
	extensions := sortedKeys(manifest.Detection.Extensions)
	filenames := sortedKeys(manifest.Detection.Filenames)
	shebangs := make([]ShebangRule, len(manifest.Detection.Shebangs))
	for i, rule := range manifest.Detection.Shebangs {
		shebangs[i] = ShebangRule{Contains: append([]string(nil), rule.Contains...), Dialect: rule.Dialect}
	}
	toolchain := make([]ToolchainRequirement, len(manifest.Toolchain))
	for i, requirement := range manifest.Toolchain {
		toolchain[i] = ToolchainRequirement{
			Label: requirement.Label, Commands: append([]string(nil), requirement.Commands...),
			InstallHint: requirement.InstallHint, WhenAnyFiles: append([]string(nil), requirement.WhenAnyFiles...),
		}
	}
	spec := &LanguageSpec{
		ID: manifest.ID, DisplayName: manifest.DisplayName, Aliases: append([]string(nil), manifest.Aliases...),
		IDPrefix: manifest.IDPrefix, QualifiedSeparator: manifest.QualifiedSeparator,
		Extensions: extensions, Filenames: filenames,
		Detection: DetectionSpec{
			Extensions: cloneStringMap(manifest.Detection.Extensions), Filenames: cloneStringMap(manifest.Detection.Filenames),
			Shebangs: shebangs,
		},
		Capabilities: CapabilitySet{
			Definitions: manifest.Capabilities.Definitions, Containers: manifest.Capabilities.Containers,
			LocalCalls: manifest.Capabilities.LocalCalls, Imports: manifest.Capabilities.Imports,
			CrossFileCalls: manifest.Capabilities.CrossFileCalls, OverloadResolve: manifest.Capabilities.OverloadResolve,
			Implementations: manifest.Capabilities.Implementations, IncrementalSyntax: manifest.Capabilities.IncrementalSyntax,
		},
		Toolchain: toolchain, module: module, source: source,
		packageSemantic: manifest.Semantic, projectManifests: append([]string(nil), manifest.Project.Manifests...),
	}
	if manifest.CaseInsensitive {
		spec.NormalizeIdentity = strings.ToLower
	}
	if manifest.Semantic != nil {
		spec.Semantic = &SemanticBinding{ProviderID: manifest.Semantic.ProviderID, Mode: manifest.Semantic.Mode}
	}
	return spec
}

func sortedKeys(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, strings.ToLower(key))
	}
	sort.Strings(result)
	return result
}

func cloneStringMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[strings.ToLower(key)] = value
	}
	return result
}

func pooledProcessLanguageModule(manifest languageprotocol.Manifest, executable string) *processLanguageModule {
	key := manifest.ID + "@" + manifest.Version + ":" + executable
	languageModulePool.Lock()
	defer languageModulePool.Unlock()
	if module := languageModulePool.modules[key]; module != nil {
		return module
	}
	module := newProcessLanguageModule(manifest, executable)
	languageModulePool.modules[key] = module
	return module
}

func CloseLanguageModules() {
	languageModulePool.Lock()
	modules := languageModulePool.modules
	languageModulePool.modules = make(map[string]*processLanguageModule)
	languageModulePool.Unlock()
	for _, module := range modules {
		_ = module.Close()
	}
}

func userLanguageRoot() string {
	root, err := os.UserConfigDir()
	if err != nil || root == "" {
		root = "."
	}
	return filepath.Join(root, "astramap", "languages")
}

func readActiveLanguageSet(root string) (activeLanguageSet, error) {
	data, err := os.ReadFile(filepath.Join(root, "active.json"))
	if err != nil {
		return activeLanguageSet{}, err
	}
	var active activeLanguageSet
	if err := json.Unmarshal(data, &active); err != nil {
		return activeLanguageSet{}, fmt.Errorf("decode language active lock %s: %w", root, err)
	}
	if active.Schema != 1 || active.Packages == nil {
		return activeLanguageSet{}, fmt.Errorf("invalid language active lock: %s", root)
	}
	for id, version := range active.Packages {
		if err := validateLanguagePackageCoordinate(id, version); err != nil {
			return activeLanguageSet{}, fmt.Errorf("invalid language active lock %s: %w", root, err)
		}
	}
	return active, nil
}

func loadInstalledManifest(packageDir string) (languageprotocol.Manifest, string, error) {
	data, err := os.ReadFile(filepath.Join(packageDir, languageManifestName))
	if err != nil {
		return languageprotocol.Manifest{}, "", fmt.Errorf("read language manifest %s: %w", packageDir, err)
	}
	var manifest languageprotocol.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, "", fmt.Errorf("decode language manifest %s: %w", packageDir, err)
	}
	if err := validateLanguageManifest(manifest); err != nil {
		return manifest, "", fmt.Errorf("validate language manifest %s: %w", packageDir, err)
	}
	if err := verifyLanguageInstallReceipt(packageDir, manifest); err != nil {
		return manifest, "", err
	}
	artifact, ok := artifactForCurrentPlatform(manifest.Artifacts)
	if !ok {
		return manifest, "", fmt.Errorf("language %s %s has no artifact for %s/%s", manifest.ID, manifest.Version, runtime.GOOS, runtime.GOARCH)
	}
	path := filepath.Join(packageDir, filepath.FromSlash(artifact.Path))
	if err := verifyFileSHA256(path, artifact.SHA256); err != nil {
		return manifest, "", err
	}
	return manifest, path, nil
}

func verifyLanguageInstallReceipt(packageDir string, manifest languageprotocol.Manifest) error {
	data, err := os.ReadFile(filepath.Join(packageDir, languageInstallReceiptName))
	if err != nil {
		return fmt.Errorf("read language install receipt %s: %w", packageDir, err)
	}
	var receipt languageInstallReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return fmt.Errorf("decode language install receipt %s: %w", packageDir, err)
	}
	if receipt.Schema != 1 || !strings.EqualFold(receipt.ManifestSHA256, languageManifestSHA256(manifest)) {
		return fmt.Errorf("language install receipt mismatch: %s", packageDir)
	}
	if manifest.Signature == "" {
		if !receipt.Unsigned {
			return fmt.Errorf("unsigned language package has no explicit trust receipt: %s", packageDir)
		}
		return nil
	}
	if receipt.KeyID != manifest.KeyID {
		return fmt.Errorf("language install receipt key mismatch: %s", packageDir)
	}
	publicKey, err := base64.StdEncoding.DecodeString(receipt.PublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize || !VerifyLanguageManifestSignature(manifest, ed25519.PublicKey(publicKey)) {
		return fmt.Errorf("installed language package signature verification failed: %s", packageDir)
	}
	return nil
}

func validateLanguageManifest(manifest languageprotocol.Manifest) error {
	switch {
	case manifest.Schema != 1:
		return fmt.Errorf("unsupported manifest schema: %d", manifest.Schema)
	case manifest.ID == "" || manifest.Version == "" || manifest.DisplayName == "" || manifest.IDPrefix == "":
		return fmt.Errorf("manifest identity is incomplete")
	case strings.TrimSpace(manifest.Publisher) == "":
		return fmt.Errorf("manifest publisher is required")
	case manifest.Signature != "" && strings.TrimSpace(manifest.KeyID) == "":
		return fmt.Errorf("signed manifest key id is required")
	case !languagePackageIDPattern.MatchString(manifest.ID):
		return fmt.Errorf("invalid language package id: %s", manifest.ID)
	case !languagePackageVersionPattern.MatchString(manifest.Version):
		return fmt.Errorf("invalid language package version: %s", manifest.Version)
	case !languagePrefixPattern.MatchString(manifest.IDPrefix):
		return fmt.Errorf("invalid language id prefix: %s", manifest.IDPrefix)
	case manifest.ProtocolMin > languageprotocol.Version || manifest.ProtocolMax < languageprotocol.Version:
		return fmt.Errorf("language protocol range %d-%d is incompatible with %d", manifest.ProtocolMin, manifest.ProtocolMax, languageprotocol.Version)
	case len(manifest.Detection.Extensions) == 0 && len(manifest.Detection.Filenames) == 0 && len(manifest.Detection.Shebangs) == 0:
		return fmt.Errorf("manifest has no file detection rules")
	case len(manifest.Artifacts) == 0:
		return fmt.Errorf("manifest has no worker artifacts")
	}
	for ext := range manifest.Detection.Extensions {
		if !strings.HasPrefix(ext, ".") || ext != strings.ToLower(ext) {
			return fmt.Errorf("invalid extension: %s", ext)
		}
	}
	seenAliases := make(map[string]bool, len(manifest.Aliases))
	for _, alias := range manifest.Aliases {
		key := strings.ToLower(strings.TrimSpace(alias))
		if key == "" || strings.ContainsAny(key, `/\\`) || strings.ContainsRune(key, '\x00') || seenAliases[key] {
			return fmt.Errorf("invalid language alias: %s", alias)
		}
		seenAliases[key] = true
	}
	for name := range manifest.Detection.Filenames {
		if strings.TrimSpace(name) == "" || filepath.Base(name) != name || strings.Contains(name, `\`) {
			return fmt.Errorf("invalid language filename: %s", name)
		}
	}
	for _, shebang := range manifest.Detection.Shebangs {
		if len(shebang.Contains) == 0 {
			return fmt.Errorf("language shebang rule has no match value")
		}
		for _, value := range shebang.Contains {
			if strings.TrimSpace(value) == "" || strings.ContainsRune(value, '\x00') {
				return fmt.Errorf("invalid language shebang match")
			}
		}
	}
	for _, marker := range manifest.Project.Manifests {
		name := strings.TrimSpace(marker)
		if name == "" || strings.ContainsAny(name, `/\\`) || name == "*" {
			return fmt.Errorf("invalid language project manifest: %s", marker)
		}
	}
	if manifest.Semantic != nil && strings.TrimSpace(manifest.Semantic.ProviderID) == "" {
		return fmt.Errorf("language semantic provider id is required")
	}
	seenArtifacts := make(map[string]bool, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		clean, err := cleanArchivePath(artifact.Path)
		if err != nil || clean != artifact.Path || artifact.OS == "" || artifact.Arch == "" || !languageSHA256Pattern.MatchString(artifact.SHA256) {
			return fmt.Errorf("invalid language artifact: %s/%s %s", artifact.OS, artifact.Arch, artifact.Path)
		}
		key := artifact.OS + "/" + artifact.Arch
		if seenArtifacts[key] {
			return fmt.Errorf("duplicate language artifact platform: %s", key)
		}
		if checksum, ok := manifest.Files[artifact.Path]; !ok || !strings.EqualFold(checksum, artifact.SHA256) {
			return fmt.Errorf("language artifact is not covered by signed files: %s", artifact.Path)
		}
		seenArtifacts[key] = true
	}
	for name, checksum := range manifest.Files {
		clean, err := cleanArchivePath(name)
		if err != nil || clean != name || name == languageManifestName || !languageSHA256Pattern.MatchString(checksum) {
			return fmt.Errorf("invalid signed language package file: %s", name)
		}
	}
	return nil
}

func artifactForCurrentPlatform(artifacts []languageprotocol.Artifact) (languageprotocol.Artifact, bool) {
	for _, artifact := range artifacts {
		if artifact.OS == runtime.GOOS && artifact.Arch == runtime.GOARCH {
			return artifact, true
		}
	}
	return languageprotocol.Artifact{}, false
}

func verifyFileSHA256(path, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open language artifact %s: %w", path, err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("hash language artifact %s: %w", path, err)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("language artifact checksum mismatch: %s", path)
	}
	return nil
}
