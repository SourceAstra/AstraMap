# AstraMap 2 分钟部署指南

> 从零到 AI 工具集成代码地图，只需五步。

---

## 第一步：安装并加入 PATH

```bash
mkdir -p ~/bin
cp ./amap ~/bin
chmod 777 ~/bin/amap
grep -qxF 'export PATH="$HOME/bin:$PATH"' ~/.bashrc || echo 'export PATH="$HOME/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

验证安装：

```bash
which amap
# 应输出: /home/you/bin/amap
```

## 第二步：配置 ~/.bashrc 达到无感降本工作流

把下面这段追加到 `~/.bashrc`。它的目标是：平时先刷新一次代码地图，然后后台静默保活一个低频 watch；遇到问题时，再单独开前台 watch 看变化。

```bash
# AstraMap local binary
export PATH="$HOME/bin:$PATH"

# AI coding runtime defaults
export CLAUDE_CODE_EFFORT_LEVEL=high
export DISABLE_TELEMETRY=1

# 平时：静默版。先更新一次代码地图，再后台静默保活，最后进入 Claude
alias cc='amap index && (amap watch 30 >/dev/null 2>&1 &); claude'
```

排障时，改用下面这组双窗口命令：

```bash
# 排障：双窗口。前台手动看增量更新日志，能直接看到哪些文件被刷新
alias cc='amap index && claude'
alias amapw='amap watch 30'
```

立刻生效：

```bash
source ~/.bashrc
```

推荐终端分屏：

```bash
# 窗口 1：持续低频增量刷新代码地图
amapw

# 窗口 2：先刷新索引，再启动 AI 编码工具
cc
```

风险提示：

- 不要长期同时保留多个 `amap watch 30`
- 如果后台 watch 和前台 watch 同时跑，会重复扫描同一个索引目录，终端日志会重复，数据库写入也会变多
- `cc` 里的后台 watch 会静默常驻；如果你只想排障查看更新，临时切到双窗口版再开 `amapw`

## 第三步：进入目标项目，一键注册

```bash
cd /path/to/your/project

# 先探测当前机器上可用的 IDE / 客户端，再只注册探测到的项
amap install
```

执行时会先列出可用的客户端，比如 `Claude Code`、`VS Code`、`Cursor`、`Codex`、`Windsurf`、`Antigravity`。没有探测到的目标会跳过，不再无脑写一遍所有配置。

注册成功后输出：

```
  ✓ Claude Code  — MCP 已注册 (user scope) + /amap 命令已就绪
  ✓ VS Code      — MCP 已注册 (code --add-mcp) + Copilot 规则已写入
  ✓ Cursor       — MCP 已写入 + 规则已注册 (.cursor/rules/astramap.mdc)
  ✓ 项目 .mcp.json — 已写入 (团队成员自动可用)
  ✓ Codex         — MCP 已注册 + 规则已写入 AGENTS.md
  ✓ Windsurf      — 规则已写入 .windsurfrules
  ✓ Cline         — 规则已写入 .clinerules/astramap.md
  ✓ Antigravity  — MCP 已注册 (已写入 .agents/mcp_config.json, ~/.gemini/config/mcp_config.json, ~/.gemini/antigravity-cli/mcp_config.json) + 规则已追加写入 AGENTS.md

安装完成！N/M 工具注册成功。
```

紧接着还会有一段 `注册核验`，逐项检查实际落盘文件，确认当前注册状态是否真的生效。

## 第四步：构建索引

```bash
# 默认模式：首次初始化 SCIP；之后快速增量更新，复用 config.yaml 里的语言选择
amap index

# 指定仅导入某语言（跳过交互选择）
amap index --lang go

# 仅执行 Tree-sitter 语法层，跳过 SCIP 生成与导入
amap index --tree-sitter

# 仅导入 SCIP，跳过可选 Syntax Overlay
amap index --scip-only

# 强制刷新 SCIP 层
amap index --refresh-scip

# 全量刷新一次
amap index --full

# 启动时索引一次，然后在后台静默30 秒最多增量刷新一次
amap index --watch 30

# 前台增量刷新一次默认 10 秒；可看到哪些文件被更新，也可写 amap watch 30，即30s
amap watch
```

语言选择会写入 `.astramap/config.yaml` 的 `index.languages`。第二次普通 `amap index` 会复用该配置并静默执行。

索引输出示例（Go 项目）：

```
检测到以下语言文件:
  1. Go (42 个源文件)

将导入语言: Go
检测到 Go 项目，正在生成 SCIP 索引 (/home/you/go/bin/scip-go)...
正在导入 SCIP 索引: /path/to/project/.astramap/index-go.scip
SCIP 索引导入完成

── 索引来源统计 ──
  节点 (按语言): Go=356 (合计=356)
  边   (按来源): scip=892, syntax-package=41, heuristic=23 (合计=956)

索引构建完成！
```

## 第五步：启动可视化控制台

```bash
amap dashboard
```

输出：

```
AstraMap Dashboard started in background
Host: 0.0.0.0
Port: 3000
Local: http://localhost:3000
LAN: http://192.168.1.100:3000
PID: 12345
Log: /path/to/project/.astramap/dashboard.log
```

浏览器访问 `http://localhost:3000` 即可使用探索视界和依赖分析功能。

---

## 工作原理

```
源码 → 内置 Tree-sitter 实时层 + SCIP 最终语义 → SQLite 知识图谱 → MCP/HTTP API → 本地可视化与工具客户端
```

## 核心优势

- **95%+ 语义精度** — SCIP 编译器级索引，区分重载/泛型
- **60-95% Token 节约** — 单次调用替代多次 grep+Read
- **确定性持续更新** — `amap watch [秒数]` 或 `amap index --watch [秒数]` 先实时提交 Tree-sitter 结果，再刷新 SCIP 收敛跨文件语义

## 工具对比

| 维度 | CodeGraph | GitNexus | Graphify | AstraMap |
|------|-----------|----------|----------|----------|
| 索引源 | Tree-sitter | Tree-sitter | Tree-sitter+静态图 | **Tree-sitter 实时层 + SCIP 最终语义** |
| 语义精度 | 启发式 | 符号级 | 混合 | **编译器级** |
| 项目规模 | 百万行 | 千万行 | 百万行 | **亿行级** |
| 部署复杂度 | 中等 | 简单 | 简单 | **零配置** |
| 交互可视化 | 纯文本 | 静态图 | 只读图表 | **力导向图+追踪** |

## MCP 工具触发场景

| 工具 | 何时使用 |
|------|---------|
| `astramap_search` | "X 在哪定义" / "找一下 Y 函数" |
| `astramap_explore` | "X 和 Y 是怎么关联的" |
| `astramap_node` | "X 的源码是什么" |
| `astramap_callers` | "谁调用了 X" |
| `astramap_callees` | "X 依赖什么" |
| `astramap_impact` | "改了 X 会影响什么" |
| `astramap_trace` | "从 A 到 B 的调用链" |
| `astramap_status` | "索引好了吗" |

## Claude Code `/amap` 斜杠命令

```
/amap search QuerySearch
/amap explore "MCP Server 启动流程"
/amap callers go:astramap/service.go:QuerySearch
/amap impact go:astramap/service.go:QuerySearch
/amap trace main QuerySearch
/amap status
```

---

© 2026-2026 AstraMap — 高精度代码地图引擎  
作者: 何志川 | 版本: v0.1
