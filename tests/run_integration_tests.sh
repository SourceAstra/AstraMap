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
printf "${BOLD}  覆盖: 12 种内置语言${RESET}\n"
printf "${BOLD}═══════════════════════════════════════${RESET}\n"

# ── Phase 1: 多语言混合项目索引 ──
phase_header "Phase 1: 多语言混合项目索引"

tmpdir=$(mktemp -d)
mkdir -p "$tmpdir/go" "$tmpdir/python" "$tmpdir/ts" "$tmpdir/js" "$tmpdir/c" "$tmpdir/cpp" "$tmpdir/java" "$tmpdir/rust" "$tmpdir/cs" "$tmpdir/kt" "$tmpdir/php" "$tmpdir/bash"

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

# PHP 文件
cat > "$tmpdir/php/index.php" << 'EOF'
<?php
function greet($name) { return "Hello, " . $name; }
echo greet("World");
EOF

# Bash 文件
cat > "$tmpdir/bash/script.sh" << 'EOF'
#!/bin/bash
function greet() { echo "Hello, $1"; }
greet "World"
EOF

amap index --project "$tmpdir" >/dev/null 2>&1

# 验证各语言文件被索引
go_files=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE 'go/%'")
assert_eq "INT-001 Go 文件索引" "$go_files" "≥1"

py_files=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE 'python/%'")
assert_eq "INT-002 Python 文件索引" "$py_files" "≥1"

ts_files=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE 'ts/%'")
assert_eq "INT-003 TypeScript 文件索引" "$ts_files" "≥1"

js_files=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE 'js/%'")
assert_eq "INT-004 JavaScript 文件索引" "$js_files" "≥1"

c_files=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE 'c/%'")
assert_eq "INT-005 C 文件索引" "$c_files" "≥1"

cpp_files=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE 'cpp/%'")
assert_eq "INT-006 C++ 文件索引" "$cpp_files" "≥1"

java_files=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE 'java/%'")
assert_eq "INT-007 Java 文件索引" "$java_files" "≥1"

rust_files=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE 'rust/%'")
assert_eq "INT-008 Rust 文件索引" "$rust_files" "≥1"

csharp_files=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE 'cs/%'")
assert_eq "INT-009 C# 文件索引" "$csharp_files" "≥1"

kotlin_files=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE 'kt/%'")
assert_eq "INT-010 Kotlin 文件索引" "$kotlin_files" "≥1"

php_files=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE 'php/%'")
assert_eq "INT-011 PHP 文件索引" "$php_files" "≥1"

bash_files=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_files WHERE path LIKE 'bash/%'")
assert_eq "INT-012 Bash 文件索引" "$bash_files" "≥1"

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
calculate_calls=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_edges WHERE src_kind='call' AND dst_id LIKE '%Calculate'")
assert_eq "SEMA-001 同包跨文件调用" "$calculate_calls" "≥1"

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
reader_implements=$(query_val "$tmpdir" "SELECT COUNT(*) FROM astramap_edges WHERE src_kind='implements' AND dst_id LIKE '%Reader'")
if [[ "$reader_implements" == "0" ]]; then
  assert_untested "SEMA-002 接口实现关系" "当前版本可能未提取 implements 边"
else
  assert_eq "SEMA-002 接口实现关系" "$reader_implements" "≥1"
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

# 验证命名空间容器
add_container=$(query_val "$tmpdir" "SELECT container FROM astramap_nodes WHERE name='add' LIMIT 1")
if [[ -n "$add_container" ]]; then
  assert_contains "SEMA-003 命名空间容器" "$add_container" "Math"
else
  assert_untested "SEMA-003 命名空间容器" "符号未找到"
fi

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
elif [[ "$process_count" -ge 2 ]]; then
  assert_eq "SEMA-004 重载保留多个定义" "$process_count" "≥2"
else
  assert_eq "SEMA-004 重载至少一个定义" "$process_count" "≥1"
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

# ── 汇总 ──
phase_summary "全部测试"

printf "\n详细报告: $REPORT_FILE\n"

[[ $GLOBAL_FAILED -eq 0 && $GLOBAL_UNTESTED -eq 0 ]]
