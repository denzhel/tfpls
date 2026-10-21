package main

import (
	"os"
	"time"

	"github.com/denzhel/tfpls/internal/driftreport"
	"github.com/denzhel/tfpls/internal/gh"
	"github.com/denzhel/tfpls/internal/ghactions"
)

func init() {
	register("drift-report", "turn a drift result into a transition and keep one issue per drifted workspace (env: NAME, STATE, ...)", func([]string) int {
		return driftreport.Run(driftreport.Env{Getenv: os.Getenv, Stdout: os.Stdout, GH: gh.Client{},
			SetOutput: ghactions.New().SetOutput, Now: time.Now})
	})
}
