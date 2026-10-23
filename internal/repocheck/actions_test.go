package repocheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// GitHub evaluates `${{ ... }}` inside an action's input descriptions as an expression, and an expression that is not valid
// there (for example `needs`) stops the action from loading at all ("Unrecognized named-value").
func TestActionInputDescriptionsHaveNoExpression(t *testing.T) {
	files := glob(t, "actions/*/action.yml")
	for _, f := range files {
		name := filepath.Base(filepath.Dir(f))
		var hits []string
		in, _ := at(load(t, f), "inputs").(map[string]any)
		for k, v := range in {
			if d, _ := at(v, "description").(string); strings.Contains(d, "${{") {
				hits = append(hits, k)
			}
		}
		check(t, name+": no expression in an input description", len(hits) == 0, f+": input description with an expression: "+strings.Join(hits, " "))
	}
	check(t, "checked actions", len(files) > 10, "found too few actions")
}

var usesLine = regexp.MustCompile(`^\s*(-\s*)?uses:`)
var usesRef = regexp.MustCompile(`^\s*(-\s*)?uses:\s*(\S+)`)

type usesAt struct{ file, ref string }

func usesIn(t *testing.T, files []string) []usesAt {
	t.Helper()
	var out []usesAt
	for _, f := range files {
		for _, l := range strings.Split(read(t, f), "\n") {
			if usesLine.MatchString(l) {
				if m := usesRef.FindStringSubmatch(l); m != nil {
					out = append(out, usesAt{f, m[2]})
				}
			}
		}
	}
	return out
}

var (
	pinLen = regexp.MustCompile(`@.{40}$`)
	pinHex = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// pinProblem is "" for a reference that is allowed: a local path, this repository's own, or a third-party action pinned to a
// full commit SHA.
func pinProblem(ref string) string {
	switch {
	case strings.HasPrefix(ref, "./"), strings.HasPrefix(ref, "denzhel/tfpls/"):
		return ""
	case pinLen.MatchString(ref):
		if pinHex.MatchString(ref[strings.LastIndex(ref, "@")+1:]) {
			return ""
		}
		return "not a hex SHA: " + ref
	}
	return "third-party action not pinned to a SHA: " + ref
}

// Every third-party action must be pinned to a full commit SHA (supply-chain safety). Our own repo's references
// (denzhel/tfpls/...@main on main, @<version> in releases) and local ./ paths are allowed.
func TestThirdPartyActionsPinnedToSHA(t *testing.T) {
	files := append(glob(t, ".github/workflows/*.yml"), glob(t, "actions/*/action.yml")...)
	files = append(files, glob(t, "examples/.tfpls.yml")...)
	us := usesIn(t, files)
	for _, u := range us {
		check(t, u.ref+" ("+u.file+")", pinProblem(u.ref) == "", pinProblem(u.ref)+" ("+u.file+")")
	}
	check(t, "scanned uses: lines", len(us) > 0, "found no uses: lines")
}

func TestPinProblemFindsUnpinned(t *testing.T) {
	sha := strings.Repeat("a", 40)
	for ref, want := range map[string]bool{
		"actions/checkout@" + sha:                     true,
		"actions/checkout@v4":                         false,
		"actions/checkout@main":                       false,
		"actions/checkout":                            false,
		"actions/checkout@" + strings.Repeat("g", 40): false,
		"actions/checkout@" + strings.Repeat("A", 40): false,
		"actions/checkout@" + sha[:39]:                false,
		"./actions/tofu":                              true,
		"denzhel/tfpls/actions/tofu@main":             true,
	} {
		eq(t, "pin check of "+ref, pinProblem(ref) == "", want)
	}
}

// actions that still run on Node.js 20 (GitHub forces Node 24 and prints a deprecation warning): none of these pins may come back
var node20 = []string{
	"actions/checkout@11d5960a326750d5838078e36cf38b85af677262",              // v4.4.0
	"actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02",       // v4.6.2
	"actions/upload-pages-artifact@56afc609e74202658d3ffba0e8f6dda462b719fa", // v3.0.1 (uses upload-artifact v4)
	"actions/deploy-pages@d6db90164ac5ed86f2b6aed7e0febac5b3c0c03e",          // v4.0.5
}

// node20Count counts the lines under the directories that contain one of the pins.
func node20Count(dirs ...string) int {
	n := 0
	for _, d := range dirs {
		_ = filepath.Walk(d, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			for _, l := range strings.Split(string(b), "\n") {
				for _, pin := range node20 {
					if strings.Contains(l, pin) {
						n++
					}
				}
			}
			return nil
		})
	}
	return n
}

func TestNoNode20ActionPins(t *testing.T) {
	r := root(t)
	eq(t, "no Node.js 20 action pin in the workflows, actions and examples",
		node20Count(filepath.Join(r, ".github"), filepath.Join(r, "actions"), filepath.Join(r, "examples")), 0)
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "old.yml"), []byte("      - uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eq(t, "the guard finds a Node.js 20 pin that is put back (fixture)", node20Count(d), 1)
}
