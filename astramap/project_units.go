package astramap

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ProjectUnit struct {
	Root       string   `json:"root"`
	Ecosystem  string   `json:"ecosystem"`
	ProviderID string   `json:"providerId"`
	Manifests  []string `json:"manifests"`
	Languages  []string `json:"languages"`
	Identity   string   `json:"identity"`
}

type projectMarker struct {
	Ecosystem string
	Providers []string
	Exact     []string
	Suffixes  []string
}

var projectMarkers = []projectMarker{
	{Ecosystem: "go", Providers: []string{"go"}, Exact: []string{"go.mod"}},
	{Ecosystem: "node", Providers: []string{"typescript"}, Exact: []string{"package.json", "tsconfig.json"}},
	{Ecosystem: "python", Providers: []string{"python"}, Exact: []string{"pyproject.toml", "setup.py"}},
	{Ecosystem: "clang", Providers: []string{"clang"}, Exact: []string{"compile_commands.json", "CMakeLists.txt", "Makefile", "makefile"}},
	{Ecosystem: "jvm", Providers: []string{"java"}, Exact: []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"}},
	{Ecosystem: "rust", Providers: []string{"rust"}, Exact: []string{"Cargo.toml"}},
	{Ecosystem: "dotnet", Providers: []string{"dotnet"}, Suffixes: []string{".sln", ".csproj"}},
	{Ecosystem: "php", Providers: []string{"php"}, Exact: []string{"composer.json"}},
}

func DetectProjectUnits(projectRoot string, languages []string, filter *IndexFilter) []ProjectUnit {
	registry := languageRegistryForProject(projectRoot)
	allowedProviders := make(map[string][]string)
	for _, language := range languages {
		if spec := registry.specForID(language); spec != nil && spec.Semantic != nil {
			allowedProviders[spec.Semantic.ProviderID] = append(allowedProviders[spec.Semantic.ProviderID], spec.ID)
		}
	}
	markers := append([]projectMarker(nil), projectMarkers...)
	for _, spec := range registry.languages {
		if spec.Semantic == nil || len(spec.projectManifests) == 0 {
			continue
		}
		marker := projectMarker{Ecosystem: spec.ID, Providers: []string{spec.Semantic.ProviderID}}
		for _, pattern := range spec.projectManifests {
			if strings.HasPrefix(pattern, "*") {
				marker.Suffixes = append(marker.Suffixes, strings.TrimPrefix(pattern, "*"))
			} else {
				marker.Exact = append(marker.Exact, pattern)
			}
		}
		markers = append(markers, marker)
	}

	type unitKey struct {
		root, provider string
	}
	units := make(map[unitKey]*ProjectUnit)
	_ = filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(projectRoot, path)
		if info.IsDir() {
			if path != projectRoot && (hasHiddenSegment(info.Name()) || (filter != nil && !filter.AllowsDir(rel, StageScip))) {
				return filepath.SkipDir
			}
			return nil
		}
		for _, marker := range markers {
			if !markerMatches(marker, info.Name()) {
				continue
			}
			for _, provider := range marker.Providers {
				langs := allowedProviders[provider]
				if len(langs) == 0 {
					continue
				}
				root := filepath.Dir(path)
				key := unitKey{root: root, provider: provider}
				unit := units[key]
				if unit == nil {
					unit = &ProjectUnit{
						Root: root, Ecosystem: marker.Ecosystem, ProviderID: provider,
						Languages: append([]string(nil), langs...),
					}
					units[key] = unit
				}
				unit.Manifests = append(unit.Manifests, path)
			}
		}
		return nil
	})

	for provider, langs := range allowedProviders {
		found := false
		for key := range units {
			found = found || key.provider == provider
		}
		if !found {
			units[unitKey{root: projectRoot, provider: provider}] = &ProjectUnit{
				Root: projectRoot, Ecosystem: provider, ProviderID: provider,
				Languages: append([]string(nil), langs...),
			}
		}
	}

	result := make([]ProjectUnit, 0, len(units))
	for _, unit := range units {
		sort.Strings(unit.Manifests)
		sort.Strings(unit.Languages)
		sum := sha256.Sum256([]byte(unit.ProviderID + "\x00" + unit.Root + "\x00" + strings.Join(unit.Manifests, "\x00")))
		unit.Identity = fmt.Sprintf("%x", sum[:8])
		result = append(result, *unit)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ProviderID == result[j].ProviderID {
			return result[i].Root < result[j].Root
		}
		return result[i].ProviderID < result[j].ProviderID
	})
	result = mergeProviderSubUnits(result, projectRoot)
	return result
}

func markerMatches(marker projectMarker, name string) bool {
	for _, exact := range marker.Exact {
		if name == exact {
			return true
		}
	}
	lower := strings.ToLower(name)
	for _, suffix := range marker.Suffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// mergeProviderSubUnits keeps only top-level roots for each provider. A nested
// manifest configures its ancestor unit; sibling roots remain independent.
func mergeProviderSubUnits(units []ProjectUnit, projectRoot string) []ProjectUnit {
	roots := make(map[string]map[string]bool)
	for _, unit := range units {
		if roots[unit.ProviderID] == nil {
			roots[unit.ProviderID] = make(map[string]bool)
		}
		roots[unit.ProviderID][filepath.Clean(unit.Root)] = true
	}

	result := make([]ProjectUnit, 0, len(units))
	for _, unit := range units {
		root := filepath.Clean(unit.Root)
		if hasProjectUnitAncestor(root, projectRoot, roots[unit.ProviderID]) {
			continue
		}
		result = append(result, unit)
	}
	return result
}

func hasProjectUnitAncestor(dir, projectRoot string, owners map[string]bool) bool {
	if len(owners) == 0 {
		return false
	}
	root := filepath.Clean(projectRoot)
	for parent := filepath.Dir(dir); ; parent = filepath.Dir(parent) {
		if owners[parent] {
			return true
		}
		if parent == root || parent == filepath.Dir(parent) {
			return false
		}
	}
}
