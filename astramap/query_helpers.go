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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jmoiron/sqlx"
)

var (
	filePathCache = make(map[string]string)
	filePathMu    sync.RWMutex

	sourceLineCache = make(map[sourceCacheKey][]string)
	sourceLineMu    sync.RWMutex
)

type sourceCacheKey struct {
	ProjectRoot string
	FilePath    string
	ModTimeUnix int64
	Size        int64
}

func InvalidateQueryHelperCache() {
	filePathMu.Lock()
	filePathCache = make(map[string]string)
	filePathMu.Unlock()

	sourceLineMu.Lock()
	sourceLineCache = make(map[sourceCacheKey][]string)
	sourceLineMu.Unlock()
}

func InvalidateQueryHelperCacheForFile(filePath string) {
	filePath = filepath.ToSlash(strings.TrimPrefix(filepath.Clean(filePath), string(filepath.Separator)))

	filePathMu.Lock()
	for nodeID, cachedFilePath := range filePathCache {
		if filepath.ToSlash(cachedFilePath) == filePath {
			delete(filePathCache, nodeID)
		}
	}
	filePathMu.Unlock()

	sourceLineMu.Lock()
	for key := range sourceLineCache {
		if filepath.ToSlash(key.FilePath) == filePath {
			delete(sourceLineCache, key)
		}
	}
	sourceLineMu.Unlock()
}

var allowedSearchKinds = map[string]struct{}{
	"function":  {},
	"method":    {},
	"class":     {},
	"struct":    {},
	"interface": {},
	"enum":      {},
	"typedef":   {},
	"type":      {},
	"macro":     {},
	"variable":  {},
	"route":     {},
	"external":  {},
}

func validateSearchKind(kind string) error {
	if kind == "" {
		return nil
	}
	if _, ok := allowedSearchKinds[kind]; ok {
		return nil
	}
	return fmt.Errorf("invalid search kind: %s", kind)
}

func normalizeImpactDepth(depth int) int {
	if depth < 0 {
		return 1
	}
	return depth
}

func resolvePrimarySymbolID(db *sqlx.DB, symbol string) (string, error) {
	ids, err := ResolveSymbolToIDs(db, symbol)
	if err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", nil
	}
	return ids[0], nil
}

func annotateConditionalMetadata(db *sqlx.DB, edge *AstraMapEdge) {
	annotateConditionalMetadataWithFileMap(db, projectRootFromCwd(), nil, edge)
}

func annotateConditionalMetadataWithFileMap(db *sqlx.DB, projectRoot string, sourceFileMap map[string]string, edge *AstraMapEdge) {
	if edge == nil || edge.Line <= 0 || edge.Source == "" {
		return
	}
	if strings.TrimSpace(edge.Metadata) != "" {
		return
	}

	filePath := ""
	if sourceFileMap != nil {
		filePath = sourceFileMap[edge.Source]
	}
	if filePath == "" {
		filePathMu.RLock()
		filePath = filePathCache[edge.Source]
		filePathMu.RUnlock()
	}
	if filePath == "" && db != nil {
		var fp string
		if err := db.Get(&fp, "SELECT file_path FROM astramap_nodes WHERE id = ? LIMIT 1", edge.Source); err == nil && fp != "" {
			filePath = fp
			filePathMu.Lock()
			filePathCache[edge.Source] = fp
			filePathMu.Unlock()
		}
	}

	if filePath == "" {
		return
	}

	lines := cachedSourceLines(projectRoot, filePath)
	if len(lines) == 0 {
		return
	}

	guards := activePreprocessorGuards(lines, edge.Line)
	if len(guards) == 0 {
		return
	}
	edge.Metadata = strings.Join(guards, " && ")
}

func BatchNodeFilePaths(db *sqlx.DB, ids []string) (map[string]string, error) {
	result := make(map[string]string)
	if len(ids) == 0 {
		return result, nil
	}
	uniq := make(map[string]struct{})
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, exists := uniq[id]; exists {
			continue
		}
		uniq[id] = struct{}{}
		filePathMu.RLock()
		filePath := filePathCache[id]
		filePathMu.RUnlock()
		if filePath != "" {
			result[id] = filePath
		}
	}

	missing := make([]string, 0, len(uniq))
	for id := range uniq {
		if result[id] == "" {
			missing = append(missing, id)
		}
	}
	const batchSize = 500
	for i := 0; i < len(missing); i += batchSize {
		end := i + batchSize
		if end > len(missing) {
			end = len(missing)
		}
		query, args, err := sqlx.In("SELECT id, file_path FROM astramap_nodes WHERE id IN (?)", missing[i:end])
		if err != nil {
			return nil, err
		}
		query = db.Rebind(query)
		var rows []struct {
			ID       string `db:"id"`
			FilePath string `db:"file_path"`
		}
		if err := db.Select(&rows, query, args...); err != nil {
			return nil, err
		}
		filePathMu.Lock()
		for _, row := range rows {
			if row.ID == "" || row.FilePath == "" {
				continue
			}
			result[row.ID] = row.FilePath
			filePathCache[row.ID] = row.FilePath
		}
		filePathMu.Unlock()
	}
	return result, nil
}

func cachedSourceLines(projectRoot, relPath string) []string {
	if projectRoot == "" || relPath == "" {
		return nil
	}
	stat, err := os.Stat(filepath.Join(projectRoot, relPath))
	if err != nil {
		return nil
	}
	key := sourceCacheKey{
		ProjectRoot: projectRoot,
		FilePath:    filepath.ToSlash(relPath),
		ModTimeUnix: stat.ModTime().UnixNano(),
		Size:        stat.Size(),
	}

	sourceLineMu.RLock()
	lines, ok := sourceLineCache[key]
	sourceLineMu.RUnlock()
	if ok {
		return lines
	}

	lines = readSourceLinesBestEffort(projectRoot, relPath)
	sourceLineMu.Lock()
	sourceLineCache[key] = lines
	sourceLineMu.Unlock()
	return lines
}

func projectRootFromCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(cwd, ".astramap", "astramap.db")); err == nil {
			return cwd
		}
		parent := filepath.Dir(cwd)
		if parent == cwd {
			return cwd
		}
		cwd = parent
	}
}

func activePreprocessorGuards(lines []string, lineNo int) []string {
	if lineNo <= 0 {
		return nil
	}
	stack := make([]string, 0, 4)
	limit := lineNo
	if limit > len(lines) {
		limit = len(lines)
	}
	for i := 0; i < limit; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "#if", "#ifdef", "#ifndef":
			stack = append(stack, trimmed)
		case "#endif":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case "#else", "#elif":
			// Keep the active guard frame; the enclosing conditional still applies.
		}
	}
	return stack
}
