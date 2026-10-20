package main

import (
	"os"

	"github.com/denzhel/tfpls/internal/result"
)

func init() {
	register("result", "one check for branch protection (reads the needs JSON from $NEEDS)", func([]string) int {
		return result.Run(os.Getenv("NEEDS"), os.Stdout)
	})
}
