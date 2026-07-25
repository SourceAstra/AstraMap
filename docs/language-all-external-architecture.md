# AstraMap 双层语言索引架构

> 状态：实施基线。本文定义 AstraMap 的唯一语言索引契约；README、CLI、MCP、Dashboard
> 和回归测试不得建立与本文冲突的更新语义。

## 1. 不可约简的事实

代码地图同时面对两种性质不同的事实：

1. 源文件结构事实：定义、容器、签名、注释、导入、文件内调用表达式。它们只依赖当前文件，
   必须在文件变化后立即更新。
2. 工程语义事实：跨文件引用、重载消歧、实现关系、编译条件、宏展开后的调用目标。它们依赖
   完整构建上下文，必须由 SCIP Provider 产生。

Tree-sitter 不能确定完整工程语义；SCIP Provider 不能满足编辑期实时性。任何单层方案都必然
牺牲准确性或时效性。因此 AstraMap 固定采用双层索引：

```text
当前源码 ──Tree-sitter──> 实时语法层（syntax）
    │
    └──SCIP Provider────> 最终语义层（scip）
```

Tree-sitter 是所有正式语言的内置实时能力，不是可有可无的降级包；SCIP 是最终跨文件语义，
不是文件变化检测器。两层共享语言 Registry，但拥有独立来源、生命周期和替换边界。

## 2. 支持矩阵

正式 Registry 中的语言必须同时具备内置 Tree-sitter grammar 与可靠 SCIP Provider。缺少任意
一层都不能声明 `full`：

| 语言 | Tree-sitter 实时层 | SCIP 最终语义层 |
|---|---|---|
| Go | `tree-sitter-go` | `scip-go` |
| TypeScript | `tree-sitter-typescript` | `scip-typescript` |
| JavaScript | TypeScript grammar 的 JS/JSX dialect | `scip-typescript` |
| Python | `tree-sitter-python` | `scip-python` |
| Java | `tree-sitter-java` | `scip-java` |
| C | `tree-sitter-c` | `scip-clang` |
| C++ | `tree-sitter-cpp` | `scip-clang` |
| Rust | `tree-sitter-rust` | `rust-analyzer` SCIP 输出 |
| C# | `tree-sitter-c-sharp` | `scip-dotnet` |
| Kotlin | `tree-sitter-kotlin` | `scip-java` |
| Ruby | `tree-sitter-ruby` | `scip-ruby` |
| Scala | `tree-sitter-scala` | `scip-java` |

未经两层能力门禁的新语言不能进入正式 Registry。外置语言模块可以覆盖内置语法实现，但不能
改变语言 ID、扩展名归属或 SCIP Provider，也不能把局部语法事实提升为确定跨文件语义。

## 3. 模块边界

Core 保留唯一的语法模块契约 `languageModule`：

```go
type languageModule interface {
    Manifest() languageprotocol.Manifest
    Parse(languageprotocol.ParseRequest) (languageprotocol.FileFacts, error)
    Close() error
}
```

内置 Tree-sitter 和可安装的进程外模块都是该接口的实现。不得再新增同义的 `SyntaxProvider`、
`ParserProvider` 或按语言复制解析主流程。差异只能存在于声明式 grammar、定义规则、调用规则和
导入规则中。

数据库、文件遍历、哈希判断、事务替换和缓存失效只属于 Core；grammar 实现不得直接访问数据库。

## 4. 来源与可信度

节点和边必须带来源：

| provenance | 含义 | 允许承载的事实 |
|---|---|---|
| `scip` | Provider 产生的工程语义 | 定义、引用、跨文件调用、实现关系 |
| `syntax-package` | Tree-sitter 当前文件事实 | 定义、容器、签名、注释、导入、局部调用表达式 |
| `heuristic` | Core 的显式推断 | 仅作补充，必须可独立删除和重建 |

可信度顺序为 `scip > syntax-package > heuristic`，但“更新”优先于“陈旧”：文件发生变化后，
涉及该文件的旧 SCIP 边必须立即失效，不能与新语法节点共同构成自相矛盾的图。

## 5. 文件状态机

每个文件只有以下状态：

```text
unseen
  └── Tree-sitter 成功 ──> syntax-live

syntax-live
  └── SCIP 成功且哈希一致 ──> converged

converged
  └── 文件变化 ──> syntax-live

任意状态
  └── 文件删除/被过滤 ──> absent
```

状态转换必须对称：

- 新增文件：创建语法节点、边和文件记录；不得要求它先进入编译数据库。
- 修改文件：原子删除该文件旧语法层，写入新语法层，同时失效该文件关联的旧 SCIP 边。
- 重命名：等价于旧路径删除与新路径新增。
- 删除或过滤：删除该文件所有节点、所有入边/出边和文件记录。
- SCIP 收敛：按语义来源原子替换 SCIP 层，再根据当前源码重建语法层。

任何失败都必须保持事务前状态；不得先删后报错，也不得留下跨来源孤儿边。

## 6. 增量索引

### 6.1 普通 `amap index`

普通索引始终执行源码扫描，不以“已有 SCIP”为跳过条件：

```text
加载 Registry 与过滤器
→ 遍历所有受支持源码
→ 内容哈希跳过未变化文件
→ Tree-sitter 解析新增/变化文件
→ 逐文件事务替换实时语法层
→ 清理删除和新排除文件
→ 仅对变化文件重建 heuristic 边
```

已有 SCIP 时普通索引可以不重新运行 Provider，但必须完成实时语法更新。命令成功表示磁盘源码
已经反映到 syntax 层，不能仅表示进程退出码为零。

### 6.2 `amap index --refresh-scip` / `--full`

```text
生成或导入所选 ProjectUnit 的 SCIP
→ 校验语言和项目边界
→ 原子替换 SCIP 层
→ 扫描当前源码并重建 Tree-sitter 层
→ 重建 heuristic 层
```

`--full` 与 `--refresh-scip` 都刷新最终语义；`--full` 还强制忽略语法哈希快路径。

### 6.3 `amap watch`

watch 对文件事件执行两个时间尺度：

1. 低延迟路径：防抖后立即用 Tree-sitter 更新变化文件，查询马上可见。
2. 收敛路径：按 ProjectUnit 合并变化，后台刷新 SCIP；成功后原子提交并重建对应语法层。

SCIP 失败不能回滚已经成功的实时语法层，但必须保留 `semanticDirty` 状态和错误诊断。watch 不得
静默丢弃事件，也不得在没有外置包时跳过 Tree-sitter，因为正式语言全部具有内置实现。

## 7. 脏文件与可观测性

脏文件发现必须比较“磁盘文件集合”和“数据库文件集合”，不能只遍历 `astramap_files`：

- 磁盘存在、数据库不存在：新增脏文件；
- 两者存在、内容哈希不同：修改脏文件；
- 数据库存在、磁盘不存在：删除脏文件；
- 过滤规则变化导致集合变化：配置脏文件。

状态 API 至少暴露：

- `dirtyCount` / `dirtyFiles`：实时语法层尚未追上磁盘；
- `semanticDirtyCount` / `semanticDirtyFiles`：SCIP 最终语义尚未追上实时语法层；
- 每种语言的 Tree-sitter 与 SCIP 实际可用状态。

不得用节点总数阈值冒充健康状态。健康度由覆盖文件集合、来源完整性、哈希一致性和无孤儿边决定。

## 8. 查询合并规则

查询层按以下确定顺序合并：

1. 同一文件、同一逻辑符号存在当前语法事实时，签名、注释和位置采用语法层。
2. 哈希一致的 SCIP 定义提供稳定语义身份和跨文件关系。
3. 文件处于 `syntax-live` 时，不返回涉及该文件的旧 SCIP 调用边。
4. heuristic 只补充不存在更高来源事实的关系。
5. 公共 API 输出规范化 ID，不泄露 SCIP local ID 或 Tree-sitter 内部 LocalID。

## 9. 性能边界

- 常驻进程缓存最近语法树，按最小字节编辑区间执行 `Tree.Edit` 和旧树增量解析；缓存未命中时才完整解析该文件。
- 文件遍历通过 mtime、size 和内容哈希三级收敛，未变化文件不解析。
- SCIP 按 ProjectUnit 合并刷新，watch 防抖避免每个保存事件启动一次 Provider。
- 数据写入按文件事务化；批量 SCIP 替换按来源事务化。
- 文件级 SHA-256 决定是否进入解析；内置模块内部以有界语法树缓存执行原生增量 AST，
  数据库仍以整文件事实事务替换保证一致性。

## 10. 2026-07-20 回归报告的根因

`docs/astramap-test-report-latest.md` 的 16 个失败不是 16 个独立缺陷：

- `INC-01/02/03/06/B01`、`CE-08`、`FI-32` 的共同根因是已有 SCIP 时普通 index 缺失内置
  Tree-sitter 写入路径。删除仍能通过修剪生效，新增和修改却没有事实来源；所有语言共用该断点。
- `N-05`、`N-09`、`CE-B02` 属于 SCIP 构建单元未覆盖文件后没有语法层补位，分别表现为定义、注释和
  局部调用缺失。
- `SE-07/SE-B04` 是 C 宏公共限定名未规范化，与增量机制独立。
- `P-02/S-04` 的固定节点/边阈值以及 `EX-08` 的项目命名占比是数据集规模断言，不是功能契约；
  索引过滤或构建单元变化会使其失真。
- Phase 9 未测试是测试入口被删除，不是语言解析结果。

因此修复必须恢复所有正式语言的内置实时层、分离双哈希状态并失效过期 SCIP，而不是针对 C 测试夹具
增加分支或降低断言。

## 11. 验收门禁

每种正式语言都必须使用真实源码夹具验证：

1. 新增文件立即出现定义；
2. 新增、删除、重命名函数对称更新；
3. 签名和文档注释更新；
4. 文件内调用与 import 更新；
5. 删除文件后不存在节点和入边/出边；
6. 普通 index 不需要刷新 SCIP 即可更新 syntax 层；
7. SCIP 刷新后跨文件边收敛且无重复逻辑符号；
8. watch 的实时更新先于 SCIP 收敛；
9. 新文件能被 dirty 状态发现；
10. 任何来源替换失败都保持事务前图完整。

测试不得使用固定节点总数、项目命名占比或不属于构建单元的 SCIP 数量作为功能正确性断言。
