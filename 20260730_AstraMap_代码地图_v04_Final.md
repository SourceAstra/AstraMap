# 面向 AI 编程代理的高精度语义代码地图

> 当 Claude Code 在一个 50 万行代码的项目里反复 `grep` + `Read`，单次定位消耗 3-5 次工具调用、5K-20K Token——这不是效率问题，是结构性缺陷：**AI Agent 缺乏代码空间的语义索引，每次定位都是全量盲搜。**

<p align="center">
  <img src="pic/banner.png" alt="AstraMap Banner" width="100%">
</p>

---

## 一、核心矛盾：Token 燃烧与语义真空

AI 编程代理（Claude Code、Codex、Cursor、Windsurf）正在重塑软件工程，但面对大型工业级代码库时，始终受制于一个结构性困境：

> **没有语义索引，只能逐文件盲搜。**

### 一个真实的成本场景

Agent 需要定位 `handleRequest` 的底层调用链：

1. **第一次调用**：执行 `grep` 搜索，返回 47 个匹配行。
2. **第二至四次调用**：逐个 `Read` 相关文件，上下文注入 15K Token。
3. **结果**：其中 38 个是同名不同作用域的无关函数——Token 白烧，上下文窗口被噪音填满。

### 引入语义代码地图后的量化对比

| 操作维度 | 传统盲搜 | AstraMap 语义代码地图 | 改善幅度 |
|:---|:---|:---|:---|
| **精准定位符号** | 3-5 次工具轮询，5K-20K Token | 1 次结构化查询，约 800 Token（单次 MCP 调用开销） | Token 消耗降低约 80% |
| **链路追溯** | 10-20 次 grep + Read 盲目遍历 | 1 次 `explore` / `trace` 直达调用路径 | 工具调用次数降低约 10 倍 |
| **变更影响分析** | 正则启发式，约 70% 准确率（假阳性主导） | 1 次 `impact` 递归传播，编译器级确定性 | 从"启发式估算"到"确定性传播" |

> **注**：上述数据基于 AstraMap 在 Go/TypeScript 项目中的内部基准测试。实际效果取决于 SCIP Provider 覆盖率与项目类型系统复杂度。

---

## 二、为什么纯 Tree-sitter 无法解决语义消歧问题？

市面上已有不少代码地图工具，按索引引擎可分为三个流派：

- **纯 Tree-sitter 流**：CodeGraph、GitNexus 等，基于 Tree-sitter 快速构建 AST，部署极简。
- **Tree-sitter + LLM 语义流**：Graphify（GitHub 63K+ Stars），在 Tree-sitter AST 之上叠加 LLM 语义提取，支持代码+文档+PDF+图片多模态入图，擅长回答"代码为什么这样设计"。
- **纯 SCIP / LSIF 流**：Sourcegraph，编译器级语义索引，企业级全仓库搜索平台。

**Tree-sitter 是优秀的轻量级 AST 解析器**——无需编译环境、解析速度极快，在小项目或局部语法提取上体验良好。Graphify 的多模态能力在设计意图理解场景中也有独特价值。但在大型、多模块、存在类型系统的工业级代码库中，**纯 Tree-sitter 存在结构性语义盲区**，而 LLM 语义层虽能补充设计意图，却无法提供编译器级的确定性调用拓扑：

### 1. 接口实现的多义关联

- **Tree-sitter 局限**：只做文本语法分析，没有类型推导。在 Go 或 Java 中，当存在 `io.Reader` 或某个通用接口时，所有实现了 `Read()` 方法的结构体都会被混为一谈。调用图退化为高噪音全连接网。
- **实际后果**：AI 做影响分析时，改动一个接口方法，工具报告"全库 200 个文件受影响"，上下文直接爆表。

### 2. 同名符号与函数重载的全局混淆

- **Tree-sitter 局限**：`pkgA.Init()` 与 `pkgB.Init()` 拼写完全相同。缺少编译器级的命名空间消歧，Tree-sitter 只能按字面量匹配，将两个毫不相干的业务链路硬连在一起。

### 3. 跨模块/第三方 SDK 的调用链断裂

- **Tree-sitter 局限**：无法解析外部依赖包（如 `import` 的 SDK 或私有库），调用链到达项目边界即断裂，AI 无法追溯底层实现。

### 4. 影响分析的假阳性扩散

- **Tree-sitter 局限**：充斥启发式推演，生成的拓扑连线存在大量假阳性，导致死代码检测和变更影响分析失去工程指导价值。

**根本原因**：Tree-sitter 缺乏类型系统，无法做编译器级的符号消歧。Graphify 的 LLM 语义层能推断"这两个 Init 大概不是同一个"，但推断不等于确定性——在影响分析等需要精确拓扑的场景中，启发式推断的假阳性率仍然不可控。

---

## 三、AstraMap 的解法：SCIP 语义主导 + Tree-sitter 实时增量

针对上述语义盲区，AstraMap 采用"SCIP 高精度语义为主，Tree-sitter 实时语法为辅"的双层混合架构：

```
源码 ──┬──→ Tree-sitter 实时层（毫秒级单文件 AST 增量）
       │
       └──→ SCIP 语义 Provider（编译器级全量类型推导）
                    │
                    ▼
            AstraMap 合并引擎
                    │
                    ▼
          SQLite 语义代码图谱
              ┌─────┴─────┐
              ▼           ▼
         MCP Server    REST API
              │           │
              ▼           ▼
        AI Agent     Web Dashboard
```

### 双层分工

- **SCIP 语义主导层（决定图谱的精度上限）**：结合语言工具链（LSP/LSIF/SCIP Provider）进行完整的静态类型推导，生成包含跨文件消歧、精准多态跳转、全局唯一符号标识（U.S.N.）的确定性拓扑图。**精度取决于 SCIP Provider 的编译环境完整性。**
- **Tree-sitter 语法辅助层（决定图谱的实时性）**：在 SCIP 构建的骨架之上，Tree-sitter 毫秒级监听磁盘文件变更，提供轻量级动态覆写（修复编辑导致的行号漂移、实时追加新方法），避免频繁触发编译器全量重建。**两次 SCIP 运行之间，Tree-sitter 覆盖层提供降级可用性。**

| 维度 | SCIP 语义主导层 | Tree-sitter 语法辅助层 |
|:---|:---|:---|
| **定位与角色** | 高精度跨文件语义核心 | 实时增量结构补丁 |
| **解析机理** | 编译器类型推导 + 符号消歧 | 纯文本 AST 解析，无需编译 |
| **精度** | 编译器级确定性（取决于 Provider 覆盖率） | 启发式补充，降级可用 |
| **更新时机** | 按需/定时构建（`amap index`） | 实时监听（`amap watch`），保存即生效 |

---

## 四、12 种语言支持矩阵

| 语言 | Tree-sitter 实时层 | SCIP 语义 Provider | 备注 |
|:---|:---|:---|:---|
| Go | 内置 | scip-go | |
| TypeScript | 内置 | scip-typescript | |
| JavaScript | 内置 | scip-typescript | |
| Python | 内置 | scip-python | |
| Java | 内置 | scip-java | |
| Kotlin | 内置 | scip-java | |
| Scala | 内置 | scip-java | |
| C | 内置 | scip-clang | 需 `compile_commands.json` |
| C++ | 内置 | scip-clang | 需 `compile_commands.json` |
| Rust | 内置 | scip-rust | |
| C# | 内置 | scip-dotnet | |
| Ruby | 内置 | scip-ruby | |

> SCIP Provider 是否可用取决于项目工具链配置。例如 C/C++ 高精度索引需要有效的 `compile_commands.json`；无 SCIP Provider 时，系统降级到 Tree-sitter 启发式推演。

---

## 五、Web Dashboard：三种交互式阅读视界

AstraMap 内置 Web Dashboard，将抽象架构提炼为三种交互式阅读体验：

### 1. 探索视界（Explore View）

动态星图级的交互式文件与符号导航。支持从项目、目录、文件逐层深入到局部函数实现，一键洞悉模块层级深度与节点聚合度。

<p align="center">
  <img src="pic/explore.png" alt="AstraMap Explore View" width="100%">
</p>

### 2. 依赖拓扑（Dependency Graph）

基于力导向图的可视化调用拓扑。精准还原目标函数的 `callers`（谁调用了我）与 `callees`（我调用了谁）调用邻域，支持鼠标交互拉伸与路径追踪。

<p align="center">
  <img src="pic/trace.png" alt="AstraMap Dependency Graph" width="100%">
</p>

### 3. 理解文档（Understanding Documents）

自动生成的模块级与文件级语义说明书。结合上下文源码片段与结构关系脉络，无需在几十个文件间频繁切换，即可完成架构速读与团队交接。

<p align="center">
  <img src="pic/understand.png" alt="AstraMap Understanding Documents" width="100%">
</p>

---

## 六、MCP 工具矩阵：9 个结构化查询工具

AstraMap 通过 MCP 协议暴露 9 个结构化查询工具，让 AI Agent 可以像资深架构师一样精准操控代码库：

| MCP 工具 | 功能 | AI Agent 典型调用场景 |
|:---|:---|:---|
| `astramap_search` | 符号模糊与全文搜索 | "查找 `UserService` 在哪定义" |
| `astramap_explore` | 区域代码流与关系探索 | "探索 `Auth` 模块和 `DB` 模块怎么关联" |
| `astramap_node` | 符号实体详情与源码片段 | "读取 `ProcessOrder` 的函数签名与源码" |
| `astramap_callers` | 查询直接上游调用者 | "看下谁调用了 `PaymentGateway.Pay`" |
| `astramap_callees` | 查询直接下游被调用者 | "查看 `Execute` 方法依赖了哪些底层 API" |
| `astramap_impact` | 递归变更影响范围分析 | "如果修改 `User` 结构体字段，会波及哪些文件" |
| `astramap_trace` | 跨符号调用路径追踪 | "追踪从 HTTP Route 到 DB 写操作的调用路径" |
| `astramap_status` | 检查索引健康度与来源 | "确认当前代码地图的 SCIP 覆盖率" |
| `astramap_files` | 按路径/模式查询已索引文件 | "列出 `pkg/network` 下的所有 Go 源文件" |

---

## 七、CLI 速查

| 命令 | 说明 |
|:---|:---|
| `amap index` | 构建/增量更新代码地图 |
| `amap index --full` | 全量刷新 |
| `amap watch 30` | 30 秒间隔持续同步 |
| `amap dashboard` | 启动 Web 可视化（`http://localhost:3000`） |
| `amap locate <symbol>` | 定位符号定义 |
| `amap tree <symbol>` | 调用拓扑树 |
| `amap diff --suggest-tests` | Git 变更影响 + 测试建议 |
| `amap hotspots` | 代码热点检测 |
| `amap deadcode` | 死代码分析 |
| `amap cycles` | 循环依赖检测 |
| `amap coupling` | 模块耦合分析 |
| `amap query "<SQL>"` | 直接查询 SQLite 图谱 |

---

## 八、代码地图工具客观横评

在评估代码地图工具时，必须结合具体工程场景。以下是 AstraMap 与主流开源/商业解法的客观对比：

| 对比维度 | CodeGraph / GitNexus | Graphify | Sourcegraph (SCIP) | **AstraMap** |
|:---|:---|:---|:---|:---|
| **核心索引引擎** | 纯 Tree-sitter | Tree-sitter + LLM 语义 | 纯 SCIP / LSIF | **SCIP + Tree-sitter 双层混合** |
| **跨文件消歧能力** | 弱（基于文本/启发式） | 中（LLM 推断，非编译器级确定性） | 强（编译器级） | **强（编译器级，取决于 Provider 覆盖率）** |
| **多模态支持** | 无 | 支持（代码+文档+PDF+图片） | 无 | 无 |
| **实时更新开销** | 极低（毫秒级） | 中（LLM 语义需重提取） | 较高（需重新构建） | **低（Tree-sitter 增量补丁热更新）** |
| **部署与使用门槛** | 极简 | 简单（pip install） | 复杂（Cloud 或 Self-hosted 集群） | **极简（单二进制，`amap install` 零配置）** |
| **MCP 原生支持** | GitNexus 16 工具 / CodeGraph 基础 | Skill 模式（/graphify 命令） | 依赖插件适配 | **原生内置 9 个 MCP 工具** |
| **本地可视化能力** | GitNexus 浏览器端 / CodeGraph 极简 | 交互式 graph.html | 偏网页代码搜索 | **内置星图 + 力导拓扑 Dashboard** |

> **客观定位**：如果你的项目规模小、语言动态且无复杂类型继承，纯 Tree-sitter 工具部署最快；如果你需要"理解代码为什么这样设计"而非精确调用拓扑，Graphify 的多模态语义图谱是独特选择；如果你需要企业级全仓库代码搜索与服务端协同，Sourcegraph 是行业标杆；而如果你需要在**本地开发环境中，同时拥有 SCIP 级精准度与 Tree-sitter 实时热更新，并为 AI Agent 提供低 Token 消耗的 MCP 接口**，AstraMap 是专为这一场景设计的方案。

---

## 九、3 条命令开启代码地图

无需手动修改 JSON 配置文件，无需云端服务绑定。AstraMap 的设计原则是**零额外配置、开箱即用**。

完成二进制构建与安装后：

```bash
# 步骤 1: 构建并安装 CLI（仅需执行一次）
./build.sh && install -m 755 ./amap ~/.local/bin/amap
```

进入目标项目根目录，**3 条命令**完成全套部署：

```bash
cd /path/to/your/project

# 1️⃣ 一键配置：自动探测本机 Claude Code / Cursor / VS Code / Windsurf 并写入 MCP
amap install

# 2️⃣ 一键构建：自动识别语言与工具链，生成 SQLite 语义代码地图
amap index

# 3️⃣ 一键增量：开启后台低开销持续同步（每 30 秒监听增量变更）
amap watch 30
```

> 运行 `amap dashboard`，浏览器打开 `http://localhost:3000` 即可在星图与力导向拓扑网中探索。

---

## 十、本地优先，隐私可控

- **全本地闭环**：代码读取、AST 解析与 SQLite 图谱生成全部在本地执行，数据保存在项目 `.astramap/` 目录中。
- **MCP 通信**：MCP Server 通过本地 `stdio` 与 AI 客户端通信，AstraMap 本身不上传源码到远程服务。
- **客户端侧**：AI 客户端是否将查询结果发送到远程模型，取决于客户端自身配置。

---

## 开源地址与社区

- **GitHub 仓库**：`https://github.com/SourceAstra/astra-code-map`
- **开源许可证**：Apache 2.0
- **项目状态**：活跃迭代中

---

> **代码地图的意义不是替代源码，也不是替代编译器，而是为 AI Agent 和工程师提供一张可查询、可追踪、可持续更新的代码导航底图。**
>
> 从 `amap index` 开始。
