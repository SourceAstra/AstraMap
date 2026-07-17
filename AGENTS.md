# Repository Guidelines

## 项目结构

- `astramap/`：核心 Go 实现，包括索引、Tree-sitter 解析、调用图、MCP 服务和 HTTP 服务。
- `cmd/amap/`：命令行入口，提供 `index`、`dashboard`、`serve` 等命令。
- `astramap/web/`：Dashboard 的 HTML、CSS 和 JavaScript 前端资源。
- `docs/`：设计文档和项目说明；`README.md`、`QUICKSTART.md` 提供用户级使用文档。
- `.astramap/`：本地代码地图数据库和索引状态，不应提交生成数据。

## 构建、测试与开发

```bash
make build                 # 构建本地 amap 二进制
make test                  # 运行全部 Go 测试
make vet                   # 执行 go vet
./amap index --project .   # 构建或更新当前项目索引
./amap dashboard --project .  # 启动 Web Dashboard
```

发布 Linux 静态二进制使用 `make build-static-linux`；该命令要求构建机安装 `musl-gcc`。修改解析器或索引逻辑后，应重新运行 `amap index --full` 清理旧索引结果。

## 代码风格与命名

1.Go 代码使用 `gofmt`，保持制表符缩进；导出类型、函数和字段使用 PascalCase，内部变量使用 camelCase。错误应带上下文并向上传递。前端 JavaScript 使用现有模块和命名风格，避免引入重复的全局状态。提交前运行 `gofmt -w` 处理改动过的 Go 文件；


## 测试指南

测试框架使用 Go 标准库 `testing`，测试文件命名为 `*_test.go`，测试函数命名为 `TestXxx`。当前仓库测试数量较少；新增解析、索引或 API 行为时，应在对应包增加回归测试，并使用 `go test ./...` 验证。

## 提交与合并请求

提交信息采用简短的组件前缀和祈使式描述，例如 `treesitter.go: narrow heuristic call resolution` 或 `trace.js: preserve qualified names`。合并请求应说明问题、实现范围和验证命令；涉及 Dashboard 的改动附上截图，涉及索引数据的改动说明是否需要重新生成 `.astramap` 数据。不要提交本地数据库、构建产物或临时截图。

## AstraMap 工具约定

询问代码结构时优先使用 AstraMap 工具，而不是 grep 或递归文件搜索：定义用 `astramap_search`，上下文用 `astramap_explore`，源码详情用 `astramap_node`，调用方/被调用方用 `astramap_callers` / `astramap_callees`，影响范围用 `astramap_impact`，调用路径用 `astramap_trace`，索引状态用 `astramap_status`。
