# astra-code-map — High-Precision Semantic Code Map for AI Coding Agents

<p align="center">
  <img src="pic/banner.png" alt="astra-code-map Hero Banner" width="100%">
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License"></a>
  <a href="https://golang.org/"><img src="https://img.shields.io/badge/Go-1.25+-00ADD8.svg" alt="Go Version"></a>
  <a href="THIRD_PARTY_NOTICES.md"><img src="https://img.shields.io/badge/Compliance-Third--Party_Notices-green.svg" alt="Compliance"></a>
</p>

[中文](README.md) | **English**

> Semantic Code Map · Code Graph · Code Intelligence · MCP Server

astra-code-map is a local-first, blazing-fast **high-precision semantic code map and code graph engine**, engineered specifically for next-generation AI coding agents (such as Claude Code, Codex, and Cursor) as well as modern development teams. It deep-dives into complex codebases to construct a highly deterministic SQLite-backed symbol topology, eliminating context waste caused by AI agents repeatedly running `grep` or reading huge raw files.

Beyond equipping AI agents with a precise cognitive map of your code, the system features a stunning, interactive **Web Dashboard** that visualizes abstract architectures into three intuitive navigation perspectives:
* 🪐 **Explore View**: A dynamic starfield-like file explorer allowing developers to instantly gauge directory depths and node density.
* 🕸️ **Dependency Graphs**: Interactive, force-directed graph networks mapping global or local callers/callees relationships and exact code flows.
* 📖 **Understanding Documents**: Automatically generated, code-paired semantic summaries for modules and source files, providing unmatched clarity for architectural reviews and handovers.

---

### Core Technology Advantage: SCIP-first Semantics, Tree-sitter-second Syntax

In large-scale or enterprise-grade codebases, relying solely on the syntax layer (like Tree-sitter AST-based parsing) leads to significant relationship inaccuracies. Lacking static type system inference, pure syntax parsers fail to resolve polymorphism, interface implementations, overloaded methods, and complex cross-module dependencies, resulting in a distorted call graph.

To address this fundamental pain point, AstraMap implements a **two-layer hybrid architecture** with compiler-level SCIP semantics at its core, complemented by incremental Tree-sitter syntax updates:

```mermaid
graph LR
    A[Source Code] --> B[Tree-sitter Real-time Layer]
    A --> C[SCIP Semantic Providers]
    B --> D[astra-code-map Merge Engine]
    C --> D
    D --> E[(SQLite Semantic Code Graph)]
    E --> F[MCP Server]
    E --> G[REST API]
    G --> H[Web Dashboard]
    F --> I[AI Coding Agents]
```

* **SCIP (High-Precision Semantic Core - Determines the Ceiling)**: Serves as the primary **semantic backbone**. It leverages compiler pipelines and language toolchains (via LSP/LSIF/SCIP Providers) to run complete type inferences, generating a highly precise graph with **cross-file definition resolution**, **exact polymorphic dispatches**, and **trait/interface mapping**. This represents our core competitive advantage.
* **Tree-sitter (Real-time Syntax Patch - Determines the Responsiveness)**: Serves as the **incremental patch**. Operating on top of the robust SCIP graph, Tree-sitter parses modified files in milliseconds, adjusting local offsets and additions without requiring expensive compiler runs.

---

### What Problems Does SCIP Solve That Pure Tree-sitter Cannot?

When navigating large or multi-module industrial codebases, relying **solely on Tree-sitter (regex/symbol text match on AST)** introduces massive bottlenecks:

#### 1. Accurate Trait & Interface Implementation Mapping
* **Tree-sitter Pain Point**: It can only associate by name. If multiple structs/classes implement common methods like `Read` or `Close`, pure Tree-sitter creates chaotic, ambiguous edges, polluting impact analysis maps with false positives.
* **SCIP Solution**: Utilizing static compiler analysis, AstraMap maps abstract declarations directly to their correct runtime concrete implementations, ensuring every graph edge is compiler-verified.

#### 2. Scope & Signature Disambiguation for Overloaded Symbols
* **Tree-sitter Pain Point**: When different files define identical identifiers (e.g., `pkgA.Init()` and `pkgB.Init()`), syntax-only maps fail to isolate call paths, collapsing separate namespaces into one messy namespace.
* **SCIP Solution**: Generates a globally unique Unified Symbol Name (U.S.N.) for each entity. Even with identical spellings, symbols residing in distinct scopes are treated as entirely separate entities.

#### 3. Deep External & Third-Party Library Tracking
* **Tree-sitter Pain Point**: Syntax parsers cannot trace call chains into closed-source SDKs or dependency libraries outside the workspace files.
* **SCIP Solution**: Automatically imports external package metadata, bridging dependencies to draw complete boundaries.

#### 4. High-Fidelity Impact Analysis & Deadcode Elimination
* **Tree-sitter Pain Point**: Ambiguous edges cause dependencies to diffuse rapidly, making change calculations expand to the entire repository and rendering test recommendation engines useless.
* **SCIP Solution**: Operates on a deterministic call graph to support deep, reliable topological traversals (e.g., "modifying symbol X affects precisely files A, B, and C").

---

| Feature | SCIP (Semantic Backbone) | Tree-sitter (Syntax Patch) |
|---|---|---|
| **Role & Position** | **High-precision cross-file semantic core** | **Real-time incremental patch** |
| **Core Value** | Eliminates polymorphism & namespace ambiguity; provides reliable call graphs and impact tracking. | Guarantees instant agent responsiveness and maintains line alignment. |
| **Parsing Pipeline** | Collaborates with compiler/build setups for static type inference. | Standalone fast AST parsing; no build requirements or environment constraints. |
| **Update Interval** | On-demand or scheduled runs (`amap index`); full index during big changes. | Active file watcher (`amap watch`), triggers on save and commits in milliseconds. |

#### Fusion and Resolution Logic

In AstraMap's Merge Engine:
1. **SCIP establishes the source of truth**: Post-compile, SCIP logs cross-file call boundaries and inheritance trees into SQLite under `scip` provenance.
2. **Tree-sitter prevents drift**: As the developer codes, Tree-sitter patches offsets, line modifications, and new local definitions in the database under `syntax-package` provenance.
3. **Graceful Fallback**: If compiler toolchains are absent, the graph degrades cleanly to heuristic symbol matching.

---

## What astra-code-map Helps Answer

| Question | astra-code-map capability |
|---|---|
| Where is a function, type, or method defined? | Semantic symbol search and precise location |
| Who calls this function, and what does it call? | Callers and callees queries |
| How are two modules connected? | Code exploration and call-path tracing |
| What could be affected by changing a symbol? | Recursive impact analysis and Git diff analysis |
| What should be excluded from AI context in a large repository? | Ecosystem-aware filtering and generated-file exclusion |
| How can an agent understand a codebase with less context? | Structured MCP queries and on-demand source snippets |

astra-code-map does not replace source code or language toolchains. It provides AI agents, IDEs, and engineering platforms with a **queryable, traceable, continuously updated code-navigation foundation**.

## Highlights

- **Semantic code map** for functions, methods, types, files, modules, calls, references, imports, inheritance, and implementations.
- **Two-layer indexing** with Tree-sitter for real-time structure and SCIP Providers for cross-file semantics and symbol disambiguation.
- **Incremental synchronization** based on file state and content hashes.
- **MCP-native integration** for Claude Code, Codex, Cursor, VS Code, and other compatible clients.
- **Call and impact analysis** including callers, callees, path tracing, recursive impact, dependency cycles, and coupling.
- **Local visualization** through a Web Dashboard for structure, call neighborhoods, source snippets, and generated understanding documents.
- **Ecosystem-aware filtering** for dependencies, build outputs, caches, binaries, and generated source.
- **C/C++ conditional-compilation awareness** for `#if`, `#ifdef`, and `#ifndef` context.

## Screenshots

### Explore View

Start from a project, directory, file, or symbol and move from global structure to local implementation.

<img src="pic/explore.png" alt="astra-code-map Explore View">

### Dependency View

Inspect callers, callees, and related call paths around a target function.

<img src="pic/trace.png" alt="astra-code-map Dependency Graph">

### Understanding Documents

Generate structured function-, file-, module-, and project-level documents for code reading, review, refactoring, and handover.

<img src="pic/understand.png" alt="astra-code-map Understanding Documents">

## Quick Start

See [QUICKSTART_EN.md](QUICKSTART_EN.md) for platform-specific installation, SCIP Provider setup, and troubleshooting.

### 1. Build and install

Run from the astra-code-map repository root:

```bash
./build.sh

mkdir -p "$HOME/.local/bin"
install -m 755 ./amap "$HOME/.local/bin/amap"
export PATH="$HOME/.local/bin:$PATH"
```

Verify the installation:

```bash
amap --help
```

> Use the Go version required by `go.mod`. On Windows, build `amap.exe` and add its directory to the user PATH.

### 2. Enter the project to analyze

```bash
cd /path/to/your/project
```

### 3. Register MCP

```bash
amap install
```

The command detects installed clients and writes astra-code-map MCP configuration only for clients that are actually present.

### 4. Build the first code map

```bash
amap index
```

The first run creates:

```text
.astra-code-map/
├── config.yaml
└── astra-code-map.db
```

### 5. Launch the Dashboard

```bash
amap dashboard
```

Open:

```text
http://localhost:3000
```

### 6. Keep the map synchronized (optional)

Run in a dedicated terminal:

```bash
amap watch 30
```

Avoid running multiple watchers for the same project.

## AI Agent Examples

After MCP registration, ask your AI coding tool questions such as:

```text
Where is handleRequest defined?
Who calls handleRequest?
What functions does handleRequest depend on?
What modules could be affected by changing auth.ValidateToken?
What is the call path from an HTTP route to a database write?
List the indexed files under src/network.
```

MCP tools:

| Tool | Purpose |
|---|---|
| `astra-code-map_search` | Search functions, methods, types, and other symbols |
| `astra-code-map_explore` | Explore files and relationships around a concept or symbol |
| `astra-code-map_node` | Read symbol definition, signature, location, and source snippet |
| `astra-code-map_callers` | Query direct callers |
| `astra-code-map_callees` | Query direct callees |
| `astra-code-map_impact` | Analyze recursive change impact |
| `astra-code-map_trace` | Find a call path between two symbols |
| `astra-code-map_status` | Inspect index coverage and provenance |
| `astra-code-map_files` | Query indexed files by path or pattern |



## Supported Languages

Core includes built-in Tree-sitter parsing for the following languages. Install the corresponding SCIP Provider for richer cross-file semantics.

| Language | Common extensions | Semantic Provider | Real-time parsing |
|---|---|---|---|
| Go | `.go` | `scip-go` | Tree-sitter |
| TypeScript | `.ts` `.tsx` | `scip-typescript` | Tree-sitter |
| JavaScript | `.js` `.jsx` `.mjs` `.cjs` | `scip-typescript` | Tree-sitter |
| Python | `.py` | `scip-python` | Tree-sitter |
| Java | `.java` | `scip-java` | Tree-sitter |
| Kotlin | `.kt` `.kts` | `scip-java` | Tree-sitter |
| Scala | `.scala` `.sc` | `scip-java` | Tree-sitter |
| C | `.c` `.h` | `scip-clang` | Tree-sitter |
| C++ | `.cc` `.cpp` `.cxx` `.hpp` `.hxx` | `scip-clang` | Tree-sitter |
| Rust | `.rs` | `scip-rust` | Tree-sitter |
| C# | `.cs` | `scip-dotnet` | Tree-sitter |
| Ruby | `.rb` `.rake` | `scip-ruby` | Tree-sitter |

SCIP availability depends on the project, language toolchain, and build inputs. For example, high-precision C/C++ indexing commonly requires a valid `compile_commands.json`.

## Common CLI Commands

### Indexing and services

| Command | Description |
|---|---|
| `amap install` | Register MCP with local AI coding tools |
| `amap index` | Build or incrementally update the code map |
| `amap index --tree-sitter` | Use only the Tree-sitter real-time layer |
| `amap index --refresh-scip` | Force a SCIP semantic refresh |
| `amap index --full` | Perform a full refresh |
| `amap watch [seconds]` | Watch code changes and synchronize continuously |
| `amap serve` | Start the MCP stdio server |
| `amap dashboard` | Start the Web Dashboard |

### Navigation and analysis

| Command | Description |
|---|---|
| `amap locate <symbol>` | Locate a symbol definition |
| `amap tree <symbol>` | Print a call-topology tree |
| `amap diff [--suggest-tests]` | Analyze Git change impact and suggest test scope |
| `amap hotspots` | Find code hotspots |
| `amap deadcode` | Detect unreachable functions and methods |
| `amap cycles` | Detect dependency cycles |
| `amap coupling [--path=...]` | Analyze module coupling |
| `amap owners <symbol>` | Query code ownership using Git blame |
| `amap query "<SQL>"` | Query the local SQLite code graph directly |

## Ecosystem-Aware Filtering

astra-code-map follows a simple default principle:

> Prefer hand-written source code that carries business meaning.

It automatically excludes common categories such as:

- Version-control metadata
- Third-party dependency directories
- Build outputs and caches
- Generated source and compressed assets
- Binary files and unsupported resources

Use `include`, `exclude`, and `force-include` in `.astra-code-map/config.yaml` to adjust the result.

```yaml
index:
  languages:
    - go
  exclude:
    - "docs/**"
    - "vendor/**"
  include:
    - "src/**"
```

## Local Data and Privacy

astra-code-map reads and indexes code locally and stores project data under `.astra-code-map/`.

- astra-code-map does not require uploading the complete repository to a separate remote indexing service.
- The MCP Server exposes structured queries through local stdio.
- The Dashboard and REST API operate on local project data.
- Whether an AI client sends returned context to a remote model depends on that client and model configuration, not astra-code-map.

Do not commit `.astra-code-map/astra-code-map.db`. Add the following to the target project's `.gitignore`:

```gitignore
.astra-code-map/
```

## Open-Source Components and Licensing

astra-code-map builds on open-source projects including SCIP, Tree-sitter, SQLite-related components, `sqlx`, `fsnotify`, D3.js, and Marked.

- astra-code-map-owned source code is licensed under the [Apache License 2.0](LICENSE), unless a file states otherwise.
- Third-party components remain under their respective copyrights and licenses.
- See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for component, version, and license information.
- Release archives should include license texts under `LICENSES/` and a version-specific SBOM.
- External SCIP Providers are normally used as separate tools and are not bundled with astra-code-map unless a release explicitly states otherwise.

The component names in this README are an architectural overview. `THIRD_PARTY_NOTICES.md`, `LICENSES/`, and the release SBOM are the authoritative distribution records.

## Project Status

astra-code-map is under active development. Public APIs, configuration formats, and index storage may change before a stable release.

Current uses include:

- Evaluating semantic code-map capabilities on local projects
- Connecting AI coding agents for navigation and impact analysis
- Reporting false positives, missing symbols, and cross-file relationship issues
- Contributing test cases, documentation, platform support, and language fixes

## Contributing

Before opening an issue, please include:

- Operating system and astra-code-map version
- Project language and build tool
- SCIP Provider and version, when applicable
- A minimal reproducible code sample
- Actual and expected behavior

Before submitting code, open an Issue describing the problem and the proposed approach. Do not disclose sensitive security details in a public Issue.

## Changelog

See complete [CHANGELOG.md](CHANGELOG.md).

### [v0.2.0] - 2026-07-30
- 🚀 **Architecture Alignment**: Standardized on "SCIP-first Semantics, Tree-sitter-second Syntax" dual-layer model.
- ⚡ **Watch Debouncing**: Prevented rapid SCIP recompiles during file saves; added 2-minute quiet debounce convergence timer.
- 🎯 **Heuristic Precision**: Added same-package directory filtering, Go `init()` interception, and member selector ambiguity defense.

## Related Documentation

- [Quick Start](QUICKSTART_EN.md)
- [Changelog](CHANGELOG.md)
- [Third-Party Notices](THIRD_PARTY_NOTICES.md)

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.
