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

	fileContentCache = make(map[string][]string)
	fileContentMu    sync.RWMutex
)

func InvalidateQueryHelperCache() {
	filePathMu.Lock()
	filePathCache = make(map[string]string)
	filePathMu.Unlock()

	fileContentMu.Lock()
	fileContentCache = make(map[string][]string)
	fileContentMu.Unlock()
}

var allowedSearchKinds = map[string]struct{}{
	"function":  {},
	"method":    {},
	"class":     {},
	"struct":    {},
	"interface": {},
	"enum":      {},
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
	if depth <= 0 {
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
	if edge == nil || edge.Line <= 0 || edge.Source == "" {
		return
	}
	if strings.TrimSpace(edge.Metadata) != "" {
		return
	}

	filePathMu.RLock()
	filePath, ok := filePathCache[edge.Source]
	filePathMu.RUnlock()

	if !ok {
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

	fileContentMu.RLock()
	lines, ok := fileContentCache[filePath]
	fileContentMu.RUnlock()

	if !ok {
		lines = readSourceLinesBestEffort(projectRootFromCwd(), filePath)
		fileContentMu.Lock()
		fileContentCache[filePath] = lines
		fileContentMu.Unlock()
	}

	if len(lines) == 0 {
		return
	}

	guards := activePreprocessorGuards(lines, edge.Line)
	if len(guards) == 0 {
		return
	}
	edge.Metadata = strings.Join(guards, " && ")
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
