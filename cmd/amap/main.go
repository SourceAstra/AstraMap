package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"astramap-standalone/astramap"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// logInfo, logError, logWarn, logDebug 复制或模拟以确保 main 包兼容
func logInfo(format string, v ...interface{}) {
	fmt.Fprintf(os.Stderr, "[INFO] "+format+"\n", v...)
}

func logError(format string, v ...interface{}) {
	fmt.Fprintf(os.Stderr, "[ERROR] "+format+"\n", v...)
}

func logWarn(format string, v ...interface{}) {
	fmt.Fprintf(os.Stderr, "[WARN] "+format+"\n", v...)
}

func getAstraMapDB(projectRoot string) (*sqlx.DB, error) {
	dbDir := filepath.Join(projectRoot, ".astramap")
	_ = os.MkdirAll(dbDir, 0755)
	dbPath := filepath.Join(dbDir, "astramap.db")

	db, err := sqlx.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	if err := astramap.InitAstraMapSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// projectRoot holds the resolved --project path.
// stripProjectArg removes --project / --project=X from os.Args[2:] so
// per-command flag.NewFlagSet parsers don't reject it as unknown.
var projectRoot string

func stripProjectArg() {
	if len(os.Args) < 2 {
		projectRoot, _ = filepath.Abs(".")
		return
	}
	var filtered []string
	filtered = append(filtered, os.Args[0], os.Args[1])
	for i := 2; i < len(os.Args); i++ {
		if os.Args[i] == "--project" && i+1 < len(os.Args) {
			abs, _ := filepath.Abs(os.Args[i+1])
			projectRoot = abs
			i++ // skip the value too
			continue
		}
		if strings.HasPrefix(os.Args[i], "--project=") {
			val := strings.TrimPrefix(os.Args[i], "--project=")
			abs, _ := filepath.Abs(val)
			projectRoot = abs
			continue
		}
		filtered = append(filtered, os.Args[i])
	}
	if projectRoot == "" {
		projectRoot, _ = filepath.Abs(".")
	}
	os.Args = filtered
}

func main() {
	stripProjectArg()

	if len(os.Args) < 2 {
		printHelp()
		return
	}

	subcmd := os.Args[1]
	switch subcmd {
	case "serve":
		serveCmd()
	case "dashboard":
		dashboardCmd()
	case "index":
		indexCmd()
	case "install":
		installCmd()
	case "diff":
		diffCmd()
	case "locate":
		locateCmd()
	case "hotspots":
		hotspotsCmd()
	case "deadcode":
		deadcodeCmd()
	case "cycles":
		cyclesCmd()
	case "coupling":
		couplingCmd()
	case "owners":
		ownersCmd()
	case "query":
		queryCmd()
	case "tree":
		treeCmd()
	case "audit":
		auditCmd()
	default:
		fmt.Printf("未知的子命令: %s\n\n", subcmd)
		printHelp()
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Println(`AstraMap — 给 AI 编程代理使用的高精度动态代码地图 MCP 引擎

用法:
  amap <command> [arguments]
  所有命令均支持 --project <path> 指定项目根目录

核心功能命令:
  serve                                       启动 stdio MCP 服务
  dashboard                                   启动源码星空可视化控制台
  index [--lang c,python]                     构建/更新代码地图索引（交互选择语言）
  install                                     一键安装 MCP 到 Claude Code / Cursor

开发诊断工具 (CLI Diagnostics):
  diff [--suggest-tests]                      基于 git diff 评估修改影响面与测试建议
  locate <symbol>                             快速定位符号定义的物理路径及行列号
  hotspots                                    依据 Git 修改频次与圈复杂度探测代码热点
  deadcode                                    代码可达性检查，分析多余死代码
  cycles                                      循环依赖与引用检测
  coupling [--path=...]                       模块 Ca/Ce 内聚耦合度分析
  owners <symbol>                             结合 GitBlame 定位最熟悉此符号的所有者
  query "<SQL>"                               通过 SQL 直接操作和检索底层图拓扑
  tree <symbol> [--dir=up|down] [--depth=N]   在终端绘制指定符号的调用拓扑树
  audit                                       扫描质量审计 Verdicts 缺陷
`)
}

// ===== 各命令的具体执行实现 =====

func serveCmd() {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	_ = fs.Parse(os.Args[2:])

	db, err := getAstraMapDB(projectRoot)
	if err != nil {
		logError("无法连接到代码地图数据库: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	astramap.RunMcpServer(db, projectRoot)
}

func dashboardCmd() {
	fs := flag.NewFlagSet("dashboard", flag.ExitOnError)
	host := fs.String("host", "0.0.0.0", "Web服务监听地址")
	port := fs.Int("port", 3000, "Web服务端口号")
	foreground := fs.Bool("foreground", false, "以前台模式运行 Dashboard")
	_ = fs.Parse(os.Args[2:])

	resolvedPort, err := findAvailablePort(*host, *port)
	if err != nil {
		logError("无法找到可用 Dashboard 端口: %v", err)
		os.Exit(1)
	}
	if resolvedPort != *port {
		logWarn("Dashboard 端口 %d 已被占用，自动切换到 %d", *port, resolvedPort)
	}
	if !*foreground {
		startDashboardBackground(projectRoot, *host, resolvedPort)
		return
	}

	db, err := getAstraMapDB(projectRoot)
	if err != nil {
		logError("无法连接到代码地图数据库: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	printDashboardURLs(*host, resolvedPort)
	err = astramap.StartStandaloneServer(db, projectRoot, *host, resolvedPort)
	if err != nil {
		logError("Web服务器启动失败: %v", err)
		os.Exit(1)
	}
}

func findAvailablePort(host string, startPort int) (int, error) {
	if startPort <= 0 {
		startPort = 3000
	}
	for port := startPort; port <= 65535; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
		if err == nil {
			_ = ln.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("从 %d 到 65535 均不可用", startPort)
}

func startDashboardBackground(projectRoot, host string, port int) {
	exe, err := os.Executable()
	if err != nil {
		logError("无法定位当前二进制: %v", err)
		os.Exit(1)
	}

	logPath := filepath.Join(projectRoot, ".astramap", "dashboard.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		logError("无法创建日志目录: %v", err)
		os.Exit(1)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		logError("无法打开 Dashboard 日志: %v", err)
		os.Exit(1)
	}
	defer logFile.Close()

	args := []string{"dashboard", "--project", projectRoot, "--host", host, "--port", fmt.Sprintf("%d", port), "--foreground"}
	cmd := exec.Command(exe, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		logError("Dashboard 后台启动失败: %v", err)
		os.Exit(1)
	}
	if err := cmd.Process.Release(); err != nil {
		logWarn("Dashboard 进程已启动，但释放进程句柄失败: %v", err)
	}

	fmt.Printf("AstraMap Dashboard started in background\n")
	printDashboardURLs(host, port)
	fmt.Printf("PID: %d\n", cmd.Process.Pid)
	fmt.Printf("Log: %s\n", logPath)
}

func printDashboardURLs(host string, port int) {
	fmt.Printf("Host: %s\n", host)
	fmt.Printf("Port: %d\n", port)
	if host == "0.0.0.0" || host == "::" || host == "" {
		fmt.Printf("Local: http://localhost:%d\n", port)
		for _, ip := range localIPv4Addrs() {
			fmt.Printf("LAN: http://%s:%d\n", ip, port)
		}
		return
	}
	if host == "127.0.0.1" || host == "localhost" || host == "::1" {
		fmt.Printf("Local: http://localhost:%d\n", port)
		return
	}
	fmt.Printf("URL: http://%s:%d\n", host, port)
}

func localIPv4Addrs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var ips []string
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP == nil || ipNet.IP.IsLoopback() {
			continue
		}
		ip := ipNet.IP.To4()
		if ip == nil {
			continue
		}
		ips = append(ips, ip.String())
		if len(ips) >= 3 {
			break
		}
	}
	return ips
}

// ===== SCIP 自动检测与生成 =====

// LangCount holds a detected language with its source file count.
type LangCount struct {
	Lang  string
	Count int
}

// skipDirs are directory names to skip during file walks.
var skipDirs = []string{".git", ".astramap", ".understand-anything", ".cache", ".idea", ".vscode",
	"node_modules", "build", "dist", "vendor", "target", "out", "tmp", "temp"}

func countFilesByExt(projectRoot string, exts ...string) int {
	wanted := make(map[string]bool, len(exts))
	for _, ext := range exts {
		wanted[ext] = true
	}
	count := 0
	_ = filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			for _, skip := range skipDirs {
				if info.Name() == skip {
					return filepath.SkipDir
				}
			}
			if strings.HasPrefix(info.Name(), ".trash") {
				return filepath.SkipDir
			}
			return nil
		}
		if wanted[strings.ToLower(filepath.Ext(path))] {
			count++
		}
		return nil
	})
	return count
}

func detectProjectLanguages(projectRoot string) []LangCount {
	// Phase 1: detect which languages exist (project markers + file extensions)
	candidates := make(map[string]bool)

	if _, err := os.Stat(filepath.Join(projectRoot, "go.mod")); err == nil {
		candidates["go"] = true
	}
	if _, err := os.Stat(filepath.Join(projectRoot, "tsconfig.json")); err == nil {
		candidates["typescript"] = true
	} else if _, err := os.Stat(filepath.Join(projectRoot, "package.json")); err == nil {
		candidates["typescript"] = true
	} else if projectHasExtensions(projectRoot, ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs") {
		candidates["typescript"] = true
	}
	if _, err := os.Stat(filepath.Join(projectRoot, "pyproject.toml")); err == nil {
		candidates["python"] = true
	} else if _, err := os.Stat(filepath.Join(projectRoot, "setup.py")); err == nil {
		candidates["python"] = true
	} else if _, err := os.Stat(filepath.Join(projectRoot, "requirements.txt")); err == nil {
		candidates["python"] = true
	} else if projectHasExtensions(projectRoot, ".py") {
		candidates["python"] = true
	}
	if _, err := os.Stat(filepath.Join(projectRoot, "pom.xml")); err == nil {
		candidates["java"] = true
	} else if hasAnyProjectFile(projectRoot, "build.gradle", "build.gradle.kts", "gradlew") {
		candidates["java"] = true
	} else if projectHasExtensions(projectRoot, ".java") {
		candidates["java"] = true
	}
	if isCxxProject(projectRoot) {
		candidates["cpp"] = true
	} else if isCProject(projectRoot) {
		candidates["c"] = true
	}

	// Phase 2: count files per detected language and rank by count
	var ranked []LangCount
	for lang := range candidates {
		exts, ok := astramap.LangExts[lang]
		if !ok {
			continue
		}
		cnt := countFilesByExt(projectRoot, exts...)
		if cnt > 0 {
			ranked = append(ranked, LangCount{Lang: lang, Count: cnt})
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].Count > ranked[j].Count
	})
	return ranked
}

func isCProject(projectRoot string) bool {
	return projectHasExtensions(projectRoot, ".c") || (projectHasExtensions(projectRoot, ".h") && !isCxxProject(projectRoot))
}

func isCxxProject(projectRoot string) bool {
	for _, marker := range []string{"compile_commands.json", "CMakeLists.txt", "Makefile", "makefile"} {
		if _, err := os.Stat(filepath.Join(projectRoot, marker)); err == nil {
			if marker != "Makefile" && marker != "makefile" {
				return projectHasExtensions(projectRoot, ".cc", ".cpp", ".cxx", ".hpp", ".hxx")
			}
			if projectHasExtensions(projectRoot, ".cc", ".cpp", ".cxx", ".hpp", ".hxx") {
				return true
			}
		}
	}
	return projectHasExtensions(projectRoot, ".cc", ".cpp", ".cxx", ".hpp", ".hxx")
}

func projectHasExtensions(projectRoot string, exts ...string) bool {
	wanted := make(map[string]bool, len(exts))
	for _, ext := range exts {
		wanted[ext] = true
	}

	found := false
	_ = filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || found {
			return nil
		}
		if info.IsDir() {
			for _, skip := range skipDirs {
				if info.Name() == skip {
					return filepath.SkipDir
				}
			}
			if strings.HasPrefix(info.Name(), ".trash") {
				return filepath.SkipDir
			}
			return nil
		}
		if wanted[strings.ToLower(filepath.Ext(path))] {
			found = true
		}
		return nil
	})
	return found
}

func scipToolName(lang string) string {
	m := map[string]string{"go": "scip-go", "typescript": "scip-typescript", "python": "scip-python", "java": "scip-java", "c": "scip-clang", "cpp": "scip-clang"}
	return m[lang]
}

func findScipTool(lang string) (string, bool) {
	name := scipToolName(lang)
	if name == "" {
		return "", false
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	if lang == "go" {
		gopath := os.Getenv("GOPATH")
		if gopath == "" {
			gopath = filepath.Join(os.Getenv("HOME"), "go")
		}
		p := filepath.Join(gopath, "bin", name)
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}

func languageDisplayName(lang string) string {
	switch lang {
	case "go":
		return "Go"
	case "typescript":
		return "TypeScript/JavaScript"
	case "python":
		return "Python"
	case "java":
		return "Java"
	case "c":
		return "C"
	case "cpp":
		return "C++"
	default:
		return lang
	}
}

func scipInstallHint(lang string) string {
	switch lang {
	case "go":
		return "go install github.com/sourcegraph/scip-go/cmd/scip-go@latest"
	case "typescript":
		return "npm install -g @sourcegraph/scip-typescript"
	case "python":
		return "pip install scip-python"
	case "java":
		return "参见 https://github.com/sourcegraph/scip-java"
	case "c", "cpp":
		return "参见 https://github.com/sourcegraph/scip-clang"
	default:
		return ""
	}
}

func printToolStatus(label string, commands []string, installHint string) bool {
	if path := firstAvailableTool(commands...); path != "" {
		fmt.Printf("  ✓ %s: %s\n", label, path)
		return true
	}
	fmt.Printf("  ⚠ 未找到 %s: %s\n", label, strings.Join(commands, " / "))
	if installHint != "" {
		fmt.Printf("    安装: %s\n", installHint)
	}
	return false
}

func printScipToolStatus(lang string) {
	name := scipToolName(lang)
	if path, ok := findScipTool(lang); ok {
		fmt.Printf("  ✓ %s: %s\n", name, path)
		return
	}
	fmt.Printf("  ⚠ 未找到 %s\n", name)
	if hint := scipInstallHint(lang); hint != "" {
		fmt.Printf("    安装: %s\n", hint)
	}
}

func printLanguageToolchainHints(lang, projectRoot string) {
	fmt.Printf("检测到 %s 项目，检查工具链...\n", languageDisplayName(lang))
	printScipToolStatus(lang)

	switch lang {
	case "go":
		printToolStatus("Go 编译工具", []string{"go"}, "https://go.dev/doc/install")
	case "typescript":
		printToolStatus("Node.js", []string{"node"}, "Ubuntu/Debian: sudo apt install nodejs npm | macOS: brew install node")
		printToolStatus("包管理器", []string{"pnpm", "yarn", "npm"}, "Ubuntu/Debian: sudo apt install npm | macOS: brew install node")
		if hasAnyProjectFile(projectRoot, "tsconfig.json") {
			printToolStatus("TypeScript 编译器", []string{"tsc"}, "npm install -g typescript")
		}
	case "python":
		printToolStatus("Python 解释器", []string{"python3", "python"}, "Ubuntu/Debian: sudo apt install python3 python3-pip | macOS: brew install python")
		printToolStatus("pip", []string{"pip3", "pip"}, "Ubuntu/Debian: sudo apt install python3-pip | macOS: python3 -m ensurepip")
	case "java":
		printToolStatus("Java 运行时", []string{"java"}, "Ubuntu/Debian: sudo apt install default-jdk | macOS: brew install openjdk")
		printToolStatus("Java 编译器", []string{"javac"}, "Ubuntu/Debian: sudo apt install default-jdk | macOS: brew install openjdk")
		if hasAnyProjectFile(projectRoot, "pom.xml") {
			printToolStatus("Maven", []string{"mvn"}, "Ubuntu/Debian: sudo apt install maven | macOS: brew install maven")
		}
		if projectHasExtensions(projectRoot, ".gradle") || hasAnyProjectFile(projectRoot, "build.gradle", "build.gradle.kts", "gradlew") {
			if hasAnyProjectFile(projectRoot, "gradlew") {
				fmt.Printf("  ✓ Gradle Wrapper: %s\n", filepath.Join(projectRoot, "gradlew"))
			} else {
				printToolStatus("Gradle", []string{"gradle"}, "Ubuntu/Debian: sudo apt install gradle | macOS: brew install gradle")
			}
		}
	case "c", "cpp":
		printCppToolchainHints(projectRoot)
	}
}

func printCppToolchainHints(projectRoot string) {
	compdbPath := filepath.Join(projectRoot, "compile_commands.json")
	if _, err := os.Stat(compdbPath); err == nil {
		fmt.Printf("  ✓ compile_commands.json: %s\n", compdbPath)
	} else {
		fmt.Println("  ⚠ 未发现 compile_commands.json；scip-clang 高精度索引需要该文件")
		if _, err := exec.LookPath("bear"); err == nil {
			fmt.Println("  ✓ bear 已安装，将自动执行: bear -- make")
		} else {
			fmt.Println("  ⚠ bear 未安装，无法自动捕获 Makefile 编译命令")
			fmt.Println("    安装: Ubuntu/Debian: sudo apt install bear | macOS: brew install bear")
		}
		if _, err := exec.LookPath("cmake"); err == nil {
			fmt.Println("  ✓ cmake 已安装，可生成: cmake -S . -B build -DCMAKE_EXPORT_COMPILE_COMMANDS=ON")
		} else if _, err := os.Stat(filepath.Join(projectRoot, "CMakeLists.txt")); err == nil {
			fmt.Println("  ⚠ 检测到 CMakeLists.txt 但未找到 cmake")
			fmt.Println("    安装: Ubuntu/Debian: sudo apt install cmake | macOS: brew install cmake")
		}
	}

	if _, err := exec.LookPath("make"); err == nil {
		fmt.Println("  ✓ make 已安装")
	} else if hasAnyProjectFile(projectRoot, "Makefile", "makefile") {
		fmt.Println("  ⚠ 检测到 Makefile 但未找到 make")
		fmt.Println("    安装: Ubuntu/Debian: sudo apt install make | macOS: xcode-select --install")
	}

	if compiler := firstAvailableTool("cc", "clang", "gcc"); compiler != "" {
		fmt.Printf("  ✓ C/C++ 编译器可用: %s\n", compiler)
	} else {
		fmt.Println("  ⚠ 未找到 C/C++ 编译器: cc / clang / gcc")
		fmt.Println("    安装: Ubuntu/Debian: sudo apt install build-essential clang | macOS: xcode-select --install")
	}
}

func hasAnyProjectFile(projectRoot string, names ...string) bool {
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(projectRoot, name)); err == nil {
			return true
		}
	}
	return false
}

func firstAvailableTool(names ...string) string {
	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

func runScipGeneration(toolPath, lang, projectRoot string) (string, error) {
	_ = os.MkdirAll(filepath.Join(projectRoot, ".astramap"), 0755)
	scipPath := filepath.Join(projectRoot, ".astramap", "index-"+lang+".scip")
	var cmd *exec.Cmd
	switch lang {
	case "go":
		cmd = exec.Command(toolPath, "index", "--module-root", projectRoot, "-o", scipPath)
	case "typescript":
		if err := ensureTsConfig(projectRoot); err != nil {
			return "", err
		}
		cmd = exec.Command(toolPath, "index", "--cwd", projectRoot, "--output", scipPath)
	case "python":
		cmd = exec.Command(toolPath, "index", "--cwd", projectRoot, "--output", scipPath)
	case "c", "cpp":
		compdbPath := filepath.Join(projectRoot, "compile_commands.json")
		if _, err := os.Stat(compdbPath); err != nil {
			if err := ensureCompileCommands(projectRoot, compdbPath); err != nil {
				return "", err
			}
		}
		if err := fixCompileCommandsJson(compdbPath, projectRoot); err != nil {
			logWarn("修复 compile_commands.json 路径失败: %v", err)
		}
		if ok, count, reason := validateCompileCommandsJson(compdbPath); !ok {
			return "", fmt.Errorf("compile_commands.json 无效: %s (entries=%d)", reason, count)
		}
		cmd = exec.Command(toolPath, "--compdb-path", compdbPath, "--index-output-path", scipPath, "--no-progress-report")
	default:
		return "", fmt.Errorf("不支持的语言: %s", lang)
	}
	cmd.Dir = projectRoot
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("SCIP 生成失败 (%s): %w\n%s", lang, err, strings.TrimSpace(output.String()))
	}
	return scipPath, nil
}

func ensureCompileCommands(projectRoot, compdbPath string) error {
	if _, err := exec.LookPath("bear"); err != nil {
		return fmt.Errorf("C/C++ SCIP 需要 compile_commands.json；未找到 bear，无法自动生成\n安装: Ubuntu/Debian: sudo apt install bear | macOS: brew install bear")
	}
	if _, err := exec.LookPath("make"); err != nil {
		return fmt.Errorf("C/C++ SCIP 需要 compile_commands.json；未找到 make，无法执行 bear -- make\n安装: Ubuntu/Debian: sudo apt install make | macOS: xcode-select --install")
	}
	if !hasAnyProjectFile(projectRoot, "Makefile", "makefile") {
		return fmt.Errorf("C/C++ SCIP 需要 compile_commands.json；当前项目没有 Makefile，无法执行 bear -- make")
	}

	fmt.Println("未发现 compile_commands.json，正在执行 bear -- make 生成编译数据库...")
	cmd := exec.Command("bear", "--", "make")
	cmd.Dir = projectRoot
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("bear -- make 执行失败: %w\n%s", err, strings.TrimSpace(output.String()))
	}
	if _, err := os.Stat(compdbPath); err != nil {
		return fmt.Errorf("bear -- make 已执行，但未生成 compile_commands.json\n%s", strings.TrimSpace(output.String()))
	}
	fmt.Println("compile_commands.json 生成完成")
	return nil
}

// ensureTsConfig generates a minimal tsconfig.json for JS/TS projects that lack one.
// scip-typescript requires tsconfig.json to discover source files.
func ensureTsConfig(projectRoot string) error {
	tsconfigPath := filepath.Join(projectRoot, "tsconfig.json")
	if _, err := os.Stat(tsconfigPath); err == nil {
		return nil
	}

	fmt.Println("未发现 tsconfig.json，正在为 JS/TS 项目生成最小化配置...")
	tsconfig := `{
  "compilerOptions": {
    "target": "ES2020",
    "module": "ESNext",
    "moduleResolution": "node",
    "allowJs": true,
    "checkJs": false,
    "noEmit": true,
    "skipLibCheck": true,
    "esModuleInterop": true
  },
  "include": ["**/*.js", "**/*.jsx", "**/*.ts", "**/*.tsx"],
  "exclude": ["node_modules", "dist", ".astramap", "build"]
}
`
	if err := os.WriteFile(tsconfigPath, []byte(tsconfig), 0644); err != nil {
		return fmt.Errorf("写入 tsconfig.json 失败: %w", err)
	}
	fmt.Println("tsconfig.json 生成完成")
	return nil
}

func validateCompileCommandsJson(compdbPath string) (bool, int, string) {
	data, err := os.ReadFile(compdbPath)
	if err != nil {
		return false, 0, "无法读取文件"
	}
	if len(data) < 4 {
		return false, 0, "文件为空或格式无效"
	}

	var entries []map[string]interface{}
	if err := json.Unmarshal(data, &entries); err != nil {
		return false, 0, "JSON 解析失败: " + err.Error()
	}
	if len(entries) == 0 {
		return false, 0, "没有编译单元"
	}

	for _, entry := range entries {
		dir, hasDir := entry["directory"].(string)
		if !hasDir {
			return false, len(entries), "缺少 directory 字段"
		}
		filePath, ok := entry["file"].(string)
		if !ok {
			return false, len(entries), "缺少 file 字段"
		}
		if _, hasCmd := entry["command"]; !hasCmd {
			if _, hasArgs := entry["arguments"]; !hasArgs {
				return false, len(entries), "缺少 command 或 arguments 字段"
			}
		}
		resolvedPath := filePath
		if !filepath.IsAbs(filePath) {
			resolvedPath = filepath.Join(dir, filePath)
		}
		if _, err := os.Stat(resolvedPath); os.IsNotExist(err) {
			return false, len(entries), fmt.Sprintf("源文件不存在: %s", resolvedPath)
		}
	}
	return true, len(entries), ""
}

func fixCompileCommandsJson(compdbPath, projectRoot string) error {
	data, err := os.ReadFile(compdbPath)
	if err != nil {
		return err
	}

	var entries []map[string]interface{}
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}

	modified := false
	for i, entry := range entries {
		if dir, ok := entry["directory"].(string); ok && !filepath.IsAbs(dir) {
			entries[i]["directory"] = filepath.Join(projectRoot, dir)
			modified = true
		}
		if file, ok := entry["file"].(string); ok && !filepath.IsAbs(file) {
			dir, _ := entries[i]["directory"].(string)
			entries[i]["file"] = filepath.Clean(filepath.Join(dir, file))
			modified = true
		}
	}
	if !modified {
		return nil
	}
	newData, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(compdbPath, newData, 0644)
}

// autoGenerateScip finds and runs SCIP tools for all selected languages.
// Returns collected (scipFilePaths, languagesWithScip).
func autoGenerateScip(projectRoot string, selectedLangs []LangCount) ([]string, []string) {
	if len(selectedLangs) == 0 {
		fmt.Println("未检测到已知项目语言，使用 Tree-sitter 模式")
		return nil, nil
	}
	var scipPaths []string
	var scipLangs []string
	for _, lc := range selectedLangs {
		lang := lc.Lang
		printLanguageToolchainHints(lang, projectRoot)
		toolPath, found := findScipTool(lang)
		if !found {
			fmt.Printf("检测到 %s 项目，但未找到 %s，跳过 SCIP\n", languageDisplayName(lang), scipToolName(lang))
			continue
		}
		fmt.Printf("检测到 %s 项目，正在生成 SCIP 索引 (%s)...\n", languageDisplayName(lang), toolPath)
		scipPath, err := runScipGeneration(toolPath, lang, projectRoot)
		if err != nil {
			logWarn("SCIP 生成失败: %v，回退到 Tree-sitter", err)
			continue
		}
		scipPaths = append(scipPaths, scipPath)
		scipLangs = append(scipLangs, lang)
	}
	return scipPaths, scipLangs
}

func indexCmd() {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	scipFile := fs.String("scip-file", "", "已有 SCIP 索引文件路径（直接导入）")
	scip := fs.Bool("scip", false, "自动检测项目语言并生成 SCIP 索引（高精度模式）")
	treesitterOnly := fs.Bool("treesitter-only", false, "只使用轻量 Tree-sitter 进行快速文件扫描")
	langFlag := fs.String("lang", "", "指定导入语言，逗号分隔 (如 --lang c 或 --lang go,python)；跳过交互选择")
	_ = fs.Parse(os.Args[2:])

	// Detect languages with file counts
	detected := detectProjectLanguages(projectRoot)
	if len(detected) == 0 {
		fmt.Println("未检测到已知项目语言")
		os.Exit(1)
	}

	// Display detected languages
	fmt.Println("检测到以下语言文件:")
	for i, lc := range detected {
		fmt.Printf("  %d. %s (%d 个源文件)\n", i+1, languageDisplayName(lc.Lang), lc.Count)
	}

	// Determine selected languages
	var selected []LangCount
	if *langFlag != "" {
		// Non-interactive: --lang c,python
		selected = resolveLangNames(*langFlag, detected)
	} else if len(detected) > 1 {
		fmt.Println()
		fmt.Print("请选择要导入的语言 (输入序号，多选用逗号分隔，如 1,3；回车导入全部): ")
		var input string
		fmt.Scanln(&input)
		if input != "" {
			selected = parseLangSelection(input, detected)
		} else {
			selected = detected
		}
	} else {
		selected = detected
	}

	fmt.Printf("\n将导入语言: ")
	var langNames []string
	for _, lc := range selected {
		langNames = append(langNames, languageDisplayName(lc.Lang))
	}
	fmt.Println(strings.Join(langNames, ", "))

	db, err := getAstraMapDB(projectRoot)
	if err != nil {
		logError("无法连接数据库: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	// Track auto-generated tsconfig.json for cleanup
	tsconfigExisted := hasAnyProjectFile(projectRoot, "tsconfig.json")

	// Generate SCIP indexes for all selected languages
	var scipPaths []string
	if *scipFile != "" && !*treesitterOnly {
		scipPaths = []string{*scipFile}
	} else if !*treesitterOnly {
		scipPaths, _ = autoGenerateScip(projectRoot, selected)
	}

	// Import all SCIP indexes
	for _, scipPath := range scipPaths {
		fmt.Printf("正在导入 SCIP 索引: %s\n", scipPath)
		if err := astramap.ImportScipIndexToAstraMap(db, scipPath, projectRoot); err != nil {
			logError("SCIP 导入失败: %v", err)
			os.Exit(1)
		}
		fmt.Println("SCIP 索引导入完成")
		if *scip || *scipFile == "" {
			os.Remove(scipPath)
		}
	}

	// Cleanup auto-generated tsconfig.json
	if !tsconfigExisted {
		tsconfigPath := filepath.Join(projectRoot, "tsconfig.json")
		os.Remove(tsconfigPath)
	}

	// Tree-sitter incremental scan for selected languages only
	var langFilter []string
	for _, lc := range selected {
		langFilter = append(langFilter, lc.Lang)
	}
	if err := astramap.SyncAllFilesAstraMap(db, projectRoot, langFilter...); err != nil {
		logError("增量扫描失败: %v", err)
		os.Exit(1)
	}

	// Show provenance breakdown: SCIP vs Tree-sitter vs heuristic
	nodeStats, edgeStats, _ := astramap.ProvenanceStats(db)
	fmt.Println("索引构建完成！")
	fmt.Println()
	fmt.Println("── 索引来源统计 ──")
	fmt.Printf("  节点 (按语言): %s\n", formatLangStats(nodeStats))
	fmt.Printf("  边   (按来源): %s\n", formatProvStats(edgeStats))
}

func parseLangSelection(input string, detected []LangCount) []LangCount {
	parts := strings.Split(input, ",")
	var selected []LangCount
	for _, p := range parts {
		p = strings.TrimSpace(p)
		idx, err := strconv.Atoi(p)
		if err != nil || idx < 1 || idx > len(detected) {
			fmt.Printf("忽略无效序号: %s\n", p)
			continue
		}
		selected = append(selected, detected[idx-1])
	}
	if len(selected) == 0 {
		return detected
	}
	return selected
}

func resolveLangNames(langFlag string, detected []LangCount) []LangCount {
	parts := strings.Split(langFlag, ",")
	detectedMap := make(map[string]LangCount, len(detected))
	for _, lc := range detected {
		detectedMap[lc.Lang] = lc
	}
	var selected []LangCount
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if lc, ok := detectedMap[p]; ok {
			selected = append(selected, lc)
		} else {
			fmt.Printf("忽略未检测到的语言: %s\n", p)
		}
	}
	if len(selected) == 0 {
		return detected
	}
	return selected
}

func formatLangStats(stats map[string]int) string {
	var parts []string
	for _, lang := range []string{"Go", "Python", "TypeScript", "JavaScript", "Java", "C", "C++"} {
		if cnt, ok := stats[lang]; ok {
			parts = append(parts, fmt.Sprintf("%s=%d", lang, cnt))
		}
	}
	for lang, cnt := range stats {
		found := false
		for _, known := range []string{"Go", "Python", "TypeScript", "JavaScript", "Java", "C", "C++"} {
			if lang == known {
				found = true
				break
			}
		}
		if !found {
			parts = append(parts, fmt.Sprintf("%s=%d", lang, cnt))
		}
	}
	if len(parts) == 0 {
		return "(无)"
	}
	var total int
	for _, cnt := range stats {
		total += cnt
	}
	return strings.Join(parts, ", ") + fmt.Sprintf(" (合计=%d)", total)
}

func formatProvStats(stats map[string]int) string {
	// Display in fixed order: scip → tree-sitter → heuristic → others
	var parts []string
	for _, prov := range []string{"scip", "tree-sitter", "heuristic"} {
		if cnt, ok := stats[prov]; ok {
			parts = append(parts, fmt.Sprintf("%s=%d", prov, cnt))
		}
	}
	for prov, cnt := range stats {
		if prov != "scip" && prov != "tree-sitter" && prov != "heuristic" {
			parts = append(parts, fmt.Sprintf("%s=%d", prov, cnt))
		}
	}
	if len(parts) == 0 {
		return "(无)"
	}
	var total int
	for _, cnt := range stats {
		total += cnt
	}
	return strings.Join(parts, ", ") + fmt.Sprintf(" (合计=%d)", total)
}

func installCmd() {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	showConfig := fs.Bool("show-config", false, "仅输出各工具配置 JSON，不执行写入")
	_ = fs.Parse(os.Args[2:])

	// 1. 确定自身绝对路径
	selfPath, err := os.Executable()
	if err != nil {
		logError("无法确定自身路径: %v", err)
		os.Exit(1)
	}
	selfPath, err = filepath.Abs(selfPath)
	if err != nil {
		logError("无法解析绝对路径: %v", err)
		os.Exit(1)
	}

	// 2. 确定项目绝对路径
	absProj := projectRoot

	// --show-config 模式：仅输出配置
	if *showConfig {
		printConfigs(selfPath, absProj)
		return
	}

	fmt.Println("正在注册 AstraMap MCP 服务与规则文件...")
	fmt.Println()

	success := 0
	total := 7

	// 3.1 Claude Code (MCP + /amap slash command)
	if installClaudeCode(selfPath, absProj) {
		success++
	}

	// 3.2 VS Code (MCP + Copilot instructions)
	if installVSCode(selfPath, absProj) {
		success++
	}

	// 3.3 Cursor (MCP + .cursor/rules)
	if installCursor(selfPath, absProj) {
		success++
	}

	// 3.4 项目级 .mcp.json
	if installProjectMcpJson(selfPath, absProj) {
		success++
	}

	// 3.5 Codex (MCP + AGENTS.md)
	if installCodex(selfPath, absProj) {
		success++
	}

	// 3.6 Windsurf (.windsurfrules)
	if installWindsurf(absProj) {
		success++
	}

	// 3.7 Cline (.clinerules)
	if installCline(absProj) {
		success++
	}

	fmt.Println()
	if success == total {
		fmt.Printf("安装完成！%d/%d 工具注册成功。\n", success, total)
	} else {
		fmt.Printf("安装完成！%d/%d 工具注册成功。未成功的工具可手动配置，运行 amap install --show-config 查看配置。\n", success, total)
	}

	// 4. 提示用户构建索引
	fmt.Println("\n下一步：构建代码地图索引")
	fmt.Println("  amap index                    # 交互选择语言，SCIP 优先；无 SCIP 时 Tree-sitter 回退")
	fmt.Println("  amap index --lang c           # 指定仅导入 C 语言（跳过交互）")
	fmt.Println("  amap index --scip             # 强制自动生成 SCIP 索引（高精度）")
	fmt.Println("  amap index --treesitter-only  # 仅 Tree-sitter 快速扫描")
}

// printConfigs 输出各工具的 MCP 配置 JSON
func printConfigs(amapPath, projectPath string) {
	fmt.Println("=== Claude Code (~/.claude.json 或项目 .mcp.json) ===")
	claudeCfg := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"astramap": map[string]interface{}{
				"command": amapPath,
				"args":    []string{"serve", "--project", "."},
				"env":     map[string]string{},
			},
		},
	}
	claudeData, _ := json.MarshalIndent(claudeCfg, "", "  ")
	fmt.Println(string(claudeData))

	fmt.Println("\n=== VS Code (.vscode/mcp.json) ===")
	vscodeCfg := map[string]interface{}{
		"servers": map[string]interface{}{
			"astramap": map[string]interface{}{
				"command": amapPath,
				"args":    []string{"serve", "--project", "."},
			},
		},
	}
	vscodeData, _ := json.MarshalIndent(vscodeCfg, "", "  ")
	fmt.Println(string(vscodeData))

	fmt.Println("\n=== Cursor (~/.cursor/mcp.json 或项目 .cursor/mcp.json) ===")
	cursorCfg := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"astramap": map[string]interface{}{
				"command": amapPath,
				"args":    []string{"serve", "--project", "${workspaceFolder}"},
			},
		},
	}
	cursorData, _ := json.MarshalIndent(cursorCfg, "", "  ")
	fmt.Println(string(cursorData))

	fmt.Println("\n=== CLI 快速安装命令 ===")
	fmt.Printf("  Claude Code:  claude mcp add --scope user astramap -- %s serve --project .\n", amapPath)
	fmt.Printf("  VS Code:      code --add-mcp '{\"name\":\"astramap\",\"command\":\"%s\",\"args\":[\"serve\",\"--project\",\".\"]}'\n", amapPath)
	fmt.Println("\n=== Claude Code Slash Command ===")
	fmt.Println("  /amap 命令: 安装后自动注册到 .claude/commands/amap.md")
	fmt.Println("  用法: /amap search <关键词> | /amap explore <描述> | /amap status")

	fmt.Println("\n=== 各工具规则文件 ===")
	fmt.Println("  Claude Code:  .claude/commands/amap.md (slash command)")
	fmt.Println("  VS Code:      .github/copilot-instructions.md (追加 AstraMap 段落)")
	fmt.Println("  Cursor:       .cursor/rules/astramap.mdc (alwaysApply: true)")
	fmt.Println("  Codex:        AGENTS.md (追加 AstraMap 段落) + ~/.codex/config.toml (MCP)")
	fmt.Println("  Windsurf:     .windsurfrules (追加 AstraMap 段落)")
	fmt.Println("  Cline:        .clinerules/astramap.md")

	fmt.Println("\n=== Codex MCP (TOML) ===")
	fmt.Printf("  CLI: codex mcp add astramap -- %s serve --project .\n", amapPath)
	fmt.Println("  或手动编辑 ~/.codex/config.toml:")
	fmt.Printf("    [mcp_servers.astramap]\n    command = \"%s\"\n    args = [\"serve\", \"--project\", \".\"]\n", amapPath)
	fmt.Println("    # 每个工具需设置 approval_mode = \"approve\"")
}

// installClaudeCode 注册到 Claude Code (MCP server + /amap slash command)
func installClaudeCode(amapPath, projectPath string) bool {
	mcpOK := false
	mcpMethod := ""

	// 优先使用 claude CLI
	if cliPath, err := exec.LookPath("claude"); err == nil {
		cmd := exec.Command(cliPath, "mcp", "add", "--scope", "user", "astramap", "--", amapPath, "serve", "--project", ".")
		output, err := cmd.CombinedOutput()
		if err == nil {
			mcpOK = true
			mcpMethod = "claude mcp add (user scope)"
		} else {
			logWarn("'claude mcp add' 执行失败: %s, 回退到手动写入配置", strings.TrimSpace(string(output)))
		}
	}

	// Fallback: 手动写入 ~/.claude.json
	if !mcpOK {
		home, _ := os.UserHomeDir()
		configPath := filepath.Join(home, ".claude.json")
		if err := writeMcpConfig(configPath, "mcpServers", "astramap", map[string]interface{}{
			"command": amapPath,
			"args":    []string{"serve", "--project", "."},
			"env":     map[string]string{},
		}); err != nil {
			fmt.Printf("  ✗ Claude Code  — MCP 注册失败: %v\n", err)
			return false
		}
		mcpOK = true
		mcpMethod = configPath
	}

	// 注册 /amap slash command
	cmdOK := installSlashCommand(projectPath)

	// 汇总输出
	if mcpOK && cmdOK {
		fmt.Printf("  ✓ Claude Code  — MCP 已注册 (%s) + /amap 命令已就绪\n", mcpMethod)
	} else if mcpOK {
		fmt.Printf("  ✓ Claude Code  — MCP 已注册 (%s)，/amap 命令注册失败\n", mcpMethod)
	}
	return mcpOK
}

// installSlashCommand 创建 .claude/commands/amap.md 注册 /amap slash command
func installSlashCommand(projectPath string) bool {
	cmdsDir := filepath.Join(projectPath, ".claude", "commands")
	if err := os.MkdirAll(cmdsDir, 0755); err != nil {
		logWarn("创建 .claude/commands 目录失败: %v", err)
		return false
	}

	amapCmdPath := filepath.Join(cmdsDir, "amap.md")
	if err := os.WriteFile(amapCmdPath, []byte(amapSlashCommandTpl), 0644); err != nil {
		logWarn("写入 %s 失败: %v", amapCmdPath, err)
		return false
	}
	return true
}

const amapSlashCommandTpl = `---
description: AstraMap 代码地图查询
argument-hint: <子命令> <参数>
allowed-tools: astramap_search astramap_explore astramap_node astramap_callers astramap_callees astramap_impact astramap_status astramap_verdict astramap_trace astramap_files
---

根据用户输入执行 AstraMap 代码地图查询。

子命令映射：
- ` + "`" + `search <关键词>` + "`" + ` → 调用 astramap_search 模糊搜索符号
- ` + "`" + `explore <描述>` + "`" + ` → 调用 astramap_explore 探索代码上下文
- ` + "`" + `node <符号名>` + "`" + ` → 调用 astramap_node 查看符号详情
- ` + "`" + `callers <符号>` + "`" + ` → 调用 astramap_callers 追溯调用源
- ` + "`" + `callees <符号>` + "`" + ` → 调用 astramap_callees 追溯被调用依赖
- ` + "`" + `impact <符号>` + "`" + ` → 调用 astramap_impact 分析变更波及
- ` + "`" + `trace <from> <to>` + "`" + ` → 调用 astramap_trace 追踪调用路径
- ` + "`" + `status` + "`" + ` → 调用 astramap_status 查看索引状态
- ` + "`" + `files [路径]` + "`" + ` → 调用 astramap_files 列出已索引文件

用户输入: $ARGUMENTS
`

// astramapRulesContent 是所有工具规则文件共享的核心指令内容
const astramapRulesContent = `AstraMap 是当前项目的代码地图 MCP 服务。当用户询问代码结构相关问题时，必须优先使用 AstraMap 工具而非 grep 或文件搜索：

- 查找符号定义 → astramap_search
- 理解代码上下文和调用关系 → astramap_explore
- 查看符号详情和源码 → astramap_node
- 追溯谁调用了某符号 → astramap_callers
- 追溯某符号调用了什么 → astramap_callees
- 评估修改影响范围 → astramap_impact
- 追踪 A 到 B 的调用路径 → astramap_trace
- 检查索引状态 → astramap_status
`

// installVSCode 注册到 VS Code (MCP server + Copilot instructions)
func installVSCode(amapPath, projectPath string) bool {
	mcpOK := false
	mcpMethod := ""

	// 优先使用 code CLI
	if cliPath, err := exec.LookPath("code"); err == nil {
		mcpJson, _ := json.Marshal(map[string]interface{}{
			"name":    "astramap",
			"command": amapPath,
			"args":    []string{"serve", "--project", "."},
		})
		cmd := exec.Command(cliPath, "--add-mcp", string(mcpJson))
		output, err := cmd.CombinedOutput()
		if err == nil {
			mcpOK = true
			mcpMethod = "code --add-mcp"
		} else {
			logWarn("'code --add-mcp' 执行失败: %s, 回退到手动写入配置", strings.TrimSpace(string(output)))
		}
	}

	// Fallback: 写入 .vscode/mcp.json
	if !mcpOK {
		configPath := filepath.Join(projectPath, ".vscode", "mcp.json")
		if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
			fmt.Printf("  ✗ VS Code      — 创建 .vscode 目录失败: %v\n", err)
			return false
		}
		if err := writeMcpConfig(configPath, "servers", "astramap", map[string]interface{}{
			"command": amapPath,
			"args":    []string{"serve", "--project", "."},
		}); err != nil {
			fmt.Printf("  ✗ VS Code      — MCP 注册失败: %v\n", err)
			return false
		}
		mcpOK = true
		mcpMethod = configPath
	}

	// 注册 Copilot instructions
	instOK := appendRulesFile(filepath.Join(projectPath, ".github", "copilot-instructions.md"), "## AstraMap", astramapRulesContent)

	if mcpOK && instOK {
		fmt.Printf("  ✓ VS Code      — MCP 已注册 (%s) + Copilot 规则已写入\n", mcpMethod)
	} else if mcpOK {
		fmt.Printf("  ✓ VS Code      — MCP 已注册 (%s)，Copilot 规则写入失败\n", mcpMethod)
	}
	return mcpOK
}

// installCursor 注册到 Cursor (MCP server + rules)
func installCursor(amapPath, projectPath string) bool {
	home, _ := os.UserHomeDir()

	// 写入全局 ~/.cursor/mcp.json
	globalPath := filepath.Join(home, ".cursor", "mcp.json")
	if err := writeMcpConfig(globalPath, "mcpServers", "astramap", map[string]interface{}{
		"command": amapPath,
		"args":    []string{"serve", "--project", "${workspaceFolder}"},
	}); err != nil {
		fmt.Printf("  ✗ Cursor       — 写入 %s 失败: %v\n", globalPath, err)
		return false
	}

	// 注册 .cursor/rules/astramap.mdc
	rulesDir := filepath.Join(projectPath, ".cursor", "rules")
	mdcOK := false
	if err := os.MkdirAll(rulesDir, 0755); err == nil {
		mdcContent := "---\nalwaysApply: true\n---\n\n" + astramapRulesContent
		mdcPath := filepath.Join(rulesDir, "astramap.mdc")
		if err := os.WriteFile(mdcPath, []byte(mdcContent), 0644); err == nil {
			mdcOK = true
		}
	}

	if mdcOK {
		fmt.Printf("  ✓ Cursor       — MCP 已写入 + 规则已注册 (.cursor/rules/astramap.mdc)\n")
	} else {
		fmt.Printf("  ✓ Cursor       — MCP 已写入 %s\n", globalPath)
	}
	return true
}

// installProjectMcpJson 写入项目级 .mcp.json（Claude Code 团队共享）
func installProjectMcpJson(amapPath, projectPath string) bool {
	configPath := filepath.Join(projectPath, ".mcp.json")
	if err := writeMcpConfig(configPath, "mcpServers", "astramap", map[string]interface{}{
		"command": amapPath,
		"args":    []string{"serve", "--project", "."},
		"type":    "stdio",
	}); err != nil {
		fmt.Printf("  ✗ 项目 .mcp.json — 写入 %s 失败: %v\n", configPath, err)
		return false
	}
	fmt.Printf("  ✓ 项目 .mcp.json — 已写入 %s (团队成员自动可用)\n", configPath)
	return true
}

// installCodex 注册到 OpenAI Codex (AGENTS.md)
func installCodex(amapPath, projectPath string) bool {
	ok1 := appendRulesFile(filepath.Join(projectPath, "AGENTS.md"), "## AstraMap", astramapRulesContent)
	ok2 := installCodexMcp(amapPath)
	switch {
	case ok1 && ok2:
		fmt.Println("  ✓ Codex         — MCP 已注册 + 规则已写入 AGENTS.md")
	case ok1:
		fmt.Println("  ✓ Codex         — 规则已写入 AGENTS.md（MCP 注册失败，请手动运行: codex mcp add astramap -- <amap-path> serve --project .）")
	case ok2:
		fmt.Println("  ✓ Codex         — MCP 已注册（AGENTS.md 写入失败）")
	default:
		fmt.Println("  ✗ Codex         — MCP 注册与 AGENTS.md 写入均失败")
	}
	return ok1 || ok2
}

func installCodexMcp(amapPath string) bool {
	// 优先使用 codex mcp add CLI
	if p, err := exec.LookPath("codex"); err == nil {
		cmd := exec.Command(p, "mcp", "add", "astramap", "--", amapPath, "serve", "--project", ".")
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			logWarn("codex mcp add 失败: %v，回退到 TOML 编辑", err)
		} else {
			// 追加工具审批配置
			appendCodexToolApprovals()
			return true
		}
	}

	// 回退：直接编辑 ~/.codex/config.toml
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	configPath := filepath.Join(home, ".codex", "config.toml")
	return appendCodexTomlMcp(configPath, amapPath)
}

func appendCodexToolApprovals() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	configPath := filepath.Join(home, ".codex", "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return
	}
	content := string(data)
	if strings.Contains(content, "[mcp_servers.astramap.tools.astramap_search]") {
		return // 已有工具审批配置
	}
	tools := []string{
		"astramap_search", "astramap_explore", "astramap_node",
		"astramap_callers", "astramap_callees", "astramap_impact",
		"astramap_status", "astramap_verdict", "astramap_trace", "astramap_files",
	}
	var sb strings.Builder
	sb.WriteString("\n")
	for _, t := range tools {
		sb.WriteString(fmt.Sprintf("\n[mcp_servers.astramap.tools.%s]\napproval_mode = \"approve\"", t))
	}
	// 在 [mcp_servers.astramap] 段落之后追加
	idx := strings.Index(content, "[mcp_servers.astramap]")
	if idx == -1 {
		return
	}
	// 找到下一个 [ 段落的位置
	nextSection := strings.Index(content[idx+1:], "\n[")
	if nextSection == -1 {
		content += sb.String()
	} else {
		insertPos := idx + 1 + nextSection
		content = content[:insertPos] + sb.String() + content[insertPos:]
	}
	_ = os.WriteFile(configPath, []byte(content), 0644)
}

func appendCodexTomlMcp(configPath, amapPath string) bool {
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return false
	}
	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return false
	}
	content := string(data)
	if strings.Contains(content, "[mcp_servers.astramap]") {
		return true // 已注册
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n[mcp_servers.astramap]\ncommand = \"%s\"\nargs = [\"serve\", \"--project\", \".\"]", amapPath))
	tools := []string{
		"astramap_search", "astramap_explore", "astramap_node",
		"astramap_callers", "astramap_callees", "astramap_impact",
		"astramap_status", "astramap_verdict", "astramap_trace", "astramap_files",
	}
	for _, t := range tools {
		sb.WriteString(fmt.Sprintf("\n\n[mcp_servers.astramap.tools.%s]\napproval_mode = \"approve\"", t))
	}
	if !strings.HasSuffix(content, "\n") {
		sb.WriteString("\n")
	}
	content += sb.String()
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return false
	}
	return true
}

// installWindsurf 注册到 Windsurf (.windsurfrules)
func installWindsurf(projectPath string) bool {
	rulesPath := filepath.Join(projectPath, ".windsurfrules")
	if ok := appendRulesFile(rulesPath, "## AstraMap", astramapRulesContent); ok {
		fmt.Println("  ✓ Windsurf      — 规则已写入 .windsurfrules")
		return true
	}
	fmt.Println("  ✗ Windsurf      — .windsurfrules 写入失败")
	return false
}

// installCline 注册到 Cline (.clinerules/astramap.md)
func installCline(projectPath string) bool {
	rulesDir := filepath.Join(projectPath, ".clinerules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		fmt.Printf("  ✗ Cline         — 创建 .clinerules 目录失败: %v\n", err)
		return false
	}
	rulesPath := filepath.Join(rulesDir, "astramap.md")
	if err := os.WriteFile(rulesPath, []byte(astramapRulesContent), 0644); err != nil {
		fmt.Printf("  ✗ Cline         — 写入 %s 失败: %v\n", rulesPath, err)
		return false
	}
	fmt.Println("  ✓ Cline         — 规则已写入 .clinerules/astramap.md")
	return true
}

// appendRulesFile 向规则文件追加段落：若文件已存在且包含同标题段落则跳过，否则追加
func appendRulesFile(filePath, sectionTitle, sectionContent string) bool {
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return false
	}

	var existing string
	data, err := os.ReadFile(filePath)
	if err == nil {
		existing = string(data)
	}

	// 已存在同标题段落则跳过
	if strings.Contains(existing, sectionTitle) {
		return true
	}

	var newContent string
	if existing == "" {
		newContent = sectionTitle + "\n\n" + sectionContent
	} else {
		// 确保前文末尾有换行
		if !strings.HasSuffix(existing, "\n") {
			existing += "\n"
		}
		newContent = existing + "\n" + sectionTitle + "\n\n" + sectionContent
	}

	return os.WriteFile(filePath, []byte(newContent), 0644) == nil
}

// writeMcpConfig 安全写入 MCP 配置：备份 → 合并 → 写入 → 验证
func writeMcpConfig(configPath, topKey, serverName string, serverCfg map[string]interface{}) error {
	// 确保父目录存在
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	// 读取现有配置
	var cfg map[string]interface{}
	data, err := os.ReadFile(configPath)
	if err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &cfg); err != nil {
			// JSON 损坏，备份后重建
			backupPath := configPath + ".bak"
			_ = os.WriteFile(backupPath, data, 0644)
			logWarn("现有配置 JSON 损坏已备份到 %s，将重建", backupPath)
			cfg = make(map[string]interface{})
		}
	}
	if cfg == nil {
		cfg = make(map[string]interface{})
	}

	// 获取或创建顶层 key (mcpServers / servers)
	topVal, exists := cfg[topKey]
	var servers map[string]interface{}
	if exists {
		if m, ok := topVal.(map[string]interface{}); ok {
			servers = m
		}
	}
	if servers == nil {
		servers = make(map[string]interface{})
	}

	// 注入服务器配置
	servers[serverName] = serverCfg
	cfg[topKey] = servers

	// 写入
	newData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("JSON 编码失败: %w", err)
	}
	if err := os.WriteFile(configPath, newData, 0644); err != nil {
		return fmt.Errorf("写入文件失败: %w", err)
	}

	// 验证
	verifyData, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("写入后验证读取失败: %w", err)
	}
	if !json.Valid(verifyData) {
		return fmt.Errorf("写入后验证 JSON 非法")
	}
	return nil
}

func diffCmd() {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	suggestTests := fs.Bool("suggest-tests", false, "提供单元测试执行建议")
	_ = fs.Parse(os.Args[2:])

	db, err := getAstraMapDB(projectRoot)
	if err != nil {
		logError("无法连接数据库: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	// 运行 git diff
	cmd := exec.Command("git", "diff", "--name-only")
	out, err := cmd.Output()
	if err != nil {
		fmt.Println("目前无脏改动文件，工作区干净！")
		return
	}

	files := strings.Split(string(out), "\n")
	var symbols []string
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		// 查出该文件中的所有符号
		var symIDs []string
		_ = db.Select(&symIDs, "SELECT id FROM astramap_nodes WHERE file_path = ?", f)
		symbols = append(symbols, symIDs...)
	}

	if len(symbols) == 0 {
		fmt.Println("没有检测到因改动而影响的代码符号。")
		return
	}

	fmt.Printf("检测到您变动了 %d 个符号，正在分析上游波及面...\n\n", len(symbols))
	seen := make(map[string]bool)
	for _, sym := range symbols {
		res, err := astramap.AnalyzeImpact(db, sym, 2)
		if err == nil {
			for _, node := range res.AffectedNodes {
				if !seen[node.SymbolID] {
					seen[node.SymbolID] = true
					fmt.Printf("- %s [%s] (%s)\n", node.SymbolID, node.ImpactLevel, node.Reason)
				}
			}
		}
	}

	if *suggestTests {
		fmt.Println("\n[测试建议]:")
		fmt.Println("建议运行关联模块单元测试：")
		fmt.Println("  go test -v ./...")
	}
}

func locateCmd() {
	if len(os.Args) < 3 {
		fmt.Println("用法: amap locate <symbol_name>")
		os.Exit(1)
	}
	symbol := os.Args[2]

	db, err := getAstraMapDB(projectRoot)
	if err != nil {
		logError("无法连接数据库: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	var nodes []struct {
		FilePath  string `db:"file_path"`
		StartLine int    `db:"start_line"`
		Kind      string `db:"kind"`
	}

	err = db.Select(&nodes, "SELECT file_path, start_line, kind FROM astramap_nodes WHERE qualified_name LIKE ? OR name = ?", "%"+symbol+"%", symbol)
	if err != nil || len(nodes) == 0 {
		fmt.Printf("无法定位符号 \"%s\"\n", symbol)
		os.Exit(1)
	}

	for _, n := range nodes {
		fmt.Printf("[%s] %s:%d\n", n.Kind, n.FilePath, n.StartLine)
	}
}

func hotspotsCmd() {
	fs := flag.NewFlagSet("hotspots", flag.ExitOnError)
	_ = fs.Parse(os.Args[2:])

	db, err := getAstraMapDB(projectRoot)
	if err != nil {
		logError("数据库连接失败: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	// 获取所有已索引文件路径
	var files []string
	if err := db.Select(&files, "SELECT path FROM astramap_files"); err != nil {
		logError("查询文件列表失败: %v", err)
		os.Exit(1)
	}

	if len(files) == 0 {
		fmt.Println("索引中没有文件记录，请先执行 amap index。")
		return
	}

	// 检测 git 可用性
	useGit := true
	if err := exec.Command("git", "rev-parse", "--git-dir").Run(); err != nil {
		logWarn("当前目录非 git 仓库或 git 不可用，将使用文件修改时间代替提交次数。")
		useGit = false
	}

	type hotspot struct {
		FilePath  string
		Commits   int
		FuncCount int
	}

	var results []hotspot
	for _, fp := range files {
		var commits int
		if useGit {
			out, err := exec.Command("git", "log", "--oneline", "--follow", fp).Output()
			if err == nil {
				lines := strings.Split(strings.TrimSpace(string(out)), "\n")
				if len(lines) == 1 && lines[0] == "" {
					commits = 0
				} else {
					commits = len(lines)
				}
			}
		} else {
			info, err := os.Stat(fp)
			if err == nil {
				// 用距今天数作为伪提交次数（越新越活跃）
				commits = int(info.ModTime().Unix() / 86400)
			}
		}

		var funcCount int
		_ = db.Get(&funcCount, "SELECT COUNT(*) FROM astramap_nodes WHERE file_path = ? AND kind IN ('function', 'method')", fp)

		results = append(results, hotspot{FilePath: fp, Commits: commits, FuncCount: funcCount})
	}

	// 按提交次数降序排列
	sort.Slice(results, func(i, j int) bool {
		return results[i].Commits > results[j].Commits
	})

	// 输出 Top 10
	limit := 10
	if len(results) < limit {
		limit = len(results)
	}

	fmt.Println("### ── 代码热点 Top 10 (按变更频次降序) ──\n")
	fmt.Printf("%-60s  %s  %s\n", "文件路径", "提交次数", "函数数量")
	fmt.Println(strings.Repeat("─", 80))
	for i := 0; i < limit; i++ {
		h := results[i]
		fmt.Printf("%-60s  %-8d  %d\n", h.FilePath, h.Commits, h.FuncCount)
	}
}

func deadcodeCmd() {
	db, err := getAstraMapDB(projectRoot)
	if err != nil {
		logError("数据库失败: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	dead, err := astramap.FindDeadCode(db, nil)
	if err != nil {
		logError("Deadcode 遍历错误: %v", err)
		os.Exit(1)
	}

	fmt.Printf("### ── Deadcode 检查结果 (找到 %d 个死节点) ──\n\n", len(dead))
	if len(dead) == 0 {
		fmt.Println("🎉 完美！您的项目中所有声明函数均由已知入口可达，无任何死代码冗余。")
	} else {
		for i, d := range dead {
			fmt.Printf("%d. [%s] %s (%s:%d)\n", i+1, d.Kind, d.QualifiedName, d.FilePath, d.StartLine)
		}
	}
}

func cyclesCmd() {
	db, err := getAstraMapDB(projectRoot)
	if err != nil {
		os.Exit(1)
	}
	defer db.Close()

	cycles, err := astramap.FindCycles(db, "file")
	if err != nil {
		logError("依赖检测失败: %v", err)
		os.Exit(1)
	}

	fmt.Printf("### ── 循环依赖链检测 (找到 %d 个循环环路) ──\n\n", len(cycles))
	if len(cycles) == 0 {
		fmt.Println("🎉 成功！没有检测到任何文件/包之间的循环依赖导入。")
	} else {
		for i, c := range cycles {
			fmt.Printf("Cycle %d:\n  %s\n", i+1, strings.Join(c, " ──► "))
		}
	}
}

func couplingCmd() {
	fs := flag.NewFlagSet("coupling", flag.ExitOnError)
	path := fs.String("path", "", "特定模块路径前缀")
	_ = fs.Parse(os.Args[2:])

	db, err := getAstraMapDB(projectRoot)
	if err != nil {
		os.Exit(1)
	}
	defer db.Close()

	metrics, err := astramap.GetCouplingMetrics(db, *path)
	if err != nil {
		logError("获取耦合失败: %v", err)
		os.Exit(1)
	}

	fmt.Printf("### ── 架构内聚度 Ca/Ce 分析 ──\n\n")
	fmt.Printf("目标前缀范围: \"%s\"\n", *path)
	fmt.Printf("• 输入耦合 (Afferent Coupling, Ca): %d (外部调用本包的链接数)\n", metrics.Ca)
	fmt.Printf("• 输出耦合 (Efferent Coupling, Ce): %d (本包调用外部的链接数)\n", metrics.Ce)
	instability := 0.0
	if metrics.Ca+metrics.Ce > 0 {
		instability = float64(metrics.Ce) / float64(metrics.Ca+metrics.Ce)
	}
	fmt.Printf("• 架构不稳定系数 (Instability, I): %.2f (0:高度稳定, 1:高度脆弱)\n", instability)
}

func ownersCmd() {
	if len(os.Args) < 3 {
		fmt.Println("用法: amap owners <symbol_id>")
		os.Exit(1)
	}
	symbol := os.Args[2]

	db, err := getAstraMapDB(projectRoot)
	if err != nil {
		os.Exit(1)
	}
	defer db.Close()

	owners, err := astramap.GetCodeOwners(db, symbol, projectRoot)
	if err != nil {
		logError("提取作者失败: %v", err)
		os.Exit(1)
	}

	fmt.Printf("### ── 符号 %s 代码所有权分布 (Code Owners) ──\n\n", symbol)
	for i, o := range owners {
		fmt.Printf("%d. %s — 贡献度: %.1f%% (提交次数: %d)\n", i+1, o.Author, o.Percent, o.CommitCount)
	}
}

func queryCmd() {
	if len(os.Args) < 3 {
		fmt.Println("用法: amap query \"<SQL>\"")
		os.Exit(1)
	}
	sqlStr := os.Args[2]

	db, err := getAstraMapDB(projectRoot)
	if err != nil {
		os.Exit(1)
	}
	defer db.Close()

	rows, err := db.Queryx(sqlStr)
	if err != nil {
		logError("SQL 语法或执行错误: %v", err)
		os.Exit(1)
	}
	defer rows.Close()

	cols, _ := rows.Columns()
	fmt.Println(strings.Join(cols, "\t| "))
	fmt.Println(strings.Repeat("-", 60))

	count := 0
	for rows.Next() {
		results, _ := rows.SliceScan()
		var strs []string
		for _, r := range results {
			if r == nil {
				strs = append(strs, "NULL")
			} else {
				strs = append(strs, fmt.Sprintf("%v", r))
			}
		}
		fmt.Println(strings.Join(strs, "\t| "))
		count++
	}
	fmt.Printf("\n(%d rows returned)\n", count)
}

func treeCmd() {
	if len(os.Args) < 3 {
		fmt.Println("用法: amap tree <symbol> [--dir=up|down] [--depth=3]")
		os.Exit(1)
	}
	symbol := os.Args[2]

	fs := flag.NewFlagSet("tree", flag.ExitOnError)
	dir := fs.String("dir", "down", "遍历方向: up (calls) 或 down (callees)")
	depth := fs.Int("depth", 3, "递归树深度")
	_ = fs.Parse(os.Args[3:])

	db, err := getAstraMapDB(projectRoot)
	if err != nil {
		os.Exit(1)
	}
	defer db.Close()

	ids, resolveErr := astramap.ResolveSymbolToIDs(db, symbol)
	if resolveErr != nil || len(ids) == 0 {
		fmt.Printf("符号 \"%s\" 未找到\n", symbol)
		os.Exit(1)
	}
	resolvedID := ids[0]

	fmt.Printf("### ── 符号 %s 调用拓扑树 (方向:%s, 深度:%d) ──\n\n", symbol, *dir, *depth)

	seen := map[string]bool{resolvedID: true}

	var printTree func(id string, level int, isLast bool)
	printTree = func(id string, level int, isLast bool) {
		if level > 0 {
			for i := 0; i < level-1; i++ {
				fmt.Print("│   ")
			}
			if isLast {
				fmt.Print("└── ")
			} else {
				fmt.Print("├── ")
			}
		}
		fmt.Println(id)

		if level >= *depth {
			return
		}

		var children []string
		if *dir == "down" {
			callees, _ := astramap.GetCallees(db, id)
			for _, c := range callees {
				children = append(children, c.Target)
			}
		} else {
			callers, _ := astramap.GetCallers(db, id)
			for _, c := range callers {
				children = append(children, c.Source)
			}
		}

		for i, child := range children {
			if seen[child] {
				for j := 0; j < level; j++ {
					fmt.Print("│   ")
				}
				if i == len(children)-1 {
					fmt.Printf("└── %s (cycle)\n", child)
				} else {
					fmt.Printf("├── %s (cycle)\n", child)
				}
				continue
			}
			seen[child] = true
			printTree(child, level+1, i == len(children)-1)
		}
	}

	printTree(resolvedID, 0, true)
}

func auditCmd() {
	db, err := getAstraMapDB(projectRoot)
	if err != nil {
		os.Exit(1)
	}
	defer db.Close()

	var rejectCount int
	_ = db.Get(&rejectCount, "SELECT COUNT(*) FROM astramap_verdicts WHERE has_active_defect = 1")

	fmt.Printf("正在执行本地审计门禁扫描...\n")
	if rejectCount > 0 {
		fmt.Printf("❌ 审计未通过！在当前项目发现有 %d 处 REJECTED 的代码缺陷 Verdicts。\n", rejectCount)
		os.Exit(1)
	}
	fmt.Println("✅ 审计通过！没有检测到任何被拦截的缺陷。")
}
