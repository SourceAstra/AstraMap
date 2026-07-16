# AstraMap - 高精度语义代码地图引擎

> 源码 → SCIP 编译器级索引 + Tree-sitter 按哈希增量补丁 → SQLite 知识图谱 → MCP/HTTP API → AI 工具客户端

面向 AI 编程代理的代码地图引擎。将源码解析为符号节点与调用边的知识图谱，通过 MCP 和 REST API 提供毫秒级结构化查询，单个静态二进制零配置部署。

## v0.3 核心能力与极致性能优化

### 1. 核心架构特性

| 能力 | 一句话说明 |
|------|--------|
| **🛡️ 生态感知智能过滤** | 自动识别 Go/Node/Rust/Maven/Gradle/CMake/Python/Swift/Bazel 工程根，按生态生成构建产物排除规则；内置 40+ 条通用排除规则（VCS/依赖/缓存/二进制/压缩/生成源码），零配置开箱即用 |
| **📋 结构化过滤报告** | `amap index` 输出按规则类型分组的排除报告，含规则 ID、排除原因、覆盖数量，告别黑盒过滤 |
| **🔁 `amap watch` 持续文件监听** | 独立守护进程增量轮询刷新，源码变更秒级体现，无需手动重新索引 |
| **⚡ 按哈希增量索引更新** | 跳过未变动文件，仅对变更部分进行 AST 与 SCIP 增量导入 |
| **🖥️ 交互式 Dashboard** | D3.js 力导向图与关系拓扑，集成全局探索、追溯依赖和一键架构理解文档 |
| **🔧 条件编译感知** | C/C++ `#if`/`#ifdef` 预处理守卫自动标注在调用边 metadata，图遍历直达条件分支 |
| **🎯 多定义合并消歧** | callers/callees/impact/trace 统一做 `ResolveSymbolToIDs` 多符号解析与结果合并去重，消除单符号选择导致的信息丢失 |
| **📊 callers 结果分页** | MCP `astramap_callers` 和 REST API 支持 `limit` 参数，超限截断提示，避免大项目结果膨胀 |
| **🔗 C/C++ 函数指针与宏调用解析** | 跨文件启发式识别 `.field = &func` 指定初始化、`struct var = { func1, func2 }` 顺序初始化、`XXXRETURN(func)` 宏展开调用、`DECLARE_xxx(func)` 宏隐式函数定义模式 |
| **🧹 脏文件低开销检测** | status 接口返回 `dirtyCount`，智能判断同步状态，免去频繁全量 hash 开销 |
| **📄 分页与歧义过滤** | `astramap_files` 分页限制；启发式消歧自动过滤 Python dunder 并选择最窄 span 节点；中文查询单字分词与停用词过滤 |

### 2. 性能重构与指标优化

在 v0.2 版本中，我们对前端 JS/CSS 与后端 Go/SQLite 的交互和计算逻辑进行了重构，主要指标如下：
- **传输带宽优化**：引入动态 Gzip 压缩，静态资源首屏传输体积由 608KB 降至 130KB。
- **静态资源强缓存**：为 JS/CSS 响应设置 `immutable` 缓存头，二次加载直接读取本地缓存。
- **按需加载模块**：`trace.js`（97KB）改为动态懒加载，仅在用户进入依赖分析视图时获取，缩短首屏可交互时间（TTI）。
- **消除 N+1 数据库查询**：设计并使用 `BatchCanonicalSymbolIDs` 批量查询，合并 callers、callees 接口中对边节点 ID 的循环单条 SQL 检索。`QueryTraceCTE` 条件编译 metadata 标注使用 `BatchNodeFilePaths` 批量获取文件路径，消除逐边单条查询。
- **文件读取缓存**：在预处理守卫条件匹配中，对节点路径和文件内容使用包级读写锁 Map 缓存，减少 callers/callees 查询时的重复磁盘 I/O。缓存键从路径改为 `(path, modTime, size)` 三元组，文件修改后自动失效；单文件同步/删除时精准清除该文件缓存，不再全量清空。
- **动画与查找算法优化**：将 Canvas 帧循环以及布局算法中的 $O(N)$ 线性查找 `.find()` 全部替换为 $O(1)$ 的 Map 映射检索，消除大图下的 CPU 计算瓶颈。
- **交互节流与防抖**：对 resizer 拖拽鼠标事件使用 `requestAnimationFrame` 节流，对 resize 和搜索框 input 增加 debounce 机制；缓存 Canvas 绘制所需的主题颜色属性以减少 `getComputedStyle` 样式重算开销；降低卡片容器模糊半径并移除 overlay 的 blur 属性以减少 GPU 合成开销。
- **Watcher Timer 泄漏防护**：将 watch 机制中 select 循环的 `time.After` 替换为可重置的 `time.Timer`，提取 `resetTimer` 工具函数统一重置逻辑，规避高频文件变更下的 Goroutine 积压与 `Stop/Reset` 竞态。

## 核心优势

| 优势 | 说明 |
|------|------|
| **编译器级语义精度** | 以 SCIP 为主索引，跨文件调用关系由编译器预计算，非启发式猜测。重载消歧、条件编译、宏展开均确定性处理 |
| **条件编译感知** | C/C++ `#if`/`#ifdef`/`#ifndef` 守卫自动标注到边 metadata，图遍历结果直接暴露条件编译上下文，无需手动追溯 |
| **自动持续同步** | `amap watch` 文件监听守护 + Tree-sitter 按哈希增量跳过，源码变更自动反映到图谱，无需手动干预 |
| **60-95% Token 节约** | 单次 `astramap_explore` 替代 10+ 次 grep+Read，一次返回源码+调用关系+依赖文件 |
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

Dashboard 不是静态截图式代码地图，而是直接运行在同一份 SQLite 语义图谱之上的交互式工作台。浏览、定位、追踪、源码预览和理解文档生成共享同一套符号 ID 与调用边，避免在文件树、grep 结果和调用链之间来回切换。

**持续同步**：`amap watch` 作为独立守护进程监听文件变更，检测到变化自动触发增量刷新，保持图谱始终与磁盘一致。

### 探索视界

探索视界面向"先看全局，再钻局部"的代码理解流程：项目、目录、文件、函数都能作为入口，图谱节点与源码预览联动，适合快速识别模块边界、热点文件、核心类型与关键函数。相比线性文件树，它直接暴露结构关系，AI 代理和工程师都能以更少上下文定位真正需要阅读的代码区域。

<img src="pic/探索视界.png">

### 依赖关系

依赖关系视图围绕一个函数展开完整调用邻域：父祖辈调用者、儿孙辈被调函数和兄弟级调用上下文同时可见，深度控制父祖辈与儿孙辈追溯代数。它保留真实调用边，不把下级函数折叠成模糊的"辅助函数列表"，因此能直接回答"谁触发了它""它继续影响谁""同一父调用下还有哪些并行动作"。

<img src="pic/依赖关系.png">

### 理解文档

理解文档视图一键生成函数/文件/模块/项目四个粒度的结构化理解文档。函数级文档包含角色推断、调用链摘要、复杂度风险指标（圈复杂度/有效 LOC/嵌套深度/扇入扇出/跨模块调用）和对称性风险检测；文件级文档聚合所有符号角色与 Mermaid 依赖图；模块级文档检测架构边界违规；项目级文档输出全局架构概览与热点模块排名。

<img src="pic/理解文档.png">

## 竞品对比

| | CodeGraph | GitNexus | Graphify | Understand-Anything | **AstraMap** |
|---|-----------|----------|----------|--------------------|----|
| **索引源** | Tree-sitter | Tree-sitter + 推导 | Tree-sitter + LLM | Tree-sitter + LLM | **SCIP + Tree-sitter** |
| **语义精度** | 启发式 | 符号级推导 | 混合语义 | LLM 语义 | **编译器级** |
| **C/C++** | 有限 | 基础 | 基础 | 基础 | **C + C++ 完整** |
| **条件编译** | 无 | 单路径 | 无 | LLM 盲猜 | **边级 metadata 标注** |
| **部署** | npm | 二进制/WASM | Python | npm | **单静态二进制+Web UI** |
| **MCP** | stdio | stdio | 弱 | 无 | **stdio + REST** |
| **理解文档** | 无 | 无 | 知识库 | Onboarding | **自动生成+风险检测** |
| **额外价值** | 纯地图 | 审计+链路 | 知识库 | Onboarding | **地图+治理+文档** |

与 Sourcegraph 在 SCIP 精度上同源，定位互补：Sourcegraph 是跨仓库企业级云端平台，AstraMap 是单机零依赖的代码地图+治理引擎。差异化在**条件编译边级标注**（调用边直接携带 `#ifdef` 守卫上下文）、**治理一体化**（架构合规/复杂度/死代码共享图谱）和**零基础设施部署**。

## 支持语言

当前 **7 种语言**已投入生产使用，基于 `language_registry.go` 的统一注册架构实现。另有 **12 种语言**（Rust、C#、Kotlin、Scala、Ruby、PHP、Dart、VB、Swift、Lua、Bash、Zig）的扩展设计已完成，见 `docs/language-extension-guide.md`。

| 语言 | 扩展名 | Tree-sitter | 语义 Provider | 能力等级 |
|------|--------|-------------|---------------|----------|
| Go | `.go` | `tree-sitter-go` | `scip-go` 自动生成 | `semantic` |
| Python | `.py` | `tree-sitter-python` | `scip-python` 自动生成 | `semantic` |
| TypeScript / TSX | `.ts` `.tsx` | `tree-sitter-typescript` | `scip-typescript` 自动生成 | `semantic` |
| JavaScript / JSX | `.js` `.jsx` `.mjs` `.cjs` | TypeScript/TSX grammar | 与 TypeScript 共享 `scip-typescript` | `semantic` |
| C | `.c` `.h` | `tree-sitter-c` | 与 C++ 共享 `scip-clang` | `semantic` |
| C++ | `.cc` `.cpp` `.cxx` `.hpp` `.hxx`，以及 C++ 项目中的 `.h` | `tree-sitter-cpp` | 与 C 共享 `scip-clang` | `semantic` |
| Java | `.java` | `tree-sitter-java` | 可导入已有 SCIP，暂不自动生成 | `local-graph` |

HTTP/MCP 状态接口同时返回 `supportedLanguages` 和 `languageCapabilities`。共享 Provider
只执行一次：C/C++ 共用一次 `scip-clang`，TypeScript/JavaScript 共用一次
`scip-typescript`。

## 快速开始

```bash
go build -o amap ./cmd/amap    # 构建
amap install                    # 一键注册 MCP 到 Claude Code / Cursor / VS Code / Codex / Antigravity / Windsurf / Cline
amap index                      # 快速增量更新（按文件哈希跳过未变更文件）；首次运行自动初始化 SCIP
amap watch [10]                 # 持续监听源码变更守护进程，检测到变化自动增量索引，无需手动 re-index
amap dashboard                  # 启动可视化控制台
```

`amap watch` 和 Dashboard 的区别：watch 是轻量级后台守护进程（无 UI），负责文件监听与自动增量索引；Dashboard 是可视化浏览器，负责图谱浏览与理解文档生成。两者可独立运行，也可同时运行。

首次运行 `amap index` 会自动生成 `.astramap/config.yaml` 示例。需要过滤辅助文件或目录时，编辑该文件后重新运行 `amap index`：

```yaml
index:
  languages:
    - go
  exclude:
    - "docs/**"
    - "vendor/**"
    - "**/*.pb.go"
  include:
    - "src/**"
```

`exclude` 作用于所有阶段；`include` 为白名单模式，仅索引匹配路径。已索引但后被排除的文件会在下次 `amap index` 时自动清理。

`languages` 会记录上次索引选择。后续普通 `amap index` 会复用该选择并静默执行；显式传 `--lang` 会更新它。

## 命令一览

### 核心服务

| 命令 | 说明 |
|------|------|
| `amap serve [--project <path>]` | MCP stdio 服务 |
| `amap dashboard [--project <path>] [--host <addr>] [--port <port>]` | Web 可视化控制台 |
| `amap index [选项]` | 快速增量更新一次（按文件哈希跳过未变更文件）；已有 SCIP 时默认不重建 SCIP；输出结构化过滤报告 |
| `amap index --refresh-scip` | 强制重新生成并导入 SCIP |
| `amap index --full` | 全量刷新 SCIP 层，再执行 Tree-sitter 增量扫描 |
| `amap index --treesitter-only` | 仅 Tree-sitter 快速扫描，跳过 SCIP |
| `amap watch [秒数]` | 持续低频监听守护进程，检测到文件变更自动增量索引（默认 10 秒防抖） |
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
| `astramap_callers` | "谁调用了 X"（支持 `limit` 分页） |
| `astramap_callees` | "X 依赖什么" |
| `astramap_impact` | "改了 X 会影响什么" |
| `astramap_trace` | "从 A 到 B 的调用链" |
| `astramap_status` | "索引好了吗" / "图谱是否与磁盘一致" |
| `astramap_files` | "项目有哪些文件" |

### MCP 增强

| 增强 | 说明 |
|------|------|
| **条件编译 metadata** | callers/callees/trace/impact 结果中，C/C++ 调用边自动携带 `metadata` 字段，标注 `#if`/`#ifdef`/`#ifndef` 守卫条件 |
| **脏文件检测** | `astramap_status` 返回 `dirtyCount` + `dirtyFiles`，AI 代理可判断图谱是否过期 |
| **文件分页** | `astramap_files` 支持 `limit`/`offset` 参数，大项目分页查询 |
| **单符号精确解析** | callers/callees/impact/trace 使用 `ResolveSymbolToIDs` 多符号解析，重载/多定义结果自动合并去重 |
| **搜索 kind 校验** | `astramap_search` 的 `kind` 参数校验，非法值返回错误；新增 `typedef` kind |
| **callers 分页** | `astramap_callers` 支持 `limit` 参数，超限截断并提示 |

## REST API

Dashboard 同时暴露 REST JSON API：

| 端点 | 方法 | 参数 | 说明 |
|------|------|------|------|
| `/api/astramap/status` | GET | — | 索引状态统计（含脏文件检测） |
| `/api/astramap/search` | GET | `q`, `kind` | 符号搜索（kind 校验） |
| `/api/astramap/node/{id}` | GET | 路径 `id` | 节点详情 |
| `/api/astramap/callers/{id}` | GET | 路径 `id`, `limit` | 上游调用者（含条件编译 metadata，支持分页） |
| `/api/astramap/callees/{id}` | GET | 路径 `id` | 下游被调用者（含条件编译 metadata） |
| `/api/astramap/impact/{id}` | GET | `depth` | 影响分析 |
| `/api/astramap/explore` | GET | `q`, `maxFiles` | 区域探索 |
| `/api/astramap/trace` | GET | `from`, `to` | 路径追踪（含条件编译 metadata） |
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
    ├── astramap_edges    关系边：calls、contains、imports（含条件编译 metadata）
    ├── astramap_files    文件跟踪：路径、哈希、语言
    └── astramap_fts      FTS5 全文搜索
    ↓
    ├── MCP stdio JSON-RPC → Claude Code / Cursor / VS Code / Codex / Antigravity
    └── HTTP REST API + D3.js Dashboard → 浏览器可视化
```

边的来源标识：`scip`（编译器级精度）> `tree-sitter`（AST 解析）> `heuristic`（模式匹配）。同源冲突时 SCIP 边优先。

C/C++ 调用边额外携带 `metadata` 字段，标注预处理器守卫条件（如 `#ifdef USE_SSL`），图遍历结果直接暴露条件编译上下文。

### 持续同步链路

```
源码编辑保存
    ↓
amap watch（进程级轮询）
    ↓ 自定义间隔（默认 10 秒）
按文件哈希比较 → 仅处理新增/修改文件
    ↓
Tree-sitter 增量解析 → SQLite 图谱更新
    ↓
MCP/HTTP 查询 → 始终返回最新结果
```

三种模式按需选择：

| 场景 | 推荐方式 | 说明 |
|------|----------|------|
| **开发中，需要浏览器图谱** | `amap dashboard` | 可视化图谱浏览与理解文档生成 |
| **开发中，仅需 MCP 查询** | `amap watch` + `amap serve` | 轻量守护进程，无 UI 开销 |
| **一次性索引** | `amap index` | 快速增量更新，按哈希跳过未变更文件 |

## 项目结构

```
astramap/
├── cmd/amap/main.go          CLI 入口
├── astramap/
│   ├── schema.go             SQLite DDL
│   ├── astramap.go           SCIP 导入 + 增量同步
│   ├── treesitter.go         Tree-sitter 解析 + 跨文件调用启发（歧义过滤 + 最窄 span 选择 + 宏隐式函数定义 + 顺序初始化函数指针）
│   ├── service.go            共享查询服务层（MCP/REST 共用）+ 脏文件检测 + 分页
│   ├── query_helpers.go      查询辅助：kind 校验、depth 规范化、符号解析、条件编译标注、批量文件路径、源码缓存
│   ├── filter.go             索引过滤配置（.astramap/config.yaml）。生态感知过滤：自动识别工程根 → 生成生态排除规则；内置 40+ 通用排除规则（VCS/依赖/缓存/二进制/生成源码）；生成文件头检测；统一 Evaluate 引擎；结构化过滤报告
│   ├── graph.go              图遍历引擎（BFS/DFS/可达性/耦合）+ 条件编译 metadata 标注
│   ├── mcp.go                MCP JSON-RPC stdio 服务
│   ├── server.go             HTTP REST API + Dashboard + 理解文档生成
│   └── web/                  D3.js 可视化（go:embed）
├── go.mod                    Go 1.25
├── build.sh                  Linux 静态构建（CGO + musl）
└── QUICKSTART.md             2 分钟部署指南
```

### 生态感知过滤

AstraMap 自动识别项目生态并应用对应过滤规则，无需手动配置：

| 生态 | 识别标记 | 自动排除 |
|------|---------|---------|
| Go | `go.mod`, `go.work` | — |
| Node.js | `package.json` | `dist/`, `coverage/` |
| Rust | `Cargo.toml` | `target/` |
| Maven | `pom.xml` | `target/` |
| Gradle | `build.gradle` | `build/` |
| CMake | `CMakeLists.txt` | `cmake-build-*/` |
| Python | `pyproject.toml`, `setup.py` | — |
| Swift | `Package.swift` | `.build/` |
| Bazel | `WORKSPACE`, `MODULE.bazel` | `bazel-*/` |

内置通用规则覆盖：VCS 元数据 (`.git/`, `.svn/`, `.hg/`, `.astramap/`)、第三方依赖 (`node_modules/`, `vendor/`, `Pods/`, `third_party/`)、缓存 (`__pycache__/`, `.pytest_cache/`, `.mypy_cache/`, `.cache/`)、OS/编辑器垃圾 (`.DS_Store`, `*.swp`, `*~`)、压缩/Source Map (`*.min.js`, `*.js.map`)、生成源码 (`*.pb.go`, `*_pb2.py`, `*.pb.cc`, `*.g.dart`)、二进制文件 (`*.o`, `*.class`, `*.jar`, `*.wasm`, `*.zip`, `*.png`, `*.woff`)，以及文件头含 `Code generated` / `DO NOT EDIT` 的生成文件。

如需覆盖内置规则，在 `.astramap/config.yaml` 中使用 `advanced.forceInclude`：

```yaml
index:
  # exclude:
  #   - "examples/legacy/**"
  # advanced:
  #   forceInclude:
  #     - "src/.domain/**"
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

### v0.3

**三大特性**：

| 特性 | 说明 |
|------|------|
| **🛡️ 生态感知智能过滤** | 自动识别 9 种工程生态（Go/Node/Rust/Maven/Gradle/CMake/Python/Swift/Bazel），按生态生成构建产物排除规则；内置 40+ 条通用排除规则覆盖 VCS/依赖/缓存/二进制/压缩/生成源码，零配置开箱即用 |
| **📋 结构化过滤报告** | `amap index` 输出按规则类型分组的排除报告（规则 ID + 原因 + 数量），过滤行为完全透明 |
| **🔗 外部调用占位符绑定** | 文件重索引时，被删除节点的入边自动迁移到 `external:` 占位符；新索引的同名函数自动绑定原有外部调用边，跨文件调用链在增量更新后保持连续 |

**完整变更**：

| 变更 | 说明 |
|------|------|
| **生态感知工程根识别** | `DetectProjectRoots` 扫描 `go.mod`/`package.json`/`Cargo.toml`/`pom.xml`/`build.gradle`/`CMakeLists.txt`/`pyproject.toml`/`Package.swift`/`WORKSPACE`/`MODULE.bazel` 等标记文件，建立工程根映射 |
| **动态生态排除规则** | `BuildEcosystemRules` 按识别到的生态自动生成排除规则：Node.js → `dist/`/`coverage/`、Rust → `target/`、Maven → `target/`、Gradle → `build/`、CMake → `cmake-build-*/`、Swift → `.build/`、Bazel → `bazel-*/` |
| **40+ 内置通用排除规则** | 统一规则引擎覆盖 VCS 元数据、第三方依赖（node_modules/vendor/Pods/third_party）、缓存（__pycache__/.pytest_cache/.mypy_cache/.cache/.sass-cache）、OS/编辑器垃圾（.DS_Store/Thumbs.db/*.swp/*~/*.tmp）、压缩/Source Map（*.min.js/*.js.map/*.d.ts.map）、生成源码（*.pb.go/*_pb2.py/*.pb.cc/*.g.dart）、二进制（*.o/*.class/*.jar/*.wasm/*.zip/*.png/*.woff）、模型/字体文件 |
| **生成文件头检测** | `IsGeneratedByHeader` 扫描文件前 8KB / 100 行，正则匹配 `Code generated.*DO NOT EDIT` / `Generated by.*DO NOT EDIT` / `This file (was )?automatically generated` / `DO NOT EDIT THIS FILE` / `<auto-generated>` 等标记，自动排除生成文件 |
| **统一过滤评估引擎** | `Evaluate`/`EvaluateDir` 替代旧 `Allows`/`AllowsDir`，内置规则 → 隐藏路径 → 用户 Include → 用户 Exclude 四级优先级；`forceInclude` 可覆盖任何 `Overridable` 规则 |
| **向后兼容配置解析** | 旧配置中的 `scipExclude`/`treeSitterExclude` 自动合并到 `Exclude`；新增 `advanced.forceInclude` 配置键 |
| **结构化过滤报告** | `IndexFilterMatchReport` 从三段字符串数组升级为 `[]IndexFilterExcludedEntry`，含 Path/RuleID/Kind/Reason；`printIndexFilterMatchReport` 按 Kind 分组输出，超限折叠 |
| **外部调用占位符迁移** | `SyncFileAstraMap` 删除旧节点前，将入边目标更新为 `external:<prefix> . . $ <name>.` 占位符；新节点索引后，将 `external:` 占位符边重新绑定到实际节点，增量更新不破坏跨文件调用链 |
| **增量文件启发式刷新** | `SyncFileAstraMap` 完成后自动执行 `ResolveCrossFileCallsForFiles`，确保单文件增量更新后函数指针/宏调用启发式边即时生效 |
| **WAL 残留锁回收** | 数据库初始化时执行 `PRAGMA wal_checkpoint(TRUNCATE)`，回收上次异常退出残留的 WAL 锁，避免 "database is locked" |
| **watch 目录过滤统一** | `addIndexWatchDirs` 使用 `IndexFilter.AllowsDir` 替代硬编码 `skipDirs`，watch 行为与索引过滤完全一致 |
| **文件遍历目录过滤统一** | `SyncAllFilesAstraMapResult` 和 `countFilesByExt`/`projectHasExtensions` 移除硬编码 `skipDirs`，统一走 `IndexFilter.AllowsDir` |
| **explore 结构关系增强** | `QueryExplore` 对 struct/class/interface 节点额外查询 `contains`/`implements` 边，输出成员归属与实现关系 |
| **Go typedef 搜索增强** | `QuerySearchPaged` `kind=typedef` 查询自动包含 Go `kind=struct` 节点，支持 Go type alias 检索 |
| **callers/callees/impact 规范化起点** | MCP 工具在查询前通过 `resolveCanonicalTraceStart` 将输入符号 ID 规范化为主定义 ID，消除 tree-sitter/SCIP 格式差异导致的查询起点歧义 |
| **消除硬编码目录跳过列表** | 删除 `shouldSkipIndexDir`、`skipDirs`、`shouldSkipWatchDir` 等硬编码列表，全部收敛到 `IndexFilter` 规则引擎 |
| **配置模板精简** | 示例配置移除 `scipExclude`/`treeSitterExclude`，仅保留 `include`/`exclude`/`advanced.forceInclude` |

### v0.2

**三大特性**：

| 特性 | 说明 |
|------|------|
| **🔁 `amap watch` 持续文件监听** | 独立守护进程，低频轮询文件变更，检测到变化自动触发增量刷新，保持图谱始终与磁盘一致。无需手动 `amap index`，无需保持 Dashboard 运行 |
| **⚡ 按哈希增量更新** | Tree-sitter 解析按文件内容哈希跳过未变更文件；SCIP 已有则默认不重建。普通 `amap index` 秒级完成，仅在新增/修改文件时执行解析 |
| **🖥️ Dashboard 可视化** | D3.js 交互式图谱浏览，探索视界 + 依赖关系 + 理解文档 |

**完整变更**：

| 变更 | 说明 |
|------|------|
| **条件编译 metadata 标注** | C/C++ 预处理器守卫（`#if`/`#ifdef`/`#ifndef`）自动标注到调用边 metadata，GetCallers/GetCallees/TracePath/AnalyzeImpact 结果直接暴露条件编译上下文 |
| **多定义合并查询** | callers/callees/impact/trace 统一使用 `ResolveSymbolToIDs` 多符号解析，重载/多定义结果自动合并去重，消除单符号选择导致的信息丢失 |
| **TracePath 多起点多终点** | `TracePath` 支持多 from/to ID 集合 BFS，重载符号间路径一次查询全部返回 |
| **脏文件检测** | `QueryDirtyFiles`/`QueryDirtyFilesWithCount`，status 查询返回 `dirtyCount` + `dirtyFiles`，AI 代理可判断图谱是否与磁盘一致 |
| **文件列表分页** | `QueryFilesPaged` + MCP `astramap_files` 支持 `limit`/`offset` 参数 |
| **搜索 kind 校验** | `validateSearchKind` 校验 kind 参数，非法值返回错误（REST 400 / MCP error） |
| **歧义调用过滤** | 跨文件启发式调用解析过滤 Python dunder 方法（`__xxx__`）和无点号全局函数名的多目标歧义 |
| **最窄 span 选择** | 跨文件调用解析在多个候选函数中选择行跨度最小的（最精确的）作为调用者 |
| **节点文件路径模糊匹配** | `QueryNodeBySymbol` 文件参数支持尾部匹配，不再要求精确路径 |
| **查询辅助提取** | 新增 `query_helpers.go`，集中 kind 校验、depth 规范化、符号解析、条件编译标注等辅助函数 |
| **索引过滤系统** | `.astramap/config.yaml` 支持 include/exclude/scipExclude/treeSitterExclude 四级过滤，已排除文件自动清理 |
| **模块级聚合图** | `/api/astramap/overview` 项目级聚合 + `/api/graph/module` 模块内钻取 |
| **理解文档生成** | `/api/documents/generate` 一键生成函数/文件/模块/项目级理解文档，含 Mermaid 依赖图、复杂度风险表、架构边界违规检测 |
| **C 语言独立支持** | `tree-sitter-c` 独立绑定，`.h` 文件智能 C/C++ 判定 |
| **MCP 符号消歧** | callers/callees/impact/trace 支持模糊符号解析，重载/多定义自动合并去重 |
| **Trace 邻域重构** | 兄弟感知邻域模型，修复跨文件自调用过滤 |
| **SCIP 边丢失修复** | 全局符号映射修复跨文档引用丢失 |
| **静态链接构建** | musl 静态编译，无 GLIBC 依赖 |
| **Dashboard 增强** | `--host` 参数、自动端口探测、LAN IP 显示、后台启动、请求日志 |
| **install 增强** | 探测式注册（仅写入已安装客户端），支持 Antigravity/Windsurf/Cline，注册核验 |
| **CLI 精简** | `--project` 全局参数，移除未实现命令 |
| **Trace API 多定义路径** | REST `/api/astramap/trace` 和 MCP `astramap_trace` 使用 `ResolveSymbolToIDs` 多符号解析 + `TracePath(fromIDs, toIDs)` 多起点多终点 BFS |
| **Dashboard 头文件降噪** | 图谱查询、模块聚合图、函数列表统一排除 `.h`/`.hpp`/`.hh` 头文件节点，减少 C/C++ 项目噪声 |
| **函数树目录层级重构** | 依赖分析视图函数树从扁平单层目录改为完整多级目录树，按需展开填充函数按钮，搜索模式扁平化展示 |
| **函数列表独立懒加载** | 依赖分析视图不再依赖探索视界先完成全局图初始化，独立从 `/api/astramap/functions` 拉取函数列表 |
| **trace.js 懒加载加固** | Promise 去重防止重复加载、加载失败自动重置状态、缓存版本号破缓存 |
| **callers 结果分页** | MCP `astramap_callers` 新增 `limit` 参数，REST `/api/astramap/callees/{id}` 新增 `limit` 查询参数，超限截断并提示 |
| **C/C++ 函数指针调用解析** | 跨文件启发式新增 `.field = &func` 指定初始化模式 + `struct_name var = { func1, func2 }` 顺序初始化模式识别，`->field()` / `.field()` 调用自动解析到函数指针目标 |
| **C/C++ 宏返回调用解析** | 识别 `XXXRETURN(func)` 模式的宏展开调用，提取内部函数名建立调用边 |
| **C/C++ typedef 分类修正** | `typedef struct` → `struct`，`typedef enum` → `enum`，纯 `typedef` → `type`；搜索 `kind=struct` 含 `typedef struct`，搜索 `kind=enum` 含 `typedef enum`；`normalizeSearchNodeKind` 按请求 kind 精确归一 |
| **搜索 kind 新增 typedef/type** | `astramap_search` 的 `kind` 参数新增 `typedef` 和 `type` 值，C/C++ typedef 类型可独立检索；`typedef`/`type` 搜索自动合并两种 kind |
| **源码缓存按 ModTime 失效** | 文件内容缓存键从路径改为 `(path, modTime, size)` 三元组，文件修改后自动失效，消除条件编译 metadata 标注的脏数据 |
| **单文件缓存精准失效** | 新增 `InvalidateQueryHelperCacheForFile`，文件同步/删除时仅清除该文件相关缓存，不再全量清空 |
| **条件编译 metadata 批量标注** | `QueryTraceCTE` 使用 `BatchNodeFilePaths` 批量获取文件路径，消除 N+1 查询；`annotateConditionalMetadataWithFileMap` 支持预传入文件映射 |
| **Impact depth=0 根符号返回** | `AnalyzeImpact` 在 `depth=0` 时返回根符号自身而非空结果，语义完整 |
| **Gzip 中间件增强** | 新增 `shouldGzipResponse` 判断逻辑，API 响应和文本类静态资源统一压缩；添加 `Vary: Accept-Encoding` 头 |
| **Watcher Timer 泄漏防护加固** | 提取 `resetTimer` 工具函数，统一 Timer 重置逻辑，消除 `Stop/Reset` 竞态 |
| **trace.js O(1) 图索引** | 新增 `traceLinkMap`（边索引）和 `visibleNodeSet`（可见节点集合），公共叶子节点克隆去重从 `Array.some` 改为 `Set.has` |
| **CSS blur 半径统一收敛** | 所有 `backdrop-filter: blur()` 和 `filter: blur()` 统一引用 `--panel-blur` CSS 变量或降至 4px，减少 GPU 合成开销 |
| **explore 输出截断** | MCP `astramap_explore` 输出设 60KB 总量预算，单段源码 12KB 上限，超限截断并提示缩小 `maxFiles` 或收窄查询 |
| **explore 默认 maxFiles 收敛** | `QueryExplore` 默认 `maxFiles` 从 10 降为 3，减少 AI 代理 Token 消耗，用户可显式传参扩大 |
| **SyncAllFiles 启发式刷新** | 即使文件无变更也执行跨文件启发式调用关系解析，确保函数指针/宏调用等新增启发式边在首次 `amap index` 后生效 |
| **C/C++ 宏隐式函数定义** | 识别 `DECLARE_xxx(func)` / `xxx_INIT(func)` / `xxx_REGISTER(func)` 等声明式宏模式，自动创建函数节点并标注来源宏名 |
| **C/C++ `#define` 宏节点** | Tree-sitter 捕获 `preproc_def`/`preproc_function_def` 为 `macro` kind 节点 |
| **C/C++ 前向声明过滤** | `declaration` 节点仅当含 `parameter_list` 时识别为函数，过滤纯前向声明噪声 |
| **SCIP C/C++ 签名与文档还原** | SCIP 索引中 C/C++ 节点自动提取首行代码作为签名、前导注释作为 docstring |
| **Tree-sitter 文档注释提取** | 所有语言节点自动提取前导 `//`/`/* */` 注释作为 docstring，含块注释与空行容忍 |
| **中文查询分词** | explore/search 支持中文单字分词，过滤中文停用词（的/了/和/是/在…），中英混合查询无缝工作 |
| **空查询拒绝** | `QuerySearchPaged` 拒绝空字符串查询，避免全表扫描 |
| **`::` 分隔符规范化** | 边端点显示时正确处理 C++ `namespace::symbol` 格式，不再错误截断 |

### v0.1

| 特性 | 说明 |
|------|------|
| **SCIP + Tree-sitter 双引擎索引** | SCIP 编译器级索引为主（跨文件调用、符号消歧），Tree-sitter AST 为辅（签名、嵌套归属、源码还原） |
| **SQLite 知识图谱** | nodes/edges/files/FTS5 四类核心结构，WAL 模式单写者，内容哈希去重 |
| **MCP stdio 服务** | 9 个工具（search/explore/node/callers/callees/impact/trace/status/files），JSON-RPC stdio 协议 |
| **REST API** | 11 个端点，覆盖搜索、节点详情、调用链追踪、影响分析、区域探索 |
| **D3.js 交互式 Dashboard** | 探索视界（全局→局部图谱浏览）、依赖关系视图（调用邻域展开） |
| **7 种语言支持** | Go / Python / TypeScript / JavaScript / C / C++ / Java；能力等级由注册表统一派生 |
| **增量索引** | Tree-sitter 按文件哈希跳过未变更文件；`amap index` 快速增量；`amap watch` 持续监听 |
| **CLI 诊断工具集** | locate / diff / hotspots / deadcode / cycles / coupling / owners / tree / query |
| **一键 MCP 注册** | `amap install` 注册到 Claude Code / VS Code / Cursor / Codex |
| **单二进制部署** | 内嵌 SQLite + Tree-sitter WASM + D3.js Dashboard，零依赖开箱即用 |

## 许可

© 2025-2026 何志川 — AstraMap v0.3
