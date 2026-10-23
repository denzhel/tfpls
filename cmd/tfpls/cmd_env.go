package main

import (
	"os"

	"github.com/denzhel/tfpls/internal/credentials"
	"github.com/denzhel/tfpls/internal/gh"
)

func init() {
	register("env", "export the job credentials (reads $JSON, $ROLE_ARN, $REPORT_PR, $GH_TOKEN)", func([]string) int {
		return credentials.Run(credentials.Env{Getenv: os.Getenv, Stdout: os.Stdout, GH: gh.Client{}})
	})
}
