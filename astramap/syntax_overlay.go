package astramap

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"

	"astramap-standalone/languageprotocol"
)

// ParseFileIncrementalWithProfile asks the selected built-in Tree-sitter module
// or an explicit external override for current file-local structural facts.
func ParseFileIncrementalWithProfile(profile ProjectProfile, filePath string) ([]*AstraMapNode, []*AstraMapEdge, string, error) {
	absPath := filePath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(profile.ProjectRoot, filePath)
	}
	code, err := os.ReadFile(absPath)
	if err != nil {
		return nil, nil, "", err
	}
	sum := sha256.Sum256(code)
	contentHash := hex.EncodeToString(sum[:])
	selection, supported := ResolveLanguageWithProfile(profile, filePath)
	if !supported || selection.Module == nil {
		return nil, nil, contentHash, nil
	}
	relPath, err := filepath.Rel(profile.ProjectRoot, absPath)
	if err != nil {
		relPath = filePath
	}
	relPath = filepath.ToSlash(relPath)
	facts, err := selection.Module.Parse(languageprotocol.ParseRequest{
		Language: selection.ID, Dialect: selection.Dialect, ProjectRoot: profile.ProjectRoot,
		RelativePath: relPath, ContentHash: contentHash, Source: code,
	})
	if err != nil {
		return nil, nil, contentHash, err
	}
	nodes, edges, err := languageFactsToGraph(selection, facts, relPath, time.Now().Unix())
	return nodes, edges, contentHash, err
}
