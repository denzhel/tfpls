package tofu

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var doneRe = regexp.MustCompile(`^ *(.*?): (Creation|Modifications|Destruction) complete`)
var rollbackErrRe = regexp.MustCompile(`^(│ )?Error: `)

// Rollback is Markdown hints after a failed apply: what was already changed and how to get back. sha is the commit to revert.
func Rollback(applyText, sha string) string {
	text := ansiRe.ReplaceAllString(applyText, "")
	doneSet := map[string]bool{}
	errSet := map[string]bool{}
	type item struct{ addr, kind string }
	var done []item
	for _, l := range strings.Split(text, "\n") {
		if m := doneRe.FindStringSubmatch(l); m != nil {
			kind := m[2]
			if kind == "Modifications" {
				kind = "Modification"
			}
			key := m[1] + "\x00" + kind
			if !doneSet[key] {
				doneSet[key] = true
				done = append(done, item{m[1], kind})
			}
		}
		if rollbackErrRe.MatchString(l) {
			errSet[strings.TrimPrefix(l, "│ ")] = true
		}
	}
	// sort -u over the lines "<address> (<Kind>)"
	sort.Slice(done, func(i, j int) bool {
		return done[i].addr+" ("+done[i].kind+")" < done[j].addr+" ("+done[j].kind+")"
	})
	var errs []string
	for e := range errSet {
		errs = append(errs, e)
	}
	sort.Strings(errs)
	if len(errs) > 5 {
		errs = errs[:5]
	}
	var b strings.Builder
	b.WriteString("**The apply failed part-way.** Terraform state already contains what was finished; nothing is rolled back automatically.\n\n")
	if n := len(done); n > 0 {
		fmt.Fprintf(&b, "Already changed before the failure (%d):\n", n)
		for i, d := range done {
			if i == 20 {
				break
			}
			fmt.Fprintf(&b, "- `%s` (%s)\n", d.addr, d.kind)
		}
		if n > 20 {
			fmt.Fprintf(&b, "- … %d more\n", n-20)
		}
		b.WriteString("\n")
	} else {
		b.WriteString("No resource was changed before the failure.\n\n")
	}
	if len(errs) > 0 {
		b.WriteString("Errors:\n")
		for _, e := range errs {
			b.WriteString("- " + e + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("What you can do:\n")
	b.WriteString("1. **Fix forward**: correct the cause (permissions, quota, a conflicting resource) and run `/tofu plan` again; the next plan shows only what is left.\n")
	if sha != "" {
		fmt.Fprintf(&b, "2. **Go back**: revert the change (`git revert %s`) in a new pull request; its plan shows exactly what the revert would undo, and applying it restores the previous configuration.\n", cutChars(sha, 12))
	} else {
		b.WriteString("2. **Go back**: revert the change in a new pull request; its plan shows exactly what the revert would undo.\n")
	}
	b.WriteString("3. Do not `force-unlock` or delete state by hand unless `/tofu unlock` reports a stale lock.\n")
	return b.String()
}
