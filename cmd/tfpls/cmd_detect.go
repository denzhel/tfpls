package main

import (
	"fmt"
	"os"

	"github.com/denzhel/tfpls/internal/detect"
)

func init() {
	register("detect", "build the job matrix from .tfpls.yml (reads CONFIG, MODE, BASE, HEAD_REF, NAME)", func([]string) int {
		return detect.Run(detect.DefaultEnv())
	})
	register("validate-config", "validate a .tfpls.yml (YAML or JSON on stdin) and print the problems as a JSON array", func([]string) int {
		if err := detect.ValidateReader(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "tfpls validate-config:", err)
			return 1
		}
		return 0
	})
}
