# AstraMap 多语言测试策略与执行计划

**文档版本**：v1.0  
**更新日期**：2026-07-16  
**状态**：设计提案

---

## 1. 结论

当前测试依赖真实项目（`/home/he/sdk/tests/astramap`、`/home/he/SourceAstra/tests/astramap`），存在以下结构性缺陷：

1. **覆盖不系统**：Go 项目为主，Python/TypeScript/C/C++/Java 仅被动验证
2. **边界难触及**：错误处理、重载消歧、宏生成符号等边缘场景无专用夹具
3. **回归无保障**：修改 `treesitter.go` 后无法自动验证各语言解析正确性
4. **性能无基准**：大项目索引耗时无量化指标

**目标架构**：构建语言无关的测试框架，通过声明式夹具驱动，实现一次编写、多语言复用。

```text
测试 = LanguageSpec + TestFixture + AssertionRule
执行 = Fixture → Parse → Normalize → Validate
```

---

## 2. 第一性原理：测试的本质

### 2.1 不可变公理

无论源语言是什么，代码地图最终只消费以下规范化事实：

```text
File          → 语言识别
Symbol        → 定义提取
Relation      → 调用/包含/导入
SourceRange   → 位置信息
Provenance    → 来源标记
```

测试必须验证：**输入源码 → 规范化事实** 的映射是否正确。

### 2.2 差异只能存在于三个边界

```text
Detection Boundary   文件扩展名 → LanguageSelection
Syntax Boundary      AST 节点 → CaptureRecord
Semantic Boundary    项目根目录 → SCIP Index
```

测试必须分层验证：
- **Detection 层**：扩展名/方言识别正确性
- **Syntax 层**：定义/调用/导入提取正确性
- **Semantic 层**：跨文件调用消歧正确性

### 2.3 最小测试单位

抽象单位不是“语言”，而是可组合的语法能力：

| 能力片段 | 测试焦点 | 示例断言 |
|----------|----------|----------|
| `NamedDefinition` | 名称提取 | `name == "foo"` |
| `CallableDefinition` | 签名提取 | `signature == "func(int)"` |
| `ScopedDefinition` | 容器归属 | `container == "pkg.Class"` |
| `CallByFunctionField` | 调用目标 | `callee == "bar"` |
| `ImportByPathField` | 导入路径 | `path == "fmt"` |

一种语言只是这些能力的确定组合。

---

## 3. 目标架构

### 3.1 目录结构

```text
tests/
├── fixtures/                    # 语言无关夹具库
│   ├── definition/             # 定义提取夹具
│   │   ├── named_function.yaml
│   │   ├── method_with_receiver.yaml
│   │   └── nested_class.yaml
│   ├── call/                   # 调用提取夹具
│   │   ├── simple_call.yaml
│   │   ├── qualified_call.yaml
│   │   └── macro_expanded_call.yaml
│   └── import/                 # 导入提取夹具
│       ├── module_import.yaml
│       └── package_import.yaml
│
├── languages/                   # 语言特定夹具
│   ├── go/
│   │   ├── basic.yaml          # 基础函数/方法/接口
│   │   ├── advanced.yaml       # 泛型/嵌套/匿名函数
│   │   └── edge_cases.yaml     # 重载/导出/未导出
│   ├── python/
│   │   ├── basic.yaml
│   │   ├── advanced.yaml
│   │   └── edge_cases.yaml
│   ├── typescript/
│   ├── c/
│   ├── cpp/
│   └── java/
│
├── scenarios/                   # 跨文件场景
│   ├── cross_file_calls.yaml
│   ├── interface_implementation.yaml
│   └── cyclic_dependency.yaml
│
├── benchmarks/                  # 性能基准
│   ├── small_project.yaml      # < 1K 文件
│   ├── medium_project.yaml     # 1K-10K 文件
│   └── large_project.yaml      # > 10K 文件
│
├── run_unit_tests.sh           # 单元测试入口
├── run_integration_tests.sh    # 集成测试入口
├── run_benchmarks.sh           # 性能基准入口
└── lib.sh                      # 测试框架库
```

### 3.2 夹具格式（YAML）

```yaml
# tests/languages/go/basic.yaml
metadata:
  language: go
  description: "Go 基础定义与调用提取"
  complexity: basic

fixtures:
  - id: func_declaration
    source: |
      package main
      func add(a, b int) int { return a + b }
    expected:
      symbols:
        - name: "add"
          kind: function
          signature: "func(a int, b int) int"
          container: "main"
          exported: true

  - id: method_declaration
    source: |
      type Point struct{ X, Y float64 }
      func (p Point) Distance() float64 { return p.X*p.X + p.Y*p.Y }
    expected:
      symbols:
        - name: "Distance"
          kind: method
          receiver: "Point"
          signature: "func(p Point) float64"
          container: "Point"

  - id: interface_definition
    source: |
      type Writer interface { Write([]byte) (int, error) }
    expected:
      symbols:
        - name: "Writer"
          kind: interface
          methods:
            - name: "Write"
              signature: "func([]byte) (int, error)"

  - id: simple_call
    source: |
      func main() { result := add(1, 2) }
    expected:
      calls:
        - callee: "add"
          caller: "main"
          location: "main.go:3:15"

assertions:
  - all_symbols_extracted
  - all_calls_resolved
  - no_duplicate_symbols
```

### 3.3 测试框架核心

```bash
# tests/lib.sh 核心函数

# 加载夹具
load_fixture() {
  local fixture_file="$1"
  local tmpdir=$(mktemp -d)
  
  # 创建临时项目结构
  mkdir -p "$tmpdir/src"
  echo "$FIXTURE_SOURCE" > "$tmpdir/src/test.go"
  
  # 索引项目
  amap index --project "$tmpdir" >/dev/null 2>&1
  
  # 验证结果
  validate_expectations "$tmpdir" "$FIXTURE_EXPECTED"
  
  # 清理
  rm -rf "$tmpdir"
}

# 验证符号提取
validate_symbols() {
  local project_dir="$1"
  local expected_symbols="$2"
  
  for sym in $(echo "$expected_symbols" | jq -r '.[].name'); do
    local count=$(query_val "$project_dir" \
      "SELECT COUNT(*) FROM astramap_nodes WHERE name='$sym'")
    if [[ "$count" != "1" ]]; then
      fail "Symbol $sym not found or duplicated (count=$count)"
    fi
  done
}

# 验证调用关系
validate_calls() {
  local project_dir="$1"
  local expected_calls="$2"
  
  for call in $(echo "$expected_calls" | jq -r '.[]'); do
    local callee=$(echo "$call" | jq -r '.callee')
    local caller=$(echo "$call" | jq -r '.caller')
    local count=$(query_val "$project_dir" \
      "SELECT COUNT(*) FROM astramap_edges 
       WHERE kind='calls' AND source LIKE '%$caller%' AND target LIKE '%$callee%'")
    if [[ "$count" != "1" ]]; then
      fail "Call $caller -> $callee not found (count=$count)"
    fi
  done
}
```

---

## 4. 测试分层

### 4.1 L1: 语言识别测试（Detection Layer）

**目标**：验证文件扩展名 → 语言识别的正确性。

| 测试ID | 输入 | 预期输出 |
|--------|------|----------|
| DETECT-001 | `main.go` | `go` |
| DETECT-002 | `index.ts` | `typescript` |
| DETECT-003 | `app.py` | `python` |
| DETECT-004 | `util.h` | `c` 或 `cpp`（需消歧） |
| DETECT-005 | `main.rs` | `rust`（待支持） |

**执行方式**：
```bash
# 创建临时文件
echo 'int main() {}' > /tmp/test.c
amap locate --project /tmp test_main
# 验证返回的语言前缀为 "c:"
```

### 4.2 L2: 语法提取测试（Syntax Layer）

**目标**：验证 AST → CaptureRecord 的正确性。

#### 4.2.1 定义提取

| 测试ID | 语言 | 测试点 | 预期 |
|--------|------|--------|------|
| DEF-GO-001 | Go | 函数声明 | `name=add, kind=function` |
| DEF-GO-002 | Go | 方法声明 | `name=Distance, receiver=Point` |
| DEF-GO-003 | Go | 接口定义 | `name=Writer, methods=[Write]` |
| DEF-PY-001 | Python | 类定义 | `name=Point, kind=class` |
| DEF-PY-002 | Python | 方法定义 | `name=distance, self=True` |
| DEF-TS-001 | TypeScript | 接口定义 | `name=User, kind=interface` |
| DEF-TS-002 | TypeScript | 泛型函数 | `name=wrap, signature=T->T` |
| DEF-C-001 | C | 结构体定义 | `name=Point, fields=[x,y]` |
| DEF-CPP-001 | C++ | 类定义 | `name=Vector, methods=[push_back]` |
| DEF-JAVA-001 | Java | 类定义 | `name=HashMap, implements=Map` |

#### 4.2.2 调用提取

| 测试ID | 语言 | 测试点 | 预期 |
|--------|------|--------|------|
| CALL-GO-001 | Go | 简单调用 | `callee=add, caller=main` |
| CALL-GO-002 | Go | 方法调用 | `callee=Distance, receiver=p` |
| CALL-PY-001 | Python | 函数调用 | `callee=add, args=[1,2]` |
| CALL-TS-001 | TypeScript | 箭头函数调用 | `callee=handler, context=this` |
| CALL-C-001 | C | 宏展开调用 | `callee=MAX, expanded=true` |

#### 4.2.3 导入提取

| 测试ID | 语言 | 测试点 | 预期 |
|--------|------|--------|------|
| IMP-GO-001 | Go | 包导入 | `path=fmt, alias=""` |
| IMP-PY-001 | Python | 模块导入 | `path=os, from=False` |
| IMP-PY-002 | Python | 从导入 | `path=os.path, names=[join]` |
| IMP-TS-001 | TypeScript | 默认导入 | `path=fs, default=true` |
| IMP-C-001 | C | 头文件包含 | `path=stdio.h, system=true` |

### 4.3 L3: 语义解析测试（Semantic Layer）

**目标**：验证跨文件调用消歧正确性。

| 测试ID | 场景 | 预期 |
|--------|------|------|
| SEMA-001 | 同包内调用 | 精确匹配 |
| SEMA-002 | 接口实现 | `implements` 边 |
| SEMA-003 | 命名空间容器 | `namespace -contains-> function` |
| SEMA-004 | 重载定义 | 保留多个同名定义 |
| SEMA-005 | 宏定义 | 提取 `macro` 节点 |

### 4.4 L4: 边界与错误处理测试

**目标**：验证异常输入的处理正确性。

| 测试ID | 输入 | 预期行为 |
|--------|------|----------|
| ERR-001 | 空文件 | 跳过，不报错 |
| ERR-002 | 语法错误 | 记录错误，继续索引其他文件 |
| ERR-003 | 超大文件（>1MB） | 跳过或截断 |
| ERR-004 | 二进制文件 | 跳过 |
| ERR-005 | 循环包含 | 检测并中断 |
| ERR-006 | 重复定义 | 保留所有，不覆盖 |
| ERR-007 | 无效UTF-8 | 跳过或转义 |

### 4.5 L5: 性能基准测试

**目标**：量化索引性能，建立回归基线。

| 测试ID | 项目规模 | 语言分布 | 预期指标 |
|--------|----------|----------|----------|
| PERF-001 | 1K 文件 | Go 100% | < 5s |
| PERF-002 | 5K 文件 | Go 50%, Python 30%, TS 20% | < 30s |
| PERF-003 | 10K 文件 | 混合语言 | < 60s |
| PERF-004 | 单文件 10K 行 | Go | < 1s |
| PERF-005 | 内存占用峰值 | 10K 文件 | < 512MB |

---

## 5. 执行计划

### Phase 0: 基础设施搭建（Week 1）

**目标**：建立测试框架骨架。

| 任务 | 产出 | 验收标准 |
|------|------|----------|
| 创建 `tests/` 目录结构 | 目录树 | 符合 §3.1 |
| 实现 `lib.sh` 核心函数 | 可执行脚本 | `load_fixture` 可用 |
| 编写 3 个示例夹具 | YAML 文件 | Go/Python/TypeScript |
| CI 集成 | GitHub Actions | PR 触发测试 |

### Phase 1: 核心语言覆盖（Week 2-3）

**目标**：为 6 种支持语言建立基础夹具库。

| 语言 | 夹具数量 | 重点覆盖 |
|------|----------|----------|
| Go | 20 | 函数/方法/接口/结构体/嵌入 |
| Python | 15 | 类/方法/装饰器/导入 |
| TypeScript | 15 | 接口/类/泛型/命名空间 |
| C | 10 | 结构体/函数/宏/指针 |
| C++ | 10 | 类/模板/命名空间/继承 |
| Java | 10 | 类/接口/注解/泛型 |

**验收标准**：每种语言至少 10 个通过用例。

### Phase 2: 边界与错误处理（Week 4）

**目标**：建立健壮性测试集。

| 任务 | 夹具数量 | 验收标准 |
|------|----------|----------|
| 空文件/注释文件 | 5 | 全部跳过 |
| 语法错误文件 | 5 | 记录错误，不崩溃 |
| 超大文件 | 3 | 超时或截断 |
| 二进制文件 | 3 | 正确跳过 |
| 重复定义 | 5 | 全部保留 |

### Phase 3: 性能基准（Week 5）

**目标**：建立性能回归基线。

| 任务 | 产出 | 验收标准 |
|------|------|----------|
| 生成合成项目 | 1K/5K/10K 文件 | 可复现 |
| 运行基准测试 | 性能报告 | 指标符合 §4.5 |
| 建立 CI 监控 | GitHub Actions | PR 性能对比 |

### Phase 4: 集成与自动化（Week 6）

**目标**：实现全流程自动化。

| 任务 | 产出 | 验收标准 |
|------|------|----------|
| 统一测试入口 | `run_all_tests.sh` | 一键执行 |
| 报告生成 | Markdown 报告 | 包含覆盖率 |
| CI 集成 | GitHub Actions | PR 自动触发 |
| 失败通知 | Slack/邮件 | 实时告警 |

---

## 6. 与现有测试的整合

### 6.1 迁移策略

现有测试（`/home/he/sdk/tests/astramap/*.sh`）保留作为**集成测试**，新增 `tests/` 作为**单元测试**。

```text
tests/                    # 单元测试（新）
  → fixtures/            # 声明式夹具
  → run_unit_tests.sh    # 快速反馈（< 30s）

/home/he/sdk/tests/astramap/  # 集成测试（现有）
  → phase1~8.sh          # 真实项目验证
  → run.sh               # 全量回归（~10min）
```

### 6.2 执行顺序

```bash
# CI 流水线
./tests/run_unit_tests.sh          # 单元测试（必过）
./tests/run_benchmarks.sh          # 性能基准（可选）
/home/he/sdk/tests/astramap/run.sh # 集成测试（可选）
```

---

## 7. 验收约束

### 7.1 架构约束

- 测试夹具与语言绑定，不硬编码语言逻辑
- 同一测试可在多种语言上执行（如 `NamedDefinition`）
- 新增语言只需添加夹具，无需修改测试框架
- 测试失败时提供最小差异定位（diff 级别）

### 7.2 数据约束

- 夹具文件大小 < 1KB（单文件）
- 夹具可组合（如 `method_with_receiver` 复用 `named_function`）
- 预期结果可声明（如 `all_symbols_extracted`）
- 实际结果可序列化（JSON/SQLite）

### 7.3 复杂度约束

- 单个夹具解析时间 < 100ms
- 100 个夹具并行执行 < 10s
- 测试框架代码 < 500 行
- 新增语言的夹具编写时间 < 2h

---

## 8. 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| Tree-sitter grammar 变更 | 夹具失效 | 锁定 grammar 版本 |
| 语言特性不支持 | 测试失败 | 标记为 `unsupported`，不阻塞 |
| 性能基准漂移 | 误报 | 定期校准，允许 ±20% 波动 |
| CI 环境差异 | 结果不一致 | 使用 Docker 固定环境 |

---

## 9. 参考

- AstraMap 当前实现：`astramap/treesitter.go`
- AstraMap 多语言与语言包统一文档：`docs/language-plugin-long-term-architecture.md`
- Tree-sitter Query 语法：https://tree-sitter.github.io/tree-sitter/using-parsers#query-syntax
- SCIP 协议规范：https://github.com/sourcegraph/scip
