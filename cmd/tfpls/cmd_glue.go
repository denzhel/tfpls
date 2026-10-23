package main

import (
	"os"

	"github.com/denzhel/tfpls/internal/gh"
	"github.com/denzhel/tfpls/internal/glue"
)

func init() {
	register("glue", "small workflow steps: fork-check | drift-state | merged-pr | drift-issue | store-page", func(args []string) int {
		return glue.Main(args, glue.Env{Getenv: os.Getenv, Stdout: os.Stdout, Stderr: os.Stderr, GH: gh.Client{}})
	})
}
