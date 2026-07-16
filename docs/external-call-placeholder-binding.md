# AstraMap 外部调用占位符绑定机制

**文档版本**: v0.3  
**更新日期**: 2026-07-16  
**作者**: AstraMap Team

---

## 1. 问题背景

### 1.1 增量更新的挑战

在代码地图的增量更新场景中，文件被修改后需要：
1. 删除该文件在数据库中的旧节点和旧边
2. 重新解析文件生成新节点和新边
3. 保持跨文件调用链的连续性

### 1.2 核心问题

**场景**：文件 A 中的函数 `foo()` 被文件 B 调用。当文件 A 被修改后：
- 旧 `foo()` 节点被删除
- 文件 B → 旧 `foo()` 的调用边成为"悬空边"
- 新 `foo()` 节点生成，但文件 B 的调用边未自动恢复

**结果**：跨文件调用链在增量更新后断裂。

---

## 2. 解决方案：外部调用占位符

### 2.1 核心思想

在删除旧节点前，将所有指向该节点的入边迁移到**外部调用占位符**（external placeholder）。新节点生成后，将占位符边重新绑定到实际节点。

### 2.2 占位符格式

```
external:<lang_prefix> . . $ <function_name>.
```

示例：
- Go 函数 `QuerySearch` → `external:go . . $ QuerySearch.`
- C++ 函数 `ProcessData` → `external:cxx . . $ ProcessData.`

### 2.3 绑定流程

```
文件 A 被修改
    │
    ▼
┌─────────────────────────────────────────┐
│ Step 1: 查找即将被删除的节点            │
│ SELECT id, name, language, kind         │
│ FROM astramap_nodes                     │
│ WHERE file_path = ? AND id NOT LIKE 'external%' │
└─────────────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────────────┐
│ Step 2: 将入边迁移到外部占位符           │
│ UPDATE astramap_edges                   │
│ SET target = 'external:<prefix> . . $ <name>.' │
│ WHERE target = <old_node_id>            │
│   AND provenance != 'heuristic'          │
└─────────────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────────────┐
│ Step 3: 删除旧节点和旧边                │
│ DELETE FROM astramap_edges              │
│ WHERE source IN (SELECT id FROM ...)    │
│    OR target IN (SELECT id FROM ...)   │
│                                         │
│ DELETE FROM astramap_nodes             │
│ WHERE file_path = ? AND id NOT LIKE 'external%' │
└─────────────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────────────┐
│ Step 4: 重新解析文件，生成新节点         │
│ ParseFileIncremental(...)               │
└─────────────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────────────┐
│ Step 5: 将外部占位符边绑定到新节点       │
│ UPDATE astramap_edges                   │
│ SET target = <new_node_id>              │
│ WHERE target = 'external:<prefix> . . $ <name>.' │
│   AND kind = 'calls'                    │
└─────────────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────────────┐
│ Step 6: 重建启发式调用边                 │
│ ResolveCrossFileCallsForFiles(db, projectRoot, [filePath]) │
└─────────────────────────────────────────┘
```

---

## 3. 代码实现

### 3.1 核心逻辑（`astramap/astramap.go`）

```go
func SyncFileAstraMap(db *sqlx.DB, projectRoot, filePath string) (bool, error) {
    // ... 事务开始 ...

    // Step 1: 查找即将被删除的节点
    var deletedNodes []struct {
        ID       string `db:"id"`
        Name     string `db:"name"`
        Language string `db:"language"`
        Kind     string `db:"kind"`
    }
    _ = tx.Select(&deletedNodes,
        "SELECT id, name, language, kind FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%'",
        relPath)

    // Step 2: 将入边迁移到外部占位符
    for _, dn := range deletedNodes {
        if dn.Kind == "function" || dn.Kind == "method" {
            prefix := getLangPrefix(dn.Language)
            if prefix == "cpp" {
                prefix = "cxx"
            }
            extID := fmt.Sprintf("external:%s . . $ %s.", prefix, dn.Name)
            _, _ = tx.Exec(
                "UPDATE astramap_edges SET target = ? WHERE target = ? AND provenance != 'heuristic'",
                extID, dn.ID)
        }
    }

    // Step 3: 删除旧节点和旧边
    _, _ = tx.Exec(
        "DELETE FROM astramap_edges WHERE source IN (SELECT id FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%') OR target IN (SELECT id FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%')",
        relPath, relPath)
    _, _ = tx.Exec(
        "DELETE FROM astramap_nodes WHERE file_path = ? AND id NOT LIKE 'external%'",
        relPath)

    // ... 重新解析文件 ...

    // Step 5: 将外部占位符边绑定到新节点
    for _, n := range nodes {
        if n.Kind == "function" || n.Kind == "method" {
            prefix := getLangPrefix(n.Language)
            if prefix == "cpp" {
                prefix = "cxx"
            }
            extID := fmt.Sprintf("external:%s . . $ %s.", prefix, n.Name)
            _, _ = db.Exec(
                "UPDATE astramap_edges SET target = ? WHERE target = ? AND kind = 'calls'",
                n.ID, extID)
        }
    }

    // Step 6: 重建启发式调用边
    if err2 := ResolveCrossFileCallsForFiles(db, projectRoot, []string{relPath}); err2 != nil {
        logError("ResolveCrossFileCalls failed for %s: %v", relPath, err2)
    }

    // ...
}
```

---

## 4. 设计考量

### 4.1 为什么只处理 function/method？

- 外部调用边（`calls` kind）的 target 只能是函数或方法
- 类、结构体、变量等不会被其他文件"调用"
- 简化逻辑，减少不必要的占位符生成

### 4.2 为什么排除 heuristic provenance？

```go
_, _ = tx.Exec(
    "UPDATE astramap_edges SET target = ? WHERE target = ? AND provenance != 'heuristic'",
    extID, dn.ID)
```

- heuristic 边是运行时生成的，不持久化存储
- 排除 heuristic 边避免重复处理

### 4.3 为什么使用 `external:` 前缀？

- 与现有 `external:` 节点 ID 格式一致
- 避免与真实节点 ID 冲突
- 便于查询和清理

### 4.4 边界情况

| 场景 | 处理 |
|------|------|
| 函数被删除（不再存在） | 占位符边保留，指向 `external:` 节点 |
| 函数重命名 | 旧占位符保留，新函数生成新的外部调用边 |
| 函数签名变更但名称不变 | 占位符边自动绑定到新节点 |
| 多文件同时修改 | 每个文件的 SyncFileAstraMap 独立处理，无冲突 |

---

## 5. 与 SCIP 的关系

### 5.1 SCIP 导入路径

SCIP 导入时也会生成 `external:` 节点：
- SCIP 索引中的跨文件引用指向不存在的符号时
- 自动生成 `external:` 占位符节点

### 5.2 Tree-sitter 与 SCIP 的协同

- SCIP 导入的 `external:` 节点与 Tree-sitter 生成的占位符边**格式一致**
- 新 Tree-sitter 节点生成后，占位符边自动绑定到实际节点
- 实现 SCIP 和 Tree-sitter 两种来源的调用链无缝衔接

---

## 6. 相关文件

| 文件 | 作用 |
|------|------|
| `astramap/astramap.go` | `SyncFileAstraMap` 函数：外部调用占位符绑定核心逻辑 |
| `astramap/treesitter.go` | `ParseFileIncremental`：文件解析，生成新节点 |
| `astramap/graph.go` | 图遍历引擎，处理 `external:` 节点的查询 |

---

## 7. 变更历史

| 版本 | 变更 |
|------|------|
| v0.3 | 新增外部调用占位符绑定机制：`SyncFileAstraMap` 删除旧节点前迁移入边到 `external:` 占位符，新节点生成后自动绑定 |
