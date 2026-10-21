package approvals

import (
	"encoding/json"
	"fmt"
	"os"
)

const usage = "usage: tfpls approvals rules | enforce | check <reviews.json> <sha> <strict|basic> <required>"

// Run dispatches `tfpls approvals <rules|enforce|check>`.
func Run(args []string, e Env) int {
	if len(args) == 0 {
		fmt.Fprintln(e.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "rules":
		return Rules(e)
	case "enforce":
		return Enforce(e)
	case "check":
		return checkCmd(args[1:], e)
	}
	fmt.Fprintln(e.Stderr, usage)
	return 2
}

// checkCmd is check.sh: print "ok" or the refusal reason; exit 0 satisfied, 1 refused, 2 usage.
func checkCmd(args []string, e Env) int {
	if len(args) != 4 {
		fmt.Fprintln(e.Stderr, usage)
		return 2
	}
	b, err := os.ReadFile(args[0])
	need, perr := ParseRule(args[2], args[3])
	if err != nil || args[1] == "" || perr != nil {
		fmt.Fprintln(e.Stderr, usage)
		return 2
	}
	var reviews []Review
	if err := json.Unmarshal(b, &reviews); err != nil {
		fmt.Fprintln(e.Stderr, "check: the reviews file is not a JSON list of reviews")
		return 2
	}
	if ok, why := Check(reviews, args[1], args[2], need); !ok {
		fmt.Fprintln(e.Stdout, why)
		return 1
	}
	fmt.Fprintln(e.Stdout, "ok")
	return 0
}
