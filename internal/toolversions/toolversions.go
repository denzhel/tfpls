// Package toolversions is the weekly watch of the default tool versions (it replaces tools/tool-versions.sh): it compares
// the OpenTofu and conftest defaults (internal/setup/setup.go) and the actionlint version CI installs
// (.github/workflows/ci.yml) with the highest published GitHub release of each, and keeps exactly one open issue titled
// "Tool versions behind": opened when a default is behind, edited when the list changes, closed with a comment when every
// default is current. It only reads files; bumping a default stays a reviewed pull request. Run through cmd/toolversions
// (not part of the shipped binary).
package toolversions

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/denzhel/tfpls/internal/gh"
)

const (
	issueTitle = "Tool versions behind"
	label      = "dependencies"
)

// Env carries what Run needs from the outside, so tests can inject it.
type Env struct {
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	GH             gh.Client
	Sleep          func(time.Duration) // default time.Sleep
}

var (
	semver     = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	reTofu     = regexp.MustCompile(`^[ \t]*DefaultTofu[ \t]*=[ \t]*"([0-9.]+)"`)
	reConftest = regexp.MustCompile(`^[ \t]*DefaultConftest[ \t]*=[ \t]*"([0-9.]+)"`)
	reActionl  = regexp.MustCompile(`^[ \t]*v=([0-9.]+); sum=[0-9a-f]{64}`)
)

type failure string

func (f failure) Error() string { return string(f) }

// defaultVersion returns the first version the pattern finds in the file, which must be X.Y.Z.
func defaultVersion(root, file string, re *regexp.Regexp) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, file))
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if m := re.FindStringSubmatch(line); m != nil {
				if semver.MatchString(m[1]) {
					return m[1], nil
				}
				break
			}
		}
	}
	return "", failure("cannot read the default version from " + file)
}

func parts(v string) [3]int {
	var p [3]int
	for i, s := range strings.Split(v, ".") {
		p[i], _ = strconv.Atoi(s)
	}
	return p
}

// Newer reports whether version b is higher than version a (both X.Y.Z, compared numerically).
func Newer(a, b string) bool {
	pa, pb := parts(a), parts(b)
	for i := range pa {
		if pb[i] != pa[i] {
			return pb[i] > pa[i]
		}
	}
	return false
}

type release struct {
	TagName      string `json:"tagName"`
	IsDraft      bool   `json:"isDraft"`
	IsPrerelease bool   `json:"isPrerelease"`
	URL          string `json:"url"`
}

// Latest picks the highest published X.Y.Z release (drafts, pre-releases and other tags are skipped) from a GraphQL
// answer: the highest version, not the newest by date, since a backport of an older line can be released later.
func Latest(graphql []byte) (version, url string, err error) {
	var doc struct {
		Data struct {
			Repository struct {
				Releases struct {
					Nodes []release `json:"nodes"`
				} `json:"releases"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(graphql, &doc); err != nil {
		return "", "", err
	}
	for _, r := range doc.Data.Repository.Releases.Nodes {
		v := strings.TrimPrefix(r.TagName, "v")
		if r.IsDraft || r.IsPrerelease || !semver.MatchString(v) {
			continue
		}
		if version == "" || Newer(version, v) {
			version, url = v, r.URL
		}
	}
	if version == "" {
		return "", "", fmt.Errorf("no published release")
	}
	return version, url, nil
}

// The REST list of 100 releases carries every release text and can time out (HTTP 504): GraphQL with four fields.
const query = `query($owner: String!, $name: String!) {
    repository(owner: $owner, name: $name) { releases(first: 100, orderBy: {field: CREATED_AT, direction: DESC}) {
      nodes { tagName isDraft isPrerelease url } } } }`

type checker struct {
	env  Env
	rows string
	list []string
}

// api runs gh with three attempts and a growing pause (a 502/504 from GitHub is usually gone seconds later).
func (c *checker) api(args ...string) (string, error) {
	delay := 5
	if s := c.env.Getenv("TFPLS_RETRY_DELAY"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			delay = n
		}
	}
	var err error
	for n := 1; n <= 3; n++ {
		var out string
		if out, err = c.env.GH.Run(args...); err == nil {
			return out, nil
		}
		if n < 3 {
			c.env.Sleep(time.Duration(delay*n) * time.Second)
		}
	}
	return "", err
}

func (c *checker) check(tool, cur, src, where string) error {
	owner, name, _ := strings.Cut(src, "/")
	out, err := c.api("api", "graphql", "-f", "owner="+owner, "-f", "name="+name, "-f", "query="+query)
	if err != nil {
		return failure("cannot list the releases of " + src)
	}
	ver, url, err := Latest([]byte(out))
	if err != nil {
		return failure("no published release of " + src)
	}
	if Newer(cur, ver) {
		c.rows += fmt.Sprintf("| %s | %s | [%s](%s) | %s |\n", tool, cur, ver, url, where)
		fmt.Fprintf(c.env.Stdout, "%s: default %s, latest %s (behind)\n", tool, cur, ver)
	} else {
		fmt.Fprintf(c.env.Stdout, "%s: default %s, latest %s (current)\n", tool, cur, ver)
	}
	c.list = append(c.list, tool+" "+cur)
	return nil
}

// Run checks the defaults and keeps the issue. It returns the exit code: 1 when anything cannot be read, so a broken
// check is never reported as "current".
func Run(env Env) int {
	if env.Sleep == nil {
		env.Sleep = time.Sleep
	}
	if err := run(env); err != nil {
		fmt.Fprintf(env.Stderr, "::error::tool-versions: %s\n", err)
		return 1
	}
	return 0
}

func run(env Env) error {
	root := env.Getenv("TFPLS_ROOT")
	if root == "" {
		root = "."
	}
	repo := env.Getenv("REPO")
	if repo == "" {
		repo = env.Getenv("GITHUB_REPOSITORY")
	}
	if repo == "" {
		return failure("REPO (owner/repo) is not set")
	}
	tofu, err := defaultVersion(root, "internal/setup/setup.go", reTofu)
	if err != nil {
		return err
	}
	conftest, err := defaultVersion(root, "internal/setup/setup.go", reConftest)
	if err != nil {
		return err
	}
	actionlint, err := defaultVersion(root, ".github/workflows/ci.yml", reActionl)
	if err != nil {
		return err
	}
	c := &checker{env: env}
	for _, t := range []struct{ tool, cur, src, where string }{
		{"OpenTofu", tofu, "opentofu/opentofu", "`internal/setup/setup.go` (`DefaultTofu`)"},
		{"conftest", conftest, "open-policy-agent/conftest", "`internal/setup/setup.go` (`DefaultConftest`)"},
		{"actionlint", actionlint, "rhysd/actionlint", "`.github/workflows/ci.yml` (`v=` and its `sum=`)"},
	} {
		if err := c.check(t.tool, t.cur, t.src, t.where); err != nil {
			return err
		}
	}

	// the open issue with exactly this title (the oldest, should a second one ever exist)
	var issues []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
	}
	if err := env.GH.JSON(&issues, "issue", "list", "--repo", repo, "--state", "open", "--limit", "500", "--json", "number,title,body"); err != nil {
		return failure("cannot list the open issues of " + repo)
	}
	sort.SliceStable(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	num, old := 0, ""
	for _, is := range issues {
		if is.Title == issueTitle {
			num, old = is.Number, strings.TrimRight(strings.ReplaceAll(is.Body, "\r", ""), "\n")
			break
		}
	}

	if c.rows == "" {
		if num == 0 {
			fmt.Fprintln(env.Stdout, "all current; no issue to keep")
			return nil
		}
		comment := "Every default is current: " + strings.Join(c.list, ", ")
		if u := env.Getenv("RUN_URL"); u != "" {
			comment += " ([check](" + u + "))"
		}
		if _, err := env.GH.Run("issue", "close", strconv.Itoa(num), "--repo", repo, "--comment", comment+"."); err != nil {
			return failure(fmt.Sprintf("cannot close issue #%d", num))
		}
		fmt.Fprintf(env.Stdout, "all current; closed issue #%d\n", num)
		return nil
	}

	// the body depends only on the list, so an unchanged list leaves the issue alone
	body := "The default version of these tools is behind their latest release:\n\n" +
		"| Tool | Default | Latest release | Where the default is |\n|---|---|---|---|\n" + c.rows + "\n" +
		"To update one: change the default where the table says and every other place that names the old version (`git grep -n <old version>`: the CI check of the installed versions, input descriptions, docs). For actionlint, `sum=` is the SHA-256 of `actionlint_<version>_linux_amd64.tar.gz` from the release's checksums file. Add a line to `CHANGELOG.md`; CI runs the new versions.\n\n" +
		"Kept up to date by the weekly `tool-versions` workflow ([docs/dependencies.md](https://github.com/" + repo + "/blob/main/docs/dependencies.md#tool-versions)): edited when the list changes, closed when every default is current."
	if p := env.Getenv("GITHUB_STEP_SUMMARY"); p != "" {
		if err := appendSummary(p, c.rows); err != nil {
			return failure("cannot write the step summary: " + err.Error())
		}
	}

	switch {
	case num == 0:
		_, _ = env.GH.Run("label", "create", label, "--repo", repo, "--color", "0366d6", "--description", "Pull requests that update a dependency file")
		out, err := env.GH.Run("issue", "create", "--repo", repo, "--title", issueTitle, "--label", label, "--body", body)
		if err != nil {
			return failure("cannot open the issue")
		}
		fmt.Fprintf(env.Stdout, "opened %s\n", strings.TrimSpace(out))
	case old != body:
		if _, err := env.GH.Run("issue", "edit", strconv.Itoa(num), "--repo", repo, "--body", body); err != nil {
			return failure(fmt.Sprintf("cannot edit issue #%d", num))
		}
		fmt.Fprintf(env.Stdout, "updated issue #%d (the list changed)\n", num)
	default:
		fmt.Fprintf(env.Stdout, "issue #%d unchanged (same list)\n", num)
	}
	return nil
}

func appendSummary(path, rows string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	_, werr := fmt.Fprintf(f, "### Tool versions behind\n\n| Tool | Default | Latest release | Where the default is |\n|---|---|---|---|\n%s\n", rows)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}
