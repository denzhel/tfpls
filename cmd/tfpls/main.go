// Command tfpls is the single binary behind the tfpls actions and tools.
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// version is set at build time: -ldflags "-X main.version=v0.5.0".
var version = "dev"

// command is one subcommand: it gets the arguments after its name and returns the exit code.
type command struct {
	run  func(args []string) int
	help string // one line for `tfpls help`
}

// commands is filled by the cmd_<name>.go files (one per module, each registers itself in init), so adding a module never
// touches this file.
var commands = map[string]command{}

func register(name, help string, run func(args []string) int) {
	commands[name] = command{run: run, help: help}
}

func usage() string {
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("tfpls: OpenTofu on GitHub Actions\n\nUsage:\n  tfpls version\n")
	for _, n := range names {
		fmt.Fprintf(&b, "  tfpls %-16s %s\n", n, commands[n].help)
	}
	b.WriteString("\n")
	return b.String()
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage())
		return 2
	}
	switch args[0] {
	case "version", "--version":
		fmt.Println(version)
		return 0
	case "help", "--help", "-h":
		fmt.Print(usage())
		return 0
	}
	if c, ok := commands[args[0]]; ok {
		return c.run(args[1:])
	}
	fmt.Fprintf(os.Stderr, "tfpls: unknown command %q\n\n%s", args[0], usage())
	return 2
}
