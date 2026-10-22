// Command release is the maintainer tool that cuts a tfpls release and builds its assets. It is not part of the shipped binary.
//
//	go run ./cmd/release [--dry-run] vX.Y.Z|vX.Y.Z-rc.N     cut a release (tools/release.sh runs this)
//	go run ./cmd/release build <tag> <outdir>                build the release assets (.github/workflows/release.yml)
package main

import (
	"fmt"
	"os"

	"github.com/denzhel/tfpls/internal/release"
)

func main() {
	wd, _ := os.Getwd()
	env := release.Env{Stdout: os.Stdout, Stderr: os.Stderr, Dir: wd}
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "build" {
		os.Exit(release.Build(args[1:], env, os.Getenv))
	}
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(os.Stderr, "usage: release [--dry-run] vX.Y.Z | vX.Y.Z-rc.N\n       release build <tag> <outdir>")
		os.Exit(2)
	}
	os.Exit(release.Release(args, env))
}
