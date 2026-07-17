# AstraMap CLI 命令行架构分析

> 基于 `~/.claude/CLAUDE.md` 第一性原理与代码哲学的深度审视

---

## 一、当前命令全景

### 1.1 核心服务命令

| 命令 | 参数 | 功能 |
|------|------|------|
| `serve` | — | 启动 MCP stdio JSON-RPC 服务 |
| `dashboard` | `[--host]` `[--port]` `[--foreground]` | 启动 Web Dashboard（默认后台运行） |
| `index` | `[--lang]` `[--scip]` `[--scip-only]` `[--refresh-scip]` `[--full]` `[--tree-sitter]` `[--watch [秒]]` | 构建/更新代码地图索引 |
| `watch` | `[秒]` | 持续监听文件变更并自动增量索引（默认 10 秒防抖） |
| `install` | `[--show-config]` | 一键注册 MCP 到 Claude Code / VS Code / Cursor / Codex / Windsurf / Cline / Antigravity |
| `language` | `<action>` | 语言包管理子命令 |

### 1.2 语言包管理子命令

| 子命令 | 参数 | 功能 |
|--------|------|------|
| `install` | `[--scope user\|project]` `[--allow-unsigned]` `[--trust-key]` `[--catalog]` `<id\|path\|url>` | 安装签名语言包 |
| `update` | 同 install | 更新语言包 |
| `list` | `[--scope user\|project]` `[--json]` | 列出已安装语言包 |
| `doctor` | `[--scope]` `<id>` | 诊断语言包状态 |
| `enable` | `[--scope]` `<id>` `<version>` | 启用指定版本语言包 |
| `disable` | `[--scope]` `<id>` | 禁用语言包 |
| `remove` | `[--scope]` `<id>` `<version>` | 移除指定版本语言包 |

### 1.3 诊断工具命令

| 命令 | 参数 | 功能 |
|------|------|------|
| `diff` | `[--suggest-tests]` | 基于 git diff 分析变更影响范围 |
| `locate` | `<symbol>` | 符号定位（文件+行号） |
| `hotspots` | — | 代码热点 Top 10（git 提交频率 + 函数密度） |
| `deadcode` | — | 死代码检测（不可达函数分析） |
| `cycles` | — | 文件级循环依赖检测 |
| `coupling` | `[--path=...]` | 模块 Ca/Ce 耦合度与架构不稳定性分析 |
| `owners` | `<symbol>` | 代码所有权（git blame 贡献度统计） |
| `query` | `"<SQL>"` | SQL 直接查询底层图谱 |
| `tree` | `<symbol>` `[--dir=up\|down]` `[--depth=N]` | 终端调用拓扑树绘制 |

---

## 二、违反"对称之美 (Symmetry)"的项

### 2.1 `serve` vs `dashboard` 前后台模式不对称

```go
// serve: 直接启动，无后台模式
func serveCmd() { ... }

// dashboard: 默认后台，需 --foreground 才前台
func dashboardCmd() { ... }
```

**问题**：`serve` 无后台能力，`dashboard` 默认后台。两者行为模式不镜像。

**优化**：统一为两种模式均支持前台/后台，或统一默认前台。推荐后者——CLI 工具默认前台运行是 Unix 惯例，后台化由用户用 `&` 或 systemd 决定。

### 2.2 `index` 与 `watch` 职责重叠

```go
case "index": indexCmd()
case "watch": watchCmd()
```

`index` 支持 `--watch` 参数，`watch` 又独立成命令。两者均能触发持续监听，形成功能重叠。

**优化**：`watch` 作为 `index --watch` 的语法糖，而非独立命令。或彻底分离：`index` 仅单次，`watch` 仅守护。

### 2.3 资源获取与释放不对称

```go
db, err := getAstraMapDB(projectRoot)
if err != nil { ... }
defer db.Close()
```

此模式在每个命令函数中重复出现，但无统一封装。有获取必有释放的对称性仅通过 `defer` 保证，缺乏结构化的生命周期管理。

**优化**：引入 `withAstraMapDB(fn func(*sqlx.DB))` 高阶函数，强制对称：

```go
func withAstraMapDB(projectRoot string, fn func(*sqlx.DB)) {
    db, err := getAstraMapDB(projectRoot)
    if err != nil { ... }
    defer db.Close()
    fn(db)
}
```

---

## 三、违反"简单之道与零二义性"的项

### 3.1 `switch` 巨型分发器

```go
switch subcmd {
case "serve": serveCmd()
case "dashboard": dashboardCmd()
case "index": indexCmd()
// ... 15+ 个 case
}
```

**问题**：命令注册是硬编码枚举，新增命令需修改 `switch` 和 `printHelp()` 两处。违背"零二义性"——命令元数据分散在代码各处。

**优化**：命令注册表化，单一定义消除二义性：

```go
type Command struct {
    Name        string
    Description string
    Run         func([]string)
}

var commands = map[string]Command{
    "serve":     {Name: "serve", Description: "Launch stdio MCP server", Run: serveCmd},
    "dashboard": {Name: "dashboard", Description: "Start Web console", Run: dashboardCmd},
    // ...
}
```

### 3.2 `index` 参数语义冲突

```go
--scip-only        // 仅 SCIP
--refresh-scip     // 强制刷新 SCIP
--full             // 全量刷新 SCIP + Tree-sitter
--tree-sitter      // 仅 Tree-sitter
```

四个布尔标志存在交集和冲突（如 `--scip-only` + `--tree-sitter` 无意义）。运行时未校验互斥性。

**优化**：收敛为单一模式参数：

```go
--mode=scip-only      // 仅 SCIP
--mode=scip-refresh   // 强制刷新 SCIP
--mode=full           // 全量
--mode=treesitter     // 仅 Tree-sitter
--mode=incremental    // 默认：增量
```

### 3.3 `--project` 全局参数的特殊处理

```go
var projectRoot string
func stripProjectArg() { ... }
```

`stripProjectArg` 在 `main()` 之前修改 `os.Args`，副作用不可见。`--project` 的处理与其他参数不一致。

**优化**：使用标准 `flag` 包或 `cobra`/`urfave/cli` 统一处理全局参数，消除副作用。

---

## 四、违反"奥卡姆剃刀与极致收敛"的项

### 4.1 重复的数据库连接逻辑

每个命令函数均包含：

```go
db, err := getAstraMapDB(projectRoot)
if err != nil { ... }
defer db.Close()
```

**优化**：如上 `withAstraMapDB` 封装，或命令注册时自动注入。

### 4.2 `printHelp()` 与代码不同步

`printHelp()` 是硬编码字符串，命令实现变更后帮助文本易过时。

**优化**：从命令注册表自动生成帮助文本，单点维护。

### 4.3 语言选择交互逻辑过度复杂

```go
if opts.langFlag == "" {
    if saved, ok := loadSavedIndexLanguages(...); ok {
        selected = saved
        // ...
    }
}
if len(selected) == 0 {
    detected = detectProjectLanguages(...)
}
if len(selected) == 0 && opts.langFlag != "" {
    selected = resolveLangNames(opts.langFlag, detected)
}
// 更多分支...
```

四层嵌套条件判断语言选择逻辑，状态机隐式。

**优化**：显式状态机或管道模式：

```go
selected := pipeline(
    tryLoadSavedLanguages,
    tryDetectProjectLanguages,
    tryResolveLangFlag,
    interactiveSelect,
)
```

---

## 五、违反"接口契约优先"的项

### 5.1 命令函数签名不统一

```go
func serveCmd()                    // 无参，读全局 os.Args
func indexCmd()                    // 无参，读全局变量
func watchCmd()                    // 无参
func diffCmd()                      // 无参
func locateCmd()                    // 无参，读 os.Args[2]
```

**问题**：命令函数无统一接口，依赖全局状态（`os.Args`、`projectRoot`），不可测试、不可组合。

**优化**：统一契约：

```go
type CommandContext struct {
    ProjectRoot string
    Args        []string
    Stdout      io.Writer
    Stderr      io.Writer
}

type CommandFunc func(ctx CommandContext) error
```

### 5.2 `language` 子命令与其他命令层级不一致

`language` 是二级命令（`amap language install`），但实现方式与一级命令相同（`languageCmd()` 内再 `switch`）。层级感知不一致。

**优化**：统一为命令树结构，无论层级深度均走同一套路由机制。

---

## 六、违反"扩展点预埋"的项

### 6.1 命令注册无 Hook 点

新增命令必须修改 `main()` 的 `switch`，无法通过插件或配置扩展。

**优化**：命令注册表支持运行时注入，预留插件 Hook：

```go
type Plugin interface {
    Name() string
    Commands() []Command
}

func RegisterPlugin(p Plugin) { ... }
```

### 6.2 `install` 命令硬编码 7 个 IDE

```go
if probes["Claude Code"] { ... }
if probes["VS Code"] { ... }
// ... 7 个
```

新增 IDE 需修改源码。

**优化**：IDE 注册表化，配置文件驱动：

```go
var ideRegistry = map[string]IDEInstaller{
    "claude":     claudeInstaller{},
    "vscode":     vscodeInstaller{},
    // ...
}
```

---

## 七、违反"数据模型向前兼容"的项

### 7.1 `savedIndexState` 结构无版本

```go
type savedIndexState struct {
    Languages []string `json:"languages"`
    UpdatedAt int64    `json:"updated_at"`
}
```

未来新增字段将导致旧版本无法读取。

**优化**：增加版本字段：

```go
type savedIndexState struct {
    Version   int      `json:"version"`
    Languages []string `json:"languages"`
    UpdatedAt int64    `json:"updated_at"`
}
```

### 7.2 配置解析手写 YAML

```go
func readIndexLanguagesFromConfig(projectRoot string) ([]string, bool) {
    // 逐行解析 YAML...
}
```

手写解析器脆弱，新增配置项需修改解析逻辑。

**优化**：使用 `gopkg.in/yaml.v3` 标准库，结构体映射。

---

## 八、优化方案汇总

| 原则 | 当前问题 | 优化方向 |
|------|---------|---------|
| **对称之美** | serve/dashboard 前后台不一致；资源获取释放分散 | 统一生命周期管理；默认前台运行 |
| **零二义性** | 巨型 switch；参数语义冲突 | 命令注册表；单模式参数 |
| **奥卡姆剃刀** | 重复 DB 连接；help 与代码不同步 | 高阶函数封装；自动生成 help |
| **接口契约** | 命令函数无统一签名；依赖全局状态 | `CommandContext` 统一注入 |
| **扩展点预埋** | 命令/IDE 硬编码 | 注册表 + 插件接口 |
| **向前兼容** | 状态无版本；手写 YAML 解析 | 版本字段；标准库解析 |

---

## 九、重构后的命令注册表示例

```go
type Command struct {
    Name        string
    Description string
    Usage       string
    Flags       []Flag
    Run         CommandFunc
}

var commands = []Command{
    {
        Name:        "serve",
        Description: "Launch stdio MCP server",
        Run: func(ctx CommandContext) error {
            return withDB(ctx.ProjectRoot, func(db *sqlx.DB) {
                astramap.RunMcpServer(db, ctx.ProjectRoot)
            })
        },
    },
    // ...
}
```

`main()` 简化为：

```go
func main() {
    cmd := findCommand(os.Args[1])
    if cmd == nil {
        printHelp(commands)
        os.Exit(1)
    }
    ctx := CommandContext{
        ProjectRoot: resolveProjectRoot(),
        Args:        os.Args[2:],
        Stdout:      os.Stdout,
        Stderr:      os.Stderr,
    }
    if err := cmd.Run(ctx); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
```

此架构满足所有原则：对称（统一生命周期）、零二义（注册表单点）、收敛（消除重复）、契约（统一接口）、扩展（运行时注册）、兼容（版本化状态）。
