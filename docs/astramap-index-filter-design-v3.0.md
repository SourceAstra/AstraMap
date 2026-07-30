# AstraMap 代码地图索引过滤设计方案

> 文档版本：v3.0  
> 设计宗旨：**代码地图只索引手写的、承载业务语义的源代码。任何由工具、编译器、构建系统自动生成或产出的文件，都没有被索引的正当理由。**  
> 核心目标：系统默认即可正确过滤绝大多数无关文件，不能把识别生成代码、构建产物和第三方依赖的责任转嫁给用户配置。

---

## 1. 设计结论

代码地图的过滤机制不应以项目 `config.yaml` 为主体，而应以一套经过验证、持续演进的**内置通用规则引擎**为主体。

推荐结构：

```text
内置通用识别
  ├─ 仓库元数据
  ├─ 隐藏路径
  ├─ 第三方依赖
  ├─ 构建产物
  ├─ 缓存和临时文件
  ├─ 自动生成源码
  ├─ 压缩、映射和二进制文件
  └─ 编辑器和操作系统垃圾文件

项目配置
  ├─ include：可选，限制索引范围
  ├─ exclude：可选，补充项目特有排除
  └─ advanced.forceInclude：极少数误判时的逃生通道
```

普通用户不需要理解：

- SCIP；
- Tree-sitter；
- 生成代码文件名规则；
- 各语言构建目录；
- 第三方包管理器目录；
- 生成文件头标记。

系统应自动完成这些判断。

---

## 2. 索引对象的边界

### 2.1 应索引

代码地图只索引：

1. 项目团队手写的一方源码；
2. 承载业务逻辑、领域模型、协议处理和模块协作语义的源码；
3. 手写的接口定义文件，如 `.proto`、`.thrift`、GraphQL Schema 等；
4. 手写测试代码是否索引，由产品目标决定，但不能因为位于 `test/` 就默认视为生成文件。

### 2.2 不应索引

以下内容默认没有进入代码地图的正当理由：

- 自动生成源码；
- 编译产物；
- 打包产物；
- 压缩或混淆产物；
- Source Map；
- 缓存；
- 临时文件；
- 第三方依赖源码；
- 包管理器下载内容；
- 版本控制内部数据；
- IDE 和操作系统产生的状态文件；
- 二进制文件、模型文件、归档文件；
- 隐藏目录中的工程工具数据。

### 2.3 生成代码不做摘要索引

本设计不采用：

```text
生成代码 → SUMMARY
第三方依赖 → BOUNDARY
```

而采用：

```text
生成代码 → SKIP
第三方依赖 → SKIP
构建产物 → SKIP
缓存和临时文件 → SKIP
```

原因：

1. 生成代码会显著膨胀节点和边；
2. 生成代码中大部分是模板化样板逻辑；
3. 生成关系应回溯到手写 IDL 或 Schema，而不是生成物；
4. 第三方依赖不属于项目自身代码地图；
5. 外部调用可通过 import、包名、API 名称建立外部引用节点，无需索引依赖实现。

---

## 3. 设计原则

### 原则一：默认正确，不依赖用户补规则

用户创建项目后，不配置任何排除规则，也不应把常见生成代码和构建垃圾载入代码地图。

错误做法：

```yaml
exclude:
  - "node_modules/**"
  - "target/**"
  - "build/**"
  - "**/*.pb.go"
  - "**/*_pb2.py"
```

这些属于系统应掌握的通用知识，不应要求每个项目重复配置。

### 原则二：硬编码的是“工具链契约”，不是“项目命名习惯”

可以内置：

```text
Cargo crate 根目录下的 target/
Maven 模块根目录下的 target/
Gradle 模块根目录下的 build/
.NET 项目根目录下的 bin/ 和 obj/
Protobuf 的 *.pb.go、*_pb2.py
Go 官方生成文件头
```

不应仅凭普通名称全局排除：

```text
任意位置的 build/
任意位置的 target/
任意位置的 bin/
任意名为 generated 的业务模块
```

内置规则可以复杂，但必须对用户透明。

### 原则三：优先使用多证据硬判，不把模糊模式交给用户

当文件名本身不够确定时，系统应组合：

```text
目录位置
+ 工程根识别
+ 构建清单
+ 文件头
+ 工具链输出规则
+ Git 忽略状态
+ 源 IDL 对应关系
```

例如不应简单写：

```text
**/build/** → 排除
```

而应写成：

```text
Gradle 模块根 + build/** → 排除
CMake 构建根 + CMakeCache.txt → 排除
Node 包根 + package.json 指定输出目录 → 排除
```

### 原则四：所有分析器共用同一份过滤结果

过滤必须发生在分析器之前，并生成统一文件计划。

SCIP、Tree-sitter、启发式分析、文本切片和向量化都只能处理已通过过滤的文件。

### 原则五：允许极少数高级纠错，但不依赖纠错配置

内置规则必须追求高准确率，但任何复杂系统都可能存在边界误判。

因此保留：

```yaml
advanced:
  forceInclude:
    - "特殊路径/**"
```

该字段：

- 不出现在默认模板；
- 不要求普通用户配置；
- 仅用于纠正内置规则误判；
- 使用时输出告警和审计记录；
- 不能覆盖安全规则。

---

## 4. 文件发现

### 4.1 Git 仓库

默认发现模式：

```text
Git 跟踪文件
+ 未被 .gitignore 忽略的工作区新增文件
```

建议底层使用：

```bash
git ls-files -z --cached --others --exclude-standard
```

优点：

- 本地构建产物通常天然被排除；
- 包管理器下载目录通常天然被排除；
- 未提交的新业务源码仍能进入代码地图；
- 不需要用户理解 Git 细节。

注意：

`.gitignore` 只是第一层候选过滤，不是最终生成代码识别依据。已提交的生成源码仍必须由内置规则排除。

### 4.2 非 Git 仓库

使用文件系统遍历，并强制启用：

- 隐藏路径过滤；
- 内置目录规则；
- 二进制检测；
- 文件大小限制；
- 符号链接边界检查；
- 生成文件检测。

---

## 5. 工程根识别

构建目录不能仅按目录名全局判断，系统应先识别工程或模块根。

常见根标记：

| 生态 | 根标记 |
|---|---|
| Go | `go.mod`, `go.work` |
| Node.js | `package.json` |
| Rust | `Cargo.toml` |
| Maven | `pom.xml` |
| Gradle | `build.gradle`, `build.gradle.kts`, `settings.gradle` |
| .NET | `*.csproj`, `*.fsproj`, `*.vbproj`, `*.sln` |
| CMake | `CMakeLists.txt`, `CMakeCache.txt` |
| Python | `pyproject.toml`, `setup.py`, `setup.cfg` |
| Swift | `Package.swift` |
| CocoaPods | `Podfile`, `Podfile.lock` |
| Bazel | `WORKSPACE`, `WORKSPACE.bazel`, `MODULE.bazel` |

对于 Monorepo，每个子模块都应建立独立的 `ProjectRoot`。

---

## 6. 内置硬排除规则

### 6.1 隐藏路径

在当前“只索引业务源码”的产品边界下，任意路径段以 `.` 开头，默认直接排除：

```text
.git/
.github/
.vscode/
.idea/
.devcontainer/
.circleci/
.cargo/
.gradle/
.next/
.nuxt/
```

原因：

- 这些目录主要承载工具、环境、流水线和状态信息；
- 不属于手写业务源码；
- 代码地图不是工程配置地图；
- 用户不应为排除这些目录进行额外配置。

其中 `.git/.svn/.hg` 属于不可恢复硬排除。

其他隐藏路径理论上允许通过高级 `forceInclude` 纠正极端误判，但默认全部排除。

### 6.2 版本控制元数据

```text
**/.git/**
**/.svn/**
**/.hg/**
```

永久排除，不允许覆盖。

### 6.3 操作系统和编辑器垃圾

```text
**/.DS_Store
**/Thumbs.db
**/*~
**/*.swp
**/*.swo
**/*.tmp
**/*.temp
```

### 6.4 缓存

Python：

```text
**/__pycache__/**
**/*.pyc
**/*.pyo
**/.pytest_cache/**
**/.mypy_cache/**
**/.ruff_cache/**
**/.tox/**
**/.nox/**
```

通用：

```text
**/.cache/**
**/.sass-cache/**
**/.eslintcache
```

### 6.5 第三方依赖

```text
**/node_modules/**
**/bower_components/**
**/jspm_packages/**
**/Pods/**
**/vendor/**
**/third_party/**
```

第三方依赖不进入项目代码地图。

即使依赖被提交到仓库，仍应排除。项目代码对外部依赖的引用，可保留为外部包或外部符号节点。

---

## 7. 生态感知的构建产物排除

### 7.1 Node.js / TypeScript

在 `package.json` 所在包根下默认排除：

```text
dist/
build/
out/
coverage/
.next/
.nuxt/
.svelte-kit/
.angular/
```

同时读取：

- `tsconfig.json` 的 `compilerOptions.outDir`；
- `package.json` 脚本；
- 常见构建工具配置中的输出目录。

动态发现的输出目录也直接排除。

### 7.2 Rust

在 `Cargo.toml` 所在 crate 根下排除：

```text
target/
```

若环境或 Cargo 配置指定了其他 target directory，同样排除。

### 7.3 Maven

在 `pom.xml` 所在模块根下排除：

```text
target/
```

同时读取 Maven build directory 配置。

### 7.4 Gradle

在 Gradle 模块根下排除：

```text
build/
.gradle/
```

### 7.5 .NET

在项目根下排除：

```text
bin/
obj/
```

这两个目录只有在识别为 .NET 项目根后才作为构建产物硬排除，不能全仓库按名字排除。

### 7.6 CMake

以下目录可直接识别为构建树：

- 包含 `CMakeCache.txt`；
- 包含 `CMakeFiles/`；
- 名称匹配 `cmake-build-*`；
- 由 CMake Presets 声明为 `binaryDir`。

整个构建树排除。

### 7.7 Bazel

排除 Bazel 工作区下的：

```text
bazel-bin/
bazel-out/
bazel-testlogs/
bazel-<workspace>/
```

以及识别为 Bazel 输出符号链接的路径。

### 7.8 Swift

在 `Package.swift` 根下排除：

```text
.build/
```

隐藏路径规则通常已覆盖。

---

## 8. 自动生成源码识别

生成源码识别采用三类证据：

```text
确定性文件名
确定性文件头
确定性生成目录
```

任意高置信度规则命中，即从全部分析流程中排除。

### 8.1 通用生成文件头

只扫描文件前 8 KB 或前 100 行。

高置信度标记包括：

```text
Code generated ... DO NOT EDIT.
Generated by ... DO NOT EDIT.
This file was automatically generated
This file is auto-generated
DO NOT EDIT THIS FILE
<auto-generated>
```

要求：

- 位于文件头区域；
- 位于注释中；
- 表达明确的自动生成含义；
- 不能在整个文件任意搜索 `generated`。

### 8.2 Go

确定性文件名：

```text
**/*.pb.go
**/*.grpc.pb.go
```

官方生成文件头正则：

```regex
^// Code generated .* DO NOT EDIT\.$
```

文件头命中后，无论文件名是什么，均排除。

这还能覆盖：

- stringer；
- mockgen；
- controller-gen；
- ent；
- sqlc；
- wire；
- 各类 Go 代码生成器。

### 8.3 Python

Protobuf / gRPC：

```text
**/*_pb2.py
**/*_pb2.pyi
**/*_pb2_grpc.py
```

缓存：

```text
**/__pycache__/**
**/*.pyc
**/*.pyo
```

其他生成源码优先通过文件头和生成目录识别，不使用过宽的 `*_generated.py` 作为唯一依据。

### 8.4 C / C++

Protobuf / gRPC：

```text
**/*.pb.cc
**/*.pb.h
**/*.grpc.pb.cc
**/*.grpc.pb.h
```

FlatBuffers：

```text
**/*_generated.h
```

Cap'n Proto：

```text
**/*.capnp.h
**/*.capnp.c++
```

Qt 自动生成：

```text
**/moc_*.cpp
**/moc_*.h
**/ui_*.h
**/qrc_*.cpp
```

Qt 规则仅在识别到 Qt 工程或生成目录时启用，避免全局误判。

### 8.5 Java / Kotlin

Java 和 Kotlin 的 Protobuf 文件名不具有足够统一的全局后缀，不使用：

```text
*Proto.java
*Grpc.java
*Kt.kt
```

作为单一硬排除依据。

通过以下方式识别：

- Maven/Gradle 生成源码目录；
- Protobuf Gradle/Maven 插件输出路径；
- 文件头标记；
- `.proto` 与生成输出映射。

常见生成目录：

```text
target/generated-sources/
build/generated/
build/generated/source/proto/
```

### 8.6 C#

不使用 `*.pb.cs` 作为 Protobuf 通用规则。

通过：

- `obj/`；
- 生成文件头；
- MSBuild `Generated` 项；
- Protobuf 编译输出；
- `*.g.cs`、`*.g.i.cs` 与项目生成上下文组合判断；
- `*.Designer.cs` 与设计器/项目元数据组合判断。

单独看到 `*.Designer.cs` 或 `*.g.cs` 时，不应脱离 .NET 工程上下文全局判断。

### 8.7 TypeScript / JavaScript

确定性产物：

```text
**/*.min.js
**/*.min.mjs
**/*.min.cjs
**/*.min.css
**/*.js.map
**/*.css.map
**/*.d.ts.map
```

构建输出优先通过包根和输出目录识别。

`.d.ts` 不应一概排除，因为可能是手写类型契约。

### 8.8 Dart

常见生成文件：

```text
**/*.g.dart
**/*.freezed.dart
**/*.gr.dart
**/*.mocks.dart
**/*.pb.dart
**/*.pbjson.dart
**/*.pbenum.dart
**/*.pbserver.dart
```

仅在 Dart/Flutter 工程中启用对应规则。

### 8.9 Objective-C / Swift

Protobuf：

```text
**/*.pbobjc.h
**/*.pbobjc.m
```

SwiftProtobuf：

```text
**/*.pb.swift
```

Swift 规则仅在检测到 SwiftProtobuf 生成上下文或文件头时启用。

### 8.10 Ruby

Protobuf：

```text
**/*_pb.rb
```

必须使用精确后缀，不使用过宽的 `*_pb*`。

---

## 9. 通用命名模式的处理

以下模式很常见，但仅靠文件名不能达到普适性要求：

```text
*_generated.*
*.generated.*
*.gen.*
*.g.*
generated/
gen/
```

它们不能作为全局无条件规则。

系统应结合至少一项额外证据：

- 文件头明确声明自动生成；
- 目录由构建工具声明；
- 目录位于已识别工程根的标准生成路径；
- 有源 IDL/Schema 映射；
- 文件由工具 manifest 声明；
- 文件被明确列入生成任务输出。

组合命中后，直接排除。

---

## 10. 二进制和不可解析文件

独立的 `BinaryDetector` 应在语言解析前执行。

检测依据：

- 已知二进制扩展名；
- 文件魔数；
- NUL 字节；
- MIME；
- 文件大小；
- 压缩格式。

典型排除：

```text
*.o
*.obj
*.a
*.lib
*.so
*.dylib
*.dll
*.exe
*.class
*.jar
*.war
*.ear
*.wasm
*.tflite
*.zip
*.tar
*.gz
*.7z
*.png
*.jpg
*.pdf
```

这些内容不属于源代码地图。

---

## 11. 手写 IDL 与生成代码的关系

手写 IDL 是业务语义源，可以索引：

```text
.proto
.thrift
.graphql
.fbs
.capnp
OpenAPI YAML/JSON
```

但从它们产生的代码不索引。

正确关系：

```text
业务源码
  → 引用 IDL 中的服务、消息或接口定义
```

而不是：

```text
业务源码
  → 大量生成类
  → 生成器样板实现
```

当业务源码引用生成符号时，代码地图可以把引用重定向到对应的手写 IDL 定义。

---

## 12. 配置设计

### 12.1 默认配置必须简单

推荐默认配置：

```yaml
version: 1

index:
  languages:
    - go
    - typescript
```

甚至语言也可以自动识别：

```yaml
version: 1

index: {}
```

### 12.2 可选的项目范围配置

仅当用户希望限制模块范围时使用：

```yaml
index:
  include:
    - "src/**"
    - "internal/**"

  exclude:
    - "examples/legacy/**"
    - "testdata/huge-fixtures/**"
```

职责：

- `include`：限制业务源码范围；
- `exclude`：排除系统无法通用识别的项目特有内容。

用户不应配置通用生成文件、依赖和构建目录。

### 12.3 高级误判纠正

不出现在默认模板中：

```yaml
index:
  advanced:
    forceInclude:
      - "src/.domain/**"
      - "third_party/company_owned_fork/**"
```

使用条件：

- 内置规则确实误判；
- 文件确认是一方手写业务源码；
- 系统记录规则覆盖行为；
- 后续应推动修正内置规则，而不是长期依赖配置。

### 12.4 不暴露分析器配置

默认配置不提供：

```yaml
scipExclude:
treeSitterExclude:
heuristicExclude:
```

SCIP、Tree-sitter 和启发式分析属于内部实现。

---

## 13. 统一过滤流程

```text
文件发现
  ↓
路径规范化与仓库边界检查
  ↓
版本控制、隐藏路径、缓存和依赖排除
  ↓
工程根识别
  ↓
生态构建目录识别
  ↓
确定性生成文件名识别
  ↓
生成文件头检测
  ↓
二进制和压缩文件检测
  ↓
项目 include / exclude
  ↓
高级 forceInclude 纠错
  ↓
生成唯一 FileIndexPlan
  ↓
SCIP / Tree-sitter / Heuristic / Chunk / Embedding
```

注意：

`forceInclude` 只能纠正可覆盖内置规则，不能绕过：

- 仓库边界；
- 版本控制内部目录；
- 二进制文件；
- 敏感文件；
- 不安全符号链接。

---

## 14. SCIP 的二次过滤

即使文件扫描阶段已经排除生成代码，SCIP 文件中仍可能包含这些文件，因为 SCIP 可能由外部工具提前生成。

SCIP 导入时必须：

1. 将 SCIP Document URI 规范化为仓库相对路径；
2. 查询统一 `FileIndexPlan`；
3. 未进入索引集合的文件不导入；
4. 该文件中的 Symbol、Occurrence、Relationship 一并丢弃；
5. 指向被排除生成代码的引用，尝试回溯到 IDL 或保留为未展开外部符号。

不能允许 SCIP 把已排除文件重新带入代码地图。

---

## 15. Tree-sitter 和启发式分析

Tree-sitter 只处理：

```text
FileIndexPlan.Indexed = true
```

启发式分析同样只处理已索引的一方手写源码。

系统不应让某个分析器拥有独立的“重新纳入文件”能力。

---

## 16. 推荐数据结构

```go
type ExcludeKind string

const (
    ExcludeVCSMetadata      ExcludeKind = "VCS_METADATA"
    ExcludeHiddenPath       ExcludeKind = "HIDDEN_PATH"
    ExcludeDependency       ExcludeKind = "DEPENDENCY"
    ExcludeBuildArtifact    ExcludeKind = "BUILD_ARTIFACT"
    ExcludeGeneratedSource  ExcludeKind = "GENERATED_SOURCE"
    ExcludeCache            ExcludeKind = "CACHE"
    ExcludeMinified         ExcludeKind = "MINIFIED"
    ExcludeBinary           ExcludeKind = "BINARY"
    ExcludeUserConfigured   ExcludeKind = "USER_CONFIGURED"
)

type Evidence struct {
    Kind  string
    Value string
}

type FileIndexPlan struct {
    Path       string
    Language   string
    Indexed    bool
    RuleID     string
    Kind       ExcludeKind
    Reason     string
    Evidence   []Evidence
    Overridden bool
}
```

分析器计划应基于 `Indexed` 生成：

```go
type AnalyzerPlan struct {
    RunSCIP       bool
    RunTreeSitter bool
    RunHeuristic  bool
}
```

---

## 17. 内置规则表示

不要继续使用无法表达上下文的单一字符串数组：

```go
var builtInIndexExcludes = []string{ ... }
```

建议：

```go
type ExcludeRule struct {
    ID          string
    Description string
    Ecosystem   string
    Match       RuleMatcher
    Kind        ExcludeKind
    Confidence  int
    Overridable bool
}
```

示例：

```go
ExcludeRule{
    ID:          "generated.protobuf.go",
    Description: "Go source generated by protoc",
    Ecosystem:   "go",
    Match:       Glob("**/*.pb.go"),
    Kind:        ExcludeGeneratedSource,
    Confidence:  100,
    Overridable: true,
}
```

上下文规则：

```go
ExcludeRule{
    ID:          "build.maven.target",
    Description: "Maven module build output",
    Ecosystem:   "java",
    Match:       ProjectRelativeDir("maven", "target"),
    Kind:        ExcludeBuildArtifact,
    Confidence:  100,
    Overridable: true,
}
```

---

## 18. 规则进入内置清单的标准

一个规则只有满足以下条件，才可进入内置规则集：

### A. 工具链契约明确

规则来自：

- 官方文件命名；
- 官方输出目录；
- 官方生成文件头；
- 构建配置的明确输出字段。

### B. 跨项目稳定

同一工具链和生态中可复用，不依赖单个项目命名习惯。

### C. 能证明不是一方手写业务源码

通过：

- 确定性文件名；
- 确定性文件头；
- 确定性工程上下文；
- 确定性构建输出映射。

### D. 可自动测试

每条规则必须具有：

- 正例；
- 反例；
- 误伤测试；
- 多平台路径测试；
- Monorepo 测试。

### E. 可解释

每次排除必须输出：

```text
文件
规则 ID
排除类型
命中证据
是否可覆盖
```

---

## 19. 测试策略

### 19.1 正例

```text
Go *.pb.go 被排除
Python *_pb2.py 被排除
Maven 模块 target/ 被排除
Rust crate target/ 被排除
.NET 项目 obj/ 被排除
Node 包 node_modules/ 被排除
CMake build tree 被排除
带官方生成文件头的源码被排除
```

### 19.2 反例

```text
src/target/device.c 不应因名称 target 被排除
src/build/planner.go 不应因名称 build 被排除
types/global.d.ts 不应被排除
手写 test 源码不应因位于 tests/ 被排除
普通 BarKt.kt 不应仅因 Kt 后缀被排除
手写 Designer.cs 不应脱离 .NET 上下文被排除
```

### 19.3 回归仓库

建议使用真实开源仓库构建测试集：

- Go；
- C/C++；
- Java/Gradle/Maven；
- TypeScript/Node；
- Python；
- Rust；
- .NET；
- Flutter/Dart；
- 多语言 Monorepo。

每次规则变化统计：

```text
新增排除文件数
新增保留文件数
手写源码误排数
生成代码漏排数
规则命中分布
```

---

## 20. 验收标准

### 用户体验

- 新项目无需配置排除规则即可使用；
- 默认配置不出现 SCIP、Tree-sitter 等内部术语；
- 常见生成代码、依赖和构建产物自动排除；
- 用户只为项目特有目录配置 `exclude`；
- 高级纠错配置不进入默认模板。

### 准确性

```text
一方手写源码误排率 < 0.1%
常见生成源码漏排率 < 1%
常见依赖目录漏排率接近 0
常见构建产物漏排率 < 1%
所有排除结果可解释率 = 100%
```

### 性能

- 文件头检测只扫描前 8 KB；
- 已命中目录硬排除的文件不读取内容；
- 工程根只解析一次并缓存；
- 规则预编译；
- SCIP 导入使用路径集合 O(1) 查询。

---

## 21. 实施顺序

### 第一阶段：规则收敛

1. 明确“只索引一方手写业务源码”；
2. 删除生成代码 `SUMMARY` 和依赖 `BOUNDARY` 方案；
3. 隐藏路径默认硬编码排除；
4. 内置依赖、缓存和垃圾文件规则；
5. 修正 Protobuf 等语言规则；
6. 从默认配置中删除分析器级字段。

### 第二阶段：生态感知

1. 工程根识别；
2. Maven/Gradle/Cargo/.NET/Node/CMake 输出目录识别；
3. 读取构建配置中的动态输出目录；
4. 支持 Monorepo 多模块根。

### 第三阶段：生成代码检测

1. 通用文件头检测；
2. Go 官方生成标记；
3. 常见语言和生成器规则；
4. IDL 到生成文件映射；
5. SCIP 导入二次过滤。

### 第四阶段：质量闭环

1. `index explain <path>`；
2. 排除报告；
3. 真实仓库回归集；
4. 规则版本管理；
5. 误判反馈驱动内置规则升级。

---

## 22. 最终配置建议

默认配置：

```yaml
version: 1

index:
  languages:
    - go
    - typescript
```

项目确有特殊范围时：

```yaml
version: 1

index:
  languages:
    - go
    - typescript

  include:
    - "src/**"
    - "internal/**"

  exclude:
    - "examples/legacy/**"
    - "testdata/large-fixtures/**"
```

极少数内置误判：

```yaml
version: 1

index:
  advanced:
    forceInclude:
      - "src/.domain/**"
```

---

## 23. 最终结论

AstraMap 的索引过滤能力应遵循：

```text
系统掌握通用工具链知识
用户只表达项目特例
```

而不是：

```text
系统只提供 glob
用户负责告诉系统什么是垃圾
```

最终模型：

```text
代码地图
= 一方手写业务源码
- 自动生成源码
- 第三方依赖
- 构建产物
- 缓存和临时文件
- 隐藏工具数据
- 二进制与压缩文件
```

其中：

- 通用规则必须内置；
- 模糊规则通过工程上下文和多证据判断；
- 配置不是正常识别机制，而是项目特例和极端误判的补充；
- SCIP、Tree-sitter 等分析器不能绕过统一过滤；
- 所有自动生成或工具产出文件一律不进入代码地图。
