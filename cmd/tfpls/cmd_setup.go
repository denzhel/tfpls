package main

import "github.com/denzhel/tfpls/internal/setup"

func init() {
	register("setup", "install OpenTofu and conftest (verified, cached); `setup detect [dir]` prints the version a repository asks for", func(args []string) int {
		return setup.Run(args, setup.DefaultEnv())
	})
}
