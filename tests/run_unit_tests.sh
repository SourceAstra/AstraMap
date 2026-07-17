#!/bin/bash
# AstraMap 多语言单元测试 — 主入口
# 分层架构：L1 语言识别 → L2 语法提取 → L3 语义解析 → L4 边界容错
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

RUN_STARTED_AT="$(date '+%Y-%m-%d %H:%M:%S %Z')"
REPORT_FILE="${ASTRAMAP_REPORT_FILE:-$SCRIPT_DIR/reports/test-report-latest.md}"
mkdir -p "$SCRIPT_DIR/reports"

printf "${BOLD}═══════════════════════════════════════${RESET}\n"
printf "${BOLD}  AstraMap 多语言单元测试 v2.1${RESET}\n"
printf "${BOLD}  日期: $(date '+%Y-%m-%d %H:%M:%S')${RESET}\n"
printf "${BOLD}  覆盖: 12 种内置语言 + 7 种语言包 case${RESET}\n"
printf "${BOLD}═══════════════════════════════════════${RESET}\n\n"

# ── Phase 0: 环境检测 ──
phase_header "Phase 0: 环境检测"
check_scip_availability
amap_bin=$(get_amap_bin)
printf "  amap: %s\n" "$amap_bin"
python3 -c "import yaml; print('  PyYAML: OK')" 2>/dev/null || { echo "  PyYAML: 缺失，请安装 python3-yaml"; exit 1; }
echo ""

# ════════════════════════════════════════════════
# L1: 语言识别测试（12 项）
# ════════════════════════════════════════════════
phase_header "Phase 1: 语言识别测试 (L1, 12项)"

# DETECT-GO: .go 文件识别为 go（需 go.mod）
tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)
echo 'module test' > "$tmpdir/go.mod"
echo 'package main; func main() {}' > "$tmpdir/main.go"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
lang=$(query_val "$tmpdir" "SELECT language FROM astramap_files WHERE path='main.go'")
assert_eq "DETECT-GO .go→go" "$lang" "go"
rm -rf "$tmpdir"

# DETECT-TS: .ts 文件识别为 typescript
tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)
echo 'const x = 1' > "$tmpdir/index.ts"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
lang=$(query_val "$tmpdir" "SELECT language FROM astramap_files WHERE path='index.ts'")
assert_eq "DETECT-TS .ts→typescript" "$lang" "typescript"
rm -rf "$tmpdir"

# DETECT-JS: .js 文件识别为 javascript
tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)
echo 'const x = 1;' > "$tmpdir/index.js"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
lang=$(query_val "$tmpdir" "SELECT language FROM astramap_files WHERE path='index.js'")
assert_eq "DETECT-JS .js→javascript" "$lang" "javascript"
rm -rf "$tmpdir"

# DETECT-PY: .py 文件识别为 python
tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)
echo 'x = 1' > "$tmpdir/app.py"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
lang=$(query_val "$tmpdir" "SELECT language FROM astramap_files WHERE path='app.py'")
assert_eq "DETECT-PY .py→python" "$lang" "python"
rm -rf "$tmpdir"

# DETECT-JAVA: .java 文件识别为 java
tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)
echo 'public class Main {}' > "$tmpdir/Main.java"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
lang=$(query_val "$tmpdir" "SELECT language FROM astramap_files WHERE path='Main.java'")
assert_eq "DETECT-JAVA .java→java" "$lang" "java"
rm -rf "$tmpdir"

# DETECT-C: .c 文件识别为 c
tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)
echo 'int main() { return 0; }' > "$tmpdir/util.c"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
lang=$(query_val "$tmpdir" "SELECT language FROM astramap_files WHERE path='util.c'")
assert_eq "DETECT-C .c→c" "$lang" "c"
rm -rf "$tmpdir"

# DETECT-CPP: .cpp 文件识别为 cpp
tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)
echo 'int main() { return 0; }' > "$tmpdir/main.cpp"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
lang=$(query_val "$tmpdir" "SELECT language FROM astramap_files WHERE path='main.cpp'")
assert_eq "DETECT-CPP .cpp→cpp" "$lang" "cpp"
rm -rf "$tmpdir"

# DETECT-RUST: .rs 文件识别为 rust
tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)
echo 'fn main() {}' > "$tmpdir/main.rs"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
lang=$(query_val "$tmpdir" "SELECT language FROM astramap_files WHERE path='main.rs'")
assert_eq "DETECT-RUST .rs→rust" "$lang" "rust"
rm -rf "$tmpdir"

# DETECT-CS: .cs 文件识别为 csharp
tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)
echo 'class Program { static void Main() {} }' > "$tmpdir/Program.cs"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
lang=$(query_val "$tmpdir" "SELECT language FROM astramap_files WHERE path='Program.cs'")
assert_eq "DETECT-CS .cs→csharp" "$lang" "csharp"
rm -rf "$tmpdir"

# DETECT-KT: .kt 文件识别为 kotlin
tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)
echo 'fun main() {}' > "$tmpdir/Main.kt"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
lang=$(query_val "$tmpdir" "SELECT language FROM astramap_files WHERE path='Main.kt'")
assert_eq "DETECT-KT .kt→kotlin" "$lang" "kotlin"
rm -rf "$tmpdir"

# DETECT-PHP: .php 文件识别为 php
tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)
echo '<?php echo "hello";' > "$tmpdir/index.php"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
lang=$(query_val "$tmpdir" "SELECT language FROM astramap_files WHERE path='index.php'")
assert_eq "DETECT-PHP .php→php" "$lang" "php"
rm -rf "$tmpdir"

# DETECT-BASH: .sh 文件识别为 bash
tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)
echo '#!/bin/bash\nfunction greet() { echo "hello"; }' > "$tmpdir/script.sh"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
lang=$(query_val "$tmpdir" "SELECT language FROM astramap_files WHERE path='script.sh'")
assert_eq "DETECT-BASH .sh→bash" "$lang" "bash"
rm -rf "$tmpdir"

# DETECT-H-C: .h 文件在纯 C 项目中归 c
tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)
echo 'int add(int, int);' > "$tmpdir/util.h"
echo 'int main() { return 0; }' > "$tmpdir/main.c"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
lang=$(query_val "$tmpdir" "SELECT language FROM astramap_files WHERE path='util.h'")
assert_eq "DETECT-H-C .h(纯C项目)→c" "$lang" "c"
rm -rf "$tmpdir"

# DETECT-H-CPP: .h 文件在含 C++ 项目中的归属是已知限制
# tree-sitter 仅按扩展名识别，无法区分 C/C++ 头文件，需编译上下文
assert_untested "DETECT-H-CPP .h(含C++项目)归属" "tree-sitter cannot distinguish C/C++ .h without compile context"

phase_summary "Phase 1: 语言识别测试"

# ════════════════════════════════════════════════
# L2: 语法提取测试（12 内置语言 + 7 语言包 basic.yaml）
# ════════════════════════════════════════════════
phase_header "Phase 2: 语法提取测试 (L2)"

for lang in go python typescript javascript c cpp java rust csharp kotlin php bash; do
  run_fixture "$SCRIPT_DIR/languages/$lang/basic.yaml"
done
for lang in ruby dart swift lua scala zig visualbasic; do
  if astramap_language_active "$lang"; then
    run_fixture "$SCRIPT_DIR/languages/$lang/basic.yaml"
  else
    assert_untested "[$lang] language package basic case" "语言包未安装或未激活"
  fi
done

phase_summary "Phase 2: 语法提取测试"

# ════════════════════════════════════════════════
# L3: 语义解析测试（内置语言 advanced.yaml + edge_cases.yaml + 已激活语言包 advanced.yaml + 场景夹具）
# ════════════════════════════════════════════════
phase_header "Phase 3: 语义解析测试 (L3)"

for lang in go python typescript javascript c cpp java rust csharp kotlin php bash; do
  if [[ -f "$SCRIPT_DIR/languages/$lang/advanced.yaml" ]]; then
    run_fixture "$SCRIPT_DIR/languages/$lang/advanced.yaml"
  fi
  if [[ -f "$SCRIPT_DIR/languages/$lang/edge_cases.yaml" ]]; then
    run_fixture "$SCRIPT_DIR/languages/$lang/edge_cases.yaml"
  fi
done
for lang in ruby dart swift lua scala zig visualbasic; do
  if astramap_language_active "$lang" && [[ -f "$SCRIPT_DIR/languages/$lang/advanced.yaml" ]]; then
    run_fixture "$SCRIPT_DIR/languages/$lang/advanced.yaml"
  fi
  if astramap_language_active "$lang" && [[ -f "$SCRIPT_DIR/languages/$lang/edge_cases.yaml" ]]; then
    run_fixture "$SCRIPT_DIR/languages/$lang/edge_cases.yaml"
  fi
done

# 跨文件场景
for scenario in "$SCRIPT_DIR"/scenarios/*.yaml; do
  if [[ -f "$scenario" ]]; then
    run_fixture "$scenario"
  fi
done

phase_summary "Phase 3: 语义解析测试"

# ════════════════════════════════════════════════
# L4: 边界容错测试（7 项，语言无关）
# ════════════════════════════════════════════════
phase_header "Phase 4: 边界容错测试 (L4, 7项)"

# ERR-001: 空文件跳过
tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)
touch "$tmpdir/empty.c"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
node_count=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_nodes")
assert_eq "ERR-001 空文件: 0节点" "$node_count" "0"
rm -rf "$tmpdir"

# ERR-002: 纯注释文件无符号
tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)
echo '// This is a comment only file' > "$tmpdir/comment.c"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
node_count=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_nodes")
assert_eq "ERR-002 纯注释文件: 0节点" "$node_count" "0"
rm -rf "$tmpdir"

# ERR-003: 语法错误文件不崩溃（amap 优雅处理，exit=0）
tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)
echo 'func { broken syntax' > "$tmpdir/broken.c"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
exit_code=$?
assert_eq "ERR-003 语法错误: 不崩溃(exit=0)" "$exit_code" "0"
rm -rf "$tmpdir"

# ERR-004: 二进制文件跳过
tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)
printf '\x89PNG\r\n\x1a\n' > "$tmpdir/image.png"
echo 'int main() { return 0; }' > "$tmpdir/main.c"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
png_count=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path='image.png'")
assert_eq "ERR-004 二进制文件: 跳过" "$png_count" "0"
rm -rf "$tmpdir"

# ERR-005: 无效 UTF-8 不崩溃
tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)
printf 'int main() { return \xff\xfe; }' > "$tmpdir/bad_utf8.c"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
exit_code=$?
assert_eq "ERR-005 无效UTF-8: 不崩溃(exit=0)" "$exit_code" "0"
rm -rf "$tmpdir"

# ERR-006: 混合语言项目各语言独立索引（Go 需 go.mod）
tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)
echo 'module test' > "$tmpdir/go.mod"
echo 'package main; func main() {}' > "$tmpdir/main.go"
echo 'def hello(): pass' > "$tmpdir/app.py"
echo 'const x = 1;' > "$tmpdir/index.js"
echo 'int main() { return 0; }' > "$tmpdir/util.c"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
go_count=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE language='go'")
py_count=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE language='python'")
js_count=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE language='javascript'")
c_count=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE language='c'")
assert_gt "ERR-006 混合语言: Go文件>0" "$go_count" "0"
assert_gt "ERR-006 混合语言: Python文件>0" "$py_count" "0"
assert_gt "ERR-006 混合语言: JavaScript文件>0" "$js_count" "0"
assert_gt "ERR-006 混合语言: C文件>0" "$c_count" "0"
rm -rf "$tmpdir"

# ERR-007: 隐藏目录文件被排除
tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)
mkdir -p "$tmpdir/.hidden"
echo 'int secret() { return 0; }' > "$tmpdir/.hidden/secret.c"
echo 'int main() { return 0; }' > "$tmpdir/main.c"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
hidden_count=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE '.hidden/%'")
assert_eq "ERR-007 隐藏目录: 排除" "$hidden_count" "0"
rm -rf "$tmpdir"

phase_summary "Phase 4: 边界容错测试"

# ── 生成报告 ──
generate_report "$REPORT_FILE"

# ── 汇总 ──
phase_summary "全部测试"

printf "\n详细报告: $REPORT_FILE\n"

[[ $GLOBAL_FAILED -eq 0 && $GLOBAL_UNTESTED -eq 0 ]]
