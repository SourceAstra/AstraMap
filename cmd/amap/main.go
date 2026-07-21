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
	"time"

	"astramap-standalone/astramap"
	"github.com/fsnotify/fsnotify"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// logInfo, logError, logWarn, logDebug replicated or simulated to ensure main package compatibility
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

	db, err := sqlx.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(10000)&_pragma=mmap_size(268435456)&_pragma=cache_size(-65536)&_pragma=temp_store(MEMORY)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	// Reclaim WAL lock left over from previous abnormal exit
	db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")

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
	defer astramap.CloseLanguageModules()

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
	case "watch":
		watchCmd()
	case "install":
		installCmd()
	case "syntax":
		syntaxCmd()
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
	default:
		fmt.Printf("Unknown subcommand: %s\n\n", subcmd)
		printHelp()
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Println(`AstraMap — High-precision dynamic code map MCP engine for AI programming agents

Usage:
  amap <command> [arguments]
  All commands support --project <path> to specify project root directory

Core Commands:
  serve                                       Launch stdio MCP server
  dashboard [--host] [--port]                Start source code starfield visualization console
  index [options]                            Build/update code map index
      --lang c,python                        Specify language
      --scip index.scip                      Import existing SCIP index file
      --scip-only                           Import only SCIP, skip optional Syntax Overlay
      --tree-sitter                         Syntax Overlay only, skip SCIP generation and import
      --refresh-scip                         Force regenerate and import SCIP
      --full                                Full refresh SCIP layer, then optional Syntax Overlay
      --watch [seconds]                      Continue low-frequency monitoring and incremental refresh after indexing (default 10s)
  watch [seconds]                            Initial indexing then continuous low-frequency monitoring with incremental refresh (default 10s)
  install                                   One-click MCP installation to Claude Code / Cursor
  syntax <action>                           Install and manage optional Syntax Overlays

Common Examples:
  amap index                                  Quick incremental update once
  amap index --full                           Full refresh once
  amap index --watch 30                       Index once at startup, then incremental refresh every 30 seconds max
  amap watch                                  Incremental refresh every 10 seconds max
  amap watch 30                               Incremental refresh every 30 seconds max

Development Diagnostic Tools (CLI Diagnostics):
  diff [--suggest-tests]                      Evaluate modification impact and test suggestions based on git diff
  locate <symbol>                             Quickly locate symbol definition's physical path and line/column numbers
  hotspots                                    Detect code hotspots based on Git modification frequency and cyclomatic complexity
  deadcode                                    Code reachability check, analyze redundant dead code
  cycles                                      Circular dependency detection
  coupling [--path=...]                       Module Ca/Ce cohesion and coupling analysis
  owners <symbol>                             Use GitBlame to identify the owner most familiar with this symbol
  query "<SQL>"                               Directly manipulate and retrieve underlying graph topology via SQL
  tree <symbol> [--dir=up|down] [--depth=N]   Draw call topology tree of specified symbol in terminal
`)
}

// ===== Specific command implementations =====

func serveCmd() {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	_ = fs.Parse(os.Args[2:])

	db, err := getAstraMapDB(projectRoot)
	if err != nil {
		logError("Failed to connect to code map database: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	astramap.RunMcpServer(db, projectRoot)
}

func dashboardCmd() {
	fs := flag.NewFlagSet("dashboard", flag.ExitOnError)
	host := fs.String("host", "0.0.0.0", "Web service listening address")
	port := fs.Int("port", 3000, "Web service port number")
	foreground := fs.Bool("foreground", false, "Run Dashboard in foreground mode")
	_ = fs.Parse(os.Args[2:])

	resolvedPort, err := findAvailablePort(*host, *port)
	if err != nil {
		logError("Failed to find available Dashboard port: %v", err)
		os.Exit(1)
	}
	if resolvedPort != *port {
		logWarn("Dashboard port %d is already in use, automatically switching to %d", *port, resolvedPort)
	}
	if !*foreground {
		startDashboardBackground(projectRoot, *host, resolvedPort)
		return
	}

	db, err := getAstraMapDB(projectRoot)
	if err != nil {
		logError("Failed to connect to code map database: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	printDashboardURLs(*host, resolvedPort)
	err = astramap.StartStandaloneServer(db, projectRoot, *host, resolvedPort)
	if err != nil {
		logError("Web server startup failed: %v", err)
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
	return 0, fmt.Errorf("No available ports from %d to 65535", startPort)
}

func startDashboardBackground(projectRoot, host string, port int) {
	exe, err := os.Executable()
	if err != nil {
		logError("Failed to locate current binary: %v", err)
		os.Exit(1)
	}

	logPath := filepath.Join(projectRoot, ".astramap", "dashboard.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		logError("Failed to create log directory: %v", err)
		os.Exit(1)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		logError("Failed to open Dashboard log: %v", err)
		os.Exit(1)
	}
	defer logFile.Close()

	args := []string{"dashboard", "--project", projectRoot, "--host", host, "--port", fmt.Sprintf("%d", port), "--foreground"}
	cmd := exec.Command(exe, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		logError("Dashboard background startup failed: %v", err)
		os.Exit(1)
	}
	if err := cmd.Process.Release(); err != nil {
		logWarn("Dashboard process started but failed to release process handle: %v", err)
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

// ===== SCIP Auto-detection and Generation =====

// LangCount holds a detected language with its source file count.
type LangCount struct {
	Lang  string
	Count int
}

func detectProjectLanguages(projectRoot string, filter *astramap.IndexFilter) []LangCount {
	profile := astramap.BuildProjectProfile(projectRoot, filter, astramap.StageDetect)
	countsByLanguage := astramap.ProjectLanguageCounts(profile)

	ranked := make([]LangCount, 0, len(countsByLanguage))
	for _, spec := range astramap.LanguageSpecsForProject(projectRoot) {
		count := countsByLanguage[spec.ID]
		if count > 0 {
			ranked = append(ranked, LangCount{Lang: spec.ID, Count: count})
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].Count > ranked[j].Count
	})
	return ranked
}

func findScipTool(lang, registryRoot, unitRoot string) (string, bool) {
	name := astramap.ScipToolNameForProject(registryRoot, lang)
	if name == "" {
		return "", false
	}
	_ = unitRoot
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	if provider, ok := astramap.SemanticProviderForProjectLanguage(registryRoot, lang); ok && provider.Recipe == astramap.ScipRecipeGo {
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
	return astramap.LanguageDisplayNameForProject(projectRoot, lang)
}

type cleanupStack struct {
	actions []func() error
}

func (stack *cleanupStack) add(action func() error) {
	stack.actions = append(stack.actions, action)
}

func (stack *cleanupStack) runReverse() {
	for i := len(stack.actions) - 1; i >= 0; i-- {
		if err := stack.actions[i](); err != nil && !os.IsNotExist(err) {
			logWarn("SCIP cleanup failed: %v", err)
		}
	}
}

type preparedScipRun struct {
	command  *exec.Cmd
	artifact string
	cleanup  cleanupStack
}

type scipRecipe func(string, astramap.SemanticProviderSpec, string, string, *astramap.IndexFilter) (preparedScipRun, error)

var scipRecipes = map[astramap.ScipRecipe]scipRecipe{
	astramap.ScipRecipeGo:      defaultArtifactRecipe(),
	astramap.ScipRecipeNode:    prepareNodeScip,
	astramap.ScipRecipePython:  preparePythonScip,
	astramap.ScipRecipeClang:   prepareClangScip,
	astramap.ScipRecipeJVM:     commandRecipe("index", "--output"),
	astramap.ScipRecipeRust:    defaultArtifactRecipe("scip", "."),
	astramap.ScipRecipeDotNet:  defaultArtifactRecipe("index"),
	astramap.ScipRecipePackage: preparePackageScip,
}

type scipReadiness func(astramap.ProjectUnit) bool

var scipReadinessChecks = map[astramap.ScipRecipe]scipReadiness{
	astramap.ScipRecipeGo:     unitHasExactManifest("go.mod"),
	astramap.ScipRecipeClang:  clangUnitReady,
	astramap.ScipRecipeJVM:    unitHasExactManifest("pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", "build.sbt", "scip-java.json"),
	astramap.ScipRecipeRust:   unitHasExactManifest("Cargo.toml"),
	astramap.ScipRecipeDotNet: unitHasManifestSuffix(".sln", ".csproj"),
	astramap.ScipRecipePackage: func(unit astramap.ProjectUnit) bool {
		return len(unit.Manifests) > 0
	},
}

func scipUnitReady(recipe astramap.ScipRecipe, unit astramap.ProjectUnit) bool {
	check := scipReadinessChecks[recipe]
	return check == nil || check(unit)
}

func ensureUnitToolchains(projectRoot string, unit astramap.ProjectUnit) error {
	for _, language := range unit.Languages {
		if provider, ok := astramap.SemanticProviderForProjectLanguage(projectRoot, language); ok && provider.Recipe == astramap.ScipRecipeJVM {
			var supportedBuildFiles []string
			switch language {
			case "kotlin":
				supportedBuildFiles = []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", "scip-java.json"}
			case "scala":
				supportedBuildFiles = []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", "build.sbt", "scip-java.json"}
			default:
				supportedBuildFiles = []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", "build.sbt", "scip-java.json"}
			}
			if !unitHasAnyManifest(unit, supportedBuildFiles) {
				return fmt.Errorf("%s is not supported by scip-java for the detected build tool in %s", languageDisplayName(language), unit.Root)
			}
		}
		for _, requirement := range astramap.LanguageToolchainRequirementsForProject(projectRoot, language) {
			if len(requirement.WhenAnyFiles) > 0 && !unitHasAnyManifest(unit, requirement.WhenAnyFiles) {
				continue
			}
			if toolchainRequirementAvailable(unit.Root, requirement) {
				continue
			}
			if requirement.Installer == nil {
				return fmt.Errorf("%s dependency is unavailable for %s\nInstall: %s",
					requirement.Label, languageDisplayName(language), requirement.InstallHint)
			}
			if err := installToolchainRequirement(unit.Root, requirement); err != nil {
				return fmt.Errorf("install %s dependency for %s: %w\nInstall manually: %s",
					requirement.Label, languageDisplayName(language), err, requirement.InstallHint)
			}
			if !toolchainRequirementAvailable(unit.Root, requirement) {
				return fmt.Errorf("%s installation completed but its command is still unavailable for %s\nCheck PATH or install manually: %s",
					requirement.Label, languageDisplayName(language), requirement.InstallHint)
			}
		}
	}
	return nil
}

func toolchainRequirementAvailable(unitRoot string, requirement astramap.ToolchainRequirement) bool {
	if requirement.ProjectExecutable != "" {
		path := filepath.Join(unitRoot, requirement.ProjectExecutable)
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return true
		}
	}
	for _, command := range requirement.Commands {
		if _, err := exec.LookPath(command); err == nil {
			return true
		}
	}
	return false
}

func installToolchainRequirement(unitRoot string, requirement astramap.ToolchainRequirement) error {
	installer := requirement.Installer
	if installer == nil {
		return fmt.Errorf("no automatic installer is registered")
	}
	commandPath, err := exec.LookPath(installer.Command)
	if err != nil {
		return fmt.Errorf("installer %s is unavailable", installer.Command)
	}
	fmt.Printf("Installing missing dependency: %s...\n", requirement.Label)
	cmd := exec.Command(commandPath, installer.Args...)
	cmd.Dir = unitRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed: %w", strings.Join(append([]string{installer.Command}, installer.Args...), " "), err)
	}
	fmt.Printf("Installed dependency: %s\n", requirement.Label)
	return nil
}

func unitHasAnyManifest(unit astramap.ProjectUnit, names []string) bool {
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[strings.ToLower(name)] = true
	}
	for _, manifest := range unit.Manifests {
		if wanted[strings.ToLower(filepath.Base(manifest))] {
			return true
		}
	}
	return false
}

func clangUnitReady(unit astramap.ProjectUnit) bool {
	compdbPath := filepath.Join(unit.Root, "compile_commands.json")
	if requireCompileCommands(compdbPath) == nil {
		return true
	}
	// compdb absent or invalid — check whether it can be generated
	if _, err := exec.LookPath("bear"); err != nil {
		return false
	}
	if _, err := exec.LookPath("make"); err != nil {
		return false
	}
	for _, name := range []string{"Makefile", "makefile"} {
		if _, err := os.Stat(filepath.Join(unit.Root, name)); err == nil {
			return true
		}
	}
	return false
}

func unitHasExactManifest(names ...string) scipReadiness {
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[strings.ToLower(name)] = true
	}
	return func(unit astramap.ProjectUnit) bool {
		for _, manifest := range unit.Manifests {
			if wanted[strings.ToLower(filepath.Base(manifest))] {
				return true
			}
		}
		return false
	}
}

func unitHasManifestSuffix(suffixes ...string) scipReadiness {
	return func(unit astramap.ProjectUnit) bool {
		for _, manifest := range unit.Manifests {
			name := strings.ToLower(filepath.Base(manifest))
			for _, suffix := range suffixes {
				if strings.HasSuffix(name, strings.ToLower(suffix)) {
					return true
				}
			}
		}
		return false
	}
}

func runScipGeneration(toolPath string, provider astramap.SemanticProviderSpec, unitRoot, repositoryRoot, unitID string, filter *astramap.IndexFilter) (string, error) {
	artifactDir := filepath.Join(repositoryRoot, ".astramap", "scip", provider.ID, unitID)
	if err := os.MkdirAll(artifactDir, 0755); err != nil {
		return "", err
	}
	scipPath := filepath.Join(artifactDir, "index.scip")
	pendingPath := scipPath + ".pending"
	removeOwnedFile(pendingPath)

	recipe := scipRecipes[provider.Recipe]
	if recipe == nil {
		return "", fmt.Errorf("Semantic Provider %s has no SCIP recipe configured", provider.ID)
	}
	prepared, err := recipe(toolPath, provider, unitRoot, pendingPath, filter)
	if err != nil {
		prepared.cleanup.runReverse()
		return "", err
	}
	defer prepared.cleanup.runReverse()
	cmd := prepared.command
	cmd.Dir = unitRoot
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		removeOwnedFile(pendingPath)
		return "", fmt.Errorf("SCIP generation failed (%s): %w\n%s", provider.ID, err, strings.TrimSpace(output.String()))
	}
	if prepared.artifact != pendingPath {
		if err := os.Rename(prepared.artifact, pendingPath); err != nil {
			return "", fmt.Errorf("SCIP artifact staging failed (%s): %w", provider.ID, err)
		}
	}
	if err := astramap.ValidateScipIndexFile(pendingPath); err != nil {
		removeOwnedFile(pendingPath)
		return "", fmt.Errorf("SCIP output validation failed (%s): %w", provider.ID, err)
	}
	if err := os.Rename(pendingPath, scipPath); err != nil {
		return "", fmt.Errorf("SCIP artifact commit failed (%s): %w", provider.ID, err)
	}
	return scipPath, nil
}

func commandRecipe(arguments ...string) scipRecipe {
	return func(toolPath string, _ astramap.SemanticProviderSpec, _, output string, _ *astramap.IndexFilter) (preparedScipRun, error) {
		args := append([]string(nil), arguments...)
		args = append(args, output)
		return preparedScipRun{command: exec.Command(toolPath, args...), artifact: output}, nil
	}
}

func defaultArtifactRecipe(arguments ...string) scipRecipe {
	return func(toolPath string, _ astramap.SemanticProviderSpec, projectRoot, _ string, _ *astramap.IndexFilter) (preparedScipRun, error) {
		artifact := filepath.Join(projectRoot, "index.scip")
		prepared := preparedScipRun{command: exec.Command(toolPath, arguments...), artifact: artifact}
		if _, err := os.Stat(artifact); err == nil {
			backup := filepath.Join(projectRoot, ".index.scip.astramap-backup")
			removeOwnedFile(backup)
			if err := os.Rename(artifact, backup); err != nil {
				return preparedScipRun{}, err
			}
			prepared.cleanup.add(func() error {
				removeOwnedFile(artifact)
				return os.Rename(backup, artifact)
			})
		} else {
			prepared.cleanup.add(func() error { return os.Remove(artifact) })
		}
		return prepared, nil
	}
}

func prepareNodeScip(toolPath string, _ astramap.SemanticProviderSpec, projectRoot, output string, filter *astramap.IndexFilter) (preparedScipRun, error) {
	prepared := preparedScipRun{artifact: output}
	backupPath, createdConfig, err := ensureTsConfig(projectRoot, filter)
	if err != nil {
		return prepared, err
	}
	if backupPath != "" {
		prepared.cleanup.add(func() error {
			_ = os.Rename(backupPath, filepath.Join(projectRoot, "tsconfig.json"))
			return nil
		})
	} else if createdConfig {
		prepared.cleanup.add(func() error { return os.Remove(filepath.Join(projectRoot, "tsconfig.json")) })
	}
	prepared.command = exec.Command(toolPath, "index", "--cwd", projectRoot, "--output", output)
	return prepared, nil
}

func preparePythonScip(toolPath string, _ astramap.SemanticProviderSpec, projectRoot, output string, _ *astramap.IndexFilter) (preparedScipRun, error) {
	return preparedScipRun{
		command: exec.Command(toolPath, "index", "--cwd", projectRoot, "--output", output), artifact: output,
	}, nil
}

func prepareClangScip(toolPath string, _ astramap.SemanticProviderSpec, projectRoot, output string, filter *astramap.IndexFilter) (preparedScipRun, error) {
	prepared := preparedScipRun{artifact: output}
	compdbPath := filepath.Join(projectRoot, "compile_commands.json")
	_, createErr := ensureCompileCommands(projectRoot, compdbPath)
	if createErr != nil {
		return prepared, createErr
	}
	// Do NOT add compdb to cleanup: compile_commands.json is a project build
	// artifact that should persist for reuse in subsequent index runs.
	filteredPath, err := prepareCompileCommandsJson(compdbPath, projectRoot, filter)
	if err != nil {
		prepared.cleanup.runReverse()
		return preparedScipRun{}, fmt.Errorf("prepare compile_commands.json: %w", err)
	}
	if filteredPath != compdbPath {
		prepared.cleanup.add(func() error { return os.Remove(filteredPath) })
	}
	if ok, count, reason := validateCompileCommandsJson(filteredPath); !ok {
		prepared.cleanup.runReverse()
		return preparedScipRun{}, fmt.Errorf("compile_commands.json invalid: %s (entries=%d)", reason, count)
	}
	prepared.command = exec.Command(toolPath, "--compdb-path", filteredPath, "--index-output-path", output, "--no-progress-report")
	return prepared, nil
}

func preparePackageScip(toolPath string, provider astramap.SemanticProviderSpec, projectRoot, output string, _ *astramap.IndexFilter) (preparedScipRun, error) {
	expand := func(value string) string {
		value = strings.ReplaceAll(value, "{projectRoot}", projectRoot)
		return strings.ReplaceAll(value, "{output}", output)
	}
	args := make([]string, len(provider.Args))
	for i, arg := range provider.Args {
		args[i] = expand(arg)
	}
	artifact := output
	if provider.Artifact != "" {
		artifact = expand(provider.Artifact)
		if !filepath.IsAbs(artifact) {
			artifact = filepath.Join(projectRoot, artifact)
		}
	}
	command := exec.Command(toolPath, args...)
	command.Dir = projectRoot
	prepared := preparedScipRun{command: command, artifact: artifact}
	if artifact != output {
		if _, err := os.Stat(artifact); err == nil {
			backup := artifact + ".astramap-backup"
			removeOwnedFile(backup)
			if err := os.Rename(artifact, backup); err != nil {
				return preparedScipRun{}, err
			}
			prepared.cleanup.add(func() error {
				removeOwnedFile(artifact)
				return os.Rename(backup, artifact)
			})
		} else if !os.IsNotExist(err) {
			return preparedScipRun{}, err
		} else {
			prepared.cleanup.add(func() error { return os.Remove(artifact) })
		}
	}
	return prepared, nil
}

func removeOwnedFile(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		logWarn("Failed to clean up AstraMap temp file %s: %v", path, err)
	}
}

func cleanupOwnedFiles(paths []string) {
	for i := len(paths) - 1; i >= 0; i-- {
		removeOwnedFile(paths[i])
	}
}

func requireCompileCommands(compdbPath string) error {
	if _, err := os.Stat(compdbPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("compile_commands.json not found; generate it with the project build system")
		}
		return fmt.Errorf("inspect compile_commands.json: %w", err)
	}
	if valid, count, reason := validateCompileCommandsJson(compdbPath); !valid {
		return fmt.Errorf("compile_commands.json invalid: %s (entries=%d)", reason, count)
	}
	return nil
}

// ensureCompileCommands ensures a valid compile_commands.json exists.
// If one already exists and is valid, it is reused (created=false).
// Otherwise, it attempts to generate one via "bear -- make" with
// backup/restore safety: existing file is backed up before regeneration
// and restored on failure.
func ensureCompileCommands(projectRoot, compdbPath string) (bool, error) {
	if requireCompileCommands(compdbPath) == nil {
		return false, nil
	}

	if _, err := exec.LookPath("bear"); err != nil {
		return false, fmt.Errorf("C/C++ SCIP requires a valid compile_commands.json; bear not found, cannot auto-generate\nInstall: Ubuntu/Debian: sudo apt install bear | macOS: brew install bear")
	}
	if _, err := exec.LookPath("make"); err != nil {
		return false, fmt.Errorf("C/C++ SCIP requires compile_commands.json; make not found, cannot execute bear -- make\nInstall: Ubuntu/Debian: sudo apt install make | macOS: xcode-select --install")
	}

	hasMakefile := false
	for _, name := range []string{"Makefile", "makefile"} {
		if _, err := os.Stat(filepath.Join(projectRoot, name)); err == nil {
			hasMakefile = true
			break
		}
	}
	if !hasMakefile {
		return false, fmt.Errorf("C/C++ SCIP requires compile_commands.json; no Makefile found in %s, cannot execute bear -- make", projectRoot)
	}

	// Backup existing invalid compdb before regeneration.
	hadExisting := false
	var backupPath string
	if _, err := os.Stat(compdbPath); err == nil {
		hadExisting = true
		backupPath = compdbPath + ".astramap-backup"
		removeOwnedFile(backupPath)
		if err := os.Rename(compdbPath, backupPath); err != nil {
			return false, fmt.Errorf("backup invalid compile_commands.json: %w", err)
		}
	}

	fmt.Println("Regenerating compile_commands.json via bear -- make...")
	cmd := exec.Command("bear", "--", "make")
	cmd.Dir = projectRoot
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	regenErr := cmd.Run()

	if regenErr != nil {
		restoreCompileCommands(compdbPath, backupPath)
		return false, fmt.Errorf("bear -- make failed: %w\n%s", regenErr, strings.TrimSpace(output.String()))
	}

	// Check if incremental build produced empty compile_commands.json.
	compdbEmpty := false
	if info, statErr := os.Stat(compdbPath); statErr != nil || info.Size() < 4 {
		compdbEmpty = true
	} else if valid, count, _ := validateCompileCommandsJson(compdbPath); !valid || count == 0 {
		compdbEmpty = true
	}

	if compdbEmpty {
		// Incremental build produced no compilation commands.
		// Force a clean rebuild to capture all compiler invocations.
		fmt.Println("Incremental build produced empty compile_commands.json, forcing clean rebuild...")
		removeOwnedFile(compdbPath)
		cleanCmd := exec.Command("make", "clean")
		cleanCmd.Dir = projectRoot
		var cleanOut bytes.Buffer
		cleanCmd.Stdout = &cleanOut
		cleanCmd.Stderr = &cleanOut
		if cleanErr := cleanCmd.Run(); cleanErr != nil {
			logWarn("make clean failed (non-fatal): %v", cleanErr)
		}
		output.Reset()
		cmd2 := exec.Command("bear", "--", "make")
		cmd2.Dir = projectRoot
		cmd2.Stdout = &output
		cmd2.Stderr = &output
		if regenErr = cmd2.Run(); regenErr != nil {
			restoreCompileCommands(compdbPath, backupPath)
			return false, fmt.Errorf("bear -- make (clean rebuild) failed: %w\n%s", regenErr, strings.TrimSpace(output.String()))
		}
	}

	if _, err := os.Stat(compdbPath); err != nil {
		restoreCompileCommands(compdbPath, backupPath)
		return false, fmt.Errorf("bear -- make produced no compile_commands.json\n%s", strings.TrimSpace(output.String()))
	}
	if valid, count, reason := validateCompileCommandsJson(compdbPath); !valid {
		removeOwnedFile(compdbPath)
		restoreCompileCommands(compdbPath, backupPath)
		return false, fmt.Errorf("generated compile_commands.json invalid: %s (entries=%d)", reason, count)
	}

	// New compdb valid; discard backup.
	if backupPath != "" {
		removeOwnedFile(backupPath)
	}
	fmt.Println("compile_commands.json generation complete")
	return !hadExisting, nil
}

func restoreCompileCommands(compdbPath, backupPath string) {
	if err := os.Remove(compdbPath); err != nil && !os.IsNotExist(err) {
		logWarn("Failed to remove invalid compile_commands.json: %v", err)
	}
	if backupPath == "" {
		return
	}
	if err := os.Rename(backupPath, compdbPath); err != nil {
		logWarn("Failed to restore compile_commands.json from backup: %v", err)
	}
}

// ensureTsConfig guarantees a tsconfig.json with allowJs so scip-typescript
// indexes both .ts and .js sources. Three cases:
//  1. No tsconfig exists → generate one (caller removes it).
//  2. tsconfig exists but lacks allowJs → back it up, generate a complete
//     one (caller restores the backup).
//  3. tsconfig exists with allowJs → no-op.
func ensureTsConfig(projectRoot string, filter *astramap.IndexFilter) (backupPath string, created bool, err error) {
	tsconfigPath := filepath.Join(projectRoot, "tsconfig.json")
	needsAllowJs := projectHasJavaScriptFiles(projectRoot, filter)

	if _, statErr := os.Stat(tsconfigPath); statErr != nil {
		if !needsAllowJs {
			return "", false, nil
		}
		fmt.Println("tsconfig.json not found, generating minimal configuration for JS/TS project...")
		if writeErr := writeAstraMapTsConfig(tsconfigPath, filter); writeErr != nil {
			return "", false, writeErr
		}
		fmt.Println("tsconfig.json generation complete")
		return "", true, nil
	}

	if !needsAllowJs {
		return "", false, nil
	}

	// Existing tsconfig may lack allowJs; inspect and replace if needed.
	data, readErr := os.ReadFile(tsconfigPath)
	if readErr != nil {
		return "", false, nil
	}
	var cfg map[string]interface{}
	if unmarshalErr := json.Unmarshal(data, &cfg); unmarshalErr != nil {
		return "", false, nil
	}
	if hasAllowJs(cfg) {
		return "", false, nil
	}

	// Back up the existing tsconfig and write a complete one.
	backupPath = tsconfigPath + ".astramap-backup"
	if removeErr := os.Remove(backupPath); removeErr != nil && !os.IsNotExist(removeErr) {
		return "", false, fmt.Errorf("remove old tsconfig backup: %w", removeErr)
	}
	if renameErr := os.Rename(tsconfigPath, backupPath); renameErr != nil {
		return "", false, fmt.Errorf("back up tsconfig.json: %w", renameErr)
	}
	fmt.Println("Existing tsconfig.json lacks allowJs; generating AstraMap configuration for JS/TS project...")
	if writeErr := writeAstraMapTsConfig(tsconfigPath, filter); writeErr != nil {
		_ = os.Rename(backupPath, tsconfigPath)
		return "", false, writeErr
	}
	fmt.Println("tsconfig.json generation complete (original backed up)")
	return backupPath, true, nil
}

func projectHasJavaScriptFiles(projectRoot string, filter *astramap.IndexFilter) bool {
	profile := astramap.BuildProjectProfile(projectRoot, filter, astramap.StageScip)
	for _, ext := range []string{".js", ".jsx", ".mjs", ".cjs"} {
		if profile.ExtensionCounts[ext] > 0 {
			return true
		}
	}
	return false
}

func hasAllowJs(cfg map[string]interface{}) bool {
	compilerOpts, ok := cfg["compilerOptions"].(map[string]interface{})
	if !ok {
		return false
	}
	allowJs, ok := compilerOpts["allowJs"].(bool)
	return ok && allowJs
}

func writeAstraMapTsConfig(tsconfigPath string, filter *astramap.IndexFilter) error {
	exclude := []string{"node_modules", "dist", ".astramap", "build"}
	if filter != nil {
		exclude = append(exclude, filter.Exclude...)
	}
	tsconfig := map[string]interface{}{
		"compilerOptions": map[string]interface{}{
			"target":           "ES2020",
			"module":           "ESNext",
			"moduleResolution": "node",
			"allowJs":          true,
			"checkJs":          false,
			"noEmit":           true,
			"skipLibCheck":     true,
			"esModuleInterop":  true,
		},
		"include": []string{"**/*.js", "**/*.jsx", "**/*.mjs", "**/*.cjs", "**/*.ts", "**/*.tsx"},
		"exclude": exclude,
	}
	data, err := json.MarshalIndent(tsconfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to generate tsconfig.json: %w", err)
	}
	if err := os.WriteFile(tsconfigPath, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("failed to write tsconfig.json: %w", err)
	}
	return nil
}

func validateCompileCommandsJson(compdbPath string) (bool, int, string) {
	data, err := os.ReadFile(compdbPath)
	if err != nil {
		return false, 0, "Unable to read file"
	}
	if len(data) < 4 {
		return false, 0, "File is empty or format invalid"
	}

	var entries []map[string]interface{}
	if err := json.Unmarshal(data, &entries); err != nil {
		return false, 0, "JSON parse failed: " + err.Error()
	}
	if len(entries) == 0 {
		return false, 0, "No compilation units"
	}

	for _, entry := range entries {
		dir, hasDir := entry["directory"].(string)
		if !hasDir {
			return false, len(entries), "Missing directory field"
		}
		filePath, ok := entry["file"].(string)
		if !ok {
			return false, len(entries), "Missing file field"
		}
		if _, hasCmd := entry["command"]; !hasCmd {
			if _, hasArgs := entry["arguments"]; !hasArgs {
				return false, len(entries), "Missing command or arguments field"
			}
		}
		resolvedPath := filePath
		if !filepath.IsAbs(filePath) {
			resolvedPath = filepath.Join(dir, filePath)
		}
		if _, err := os.Stat(resolvedPath); os.IsNotExist(err) {
			return false, len(entries), fmt.Sprintf("Source file does not exist: %s", resolvedPath)
		}
	}
	return true, len(entries), ""
}

func prepareCompileCommandsJson(compdbPath, projectRoot string, filter *astramap.IndexFilter) (string, error) {
	data, err := os.ReadFile(compdbPath)
	if err != nil {
		return "", err
	}

	var entries []map[string]interface{}
	if err := json.Unmarshal(data, &entries); err != nil {
		return "", err
	}

	modified := false
	filteredEntries := make([]map[string]interface{}, 0, len(entries))
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
		filePath, ok := entries[i]["file"].(string)
		if ok && filter != nil {
			relPath, err := filepath.Rel(projectRoot, filePath)
			if err == nil && !filter.Allows(relPath, astramap.StageScip) {
				modified = true
				continue
			}
		}
		filteredEntries = append(filteredEntries, entries[i])
	}
	if !modified {
		return compdbPath, nil
	}
	newData, err := json.MarshalIndent(filteredEntries, "", "  ")
	if err != nil {
		return "", err
	}
	filteredPath := filepath.Join(projectRoot, ".astramap", "compile_commands.filtered.json")
	if err := os.MkdirAll(filepath.Dir(filteredPath), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filteredPath, newData, 0644); err != nil {
		return "", err
	}
	return filteredPath, nil
}

// autoGenerateScip runs each semantic provider once per detected project unit.
func autoGenerateScip(projectRoot string, selectedLangs []LangCount, filter *astramap.IndexFilter, offerExisting bool) ([]string, []string, error) {
	if len(selectedLangs) == 0 {
		return nil, nil, fmt.Errorf("no supported project language selected")
	}
	var scipPaths []string
	var generatedPaths []string
	usedPaths := make(map[string]bool)
	existingDecisions := make(map[string]bool)
	languages := langIDs(selectedLangs)
	for _, unit := range astramap.DetectProjectUnits(projectRoot, languages, filter) {
		if len(unit.Languages) == 0 {
			continue
		}
		lang := unit.Languages[0]
		if offerExisting {
			if path, ok := matchingExistingScip(projectRoot, unit); ok {
				reuse, decided := existingDecisions[path]
				if !decided {
					reuse = confirmScipReuse(path, lang)
					existingDecisions[path] = reuse
				}
				if reuse {
					if !usedPaths[path] {
						fmt.Printf("Using existing SCIP: %s (%s)\n", languageDisplayName(lang), path)
						scipPaths = append(scipPaths, path)
						usedPaths[path] = true
					}
					continue
				}
			}
		}
		provider, providerOK := astramap.SemanticProviderForProjectByID(projectRoot, unit.ProviderID)
		if !providerOK || !astramap.CanAutoGenerateScipForProject(projectRoot, lang) {
			return nil, generatedPaths, fmt.Errorf("%s has no certified SCIP provider", languageDisplayName(lang))
		}
		if !scipUnitReady(provider.Recipe, unit) {
			return nil, generatedPaths, fmt.Errorf("%s SCIP prerequisites are missing in %s", languageDisplayName(lang), unit.Root)
		}
		if err := ensureUnitToolchains(projectRoot, unit); err != nil {
			return nil, generatedPaths, err
		}
		toolPath, found := findScipTool(lang, projectRoot, unit.Root)
		if !found {
			return nil, generatedPaths, fmt.Errorf("%s semantic provider is unavailable: %s not found\nInstall: %s",
				languageDisplayName(lang), provider.Tool, provider.InstallHint)
		}
		fmt.Printf("Generating SCIP: %s (%s)\n", languageDisplayName(lang), unit.Root)
		scipPath, err := runScipGeneration(toolPath, provider, unit.Root, projectRoot, unit.Identity, filter)
		if err != nil {
			return nil, generatedPaths, fmt.Errorf("%s SCIP generation failed: %w", languageDisplayName(lang), err)
		}
		scipPaths = append(scipPaths, scipPath)
		generatedPaths = append(generatedPaths, scipPath)
	}
	return scipPaths, generatedPaths, nil
}

func confirmScipReuse(path, language string) bool {
	fmt.Printf("Existing SCIP found: %s (%s). Reuse it instead of regenerating? [y/N]: ", languageDisplayName(language), path)
	var input string
	if _, err := fmt.Scanln(&input); err != nil {
		return false
	}
	input = strings.ToLower(strings.TrimSpace(input))
	return input == "y" || input == "yes"
}

func matchingExistingScip(projectRoot string, unit astramap.ProjectUnit) (string, bool) {
	path := filepath.Join(unit.Root, "index.scip")
	languages, err := astramap.ScipIndexProjectLanguages(path, projectRoot)
	if err != nil {
		return "", false
	}
	wanted := make(map[string]bool, len(unit.Languages))
	for _, language := range unit.Languages {
		wanted[language] = true
	}
	for _, language := range languages {
		if normalized, ok := astramap.NormalizeLanguageIDForProject(projectRoot, language); ok && wanted[normalized] {
			return path, true
		}
	}
	return "", false
}

func validateScipSelection(paths []string, selected []LangCount) error {
	wanted := make(map[string]bool, len(selected))
	for _, item := range selected {
		wanted[item.Lang] = true
	}
	covered := make(map[string]bool, len(wanted))
	for _, path := range paths {
		languages, err := astramap.ScipIndexProjectLanguages(path, projectRoot)
		if err != nil {
			return fmt.Errorf("inspect SCIP %s: %w", path, err)
		}
		for _, language := range languages {
			normalized, ok := astramap.NormalizeLanguageIDForProject(projectRoot, language)
			if !ok {
				return fmt.Errorf("SCIP %s contains unsupported language %q", path, language)
			}
			if !wanted[normalized] {
				return fmt.Errorf("SCIP %s contains unselected language %q", path, normalized)
			}
			covered[normalized] = true
		}
	}
	// Languages sharing a SCIP provider are co-indexed: if one is covered,
	// the provider ran successfully and siblings are implicitly covered even
	// when the tool omits their documents (e.g. scip-typescript may skip .js
	// files depending on tsconfig, but javascript shares the typescript provider).
	providerCovered := make(map[string]bool)
	for lang := range covered {
		if provider, ok := astramap.SemanticProviderForProjectLanguage(projectRoot, lang); ok {
			providerCovered[provider.ID] = true
		}
	}
	for lang := range wanted {
		if covered[lang] {
			continue
		}
		if provider, ok := astramap.SemanticProviderForProjectLanguage(projectRoot, lang); ok && providerCovered[provider.ID] {
			covered[lang] = true
		}
	}
	var missing []string
	for language := range wanted {
		if !covered[language] {
			missing = append(missing, language)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("SCIP output does not cover selected languages: %s", strings.Join(missing, ", "))
	}
	return nil
}

type indexOptions struct {
	scipFile      string
	scipOnly      bool
	treeSitter    bool
	refreshScip   bool
	full          bool
	watch         bool
	watchInterval time.Duration
	langFlag      string
}

func indexCmd() {
	args, watch, watchInterval := extractIndexWatchArgs(os.Args[2:])
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	scipFile := fs.String("scip", "", "SCIP index file; leave empty for auto-generation")
	scipOnly := fs.Bool("scip-only", false, "Import only SCIP, skip optional Syntax Overlay")
	treeSitter := fs.Bool("tree-sitter", false, "Syntax Overlay only, skip SCIP generation and import")
	refreshScip := fs.Bool("refresh-scip", false, "Force regenerate and import SCIP index")
	full := fs.Bool("full", false, "Full refresh high-precision SCIP layer, then execute optional Syntax Overlay")
	langFlag := fs.String("lang", "", "Language list, comma-separated")
	_ = fs.Parse(args)
	runIndex(indexOptions{
		scipFile:      *scipFile,
		scipOnly:      *scipOnly,
		treeSitter:    *treeSitter,
		refreshScip:   *refreshScip,
		full:          *full,
		watch:         watch,
		watchInterval: watchInterval,
		langFlag:      *langFlag,
	})
}

func extractIndexWatchArgs(args []string) ([]string, bool, time.Duration) {
	cleaned := make([]string, 0, len(args))
	watch := false
	interval := 10 * time.Second
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--watch" {
			watch = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				seconds, err := strconv.Atoi(args[i+1])
				if err != nil || seconds <= 0 {
					logError("--watch interval must be a positive integer in seconds, e.g.: amap index --watch 30")
					os.Exit(1)
				}
				interval = time.Duration(seconds) * time.Second
				i++
			}
			continue
		}
		cleaned = append(cleaned, arg)
	}
	return cleaned, watch, interval
}

func watchCmd() {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	_ = fs.Parse(os.Args[2:])

	interval := 10 * time.Second
	if fs.NArg() > 0 {
		seconds, err := strconv.Atoi(fs.Arg(0))
		if err != nil || seconds <= 0 {
			logError("watch interval must be a positive integer in seconds, e.g.: amap watch 10")
			os.Exit(1)
		}
		interval = time.Duration(seconds) * time.Second
	}
	if fs.NArg() > 1 {
		logError("watch accepts only one optional seconds argument, e.g.: amap watch 10")
		os.Exit(1)
	}

	runIndex(indexOptions{
		watch:         true,
		watchInterval: interval,
	})
}

func runIndex(opts indexOptions) {
	if configPath, created, err := astramap.EnsureIndexConfigExample(projectRoot); err != nil {
		logError("Failed to generate AstraMap config example: %v", err)
		os.Exit(1)
	} else if created {
		fmt.Printf("Generated index filter config example: %s\n", configPath)
		fmt.Println("To exclude auxiliary files or directories, edit this file and run amap index again.")
		fmt.Println()
	}

	filter, err := astramap.LoadIndexFilter(projectRoot)
	if err != nil {
		logError("Failed to read AstraMap config: %v", err)
		os.Exit(1)
	}

	db, err := getAstraMapDB(projectRoot)
	if err != nil {
		logError("Failed to connect to database: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	// Determine selected languages
	var selected []LangCount
	var detected []LangCount
	quiet := false
	plainIncremental := opts.langFlag == "" && opts.scipFile == "" && !opts.scipOnly && !opts.refreshScip && !opts.full && !opts.treeSitter
	if opts.langFlag == "" {
		if saved, ok := loadSavedIndexLanguages(projectRoot, db); ok {
			selected = saved
			quiet = plainIncremental
		}
	}
	if len(selected) == 0 {
		// Detect languages with file counts only when no saved language set exists.
		detected = detectProjectLanguages(projectRoot, filter)
		if len(detected) == 0 {
			fmt.Println("No known project language detected")
			os.Exit(1)
		}
	}
	if len(selected) == 0 && opts.langFlag != "" {
		// Non-interactive: --lang c,python
		selected = resolveLangNames(opts.langFlag, detected)
	}
	if len(selected) == 0 && len(detected) > 1 {
		fmt.Println("Detected the following language files:")
		for i, lc := range detected {
			fmt.Printf("  %d. %s (%d source files)\n", i+1, languageDisplayName(lc.Lang), lc.Count)
		}
		fmt.Println()
		fmt.Print("Please select languages to import (enter index numbers, separate multiple with commas, e.g., 1,3; press Enter to import all): ")
		var input string
		fmt.Scanln(&input)
		if input != "" {
			selected = parseLangSelection(input, detected)
		} else {
			selected = detected
		}
	}
	if len(selected) == 0 {
		selected = detected
	}

	if err := saveIndexLanguages(projectRoot, selected); err != nil {
		logError("Failed to save index language selection: %v", err)
		os.Exit(1)
	}
	astramap.SetQuietLogging(quiet)
	if !quiet {
		fmt.Printf("\nLanguages to be imported: ")
		var langNames []string
		for _, lc := range selected {
			langNames = append(langNames, languageDisplayName(lc.Lang))
		}
		fmt.Println(strings.Join(langNames, ", "))
	}

	existingScip, err := hasScipIndex(db)
	if err != nil {
		logError("Failed to read SCIP index status: %v", err)
		os.Exit(1)
	}

	// Generate or import SCIP indexes.
	var scipPaths []string
	var scipAutoPaths []string
	shouldRefreshScip := !opts.treeSitter && (opts.scipFile != "" || opts.refreshScip || opts.full || !existingScip)
	if shouldRefreshScip {
		if opts.scipFile != "" {
			scipPaths = []string{opts.scipFile}
		} else {
			offerExisting := !opts.refreshScip && !opts.full
			var generateErr error
			scipPaths, scipAutoPaths, generateErr = autoGenerateScip(projectRoot, selected, filter, offerExisting)
			if generateErr != nil {
				cleanupOwnedFiles(scipAutoPaths)
				scipPaths = nil
				if opts.refreshScip || opts.scipOnly {
					// Explicit semantic demand: an unavailable SCIP toolchain is fatal.
					logError("SCIP dependency check failed: %v", generateErr)
					os.Exit(1)
				}
				// Auto-detected SCIP is an optional enhancement; the syntax
				// overlay below must remain available without it.
				logWarn("SCIP semantic indexing unavailable, falling back to syntax-only: %v", generateErr)
			}
		}
	} else if !quiet {
		fmt.Println("Existing SCIP index detected, skipping full SCIP refresh. Use --refresh-scip to force refresh.")
	}

	if len(scipPaths) > 0 {
		if err := validateScipSelection(scipPaths, selected); err != nil {
			cleanupOwnedFiles(scipAutoPaths)
			logError("SCIP language validation failed: %v", err)
			os.Exit(1)
		}
		if !quiet {
			fmt.Printf("Batch importing %d SCIP indexes\n", len(scipPaths))
		}
		if err := astramap.ImportScipIndexesToAstraMap(db, scipPaths, projectRoot); err != nil {
			cleanupOwnedFiles(scipAutoPaths)
			logError("SCIP import failed: %v", err)
			os.Exit(1)
		}
		if !quiet {
			fmt.Println("SCIP batch index import complete")
		}
	}
	cleanupOwnedFiles(scipAutoPaths)

	noChange := true
	if !opts.scipOnly {
		var langFilter []string
		for _, lc := range selected {
			langFilter = append(langFilter, lc.Lang)
		}
		stopSpinner := startIndexSpinner(quiet, "AstraMap indexing incrementally")
		syncResult, err := astramap.SyncAllFilesAstraMapResult(db, projectRoot, langFilter...)
		if err != nil {
			stopSpinner("AstraMap incremental indexing failed")
			logError("Incremental scan failed: %v", err)
			os.Exit(1)
		}
		noChange = syncResult.Updated == 0 && !syncResult.Pruned && syncResult.PrunedDeleted == 0
		if noChange {
			stopSpinner("AstraMap no changes")
		} else {
			stopSpinner("AstraMap incremental indexing complete")
		}
	}

	// Show provenance breakdown: SCIP vs Syntax Overlay vs heuristic.
	// Unconditional on noChange: a fresh SCIP import or a heuristic refresh can
	// mutate the graph even when the syntax overlay reports zero file changes.
	if !quiet {
		nodeStats, edgeStats, _ := astramap.ProvenanceStats(db)
		fmt.Println()
		fmt.Println("── Index Provenance Stats ──")
		fmt.Printf("  Nodes (by language): %s\n", formatLangStats(nodeStats))
		fmt.Printf("  Edges (by source): %s\n", formatProvStats(edgeStats))
	}

	if opts.watch {
		if opts.watchInterval < time.Second {
			logWarn("watch interval too short, increased to 1s")
			opts.watchInterval = time.Second
		}
		var langFilter []string
		for _, lc := range selected {
			langFilter = append(langFilter, lc.Lang)
		}
		if err := watchIndexCmd(db, projectRoot, opts.watchInterval, !opts.scipOnly, langFilter...); err != nil {
			logError("watch failed: %v", err)
			os.Exit(1)
		}
	}
}

func watchIndexCmd(db *sqlx.DB, projectRoot string, interval time.Duration, applySyntax bool, langFilter ...string) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to initialize file watcher: %w", err)
	}
	defer watcher.Close()

	watchFilter, filterErr := astramap.LoadIndexFilter(projectRoot)
	if filterErr != nil {
		return fmt.Errorf("failed to load index filter: %w", filterErr)
	}
	if err := addIndexWatchDirs(watcher, projectRoot, watchFilter); err != nil {
		return err
	}

	fmt.Printf("watch started: %s, refreshing dirty syntax overlays at most once every %s\n", projectRoot, interval)

	dirty := make(map[string]bool)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	allowedLangs := make(map[string]bool, len(langFilter))
	for _, lang := range langFilter {
		allowedLangs[lang] = true
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if event.Has(fsnotify.Create) || event.Has(fsnotify.Rename) {
				if info, statErr := os.Stat(event.Name); statErr == nil && info.IsDir() {
					_ = addIndexWatchDirs(watcher, event.Name, watchFilter)
				}
			}
			if isIndexWatchEvent(event) {
				dirty[event.Name] = true
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			logWarn("watch event error: %v", err)
		case <-ticker.C:
			if len(dirty) == 0 {
				continue
			}
			paths := make([]string, 0, len(dirty))
			for path := range dirty {
				paths = append(paths, path)
			}
			dirty = make(map[string]bool)

			if !applySyntax {
				continue
			}
			profile := astramap.BuildProjectProfile(projectRoot, watchFilter, astramap.StageSyntax)
			changedPaths := make([]string, 0, len(paths))
			for _, path := range paths {
				relPath, relErr := filepath.Rel(projectRoot, path)
				if relErr != nil {
					relPath = path
				}
				selection, supported := astramap.ResolveLanguageWithProfile(profile, path)
				if !supported || selection.Module == nil {
					continue
				}
				if len(allowedLangs) > 0 && !allowedLangs[selection.ID] {
					continue
				}
				if !watchFilter.Allows(relPath, astramap.StageSyntax) {
					continue
				}
				changed, refreshErr := astramap.SyncDirtySyntaxOverlayWithProfile(db, profile, path)
				if refreshErr != nil {
					logWarn("watch overlay refresh failed %s: %v", relPath, refreshErr)
					for _, p := range paths {
						dirty[p] = true
					}
					changedPaths = nil
					break
				}
				if changed {
					changedPaths = append(changedPaths, relPath)
				}
			}
			if changedPaths == nil {
				continue
			}
			if len(changedPaths) > 0 {
				fmt.Printf("watch updated syntax overlay for %d file(s): %s (%s)\n", len(changedPaths), formatUpdatedFiles(changedPaths), time.Now().Format("15:04:05"))
				_ = astramap.ResolveGoInterfaces(db)
				_ = astramap.ResolveWebRoutesForFiles(db, projectRoot, changedPaths)
				if resolveErr := astramap.ResolveCrossFileCallsForFiles(db, projectRoot, changedPaths); resolveErr != nil {
					logWarn("watch heuristic refresh failed: %v", resolveErr)
				}
				if refreshErr := refreshWatchScip(db, projectRoot, watchFilter, profile, langFilter); refreshErr != nil {
					logWarn("watch SCIP convergence deferred; realtime syntax remains active: %v", refreshErr)
				}
			}
		}
	}
}

func refreshWatchScip(db *sqlx.DB, projectRoot string, filter *astramap.IndexFilter, profile astramap.ProjectProfile, languages []string) error {
	counts := astramap.ProjectLanguageCounts(profile)
	selected := make([]LangCount, 0, len(counts))
	allowed := make(map[string]bool, len(languages))
	for _, language := range languages {
		allowed[language] = true
	}
	for language, count := range counts {
		if count <= 0 || (len(allowed) > 0 && !allowed[language]) {
			continue
		}
		selected = append(selected, LangCount{Lang: language, Count: count})
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Lang < selected[j].Lang })
	if len(selected) == 0 {
		return nil
	}
	paths, owned, err := autoGenerateScip(projectRoot, selected, filter, false)
	defer cleanupOwnedFiles(owned)
	if err != nil {
		return err
	}
	if err := validateScipSelection(paths, selected); err != nil {
		return err
	}
	if err := astramap.ImportScipIndexesToAstraMap(db, paths, projectRoot); err != nil {
		return err
	}
	filters := make([]string, 0, len(selected))
	for _, language := range selected {
		filters = append(filters, language.Lang)
	}
	_, err = astramap.SyncAllFilesAstraMapResult(db, projectRoot, filters...)
	return err
}

func formatUpdatedFiles(files []string) string {
	const limit = 6
	if len(files) <= limit {
		return strings.Join(files, ", ")
	}
	return fmt.Sprintf("%s, ... +%d", strings.Join(files[:limit], ", "), len(files)-limit)
}

func startIndexSpinner(enabled bool, label string) func(string) {
	if !enabled {
		return func(string) {}
	}

	done := make(chan struct{})
	stopped := make(chan struct{})
	started := time.Now()
	go func() {
		defer close(stopped)
		frames := []byte{'-', '\\', '|', '/'}
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				fmt.Fprintf(os.Stderr, "\r%c %s... %.1fs", frames[i%len(frames)], label, time.Since(started).Seconds())
				i++
			}
		}
	}()

	return func(doneLabel string) {
		close(done)
		<-stopped
		fmt.Fprintf(os.Stderr, "\r%s (%.1fs)\n", doneLabel, time.Since(started).Seconds())
	}
}

func hasScipIndex(db *sqlx.DB) (bool, error) {
	var count int
	if err := db.Get(&count, `
		SELECT
			(SELECT COUNT(*) FROM astramap_edges WHERE provenance = 'scip') +
			(SELECT COUNT(*) FROM astramap_nodes WHERE provenance = 'scip' AND kind != 'external')
	`); err != nil {
		return false, err
	}
	return count > 0, nil
}

type savedIndexState struct {
	Languages []string `json:"languages"`
	UpdatedAt int64    `json:"updated_at"`
}

func loadSavedIndexLanguages(projectRoot string, db *sqlx.DB) ([]LangCount, bool) {
	if langs, ok := readIndexLanguagesFromConfig(projectRoot); ok {
		if selected := selectKnownLanguages(langs); len(selected) > 0 {
			return expandLegacyScriptSelection(projectRoot, selected), true
		}
	}
	if state, ok := readLegacyIndexState(projectRoot); ok {
		if selected := selectKnownLanguages(state.Languages); len(selected) > 0 {
			return expandLegacyScriptSelection(projectRoot, selected), true
		}
	}
	langs, err := inferIndexedLanguages(db)
	if err != nil || len(langs) == 0 {
		return nil, false
	}
	selected := selectKnownLanguages(langs)
	selected = expandLegacyScriptSelection(projectRoot, selected)
	return selected, len(selected) > 0
}

func expandLegacyScriptSelection(projectRoot string, selected []LangCount) []LangCount {
	hasTypeScript := false
	hasJavaScript := false
	for _, item := range selected {
		hasTypeScript = hasTypeScript || item.Lang == "typescript"
		hasJavaScript = hasJavaScript || item.Lang == "javascript"
	}
	if !hasTypeScript || hasJavaScript {
		return selected
	}
	profile := astramap.BuildProjectProfile(projectRoot, nil, astramap.StageDetect)
	count := 0
	for _, ext := range astramap.LanguageExtensions("javascript") {
		count += profile.ExtensionCounts[ext]
	}
	if count > 0 {
		selected = append(selected, LangCount{Lang: "javascript", Count: count})
	}
	return selected
}

func readLegacyIndexState(projectRoot string) (savedIndexState, bool) {
	var state savedIndexState
	data, err := os.ReadFile(indexStatePath(projectRoot))
	if err != nil {
		return state, false
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, false
	}
	return state, len(state.Languages) > 0
}

func saveIndexLanguages(projectRoot string, selected []LangCount) error {
	return writeIndexLanguagesToConfig(projectRoot, langIDs(selected))
}

func indexStatePath(projectRoot string) string {
	return filepath.Join(projectRoot, ".astramap", "index-state.json")
}

func indexConfigPath(projectRoot string) string {
	return filepath.Join(projectRoot, ".astramap", "config.yaml")
}

func readIndexLanguagesFromConfig(projectRoot string) ([]string, bool) {
	data, err := os.ReadFile(indexConfigPath(projectRoot))
	if err != nil {
		return nil, false
	}
	lines := strings.Split(string(data), "\n")
	inIndex := false
	current := ""
	var langs []string
	for _, raw := range lines {
		line := stripYAMLComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent == 0 {
			inIndex = trimmed == "index:"
			current = ""
			continue
		}
		if !inIndex {
			continue
		}
		if strings.HasPrefix(trimmed, "-") {
			if current == "languages" {
				langs = append(langs, parseYAMLScalar(strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))))
			}
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		current = strings.ToLower(strings.TrimSpace(key))
		if current != "languages" {
			continue
		}
		for _, item := range parseYAMLListValue(strings.TrimSpace(value)) {
			langs = append(langs, item)
		}
	}
	return langs, len(langs) > 0
}

func writeIndexLanguagesToConfig(projectRoot string, langs []string) error {
	path := indexConfigPath(projectRoot)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		data = []byte(astramap.IndexConfigExample())
	} else if err != nil {
		return err
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	block := indexLanguagesBlock(langs)
	indexStart, indexEnd := findTopLevelSection(lines, "index")
	if indexStart == -1 {
		lines = append(lines, "", "index:")
		lines = append(lines, block...)
	} else {
		langStart, langEnd := findNestedKeyBlock(lines, indexStart+1, indexEnd, "languages")
		if langStart == -1 {
			next := append([]string{}, lines[:indexStart+1]...)
			next = append(next, block...)
			next = append(next, lines[indexStart+1:]...)
			lines = next
		} else {
			next := append([]string{}, lines[:langStart]...)
			next = append(next, block...)
			next = append(next, lines[langEnd:]...)
			lines = next
		}
	}
	lines = ensureIndexFilterTemplate(lines)
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

func indexLanguagesBlock(langs []string) []string {
	block := []string{"  languages:"}
	for _, lang := range langs {
		block = append(block, "    - "+lang)
	}
	return block
}

func findTopLevelSection(lines []string, name string) (int, int) {
	start := -1
	for i, line := range lines {
		clean := stripYAMLComment(line)
		if strings.TrimSpace(clean) == "" {
			continue
		}
		trimmed := strings.TrimSpace(clean)
		indent := len(clean) - len(strings.TrimLeft(clean, " \t"))
		if indent == 0 && strings.TrimSuffix(trimmed, ":") == name {
			start = i
			break
		}
	}
	if start == -1 {
		return -1, len(lines)
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		clean := stripYAMLComment(lines[i])
		if strings.TrimSpace(clean) == "" {
			continue
		}
		indent := len(clean) - len(strings.TrimLeft(clean, " \t"))
		if indent == 0 {
			end = i
			break
		}
	}
	return start, end
}

func findNestedKeyBlock(lines []string, start, end int, key string) (int, int) {
	for i := start; i < end; i++ {
		clean := yamlStructuralLine(lines[i])
		if strings.TrimSpace(clean) == "" {
			continue
		}
		trimmed := strings.TrimSpace(clean)
		indent := len(clean) - len(strings.TrimLeft(clean, " \t"))
		k, _, ok := strings.Cut(trimmed, ":")
		if ok && indent > 0 && strings.EqualFold(strings.TrimSpace(k), key) {
			blockEnd := end
			for j := i + 1; j < end; j++ {
				next := yamlStructuralLine(lines[j])
				if strings.TrimSpace(next) == "" {
					continue
				}
				nextIndent := len(next) - len(strings.TrimLeft(next, " \t"))
				if nextIndent <= indent {
					blockEnd = j
					break
				}
			}
			return i, blockEnd
		}
	}
	return -1, end
}

func ensureIndexFilterTemplate(lines []string) []string {
	indexStart, indexEnd := findTopLevelSection(lines, "index")
	if indexStart == -1 {
		return lines
	}
	for i := indexStart + 1; i < indexEnd; i++ {
		clean := yamlStructuralLine(lines[i])
		if strings.TrimSpace(clean) == "" {
			continue
		}
		trimmed := strings.TrimSpace(clean)
		indent := len(clean) - len(strings.TrimLeft(clean, " \t"))
		key, _, ok := strings.Cut(trimmed, ":")
		if !ok || indent == 0 {
			continue
		}
		switch normalizeIndexTemplateKey(key) {
		case "include", "exclude", "forceinclude":
			return lines
		}
	}

	tail := []string{
		"  # include:",
		"  #   - \"src/**\"",
		"  # exclude:",
		"  #   - \"examples/legacy/**\"",
		"  # advanced:",
		"  #   forceInclude:",
		"  #     - \"src/.domain/**\"",
	}
	next := append([]string{}, lines[:indexEnd]...)
	next = append(next, tail...)
	next = append(next, lines[indexEnd:]...)
	return next
}

func normalizeIndexTemplateKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "-", "")
	key = strings.ReplaceAll(key, "_", "")
	return key
}

func yamlStructuralLine(line string) string {
	clean := stripYAMLComment(line)
	if strings.TrimSpace(clean) != "" {
		return clean
	}

	indent := len(line) - len(strings.TrimLeft(line, " \t"))
	trimmedLeft := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmedLeft, "#") {
		return clean
	}
	comment := strings.TrimPrefix(trimmedLeft, "#")
	if strings.HasPrefix(comment, " ") {
		comment = comment[1:]
	}
	return line[:indent] + comment
}

func stripYAMLComment(line string) string {
	inSingle := false
	inDouble := false
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inDouble {
			escaped = true
			continue
		}
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble {
				return line[:i]
			}
		}
	}
	return line
}

func parseYAMLListValue(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		value = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "["), "]"))
	}
	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		item := parseYAMLScalar(part)
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

func parseYAMLScalar(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	return value
}

func inferIndexedLanguages(db *sqlx.DB) ([]string, error) {
	var langs []string
	err := db.Select(&langs, "SELECT DISTINCT language FROM astramap_files WHERE language != '' ORDER BY language")
	return langs, err
}

func selectKnownLanguages(langs []string) []LangCount {
	selected := make([]LangCount, 0, len(langs))
	seen := make(map[string]bool, len(langs))
	for _, lang := range langs {
		lang = strings.TrimSpace(lang)
		if lang == "" {
			continue
		}
		normalized, ok := astramap.NormalizeLanguageIDForProject(projectRoot, lang)
		if !ok || seen[normalized] {
			continue
		}
		selected = append(selected, LangCount{Lang: normalized})
		seen[normalized] = true
	}
	return selected
}

func langIDs(selected []LangCount) []string {
	langs := make([]string, 0, len(selected))
	for _, lc := range selected {
		langs = append(langs, lc.Lang)
	}
	return langs
}

func addIndexWatchDirs(watcher *fsnotify.Watcher, root string, filter *astramap.IndexFilter) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			return nil
		}
		relPath, _ := filepath.Rel(root, path)
		if filter != nil && !filter.AllowsDir(relPath, astramap.StageDetect) {
			return filepath.SkipDir
		}
		_ = watcher.Add(path)
		return nil
	})
}

func isIndexWatchEvent(event fsnotify.Event) bool {
	if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) && !event.Has(fsnotify.Remove) && !event.Has(fsnotify.Rename) {
		return false
	}
	if event.Name == "" {
		return false
	}
	return astramap.IsPotentialSupportedPathForProject(projectRoot, event.Name)
}

func parseLangSelection(input string, detected []LangCount) []LangCount {
	parts := strings.Split(input, ",")
	var selected []LangCount
	for _, p := range parts {
		p = strings.TrimSpace(p)
		idx, err := strconv.Atoi(p)
		if err != nil || idx < 1 || idx > len(detected) {
			fmt.Printf("Ignoring invalid index: %s\n", p)
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
			fmt.Printf("Ignoring undetected language: %s\n", p)
		}
	}
	if len(selected) == 0 {
		return detected
	}
	return selected
}

func formatLangStats(stats map[string]int) string {
	var parts []string
	known := make(map[string]bool)
	for _, spec := range astramap.LanguageSpecs() {
		for _, key := range []string{spec.ID, spec.DisplayName} {
			known[key] = true
			if cnt, ok := stats[key]; ok {
				parts = append(parts, fmt.Sprintf("%s=%d", spec.DisplayName, cnt))
				break
			}
		}
	}
	for lang, cnt := range stats {
		if !known[lang] {
			parts = append(parts, fmt.Sprintf("%s=%d", lang, cnt))
		}
	}
	if len(parts) == 0 {
		return "(None)"
	}
	var total int
	for _, cnt := range stats {
		total += cnt
	}
	return strings.Join(parts, ", ") + fmt.Sprintf(" (Total=%d)", total)
}

func formatProvStats(stats map[string]int) string {
	// Display in fixed order: scip → syntax-package → heuristic → others.
	var parts []string
	for _, prov := range []string{"scip", "syntax-package", "heuristic"} {
		if cnt, ok := stats[prov]; ok {
			parts = append(parts, fmt.Sprintf("%s=%d", prov, cnt))
		}
	}
	for prov, cnt := range stats {
		if prov != "scip" && prov != "syntax-package" && prov != "heuristic" {
			parts = append(parts, fmt.Sprintf("%s=%d", prov, cnt))
		}
	}
	if len(parts) == 0 {
		return "(None)"
	}
	var total int
	for _, cnt := range stats {
		total += cnt
	}
	return strings.Join(parts, ", ") + fmt.Sprintf(" (Total=%d)", total)
}

func installCmd() {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	showConfig := fs.Bool("show-config", false, "仅输出各工具配置 JSON，不执行写入")
	_ = fs.Parse(os.Args[2:])

	// 1. 确定自身绝对路径
	selfPath, err := os.Executable()
	if err != nil {
		logError("Failed to locate current binary: %v", err)
		os.Exit(1)
	}
	selfPath, err = filepath.Abs(selfPath)
	if err != nil {
		logError("Failed to resolve absolute path: %v", err)
		os.Exit(1)
	}

	// 2. 确定项目绝对路径
	absProj := projectRoot

	// --show-config 模式：仅输出配置
	if *showConfig {
		printConfigs(selfPath, absProj)
		return
	}

	probes := probeInstallTargets()

	// Collect detected IDE names in stable order
	ideOrder := []string{"Claude Code", "VS Code", "Cursor", "Codex", "Windsurf", "Cline", "Antigravity"}
	var detectedIDEs []string
	for _, name := range ideOrder {
		if probes[name] {
			detectedIDEs = append(detectedIDEs, name)
		}
	}

	// Interactive IDE selection when multiple IDEs detected
	selectedIDEs := resolveIDESelection(detectedIDEs, ideOrder, probes)
	printInstallProbeReport(probes, selectedIDEs)

	fmt.Println("Registering AstraMap MCP service and rule files...")
	fmt.Println()

	success := 0
	total := 0

	// Install only user-selected IDEs
	selectedSet := make(map[string]bool, len(selectedIDEs))
	for _, name := range selectedIDEs {
		selectedSet[name] = true
	}

	// 3.1 Claude Code (MCP + /amap slash command)
	if selectedSet["Claude Code"] {
		total++
		if installClaudeCode(selfPath, absProj) {
			success++
		}
	} else {
		fmt.Println("  - Claude Code  — not selected, skipping")
	}

	// 3.2 VS Code (MCP + Copilot instructions)
	if selectedSet["VS Code"] {
		total++
		if installVSCode(selfPath, absProj) {
			success++
		}
	} else {
		fmt.Println("  - VS Code      — not selected, skipping")
	}

	// 3.3 Cursor (MCP + .cursor/rules)
	if selectedSet["Cursor"] {
		total++
		if installCursor(selfPath, absProj) {
			success++
		}
	} else {
		fmt.Println("  - Cursor       — not selected, skipping")
	}

	// 3.4 Project-level .mcp.json
	if selectedSet["Claude Code"] || selectedSet["VS Code"] || selectedSet["Cursor"] || selectedSet["Codex"] || selectedSet["Windsurf"] || selectedSet["Antigravity"] {
		total++
		if installProjectMcpJson(selfPath, absProj) {
			success++
		}
	} else {
		fmt.Println("  - Project .mcp.json — not selected, skipping")
	}

	// 3.5 Codex (MCP + AGENTS.md)
	if selectedSet["Codex"] {
		total++
		if installCodex(selfPath, absProj) {
			success++
		}
	} else {
		fmt.Println("  - Codex        — not selected, skipping")
	}

	// 3.6 Windsurf (.windsurfrules)
	if selectedSet["Windsurf"] {
		total++
		if installWindsurf(absProj) {
			success++
		}
	} else {
		fmt.Println("  - Windsurf     — not selected, skipping")
	}

	// 3.7 Cline (.clinerules)
	if selectedSet["Cline"] {
		total++
		if installCline(absProj) {
			success++
		}
	} else {
		fmt.Println("  - Cline        — not selected, skipping")
	}

	// 3.8 Antigravity (mcp_config.json + AGENTS.md)
	if selectedSet["Antigravity"] {
		total++
		if installAntigravity(selfPath, absProj) {
			success++
		}
	} else {
		fmt.Println("  - Antigravity  — not selected, skipping")
	}

	fmt.Println()
	if success == total {
		fmt.Printf("Installation complete! %d/%d tools successfully registered.\n", success, total)
	} else {
		fmt.Printf("Installation complete! %d/%d tools successfully registered.未成功的工具可手动配置，运行 amap install --show-config 查看配置。\n", success, total)
	}

	fmt.Println("\n── 注册核验 ──")
	printInstallVerification(absProj)

	// 4. 提示用户构建索引
	fmt.Println("\nNext Step: Build Code Map Index")
	fmt.Println("  amap index                    # Quick update; changed files require a current SCIP baseline")
	fmt.Println("  amap index --lang c           # Specify language")
	fmt.Println("  amap index --scip index.scip  # Import existing SCIP index file")
	fmt.Println("  amap index --scip-only        # Import SCIP only")
	fmt.Println("  amap index --refresh-scip     # Force regenerate and import SCIP")
	fmt.Println("  amap index --full             # Full refresh SCIP layer, then execute incremental scan")
	fmt.Println("  amap watch 10                 # Refresh incrementally at most once every 10 seconds")
}

func probeInstallTargets() map[string]bool {
	probes := map[string]bool{}
	for _, name := range []string{"claude", "code", "cursor", "codex", "windsurf", "cline", "gemini"} {
		_, err := exec.LookPath(name)
		probes[probeNameFromCommand(name)] = err == nil
	}
	home, err := os.UserHomeDir()
	if err == nil {
		probes["Antigravity"] = probes["Antigravity"] || dirExists(filepath.Join(home, ".gemini"))
	}
	return probes
}

func probeNameFromCommand(name string) string {
	switch name {
	case "claude":
		return "Claude Code"
	case "code":
		return "VS Code"
	case "cursor":
		return "Cursor"
	case "codex":
		return "Codex"
	case "windsurf":
		return "Windsurf"
	case "cline":
		return "Cline"
	case "gemini":
		return "Antigravity"
	default:
		return name
	}
}

func printInstallProbeReport(probes map[string]bool, selectedIDEs []string) {
	selectedSet := make(map[string]bool, len(selectedIDEs))
	for _, name := range selectedIDEs {
		selectedSet[name] = true
	}
	fmt.Println("Detected IDE Clients:")
	idx := 0
	for _, name := range []string{"Claude Code", "VS Code", "Cursor", "Codex", "Windsurf", "Cline", "Antigravity"} {
		if probes[name] {
			idx++
			if selectedSet[name] {
				fmt.Printf("  %d. ✓ %s  [selected]\n", idx, name)
			} else {
				fmt.Printf("  %d. ✓ %s\n", idx, name)
			}
			continue
		}
		fmt.Printf("     - %s\n", name)
	}
	fmt.Println("Workspace Shared Targets (installed when any IDE is selected):")
	fmt.Println("  - .mcp.json")
}

// resolveIDESelection prompts the user to select which detected IDEs to integrate.
// Returns the list of selected IDE names. If 0 or 1 IDE detected, returns all without prompting.
func resolveIDESelection(detectedIDEs []string, ideOrder []string, probes map[string]bool) []string {
	if len(detectedIDEs) <= 1 {
		return detectedIDEs
	}
	// Non-interactive: skip prompt when stdin is not a terminal
	if !isTerminal() {
		return detectedIDEs
	}
	fmt.Println("Detected IDE Clients:")
	for i, name := range detectedIDEs {
		fmt.Printf("  %d. %s\n", i+1, name)
	}
	fmt.Println()
	fmt.Print("Select IDEs to integrate (enter index numbers, e.g., 1,2; press Enter for all): ")
	var input string
	fmt.Scanln(&input)
	if input == "" {
		return detectedIDEs
	}
	return parseIDESelection(input, detectedIDEs)
}

// parseIDESelection parses comma-separated index numbers into IDE names.
func parseIDESelection(input string, detected []string) []string {
	parts := strings.Split(input, ",")
	var selected []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		idx, err := strconv.Atoi(p)
		if err != nil || idx < 1 || idx > len(detected) {
			fmt.Printf("Ignoring invalid index: %s\n", p)
			continue
		}
		selected = append(selected, detected[idx-1])
	}
	if len(selected) == 0 {
		return detected
	}
	return selected
}

// isTerminal returns true when stdin is connected to a terminal.
func isTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func printInstallVerification(projectPath string) {
	home, err := os.UserHomeDir()
	checks := []struct {
		name string
		ok   bool
	}{
		{"Claude Code slash command", fileContains(filepath.Join(projectPath, ".claude", "commands", "amap.md"), "allowed-tools: astramap_search")},
		{"Claude Code tool permissions", fileContains(filepath.Join(projectPath, ".claude", "settings.local.json"), "mcp__astramap__astramap_search")},
		{"VS Code Copilot rules", fileContains(filepath.Join(projectPath, ".github", "copilot-instructions.md"), "## AstraMap")},
		{"Cursor MCP", fileContains(filepath.Join(projectPath, ".cursor", "mcp.json"), "\"astramap\"")},
		{"Cursor rules", fileContains(filepath.Join(projectPath, ".cursor", "rules", "astramap.mdc"), "alwaysApply: true")},
		{"Project .mcp.json", fileContains(filepath.Join(projectPath, ".mcp.json"), "\"astramap\"")},
		{"Codex AGENTS", fileContains(filepath.Join(projectPath, "AGENTS.md"), "## AstraMap")},
		{"Windsurf rules", fileContains(filepath.Join(projectPath, ".windsurfrules"), "## AstraMap")},
		{"Cline rules", fileContains(filepath.Join(projectPath, ".clinerules", "astramap.md"), "AstraMap")},
		{"Antigravity project MCP", fileContains(filepath.Join(projectPath, ".agents", "mcp_config.json"), "\"astramap\"")},
	}
	if err == nil && home != "" {
		checks = append(checks, struct {
			name string
			ok   bool
		}{"Codex MCP", fileContains(filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.astramap]")})
		checks = append(checks, struct {
			name string
			ok   bool
		}{"Antigravity global MCP", fileContains(filepath.Join(home, ".gemini", "config", "mcp_config.json"), "\"astramap\"")})
		checks = append(checks, struct {
			name string
			ok   bool
		}{"Antigravity CLI MCP", fileContains(filepath.Join(home, ".gemini", "antigravity-cli", "mcp_config.json"), "\"astramap\"")})
	}

	for _, check := range checks {
		if check.ok {
			fmt.Printf("  ✓ %s\n", check.name)
		} else {
			fmt.Printf("  - %s\n", check.name)
		}
	}
}

func fileContains(path, needle string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if !strings.Contains(string(data), needle) {
		return false
	}
	return true
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
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
	fmt.Println("  Antigravity:  .agents/AGENTS.md 或 ~/.gemini/config/AGENTS.md (追加 AstraMap 段落)")

	fmt.Println("\n=== Codex MCP (TOML) ===")
	fmt.Printf("  CLI: codex mcp add astramap -- %s serve --project .\n", amapPath)
	fmt.Println("  或手动编辑 ~/.codex/config.toml:")
	fmt.Printf("    [mcp_servers.astramap]\n    command = \"%s\"\n    args = [\"serve\", \"--project\", \".\"]\n", amapPath)
	fmt.Println("    # 每个工具需设置 approval_mode = \"approve\"")

	fmt.Println("\n=== Antigravity (~/.gemini/config/mcp_config.json 或项目 .agents/mcp_config.json) ===")
	antigravityCfg := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"astramap": map[string]interface{}{
				"command": amapPath,
				"args":    []string{"serve", "--project", projectPath},
			},
		},
	}
	antigravityData, _ := json.MarshalIndent(antigravityCfg, "", "  ")
	fmt.Println(string(antigravityData))
}

// installAntigravity 注册到 Google Antigravity / Agy (mcp_config.json + AGENTS.md)
func installAntigravity(amapPath, projectPath string) bool {
	mcpMethods := []string{}

	// 1. 项目级 .agents/mcp_config.json
	projMcpPath := filepath.Join(projectPath, ".agents", "mcp_config.json")
	if err := writeMcpConfig(projMcpPath, "mcpServers", "astramap", map[string]interface{}{
		"command": amapPath,
		"args":    []string{"serve", "--project", projectPath},
	}); err != nil {
		logWarn("Project-level Antigravity MCP registration failed (%s): %v", projMcpPath, err)
	} else {
		mcpMethods = append(mcpMethods, ".agents/mcp_config.json")
	}

	// 2. 全局级 ~/.gemini/config/mcp_config.json 和 ~/.gemini/antigravity-cli/mcp_config.json
	home, err := os.UserHomeDir()
	if err == nil {
		globalMcpPath1 := filepath.Join(home, ".gemini", "config", "mcp_config.json")
		globalMcpPath2 := filepath.Join(home, ".gemini", "antigravity-cli", "mcp_config.json")

		// Write globalMcpPath1
		if err := writeMcpConfig(globalMcpPath1, "mcpServers", "astramap", map[string]interface{}{
			"command": amapPath,
			"args":    []string{"serve", "--project", projectPath},
		}); err != nil {
			logWarn("Global-level Antigravity MCP registration failed (%s): %v", globalMcpPath1, err)
		} else {
			mcpMethods = append(mcpMethods, "~/.gemini/config/mcp_config.json")
		}

		// Write globalMcpPath2
		if err := writeMcpConfig(globalMcpPath2, "mcpServers", "astramap", map[string]interface{}{
			"command": amapPath,
			"args":    []string{"serve", "--project", projectPath},
		}); err != nil {
			logWarn("Global-level Antigravity CLI MCP registration failed (%s): %v", globalMcpPath2, err)
		} else {
			mcpMethods = append(mcpMethods, "~/.gemini/antigravity-cli/mcp_config.json")
		}
	} else {
		logWarn("Cannot retrieve user home directory, skipping global Antigravity MCP registration")
	}

	// 3. 注册规则 (AGENTS.md)
	// 项目级规则：.agents/AGENTS.md
	rulesOK1 := appendRulesFile(filepath.Join(projectPath, ".agents", "AGENTS.md"), "## AstraMap", astramapRulesContent)

	// 全局级规则：~/.gemini/config/AGENTS.md
	rulesOK2 := false
	if home != "" {
		rulesOK2 = appendRulesFile(filepath.Join(home, ".gemini", "config", "AGENTS.md"), "## AstraMap", astramapRulesContent)
	}

	// 汇总输出
	if len(mcpMethods) > 0 {
		fmt.Printf("  ✓ Antigravity  — MCP registered (written to %s)\n", strings.Join(mcpMethods, ", "))
		if rulesOK1 || rulesOK2 {
			fmt.Printf("  ✓ Antigravity  — Rules appended to AGENTS.md\n")
		}
		return true
	}
	fmt.Println("  ✗ Antigravity  — MCP registration failed")
	return false
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
			logWarn("'claude mcp add' execution failed: %s, falling back to manual config", strings.TrimSpace(string(output)))
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
			fmt.Printf("  ✗ Claude Code  — MCP registration failed: %v\n", err)
			return false
		}
		mcpOK = true
		mcpMethod = configPath
	}

	// Register /amap slash command
	cmdOK := installSlashCommand(projectPath)

	// Grant tool permissions in .claude/settings.local.json
	permOK := installClaudeCodePermissions(projectPath)

	// Summary
	if mcpOK && cmdOK && permOK {
		fmt.Printf("  ✓ Claude Code  — MCP registered (%s) + /amap command + tool permissions\n", mcpMethod)
	} else if mcpOK && cmdOK {
		fmt.Printf("  ✓ Claude Code  — MCP registered (%s) + /amap command (permissions write failed)\n", mcpMethod)
	} else if mcpOK {
		fmt.Printf("  ✓ Claude Code  — MCP registered (%s), /amap command and permissions failed\n", mcpMethod)
	}
	return mcpOK
}

// installSlashCommand 创建 .claude/commands/amap.md 注册 /amap slash command
func installSlashCommand(projectPath string) bool {
	cmdsDir := filepath.Join(projectPath, ".claude", "commands")
	if err := os.MkdirAll(cmdsDir, 0755); err != nil {
		logWarn("Failed to create .claude/commands directory: %v", err)
		return false
	}

	amapCmdPath := filepath.Join(cmdsDir, "amap.md")
	if err := os.WriteFile(amapCmdPath, []byte(amapSlashCommandTpl), 0644); err != nil {
		logWarn("Failed to write %s: %v", amapCmdPath, err)
		return false
	}
	return true
}

// installClaudeCodePermissions writes mcp__astramap__* tool permissions
// into the project-level .claude/settings.local.json so Claude Code
// auto-approves AstraMap tool calls without per-call confirmation.
func installClaudeCodePermissions(projectPath string) bool {
	settingsDir := filepath.Join(projectPath, ".claude")
	settingsPath := filepath.Join(settingsDir, "settings.local.json")

	// AstraMap MCP tool permission entries
	toolPerms := []string{
		"mcp__astramap__astramap_search",
		"mcp__astramap__astramap_explore",
		"mcp__astramap__astramap_node",
		"mcp__astramap__astramap_callers",
		"mcp__astramap__astramap_callees",
		"mcp__astramap__astramap_impact",
		"mcp__astramap__astramap_status",
		"mcp__astramap__astramap_trace",
		"mcp__astramap__astramap_files",
	}

	// Read existing settings
	var cfg map[string]interface{}
	data, err := os.ReadFile(settingsPath)
	if err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &cfg); err != nil {
			backupPath := settingsPath + ".bak"
			_ = os.WriteFile(backupPath, data, 0644)
			logWarn("Existing settings.local.json corrupted, backed up to %s and will rebuild", backupPath)
			cfg = make(map[string]interface{})
		}
	}
	if cfg == nil {
		cfg = make(map[string]interface{})
	}

	// Ensure permissions.allow array exists
	permMap, _ := cfg["permissions"].(map[string]interface{})
	if permMap == nil {
		permMap = make(map[string]interface{})
	}
	allowRaw, exists := permMap["allow"]
	var allowList []interface{}
	if exists {
		if arr, ok := allowRaw.([]interface{}); ok {
			allowList = arr
		}
	}

	// Build a set of existing entries for dedup
	existing := make(map[string]bool, len(allowList))
	for _, v := range allowList {
		if s, ok := v.(string); ok {
			existing[s] = true
		}
	}

	// Append missing tool permissions
	added := 0
	for _, perm := range toolPerms {
		if !existing[perm] {
			allowList = append(allowList, perm)
			added++
		}
	}

	if added == 0 && existing["mcp__astramap__astramap_search"] {
		// All permissions already present
		return true
	}

	permMap["allow"] = allowList
	cfg["permissions"] = permMap

	// Ensure enabledMcpjsonServers includes "astramap"
	serversRaw, _ := cfg["enabledMcpjsonServers"]
	var servers []interface{}
	if arr, ok := serversRaw.([]interface{}); ok {
		servers = arr
	}
	hasAstramap := false
	for _, v := range servers {
		if s, ok := v.(string); ok && s == "astramap" {
			hasAstramap = true
			break
		}
	}
	if !hasAstramap {
		servers = append(servers, "astramap")
		cfg["enabledMcpjsonServers"] = servers
	}

	// Write
	if err := os.MkdirAll(settingsDir, 0755); err != nil {
		logWarn("Failed to create .claude directory: %v", err)
		return false
	}
	newData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		logWarn("Failed to encode settings.local.json: %v", err)
		return false
	}
	if err := os.WriteFile(settingsPath, newData, 0644); err != nil {
		logWarn("Failed to write %s: %v", settingsPath, err)
		return false
	}
	return true
}

const amapSlashCommandTpl = `---
description: AstraMap code map query
argument-hint: <子命令> <参数>
allowed-tools: astramap_search astramap_explore astramap_node astramap_callers astramap_callees astramap_impact astramap_status astramap_trace astramap_files
---

根据用户输入执行 AstraMap code map query。

Subcommand mappings:
- ` + "`" + `search <关键词>` + "`" + ` → 调用 astramap_search fuzzy search symbols
- ` + "`" + `explore <描述>` + "`" + ` → 调用 astramap_explore explore code context
- ` + "`" + `node <符号名>` + "`" + ` → 调用 astramap_node view symbol details
- ` + "`" + `callers <符号>` + "`" + ` → 调用 astramap_callers trace call sources
- ` + "`" + `callees <符号>` + "`" + ` → 调用 astramap_callees trace callee dependencies
- ` + "`" + `impact <符号>` + "`" + ` → 调用 astramap_impact analyze change impact
- ` + "`" + `trace <from> <to>` + "`" + ` → 调用 astramap_trace trace call paths
- ` + "`" + `status` + "`" + ` → 调用 astramap_status view index status
- ` + "`" + `files [路径]` + "`" + ` → 调用 astramap_files list indexed files

User Input: $ARGUMENTS
`

// astramapRulesContent 是所有工具规则文件共享的核心指令内容
const astramapRulesContent = `AstraMap 是当前项目的代码地图 MCP 服务。当用户询问代码结构相关问题时，必须优先使用 AstraMap 工具而非 grep 或文件搜索：

- 查找符号定义 → astramap_search
- 理解代码上下文和调用关系 → astramap_explore
- view symbol details和源码 → astramap_node
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
			logWarn("'code --add-mcp' execution failed: %s, falling back to manual config", strings.TrimSpace(string(output)))
		}
	}

	// Fallback: 写入 .vscode/mcp.json
	if !mcpOK {
		configPath := filepath.Join(projectPath, ".vscode", "mcp.json")
		if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
			fmt.Printf("  ✗ VS Code      — Failed to create .vscode directory: %v\n", err)
			return false
		}
		if err := writeMcpConfig(configPath, "servers", "astramap", map[string]interface{}{
			"command": amapPath,
			"args":    []string{"serve", "--project", "."},
		}); err != nil {
			fmt.Printf("  ✗ VS Code      — MCP registration failed: %v\n", err)
			return false
		}
		mcpOK = true
		mcpMethod = configPath
	}

	// 注册 Copilot instructions
	instOK := appendRulesFile(filepath.Join(projectPath, ".github", "copilot-instructions.md"), "## AstraMap", astramapRulesContent)

	if mcpOK && instOK {
		fmt.Printf("  ✓ VS Code      — MCP registered (%s) + Copilot rules written\n", mcpMethod)
	} else if mcpOK {
		fmt.Printf("  ✓ VS Code      — MCP registered (%s), Copilot rules write failed\n", mcpMethod)
	}
	return mcpOK
}

// installCursor 注册到 Cursor (MCP server + rules)
func installCursor(amapPath, projectPath string) bool {
	home, _ := os.UserHomeDir()

	// Write全局 ~/.cursor/mcp.json
	globalPath := filepath.Join(home, ".cursor", "mcp.json")
	if err := writeMcpConfig(globalPath, "mcpServers", "astramap", map[string]interface{}{
		"command": amapPath,
		"args":    []string{"serve", "--project", "${workspaceFolder}"},
	}); err != nil {
		fmt.Printf("  ✗ Cursor       — Failed to write %s: %v\n", globalPath, err)
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
		fmt.Printf("  ✓ Cursor       — MCP written + rules registered (.cursor/rules/astramap.mdc)\n")
	} else {
		fmt.Printf("  ✓ Cursor       — MCP written to %s\n", globalPath)
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
		fmt.Printf("  ✗ 项目 .mcp.json — Failed to write %s: %v\n", configPath, err)
		return false
	}
	fmt.Printf("  ✓ Project .mcp.json — Written to %s (automatically available to team members)\n", configPath)
	return true
}

// installCodex 注册到 OpenAI Codex (AGENTS.md)
func installCodex(amapPath, projectPath string) bool {
	ok1 := appendRulesFile(filepath.Join(projectPath, "AGENTS.md"), "## AstraMap", astramapRulesContent)
	ok2 := installCodexMcp(amapPath)
	switch {
	case ok1 && ok2:
		fmt.Println("  ✓ Codex         — MCP registered + rules written to AGENTS.md")
	case ok1:
		fmt.Println("  ✓ Codex         — Rules written to AGENTS.md (MCP registration failed, run manually: codex mcp add astramap -- <amap-path> serve --project .)")
	case ok2:
		fmt.Println("  ✓ Codex         — MCP registered (AGENTS.md write failed)")
	default:
		fmt.Println("  ✗ Codex         — MCP registration and AGENTS.md write both failed")
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
			logWarn("codex mcp add failed: %v, falling back to TOML editing", err)
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
		"astramap_status", "astramap_trace", "astramap_files",
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
		"astramap_status", "astramap_trace", "astramap_files",
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
		fmt.Println("  ✓ Windsurf      — Rules written to .windsurfrules")
		return true
	}
	fmt.Println("  ✗ Windsurf      — .windsurfrules write failed")
	return false
}

// installCline 注册到 Cline (.clinerules/astramap.md)
func installCline(projectPath string) bool {
	rulesDir := filepath.Join(projectPath, ".clinerules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		fmt.Printf("  ✗ Cline         — Failed to create .clinerules directory: %v\n", err)
		return false
	}
	rulesPath := filepath.Join(rulesDir, "astramap.md")
	if err := os.WriteFile(rulesPath, []byte(astramapRulesContent), 0644); err != nil {
		fmt.Printf("  ✗ Cline         — Failed to write %s: %v\n", rulesPath, err)
		return false
	}
	fmt.Println("  ✓ Cline         — Rules written to .clinerules/astramap.md")
	return true
}

// appendRulesFile appends section to rule file: skips if section title exists, otherwise appends
func appendRulesFile(filePath, sectionTitle, sectionContent string) bool {
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return false
	}

	var existing string
	data, err := os.ReadFile(filePath)
	if err == nil {
		existing = string(data)
	}

	// Skip if section title already exists
	if strings.Contains(existing, sectionTitle) {
		return true
	}

	var newContent string
	if existing == "" {
		newContent = sectionTitle + "\n\n" + sectionContent
	} else {
		// Ensure trailing newline
		if !strings.HasSuffix(existing, "\n") {
			existing += "\n"
		}
		newContent = existing + "\n" + sectionTitle + "\n\n" + sectionContent
	}

	return os.WriteFile(filePath, []byte(newContent), 0644) == nil
}

// writeMcpConfig safely writes MCP config: backup -> merge -> write -> verify
func writeMcpConfig(configPath, topKey, serverName string, serverCfg map[string]interface{}) error {
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Read existing config
	var cfg map[string]interface{}
	data, err := os.ReadFile(configPath)
	if err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &cfg); err != nil {
			// JSON corrupted, rebuild after backup
			backupPath := configPath + ".bak"
			_ = os.WriteFile(backupPath, data, 0644)
			logWarn("Existing config JSON corrupted, backed up to %s and will rebuild", backupPath)
			cfg = make(map[string]interface{})
		}
	}
	if cfg == nil {
		cfg = make(map[string]interface{})
	}

	// Get or create top-level key (mcpServers / servers)
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

	// Inject server config
	servers[serverName] = serverCfg
	cfg[topKey] = servers

	// Write
	newData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("JSON encoding failed: %w", err)
	}
	if err := os.WriteFile(configPath, newData, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	// Verify
	verifyData, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read for verification after writing: %w", err)
	}
	if !json.Valid(verifyData) {
		return fmt.Errorf("invalid JSON after write")
	}
	return nil
}

func diffCmd() {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	suggestTests := fs.Bool("suggest-tests", false, "Provide unit test execution suggestions")
	_ = fs.Parse(os.Args[2:])

	db, err := getAstraMapDB(projectRoot)
	if err != nil {
		logError("Failed to connect to database: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	// Run git diff
	cmd := exec.Command("git", "diff", "--name-only")
	out, err := cmd.Output()
	if err != nil {
		fmt.Println("No dirty files found, workspace clean!")
		return
	}

	files := strings.Split(string(out), "\n")
	var symbols []string
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		// Query all symbols in the file
		var symIDs []string
		_ = db.Select(&symIDs, "SELECT id FROM astramap_nodes WHERE file_path = ?", f)
		symbols = append(symbols, symIDs...)
	}

	if len(symbols) == 0 {
		fmt.Println("No affected code symbols detected.")
		return
	}

	fmt.Printf("Detected %d changed symbols, analyzing upstream impact...\n\n", len(symbols))
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
		fmt.Println("Suggest running unit tests for associated modules:")
		fmt.Println("  go test -v ./...")
	}
}

func printIndexFilterMatchReport(projectRoot string, filter *astramap.IndexFilter) {
	report, err := astramap.BuildIndexFilterMatchReport(projectRoot, filter)
	if err != nil {
		logWarn("索引过滤命中列表生成失败: %v", err)
		return
	}
	if len(report.Excluded) == 0 {
		return
	}

	fmt.Println("── 索引过滤命中 ──")
	byKind := make(map[astramap.ExcludeKind][]string)
	for _, entry := range report.Excluded {
		byKind[entry.Kind] = append(byKind[entry.Kind], entry.Path)
	}
	for kind, paths := range byKind {
		fmt.Printf("  %s: %d\n", kind, len(paths))
		const limit = 20
		for i, p := range paths {
			if i >= limit {
				fmt.Printf("    ... +%d more\n", len(paths)-limit)
				break
			}
			fmt.Printf("    - %s\n", p)
		}
	}
	fmt.Println()
}

func locateCmd() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: amap locate <symbol_name>")
		os.Exit(1)
	}
	symbol := os.Args[2]

	db, err := getAstraMapDB(projectRoot)
	if err != nil {
		logError("Failed to connect to database: %v", err)
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
		fmt.Printf("Unable to locate symbol \"%s\"\n", symbol)
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

	// 获取所有已索引File Path
	var files []string
	if err := db.Select(&files, "SELECT path FROM astramap_files"); err != nil {
		logError("查询文件列表失败: %v", err)
		os.Exit(1)
	}

	if len(files) == 0 {
		fmt.Println("索引中没有文件记录，请先执行 amap index。")
		return
	}

	// Check git availability
	useGit := true
	if err := exec.Command("git", "rev-parse", "--git-dir").Run(); err != nil {
		logWarn("Current directory is not a git repository or git is unavailable, file modification time will be used instead of commit counts.")
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
				// 用距今天数作为伪Commit Count（越新越活跃）
				commits = int(info.ModTime().Unix() / 86400)
			}
		}

		var funcCount int
		_ = db.Get(&funcCount, "SELECT COUNT(*) FROM astramap_nodes WHERE file_path = ? AND kind IN ('function', 'method')", fp)

		results = append(results, hotspot{FilePath: fp, Commits: commits, FuncCount: funcCount})
	}

	// 按Commit Count降序排列
	sort.Slice(results, func(i, j int) bool {
		return results[i].Commits > results[j].Commits
	})

	// Output Top 10
	limit := 10
	if len(results) < limit {
		limit = len(results)
	}

	fmt.Println("### ── Code Hotspots Top 10 (Descending by Change Frequency) ──\n")
	fmt.Printf("%-60s  %s  %s\n", "File Path", "Commit Count", "Function Count")
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

	fmt.Printf("### ── Deadcode Check Results (Found %d dead nodes) ──\n\n", len(dead))
	if len(dead) == 0 {
		fmt.Println("🎉 Perfect! All declared functions in your project are reachable from known entry points, no dead code redundancy.")
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

	fmt.Printf("### ── Circular Dependency Detection (Found %d dependency cycles) ──\n\n", len(cycles))
	if len(cycles) == 0 {
		fmt.Println("🎉 Success! No circular dependency imports detected between any files/packages.")
	} else {
		for i, c := range cycles {
			fmt.Printf("Cycle %d:\n  %s\n", i+1, strings.Join(c, " ──► "))
		}
	}
}

func couplingCmd() {
	fs := flag.NewFlagSet("coupling", flag.ExitOnError)
	path := fs.String("path", "", "Specific module path prefix")
	_ = fs.Parse(os.Args[2:])

	db, err := getAstraMapDB(projectRoot)
	if err != nil {
		os.Exit(1)
	}
	defer db.Close()

	metrics, err := astramap.GetCouplingMetrics(db, *path)
	if err != nil {
		logError("Failed to get coupling: %v", err)
		os.Exit(1)
	}

	fmt.Printf("### ── Architectural Cohesion Ca/Ce Analysis ──\n\n")
	fmt.Printf("Target prefix range: \"%s\"\n", *path)
	fmt.Printf("• Afferent Coupling (Ca): %d (Number of external links calling this package)\n", metrics.Ca)
	fmt.Printf("• Efferent Coupling (Ce): %d (Number of external links this package calls)\n", metrics.Ce)
	instability := 0.0
	if metrics.Ca+metrics.Ce > 0 {
		instability = float64(metrics.Ce) / float64(metrics.Ca+metrics.Ce)
	}
	fmt.Printf("• Architectural Instability (I): %.2f (0: highly stable, 1: highly fragile)\n", instability)
}

func ownersCmd() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: amap owners <symbol_id>")
		os.Exit(1)
	}
	symbol := os.Args[2]

	db, err := getAstraMapDB(projectRoot)
	if err != nil {
		os.Exit(1)
	}
	defer db.Close()

	ids, resolveErr := astramap.ResolveSymbolToIDs(db, symbol)
	targetID := symbol
	if resolveErr == nil && len(ids) > 0 {
		targetID = ids[0]
	}

	owners, err := astramap.GetCodeOwners(db, targetID, projectRoot)
	if err != nil {
		logError("Failed to extract authors: %v", err)
		os.Exit(1)
	}

	fmt.Printf("### ── Code Ownership Distribution for Symbol %s (Code Owners) ──\n\n", symbol)
	for i, o := range owners {
		fmt.Printf("%d. %s — 贡献度: %.1f%% (Commit Count: %d)\n", i+1, o.Author, o.Percent, o.CommitCount)
	}
}

func queryCmd() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: amap query \"<SQL>\"")
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
		logError("SQL syntax or execution error: %v", err)
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
		fmt.Println("Usage: amap tree <symbol> [--dir=up|down] [--depth=3]")
		os.Exit(1)
	}
	symbol := os.Args[2]

	fs := flag.NewFlagSet("tree", flag.ExitOnError)
	dir := fs.String("dir", "down", "Traversal direction: up (calls) or down (callees)")
	depth := fs.Int("depth", 3, "Recursive tree depth")
	_ = fs.Parse(os.Args[3:])

	db, err := getAstraMapDB(projectRoot)
	if err != nil {
		os.Exit(1)
	}
	defer db.Close()

	ids, resolveErr := astramap.ResolveSymbolToIDs(db, symbol)
	if resolveErr != nil || len(ids) == 0 {
		fmt.Printf("Symbol \"%s\" not found\n", symbol)
		os.Exit(1)
	}
	resolvedID := ids[0]

	fmt.Printf("### ── Call Topology Tree for Symbol %s (Direction: %s, Depth: %d) ──\n\n", symbol, *dir, *depth)

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
