// Command tfpls is the single binary behind the tfpls actions and tools.
package main

import (
	"fmt"
	"os"
)

// version is set at build time: -ldflags "-X main.version=v0.5.0".
var version = "dev"

const usage = `tfpls: OpenTofu on GitHub Actions

Usage:
  tfpls version
  tfpls onboard [flags]

`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "version", "--version":
		fmt.Println(version)
		return 0
	case "onboard":
		fmt.Fprintln(os.Stderr, "tfpls onboard: not ported yet, use tools/onboard")
		return 1
	case "help", "--help", "-h":
		fmt.Print(usage)
		return 0
	}
	fmt.Fprintf(os.Stderr, "tfpls: unknown command %q\n\n%s", args[0], usage)
	return 2
}
