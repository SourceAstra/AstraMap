# AstraMap 2 分钟部署指南

> 从零到 AI 工具集成代码地图，只需四步。

---

## 第一步：加入 PATH

```bash
mkdir -p ~/bin
cp ./amap ~/bin
echo 'export PATH="$HOME/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

验证安装：

```bash
which amap
# 应输出: /home/you/bin/amap
```

## 第二步：进入目标项目，一键注册

```bash
cd /path/to/your/project

# 一键注册 MCP + 规则文件到 Claude Code / VS Code / Cursor / Codex / Windsurf / Cline / Antigravity
amap install
```

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

安装完成！8/8 工具注册成功。
```

## 第三步：构建索引

```bash
# 默认模式：自动检测语言，SCIP 优先；无 SCIP 时 Tree-sitter 回退
amap index

# 指定仅导入某语言（跳过交互选择）
amap index --lang go

# 仅 Tree-sitter 快速扫描（跳过 SCIP 检测）
amap index --treesitter-only
```

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
  边   (按来源): scip=892, tree-sitter=41, heuristic=23 (合计=956)

索引构建完成！
```

## 第四步：启动可视化控制台

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
源码 → SCIP 高精度索引 + Tree-sitter 实时补丁 → SQLite 知识图谱 → MCP/HTTP API → 本地可视化与工具客户端
```

## 核心优势

- **95%+ 语义精度** — SCIP 编译器级索引，区分重载/泛型
- **60-95% Token 节约** — 单次调用替代多次 grep+Read
- **毫秒级更新** — 文件变更 50ms 内同步

## 工具对比

| 维度 | CodeGraph | GitNexus | Graphify | AstraMap |
|------|-----------|----------|----------|----------|
| 索引源 | Tree-sitter | Tree-sitter | Tree-sitter+静态图 | **SCIP+Tree-sitter** |
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
