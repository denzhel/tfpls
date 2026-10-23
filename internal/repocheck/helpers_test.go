package repocheck

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/denzhel/tfpls/internal/yamlconf"
)

var (
	rootOnce sync.Once
	rootDir  string
)

// root is the repository root: the nearest parent of the working directory (the package directory) with a go.mod.
func root(t testing.TB) string {
	t.Helper()
	rootOnce.Do(func() {
		d, err := os.Getwd()
		if err != nil {
			return
		}
		for {
			if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
				rootDir = d
				return
			}
			p := filepath.Dir(d)
			if p == d {
				return
			}
			d = p
		}
	})
	if rootDir == "" {
		t.Fatal("repository root (go.mod) not found above the working directory")
	}
	return rootDir
}

func read(t testing.TB, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// glob returns the repository-relative paths that match a pattern, sorted.
func glob(t testing.TB, pattern string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(root(t), pattern))
	if err != nil {
		t.Fatal(err)
	}
	r := root(t)
	var out []string
	for _, p := range m {
		rel, _ := filepath.Rel(r, p)
		out = append(out, filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out
}

func load(t testing.TB, rel string) any {
	t.Helper()
	v, err := yamlconf.LoadFile(filepath.Join(root(t), rel))
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	return v
}

// at follows map keys; a missing key or a non-map on the way gives nil (like yq's null).
func at(v any, path ...string) any {
	for _, k := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

// str renders a value the way `yq -r` prints it: null for nil, a string as is, anything else with its default format.
func str(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	}
	return fmt.Sprint(v)
}

// orEmpty is yq's `// ""`: null and false become an empty string.
func orEmpty(v any) string {
	if v == nil || v == false {
		return ""
	}
	return str(v)
}

func keys(v any) []string {
	m, _ := v.(map[string]any)
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func has(v any, key string) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, ok = m[key]
	return ok
}

// stepsOf returns the steps of a job as maps.
func stepsOf(wf any, job string) []map[string]any {
	var out []map[string]any
	for _, s := range list(at(wf, "jobs", job, "steps")) {
		if m, ok := s.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// countLines is `grep -c`: the lines of text that match the expression.
func countLines(text string, re *regexp.Regexp) int {
	n := 0
	for _, l := range strings.Split(text, "\n") {
		if re.MatchString(l) {
			n++
		}
	}
	return n
}

func countLinesFixed(text, sub string) int {
	n := 0
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

// section is the body of the "## <title>" section of a Markdown file, up to the next "## " heading (the heading line excluded).
func section(text, title string) string {
	var b strings.Builder
	in := false
	for _, l := range strings.Split(text, "\n") {
		switch {
		case l == "## "+title:
			in = true
			continue
		case strings.HasPrefix(l, "## "):
			in = false
		}
		if in {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

func eq(t *testing.T, name string, got, want any) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s: got %q, want %q", name, fmt.Sprint(got), fmt.Sprint(want))
		}
	})
}

func check(t *testing.T, name string, cond bool, failure string) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		if !cond {
			t.Error(failure)
		}
	})
}

func uniqSorted(in []string) []string {
	m := map[string]bool{}
	for _, s := range in {
		m[s] = true
	}
	out := make([]string, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
