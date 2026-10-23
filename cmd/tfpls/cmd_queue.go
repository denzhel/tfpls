package main

import (
	"os"
	"time"

	"github.com/denzhel/tfpls/internal/datastore"
	"github.com/denzhel/tfpls/internal/gh"
	"github.com/denzhel/tfpls/internal/queue"
)

func init() {
	register("queue", "first-come-first-served queue per workspace (reads $MODE enter|leave, $NAME, $LABEL, $PR, $BRANCH, $TIMEOUT ...)", func([]string) int {
		return queue.Run(queue.Env{Getenv: os.Getenv, Stdout: os.Stdout, Stderr: os.Stderr, GH: gh.Client{},
			Store: datastore.FromEnv(os.Getenv), Now: time.Now, Sleep: time.Sleep, Pid: os.Getpid()})
	})
}
