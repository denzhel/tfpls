package repocheck

import (
	"bytes"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The reference must name every setting the config validator knows, and its documented defaults must equal the entry workflow's.
func TestReferenceMatchesConfigAndEntryWorkflow(t *testing.T) {
	ref := read(t, "docs/reference.md")
	quoted := regexp.MustCompile(`"[a-z_]+"`)
	decl := regexp.MustCompile(`^var (settings|stackKeys) = `)
	var found []string
	for _, l := range strings.Split(read(t, "internal/detect/validate.go"), "\n") {
		if decl.MatchString(l) {
			for _, q := range quoted.FindAllString(l, -1) {
				found = append(found, strings.Trim(q, `"`))
			}
		}
	}
	found = uniqSorted(found)
	check(t, "found settings in internal/detect/validate.go", len(found) > 0, "no settings found in internal/detect/validate.go")
	for _, k := range found {
		check(t, "reference names "+k, strings.Contains(ref, "`"+k+"`"), "reference does not name the setting "+k)
	}

	wf := load(t, ".github/workflows/tofu.yml")
	inputs := at(wf, "on", "workflow_call", "inputs")
	wfDefault := func(i string) string { return str(at(inputs, i, "default")) }
	// the second cell of the first table row that starts with the input's name, without backticks and spaces
	cell := func(text, i string) string {
		for _, l := range strings.Split(text, "\n") {
			if strings.HasPrefix(l, "| `"+i+"`") {
				p := strings.Split(l, "|")
				if len(p) > 2 {
					return strings.NewReplacer("`", "", " ", "").Replace(p[2])
				}
				return ""
			}
		}
		return ""
	}
	for _, i := range []string{"audit", "drift_issues", "runs_on", "queue"} {
		w, r := wfDefault(i), cell(ref, i)
		check(t, "reference default of "+i+" is "+w, w == r, "reference default of "+i+" is '"+r+"', the entry workflow has '"+w+"'")
	}

	// the reference's "Workflow inputs" table is exactly the inputs of tofu.yml, with the same defaults (both ways)
	sec := section(ref, "Workflow inputs")
	row := regexp.MustCompile("^\\| `([a-z_]+)` \\|")
	var tbl []string
	for _, l := range strings.Split(sec, "\n") {
		if m := row.FindStringSubmatch(l); m != nil {
			tbl = append(tbl, m[1])
		}
	}
	sort.Strings(tbl)
	wfKeys := keys(inputs)
	check(t, "reference inputs table == tofu.yml inputs", strings.Join(tbl, " ") == strings.Join(wfKeys, " "),
		"reference inputs ["+strings.Join(tbl, " ")+"] differ from tofu.yml ["+strings.Join(wfKeys, " ")+"]")
	for _, i := range wfKeys {
		w := wfDefault(i)
		if w == "" {
			w = "none"
			if i == "tofu_version" {
				w = "detected"
			}
		}
		r := cell(sec, i)
		check(t, "reference default of "+i+" is "+w+" (table)", w == r, "reference default of "+i+" is '"+r+"', tofu.yml has '"+w+"'")
	}
}

var mdLink = regexp.MustCompile(`\]\([^)]+\)`)

// mdTargets are the link targets of a Markdown text that are relative file paths (no URL, no bare anchor), anchors stripped.
func mdTargets(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		for _, m := range mdLink.FindAllString(l, -1) {
			link := strings.TrimSuffix(strings.TrimPrefix(m, "]("), ")")
			if strings.HasPrefix(link, "`") {
				continue
			}
			tgt, _, _ := strings.Cut(link, "#")
			if tgt == "" || strings.HasPrefix(tgt, "http://") || strings.HasPrefix(tgt, "https://") || strings.HasPrefix(tgt, "mailto:") {
				continue
			}
			out = append(out, link)
		}
	}
	return out
}

// Every relative Markdown link in README, CONTRIBUTING, SECURITY, CHANGELOG and docs/ must point at a file that exists.
func TestDocsRelativeLinksResolve(t *testing.T) {
	files := append([]string{"README.md", "CONTRIBUTING.md", "SECURITY.md", "CHANGELOG.md"}, glob(t, "docs/*.md")...)
	pass := 0
	for _, f := range files {
		for _, link := range mdTargets(read(t, f)) {
			tgt, _, _ := strings.Cut(link, "#")
			if _, err := os.Stat(filepath.Join(root(t), path.Dir(f), tgt)); err == nil {
				pass++
			} else {
				t.Errorf("%s links to missing %s", f, link)
			}
		}
	}
	check(t, "relative links resolve", pass > 0, "no relative link found")
	t.Logf("%d relative links resolve", pass)

	// the README opens with the hero image, and the file is a self-contained SVG (no script, no external reference)
	svg := "docs/assets/tfpls-hero.svg"
	svgText := read(t, svg)
	check(t, "the README hero image exists and is self-contained",
		strings.Contains(read(t, "README.md"), svg) && len(svgText) > 0 && !svgExternal(svgText),
		"the README hero image is missing or not self-contained")
}

var svgExt = regexp.MustCompile(`(?i)<script|@import|(href|src)=|url\(["']?https?:`)

func svgExternal(s string) bool { return svgExt.MatchString(s) }

func TestDocsLinkGuardsFindProblems(t *testing.T) {
	eq(t, "relative links are found, anchors cut, URLs skipped (fixture)",
		strings.Join(mdTargets("[a](x.md#y) [b](https://h) [c](#top) [d](mailto:a@b) [e](`code`) [f](dir/z.md)"), ","), "x.md#y,dir/z.md")
	check(t, "the svg guard finds a script (fixture)", svgExternal(`<svg><script>x</script></svg>`), "script not found")
	check(t, "the svg guard finds an external image (fixture)", svgExternal(`<svg><image href="x.png"/></svg>`), "href not found")
	check(t, "the svg guard finds an external url() (fixture)", svgExternal(`<svg style="fill:url('https://x/y')"/>`), "url() not found")
	check(t, "the svg guard accepts a local gradient (fixture)", !svgExternal(`<svg><rect fill="url(#g)"/></svg>`), "local url wrongly found")
}

// The troubleshooting and background sections that the simulation findings asked for exist and point at each other.
func TestRunbookEntries(t *testing.T) {
	for _, c := range []struct{ name, file, want string }{
		{"credentials explains that the plan role needs more than Describe*", "docs/credentials.md", "### Resource read is more than `Describe*`"},
		{"credentials names the example action", "docs/credentials.md", "ec2:GetManagedPrefixListEntries"},
		{"the runbook has the 403-on-refresh entry", "docs/runbooks.md", "## Every plan of a workspace fails with a 403 on refresh"},
		{"the runbook links to the credentials section", "docs/runbooks.md", "credentials.md#resource-read-is-more-than-describe"},
		{"credentials links to the runbook entry", "docs/credentials.md", "runbooks.md#every-plan-of-a-workspace-fails-with-a-403-on-refresh"},

		{"reviewed-plans explains what a plan contains", "docs/reviewed-plans.md", "## What a plan contains"},
		{"the runbook has the entry for a plan bigger than the diff", "docs/runbooks.md", "## The plan shows resources my pull request did not change"},
		{"the runbook links to the reviewed-plans section", "docs/runbooks.md", "reviewed-plans.md#what-a-plan-contains"},
		{"reviewed-plans links to the runbook entry", "docs/reviewed-plans.md", "runbooks.md#the-plan-shows-resources-my-pull-request-did-not-change"},
		{"the auto_apply reference links to it", "docs/reference.md", "reviewed-plans.md#what-a-plan-contains"},

		{"the runbook explains the apply-before-merge window", "docs/runbooks.md", "## Apply before merge: the window"},
		{"reviewed-plans links to the apply-before-merge window", "docs/reviewed-plans.md", "runbooks.md#apply-before-merge-the-window"},
		{"the runbook gives the safe order", "docs/runbooks.md", "Apply, then merge right away"},

		{"runner-minutes explains the ubuntu-latest move to 26.04", "docs/runner-minutes.md", "## The `ubuntu-latest` label moves to Ubuntu 26.04"},
		{"runner-minutes says how to pin the old image", "docs/runner-minutes.md", "runs_on: ubuntu-24.04"},
	} {
		check(t, c.name, strings.Contains(read(t, c.file), c.want), c.file+" lacks: "+c.want)
	}
}

// slug is GitHub's anchor for a heading: lower case, punctuation dropped, spaces become hyphens.
func slug(h string) string {
	return strings.ReplaceAll(regexp.MustCompile(`[^a-z0-9 _-]`).ReplaceAllString(strings.ToLower(h), ""), " ", "-")
}

// docs/security-model.md has the "Automated scanning" section, SECURITY.md and the docs index link to it (the anchor matches the
// heading), and the section answers what was asked: what runs when, why on every pull request and every week, how it works and
// what is AI in it, what it costs (also for private repositories), and what a person still does.
func TestSecurityModelAutomatedScanning(t *testing.T) {
	const doc = "docs/security-model.md"
	text := read(t, doc)
	heading := ""
	for _, l := range strings.Split(text, "\n") {
		if l == "## Automated scanning" {
			heading = l
			break
		}
	}
	check(t, doc+" has the section '## Automated scanning'", heading != "", doc+" has no section '## Automated scanning'")
	eq(t, "the anchor of the section", slug(strings.TrimPrefix(heading, "## ")), "automated-scanning")
	check(t, "SECURITY.md links to "+doc+"#automated-scanning", strings.Contains(read(t, "SECURITY.md"), "](docs/security-model.md#automated-scanning)"),
		"SECURITY.md does not link to "+doc+"#automated-scanning")
	check(t, "the docs index links to the section", strings.Contains(read(t, "docs/README.md"), "](security-model.md#automated-scanning)"),
		"docs/README.md does not link to security-model.md#automated-scanning")

	sec := section(text, "Automated scanning")
	// one sub-heading per question
	for _, h := range []string{"What runs and when", "Why on every pull request and every week", "How it works and what is AI", "What it costs", "What a person still does"} {
		check(t, "the section answers: "+h, strings.Contains("\n"+sec, "\n### "+h+"\n"), "the section has no sub-heading '### "+h+"'")
	}
	// the scanners turned on for this repository, and the AI parts, by name
	for _, s := range []string{"CodeQL", "Copilot Autofix", "Secret scanning", "Push protection", "Dependabot alerts", "Dependabot security updates", "Private vulnerability reporting", "AI Scan"} {
		check(t, "the section names "+s, strings.Contains(sec, s), "the section does not name "+s)
	}
	// the claims are sourced: the section cites GitHub's documentation
	var cites []string
	re := regexp.MustCompile(`\(https://docs\.github\.com/[^)]+\)`)
	for _, l := range strings.Split(sec, "\n") {
		cites = append(cites, re.FindAllString(l, -1)...)
	}
	n := len(uniqSorted(cites))
	check(t, "the section cites pages of GitHub's documentation", n >= 3, "the section cites fewer than 3 pages of GitHub's documentation")
}

// The README stays short and keeps its opening: the hero block and the first paragraph are byte for byte those of main, the file
// is under 60 lines, and the quick start is the onboarding one-liner.
const readmeOpening = `<p align="center">
  <img src="docs/assets/tfpls-hero.svg" alt="tfpls: a git diff that removes hope and adds a plan someone read. Commit message: grow up. TF, please." width="720">
</p>

**Plan on every pull request. Apply with one comment.** Revolutionary, we know: you look at the plan *before* you apply it. tfpls is OpenTofu on GitHub Actions for AWS and S3 state: reusable workflows that run in your repository, on your Actions minutes. Nothing to host, no server of ours in the path.
`

// headLines is `head -n n`: the first n lines of s with their newlines.
func headLines(s string, n int) string {
	parts := strings.SplitAfter(s, "\n")
	if len(parts) > n {
		parts = parts[:n]
	}
	return strings.Join(parts, "")
}

func TestReadme(t *testing.T) {
	readme := read(t, "README.md")
	n := strings.Count(readmeOpening, "\n")
	// the opening on main (hero block, blank line, first paragraph); it changes only on the owner's word, then here too
	check(t, "the hero block and the first paragraph are unchanged, byte for byte", headLines(readme, n) == readmeOpening,
		"the hero block or the first paragraph changed:\n"+headLines(readme, n))
	check(t, "the opening guard finds a changed paragraph (fixture)", headLines(strings.Replace(readme, "Revolutionary", "Boring", 1), n) != readmeOpening,
		"a changed opening was not found")

	// the same against main itself where the history is at hand (a clone; CI checks out a single commit of a pull request)
	git := func(args ...string) (string, error) {
		var out bytes.Buffer
		c := exec.Command("git", append([]string{"-C", root(t)}, args...)...)
		c.Stdout = &out
		err := c.Run()
		return out.String(), err
	}
	if _, err := git("rev-parse", "--verify", "-q", "origin/main"); err == nil {
		if m, err := git("show", "origin/main:README.md"); err == nil {
			check(t, "the opening is byte-identical to the README of origin/main", headLines(m, n) == headLines(readme, n),
				"the opening differs from the README of origin/main")
		}
	} else {
		t.Log("origin/main is not in this checkout: compared with the copy above only")
	}

	lines := strings.Count(readme, "\n")
	check(t, "the README is under 60 lines", lines < 60, "the README has "+strconv.Itoa(lines)+" lines, the limit is 59")

	// one way in: the onboarding one-liner (what onboarding does in detail is in docs/getting-started.md)
	info, err := os.Stat(filepath.Join(root(t), "tools/onboard"))
	check(t, "the quick start is the onboarding one-liner, and tools/onboard exists",
		strings.Contains(readme, "curl -fsSL https://raw.githubusercontent.com/denzhel/tfpls/v0/tools/onboard | bash") && err == nil && info.Mode()&0o111 != 0,
		"the quick start lacks the onboarding one-liner, or tools/onboard is missing")
}
