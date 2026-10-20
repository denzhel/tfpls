// Command tfpls is the single binary behind the tfpls actions and tools.
package main

import (
	"fmt"
	"os"

	"github.com/denzhel/tfpls/internal/onboard"
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
		cwd, _ := os.Getwd()
		return onboard.Run(args[1:], onboard.Env{
			Getenv: os.Getenv, Stdout: os.Stdout, Stderr: os.Stderr,
			StdoutIsTerminal: onboard.IsTerminal(os.Stdout), Cwd: cwd,
		})
	case "help", "--help", "-h":
		fmt.Print(usage)
		return 0
	}
	fmt.Fprintf(os.Stderr, "tfpls: unknown command %q\n\n%s", args[0], usage)
	return 2
}
