package main

import (
	"os"

	"github.com/denzhel/tfpls/internal/planstore"
)

func init() {
	register("plan", "inspect a saved reviewed plan: decrypt <file> [dir] | show <file> [dir] (key from PLAN_ENCRYPTION_KEY)", func(args []string) int {
		return planstore.Plan(args, planstore.OSEnv(), os.Stdout, os.Stderr)
	})
	register("plan-store", "the reviewed-plan store: pack, unpack, lookup, fetch, check-meta, hint (used by the tofu action)", func(args []string) int {
		return planstore.Run(args, planstore.OSEnv(), os.Stdout, os.Stderr)
	})
}
