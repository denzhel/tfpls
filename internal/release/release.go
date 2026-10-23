// Package release is the maintainer tooling that cuts a tfpls release (it replaces tools/release.sh) and builds the
// release assets of the binary (tools/build-release.sh). It is run through cmd/release, which is not shipped.
package release

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/denzhel/tfpls/internal/yamlconf"
)

// Env carries what Release and Build need from the outside, so tests can inject it.
type Env struct {
	Stdout, Stderr io.Writer
	Dir            string // working directory (inside the repository)
}

var releaseVer = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$`)

type runner struct{ env Env }

func (r runner) git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return strings.TrimRight(out.String(), "\n"), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// Slug turns a remote URL into owner/repo (git@github.com:o/r.git, https://github.com/o/r.git); anything else is kept as is.
func Slug(remote string) string {
	s := strings.TrimSpace(remote)
	for _, p := range []string{"git@github.com:", "https://github.com/"} {
		if strings.HasPrefix(s, p) {
			s = strings.TrimPrefix(s, p)
			break
		}
	}
	return strings.TrimSuffix(s, ".git")
}

const refTail = `/(?:actions/[a-z-]+|\.github/workflows/[a-z-]+\.yml)`

// Release cuts a release: args are [--dry-run] vX.Y.Z|vX.Y.Z-rc.N. It returns the exit code.
func Release(args []string, env Env) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(env.Stderr, format+"\n", a...)
		return 1
	}
	dry := false
	if len(args) > 0 && args[0] == "--dry-run" {
		dry = true
		args = args[1:]
	}
	ver := ""
	if len(args) > 0 {
		ver = args[0]
	}
	if len(args) > 1 || !releaseVer.MatchString(ver) {
		return fail("usage: tools/release.sh [--dry-run] vX.Y.Z | vX.Y.Z-rc.N")
	}
	major := ver[:strings.Index(ver, ".")]
	pre := strings.Contains(ver, "-rc.") // a pre-release is tested in a caller by its exact tag; callers on the major tag never see it

	r := runner{env}
	top, err := r.git(env.Dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return fail("not in a git repository: %v", err)
	}
	remote, _ := r.git(top, "remote", "get-url", "origin")
	slug := Slug(remote)
	if slug == "" {
		return fail("no origin remote to derive owner/repo from")
	}

	if !dry {
		if b, _ := r.git(top, "rev-parse", "--abbrev-ref", "HEAD"); b != "main" {
			return fail("run on main")
		}
		if st, err := r.git(top, "status", "--porcelain"); err != nil || st != "" {
			return fail("working tree is not clean")
		}
		if _, err := r.git(top, "fetch", "-q", "origin", "main"); err != nil {
			return fail("%v", err)
		}
		head, _ := r.git(top, "rev-parse", "HEAD")
		om, _ := r.git(top, "rev-parse", "origin/main")
		if head == "" || head != om {
			return fail("HEAD is not origin/main (push or pull first)")
		}
		if _, err := r.git(top, "rev-parse", "-q", "--verify", "refs/tags/"+ver); err == nil {
			return fail("tag %s already exists; exact version tags are never moved", ver)
		}
	}

	work, err := os.MkdirTemp("", "tfpls-release-")
	if err != nil {
		return fail("%v", err)
	}
	defer func() {
		_, _ = r.git(top, "worktree", "remove", "--force", work)
		_ = os.RemoveAll(work)
	}()
	if _, err := r.git(top, "worktree", "add", "-q", "--detach", work, "HEAD"); err != nil {
		return fail("%v", err)
	}

	// rewrite internal references: <slug>/actions/<name>@main and <slug>/.github/workflows/<file>@main -> @<ver>;
	// README/docs/examples that tell consumers to use @main or @v0 are left as written
	re := regexp.MustCompile(`(` + regexp.QuoteMeta(slug) + refTail + `)@main\b`)
	pinRe := regexp.MustCompile(regexp.QuoteMeta(slug) + refTail + `@` + regexp.QuoteMeta(ver) + `\b`)
	var files []string
	for _, root := range []string{".github/workflows", "actions"} {
		_ = filepath.WalkDir(filepath.Join(work, root), func(p string, d os.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return nil
			}
			if b, e := os.ReadFile(p); e == nil && re.Match(b) {
				files = append(files, p)
			}
			return nil
		})
	}
	if len(files) == 0 {
		return fail("no internal @main references found")
	}
	pinned := 0
	for _, p := range files {
		b, _ := os.ReadFile(p)
		info, err := os.Stat(p)
		if err != nil {
			return fail("%v", err)
		}
		nb := re.ReplaceAll(b, []byte("${1}@"+ver))
		if err := os.WriteFile(p, nb, info.Mode().Perm()); err != nil {
			return fail("%v", err)
		}
		var left []string
		for i, l := range strings.Split(string(nb), "\n") {
			if re.MatchString(l) {
				left = append(left, fmt.Sprintf("%s:%d:%s", p, i+1, l))
			}
		}
		if len(left) > 0 {
			return fail("internal @main references remain:\n%s", strings.Join(left, "\n"))
		}
		pinned += len(pinRe.FindAll(nb, -1))
		if ext := filepath.Ext(p); ext == ".yml" || ext == ".yaml" {
			if _, err := yamlconf.Parse(nb); err != nil {
				return fail("invalid YAML after rewrite: %s: %v", p, err)
			}
		}
	}

	// the binary the actions install: the job downloads exactly this release (on main the file says `dev`: built from source)
	if err := os.WriteFile(filepath.Join(work, "actions/bin/VERSION"), []byte(ver+"\n"), 0o644); err != nil {
		return fail("%v", err)
	}

	fmt.Fprintf(env.Stdout, "release %s: %d internal references now point at @%s in %d files\n", ver, pinned, ver, len(files))
	fmt.Fprintf(env.Stdout, "actions/bin/VERSION: %s\n", ver)
	if pre {
		fmt.Fprintf(env.Stdout, "pre-release: %s stays where it is\n", major)
	}
	if dry {
		stat, _ := r.git(work, "diff", "--stat")
		if i := strings.LastIndex(stat, "\n"); i >= 0 {
			stat = stat[i+1:]
		}
		fmt.Fprintln(env.Stdout, stat)
		fmt.Fprintln(env.Stdout, "dry run: nothing committed, tagged or pushed")
		return 0
	}

	steps := [][]string{
		{"-c", "user.name=tfpls release", "-c", "user.email=noreply@users.noreply.github.com", "commit", "-qam", fmt.Sprintf("release %s: internal references pinned to %s", ver, ver)},
		{"tag", "-a", ver, "-m", ver},
	}
	if !pre {
		steps = append(steps, []string{"tag", "-f", "-a", major, "-m", fmt.Sprintf("%s (moves to the latest %s.x.y release)", major, major), ver + "^{commit}"})
	}
	steps = append(steps, []string{"push", "origin", "refs/tags/" + ver})
	if !pre {
		steps = append(steps, []string{"push", "--force", "origin", "refs/tags/" + major})
	}
	for _, s := range steps {
		if _, err := r.git(work, s...); err != nil {
			return fail("%v", err)
		}
	}
	if pre {
		fmt.Fprintf(env.Stdout, "published %s (pre-release: %s not moved)\n", ver, major)
	} else {
		fmt.Fprintf(env.Stdout, "published %s and moved %s\n", ver, major)
	}
	return 0
}
