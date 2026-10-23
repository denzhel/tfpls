package detect

import "testing"

func TestGlobMatch(t *testing.T) {
	for _, c := range []struct {
		g, p string
		want bool
	}{
		{"**/*.md", "README.md", true},
		{"**/*.md", "docs/deep/a.md", true},
		{"**/*.md", "a.mdx", false},
		{"docs/**", "docs/a/b.md", true},
		{"docs/**", "other/docs/a.md", false},
		{"workspaces/*", "workspaces/dev", true},
		{"workspaces/*", "workspaces/dev/x.tf", false},
		{"a?c", "abc", true},
		{"a?c", "a/c", false},
		{"LICENSE*", "LICENSE.txt", true},
		{"a.b", "axb", false},       // the dot is literal
		{"a+b", "aab", false},       // so is every regexp character
		{"(x)", "(x)", true},        // ... and parentheses
		{"dev-a", "dev-a\n", false}, // BUG (the old shell version): `$` matched before a trailing newline; the whole string must match
		{"dev-a", "dev-a\nb", false},
	} {
		if got := GlobMatch(c.g, c.p); got != c.want {
			t.Errorf("GlobMatch(%q, %q) = %v, want %v", c.g, c.p, got, c.want)
		}
	}
}
