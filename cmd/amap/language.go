package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"astramap-standalone/astramap"
)

func languageCmd() {
	if len(os.Args) < 3 {
		printLanguageHelp()
		return
	}
	action := os.Args[2]
	fs := flag.NewFlagSet("language "+action, flag.ExitOnError)
	scope := fs.String("scope", "user", "Package scope: user or project")
	allowUnsigned := fs.Bool("allow-unsigned", false, "Allow an unsigned local package")
	trustKey := fs.String("trust-key", "", "Additional trusted-keys.json path")
	catalog := fs.String("catalog", "", "Signed language package catalog URL")
	jsonOutput := fs.Bool("json", false, "Print machine-readable JSON")
	_ = fs.Parse(os.Args[3:])
	if *scope != "user" && *scope != "project" {
		languageExit(fmt.Errorf("invalid language package scope: %s", *scope))
	}
	options := astramap.LanguageInstallOptions{
		ProjectRoot: projectRoot, ProjectScope: *scope == "project", AllowUnsigned: *allowUnsigned,
		TrustKeyPath: *trustKey, CatalogURL: *catalog,
	}
	args := fs.Args()
	switch action {
	case "install", "update":
		requireLanguageArgs(action, args, 1)
		info, err := astramap.InstallLanguagePackage(args[0], options)
		languageResult(info, err, *jsonOutput)
	case "enable":
		requireLanguageArgs(action, args, 2)
		languageExit(astramap.EnableLanguagePackage(args[0], args[1], options))
	case "disable":
		requireLanguageArgs(action, args, 1)
		languageExit(astramap.DisableLanguagePackage(args[0], options))
	case "remove":
		requireLanguageArgs(action, args, 2)
		languageExit(astramap.RemoveLanguagePackage(args[0], args[1], options))
	case "list":
		packages, err := astramap.ListLanguagePackages(options)
		if err != nil {
			languageExit(err)
		}
		if *jsonOutput {
			data, _ := json.MarshalIndent(packages, "", "  ")
			fmt.Println(string(data))
			return
		}
		if len(packages) == 0 {
			fmt.Println("No language packages installed")
			return
		}
		for _, item := range packages {
			state := "disabled"
			if item.Enabled {
				state = "active"
			}
			fmt.Printf("%s\t%s\t%s\t%s\n", item.ID, item.Version, state, item.Scope)
		}
	case "doctor":
		requireLanguageArgs(action, args, 1)
		if err := astramap.DiagnoseLanguagePackage(args[0], options); err != nil {
			languageExit(err)
		}
		fmt.Printf("Language package %s is valid and its worker handshake succeeded\n", args[0])
	default:
		fmt.Fprintf(os.Stderr, "Unknown language action: %s\n", action)
		printLanguageHelp()
		os.Exit(2)
	}
}

func requireLanguageArgs(action string, args []string, count int) {
	if len(args) == count {
		return
	}
	fmt.Fprintf(os.Stderr, "language %s requires %d argument(s)\n", action, count)
	printLanguageHelp()
	os.Exit(2)
}

func languageResult(info astramap.LanguagePackageInfo, err error, jsonOutput bool) {
	if err != nil {
		languageExit(err)
	}
	if jsonOutput {
		data, _ := json.MarshalIndent(info, "", "  ")
		fmt.Println(string(data))
		return
	}
	fmt.Printf("Installed %s %s (%s scope)\n", info.ID, info.Version, info.Scope)
}

func languageExit(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "Language package error:", err)
	os.Exit(1)
}

func printLanguageHelp() {
	fmt.Println(strings.TrimSpace(`
Usage:
  amap language install [flags] <id|path|url>
  amap language update [flags] <id|path|url>
  amap language list [--scope user|project] [--json]
  amap language doctor [flags] <id>
  amap language enable [flags] <id> <version>
  amap language disable [flags] <id>
  amap language remove [flags] <id> <version>

Unsigned packages are rejected unless --allow-unsigned is explicitly supplied.
`))
}
