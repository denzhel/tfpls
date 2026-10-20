package planstore

import (
	"fmt"
	"io"
	"os"
	"os/exec"
)

// OSEnv is the Env of the real process: its environment and the gh on PATH.
func OSEnv() Env {
	return Env{Getenv: os.Getenv, Gh: func(args ...string) ([]byte, error) {
		return exec.Command("gh", args...).Output()
	}}
}

const usage = `usage: tfpls plan-store <op> [args]
  slug <name> | targets-hash <targets> | plan-name <name> <head sha> <targets>
  pack <dir> <outdir> <meta.json> | unpack <file> <outdir> | check-meta <meta.json> <tree> <name> <dir> <tofu version>
  lookup <owner/repo> <name> | fetch <owner/repo> <name> <outdir> | hint <reason> <label> [targets]
The result goes to stdout, the reason of a refusal to stderr; exit 0 ok, 1 refused or not found, 2 failure.
The key is read from PLAN_ENCRYPTION_KEY, never from an argument.
`

var arity = map[string]int{"slug": 1, "targets-hash": 1, "plan-name": 3, "pack": 3, "unpack": 2, "check-meta": 5, "lookup": 2, "fetch": 3, "hint": 2}

// Run is `tfpls plan-store`.
func Run(args []string, env Env, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	op, a := args[0], args[1:]
	n, known := arity[op]
	if !known || (op == "hint" && (len(a) < 2 || len(a) > 3)) || (op != "hint" && len(a) != n) {
		fmt.Fprint(stderr, usage)
		return 2
	}
	var out string
	var err error
	switch op {
	case "slug":
		out = Slug(a[0])
	case "targets-hash":
		out = TargetsHash(a[0])
	case "plan-name":
		out = PlanName(a[0], a[1], a[2])
	case "pack":
		out, err = Pack(env, a[0], a[1], a[2])
	case "unpack":
		err = Unpack(env, a[0], a[1])
	case "check-meta":
		err = CheckMeta(a[0], a[1], a[2], a[3], a[4])
	case "lookup":
		out, err = Lookup(env, a[0], a[1])
	case "fetch":
		out, err = Fetch(env, a[0], a[1], a[2])
	case "hint":
		t := ""
		if len(a) == 3 {
			t = a[2]
		}
		out = Hint(env, a[0], a[1], t)
	}
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return Code(err)
	}
	if out != "" {
		fmt.Fprintln(stdout, out)
	}
	return 0
}
