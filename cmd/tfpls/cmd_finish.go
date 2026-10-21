package main

import (
	"fmt"
	"os"
	"time"

	"github.com/denzhel/tfpls/internal/finish"
	"github.com/denzhel/tfpls/internal/ghactions"
)

func init() {
	register("finish", "end of a tofu job: `finish webhook` picks the notification webhook, `finish store` writes the audit event", func(args []string) int {
		e := finish.Env{Getenv: os.Getenv, Stdout: os.Stdout, SetOutput: ghactions.New().SetOutput, Now: time.Now}
		if len(args) == 1 {
			switch args[0] {
			case "webhook":
				return finish.Webhook(e)
			case "store":
				return finish.Store(e)
			}
		}
		fmt.Fprintln(os.Stderr, "usage: tfpls finish webhook|store")
		return 2
	})
}
