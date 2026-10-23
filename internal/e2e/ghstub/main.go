// Command ghstub is the fake `gh` of the engine end-to-end tests (internal/e2e): it answers the few GitHub calls the engine makes
// and records them. It reads and writes below $STUB_DIR: gh.log (every call), posted-<n> (the newest comment body posted on pull
// request n), artifacts.json (the listing of workflow artifacts, if any) and artifact/ (the files `gh run download` copies).
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	dir := os.Getenv("STUB_DIR")
	args := os.Args[1:]
	if dir == "" {
		fmt.Fprintln(os.Stderr, "ghstub: STUB_DIR is not set")
		os.Exit(3)
	}
	if f, err := os.OpenFile(filepath.Join(dir, "gh.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintln(f, strings.Join(args, " "))
		f.Close()
	}
	joined := strings.Join(args, " ")
	switch {
	case len(args) > 1 && args[0] == "api" && strings.Contains(joined, "/issues/") && strings.Contains(joined, "-f body="):
		// POST of a comment: keep the body of a new comment (a PATCH marks an older one as superseded)
		for i, a := range args {
			if a == "-f" && i+1 < len(args) && strings.HasPrefix(args[i+1], "body=") && !strings.Contains(joined, "-X PATCH") {
				parts := strings.Split(args[1], "/")
				n := parts[len(parts)-2]
				_ = os.WriteFile(filepath.Join(dir, "posted-"+n), []byte(strings.TrimPrefix(args[i+1], "body=")), 0o644)
			}
		}
		fmt.Println(`{"id":9,"html_url":"https://github.com/o/r/pull/1#issuecomment-9"}`)
	case strings.Contains(joined, "/issues/") && strings.Contains(joined, "/comments"):
		fmt.Println("[]")
	case strings.Contains(joined, "/actions/artifacts"):
		b, err := os.ReadFile(filepath.Join(dir, "artifacts.json"))
		if err != nil {
			b = []byte(`{"artifacts":[]}`)
		}
		_, _ = os.Stdout.Write(b)
	case len(args) > 1 && args[0] == "run" && args[1] == "download":
		out := ""
		for i, a := range args {
			if a == "-D" && i+1 < len(args) {
				out = args[i+1]
			}
		}
		files, _ := filepath.Glob(filepath.Join(dir, "artifact", "*"))
		if out == "" || len(files) == 0 {
			fmt.Fprintln(os.Stderr, "ghstub: nothing to download")
			os.Exit(1)
		}
		for _, f := range files {
			src, err := os.Open(f)
			if err != nil {
				os.Exit(1)
			}
			dst, err := os.Create(filepath.Join(out, filepath.Base(f)))
			if err != nil {
				os.Exit(1)
			}
			_, _ = io.Copy(dst, src)
			src.Close()
			dst.Close()
		}
	case strings.Contains(joined, "/actions/runs"):
		// no run is active (the unlock checks)
	default:
		fmt.Fprintln(os.Stderr, "ghstub: unexpected call: "+joined)
		os.Exit(1)
	}
}
