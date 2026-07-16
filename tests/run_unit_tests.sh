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
printf "${BOLD}  AstraMap 多语言单元测试 v2.0${RESET}\n"
printf "${BOLD}  日期: $(date '+%Y-%m-%d %H:%M:%S')${RESET}\n"
printf "${BOLD}═══════════════════════════════════════${RESET}\n\n"

# ── Phase 0: 环境检测 ──
phase_header "Phase 0: 环境检测"
check_scip_availability
amap_bin=$(get_amap_bin)
printf "  amap: %s\n" "$amap_bin"
python3 -c "import yaml; print('  PyYAML: OK')" 2>/dev/null || { echo "  PyYAML: 缺失，请安装 python3-yaml"; exit 1; }
echo ""

# ════════════════════════════════════════════════
# L1: 语言识别测试（7 项）
# ════════════════════════════════════════════════
phase_header "Phase 1: 语言识别测试 (L1, 7项)"

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

# DETECT-H-C: .h 文件在纯 C 项目中归 c
tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)
echo 'int add(int, int);' > "$tmpdir/util.h"
echo 'int main() { return 0; }' > "$tmpdir/main.c"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
lang=$(query_val "$tmpdir" "SELECT language FROM astramap_files WHERE path='util.h'")
assert_eq "DETECT-H-C .h(纯C项目)→c" "$lang" "c"
rm -rf "$tmpdir"

# DETECT-H-CPP: .h 文件在含 C++ 项目中不被索引（已知限制）
# tree-sitter 仅按扩展名识别，.h 在 C++ 项目中不被索引
tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)
echo 'int add(int, int);' > "$tmpdir/util.h"
echo 'int main() { return 0; }' > "$tmpdir/main.cpp"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
h_count=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path='util.h'")
assert_eq "DETECT-H-CPP .h(含C++项目)不被索引" "$h_count" "0"
rm -rf "$tmpdir"

phase_summary "Phase 1: 语言识别测试"

# ════════════════════════════════════════════════
# L2: 语法提取测试（6 语言 × basic.yaml）
# ════════════════════════════════════════════════
phase_header "Phase 2: 语法提取测试 (L2)"

for lang in go python typescript c cpp java; do
  run_fixture "$SCRIPT_DIR/languages/$lang/basic.yaml"
done

phase_summary "Phase 2: 语法提取测试"

# ════════════════════════════════════════════════
# L3: 语义解析测试（6 语言 × advanced.yaml + 场景夹具）
# ════════════════════════════════════════════════
phase_header "Phase 3: 语义解析测试 (L3)"

for lang in go python typescript c cpp java; do
  if [[ -f "$SCRIPT_DIR/languages/$lang/advanced.yaml" ]]; then
    run_fixture "$SCRIPT_DIR/languages/$lang/advanced.yaml"
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
echo 'int main() { return 0; }' > "$tmpdir/util.c"
"$amap_bin" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1
go_count=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE language='go'")
py_count=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE language='python'")
c_count=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE language='c'")
assert_gt "ERR-006 混合语言: Go文件>0" "$go_count" "0"
assert_gt "ERR-006 混合语言: Python文件>0" "$py_count" "0"
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
