# AstraMap - 高精度语义代码地图引擎

> 源码 → SCIP 编译器级索引 + Tree-sitter 按哈希增量补丁 → SQLite 知识图谱 → MCP/HTTP API → AI 工具客户端

面向 AI 编程代理的代码地图引擎。将源码解析为符号节点与调用边的知识图谱，通过 MCP 和 REST API 提供毫秒级结构化查询，单个静态二进制零配置部署。

## 核心优势

| 优势 | 说明 |
|------|------|
| **编译器级语义精度** | 以 SCIP 为主索引，跨文件调用关系由编译器预计算，非启发式猜测。重载消歧、条件编译、宏展开均确定性处理 |
| **60-95% Token 节约** | 单次 `astramap_explore` 替代 10+ 次 grep+Read，一次返回源码+调用关系+依赖文件 |
| **低频增量同步** | Tree-sitter 按文件哈希跳过未变更文件；Dashboard 内建 fsnotify 文件监控自动同步；`amap watch` 持续批量刷新 |
| **零配置部署** | 单静态二进制（musl 静态链接，无 GLIBC 依赖）内嵌 SQLite + Tree-sitter WASM + D3.js Dashboard，开箱即用 |
| **理解文档生成** | 一键生成函数/文件/模块/项目级理解文档，含 Mermaid 依赖图、复杂度风险表、架构边界违规检测 |

## 架构选择：SCIP 为主，Tree-sitter 为辅

当前火热的代码地图——CodeGraph、GitNexus、Graphify、Understand-Anything——几乎都走纯 tree-sitter 路线。AstraMap 反其道而行，原因是一个事实：

**Tree-sitter 是单文件解析器。** 它知道 `api.c` 里有 `foo()`，但不知道 `cli.c:312` 调用了它。纯 tree-sitter 要建跨文件调用图，必须自建名称解析→符号表→引用匹配，每一步都是坑：

- **名称解析**：C 的 `#include` / `#ifdef` / 宏展开——tree-sitter 不做预处理
- **符号消歧**：同名 `static` 函数在不同编译单元是不同实体
- **跨文件引用**：需模拟编译器的 include 路径和 `-D` 宏定义，本质是重写半个编译器

SCIP 由编译器/语言服务器生成，跨文件引用是预计算的确定性数据。实测：纯 tree-sitter 对 `firefly_error_code_mapping` 的跨文件调用边为 **0**，SCIP 主索引为 **2,077**。

两者分工明确：

```
SCIP（主索引）       → 定义/引用边、跨文件调用、符号消歧  — "谁调谁"
Tree-sitter（辅助层） → 类型/签名、嵌套归属、源码还原     — "这是什么"
```

砍 SCIP 退纯 tree-sitter → 跨文件边归零，`callers/callees/trace/impact` 废掉。砍 tree-sitter 只留 SCIP → 签名/源码没了，但图遍历还能跑。**图的边比节点的装饰属性更重要**，所以 SCIP 为主。

## 交互式代码图谱浏览

Dashboard 不是静态截图式代码地图，而是直接运行在同一份 SQLite 语义图谱之上的交互式工作台。浏览、定位、追踪、源码预览和理解文档生成共享同一套符号 ID 与调用边，避免在文件树、grep 结果和调用链之间来回切换。Dashboard 启动时自动监听源码变更（fsnotify），500ms 防抖触发增量同步，无需手动 `amap index`。

### 探索视界

探索视界面向“先看全局，再钻局部”的代码理解流程：项目、目录、文件、函数都能作为入口，图谱节点与源码预览联动，适合快速识别模块边界、热点文件、核心类型与关键函数。相比线性文件树，它直接暴露结构关系，AI 代理和工程师都能以更少上下文定位真正需要阅读的代码区域。

<img src="pic/探索视界.png">

### 依赖关系

依赖关系视图围绕一个函数展开完整调用邻域：父祖辈调用者、儿孙辈被调函数和兄弟级调用上下文同时可见，深度控制父祖辈与儿孙辈追溯代数。它保留真实调用边，不把下级函数折叠成模糊的”辅助函数列表”，因此能直接回答”谁触发了它””它继续影响谁””同一父调用下还有哪些并行动作”。

<img src=”pic/依赖关系.png”>

### 理解文档

理解文档视图一键生成函数/文件/模块/项目四个粒度的结构化理解文档。函数级文档包含角色推断、调用链摘要、复杂度风险指标（圈复杂度/有效 LOC/嵌套深度/扇入扇出/跨模块调用）和对称性风险检测；文件级文档聚合所有符号角色与 Mermaid 依赖图；模块级文档检测架构边界违规；项目级文档输出全局架构概览与热点模块排名。

<img src=”pic/理解文档.png”>

## 竞品对比

| | CodeGraph | GitNexus | Graphify | Understand-Anything | **AstraMap** |
|---|-----------|----------|----------|--------------------|----|
| **索引源** | Tree-sitter | Tree-sitter + 推导 | Tree-sitter + LLM | Tree-sitter + LLM | **SCIP + Tree-sitter** |
| **语义精度** | 启发式 | 符号级推导 | 混合语义 | LLM 语义 | **编译器级** |
| **C/C++** | 有限 | 基础 | 基础 | 基础 | **C + C++ 完整** |
| **条件编译** | 无 | 单路径 | 无 | LLM 盲猜 | **合并+投影+追溯** |
| **部署** | npm | 二进制/WASM | Python | npm | **单静态二进制+Web UI** |
| **MCP** | stdio | stdio | 弱 | 无 | **stdio + REST** |
| **理解文档** | 无 | 无 | 知识库 | Onboarding | **自动生成+风险检测** |
| **额外价值** | 纯地图 | 审计+链路 | 知识库 | Onboarding | **地图+治理+文档** |

与 Sourcegraph 在 SCIP 精度上同源，定位互补：Sourcegraph 是跨仓库企业级云端平台，AstraMap 是单机零依赖的代码地图+治理引擎。差异化在**条件编译本地即时投影**（无需 CI 矩阵即可按 `build_context` 实时裁剪）、**治理一体化**（架构合规/复杂度/死代码共享图谱）和**零基础设施部署**。

## 支持语言

| 语言 | Tree-sitter | SCIP 索引器 |
|------|------------|------------|
| Go | `tree-sitter-go` | `scip-go` |
| Python | `tree-sitter-python` | `scip-python` |
| TypeScript / TSX | `tree-sitter-typescript` | `scip-typescript` |
| C | `tree-sitter-c` | `scip-clang` |
| C++ | `tree-sitter-cpp` | `scip-clang` |
| Java | `tree-sitter-java` | `scip-java` |

## 快速开始

```bash
go build -o amap ./cmd/amap    # 构建
amap install                    # 一键注册 MCP 到 Claude Code / Cursor / VS Code / Codex / Antigravity / Windsurf / Cline
amap index                      # 快速增量更新；首次运行会初始化 SCIP
amap watch 10                   # 可选：持续监听源码变更，10 秒最多刷新一次
amap dashboard                  # 启动可视化控制台（自动监听源码变更）
```

首次运行 `amap index` 会自动生成 `.astramap/config.yaml` 示例。需要过滤辅助文件或目录时，编辑该文件后重新运行 `amap index`：

```yaml
index:
  languages:
    - go
  exclude:
    - "docs/**"
    - "vendor/**"
    - "**/*.pb.go"
  scipExclude:
    - "examples/**"
  treeSitterExclude:
    - "testdata/**"
  include:
    - "src/**"
```

`exclude` 同时作用于语言检测、SCIP 导入、Tree-sitter 解析和跨文件启发式分析；`scipExclude` 和 `treeSitterExclude` 只作用于对应阶段；`include` 为白名单模式，仅索引匹配路径。已索引但后被排除的文件会在下次 `amap index` 时自动清理。

`languages` 会记录上次索引选择。后续普通 `amap index` 会复用该选择并静默执行；显式传 `--lang` 会更新它。

## 命令一览

### 核心服务

| 命令 | 说明 |
|------|------|
| `amap serve [--project <path>]` | MCP stdio 服务 |
| `amap dashboard [--project <path>] [--host <addr>] [--port <port>]` | Web 可视化控制台（自动监听源码变更） |
| `amap index [选项]` | 快速增量更新一次；已有 SCIP 时默认不重建 SCIP |
| `amap index --refresh-scip` | 强制重新生成并导入 SCIP |
| `amap index --full` | 全量刷新 SCIP 层，再执行 Tree-sitter 增量扫描 |
| `amap index --watch [秒数]` | 启动时索引一次，然后持续低频监听刷新 |
| `amap index --treesitter-only` | 仅 Tree-sitter 快速扫描，跳过 SCIP |
| `amap watch [秒数]` | 持续监听源码变更，默认 10 秒最多刷新一次 |
| `amap install` | 一键注册 MCP 到 AI 工具（探测式注册，仅写入已安装的客户端） |

### 诊断工具

| 命令 | 说明 |
|------|------|
| `amap locate <symbol>` | 符号定位（文件+行号） |
| `amap diff [--suggest-tests]` | 变更影响分析 |
| `amap hotspots` | 代码热点 Top 10 |
| `amap deadcode` | 死代码检测 |
| `amap cycles` | 循环依赖检测 |
| `amap coupling [--path=<prefix>]` | Ca/Ce 耦合度分析 |
| `amap owners <symbol>` | 代码所有权（git blame） |
| `amap tree <symbol> [--dir=up\|down] [--depth=N]` | 调用拓扑树 |
| `amap export <symbol> [--format=mermaid]` | 拓扑导出 |
| `amap query "<SQL>"` | SQL 直接查询 |

## MCP 工具清单

AI 代理可调用以下 9 个工具：

| 工具 | 触发场景 |
|------|---------|
| `astramap_search` | "X 在哪定义" / "找一下 Y 函数" |
| `astramap_explore` | "X 和 Y 是怎么关联的" / 业务流程描述 |
| `astramap_node` | "X 的源码是什么" / 签名和文档 |
| `astramap_callers` | "谁调用了 X" |
| `astramap_callees` | "X 依赖什么" |
| `astramap_impact` | "改了 X 会影响什么" |
| `astramap_trace` | "从 A 到 B 的调用链" |
| `astramap_status` | "索引好了吗" |
| `astramap_files` | "项目有哪些文件" |

## REST API

Dashboard 同时暴露 REST JSON API：

| 端点 | 方法 | 参数 | 说明 |
|------|------|------|------|
| `/api/astramap/status` | GET | — | 索引状态统计 |
| `/api/astramap/search` | GET | `q`, `kind` | 符号搜索 |
| `/api/astramap/node/{id}` | GET | 路径 `id` | 节点详情 |
| `/api/astramap/callers/{id}` | GET | 路径 `id` | 上游调用者 |
| `/api/astramap/callees/{id}` | GET | 路径 `id` | 下游被调用者 |
| `/api/astramap/impact/{id}` | GET | `depth` | 影响分析 |
| `/api/astramap/explore` | GET | `q`, `maxFiles` | 区域探索 |
| `/api/astramap/trace` | GET | `from`, `to` | 路径追踪 |
| `/api/astramap/overview` | GET | — | 模块级聚合图 |
| `/api/astramap/functions` | GET | — | 函数列表 |
| `/api/astramap/data` | GET | — | 全图谱数据（节点+边+文件） |
| `/api/graph/module` | GET | `id` | 模块内依赖图 |
| `/api/documents/generate` | GET | `type`, `key` | 理解文档生成（type: function/file/module/project） |

## 架构概览

```
源码文件
    ↓ SCIP 索引（编译器级）+ Tree-sitter AST（按哈希增量补丁）
SQLite 知识图谱 (.astramap/astramap.db)
    ├── astramap_nodes    符号节点：函数、类、结构体等
    ├── astramap_edges    关系边：calls、contains、imports
    ├── astramap_files    文件跟踪：路径、哈希、语言
    └── astramap_fts      FTS5 全文搜索
    ↓
    ├── MCP stdio JSON-RPC → Claude Code / Cursor / VS Code / Codex / Antigravity
    └── HTTP REST API + D3.js Dashboard → 浏览器可视化
        └── fsnotify 文件监控 → 自动增量同步
```

边的来源标识：`scip`（编译器级精度）> `tree-sitter`（AST 解析）> `heuristic`（模式匹配）。同源冲突时 SCIP 边优先。

持续增量监听可用 `amap watch [秒数]` 或 `amap index --watch [秒数]`。Dashboard 启动后自动监听源码变更（fsnotify + 500ms 防抖），无需手动刷新。普通 `amap index` 是一次性快速增量更新；已有 SCIP 层时不会默认重建 SCIP，需要 `--refresh-scip` 或 `--full` 才会刷新高精度 SCIP 层。

## 项目结构

```
astramap/
├── cmd/amap/main.go          CLI 入口
├── astramap/
│   ├── schema.go             SQLite DDL
│   ├── astramap.go           SCIP 导入 + 增量同步
│   ├── treesitter.go         Tree-sitter 解析 + 跨文件调用启发
│   ├── service.go            共享查询服务层（MCP/REST 共用）
│   ├── filter.go             索引过滤配置（.astramap/config.yaml）
│   ├── graph.go              图遍历引擎（BFS/DFS/可达性/耦合）
│   ├── mcp.go                MCP JSON-RPC stdio 服务
│   ├── server.go             HTTP REST API + Dashboard + 文件监控自动同步 + 理解文档生成
│   └── web/                  D3.js 可视化（go:embed）
├── go.mod                    Go 1.25
├── build.sh                  Linux 静态构建（CGO + musl）
└── QUICKSTART.md             2 分钟部署指南
```

索引数据存放在 `.astramap/`（建议加入 `.gitignore`），可随时 `amap index` 重建。

## 性能参考

| 项目规模 | 索引数据库 | 索引时间 |
|----------|-----------|---------|
| 1 万行 | 2-4 MB | < 5 秒 |
| 10 万行 | 12-20 MB | 10-30 秒 |
| 50 万行 | 50-100 MB | 1-3 分钟 |

## Claude Code `/amap` 斜杠命令

```
/amap search QuerySearch
/amap explore "MCP Server 启动流程"
/amap callers go:astramap/service.go:QuerySearch
/amap impact go:astramap/service.go:QuerySearch
/amap trace main QuerySearch
/amap status
```

## Changelog

### v0.2

| 变更 | 说明 |
|------|------|
| **索引过滤系统** | `.astramap/config.yaml` 支持 include/exclude/scipExclude/treeSitterExclude 四级过滤，已排除文件自动清理 |
| **文件监控自动同步** | Dashboard 内建 fsnotify 监听，500ms 防抖触发增量同步 |
| **模块级聚合图** | `/api/astramap/overview` 项目级聚合 + `/api/graph/module` 模块内钻取 |
| **理解文档生成** | `/api/documents/generate` 一键生成函数/文件/模块/项目级理解文档，含 Mermaid 依赖图、复杂度风险表、架构边界违规检测 |
| **C 语言独立支持** | `tree-sitter-c` 独立绑定，`.h` 文件智能 C/C++ 判定 |
| **MCP 符号消歧** | callers/callees/impact/trace 支持模糊符号解析，重载/多定义自动合并 |
| **Trace 邻域重构** | 兄弟感知邻域模型，修复跨文件自调用过滤 |
| **SCIP 边丢失修复** | 全局符号映射修复跨文档引用丢失 |
| **静态链接构建** | musl 静态编译，无 GLIBC 依赖 |
| **Dashboard 增强** | `--host` 参数、自动端口探测、LAN IP 显示、后台启动、请求日志 |
| **install 增强** | 探测式注册（仅写入已安装客户端），支持 Antigravity/Windsurf/Cline，注册核验 |
| **CLI 精简** | `--project` 全局参数，移除未实现命令 |

### v0.1

| 特性 | 说明 |
|------|------|
| **SCIP + Tree-sitter 双引擎索引** | SCIP 编译器级索引为主（跨文件调用、符号消歧），Tree-sitter AST 为辅（签名、嵌套归属、源码还原） |
| **SQLite 知识图谱** | nodes/edges/files/FTS5 四类核心结构，WAL 模式单写者，内容哈希去重 |
| **MCP stdio 服务** | 9 个工具（search/explore/node/callers/callees/impact/trace/status/files），JSON-RPC stdio 协议 |
| **REST API** | 11 个端点，覆盖搜索、节点详情、调用链追踪、影响分析、区域探索 |
| **D3.js 交互式 Dashboard** | 探索视界（全局→局部图谱浏览）、依赖关系视图（调用邻域展开） |
| **5 语言支持** | Go / Python / TypeScript+TSX / C++ / Java，对应 SCIP 索引器 + Tree-sitter 语法 |
| **增量索引** | Tree-sitter 按文件哈希跳过未变更文件；`amap index` 快速增量；`amap watch` 持续监听 |
| **CLI 诊断工具集** | locate / diff / hotspots / deadcode / cycles / coupling / owners / tree / query |
| **一键 MCP 注册** | `amap install` 注册到 Claude Code / VS Code / Cursor / Codex |
| **单二进制部署** | 内嵌 SQLite + Tree-sitter WASM + D3.js Dashboard，零依赖开箱即用 |

## 许可

© 2025-2026 何志川 — AstraMap v0.2
