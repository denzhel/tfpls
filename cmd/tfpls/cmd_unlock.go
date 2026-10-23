package main

import (
	"os"

	"github.com/denzhel/tfpls/internal/gh"
	"github.com/denzhel/tfpls/internal/unlock"
)

func init() {
	register("unlock", "release a stuck state lock, or sweep every workspace when $ENTRIES is set (reads $NAME, $DIR, $WS, $PR, ...)", func([]string) int {
		return unlock.Run(unlock.Env{Getenv: os.Getenv, Environ: os.Environ, Stdout: os.Stdout, Stderr: os.Stderr, GH: gh.Client{}})
	})
}
