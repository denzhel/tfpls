package main

import "github.com/denzhel/tfpls/internal/tofu"

func init() {
	register("tofu", "plan/apply one workspace (the tofu action); also failure, rollback, summarize, fmt helpers", func(args []string) int {
		return tofu.Main(args, tofu.OSEnv())
	})
}
