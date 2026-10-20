package main

import (
	"os"
	"time"

	"github.com/denzhel/tfpls/internal/record"
)

func init() {
	register("record", "append one audit event to the data branch (reads $WORKSPACE, $KIND, $STATUS, ...)", func([]string) int {
		return record.Run(record.Env{Getenv: os.Getenv, Stdout: os.Stdout, Now: time.Now})
	})
}
