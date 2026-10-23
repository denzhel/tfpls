package main

import (
	"fmt"
	"os"

	"github.com/denzhel/tfpls/internal/driftfix"
	"github.com/denzhel/tfpls/internal/gh"
)

func init() {
	register("drift-fix", "guard a drift fix: `parse` (comment or manual run) or `filter` (drift_fix: false)", func(args []string) int {
		e := driftfix.Env{Getenv: os.Getenv, Stdout: os.Stdout, Stderr: os.Stderr, GH: gh.Client{}}
		if len(args) == 1 && args[0] == "parse" {
			return driftfix.Parse(e)
		}
		if len(args) == 1 && args[0] == "filter" {
			return driftfix.Filter(e)
		}
		fmt.Fprintln(os.Stderr, "usage: tfpls drift-fix parse|filter")
		return 2
	})
}
