package planstore

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/denzhel/tfpls/internal/gh"
)

// OSEnv is the Env of the real process: its environment and the gh on PATH.
func OSEnv() Env {
	return Env{Getenv: os.Getenv, Gh: func(args ...string) ([]byte, error) {
		out, err := gh.Client{}.Run(args...)
		return []byte(out), err
	}}
}

const planUsage = `usage: tfpls plan decrypt <plan.tgz.enc|plan.tgz> [outdir]   extract tfplan + meta.json (outdir default ./tfpls-plan-out)
       tfpls plan show    <plan.tgz.enc|plan.tgz> [tf-dir]   print the metadata, then the readable plan (needs tofu or terraform)
An encrypted plan needs the key: export PLAN_ENCRYPTION_KEY=... (never pass it as an argument).
`

// Plan is `tfpls plan`: inspect a saved plan downloaded from a workflow run (the former tools/tfpls-plan).
func Plan(args []string, env Env, stdout, stderr io.Writer) int {
	die := func(msg string) int { fmt.Fprintln(stderr, "tfpls plan: "+msg); return 1 }
	if len(args) < 2 || len(args) > 3 {
		fmt.Fprint(stderr, planUsage)
		return 1
	}
	cmd, file := args[0], args[1]
	if st, err := os.Stat(file); err != nil || st.IsDir() {
		fmt.Fprint(stderr, planUsage)
		return 1
	}
	meta := func(dir string) {
		b, _ := os.ReadFile(dir + "/meta.json")
		var v any
		if json.Unmarshal(b, &v) == nil {
			b, _ = json.MarshalIndent(v, "", "  ")
		}
		fmt.Fprintln(stdout, string(b))
	}
	switch cmd {
	case "decrypt":
		out := "./tfpls-plan-out"
		if len(args) == 3 {
			out = args[2]
		}
		if err := Unpack(env, file, out); err != nil {
			return die(err.Error())
		}
		fmt.Fprintln(stdout, "extracted to "+out)
		meta(out)
	case "show":
		dir := "."
		if len(args) == 3 {
			dir = args[2]
		}
		tmp, err := os.MkdirTemp("", "tfpls-plan")
		if err != nil {
			return die(err.Error())
		}
		defer os.RemoveAll(tmp)
		if err := Unpack(env, file, tmp); err != nil {
			return die(err.Error())
		}
		fmt.Fprintln(stdout, "--- metadata")
		meta(tmp)
		tf, err := exec.LookPath("tofu")
		if err != nil {
			if tf, err = exec.LookPath("terraform"); err != nil {
				return die("tofu/terraform not found (use 'decrypt' to just extract)")
			}
		}
		fmt.Fprintln(stdout, "--- plan")
		c := exec.Command(tf, "show", "-no-color", tmp+"/tfplan")
		c.Dir, c.Stdout, c.Stderr = dir, stdout, stderr
		if err := c.Run(); err != nil {
			return 1
		}
	default:
		return die("unknown command: " + cmd)
	}
	return 0
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
