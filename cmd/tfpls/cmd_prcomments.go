package main

import (
	"os"

	"github.com/denzhel/tfpls/internal/gh"
	"github.com/denzhel/tfpls/internal/ghactions"
	"github.com/denzhel/tfpls/internal/prcomments"
)

func prcommentsEnv() prcomments.Env {
	return prcomments.Env{Getenv: os.Getenv, Stdout: os.Stdout, Stderr: os.Stderr, GH: gh.Client{},
		Output: ghactions.New().SetOutput, Git: prcomments.GitSuccess}
}

func init() {
	register("pr-notes", "one sticky first-run note on a pull request: introduced | no-workspaces | clear", func(args []string) int {
		return prcomments.Notes(args, prcommentsEnv())
	})
	register("mark-outdated", "banner on the plan comments of workspaces the latest commit does not touch", func([]string) int {
		return prcomments.Outdated(prcommentsEnv())
	})
}
