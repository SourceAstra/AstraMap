# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

AstraMap is a high-precision semantic code map engine for AI programming agents. It parses source code via SCIP indexes and Tree-sitter into a SQLite-backed knowledge graph (nodes = symbols, edges = calls/contains/imports), then exposes the graph through a stdio MCP server and an HTTP REST API with an embedded web dashboard.

Go module name: `astramap-standalone` (declared in `go.mod`).

## Build & Run

```bash
# Build the CLI binary (output: ./amap)
go build -o amap ./cmd/amap

# Index a project (Tree-sitter mode)
./amap index --project /path/to/project

# Tree-sitter only, skip SCIP
./amap index --project /path/to/project --treesitter-only

# Index from an existing SCIP index
./amap index --project /path/to/project --scip /path/to/index.scip

# Start MCP stdio server (for Claude Code / Cursor integration)
./amap serve --project /path/to/project

# Start web dashboard (default port 8585)
./amap dashboard --project /path/to/project --port 8585

# One-click MCP install to Claude Code / VS Code / Cursor / project .mcp.json
./amap install

# Run all tests
go test ./...

# Run tests for a single package
go test ./astramap/...

# Vet
go vet ./...
```

Index data lives at `<project_root>/.astramap/astramap.db`. Add `.astramap/` to `.gitignore`.

## Architecture

### Package Structure

- `cmd/amap/` — CLI entry point (`main.go`). Dispatches subcommands. Each subcommand is a `*Cmd()` function that opens the SQLite DB, calls into the `astramap` package, and prints results.
- `astramap/` — Core library package containing all business logic:
  - `schema.go` — SQLite DDL (tables: `astramap_nodes`, `astramap_edges`, `astramap_files`, `astramap_verdicts`; FTS5 virtual table; triggers for FTS sync). Schema init via `InitAstraMapSchema()`.
  - `astramap.go` — Indexing pipeline: SCIP import (`ImportScipIndexToAstraMap`), incremental file sync (`SyncFileAstraMap`, `SyncAllFilesAstraMap`), and heuristic resolvers (`ResolveGoInterfaces`, `ResolveWebRoutes`).
  - `treesitter.go` — Tree-sitter incremental parsing (`ParseFileIncremental`). Extracts symbol definitions, `contains` hierarchy edges, intra-file `calls` edges, and `imports` edges. Also contains `ResolveCrossFileCalls` for cross-file call heuristic resolution.
  - `service.go` — Shared query service layer. All data access for both MCP and REST goes through here: `QuerySearch`, `QueryExplore`, `QueryNodeBySymbol`, `QueryStatus`, `QueryFiles`, `QueryVerdicts`, `QueryTraceCTE`.
  - `graph.go` — Graph traversal engine: `GetCallers`, `GetCallees`, `AnalyzeImpact` (BFS upstream), `TracePath` (BFS pathfinding), `FindDeadCode` (reachability), `FindCycles` (DFS cycle detection), `GetCouplingMetrics`, `GetCodeOwners`.
  - `mcp.go` — MCP JSON-RPC stdio server (`RunMcpServer`). Handles `initialize`, `tools/list`, `tools/call` for 10 MCP tools (astramap_search, astramap_explore, astramap_node, astramap_callers, astramap_callees, astramap_impact, astramap_status, astramap_verdict, astramap_trace, astramap_files).
  - `server.go` — Standalone HTTP server (`StartStandaloneServer`) serving REST JSON APIs (`/api/astramap/*`, `/api/trace`) and the embedded web dashboard from `web/`.
  - `web/` — Embedded static assets for the D3.js visualization dashboard (index.html, explore.js, trace.js, CSS).
- `astramap/astramap_design.md` — Full design document. Describes planned features (conditional compilation, `astramap_context`, `astramap_conditional_expand`, multi-config diff merge) not yet implemented.

### Data Flow

1. **Index**: Source files → Tree-sitter AST parse → extract nodes/edges → write SQLite (with content-hash dedup)
2. **Query**: MCP client or HTTP client → service.go functions → SQLite queries → return structured results
3. **Dashboard**: Browser → HTTP API → same service.go layer → D3.js force-directed graph rendering

### Key Design Decisions

- SQLite with WAL mode, single-writer (`SetMaxOpenConns(1)`), FTS5 for full-text search
- Dual indexing: SCIP (rich, language-server-grade) for supported languages + Tree-sitter (lightweight, universal) as fallback
- Three provenance levels on edges: `scip` (from SCIP index), `tree-sitter` (from AST parse), `heuristic` (from regex/pattern matching for cross-file calls, interface implementation, web routes)
- Web UI is embedded via `//go:embed web/*` — single binary deployment
- All logs go to stderr to keep stdout clean for MCP stdio protocol
- No foreign keys on `astramap_edges` — edges reference synthetic IDs (`file:*`, `import:*`, `route:*`) not present in `astramap_nodes`

### Node ID Scheme

- SCIP-sourced: raw SCIP symbol string (truncated to 200 chars if longer, fallback to `scip:<path>::<name>`)
- Tree-sitter-sourced: `<lang_prefix>:<rel_path>::<qualified_name>` (e.g., `go:astramap/service.go:QuerySearch`)
- Synthetic: `file:<path>`, `route:<path>`, `import:<path>`, `external:<symbol>`

### Supported Languages

Go, Python, TypeScript/TSX/JSX, C/C++, Java

### CLI Diagnostic Commands

| Command | Description |
|---------|-------------|
| `amap locate <symbol>` | Symbol definition location |
| `amap diff [--suggest-tests]` | Git diff impact analysis |
| `amap hotspots` | Top 10 files by change frequency |
| `amap deadcode` | Unreachable code detection |
| `amap cycles` | Circular dependency detection |
| `amap coupling [--path=...]` | Ca/Ce coupling metrics |
| `amap owners <symbol>` | Git blame-based code ownership |
| `amap rename <sym> <new> [--preview]` | Cross-file semantic rename (preview only) |
| `amap tree <sym> [--dir=up\|down] [--depth=N]` | Call topology tree |
| `amap export <sym> [--format=mermaid]` | Export call graph |
| `amap audit` | Quality gate — exits 1 on active defects |
| `amap qa` | Project quality dashboard |
| `amap query "<SQL>"` | Direct SQLite query |
| `amap clones` | Clone detection (not yet implemented) |

### Planned / Not Yet Implemented

The design doc (`astramap_design.md`) describes these features that are not yet in the codebase:
- Conditional compilation support (`astramap_conditionals` table, `macro_conditions`/`config_hash` columns, `build_context` parameter)
- `astramap_context` MCP tool (auto-build context from natural language)
- `astramap_conditional_expand` MCP tool (macro/build-tag/cfg tracing)
- Multi-config diff merge algorithm
- `amap repl`, `amap lsp` (stub only)
- `amap review`, `amap repair`, `amap test-gen` (require SourceAstra integration)
