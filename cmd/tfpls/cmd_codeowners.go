package main

import (
	"fmt"
	"os"

	"github.com/denzhel/tfpls/internal/codeowners"
)

func init() {
	register("codeowners", "CODEOWNERS approvals for an apply: `enforce` (env) or `check <CODEOWNERS> <reviews.json> <sha> <path>...`", func(args []string) int {
		if len(args) > 0 {
			switch args[0] {
			case "enforce":
				return codeowners.Enforce(codeowners.DefaultEnv())
			case "check":
				return codeowners.RunCheck(args[1:], os.Getenv, os.Stdout, os.Stderr)
			}
		}
		fmt.Fprintln(os.Stderr, "usage: tfpls codeowners enforce | check <CODEOWNERS> <reviews.json> <sha> <path>...")
		return 2
	})
}
