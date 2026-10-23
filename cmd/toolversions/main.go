// Command toolversions is the weekly tool-version watch (.github/workflows/tool-versions.yml). It is not part of the
// shipped binary.
//
//	GH_TOKEN=... REPO=owner/repo go run ./cmd/toolversions
//
// Env: GH_TOKEN, REPO (default $GITHUB_REPOSITORY), RUN_URL, GITHUB_STEP_SUMMARY, TFPLS_ROOT (test override: the tree read).
package main

import (
	"os"

	"github.com/denzhel/tfpls/internal/gh"
	"github.com/denzhel/tfpls/internal/toolversions"
)

func main() {
	os.Exit(toolversions.Run(toolversions.Env{Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv, GH: gh.Client{}}))
}
