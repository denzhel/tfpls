package main

import (
	"os"

	"github.com/denzhel/tfpls/internal/onboard"
)

func init() {
	register("onboard", "set a repository up (workflow, config, checklist)", func(args []string) int {
		cwd, _ := os.Getwd()
		return onboard.Run(args, onboard.Env{
			Getenv: os.Getenv, Stdout: os.Stdout, Stderr: os.Stderr,
			StdoutIsTerminal: onboard.IsTerminal(os.Stdout), Cwd: cwd,
		})
	})
}
