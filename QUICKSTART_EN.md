# AstraMap 2-Minute Quickstart Guide

[中文](QUICKSTART.md) | **English**

> From zero to AI tools code-map integration in just 5 steps.

---

## Step 1: Install and Add to PATH

```bash
mkdir -p ~/bin
cp ./amap ~/bin
chmod 777 ~/bin/amap
grep -qxF 'export PATH="$HOME/bin:$PATH"' ~/.bashrc || echo 'export PATH="$HOME/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

Verify installation:

```bash
which amap
# Expected output: /home/you/bin/amap
```

## Step 2: Configure `~/.bashrc` for Seamless Cost-Saving Workflow

Append the snippet below to your `~/.bashrc`. Its goal is: under normal usage, refresh the code map once, launch a silent background watch process, and enter your AI coding tool (e.g., Claude Code). For troubleshooting, switch to dual-window mode to observe live incremental logs.

```bash
# AstraMap local binary
export PATH="$HOME/bin:$PATH"

# AI coding runtime defaults
export CLAUDE_CODE_EFFORT_LEVEL=high
export DISABLE_TELEMETRY=1

# Daily use: Silent mode. Refresh code map once, start background silent watch, then enter Claude Code
alias cc='amap index && (amap watch 30 >/dev/null 2>&1 &); claude'
```

For troubleshooting, use this dual-window command set:

```bash
# Troubleshooting: Dual window. Observe real-time incremental update logs directly in the foreground
alias cc='amap index && claude'
alias amapw='amap watch 30'
```

Apply immediately:

```bash
source ~/.bashrc
```

Recommended split-terminal workflow:

```bash
# Terminal 1: Continuously refresh code map incrementally at low frequency
amapw

# Terminal 2: Refresh index first, then launch AI coding agent
cc
```

Risk Warning:

- Avoid keeping multiple `amap watch 30` processes running simultaneously over long periods.
- If foreground and background watch processes run together, they will duplicate scans of the index directory, repeat terminal logs, and write extra data to SQLite.
- The background watch in `cc` runs silently in the background. If you need live log inspection during debugging, temporarily switch to the dual-window version and run `amapw`.

## Step 3: Navigate to Your Target Project and One-Click Install

```bash
cd /path/to/your/project

# Detect available IDEs/clients on current machine and register only detected tools
amap install
```

During execution, it will detect available clients such as `Claude Code`, `VS Code`, `Cursor`, `Codex`, `Windsurf`, and `Antigravity`. Undetected clients will be skipped automatically without generating redundant configuration.

Example output after successful registration:

```
  ✓ Claude Code  — MCP registered (user scope) + /amap slash commands ready
  ✓ VS Code      — MCP registered (code --add-mcp) + Copilot rules configured
  ✓ Cursor       — MCP registered + Rules saved (.cursor/rules/astramap.mdc)
  ✓ Project .mcp.json — Saved (automatically available for team members)
  ✓ Codex         — MCP registered + Rules saved to AGENTS.md
  ✓ Windsurf      — Rules saved to .windsurfrules
  ✓ Cline         — Rules saved to .clinerules/astramap.md
  ✓ Antigravity  — MCP registered (saved to .agents/mcp_config.json, ~/.gemini/config/mcp_config.json, ~/.gemini/antigravity-cli/mcp_config.json) + Rules appended to AGENTS.md

Installation complete! N/M tools registered successfully.
```

A subsequent `Registration Verification` step checks disk artifacts to verify that tool registrations are active.

## Step 4: Build Index

```bash
# Default mode: Initializes SCIP on first run; afterwards performs fast incremental updates reusing config.yaml language preferences
amap index

# Import specific language only (skips interactive selection)
amap index --lang go

# Run Tree-sitter syntax layer only (skips SCIP generation and import)
amap index --tree-sitter

# Import SCIP only (skips optional Syntax Overlay)
amap index --scip-only

# Force refresh SCIP layer
amap index --refresh-scip

# Full rebuild
amap index --full

# Index once on startup, then quietly refresh incrementally every 30 seconds in the background
amap index --watch 30

# Foreground incremental refresh (default 10s intervals; displays updated files)
amap watch
```

Language selections are stored in `.astramap/config.yaml` under `index.languages`. Subsequent `amap index` executions reuse this configuration quietly.

Index output example (Go Project):

```
Detected language files:
  1. Go (42 source files)

Importing language: Go
Detected Go project, generating SCIP index (/home/you/go/bin/scip-go)...
Importing SCIP index: /path/to/project/.astramap/index-go.scip
SCIP index import completed

── Index Source Statistics ──
  Nodes (by language): Go=356 (total=356)
  Edges (by source)  : scip=892, syntax-package=41, heuristic=23 (total=956)

Index build complete!
```

## Step 5: Launch Web Dashboard

```bash
amap dashboard
```

Output:

```
AstraMap Dashboard started in background
Host: 0.0.0.0
Port: 3000
Local: http://localhost:3000
LAN: http://192.168.1.100:3000
PID: 12345
Log: /path/to/project/.astramap/dashboard.log
```

Open `http://localhost:3000` in your browser to access the Explore View and Dependency View.

---

## How It Works

```
Source Code → Built-in Tree-sitter Real-time Layer + SCIP Final Semantics → SQLite Knowledge Graph → MCP/HTTP API → Local Dashboard & AI Clients
```

## Key Advantages

- **95%+ Semantic Accuracy** — SCIP compiler-grade indexing, distinguishing overloads and generics.
- **60-95% Token Savings** — Single structured query replaces multiple grep + Read steps.
- **Deterministic Continuous Sync** — `amap watch [seconds]` or `amap index --watch [seconds]` commits Tree-sitter results in real-time, then updates SCIP to converge cross-file semantics.

## Comparison

| Dimension | CodeGraph | GitNexus | Graphify | AstraMap |
|------|-----------|----------|----------|----------|
| Index Source | Tree-sitter | Tree-sitter | Tree-sitter + Static Graph | **Tree-sitter Real-time + SCIP Semantics** |
| Semantic Precision | Heuristic | Symbol level | Hybrid | **Compiler Grade** |
| Scalability | 1M lines | 10M lines | 1M lines | **100M+ lines** |
| Setup Complexity | Medium | Simple | Simple | **Zero Config** |
| Dashboard & Visualization | Plain Text | Static Graph | Read-only Chart | **Force-directed Graph + Trace** |

## MCP Tool Trigger Scenarios

| Tool | When to Use |
|------|-------------|
| `astramap_search` | "Where is X defined?" / "Find function Y" |
| `astramap_explore` | "How are X and Y related?" |
| `astramap_node` | "Show source code for X" |
| `astramap_callers` | "Who calls X?" |
| `astramap_callees` | "What does X depend on?" |
| `astramap_impact` | "What breaks if I change X?" |
| `astramap_trace` | "Show call chain from A to B" |
| `astramap_status` | "Is the index ready?" |

## Claude Code `/amap` Slash Commands

```
/amap search QuerySearch
/amap explore "MCP Server Initialization"
/amap callers go:astramap/service.go:QuerySearch
/amap impact go:astramap/service.go:QuerySearch
/amap trace main QuerySearch
/amap status
```

---

© 2026 AstraMap — High-Precision Code Map Engine  
Authors: AstraMap Team & Contributors | Version: v0.1
