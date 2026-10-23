package main

import (
	"os"

	"github.com/denzhel/tfpls/internal/gate"
	"github.com/denzhel/tfpls/internal/gh"
)

func gateEnv() gate.Env {
	return gate.Env{Getenv: os.Getenv, GH: gh.Client{}.Run, Stdout: os.Stdout, Stderr: os.Stderr}
}

func init() {
	register("gate", "the issue agent's gate: decide whether a model run is needed (elaborate|implement; env: REPO, ISSUE, EVENT_NAME, ...)", func(args []string) int {
		return gate.Run(args, gateEnv())
	})
	register("agent-report", "report what an issue-agent run used (env: PHASE, ISSUE, REPO, OUTCOME, EXEC, ...)", func([]string) int {
		return gate.Report(gateEnv())
	})
}
