package main

import (
	"os"
	"os/exec"
	"strings"

	"github.com/denzhel/tfpls/internal/dashboard"
	"github.com/denzhel/tfpls/internal/gh"
	"github.com/denzhel/tfpls/internal/ghactions"
)

func init() {
	register("dashboard", "build the status dashboard from the tfpls-data branch: build <data> <out> | action | open <owner/repo> [out]", func(args []string) int {
		return dashboard.Main(args, dashboard.Env{
			Getenv: os.Getenv, Stdout: os.Stdout, Stderr: os.Stderr, SetOutput: ghactions.New().SetOutput,
			Token: func() (string, error) {
				s, err := gh.Client{}.Run("auth", "token")
				return strings.TrimSpace(s), err
			},
			Open: func(path string) {
				for _, opener := range []string{"open", "xdg-open"} {
					if p, err := exec.LookPath(opener); err == nil {
						_ = exec.Command(p, path).Start()
						return
					}
				}
			},
		})
	})
}
