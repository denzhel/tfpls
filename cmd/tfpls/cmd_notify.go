package main

import (
	"os"

	"github.com/denzhel/tfpls/internal/gh"
	"github.com/denzhel/tfpls/internal/notify"
)

func init() {
	register("notify-slack", "post a status message to a Slack, Teams or generic webhook (env: HOOK, STATUS, NAME, ...)", func([]string) int {
		return notify.Run(notify.Env{Getenv: os.Getenv, Stdout: os.Stdout, GH: gh.Client{}})
	})
}
