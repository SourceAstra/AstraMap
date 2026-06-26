# AstraMap 部署指南

> 高精度语义代码地图引擎 — 从构建到生产环境的完整部署手册

---

## 目录

1. [环境要求](#1-环境要求)
2. [从源码构建](#2-从源码构建)
3. [索引构建](#3-索引构建)
4. [MCP Server 部署](#4-mcp-server-部署)
5. [Web Dashboard 部署](#5-web-dashboard-部署)
6. [CLI 诊断工具](#6-cli-诊断工具)
7. [生产环境部署](#7-生产环境部署)
8. [数据目录与存储](#8-数据目录与存储)
9. [故障排查](#9-故障排查)
10. [卸载与清理](#10-卸载与清理)
11. [附录](#附录-a完整配置参考)

---

## 1. 环境要求

### 1.1 编译环境

| 依赖 | 最低版本 | 说明 |
|------|---------|------|
| Go | 1.25+ | 编译工具链，`go.mod` 声明 `go 1.25.0` |
| GCC / Clang | 任意 | CGO 可能被 Tree-sitter 依赖触发 |
| Git | 2.0+ | `hotspots`、`owners`、`diff` 命令依赖 |

### 1.2 运行时环境

| 项目 | 要求 |
|------|------|
| 操作系统 | Linux (x86_64)、macOS (ARM64/x86_64)、Windows (WSL2) |
| 磁盘空间 | 索引数据库约为源码体积的 1.5–3 倍（10 万行项目约 12–20 MB） |
| 内存 | 最低 256 MB，大型项目（50 万行+）建议 1 GB |
| 网络 | MCP stdio 模式无需网络；Dashboard 模式需本地端口可用 |

### 1.3 支持的编程语言

| 语言 | Tree-sitter 解析 | SCIP 索引 |
|------|-----------------|-----------|
| Go | `tree-sitter-go` | `scip-go` |
| Python | `tree-sitter-python` | `scip-python` |
| TypeScript / TSX / JSX | `tree-sitter-typescript` | `scip-typescript` |
| C / C++ | `tree-sitter-cpp` | `scip-clang` |
| Java | `tree-sitter-java` | `scip-java` |

---

## 2. 从源码构建

### 2.1 获取源码

```bash
git clone <repository-url> astramap
cd astramap
```

### 2.2 编译二进制

```bash
# 标准编译（输出到 ./amap）
go build -o amap ./cmd/amap

# 验证构建结果
./amap
# 应输出帮助信息，列出所有子命令
```

编译产物为单个 ELF 可执行文件（Linux 约 26 MB），内嵌 Web Dashboard 静态资源（`//go:embed web/*`），**无外部运行时依赖**。

### 2.3 交叉编译

```bash
# macOS ARM64 (Apple Silicon)
GOOS=darwin GOARCH=arm64 go build -o amap-darwin-arm64 ./cmd/amap

# macOS x86_64 (Intel Mac)
GOOS=darwin GOARCH=amd64 go build -o amap-darwin-amd64 ./cmd/amap

# Linux ARM64 (服务器/树莓派)
GOOS=linux GOARCH=arm64 go build -o amap-linux-arm64 ./cmd/amap

# Windows (需 WSL 或交叉编译)
GOOS=windows GOARCH=amd64 go build -o amap.exe ./cmd/amap
```

### 2.4 安装到系统路径

```bash
# 方式一：手动复制
sudo cp ./amap /usr/local/bin/amap

# 方式二：加入 PATH（无需 root）
mkdir -p ~/bin
cp ./amap ~/bin/
echo 'export PATH="$HOME/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc

# 验证
which amap
amap
```

---

## 3. 索引构建

索引是 AstraMap 的核心数据，将源码解析为 SQLite 知识图谱。所有查询功能（MCP、Dashboard、CLI 诊断）均依赖索引。

### 3.1 一键索引（推荐）

`amap index` 自动检测项目语言，优先使用 SCIP 高精度索引，再用 Tree-sitter 补充：

```bash
# 在项目根目录执行（自动检测语言 → SCIP 生成 → 导入 → Tree-sitter 补充）
amap index

# 或指定项目路径
amap index --project /path/to/your/project
```

**自动检测逻辑**：

1. 扫描项目根目录的标志性文件（`go.mod`、`tsconfig.json`、`pyproject.toml` 等）
2. 在 `$PATH` / `$GOPATH/bin` 中查找对应的 SCIP 工具
3. 找到 → 自动生成 SCIP 索引 → 导入 → Tree-sitter 补充
4. 未找到 → 打印安装提示，回退到纯 Tree-sitter

**输出示例**（Go 项目，已安装 `scip-go`）：

```
检测到 go 项目，正在生成 SCIP 索引 (/home/you/go/bin/scip-go)...
正在导入 SCIP 索引: /path/to/project/.astramap/index.scip
SCIP 索引导入完成，正在用 Tree-sitter 补充...
索引构建完成！
```

**输出示例**（Go 项目，未安装 `scip-go`）：

```
检测到 go 项目，但未找到 scip-go，跳过 SCIP
  安装: go install github.com/sourcegraph/scip-go/cmd/scip-go@latest
索引构建完成！
```

**输出位置**：`<project_root>/.astramap/astramap.db`

### 3.2 索引模式与标志

| 标志 | 说明 |
|------|------|
| （无标志） | 自动检测语言，SCIP 优先 + Tree-sitter 补充 |
| `--scip` | 强制自动生成 SCIP 索引（高精度模式） |
| `--scip-file <path>` | 导入已有的 SCIP 索引文件 |
| `--treesitter-only` | 跳过 SCIP，仅使用 Tree-sitter 快速扫描 |

```bash
# 默认模式：自动检测，有 SCIP 工具就走双轨
amap index

# 强制 SCIP 自动生成（即使默认模式已自动检测，此标志显式要求）
amap index --scip

# 导入已有的 SCIP 索引文件（不自动生成）
amap index --scip-file /path/to/index.scip

# 仅 Tree-sitter（跳过 SCIP 检测，最快）
amap index --treesitter-only
```

### 3.3 SCIP 工具安装

各语言的 SCIP 索引工具需单独安装。安装后 `amap index` 自动发现并使用：

| 语言 | 工具 | 安装命令 | 检测标志文件 |
|------|------|---------|------------|
| Go | `scip-go` | `go install github.com/sourcegraph/scip-go/cmd/scip-go@latest` | `go.mod` |
| TypeScript | `scip-typescript` | `npm install -g @sourcegraph/scip-typescript` | `tsconfig.json` / `package.json` |
| Python | `scip-python` | `pip install scip-python` | `pyproject.toml` / `setup.py` |
| Java | `scip-java` | 参见 [scip-java 文档](https://github.com/sourcegraph/scip-java) | `pom.xml` |
| C/C++ | `scip-clang` | 参见 [scip-clang 文档](https://github.com/sourcegraph/scip-clang) | `CMakeLists.txt` |

**SCIP 特点**：
- 语义级精度（区分同名函数重载、泛型实例化）
- 边的 `provenance` 字段标记为 `scip`
- SCIP 边优先级高于 Tree-sitter 边（冲突时 SCIP 覆盖）

### 3.4 双轨融合规则

当 SCIP 和 Tree-sitter 同时索引同一项目时：

- 先导入 SCIP 高精度数据，再运行 Tree-sitter 补充
- 同源冲突时，SCIP 边优先于 Tree-sitter 边
- Tree-sitter 补充 SCIP 未覆盖的文件和符号
- 所有边通过 `provenance` 字段区分来源：`scip` / `tree-sitter` / `heuristic`
- 自动生成的临时 SCIP 文件（`.astramap/index.scip`）在导入后自动清理

### 3.5 增量更新

AstraMap 启动 MCP Server 时会自动触发增量同步（`SyncAllFilesAstraMap`）：

- 基于内容哈希（SHA-256）检测变更文件
- 仅重新解析哈希变化的文件
- 自动清理已删除文件的节点和边

手动触发增量更新：

```bash
amap index --project /path/to/project
```

### 3.6 验证索引健康度

```bash
# 通过 MCP 工具
# 调用 astramap_status

# 通过 CLI
amap query "SELECT COUNT(*) as nodes FROM astramap_nodes"
amap query "SELECT COUNT(*) as edges FROM astramap_edges"
amap query "SELECT COUNT(*) as files FROM astramap_files"

# QA 综合指标
amap qa
```

---

## 4. MCP Server 部署

MCP（Model Context Protocol）是 AstraMap 的核心服务模式，通过 stdio JSON-RPC 协议为 AI 编程代理提供代码地图查询。

### 4.1 手动启动

```bash
# 在项目根目录启动
amap serve --project /path/to/your/project

# 或使用当前目录
cd /path/to/your/project
amap serve
```

MCP Server 启动后会：
1. 自动触发增量文件同步
2. 进入 stdio JSON-RPC 循环，从 stdin 读取请求，向 stdout 写入响应
3. 所有日志输出到 stderr，不污染 stdio 协议通道

**重要**：MCP Server 设计为由宿主进程（Claude Code / Cursor）管理生命周期，不应手动长期运行。

### 4.2 一键安装（推荐）

```bash
# 在项目根目录执行
amap install

# 或指定项目路径
amap install --project /path/to/your/project

# 仅查看配置 JSON（不写入任何文件）
amap install --show-config
```

**前提条件**：
1. 已编译 `amap` 二进制（`go build -o amap ./cmd/amap`）
2. 二进制位于稳定路径（`install` 使用 `os.Executable()` 获取当前路径，如果后续移动了二进制，配置会失效）

**执行行为**（7 个目标逐一注册，互不阻塞）：

| 目标 | 注册内容 | 优先策略 | Fallback | 配置文件 |
|------|---------|---------|----------|---------|
| Claude Code | MCP + `/amap` 命令 | `claude mcp add --scope user` | 写入 `~/.claude.json` | `mcpServers` 键 |
| VS Code | MCP + Copilot 规则 | `code --add-mcp` | 写入 `.vscode/mcp.json` | `servers` 键 |
| Cursor | MCP + 规则文件 | 无 CLI | 写入 `~/.cursor/mcp.json` | `mcpServers` 键 |
| 项目共享 | `.mcp.json` | — | 写入项目根 `.mcp.json` | `mcpServers` 键 |
| Codex | MCP + `AGENTS.md` 规则 | `codex mcp add` | 写入 `~/.codex/config.toml` | `mcp_servers` 键 |
| Windsurf | `.windsurfrules` 规则 | — | 写入项目根 `.windsurfrules` | — |
| Cline | `.clinerules` 规则 | — | 写入 `.clinerules/astramap.md` | — |

**输出示例**：

```
正在注册 AstraMap MCP 服务与规则文件...

  ✓ Claude Code  — MCP 已注册 (user scope) + /amap 命令已就绪
  ✓ VS Code      — MCP 已注册 (code --add-mcp) + Copilot 规则已写入
  ✓ Cursor       — MCP 已写入 + 规则已注册 (.cursor/rules/astramap.mdc)
  ✓ 项目 .mcp.json — 已写入 /path/to/project/.mcp.json (团队成员自动可用)
  ✓ Codex         — MCP 已注册 + 规则已写入 AGENTS.md
  ✓ Windsurf      — 规则已写入 .windsurfrules
  ✓ Cline         — 规则已写入 .clinerules/astramap.md

安装完成！7/7 工具注册成功。

下一步：构建代码地图索引
  amap index                    # 自动检测语言，SCIP 优先 + Tree-sitter 补充
  amap index --scip             # 强制自动生成 SCIP 索引（高精度）
  amap index --treesitter-only  # 仅 Tree-sitter 快速扫描
```

**安全机制**：
- 每个工具独立 try/catch，单个失败不影响其他工具
- 写入前自动创建目标目录（如 `~/.cursor/` 不存在则创建）
- 写入前读取现有配置并合并（保留其他 MCP 服务器配置）
- JSON 损坏时自动备份为 `.bak` 后重建
- 规则文件采用追加模式：已有 AstraMap 段落则跳过，不覆盖其他内容
- 写入后验证 JSON 合法性
- 所有错误明确标记 `✗` 并给出原因

**`--project .` 的含义**：MCP Server 以宿主工具当前工作目录作为项目根目录。这意味着每次切换项目时，AstraMap 自动索引当前项目。

### 4.3 手动配置

如果 `amap install` 某个工具注册失败，可运行 `amap install --show-config` 查看各工具的配置 JSON，然后手动粘贴。

**Claude Code**（`~/.claude.json`）：

```json
{
  "mcpServers": {
    "astramap": {
      "command": "/home/you/bin/amap",
      "args": ["serve", "--project", "."],
      "env": {}
    }
  }
}
```

或使用 CLI：
```bash
claude mcp add --scope user astramap -- /path/to/amap serve --project /path/to/project
```

**VS Code**（`.vscode/mcp.json`，注意使用 `servers` 键而非 `mcpServers`）：

```json
{
  "servers": {
    "astramap": {
      "command": "/home/you/bin/amap",
      "args": ["serve", "--project", "."]
    }
  }
}
```

或使用 CLI：
```bash
code --add-mcp '{"name":"astramap","command":"/path/to/amap","args":["serve","--project","."]}'
```

**Cursor**（`~/.cursor/mcp.json`）：

```json
{
  "mcpServers": {
    "astramap": {
      "command": "/home/you/bin/amap",
      "args": ["serve", "--project", "${workspaceFolder}"]
    }
  }
}
```

`${workspaceFolder}` 是 Cursor 的变量，自动替换为当前工作区路径。

**Codex**（`~/.codex/config.toml`）：

```bash
# 使用 CLI 注册（推荐）
codex mcp add astramap -- /path/to/amap serve --project .
```

或手动编辑 `~/.codex/config.toml`：

```toml
[mcp_servers.astramap]
command = "/path/to/amap"
args = ["serve", "--project", "."]

[mcp_servers.astramap.tools.astramap_search]
approval_mode = "approve"

# ... 每个工具需单独设置 approval_mode = "approve"
```

Codex 要求为每个 MCP 工具显式设置 `approval_mode = "approve"`，否则每次调用都会弹出确认提示。

### 4.4 Claude Code `/amap` 斜杠命令

安装后 Claude Code 中可直接输入 `/amap` 触发代码地图查询：

```
/amap search QuerySearch
/amap explore "MCP Server 启动流程"
/amap callers go:astramap/service.go:QuerySearch
/amap impact go:astramap/service.go:QuerySearch
/amap trace main QuerySearch
/amap status
```

**实现原理**：`amap install` 在项目 `.claude/commands/amap.md` 中生成斜杠命令定义文件，包含 YAML frontmatter（`description`、`argument-hint`、`allowed-tools`）和 `$ARGUMENTS` 分发逻辑。该文件将子命令映射到对应的 MCP 工具调用。

### 4.5 AI 工具规则文件

`amap install` 不仅注册 MCP 服务，还为每个 AI 工具生成规则/指令文件，引导 AI 代理优先使用 AstraMap 而非 grep 或文件搜索。

**规则内容**（所有工具共享同一核心指令）：

```
AstraMap 是当前项目的代码地图 MCP 服务。当用户询问代码结构相关问题时，必须优先使用 AstraMap 工具而非 grep 或文件搜索：

- 查找符号定义 → astramap_search
- 理解代码上下文和调用关系 → astramap_explore
- 查看符号详情和源码 → astramap_node
- 追溯谁调用了某符号 → astramap_callers
- 追溯某符号调用了什么 → astramap_callees
- 评估修改影响范围 → astramap_impact
- 追踪 A 到 B 的调用路径 → astramap_trace
- 检查索引状态 → astramap_status
```

**各工具规则文件路径**：

| 工具 | 文件路径 | 格式 | 关键机制 |
|------|----------|------|---------|
| Claude Code | `.claude/commands/amap.md` | Markdown + YAML frontmatter | 斜杠命令 + `allowed-tools` |
| Cursor | `.cursor/rules/astramap.mdc` | MDC（`alwaysApply: true`） | 始终激活规则 |
| VS Code Copilot | `.github/copilot-instructions.md` | Markdown（追加段落） | 自动发现 |
| Codex | `AGENTS.md` + `~/.codex/config.toml` | Markdown + TOML MCP | `codex mcp add` CLI |
| Windsurf | `.windsurfrules` | Markdown | 自动发现 |
| Cline | `.clinerules/astramap.md` | Markdown | 自动发现 |

**追加行为**：对于 `AGENTS.md`、`.github/copilot-instructions.md`、`.windsurfrules`，若文件已存在则追加 AstraMap 段落（以标题分隔），不覆盖已有内容。若已包含 AstraMap 段落则跳过。

### 4.6 MCP 工具触发场景

每个 MCP 工具的 `description` 字段内置了触发场景指引，AI 代理可根据用户问题自动匹配：

| 工具 | 触发场景 |
|------|---------|
| `astramap_search` | 用户问「X 在哪定义」「找一下 Y 函数」 |
| `astramap_explore` | 用户描述业务流程或问「X 和 Y 是怎么关联的」 |
| `astramap_node` | 用户问「X 的源码是什么」「X 的签名和文档」 |
| `astramap_callers` | 用户问「谁调用了 X」「X 被哪些地方引用」 |
| `astramap_callees` | 用户问「X 依赖什么」「X 内部调用了什么」 |
| `astramap_impact` | 用户问「改了 X 会影响什么」 |
| `astramap_trace` | 用户问「从 A 到 B 的调用链是什么」「执行流如何到达 Y」 |
| `astramap_status` | 用户问「索引好了吗」「地图状态如何」 |
| `astramap_verdict` | 用户问「X 有没有代码质量问题」 |
| `astramap_files` | 用户问「项目有哪些文件」「某目录下有哪些源码」 |

### 4.7 多项目配置

如果需要同时为多个项目提供 AstraMap 服务，可以为每个项目注册独立的 MCP Server：

```json
{
  "mcpServers": {
    "astramap-project-a": {
      "command": "/home/you/bin/amap",
      "args": ["serve", "--project", "/home/you/projects/project-a"],
      "env": {}
    },
    "astramap-project-b": {
      "command": "/home/you/bin/amap",
      "args": ["serve", "--project", "/home/you/projects/project-b"],
      "env": {}
    }
  }
}
```

### 4.8 MCP 工具清单

安装成功后，AI 代理可调用以下 10 个工具：

| 工具名 | 功能 | 必需参数 |
|--------|------|---------|
| `astramap_search` | 按名称模糊检索符号 | `query` |
| `astramap_explore` | 区域性代码流探索，返回源码+拓扑 | `query` |
| `astramap_node` | 符号实体详情还原（含源码） | `symbol` |
| `astramap_callers` | 向上追溯直接上游调用者 | `symbol` |
| `astramap_callees` | 向下追溯直接下游被调用者 | `symbol` |
| `astramap_impact` | 变更影响波及评估（BFS 上游） | `symbol` |
| `astramap_status` | 索引覆盖率与健康状态 | 无 |
| `astramap_verdict` | 质量审计裁决与修复建议 | `symbolId` |
| `astramap_trace` | A→B 调用路径追踪 | `from`, `to` |
| `astramap_files` | 已索引文件列表 | 无 |

### 4.9 验证 MCP 连接

在 Claude Code 中启动后，可通过以下方式验证：

1. 调用 `astramap_status` 工具，应返回索引统计信息
2. 调用 `astramap_search` 并传入项目中的已知符号名
3. 检查 stderr 日志输出（Claude Code 的 MCP 日志面板）

---

## 5. Web Dashboard 部署

AstraMap 内置 D3.js 可视化控制台，提供交互式代码图谱浏览。

### 5.1 启动 Dashboard

```bash
# 默认端口 8585
amap dashboard --project /path/to/your/project

# 自定义端口
amap dashboard --project /path/to/your/project --port 9090
```

启动后访问 `http://127.0.0.1:8585`（或自定义端口）。

**注意**：Dashboard 仅绑定 `127.0.0.1`，不接受外部网络连接。

### 5.2 Dashboard 功能

- **搜索页**（`index.html`）：符号搜索 + D3.js 力导向图可视化
- **探索页**（`explore.js`）：区域性代码流探索，展示符号关系与源码
- **追踪页**（`trace.js`）：调用路径追踪可视化

### 5.3 REST API 端点

Dashboard 同时暴露以下 REST JSON API，可供外部工具集成：

| 端点 | 方法 | 参数 | 说明 |
|------|------|------|------|
| `/api/astramap/status` | GET | 无 | 索引状态统计 |
| `/api/astramap/search` | GET | `q`, `kind` | 符号搜索 |
| `/api/astramap/node/{id}` | GET | 路径参数 `id` | 节点详情 |
| `/api/astramap/callers/{id}` | GET | 路径参数 `id` | 上游调用者 |
| `/api/astramap/callees/{id}` | GET | 路径参数 `id` | 下游被调用者 |
| `/api/astramap/impact/{id}` | GET | 路径参数 `id`, `depth` | 影响分析 |
| `/api/astramap/explore` | GET | `q`, `maxFiles` | 区域探索 |
| `/api/astramap/trace` | GET | `from`, `to` | 路径追踪 |
| `/api/trace` | GET | `node_id`, `depth` | 拓扑子图（D3 格式） |

**示例请求**：

```bash
# 搜索符号
curl "http://127.0.0.1:8585/api/astramap/search?q=QuerySearch&kind=function"

# 查看节点详情
curl "http://127.0.0.1:8585/api/astramap/node/go:astramap/service.go:QuerySearch"

# 影响分析
curl "http://127.0.0.1:8585/api/astramap/impact/go:astramap/service.go:QuerySearch?depth=3"

# 调用路径追踪
curl "http://127.0.0.1:8585/api/astramap/trace?from=main&to=QuerySearch"
```

### 5.4 远程访问（可选）

Dashboard 默认仅监听 localhost。如需远程访问，建议通过 SSH 隧道：

```bash
# 在本地机器执行
ssh -L 8585:127.0.0.1:8585 user@remote-server

# 然后在本地浏览器访问 http://localhost:8585
```

**不建议**直接修改源码绑定 `0.0.0.0`，因为 Dashboard 无内置认证机制。

---

## 6. CLI 诊断工具

AstraMap 提供一系列命令行诊断工具，用于代码质量分析和架构洞察。

### 6.1 符号定位

```bash
# 快速定位符号定义的文件与行号
amap locate QuerySearch
# 输出: [function] astramap/service.go:42
```

### 6.2 变更影响分析

```bash
# 基于 git diff 分析修改影响面
amap diff

# 附带测试建议
amap diff --suggest-tests
```

### 6.3 代码热点

```bash
# 按变更频次排序的 Top 10 热点文件
amap hotspots --project /path/to/project
```

### 6.4 死代码检测

```bash
# 检测不可达的死代码函数
amap deadcode
```

### 6.5 循环依赖检测

```bash
# 文件级循环依赖
amap cycles

# 包级循环依赖（预留）
amap cycles --level=package
```

### 6.6 耦合度分析

```bash
# 模块 Ca/Ce 内聚耦合度
amap coupling --path=astramap/
```

### 6.7 代码所有权

```bash
# 基于 git blame 的符号级代码所有权
amap owners QuerySearch
```

### 6.8 调用拓扑树

```bash
# 向下展开调用树（默认）
amap tree QuerySearch --dir=down --depth=3

# 向上追溯调用者树
amap tree QuerySearch --dir=up --depth=2
```

### 6.9 拓扑导出

```bash
# 导出 Mermaid 格式调用图
amap export QuerySearch --format=mermaid
```

### 6.10 质量审计

```bash
# 扫描 Verdicts 缺陷，有缺陷则退出码为 1
amap audit
# 可用于 CI/CD 门禁
```

### 6.11 QA 综合指标

```bash
# 输出项目级质量大盘
amap qa
# 包含：节点/边/文件总数、死代码数、循环依赖数、调用覆盖率、健康度评分、综合评级
```

### 6.12 SQL 直接查询

```bash
# 直接查询底层 SQLite 图数据库
amap query "SELECT name, kind, file_path FROM astramap_nodes WHERE kind='function' LIMIT 10"
```

---

## 7. 生产环境部署

### 7.1 Systemd 服务（Dashboard 模式）

为团队共享的 Web Dashboard 创建 systemd 服务：

```ini
# /etc/systemd/system/astramap-dashboard.service
[Unit]
Description=AstraMap Dashboard
After=network.target

[Service]
Type=simple
User=developer
WorkingDirectory=/home/developer/projects/your-project
ExecStart=/usr/local/bin/amap dashboard --project /home/developer/projects/your-project --port 8585
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable astramap-dashboard
sudo systemctl start astramap-dashboard

# 查看日志
sudo journalctl -u astramap-dashboard -f
```

### 7.2 Nginx 反向代理

为 Dashboard 添加 HTTPS 和基本认证：

```nginx
server {
    listen 443 ssl;
    server_name astramap.your-company.com;

    ssl_certificate     /etc/ssl/certs/astramap.crt;
    ssl_certificate_key /etc/ssl/private/astramap.key;

    location / {
        proxy_pass http://127.0.0.1:8585;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # WebSocket 支持（预留）
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

### 7.3 CI/CD 集成

在 CI 管道中使用 AstraMap 进行质量门禁：

```yaml
# GitHub Actions 示例
name: AstraMap Quality Gate

on: [push, pull_request]

jobs:
  quality-gate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0  # git blame 需要

      - name: Install AstraMap
        run: |
          curl -sL https://your-releases/astramap/latest/amap-linux-amd64 -o amap
          chmod +x amap
          sudo mv amap /usr/local/bin/

      - name: Build Index
        run: amap index --project .
        # 自动检测语言，SCIP 优先 + Tree-sitter 补充
        # 如需强制 SCIP: amap index --scip --project .
        # 如仅需 Tree-sitter: amap index --treesitter-only --project .

      - name: Audit Gate
        run: amap audit
        # 有未修复缺陷时退出码为 1，CI 失败

      - name: QA Report
        run: amap qa
        # 输出质量大盘，可归档为 artifact
```

### 7.4 定时索引更新

对于大型项目，建议定时重建索引以保持数据新鲜度：

```bash
# 通过 crontab 定时更新（每小时）
crontab -e
# 添加：
0 * * * * /usr/local/bin/amap index --project /path/to/project --treesitter-only >> /var/log/astramap-index.log 2>&1
```

### 7.5 Docker 部署

```dockerfile
FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git gcc musl-dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -o /amap ./cmd/amap

FROM alpine:3.20
RUN apk add --no-cache ca-certificates git
COPY --from=builder /amap /usr/local/bin/amap
EXPOSE 8585
VOLUME /project
ENTRYPOINT ["amap"]
CMD ["dashboard", "--project", "/project", "--port", "8585"]
```

```bash
# 构建镜像
docker build -t astramap .

# 运行 Dashboard
docker run -d \
  --name astramap \
  -p 8585:8585 \
  -v /path/to/your/project:/project:ro \
  astramap

# 构建索引（自动检测语言，SCIP 优先）
docker run --rm \
  -v /path/to/your/project:/project \
  -v /path/to/your/project/.astramap:/project/.astramap \
  astramap index --project /project

# 运行 MCP Server（供 Claude Code 调用）
docker run --rm -i \
  -v /path/to/your/project:/project \
  -v /path/to/your/project/.astramap:/project/.astramap \
  astramap serve --project /project
```

---

## 8. 数据目录与存储

### 8.1 目录结构

每个项目的索引数据存放在项目根目录下的 `.astramap/` 目录中：

```
your-project/
├── .astramap/
│   ├── astramap.db          # 主 SQLite 数据库
│   ├── astramap.db-wal      # WAL（Write-Ahead Log）文件
│   └── astramap.db-shm      # 共享内存文件
├── src/
├── go.mod
└── ...
```

**注意**：`--scip` 自动生成模式会在 `.astramap/` 下临时创建 `index.scip` 文件，导入完成后自动清理。

### 8.2 数据库配置

- **WAL 模式**：默认启用 `journal_mode=WAL`，支持并发读写
- **同步模式**：`synchronous=NORMAL`，平衡性能与安全
- **单写入者**：`SetMaxOpenConns(1)`，保证 SQLite 写入安全
- **外键约束**：`PRAGMA foreign_keys = ON`，边的 `source`/`target` 必须引用 `astramap_nodes` 中的有效节点。SCIP 导入会自动为外部依赖（`external:*`）创建占位节点以满足约束。

### 8.3 数据库表结构

| 表名 | 说明 |
|------|------|
| `astramap_nodes` | 符号节点（函数、类、结构体等） |
| `astramap_edges` | 关系边（calls、contains、imports 等） |
| `astramap_files` | 文件跟踪（路径、哈希、语言、节点数） |
| `astramap_verdicts` | 质量审计裁决（缺陷标记、修复建议） |
| `astramap_fts` | FTS5 全文搜索虚拟表（自动同步） |

### 8.4 索引与触发器

- **性能索引**：`kind`、`name`、`qualified_name`、`file_path`、`lower(name)`、`source+kind`、`target+kind`、唯一边约束
- **FTS 同步触发器**：`INSERT`/`DELETE`/`UPDATE` 时自动同步 `astramap_nodes` 到 `astramap_fts`

### 8.5 Node ID 命名方案

| 来源 | ID 格式 | 示例 |
|------|---------|------|
| SCIP | SCIP 原始符号字符串 | `scip python pytorch pytorch nn utils rnn.py`/`pack_padded_sequence()` |
| Tree-sitter | `<lang>:<rel_path>::<qualified_name>` | `go:astramap/service.go:QuerySearch` |
| 合成节点 | `<type>:<path>` | `file:astramap/service.go`、`route:/api/search`、`import:fmt` |
| 外部依赖 | `external:<symbol>` | `external:fmt.Println`（SCIP 导入自动创建的占位节点，kind=`external`） |

### 8.6 磁盘空间估算

| 项目规模 | 源码大小 | 索引数据库大小 | 索引时间 |
|----------|---------|-------------|---------|
| 1 万行 | ~1 MB | ~2–4 MB | < 5 秒 |
| 10 万行 | ~10 MB | ~12–20 MB | 10–30 秒 |
| 50 万行 | ~50 MB | ~50–100 MB | 1–3 分钟 |
| 100 万行 | ~100 MB | ~100–250 MB | 3–8 分钟 |

### 8.7 .gitignore 配置

```gitignore
# AstraMap 索引数据（二进制文件，不应入库）
.astramap/

# AstraMap 安装生成的规则文件（由 amap install 管理，可选入库）
# .cursor/rules/astramap.mdc
# .clinerules/astramap.md
# .windsurfrules
```

索引数据库可在任何时刻通过 `amap index` 重建，无需版本控制。

---

## 9. 故障排查

### 9.1 索引为空

**症状**：`amap qa` 显示节点数为 0

**排查步骤**：

1. 确认项目路径正确：
   ```bash
   amap index --project /absolute/path/to/project
   ```

2. 确认项目包含支持的文件类型：
   ```bash
   find /path/to/project -name "*.go" -o -name "*.py" -o -name "*.ts" | head
   ```

3. 检查 `.astramap/` 目录是否创建：
   ```bash
   ls -la /path/to/project/.astramap/
   ```

4. 通过 MCP 检查索引状态：
   ```
   astramap_status
   ```

### 9.2 MCP Server 连接失败

**症状**：Claude Code 提示 MCP 工具不可用

**排查步骤**：

1. 确认 `amap` 二进制路径在 Claude Code 配置中正确：
   ```bash
   which amap
   # 输出路径应与 config.json 中 command 字段一致
   ```

2. 手动测试 MCP Server：
   ```bash
   echo '{"jsonrpc":"2.0","method":"initialize","id":1}' | amap serve --project /path/to/project
   # 应输出 JSON-RPC 响应
   ```

3. 检查 Claude Code 的 MCP 日志面板

4. 确认 `amap` 有执行权限：
   ```bash
   chmod +x /path/to/amap
   ```

### 9.3 Dashboard 无法访问

**症状**：浏览器无法打开 `http://127.0.0.1:8585`

**排查步骤**：

1. 确认端口未被占用：
   ```bash
   ss -tlnp | grep 8585
   ```

2. 使用其他端口：
   ```bash
   amap dashboard --project /path/to/project --port 9090
   ```

3. 检查 stderr 日志输出

### 9.4 索引数据过时

**症状**：搜索结果与源码不匹配

**解决方案**：

```bash
# 重新构建索引（增量更新）
amap index --project /path/to/project

# 完全重建（删除旧索引）
rm -rf /path/to/project/.astramap/
amap index --project /path/to/project
```

### 9.5 SQLite 锁定错误

**症状**：`database is locked` 错误

**原因**：多个进程同时写入 SQLite 数据库

**解决方案**：

1. 确保同一时间只有一个进程执行 `amap index`
2. MCP Server 在启动时执行增量同步，期间不要同时运行 `amap index`
3. 检查是否有残留的 `astramap.db-wal` 文件，如有异常可安全删除

### 9.6 日志查看

所有 AstraMap 日志输出到 **stderr**：

```bash
# Dashboard 模式 — 直接查看终端输出
amap dashboard --project /path/to/project 2>&1 | tee astramap.log

# MCP 模式 — Claude Code 的 MCP 日志面板
# 或手动测试
amap serve --project /path/to/project 2>astramap-stderr.log
```

日志级别：
- `[INFO]` — 正常操作信息
- `[WARN]` — 非致命警告
- `[ERROR]` — 需要关注的错误

---

## 10. 卸载与清理

### 10.1 移除 MCP 配置

手动编辑 Claude Code 配置文件，删除 `astramap` 条目：

- Linux: `~/.config/claude/config.json`
- macOS: `~/Library/Application Support/claude/config.json`

移除 `"astramap": { ... }` 段落。

对于 Cursor，同理编辑 `~/.cursor/mcp.json`。

### 10.2 移除规则文件

```bash
# Claude Code 斜杠命令
rm .claude/commands/amap.md

# Cursor 规则
rm .cursor/rules/astramap.mdc

# VS Code Copilot 指令（需手动编辑，删除 AstraMap 段落）
# 编辑 .github/copilot-instructions.md

# Codex MCP + 规则
codex mcp remove astramap
# 编辑 AGENTS.md，删除 AstraMap 段落

# Windsurf 规则
rm .windsurfrules

# Cline 规则
rm .clinerules/astramap.md

# 项目共享 MCP 配置
rm .mcp.json
```

### 10.3 删除索引数据

```bash
# 删除单个项目的索引
rm -rf /path/to/project/.astramap/

# 批量删除所有项目的 .astramap 目录
find /home/you/projects -name ".astramap" -type d -exec rm -rf {} +
```

### 10.4 卸载二进制

```bash
# 如果安装到系统路径
sudo rm /usr/local/bin/amap

# 如果安装到用户目录
rm ~/bin/amap
```

---

## 附录 A：完整配置参考

### A.1 Claude Code MCP 配置

```json
{
  "mcpServers": {
    "astramap": {
      "command": "/absolute/path/to/amap",
      "args": ["serve", "--project", "."],
      "env": {}
    }
  }
}
```

| 字段 | 说明 |
|------|------|
| `command` | `amap` 二进制的绝对路径，必须可执行 |
| `args[0]` | 固定为 `"serve"` |
| `args[1]` | 固定为 `"--project"` |
| `args[2]` | `"."` 表示使用 Claude Code 当前工作目录；也可硬编码为项目绝对路径 |
| `env` | 环境变量注入（当前无必需变量） |

### A.2 Cursor MCP 配置

```json
{
  "mcpServers": {
    "astramap": {
      "name": "astramap",
      "type": "command",
      "command": "/absolute/path/to/amap",
      "args": ["serve", "--project", "${workspaceFolder}"],
      "isOn": true
    }
  }
}
```

| 字段 | 说明 |
|------|------|
| `name` | 显示名称 |
| `type` | 固定为 `"command"` |
| `command` | `amap` 二进制绝对路径 |
| `args` | `${workspaceFolder}` 为 Cursor 内置变量 |
| `isOn` | `true` 启用，`false` 禁用 |

### A.3 CLI 命令速查

```
核心服务:
  amap serve [--project <path>]                    MCP stdio 服务
  amap dashboard [--project <path>] [--port <N>]   Web 可视化控制台
  amap index [--project <path>] [--scip|--scip-file <path>|--treesitter-only]  构建/更新索引
  amap install                                     一键注册 MCP + 规则文件到 Claude Code / VS Code / Cursor / Codex / Windsurf / Cline / 项目 .mcp.json

诊断工具:
  amap locate <symbol>                             符号定位
  amap diff [--suggest-tests]                      变更影响分析
  amap hotspots [--project <path>]                 代码热点
  amap deadcode                                    死代码检测
  amap cycles                                      循环依赖检测
  amap coupling [--path=<prefix>]                  耦合度分析
  amap owners <symbol>                             代码所有权
  amap tree <symbol> [--dir=up|down] [--depth=N]   调用拓扑树
  amap export <symbol> [--format=mermaid]          拓扑导出
  amap audit                                       质量审计门禁
  amap qa                                          QA 综合指标
  amap query "<SQL>"                               SQL 直接查询

预留命令:
  amap repl                                        交互式 Shell（未实现）
  amap lsp                                         LSP 桥接（未实现）
  amap review                                      智能审查（需 SourceAstra）
  amap repair                                      智能修复（需 SourceAstra）
  amap test-gen <symbol>                           测试生成（需 SourceAstra）
```

---

## 附录 B：安全注意事项

1. **MCP stdio 模式**：仅通过 stdin/stdout 通信，无网络暴露，安全性由宿主进程保证
2. **Dashboard 模式**：默认绑定 `127.0.0.1`，无内置认证。远程访问必须通过 SSH 隧道或反向代理+认证
3. **SQL 查询**：`amap query` 允许执行任意 SQL，仅限可信用户使用
4. **索引数据**：`.astramap/astramap.db` 包含源码结构和部分代码内容（docstring、signature），属于项目机密级别，不应公开暴露
5. **文件访问**：MCP Server 的 `--project` 参数决定了可访问的文件范围，确保指向正确项目

---

## 附录 C：性能调优

### C.1 大型项目优化

对于 50 万行以上的项目：

1. **优先使用 SCIP 模式**：`amap index` 默认自动检测并使用 SCIP，无需手动指定。SCIP 索引提供更精确的跨文件调用关系，减少 heuristic 误报
2. **避免频繁全量重建**：增量更新（`amap index`）仅扫描变更文件，速度快且安全
3. **关闭不必要的 FTS**：如不需要全文搜索功能，可考虑在 `schema.go` 中注释掉 FTS 触发器

### C.2 MCP Server 性能

- `astramap_search`：基于 SQLite 索引，查询延迟 < 10ms
- `astramap_explore`：涉及源码读取，延迟约 50–200ms
- `astramap_impact`：BFS 遍历，深度 3 通常 < 100ms
- `astramap_trace`：BFS 路径搜索，延迟取决于图密度

### C.3 SQLite WAL 调优

当前默认配置已针对单写入者场景优化：

```
journal_mode=WAL          # 并发读写
synchronous=NORMAL        # 平衡性能与安全
MaxOpenConns=1            # 单写入者保证
foreign_keys=ON           # 数据完整性
```

如需进一步调优，可在 `getAstraMapDB()` 中修改 PRAGMA 参数。
