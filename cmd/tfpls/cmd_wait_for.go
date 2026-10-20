package main

import (
	"os"
	"time"

	"github.com/denzhel/tfpls/internal/gh"
	"github.com/denzhel/tfpls/internal/waitfor"
)

func init() {
	register("wait-for", "wait for other jobs of this run (reads $WAIT_FOR, $TIMEOUT, $REPO, $RUN_ID)", func([]string) int {
		return waitfor.Run(waitfor.Env{Getenv: os.Getenv, Stdout: os.Stdout, GH: gh.Client{}, Now: time.Now, Sleep: time.Sleep})
	})
}
