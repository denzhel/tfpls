package tofu

import (
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/denzhel/tfpls/internal/ghactions"
)

const usage = `usage: tfpls tofu [run]                              plan/apply one workspace (the tofu action; settings from the environment)
       tfpls tofu failure <output.txt> <label>       Markdown for a failed init/plan
       tfpls tofu rollback <apply.txt> [sha]         Markdown hints after a failed apply
       tfpls tofu summarize <plan.json|-> [json|header]
       tfpls tofu fmt <clip|plan-line|apply-line|comment-body|already-applied-body|strip-refresh|apply-view|post-comment> ...
         (the comment helpers, for maintainers and the tests)
`

// Main is `tfpls tofu`.
func Main(args []string, e Env) int {
	if len(args) == 0 || args[0] == "run" {
		return Run(e)
	}
	op, a := args[0], args[1:]
	switch op {
	case "failure":
		if len(a) < 1 {
			fmt.Fprint(e.Stderr, usage)
			return 2
		}
		b, err := os.ReadFile(a[0])
		if err != nil {
			fmt.Fprintln(e.Stderr, err)
			return 2
		}
		label := "<workspace>"
		if len(a) > 1 {
			label = a[1]
		}
		fmt.Fprint(e.Stdout, Failure(string(b), label, e.Getenv("GITHUB_ACTION_REPOSITORY")))
	case "rollback":
		if len(a) < 1 {
			fmt.Fprint(e.Stderr, usage)
			return 2
		}
		b, err := os.ReadFile(a[0])
		if err != nil {
			fmt.Fprintln(e.Stderr, err)
			return 2
		}
		sha := ""
		if len(a) > 1 {
			sha = a[1]
		}
		fmt.Fprint(e.Stdout, Rollback(string(b), sha))
	case "summarize":
		return summarizeCmd(a, e)
	case "fmt":
		return fmtCmd(a, e)
	default:
		fmt.Fprint(e.Stderr, usage)
		return 2
	}
	return 0
}

func summarizeCmd(a []string, e Env) int {
	if len(a) < 1 {
		fmt.Fprint(e.Stderr, usage)
		return 2
	}
	mode := "json"
	if len(a) > 1 {
		mode = a[1]
	}
	if mode != "json" && mode != "header" {
		fmt.Fprintln(e.Stderr, "unknown mode: "+mode)
		return 2
	}
	var in []byte
	var err error
	if a[0] == "-" {
		in, err = io.ReadAll(os.Stdin)
	} else {
		in, err = os.ReadFile(a[0])
	}
	if err != nil {
		fmt.Fprintln(e.Stderr, err)
		return 1
	}
	s, err := Summarize(in)
	if err != nil {
		fmt.Fprintln(e.Stderr, err)
		return 1
	}
	if mode == "header" {
		fmt.Fprintln(e.Stdout, s.Header)
	} else {
		fmt.Fprintln(e.Stdout, compact(s))
	}
	return 0
}

func fmtCmd(a []string, e Env) int {
	need := func(n int) bool {
		if len(a) < n+1 {
			fmt.Fprint(e.Stderr, usage)
			return false
		}
		return true
	}
	switch {
	case len(a) == 0:
		fmt.Fprint(e.Stderr, usage)
		return 2
	case a[0] == "clip" && need(1):
		max := 45000
		if len(a) > 2 {
			max, _ = strconv.Atoi(a[2])
		}
		fmt.Fprint(e.Stdout, ClipPlan(a[1], max))
	case a[0] == "plan-line" && need(1):
		if l := PlanLine(a[1]); l != "" {
			fmt.Fprintln(e.Stdout, l)
		}
	case a[0] == "apply-line" && need(1):
		if l := ApplyLine(a[1]); l != "" {
			fmt.Fprintln(e.Stdout, l)
		}
	case a[0] == "strip-refresh":
		b, _ := io.ReadAll(os.Stdin)
		fmt.Fprint(e.Stdout, StripRefresh(string(b)))
	case a[0] == "apply-view":
		b, _ := io.ReadAll(os.Stdin)
		fmt.Fprint(e.Stdout, ApplyView(string(b)))
	case a[0] == "comment-body" && need(9):
		limit, _ := strconv.Atoi(a[9])
		fmt.Fprint(e.Stdout, CommentBody(a[1], a[2], a[3], a[4], a[5], a[6], a[7], a[8], limit, e.Getenv("TFPLS_NO_HINT") != ""))
	case a[0] == "already-applied-body" && need(4):
		fmt.Fprint(e.Stdout, AlreadyAppliedBody(a[1], a[2], a[3], a[4]))
	case a[0] == "post-comment" && need(2):
		PostComment(e.GH, &ghactions.Runner{Getenv: e.Getenv, Stdout: e.Stdout}, e.Getenv("GITHUB_REPOSITORY"), e.Getenv("PR"), a[1], a[2])
	default:
		fmt.Fprint(e.Stderr, usage)
		return 2
	}
	return 0
}
