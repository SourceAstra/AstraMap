#!/bin/bash
# AstraMap 多语言测试框架 — 核心库
set -uo pipefail

# ── 颜色定义 ──
BOLD="\033[1m"
GREEN="\033[32m"
RED="\033[31m"
YELLOW="\033[33m"
GRAY="\033[2m"
CYAN="\033[36m"
RESET="\033[0m"

# ── 全局状态 ──
TEST_RESULTS=()
GLOBAL_TOTAL=0
GLOBAL_PASSED=0
GLOBAL_FAILED=0
GLOBAL_UNTESTED=0
CURRENT_FIXTURE=""

# ── SCIP 可用性 ──
SCIP_AVAILABLE_GO=false
SCIP_AVAILABLE_TS=false
SCIP_AVAILABLE_PY=false
SCIP_AVAILABLE_JAVA=false
SCIP_AVAILABLE_CLANG=false

# 语言识别扩展名映射
language_ext() {
  local lang="$1"
  case "$lang" in
    go) echo "go" ;;
    python) echo "py" ;;
    typescript) echo "ts" ;;
    javascript) echo "js" ;;
    c) echo "c" ;;
    cpp) echo "cpp" ;;
    java) echo "java" ;;
    rust) echo "rs" ;;
    csharp) echo "cs" ;;
    kotlin) echo "kt" ;;
    php) echo "php" ;;
    bash) echo "sh" ;;
    ruby) echo "rb" ;;
    dart) echo "dart" ;;
    swift) echo "swift" ;;
    lua) echo "lua" ;;
    scala) echo "scala" ;;
    zig) echo "zig" ;;
    visualbasic) echo "vb" ;;
    *) echo "" ;;
  esac
}

# 语言包必须已安装且处于 active lock，测试不得把未安装包伪装成通过。
astramap_language_active() {
  local language="$1"
  local packages
  packages=$("$(get_amap_bin)" language list --json 2>/dev/null) || return 1
  python3 -c '
import json, sys
items = json.loads(sys.argv[1])
language = sys.argv[2]
raise SystemExit(0 if any(item.get("id") == language and item.get("enabled") for item in items) else 1)
' "$packages" "$language" 2>/dev/null
}

# ── 辅助函数 ──

phase_header() {
  printf "${BOLD}═══════════════════════════════════════${RESET}\n"
  printf "${BOLD}  %s${RESET}\n" "$1"
  printf "${BOLD}═══════════════════════════════════════${RESET}\n"
}

phase_summary() {
  local phase="$1"
  local total=$((GLOBAL_TOTAL))
  local passed=$((GLOBAL_PASSED))
  local failed=$((GLOBAL_FAILED))
  local untested=$((GLOBAL_UNTESTED))

  printf "\n${BOLD}${phase} 汇总${RESET}\n"
  printf "  总用例: %d\n" "$total"
  printf "  ${GREEN}通过: %d${RESET}\n" "$passed"
  if [[ $failed -gt 0 ]]; then
    printf "  ${RED}失败: %d${RESET}\n" "$failed"
  fi
  if [[ $untested -gt 0 ]]; then
    printf "  ${YELLOW}未测试: %d${RESET}\n" "$untested"
  fi
  if [[ $total -gt 0 ]]; then
    local rate=$(awk "BEGIN {printf \"%.1f\", $passed/$total*100}")
    printf "  ${BOLD}通过率: %s%%${RESET}\n" "$rate"
  fi
}

# ── 断言函数 ──

assert_eq() {
  local name="$1"
  local actual="$2"
  local expected="$3"

  GLOBAL_TOTAL=$((GLOBAL_TOTAL + 1))

  if [[ "$actual" == "$expected" ]]; then
    GLOBAL_PASSED=$((GLOBAL_PASSED + 1))
    TEST_RESULTS+=("✓|$name|实际值匹配预期")
    printf "  ${GREEN}✓${RESET} %s\n" "$name"
  else
    GLOBAL_FAILED=$((GLOBAL_FAILED + 1))
    TEST_RESULTS+=("✗|$name|预期='$expected'，实际='$actual'")
    printf "  ${RED}✗${RESET} %s (预期='%s', 实际='%s')\n" "$name" "$expected" "$actual"
  fi
}

assert_ne() {
  local name="$1"
  local actual="$2"
  local unexpected="$3"

  GLOBAL_TOTAL=$((GLOBAL_TOTAL + 1))

  if [[ "$actual" != "$unexpected" ]]; then
    GLOBAL_PASSED=$((GLOBAL_PASSED + 1))
    TEST_RESULTS+=("✓|$name|值不等于预期")
    printf "  ${GREEN}✓${RESET} %s\n" "$name"
  else
    GLOBAL_FAILED=$((GLOBAL_FAILED + 1))
    TEST_RESULTS+=("✗|$name|值不应等于'$unexpected'")
    printf "  ${RED}✗${RESET} %s (值='%s'，不应等于)" "$name" "$actual"
  fi
}

assert_gt() {
  local name="$1"
  local actual="$2"
  local threshold="$3"

  GLOBAL_TOTAL=$((GLOBAL_TOTAL + 1))

  if [[ "$actual" -gt "$threshold" ]] 2>/dev/null; then
    GLOBAL_PASSED=$((GLOBAL_PASSED + 1))
    TEST_RESULTS+=("✓|$name|值大于阈值")
    printf "  ${GREEN}✓${RESET} %s (值=%s > %s)\n" "$name" "$actual" "$threshold"
  else
    GLOBAL_FAILED=$((GLOBAL_FAILED + 1))
    TEST_RESULTS+=("✗|$name|实际=$actual；要求>$threshold")
    printf "  ${RED}✗${RESET} %s (值=%s，应>%s)\n" "$name" "$actual" "$threshold"
  fi
}

assert_lt() {
  local name="$1"
  local actual="$2"
  local threshold="$3"

  GLOBAL_TOTAL=$((GLOBAL_TOTAL + 1))

  if [[ "$actual" -lt "$threshold" ]] 2>/dev/null; then
    GLOBAL_PASSED=$((GLOBAL_PASSED + 1))
    TEST_RESULTS+=("✓|$name|值小于阈值")
    printf "  ${GREEN}✓${RESET} %s (值=%s < %s)\n" "$name" "$actual" "$threshold"
  else
    GLOBAL_FAILED=$((GLOBAL_FAILED + 1))
    TEST_RESULTS+=("✗|$name|值应小于%s，实际=%s")
    printf "  ${RED}✗${RESET} %s (值=%s，应<%s)\n" "$name" "$actual" "$threshold"
  fi
}

assert_contains() {
  local name="$1"
  local container="$2"
  local item="$3"

  GLOBAL_TOTAL=$((GLOBAL_TOTAL + 1))

  if echo "$container" | grep -q "$item"; then
    GLOBAL_PASSED=$((GLOBAL_PASSED + 1))
    TEST_RESULTS+=("✓|$name|包含目标项")
    printf "  ${GREEN}✓${RESET} %s\n" "$name"
  else
    GLOBAL_FAILED=$((GLOBAL_FAILED + 1))
    TEST_RESULTS+=("✗|$name|不包含'$item'")
    printf "  ${RED}✗${RESET} %s (不包含'%s')\n" "$name" "$item"
  fi
}

assert_not_contains() {
  local name="$1"
  local container="$2"
  local item="$3"

  GLOBAL_TOTAL=$((GLOBAL_TOTAL + 1))

  if ! echo "$container" | grep -q "$item"; then
    GLOBAL_PASSED=$((GLOBAL_PASSED + 1))
    TEST_RESULTS+=("✓|$name|不包含目标项")
    printf "  ${GREEN}✓${RESET} %s\n" "$name"
  else
    GLOBAL_FAILED=$((GLOBAL_FAILED + 1))
    TEST_RESULTS+=("✗|$name|不应包含'$item'")
    printf "  ${RED}✗${RESET} %s (包含'%s')\n" "$name" "$item"
  fi
}

assert_untested() {
  local name="$1"
  local reason="$2"

  GLOBAL_TOTAL=$((GLOBAL_TOTAL + 1))
  GLOBAL_UNTESTED=$((GLOBAL_UNTESTED + 1))

  TEST_RESULTS+=("-|$name|$reason")
  printf "  ${GRAY}-${RESET} %s (%s)\n" "$name" "$reason"
}

# ── amap 二进制定位 ──

AMAP_BIN=""
get_amap_bin() {
  if [[ -z "$AMAP_BIN" ]]; then
    if [[ -x "./amap" ]]; then
      AMAP_BIN="$(pwd)/amap"
    elif command -v amap >/dev/null 2>&1; then
      AMAP_BIN=$(command -v amap)
    else
      echo "错误: 找不到 amap 二进制文件" >&2
      exit 1
    fi
  fi
  echo "$AMAP_BIN"
}

# ── SQLite 查询辅助 ──

# 在指定项目目录执行 SQL 查询，返回单值
query_val() {
  local project_dir="$1"
  local sql="$2"
  local db_path="$project_dir/.astramap/astramap.db"

  if [[ ! -f "$db_path" ]]; then
    echo "0"
    return
  fi

  # 使用 Python sqlite3 执行查询，通过参数化传递路径避免注入
  python3 -c "
import sqlite3, sys, os
db_path = os.path.join(sys.argv[1], '.astramap', 'astramap.db')
sql = sys.argv[2]
try:
    conn = sqlite3.connect(db_path)
    cursor = conn.cursor()
    cursor.execute(sql)
    result = cursor.fetchone()
    conn.close()
    if result is not None:
        print(result[0])
    else:
        print(0)
except Exception as e:
    print(f'SQL 查询失败: {e}; SQL={sql}', file=sys.stderr)
    raise SystemExit(1)
" "$project_dir" "$sql"
}

# 统计指定项目中匹配名称的节点数
count_nodes() {
  local project_dir="$1"
  local name="$2"
  query_val "$project_dir" "SELECT COUNT(*) FROM astramap_nodes WHERE name='$name'"
}

# 统计指定项目中匹配名称和 kind 的节点数
count_nodes_by_kind() {
  local project_dir="$1"
  local name="$2"
  local kind="$3"
  query_val "$project_dir" "SELECT COUNT(*) FROM astramap_nodes WHERE name='$name' AND kind='$kind'"
}

# 统计指定项目中匹配条件的调用边数
count_calls() {
  local project_dir="$1"
  local caller="$2"
  local callee="$3"
  local sql="SELECT COUNT(*) FROM astramap_edges WHERE kind='calls'"
  if [[ -n "$caller" ]]; then
    sql="$sql AND source LIKE '%$caller%'"
  fi
  if [[ -n "$callee" ]]; then
    sql="$sql AND target LIKE '%$callee%'"
  fi
  query_val "$project_dir" "$sql"
}

# 统计指定项目中匹配路径模式的文件数
count_files_like() {
  local project_dir="$1"
  local pattern="$2"
  query_val "$project_dir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE '${pattern}'"
}

# ── SCIP 可用性检测 ──

check_scip_availability() {
  command -v scip-go >/dev/null 2>&1 && SCIP_AVAILABLE_GO=true
  command -v scip-typescript >/dev/null 2>&1 && SCIP_AVAILABLE_TS=true
  command -v scip-python >/dev/null 2>&1 && SCIP_AVAILABLE_PY=true
  command -v scip-java >/dev/null 2>&1 && SCIP_AVAILABLE_JAVA=true
  command -v scip-clang >/dev/null 2>&1 && SCIP_AVAILABLE_CLANG=true

  printf "${CYAN}SCIP 可用性:${RESET}\n"
  printf "  scip-go: %s\n" "$($SCIP_AVAILABLE_GO && echo '✓' || echo '✗')"
  printf "  scip-typescript: %s\n" "$($SCIP_AVAILABLE_TS && echo '✓' || echo '✗')"
  printf "  scip-python: %s\n" "$($SCIP_AVAILABLE_PY && echo '✓' || echo '✗')"
  printf "  scip-java: %s\n" "$($SCIP_AVAILABLE_JAVA && echo '✓' || echo '✗')"
  printf "  scip-clang: %s\n" "$($SCIP_AVAILABLE_CLANG && echo '✓' || echo '✗')"
}

# ── YAML 解析（Python 辅助） ──

# 将 YAML 文件解析为 JSON，输出到 stdout
parse_yaml_to_json() {
  local yaml_file="$1"
  python3 -c "
import yaml, json, sys
with open(sys.argv[1]) as f:
    data = yaml.safe_load(f)
print(json.dumps(data))
" "$yaml_file" 2>/dev/null
}

# 从 JSON 中提取字段值
json_get() {
  local json="$1"
  local path="$2"
  python3 -c "
import json, sys
data = json.loads(sys.argv[1])
keys = sys.argv[2].split('.')
val = data
for k in keys:
    if isinstance(val, list) and k.isdigit():
        val = val[int(k)]
    elif isinstance(val, dict) and k in val:
        val = val[k]
    else:
        val = None
        break
if val is None:
    print('')
elif isinstance(val, (list, dict)):
    print(json.dumps(val))
else:
    print(str(val))
" "$json" "$path" 2>/dev/null
}

# 从 JSON 中提取数组长度
json_array_len() {
  local json="$1"
  local path="$2"
  python3 -c "
import json, sys
data = json.loads(sys.argv[1])
keys = sys.argv[2].split('.')
val = data
for k in keys:
    if isinstance(val, list) and k.isdigit():
        val = val[int(k)]
    elif isinstance(val, dict) and k in val:
        val = val[k]
    else:
        val = []
        break
print(len(val) if isinstance(val, list) else 0)
" "$json" "$path" 2>/dev/null
}

# ── 夹具执行引擎 ──

# 运行单个 YAML 夹具文件
# 流程：解析 YAML → 创建临时项目 → 索引 → 验证 → 清理
run_fixture() {
  local yaml_file="$1"

  if [[ ! -f "$yaml_file" ]]; then
    printf "  ${RED}错误: 夹具文件不存在: $yaml_file${RESET}\n"
    return 1
  fi

  # 解析 YAML 为 JSON
  local json=$(parse_yaml_to_json "$yaml_file")
  if [[ -z "$json" || "$json" == "None" ]]; then
    printf "  ${RED}错误: YAML 解析失败: $yaml_file${RESET}\n"
    return 1
  fi

  # 提取元数据
  local language=$(json_get "$json" "metadata.language")
  local description=$(json_get "$json" "metadata.description")
  local scip_required=$(json_get "$json" "metadata.scip_required")
  local fixture_count=$(json_array_len "$json" "fixtures")

  CURRENT_FIXTURE="$yaml_file"

  printf "${CYAN}  ▸ %s (%s, %d 夹具项)${RESET}\n" "$description" "$language" "$fixture_count"

  # SCIP 降级检查
  if [[ "$scip_required" == "True" ]]; then
    local scip_ok=false
    case "$language" in
      go) $SCIP_AVAILABLE_GO && scip_ok=true ;;
      typescript) $SCIP_AVAILABLE_TS && scip_ok=true ;;
      python) $SCIP_AVAILABLE_PY && scip_ok=true ;;
      java) $SCIP_AVAILABLE_JAVA && scip_ok=true ;;
      c|cpp) $SCIP_AVAILABLE_CLANG && scip_ok=true ;;
    esac
    if ! $scip_ok; then
      assert_untested "[$language] $description" "SCIP 不可用，跳过"
      return 0
    fi
  fi

  # 逐项执行夹具
  local i=0
  while [[ $i -lt $fixture_count ]]; do
    run_fixture_item "$json" "$i" "$language"
    i=$((i + 1))
  done
}

# 运行单个夹具项
run_fixture_item() {
  local json="$1"
  local idx="$2"
  local language="$3"

  # 提取夹具项字段
  local fixture_id=$(json_get "$json" "fixtures.$idx.id")
  local files_json=$(json_get "$json" "fixtures.$idx.files")
  local source=$(json_get "$json" "fixtures.$idx.source")
  local symbols_json=$(json_get "$json" "fixtures.$idx.expected.symbols")
  local calls_json=$(json_get "$json" "fixtures.$idx.expected.calls")

  # 创建临时项目
  local tmpdir=$(mktemp -d /tmp/astramap-test-XXXXXX)

  # 写入源文件
  if [[ -n "$files_json" && "$files_json" != "None" && "$files_json" != "" ]]; then
    # 多文件模式：files 是 {path: content, ...} 映射
    python3 -c "
import json, os, sys
data = json.loads(sys.argv[1])
base = sys.argv[2]
for path, content in data.items():
    full = os.path.join(base, path)
    os.makedirs(os.path.dirname(full), exist_ok=True)
    with open(full, 'w') as f:
        f.write(content)
" "$files_json" "$tmpdir" 2>/dev/null
  elif [[ -n "$source" && "$source" != "None" ]]; then
    # 单文件模式：source 字段（向后兼容旧格式）
    local ext=$(language_ext "$language")
    if [[ -n "$ext" ]]; then
      echo "$source" > "$tmpdir/test.$ext"
    fi
  fi

  # 索引（仅 tree-sitter，确保基础层可测）
  "$(get_amap_bin)" index --project "$tmpdir" --tree-sitter >/dev/null 2>&1

  # 验证符号
  if [[ -n "$symbols_json" && "$symbols_json" != "None" && "$symbols_json" != "" ]]; then
    validate_symbols "$tmpdir" "$language" "$fixture_id" "$symbols_json"
  fi

  # 验证调用
  if [[ -n "$calls_json" && "$calls_json" != "None" && "$calls_json" != "" ]]; then
    validate_calls "$tmpdir" "$language" "$fixture_id" "$calls_json"
  fi

  # 清理
  rm -rf -rf "$tmpdir"
}

# 验证符号预期
validate_symbols() {
  local project_dir="$1"
  local language="$2"
  local fixture_id="$3"
  local symbols_json="$4"

  local sym_count=$(python3 -c "
import json, sys
data = json.loads(sys.argv[1])
print(len(data) if isinstance(data, list) else 0)
" "$symbols_json" 2>/dev/null)

  local i=0
  while [[ $i -lt $sym_count ]]; do
    local name=$(json_get "$symbols_json" "$i.name")
    local kind=$(json_get "$symbols_json" "$i.kind")

    if [[ -n "$name" && "$name" != "None" ]]; then
      if [[ -n "$kind" && "$kind" != "None" ]]; then
        local count=$(count_nodes_by_kind "$project_dir" "$name" "$kind")
        assert_gt "[$language] $fixture_id: $name(kind=$kind)" "$count" "0"
      else
        local count=$(count_nodes "$project_dir" "$name")
        assert_gt "[$language] $fixture_id: $name" "$count" "0"
      fi
    fi
    i=$((i + 1))
  done
}

# 验证调用预期
validate_calls() {
  local project_dir="$1"
  local language="$2"
  local fixture_id="$3"
  local calls_json="$4"

  local call_count=$(python3 -c "
import json, sys
data = json.loads(sys.argv[1])
print(len(data) if isinstance(data, list) else 0)
" "$calls_json" 2>/dev/null)

  local i=0
  while [[ $i -lt $call_count ]]; do
    local caller=$(json_get "$calls_json" "$i.caller")
    local callee=$(json_get "$calls_json" "$i.callee")

    if [[ -n "$callee" && "$callee" != "None" ]]; then
      local count=$(count_calls "$project_dir" "$caller" "$callee")
      assert_gt "[$language] $fixture_id: $caller→$callee" "$count" "0"
    fi
    i=$((i + 1))
  done
}

# ── 报告生成 ──

generate_report() {
  local report_file="${1:-test-report.md}"
  local tested=$((GLOBAL_PASSED + GLOBAL_FAILED))
  local pass_rate="0.0"
  local completion_rate="0.0"
  local conclusion="通过"

  if [[ $GLOBAL_TOTAL -gt 0 ]]; then
    pass_rate=$(awk "BEGIN {printf \"%.1f\", $GLOBAL_PASSED/$GLOBAL_TOTAL*100}")
    completion_rate=$(awk "BEGIN {printf \"%.1f\", $tested/$GLOBAL_TOTAL*100}")
  fi

  if [[ $GLOBAL_FAILED -gt 0 ]]; then
    conclusion="失败"
  elif [[ $GLOBAL_UNTESTED -gt 0 ]]; then
    conclusion="未完成"
  fi

  {
    printf '# AstraMap 多语言测试报告\n\n'
    printf -- '- **执行时间**: %s\n' "$(date '+%Y-%m-%d %H:%M:%S')"
    printf -- '- **结论**: **%s**\n\n' "$conclusion"
    printf '## 汇总\n\n'
    printf '| 总用例 | 已测试 | 通过 | 失败 | 未测试 | 通过率 | 完成率 |\n'
    printf '|---:|---:|---:|---:|---:|---:|---:|\n'
    printf '| %d | %d | %d | %d | %d | %s%% | %s%% |\n\n' \
      "$GLOBAL_TOTAL" "$tested" "$GLOBAL_PASSED" "$GLOBAL_FAILED" "$GLOBAL_UNTESTED" "$pass_rate" "$completion_rate"
    printf '## 详细结果\n\n'
    printf '| 状态 | 测试项 | 详情 |\n|---|---|---|\n'
    for result in "${TEST_RESULTS[@]}"; do
      IFS='|' read -r status name detail <<< "$result"
      printf '| %s | %s | %s |\n' "$status" "$name" "$detail"
    done
  } > "$report_file"

  echo "报告已生成: $report_file"
}
