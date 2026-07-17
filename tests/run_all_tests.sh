#!/bin/bash
# AstraMap 全量测试 — 主入口
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

RUN_STARTED_AT="$(date '+%Y-%m-%d %H:%M:%S %Z')"
REPORT_FILE="${ASTRAMAP_REPORT_FILE:-$SCRIPT_DIR/reports/full-report-latest.md}"
mkdir -p "$SCRIPT_DIR/reports"

printf "${BOLD}═══════════════════════════════════════${RESET}\n"
printf "${BOLD}  AstraMap 全量测试 v2.0${RESET}\n"
printf "${BOLD}  日期: $(date '+%Y-%m-%d %H:%M:%S')${RESET}\n"
printf "${BOLD}  覆盖: 12 种内置语言${RESET}\n"
printf "${BOLD}═══════════════════════════════════════${RESET}\n"

# ── Phase 1: 单元测试 ──
phase_header "Phase 1: 单元测试（语言识别 + 定义提取）"

bash "$SCRIPT_DIR/run_unit_tests.sh"
if [[ -f /tmp/astramap_unit_results.txt ]]; then
  source /tmp/astramap_unit_results.txt
fi
UNIT_PASSED=${GLOBAL_PASSED:-0}
UNIT_FAILED=${GLOBAL_FAILED:-0}
UNIT_UNTESTED=${GLOBAL_UNTESTED:-0}
UNIT_TOTAL=${GLOBAL_TOTAL:-0}

# 重置计数器
GLOBAL_TOTAL=0
GLOBAL_PASSED=0
GLOBAL_FAILED=0
GLOBAL_UNTESTED=0
TEST_RESULTS=()

# ── Phase 2: 集成测试 ──
phase_header "Phase 2: 集成测试（跨文件调用 + 接口实现）"

bash "$SCRIPT_DIR/run_integration_tests.sh"
if [[ -f /tmp/astramap_int_results.txt ]]; then
  source /tmp/astramap_int_results.txt
fi
INT_PASSED=${GLOBAL_PASSED:-0}
INT_FAILED=${GLOBAL_FAILED:-0}
INT_UNTESTED=${GLOBAL_UNTESTED:-0}
INT_TOTAL=${GLOBAL_TOTAL:-0}

# 重置计数器
GLOBAL_TOTAL=0
GLOBAL_PASSED=0
GLOBAL_FAILED=0
GLOBAL_UNTESTED=0
TEST_RESULTS=()

# ── Phase 3: 性能基准（可选，通过参数控制）──
if [[ "${1:-}" == "--benchmark" ]]; then
  phase_header "Phase 3: 性能基准测试"
  bash "$SCRIPT_DIR/run_benchmarks.sh"
  BENCH_PASSED=$GLOBAL_PASSED
  BENCH_FAILED=$GLOBAL_FAILED
  BENCH_UNTESTED=$GLOBAL_UNTESTED
else
  echo ""
  echo "跳过性能基准测试（使用 --benchmark 参数运行）"
fi

# ── 汇总 ──
phase_summary "全部测试"

# 计算总体指标
TOTAL_PASSED=$((UNIT_PASSED + INT_PASSED))
TOTAL_FAILED=$((UNIT_FAILED + INT_FAILED))
TOTAL_UNTESTED=$((UNIT_UNTESTED + INT_UNTESTED))
TOTAL_TOTAL=$((UNIT_TOTAL + INT_TOTAL))
TOTAL_TESTED=$((TOTAL_PASSED + TOTAL_FAILED))

if [[ $TOTAL_TESTED -gt 0 ]]; then
  PASS_RATE=$(awk "BEGIN {printf \"%.1f\", $TOTAL_PASSED/$TOTAL_TESTED*100}")
else
  PASS_RATE="0.0"
fi

echo ""
printf "${BOLD}═══════════════════════════════════════${RESET}\n"
printf "${BOLD}  测试汇总${RESET}\n"
printf "${BOLD}═══════════════════════════════════════${RESET}\n"
printf "  单元测试: ${GREEN}${UNIT_PASSED} passed${RESET}, ${RED}${UNIT_FAILED} failed${RESET}, ${YELLOW}${UNIT_UNTESTED} untested${RESET}\n"
printf "  集成测试: ${GREEN}${INT_PASSED} passed${RESET}, ${RED}${INT_FAILED} failed${RESET}, ${YELLOW}${INT_UNTESTED} untested${RESET}\n"
printf "  ─────────────────────────────────────────\n"
printf "  总计: ${GREEN}${TOTAL_PASSED} passed${RESET}, ${RED}${TOTAL_FAILED} failed${RESET}, ${YELLOW}${TOTAL_UNTESTED} untested${RESET}\n"
printf "  通过率: %s%%\n" "$PASS_RATE"
printf "${BOLD}═══════════════════════════════════════${RESET}\n"

# 生成最终报告
{
  printf '# AstraMap 全量测试报告\n\n'
  printf -- '- **执行时间**: %s\n' "$RUN_STARTED_AT"
  printf -- '- **结论**: **%s**\n\n' "$([ $TOTAL_FAILED -eq 0 ] && echo '通过' || echo '失败')"
  printf '## 汇总\n\n'
  printf '| 阶段 | 通过 | 失败 | 未测试 |\n'
  printf '|---|---:|---:|---:|\n'
  printf '| 单元测试 | %d | %d | %d |\n' "$UNIT_PASSED" "$UNIT_FAILED" "$UNIT_UNTESTED"
  printf '| 集成测试 | %d | %d | %d |\n' "$INT_PASSED" "$INT_FAILED" "$INT_UNTESTED"
  if [[ "${1:-}" == "--benchmark" ]]; then
    printf '| 性能基准 | %d | %d | %d |\n' "$BENCH_PASSED" "$BENCH_FAILED" "$BENCH_UNTESTED"
  fi
  printf '| **总计** | **%d** | **%d** | **%d** |\n\n' "$TOTAL_PASSED" "$TOTAL_FAILED" "$TOTAL_UNTESTED"
  printf '## 详细结果\n\n'
  printf '| 状态 | 测试项 | 详情 |\n|---|---|---|\n'
  # 合并单元测试和集成测试的详细结果
  while IFS= read -r line; do
    [[ -n "$line" ]] || continue
    IFS='|' read -r status name detail <<< "$line"
    printf '| %s | %s | %s |\n' "$status" "$name" "$detail"
  done < <(cat /tmp/astramap_unit_details.txt /tmp/astramap_int_details.txt 2>/dev/null)
} > "$REPORT_FILE"

printf "\n详细报告: $REPORT_FILE\n"

[[ $TOTAL_FAILED -eq 0 && $TOTAL_UNTESTED -eq 0 ]]
