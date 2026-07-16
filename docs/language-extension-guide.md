# AstraMap 语言扩展指南

**文档版本**: v0.3  
**更新日期**: 2026-07-16  
**作者**: AstraMap Team

---

## 1. 当前支持语言（6 种）

| 语言 | Tree-sitter 绑定 | SCIP 索引器 | 状态 |
|------|------------------|------------|------|
| Go | `tree-sitter-go` | `scip-go` | ✅ 完整支持 |
| Python | `tree-sitter-python` | `scip-python` | ✅ 完整支持 |
| TypeScript / TSX / JS / JSX | `tree-sitter-typescript` | `scip-typescript` | ✅ 完整支持 |
| C | `tree-sitter-c` | `scip-clang` | ✅ 完整支持 |
| C++ | `tree-sitter-cpp` | `scip-clang` | ✅ 完整支持 |
| Java | `tree-sitter-java` | `scip-java` | ✅ 完整支持 |

**双引擎架构**：SCIP（编译器级精度，跨文件调用）+ Tree-sitter（AST 解析，签名/源码还原）。两者互补，SCIP 为主。

---

## 2. 新增一种语言的最小工作量

### 2.1 架构概览

```
ParseFileIncremental
├── 语言识别（ext → lang → grammar）      // 第1层：注册
├── AST 节点遍历（collect）                 // 第2层：定义提取
│   ├── 函数/方法定义节点类型识别
│   ├── 类/结构体/接口定义节点类型识别
│   ├── 类型别名/枚举定义节点类型识别
│   └── 容器/命名空间提取（initialContainer）
├── 调用边收集（collectCalls）              // 第3层：调用关系
│   ├── call_expression 节点类型识别
│   └── callee 提取（extractCalleeShortName）
└── 导入边收集（collectImports）            // 第4层：依赖关系
```

### 2.2 四步实现（以 Rust 为例）

#### 步骤 1：Tree-sitter 语法绑定（~5 行）

**文件**: `astramap/treesitter.go`

```go
import rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
```

在 `switch ext` 中增加：

```go
case ".rs":
    lang = "rust"
    langGrammar = sitter.NewLanguage(rust.Language())
```

#### 步骤 2：AST 节点定义提取（~30-50 行）

在 `collect` 函数的 `switch lang` 中增加 Rust case：

```go
case "rust":
    if nodeType == "function_item" {
        nodeKind = "function"
        if nameNode := n.ChildByFieldName("name"); nameNode != nil {
            nodeName = nodeText(nameNode, codeBytes)
        }
        isDef = true
    } else if nodeType == "impl_item" {
        // impl 块内的方法
        if nameNode := n.ChildByFieldName("name"); nameNode != nil {
            nodeName = nodeText(nameNode, codeBytes)
        }
        nodeKind = "method"
        // 提取 impl 的目标类型作为 container
        if typeNode := n.ChildByFieldName("type"); typeNode != nil {
            container = nodeText(typeNode, codeBytes)
        }
        isDef = true
    } else if nodeType == "struct_item" {
        nodeKind = "struct"
        if nameNode := n.ChildByFieldName("name"); nameNode != nil {
            nodeName = nodeText(nameNode, codeBytes)
        }
        isDef = true
    } else if nodeType == "enum_item" {
        nodeKind = "enum"
        if nameNode := n.ChildByFieldName("name"); nameNode != nil {
            nodeName = nodeText(nameNode, codeBytes)
        }
        isDef = true
    } else if nodeType == "trait_item" {
        nodeKind = "interface"
        if nameNode := n.ChildByFieldName("name"); nameNode != nil {
            nodeName = nodeText(nameNode, codeBytes)
        }
        isDef = true
    } else if nodeType == "type_item" {
        nodeKind = "type"
        if nameNode := n.ChildByFieldName("name"); nameNode != nil {
            nodeName = nodeText(nameNode, codeBytes)
        }
        isDef = true
    }
```

**关键注意点**：
- Rust 的 `impl Foo { fn bar() {} }` 中，`bar` 的 `container` 需要是 `Foo`
- `impl` 块解析需要特殊处理，因为 AST 中 `impl_item` 的父节点是 `impl` 块而非类定义

#### 步骤 3：调用表达式识别（~10 行）

在 `collectCalls` 的 `switch lang` 中增加：

```go
case "rust":
    if nodeType == "call_expression" {
        isCall = true
        calleeNode = n.ChildByFieldName("function")
    } else if nodeType == "method_call_expression" {
        isCall = true
        // method_call_expression 的 callee 是 field_expression: obj.method()
        calleeNode = n.ChildByFieldName("name")
    }
```

`extractCalleeShortName` 可能需要扩展以处理 `field_expression`（如 `obj.method()` 中的 `method`）。

#### 步骤 4：导入边收集（~5 行）

在 `collectImports` 的节点类型检查中增加：

```go
if nodeType == "use_declaration" || nodeType == "extern_crate_declaration" {
    // Rust 的 use 声明和 extern crate 声明
    impPath := normalizeImportText(nodeText(n, codeBytes))
    if impPath != "" {
        targetUSN := fmt.Sprintf("import:%s", impPath)
        edges = append(edges, &AstraMapEdge{
            Source:     fmt.Sprintf("file:%s", relPath),
            Target:     targetUSN,
            Kind:       "imports",
            Provenance: "tree-sitter",
        })
    }
}
```

**`normalizeImportText` 扩展**：

Rust 的 `use` 语句格式多样：
- `use std::collections::HashMap;`
- `use crate::module::func;`
- `use super::something;`

需要扩展 `normalizeImportText` 以处理这些格式。

#### 步骤 5：初始容器（~5 行）

Rust 的模块系统：

```go
} else if lang == "rust" {
    // Rust 模块：文件名即模块名（mod.rs 除外）
    base := filepath.Base(filePath)
    if base == "mod.rs" || base == "lib.rs" || base == "main.rs" {
        initialContainer = filepath.Base(filepath.Dir(filePath))
    } else {
        initialContainer = strings.TrimSuffix(base, filepath.Ext(base))
    }
}
```

---

## 3. 各语言工作量评估

### 3.1 Tree-sitter 基础支持（定义 + 单文件调用 + 导入）

| 语言 | 语法复杂度 | 特殊 AST 节点 | 容器/命名空间 | 预估代码行数 | 预估工时 |
|------|-----------|-------------|------------|-----------|---------|
| **Rust** | 中 | impl/trait/enum/struct | 复杂（模块系统） | ~150 | 1-2 天 |
| **Ruby** | 低 | class/def/module | 简单 | ~80 | 半天 |
| **PHP** | 低 | class/function/namespace | 简单 | ~80 | 半天 |
| **C#** | 中 | class/interface/struct/enum | 中（namespace） | ~100 | 1 天 |
| **Swift** | 中 | class/struct/enum/protocol/func | 中（extension） | ~120 | 1 天 |
| **Kotlin** | 中 | class/interface/fun/object | 中 | ~100 | 1 天 |
| **Scala** | 高 | trait/class/object/def | 复杂 | ~200 | 2-3 天 |
| **Zig** | 低 | fn/struct/union/enum | 简单 | ~80 | 半天 |

### 3.2 SCIP 索引器依赖

| 语言 | SCIP 索引器可用性 | 备注 |
|------|----------------|------|
| Rust | ✅ `scip-rust` (基于 rust-analyzer) | 成熟 |
| Ruby | ⚠️ 无官方 SCIP 索引器 | 可用 tree-sitter 纯解析 |
| PHP | ⚠️ 无官方 SCIP 索引器 | 可用 tree-sitter 纯解析 |
| C# | ✅ `scip-dotnet` | 可用 |
| Swift | ⚠️ 无官方 SCIP 索引器 | 可用 tree-sitter 纯解析 |
| Kotlin | ⚠️ 无官方 SCIP 索引器 | 可用 tree-sitter 纯解析 |
| Scala | ⚠️ 无官方 SCIP 索引器 | 可用 tree-sitter 纯解析 |
| Zig | ⚠️ 无官方 SCIP 索引器 | 可用 tree-sitter 纯解析 |

**关键点**：没有 SCIP 索引器的语言，纯 tree-sitter 模式下跨文件调用边为 0。这是架构设计文档中已明确的固有限制。

---

## 4. 最小可行方案（MVP）

如果只需要 **Tree-sitter 基础支持**（定义提取 + 单文件调用 + 导入），不需要 SCIP：

1. **Rust**：~150 行，1-2 天（最优先，生态成熟）
2. **Ruby/PHP/Zig**：各 ~80 行，各半天（语法简单，AST 直接）
3. **C#/Swift/Kotlin**：各 ~100-120 行，各 1 天

**总计**：~600 行代码，4-5 天 可新增 6 种语言的 Tree-sitter 基础支持。

如果同时需要 **SCIP 支持**：
- Rust 和 C# 有现成 SCIP 索引器，额外工作量小
- 其余语言只能纯 tree-sitter，跨文件调用边为 0

---

## 5. 扩展建议：提取公共模式

当前 `treesitter.go` 的 `switch lang` 模式是可扩展的，但存在重复代码。如果计划新增多种语言，建议先提取公共模式：

```go
// LanguageHandler 接口，消除 switch 重复
type LanguageHandler interface {
    // 从 AST 节点提取定义
    ExtractDefinitions(node *sitter.Node, code []byte) (*Definition, bool)
    // 从 AST 节点提取调用
    ExtractCalls(node *sitter.Node, code []byte) (*Call, bool)
    // 从 AST 节点提取导入
    ExtractImports(node *sitter.Node, code []byte) (string, bool)
    // 获取文件的初始容器（包名/模块名）
    InitialContainer(filePath string, rootNode *sitter.Node, code []byte) string
}

type Definition struct {
    Name      string
    Kind      string
    Container string
    Signature string
    StartLine int
    EndLine   int
}

type Call struct {
    CalleeName string
    Line       int
    Col        int
}
```

这样新增语言只需实现 `LanguageHandler` 接口，主逻辑零改动。

---

## 6. 相关文件

| 文件 | 作用 |
|------|------|
| `astramap/treesitter.go` | Tree-sitter 解析主逻辑，新增语言的主要修改点 |
| `astramap/astramap.go` | SCIP 导入逻辑，新增 SCIP 支持时修改 |
| `cmd/amap/main.go` | CLI 语言检测，新增文件扩展名时修改 |
| `go.mod` | 添加新的 tree-sitter 绑定依赖 |

---

## 7. 参考资源

- [Tree-sitter Language Bindings](https://tree-sitter.github.io/tree-sitter/)
- [SCIP Indexers](https://github.com/sourcegraph/scip)
- AstraMap 设计文档: `docs/astramap_design.md`
