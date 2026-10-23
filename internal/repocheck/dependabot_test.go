package repocheck

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// .github/dependabot.yml: version updates for every action tfpls pins (the workflows and the composite actions), weekly at a
// fixed time, minor and patch grouped, each major on its own, a cooldown, the label and the commit prefix; the pins keep the
// form Dependabot rewrites (a full SHA with the version as a comment on the same line); nothing merges a pull request by itself.
func TestDependabot(t *testing.T) {
	const D = ".github/dependabot.yml"
	raw, err := os.ReadFile(filepath.Join(root(t), D))
	var probe any
	check(t, "dependabot.yml exists and parses", err == nil && len(raw) > 0 && yaml.Unmarshal(raw, &probe) == nil,
		"dependabot.yml is missing or not valid YAML")
	doc := load(t, D)
	var ups []any
	for _, u := range list(at(doc, "updates")) {
		if str(at(u, "package-ecosystem")) == "github-actions" {
			ups = append(ups, u)
		}
	}
	eq(t, "configuration version 2", str(at(doc, "version")), "2")
	eq(t, "one github-actions entry", len(ups), 1)
	if len(ups) != 1 {
		return
	}
	u := ups[0]

	var dirs []string
	for _, d := range list(at(u, "directories")) {
		dirs = append(dirs, str(d))
	}
	eq(t, "it reads the workflows (/) and the composite actions (/actions/*)", strings.Join(uniqSortedKeep(dirs), ","), "/,/actions/*")

	// /actions/* matches one level: every composite action must sit directly below actions/
	n := len(glob(t, "actions/*/action.yml"))
	deep := 0
	_ = filepath.Walk(filepath.Join(root(t), "actions"), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(filepath.Join(root(t), "actions"), p)
		if strings.Count(rel, string(filepath.Separator)) >= 2 && regexp.MustCompile(`^action\.ya?ml$`).MatchString(info.Name()) {
			deep++
		}
		return nil
	})
	check(t, "all composite actions are directly below actions/, so /actions/* covers them", n > 0 && deep == 0,
		"composite actions: "+strconv.Itoa(n)+" at one level, "+strconv.Itoa(deep)+" deeper")

	s := at(u, "schedule")
	eq(t, "weekly at a fixed time in UTC", strings.Join([]string{str(at(s, "interval")), str(at(s, "day")), str(at(s, "time")), str(at(s, "timezone"))}, " "),
		"weekly monday 05:00 Etc/UTC")
	cd := str(at(u, "cooldown", "default-days"))
	days, convErr := strconv.Atoi(cd)
	check(t, "a cooldown of "+cd+" days (at least GitHub's default of 3, at most its limit of 90)",
		regexp.MustCompile(`^[0-9]+$`).MatchString(cd) && convErr == nil && days >= 3 && days <= 90, "cooldown default-days: '"+cd+"'")
	eq(t, "only default-days (github-actions has no semver-specific cooldown)", strings.Join(keys(at(u, "cooldown")), ","), "default-days")

	var groups []string
	var applies []string
	gm, _ := at(u, "groups").(map[string]any)
	for k, g := range gm {
		var pats, types []string
		for _, p := range list(at(g, "patterns")) {
			pats = append(pats, str(p))
		}
		for _, p := range list(at(g, "update-types")) {
			types = append(types, str(p))
		}
		by := "-"
		if v := at(g, "group-by"); v != nil && v != false {
			by = str(v)
		}
		groups = append(groups, k+"="+strings.Join(pats, "+")+"/"+strings.Join(types, "+")+"/"+by)
		applies = append(applies, str(at(g, "applies-to")))
	}
	eq(t, "groups: minor and patch together; each major on its own, one pull request per action across the directories",
		strings.Join(uniqSortedKeep(groups), " "), "actions-major=*/major/dependency-name actions-minor-patch=*/minor+patch/-")
	eq(t, "the groups are for version updates", strings.Join(uniqSorted(applies), ","), "version-updates")

	lim := str(at(u, "open-pull-requests-limit"))
	l, convErr := strconv.Atoi(lim)
	check(t, "open-pull-requests-limit "+lim+" (the group plus majors)",
		regexp.MustCompile(`^[0-9]+$`).MatchString(lim) && convErr == nil && l >= 2, "open-pull-requests-limit: '"+lim+"'")
	var labels []string
	for _, x := range list(at(u, "labels")) {
		labels = append(labels, str(x))
	}
	eq(t, "the label", strings.Join(labels, ","), "dependencies")
	eq(t, "the commit prefix", str(at(u, "commit-message", "prefix")), "deps")
}

func uniqSortedKeep(in []string) []string {
	c := append([]string(nil), in...)
	sort.Strings(c)
	return c
}

var (
	pinForm = regexp.MustCompile(`^\s*(-\s*)?uses:\s*[^.\s]\S*@`)
	pinGood = regexp.MustCompile(`@[0-9a-f]{40} # v[0-9]+\.[0-9]+\.[0-9]+\s*$`)
)

// dependabotUnfit are the lines with a third-party pin in a form Dependabot would not keep up to date.
func dependabotUnfit(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if pinForm.MatchString(l) && !strings.Contains(l, "denzhel/tfpls/") && !pinGood.MatchString(l) {
			out = append(out, l)
		}
	}
	return out
}

// the pins are in the form Dependabot keeps up to date: a full SHA and the version as a comment on the same line
func TestPinsKeepTheDependabotForm(t *testing.T) {
	var bad []string
	for _, f := range append(glob(t, ".github/workflows/*.yml"), glob(t, "actions/*/action.yml")...) {
		for _, l := range dependabotUnfit(read(t, f)) {
			bad = append(bad, f+": "+l)
		}
	}
	check(t, "every third-party pin is a SHA with a same-line # vX.Y.Z comment", len(bad) == 0,
		"pins Dependabot would not keep in this form: "+strings.Join(bad, "\n"))
	sha := strings.Repeat("b", 40)
	eq(t, "the guard accepts a SHA with its version comment (fixture)", len(dependabotUnfit("      - uses: actions/checkout@"+sha+" # v4.4.0\n")), 0)
	eq(t, "the guard finds a tag pin (fixture)", len(dependabotUnfit("      - uses: actions/checkout@v4\n")), 1)
	eq(t, "the guard finds a SHA without the comment (fixture)", len(dependabotUnfit("      - uses: actions/checkout@"+sha+"\n")), 1)
	eq(t, "the guard finds a comment on another line (fixture)", len(dependabotUnfit("      - uses: actions/checkout@"+sha+" # latest\n")), 1)
}

// no auto-merge: nothing in the workflows merges a pull request or turns auto-merge on
func TestNoWorkflowMergesByItself(t *testing.T) {
	re := regexp.MustCompile(`gh pr merge|enablePullRequestAutoMerge|auto-merge`)
	n := 0
	for _, f := range glob(t, ".github/workflows/*.yml") {
		if re.MatchString(read(t, f)) {
			n++
		}
	}
	eq(t, "no workflow merges a pull request or enables auto-merge", n, 0)
}
