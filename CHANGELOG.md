# 更新日志 (Changelog)

AstraMap 项目的所有重要版本变更都将记录在此文件中。

遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.0.0/) 规范，并遵守 [语义化版本 2.0.0](https://semver.org/lang/zh-CN/)。

## [v0.2.0] - 2026-07-30

### 🚀 架构设计与核心定位
- **SCIP 语义主导 + Tree-sitter 语法覆盖架构**：明确并确立了系统“SCIP 高精度语义为主，Tree-sitter 实时语法为辅”的双层混合架构。SCIP 提供 95%+ 编译器级的跨文件类型推导与消歧，Tree-sitter 负责毫秒级的文件变更增量热更新。
- **文档全面重构**：重构了 `README.md` 与 `README_EN.md`，开篇突出了高精度代码地图的核心优势、Mermaid 双层架构图、与纯 Tree-sitter 工具的痛点横评以及 Web Dashboard 三大交互视界。

### ⚡ 性能优化与增量索引
- **Watch 模式下的防抖 SCIP 收敛**：重构了 `amap watch` 循环，彻底隔离了高频保存文件时的 SCIP 重新编译开销。高频编辑时仅触发 Tree-sitter 毫秒级补丁，引入 2 分钟静默防抖定时器，仅在系统闲置时自动于后台运行一次高精度 SCIP 收敛。
- **精准日志输出**：优化了增量更新时的终端日志，清晰显示哪些文件发生了语法层更新，并在 SCIP 更新完成时提供明确提示 (`watch successfully refreshed map via SCIP index update`)。

### 🎯 启发式解析 (Heuristic) 与消歧增强
- **同包路径就近消歧**：增强了 `ResolveCrossFileCalls` 的启发式解析。当遇到多义同名短函数时，优先匹配与调用者处于物理同包（同一目录）下的定义节点。
- **Go 隐式函数拦截**：显式拦截了 Go 语言运行时的隐式初始化函数 `init()`，杜绝启发式解析建立错误的 `calls -> init` 连线。
- **成员选择符盲连防御**：对带选择符的调用（`obj.Method()`）增加了消歧阀门。若未通过全限定名 `QualifiedName` 精确命中，且存在多个不同结构体的同名方法时，禁止退化为全库盲目乱连，防止污染调用图。
- **跨平台路径规范化**：修复了 Windows 平台下路径斜杠分隔符不一致导致同包消歧失效的问题，统一采用 `filepath.ToSlash` 进行标准化。

---

## [v0.1.0] - 2026-07-25

### 🎉 首次发布
- AstraMap 语义代码地图引擎首个版本发布。
- 原生 stdio MCP Server，提供 9 大结构化工具（`search`、`explore`、`node`、`callers`、`callees`、`impact`、`trace`、`status`、`files`）。
- 内置 Web Dashboard，提供探索视界、力导向依赖拓扑网与模块理解文档。
- 支持 12 种主流语言的 SCIP 导入与 Tree-sitter 基础解析。
