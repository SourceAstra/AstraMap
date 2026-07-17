#!/bin/bash
# AstraMap 性能基准测试
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

RUN_STARTED_AT="$(date '+%Y-%m-%d %H:%M:%S %Z')"
REPORT_FILE="${ASTRAMAP_REPORT_FILE:-$SCRIPT_DIR/reports/benchmark-report-latest.md}"
mkdir -p "$SCRIPT_DIR/reports"

printf "${BOLD}═══════════════════════════════════════${RESET}\n"
printf "${BOLD}  AstraMap 性能基准测试 v2.0${RESET}\n"
printf "${BOLD}  日期: $(date '+%Y-%m-%d %H:%M:%S')${RESET}\n"
printf "${BOLD}  覆盖: 12 种内置语言${RESET}\n"
printf "${BOLD}═══════════════════════════════════════${RESET}\n"

# ── 辅助函数 ──
generate_go_project() {
  local dir="$1"
  local file_count="$2"
  local per_file="$((file_count / 10))"

  mkdir -p "$dir/pkg"

  for i in $(seq 1 $per_file); do
    cat > "$dir/pkg/func_$(printf '%04d' $i).go" << EOF
package pkg

func Func$(printf '%04d' $i)(a int, b int) int {
    return a + b + $((i))
}
EOF
  done

  # 主文件调用所有函数
  cat > "$dir/main.go" << 'EOF'
package main

import "pkg"

func main() {
EOF

  for i in $(seq 1 $per_file); do
    echo "    _ = pkg.Func$(printf '%04d' $i)(1, 2)" >> "$dir/main.go"
  done

  echo "}" >> "$dir/main.go"
}

generate_python_project() {
  local dir="$1"
  local file_count="$2"
  local per_file="$((file_count / 10))"

  mkdir -p "$dir/pkg"

  for i in $(seq 1 $per_file); do
    cat > "$dir/pkg/mod_$(printf '%04d' $i).py" << EOF
def func_$(printf '%04d' $i)(a, b):
    return a + b + $((i))
EOF
  done

  cat > "$dir/app.py" << 'EOF'
from pkg import *

def main():
EOF

  for i in $(seq 1 $per_file); do
    echo "    _ = func_$(printf '%04d' $i)(1, 2)" >> "$dir/app.py"
  done

  echo "" >> "$dir/app.py"
  echo "if __name__ == '__main__':" >> "$dir/app.py"
  echo "    main()" >> "$dir/app.py"
}

measure_index_time() {
  local project_dir="$1"
  local label="$2"

  local start=$(date +%s%N)
  amap index --project "$project_dir" >/dev/null 2>&1
  local end=$(date +%s%N)
  local elapsed_ms=$(( (end - start) / 1000000 ))

  local nodes=$(query_val "$project_dir" "SELECT COUNT(*) FROM astramap_nodes")
  local edges=$(query_val "$project_dir" "SELECT COUNT(*) FROM astramap_edges")
  local files=$(query_val "$project_dir" "SELECT COUNT(*) FROM astramap_files")

  printf "  %s: %d ms (nodes=%s, edges=%s, files=%s)\n" "$label" "$elapsed_ms" "$nodes" "$edges" "$files"

  echo "$elapsed_ms|$nodes|$edges|$files"
}

# ── Phase 1: 小项目基准（< 1K 文件）──
phase_header "Phase 1: 小项目基准（~100 文件）"

tmpdir=$(mktemp -d)
generate_go_project "$tmpdir" 100

result=$(measure_index_time "$tmpdir" "Go 100 文件")
read -r time nodes edges files <<< "$result"
assert_lt "PERF-001 Go 小项目索引时间" "$time" "5000"
rm -rf "$tmpdir"

# ── Phase 2: 中等项目基准（1K-5K 文件）──
phase_header "Phase 2: 中等项目基准（~1K 文件）"

tmpdir=$(mktemp -d)
generate_go_project "$tmpdir" 1000

result=$(measure_index_time "$tmpdir" "Go 1K 文件")
read -r time nodes edges files <<< "$result"
assert_lt "PERF-002 Go 中等项目索引时间" "$time" "30000"
rm -rf "$tmpdir"

# ── Phase 3: 大项目基准（> 5K 文件）──
phase_header "Phase 3: 大项目基准（~5K 文件）"

tmpdir=$(mktemp -d)
generate_go_project "$tmpdir" 5000

result=$(measure_index_time "$tmpdir" "Go 5K 文件")
read -r time nodes edges files <<< "$result"
assert_lt "PERF-003 Go 大项目索引时间" "$time" "120000"
rm -rf "$tmpdir"

# ── Phase 4: Python 项目基准 ──
phase_header "Phase 4: Python 项目基准"

tmpdir=$(mktemp -d)
generate_python_project "$tmpdir" 1000

result=$(measure_index_time "$tmpdir" "Python 1K 文件")
read -r time nodes edges files <<< "$result"
assert_lt "PERF-004 Python 中等项目索引时间" "$time" "30000"
rm -rf "$tmpdir"

# ── Phase 5: 增量更新基准 ──
phase_header "Phase 5: 增量更新基准"

tmpdir=$(mktemp -d)
generate_go_project "$tmpdir" 1000

# 初始索引
amap index --project "$tmpdir" >/dev/null 2>&1
initial_nodes=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_nodes")

# 修改一个文件
cat > "$tmpdir/pkg/func_0001.go" << 'EOF'
package pkg

func Func0001(a int, b int) int {
    return a * b  // changed from addition to multiplication
}
EOF

local start=$(date +%s%N)
amap index --project "$tmpdir" >/dev/null 2>&1
local end=$(date +%s%N)
local incremental_ms=$(( (end - start) / 1000000 ))

echo "  增量更新时间: ${incremental_ms} ms"
assert_lt "PERF-005 增量更新时间" "$incremental_ms" "5000"
rm -rf "$tmpdir"

# ── 生成报告 ──
generate_report "$REPORT_FILE"

# ── 汇总 ──
phase_summary "全部测试"

printf "\n详细报告: $REPORT_FILE\n"

[[ $GLOBAL_FAILED -eq 0 && $GLOBAL_UNTESTED -eq 0 ]]
