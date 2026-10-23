package main

import (
	"os"

	"github.com/denzhel/tfpls/internal/comment"
	"github.com/denzhel/tfpls/internal/gh"
)

func init() {
	register("comment", "the /tofu comment commands of comment-ops: `parse` (who, which pull request, which arguments) or `nomatch` (reply)", func(args []string) int {
		return comment.Main(args, comment.Env{Getenv: os.Getenv, Stdout: os.Stdout, Stderr: os.Stderr, GH: gh.Client{}})
	})
}
