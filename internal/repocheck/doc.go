// Package repocheck holds tests (and only tests) that read this repository's own files and check their wiring: workflows, action
// definitions, docs and config. They replace the Bash files that did this with a YAML tool and grep; `go test ./...` runs them.
package repocheck
