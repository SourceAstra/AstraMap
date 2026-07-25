// Copyright 2026 AstraMap Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the original license at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"astramap-standalone/astramap"
)

func syntaxCmd() {
	if len(os.Args) < 3 {
		printSyntaxHelp()
		return
	}
	action := os.Args[2]
	fs := flag.NewFlagSet("syntax "+action, flag.ExitOnError)
	scope := fs.String("scope", "user", "Package scope: user or project")
	allowUnsigned := fs.Bool("allow-unsigned", false, "Allow an unsigned local package")
	trustKey := fs.String("trust-key", "", "Additional trusted-keys.json path")
	catalog := fs.String("catalog", "", "Signed syntax overlay catalog URL")
	jsonOutput := fs.Bool("json", false, "Print machine-readable JSON")
	_ = fs.Parse(os.Args[3:])
	if *scope != "user" && *scope != "project" {
		syntaxExit(fmt.Errorf("invalid syntax overlay scope: %s", *scope))
	}
	options := astramap.LanguageInstallOptions{
		ProjectRoot: projectRoot, ProjectScope: *scope == "project", AllowUnsigned: *allowUnsigned,
		TrustKeyPath: *trustKey, CatalogURL: *catalog,
	}
	args := fs.Args()
	switch action {
	case "install", "update":
		requireSyntaxArgs(action, args, 1)
		info, err := astramap.InstallLanguagePackage(args[0], options)
		syntaxResult(info, err, *jsonOutput)
	case "enable":
		requireSyntaxArgs(action, args, 2)
		syntaxExit(astramap.EnableLanguagePackage(args[0], args[1], options))
	case "disable":
		requireSyntaxArgs(action, args, 1)
		syntaxExit(astramap.DisableLanguagePackage(args[0], options))
	case "remove":
		requireSyntaxArgs(action, args, 2)
		syntaxExit(astramap.RemoveLanguagePackage(args[0], args[1], options))
	case "list":
		packages, err := astramap.ListLanguagePackages(options)
		if err != nil {
			syntaxExit(err)
		}
		if *jsonOutput {
			data, _ := json.MarshalIndent(packages, "", "  ")
			fmt.Println(string(data))
			return
		}
		if len(packages) == 0 {
			fmt.Println("No syntax overlays installed")
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
		requireSyntaxArgs(action, args, 1)
		if err := astramap.DiagnoseLanguagePackage(args[0], options); err != nil {
			syntaxExit(err)
		}
		fmt.Printf("Syntax overlay %s is valid and its worker handshake succeeded\n", args[0])
	default:
		fmt.Fprintf(os.Stderr, "Unknown syntax action: %s\n", action)
		printSyntaxHelp()
		os.Exit(2)
	}
}

func requireSyntaxArgs(action string, args []string, count int) {
	if len(args) == count {
		return
	}
	fmt.Fprintf(os.Stderr, "syntax %s requires %d argument(s)\n", action, count)
	printSyntaxHelp()
	os.Exit(2)
}

func syntaxResult(info astramap.LanguagePackageInfo, err error, jsonOutput bool) {
	if err != nil {
		syntaxExit(err)
	}
	if jsonOutput {
		data, _ := json.MarshalIndent(info, "", "  ")
		fmt.Println(string(data))
		return
	}
	fmt.Printf("Installed syntax overlay %s %s (%s scope)\n", info.ID, info.Version, info.Scope)
}

func syntaxExit(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "Syntax overlay error:", err)
	os.Exit(1)
}

func printSyntaxHelp() {
	fmt.Println(strings.TrimSpace(`
Usage:
  amap syntax install [flags] <id|path|url>
  amap syntax update [flags] <id|path|url>
  amap syntax list [--scope user|project] [--json]
  amap syntax doctor [flags] <id>
  amap syntax enable [flags] <id> <version>
  amap syntax disable [flags] <id>
  amap syntax remove [flags] <id> <version>

Unsigned packages are rejected unless --allow-unsigned is explicitly supplied.
`))
}
