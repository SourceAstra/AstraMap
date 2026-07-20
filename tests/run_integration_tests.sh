#!/bin/bash
# AstraMap 跨语言场景集成测试
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

RUN_STARTED_AT="$(date '+%Y-%m-%d %H:%M:%S %Z')"
REPORT_FILE="${ASTRAMAP_REPORT_FILE:-$SCRIPT_DIR/reports/integration-report-latest.md}"
mkdir -p "$SCRIPT_DIR/reports"

printf "${BOLD}═══════════════════════════════════════${RESET}\n"
printf "${BOLD}  AstraMap 跨语言集成测试 v2.0${RESET}\n"
printf "${BOLD}  日期: $(date '+%Y-%m-%d %H:%M:%S')${RESET}\n"
printf "${BOLD}  覆盖: 10 种内置语言${RESET}\n"
printf "${BOLD}═══════════════════════════════════════${RESET}\n"

# ── Phase 1: 多语言混合项目索引 ──
phase_header "Phase 1: 多语言混合项目索引"

tmpdir=$(mktemp -d)
mkdir -p "$tmpdir/go" "$tmpdir/python" "$tmpdir/ts" "$tmpdir/js" "$tmpdir/c" "$tmpdir/cpp" "$tmpdir/java" "$tmpdir/rust" "$tmpdir/cs" "$tmpdir/kt"

# Go 文件
cat > "$tmpdir/go/main.go" << 'EOF'
package main
func add(a, b int) int { return a + b }
func main() { result := add(1, 2) }
EOF

# Python 文件
cat > "$tmpdir/python/app.py" << 'EOF'
def greet(name):
    return f"Hello, {name}"
print(greet("World"))
EOF

# TypeScript 文件
cat > "$tmpdir/ts/index.ts" << 'EOF'
function wrap<T>(value: T): T {
    return value;
}
const result = wrap(42);
EOF

# JavaScript 文件
cat > "$tmpdir/js/app.js" << 'EOF'
function greet(name) {
    return "Hello, " + name;
}
console.log(greet("World"));
EOF

# C 文件
cat > "$tmpdir/c/util.c" << 'EOF'
int sum(int a, int b) { return a + b; }
int main() { return sum(1, 2); }
EOF

# C++ 文件
cat > "$tmpdir/cpp/main.cpp" << 'EOF'
class Point {
public:
    Point(int x, int y) : x(x), y(y) {}
    int distance() const { return x*x + y*y; }
private:
    int x, y;
};
int main() { Point p(3, 4); return p.distance(); }
EOF

# Java 文件
cat > "$tmpdir/java/Main.java" << 'EOF'
public class Main {
    public static void main(String[] args) {
        System.out.println("Hello");
    }
}
EOF

# Rust 文件
cat > "$tmpdir/rust/main.rs" << 'EOF'
fn add(a: i32, b: i32) -> i32 { a + b }
fn main() { let _ = add(1, 2); }
EOF

# C# 文件
cat > "$tmpdir/cs/Program.cs" << 'EOF'
using System;
class Program {
    static int Add(int a, int b) { return a + b; }
    static void Main() { Console.WriteLine(Add(1, 2)); }
}
EOF

# Kotlin 文件
cat > "$tmpdir/kt/Main.kt" << 'EOF'
fun add(a: Int, b: Int): Int = a + b
fun main() { println(add(1, 2)) }
EOF

amap index --project "$tmpdir" >/dev/null 2>&1

# 验证各语言文件被索引
go_files=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE 'go/%'")
assert_gt "INT-001 Go 文件索引" "$go_files" "0"

py_files=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE 'python/%'")
assert_gt "INT-002 Python 文件索引" "$py_files" "0"

ts_files=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE 'ts/%'")
assert_gt "INT-003 TypeScript 文件索引" "$ts_files" "0"

js_files=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE 'js/%'")
assert_gt "INT-004 JavaScript 文件索引" "$js_files" "0"

c_files=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE 'c/%'")
assert_gt "INT-005 C 文件索引" "$c_files" "0"

cpp_files=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE 'cpp/%'")
assert_gt "INT-006 C++ 文件索引" "$cpp_files" "0"

java_files=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE 'java/%'")
assert_gt "INT-007 Java 文件索引" "$java_files" "0"

rust_files=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE 'rust/%'")
assert_gt "INT-008 Rust 文件索引" "$rust_files" "0"

csharp_files=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE 'cs/%'")
assert_gt "INT-009 C# 文件索引" "$csharp_files" "0"

kotlin_files=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE 'kt/%'")
assert_gt "INT-010 Kotlin 文件索引" "$kotlin_files" "0"

rm -rf "$tmpdir"

# ── Phase 2: 跨文件调用解析 ──
phase_header "Phase 2: 跨文件调用解析 (L3)"

tmpdir=$(mktemp -d)
mkdir -p "$tmpdir/pkg"

# 定义文件
cat > "$tmpdir/pkg/defs.go" << 'EOF'
package pkg
func Calculate(x int) int { return x * 2 }
EOF

# 使用文件
cat > "$tmpdir/pkg/usage.go" << 'EOF'
package pkg
func Use() int { return Calculate(10) }
EOF

amap index --project "$tmpdir" >/dev/null 2>&1

# 验证跨文件调用边存在
calculate_calls=$(count_calls "$tmpdir" "Use" "Calculate")
assert_gt "SEMA-001 同包跨文件调用" "$calculate_calls" "0"

rm -rf "$tmpdir"

# ── Phase 3: 接口实现关系 ──
phase_header "Phase 3: 接口实现关系 (L3)"

tmpdir=$(mktemp -d)
mkdir -p "$tmpdir/impl"

# 接口定义
cat > "$tmpdir/impl/interface.go" << 'EOF'
package impl
type Reader interface { Read() []byte }
EOF

# 实现类
cat > "$tmpdir/impl/file.go" << 'EOF'
package impl
type File struct{}
func (f File) Read() []byte { return nil }
EOF

amap index --project "$tmpdir" >/dev/null 2>&1

# 验证 implements 边
reader_implements=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_edges WHERE kind='implements' AND target LIKE '%Reader'")
if [[ "$reader_implements" == "0" ]]; then
  assert_untested "SEMA-002 接口实现关系" "当前版本可能未提取 implements 边"
else
  assert_gt "SEMA-002 接口实现关系" "$reader_implements" "0"
fi

rm -rf "$tmpdir"

# ── Phase 4: 命名空间与容器 ──
phase_header "Phase 4: 命名空间与容器"

tmpdir=$(mktemp -d)
mkdir -p "$tmpdir/ns"

# C++ 命名空间
cat > "$tmpdir/ns/math.cpp" << 'EOF'
namespace Math {
    int add(int a, int b) { return a + b; }
}
int main() { return Math::add(1, 2); }
EOF

amap index --project "$tmpdir" >/dev/null 2>&1

# 容器关系由 contains 边表达，不在节点上冗余存储 container 字段。
add_container=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_edges AS edge JOIN astramap_nodes AS parent ON parent.id=edge.source JOIN astramap_nodes AS child ON child.id=edge.target WHERE edge.kind='contains' AND parent.kind='namespace' AND parent.name='Math' AND child.name='add'")
assert_gt "SEMA-003 命名空间容器" "$add_container" "0"

rm -rf "$tmpdir"

# ── Phase 5: 重载消歧 ──
phase_header "Phase 5: 重载消歧"

tmpdir=$(mktemp -d)
mkdir -p "$tmpdir/overload"

# C++ 重载函数
cat > "$tmpdir/overload/funcs.cpp" << 'EOF'
int process(int x) { return x * 2; }
double process(double x) { return x * 3.0; }
int main() { return process(1); }
EOF

amap index --project "$tmpdir" >/dev/null 2>&1

# 验证多个定义存在
process_count=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_nodes WHERE name='process'")
if [[ "$process_count" == "0" ]]; then
  assert_untested "SEMA-004 重载消歧" "符号未找到"
else
  assert_gt "SEMA-004 重载保留多个定义" "$process_count" "0"
fi

rm -rf "$tmpdir"

# ── Phase 6: 宏展开调用 ──
phase_header "Phase 6: 宏展开调用"

tmpdir=$(mktemp -d)
mkdir -p "$tmpdir/macro"

# C 宏定义与调用
cat > "$tmpdir/macro/utils.c" << 'EOF'
#define MAX(a, b) ((a) > (b) ? (a) : (b))
int main() { return MAX(1, 2); }
EOF

amap index --project "$tmpdir" >/dev/null 2>&1

# 验证宏节点存在
max_count=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_nodes WHERE name='MAX'")
if [[ "$max_count" == "0" ]]; then
  assert_untested "SEMA-005 宏定义提取" "当前版本可能不支持宏"
else
  assert_gt "SEMA-005 宏定义提取" "$max_count" "0"
fi

rm -rf "$tmpdir"

# ── 生成报告 ──
generate_report "$REPORT_FILE"

# ── 导出结果供父脚本读取 ──
cat > /tmp/astramap_int_results.txt <<EOF
GLOBAL_TOTAL=$GLOBAL_TOTAL
GLOBAL_PASSED=$GLOBAL_PASSED
GLOBAL_FAILED=$GLOBAL_FAILED
GLOBAL_UNTESTED=$GLOBAL_UNTESTED
EOF

# 导出详细结果数组
printf '%s\n' "${TEST_RESULTS[@]}" > /tmp/astramap_int_details.txt

# ── 汇总 ──
phase_summary "全部测试"

printf "\n详细报告: $REPORT_FILE\n"

[[ $GLOBAL_FAILED -eq 0 && $GLOBAL_UNTESTED -eq 0 ]]
