package main

import (
	"os"

	"github.com/denzhel/tfpls/internal/approvals"
	"github.com/denzhel/tfpls/internal/detect"
	"github.com/denzhel/tfpls/internal/gh"
)

func init() {
	register("approvals", "approval policy for /tofu apply: rules | enforce | check", func(args []string) int {
		return approvals.Run(args, approvals.Env{
			Getenv: os.Getenv, Stdout: os.Stdout, Stderr: os.Stderr, GH: gh.Client{}, Validate: validateConfig,
		})
	})
}

// validateConfig returns the first error of the config (ok=false), warnings are not a reason to refuse.
func validateConfig(cfg any) (string, bool) {
	for _, p := range detect.Validate(cfg) {
		if p.Level == "error" {
			return p.Msg, false
		}
	}
	return "", true
}
