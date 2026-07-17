# Third-party grammar notices

AstraMap links the following Tree-sitter grammars. Their upstream metadata declares the MIT
license; license files and copyright notices remain in the pinned Go modules where supplied.

| Grammar | Pinned module version |
|---|---|
| Rust | `github.com/tree-sitter/tree-sitter-rust v0.24.2` |
| C# | `github.com/tree-sitter/tree-sitter-c-sharp v0.23.5` |
| Kotlin | `github.com/fwcd/tree-sitter-kotlin v0.0.0-20260602151103-c8ac3d262724` |
| PHP | `github.com/tree-sitter/tree-sitter-php v0.24.2` |
| Bash | `github.com/tree-sitter/tree-sitter-bash v0.25.1` |

Ruby、Dart、Swift、Lua、Scala、Zig、Visual Basic grammar 不再链接到 Core。它们由独立
`.amaplang` 包分发，每个语言包必须携带自己的许可证和第三方声明。Visual Basic 的非规范
module path 修正仅存在于 `language-packs/go.mod`，不进入 Core 依赖图。
