module astramap-language-packs

go 1.25.0

require (
	astramap-standalone v0.0.0
	github.com/UserNobody14/tree-sitter-dart v0.0.0-20260707040301-be07cf7118d3
	github.com/alex-pinkus/tree-sitter-swift v0.0.0-20260704222518-28fe3a8a8558
	github.com/tree-sitter-grammars/tree-sitter-lua v0.5.0
	github.com/tree-sitter-grammars/tree-sitter-zig v1.1.2
	github.com/tree-sitter/go-tree-sitter v0.25.0
	github.com/tree-sitter/tree-sitter-ruby v0.23.1
	github.com/tree-sitter/tree-sitter-scala v0.26.0
	github.com/tree-sitter/tree-sitter-tree_sitter_vb_dotnet v0.0.0-20250728102902-cfca210ce8fd
)

require github.com/mattn/go-pointer v0.0.1 // indirect

replace astramap-standalone => ..

replace github.com/tree-sitter/tree-sitter-tree_sitter_vb_dotnet => github.com/CodeAnt-AI/tree-sitter-vb-dotnet v0.0.0-20250728102902-cfca210ce8fd
