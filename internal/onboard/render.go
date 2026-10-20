package onboard

import (
	"fmt"
	"strings"
)

func (s *state) renderWorkflow() string {
	var b strings.Builder
	fmt.Fprintf(&b, `name: tofu
on:
  pull_request:
  issue_comment: { types: [created] }
  push: { branches: [%s] }
  schedule: [{ cron: "17 5 * * *" }]
  workflow_dispatch:
    inputs:
      task:
        description: What to run
        type: choice
        options: [drift, locks, dashboard, fix]
      workspaces: { description: "fix: workspace names to fix (a drifted workspace); locks: workspace names whose lock is released", type: string }
      step: { description: "fix: plan, apply or fix (plan and apply in one run)", type: choice, options: [fix, plan, apply] }
permissions: { contents: write, pull-requests: write, issues: write, id-token: write, actions: read }
jobs:
  tofu:
    uses: %s/.github/workflows/tofu.yml@%s
`, s.branch, s.slug, s.ref)
	switch {
	case s.runsOn == "ubuntu-latest":
	case strings.HasPrefix(s.runsOn, "["):
		fmt.Fprintf(&b, "    with: { runs_on: '%s' }\n", s.runsOn)
	default:
		fmt.Fprintf(&b, "    with: { runs_on: %s }\n", s.runsOn)
	}
	b.WriteString("    secrets: inherit\n")
	return b.String()
}

// renderConfig also sets cfgActive (it describes a layout) and cfgWhat (one line about it).
func (s *state) renderConfig() string {
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	s.cfgActive, s.cfgWhat = false, "comments only"
	w("# tfpls config: https://github.com/%s", s.slug)
	switch s.layout {
	case "varfile":
		vfile := s.vardir + "/{workspace}.tfvars"
		if s.vardir == "." {
			vfile = "{workspace}.tfvars"
		}
		w("# One root module, one var file per workspace (%s).", vfile)
		w("stacks:")
		w("  - name: app")
		w("    dir: .")
		w("    workspaces: [%s]", strings.Join(s.varfiles, ", "))
		w("    var_file: \"%s\"", vfile)
		w("    workspace_paths: [\"%s\"]   # a change to one var file plans only that workspace", vfile)
		s.cfgActive, s.cfgWhat = true, "one stack app with the workspaces "+strings.Join(s.varfiles, ", ")
	case "envs", "root":
		if s.rootStack {
			ign := `"**/*.md", "docs/**", ".github/**", "LICENSE*", ".gitignore"`
			for _, d := range s.envdirs {
				ign += fmt.Sprintf(", \"%s/**\"", d)
			}
			w("# The root module in the repository root, with a single state: the tofu workspace default.")
			w("stacks:")
			w("  - name: %s", s.stackName)
			w("    dir: .")
			w("    workspaces: [default]")
			w("    ignore: [%s]   # changes here plan nothing", ign)
			s.cfgWhat = "one stack " + s.stackName + " in ., workspace default"
		}
		if s.layout == "envs" {
			w("# Separate root modules, one directory each; the tofu workspace is default. watch: modules a stack uses.")
			if !s.rootStack {
				w("stacks:")
			}
			for _, d := range s.envdirs {
				n := strings.ReplaceAll(d, "/", "-")
				if m := s.moduleWatch(d); len(m) > 0 {
					w("  - { name: %s, dir: %s, workspaces: [default], watch: [%s] }", n, d, strings.Join(m, ", "))
				} else {
					w("  - { name: %s, dir: %s, workspaces: [default] }", n, d)
				}
			}
			if s.rootStack {
				s.cfgWhat = fmt.Sprintf("%s, plus %d stack(s) on the default workspace", s.cfgWhat, len(s.envdirs))
			} else {
				s.cfgWhat = fmt.Sprintf("%d stack(s) on the default workspace: %s", len(s.envdirs), strings.Join(s.envdirs, ", "))
			}
		}
		s.cfgActive = true
	default:
		w("# Nothing found to run. Describe your layout, for example:")
		w("# stacks:")
		w("#   - { name: dev, dir: envs/dev, workspaces: [default], watch: [modules] }")
	}
	if s.layout == "root" {
		w("# Settings (they match a stack or workspace name, * allowed), for example:")
		w("# overrides:")
		w("#   %s: { drift_fix: false }   # drift is fixed through a pull request only", s.stackName)
	} else {
		w("# Examples (settings match a workspace name or a stack name, * allowed):")
		w("# overrides:")
		w(`#   "prod*": { drift_fix: false }   # production drift is fixed through a pull request only`)
		w(`#   "dev*":  { auto_apply: always }`)
	}
	return b.String()
}

// ---- unified diff -----------------------------------------------------------------------------------------------------

type dline struct {
	text  string
	noEOL bool
}

func splitLines(content string) []dline {
	if content == "" {
		return nil
	}
	noEOL := !strings.HasSuffix(content, "\n")
	ls := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	out := make([]dline, len(ls))
	for i, l := range ls {
		out[i] = dline{l, false}
	}
	out[len(out)-1].noEOL = noEOL
	return out
}

type dop struct {
	kind byte // ' ', '-', '+'
	l    dline
}

// unifiedDiff is `diff -u -L oldLabel -L newLabel` with 3 lines of context (empty when the contents are equal).
func unifiedDiff(oldLabel, newLabel, oldC, newC string) []string {
	a, b := splitLines(oldC), splitLines(newC)
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var ops []dop
	for i, j := 0, 0; i < n || j < m; {
		switch {
		case i < n && j < m && a[i] == b[j]:
			ops = append(ops, dop{' ', a[i]})
			i++
			j++
		case i < n && (j >= m || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, dop{'-', a[i]})
			i++
		default:
			ops = append(ops, dop{'+', b[j]})
			j++
		}
	}
	const ctx = 3
	var changes []int
	for i, o := range ops {
		if o.kind != ' ' {
			changes = append(changes, i)
		}
	}
	if len(changes) == 0 {
		return nil
	}
	out := []string{"--- " + oldLabel, "+++ " + newLabel}
	for k := 0; k < len(changes); {
		start := max(changes[k]-ctx, 0)
		end := changes[k]
		for k < len(changes) && changes[k]-end <= 2*ctx+1 {
			end = changes[k]
			k++
		}
		end = min(end+ctx, len(ops)-1)
		// line numbers at the start of the hunk
		oi, ni := 1, 1
		for _, o := range ops[:start] {
			if o.kind != '+' {
				oi++
			}
			if o.kind != '-' {
				ni++
			}
		}
		oc, nc := 0, 0
		var body []string
		for _, o := range ops[start : end+1] {
			if o.kind != '+' {
				oc++
			}
			if o.kind != '-' {
				nc++
			}
			body = append(body, string(o.kind)+o.l.text)
			if o.l.noEOL {
				body = append(body, `\ No newline at end of file`)
			}
		}
		rng := func(start, count int) string {
			switch count {
			case 1:
				return fmt.Sprint(start)
			case 0:
				return fmt.Sprintf("%d,0", start-1)
			}
			return fmt.Sprintf("%d,%d", start, count)
		}
		out = append(out, fmt.Sprintf("@@ -%s +%s @@", rng(oi, oc), rng(ni, nc)))
		out = append(out, body...)
	}
	return out
}

// showDiff shows a unified diff, coloured on a terminal, at most 60 lines.
func (s *state) showDiff(file, oldContent string, exists bool, newContent string) {
	var d []string
	if exists {
		d = unifiedDiff(file+" (yours)", file+" (recommended)", oldContent, newContent)
	} else {
		d = unifiedDiff("/dev/null", file+" (new)", "", newContent)
	}
	for i, l := range d {
		if i >= 60 {
			break
		}
		c := ""
		switch {
		case strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---"):
			c = s.B
		case strings.HasPrefix(l, "+"):
			c = s.G
		case strings.HasPrefix(l, "-"):
			c = s.RD
		case strings.HasPrefix(l, "@@"):
			c = s.C
		default:
			s.p("    %s\n", l)
			continue
		}
		s.p("    %s%s%s\n", c, l, s.R)
	}
	if len(d) > 60 {
		s.note(fmt.Sprintf("... %d more line(s)", len(d)-60))
	}
}
