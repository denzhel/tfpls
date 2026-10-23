package repocheck

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// Secrets reach a job only by name. A computed lookup (`secrets[<expression>]`) or the whole context (`toJSON(secrets)`) makes
// GitHub hand EVERY secret the workflow can see to the runner, and with `secrets: inherit` that is every repository and
// organisation secret of the caller (CodeQL actions/excessive-secrets-exposure). Every reusable workflow declares exactly the
// secrets it names under on.workflow_call.secrets (tofu.yml: those of the workflows it calls), so a caller can list them.

var (
	commentLine = regexp.MustCompile(`^\s*#`)
	ifCond      = regexp.MustCompile(`^\s*(-\s+)?if:\s*`)
	exprRe      = regexp.MustCompile(`\$\{\{(?:[^}]|\}[^}])*\}\}`)
	wholeRe     = regexp.MustCompile(`(?i)(?:^|[^.\w-])secrets(?:[^.\w-]|$)`)
	namedRe     = regexp.MustCompile(`(?i)(?:^|[^.\w-])secrets\.(\w+)`)
)

// exprs are the expressions of a workflow or action file as "<line>:<text>": every ${{ ... }} and every bare `if:` condition
// (comment lines are skipped).
func exprs(text string) []string {
	var out []string
	for i, l := range strings.Split(text, "\n") {
		if commentLine.MatchString(l) {
			continue
		}
		if m := ifCond.FindString(l); m != "" {
			out = append(out, fmt.Sprintf("%d:%s", i+1, l[len(m):]))
		}
		for _, e := range exprRe.FindAllString(l, -1) {
			out = append(out, fmt.Sprintf("%d:%s", i+1, e))
		}
	}
	return out
}

// whole are the expressions that use the secrets context as anything but secrets.NAME.
func whole(es []string) []string {
	var out []string
	for _, e := range es {
		if wholeRe.MatchString(e) {
			out = append(out, e)
		}
	}
	return out
}

func lineNos(es []string) string {
	var b strings.Builder
	for _, e := range es {
		b.WriteString(e[:strings.Index(e, ":")] + " ")
	}
	return b.String()
}

// named are the secret names a file uses (secrets.NAME), upper case, sorted, on one line.
func named(text string) string {
	var ns []string
	for _, e := range exprs(text) {
		for _, m := range namedRe.FindAllStringSubmatch(e, -1) {
			ns = append(ns, strings.ToUpper(m[1]))
		}
	}
	return strings.Join(uniqSorted(ns), " ") + " "
}

// declared are the secrets a reusable workflow declares under on.workflow_call.secrets, in the same form.
func declared(wf any) string {
	var ds []string
	for _, k := range keys(at(wf, "on", "workflow_call", "secrets")) {
		ds = append(ds, strings.ToUpper(k))
	}
	return strings.Join(uniqSorted(ds), " ") + " "
}

const badFixture = `jobs:
  a:
    steps:
      - with:
          webhook: ${{ secrets[fromJSON(needs.d.outputs.entries)[matrix.label].notify_secret || 'SLACK_WEBHOOK'] }}
          all: ${{ toJSON(secrets) }}
          spaced: ${{ tojson( SECRETS ) }}
          literal: ${{ secrets['SLACK_WEBHOOK'] }}
          passed: x-${{ inputs.a }}-${{ format('{0}', secrets) }}
      - if: secrets[inputs.name] != ''
`

const goodFixture = `# ${{ secrets[inputs.name] }} in a comment is not an expression
jobs:
  a:
    secrets: inherit
    steps:
      - if: inputs.secrets_dir != '' && needs.secrets.outputs.ok == 'true'
        with:
          webhook: ${{ secrets.SLACK_WEBHOOK }}
          group: x${{ inputs.queue && format('-{0}', github.run_id) || '' }}-${{ secrets.TFPLS_NOTIFY_WEBHOOKS }}
          note: "composite actions cannot read secrets"
`

func TestSecretsScannerFindsEveryForm(t *testing.T) {
	eq(t, "the check finds a computed lookup and the whole context in every form", lineNos(whole(exprs(badFixture))), "5 6 7 8 9 10 ")
	eq(t, "and nothing in a file that names its secrets", len(whole(exprs(goodFixture))), 0)
	eq(t, "it reads the names a file uses", named(goodFixture), "SLACK_WEBHOOK TFPLS_NOTIFY_WEBHOOKS ")
}

func TestSecretsAreNamedNeverComputed(t *testing.T) {
	files := append(glob(t, wfDir+"*.yml"), glob(t, "actions/*/action.yml")...)
	eq(t, "the scan covers the workflows and the actions", len(files) >= 30, true)
	var found []string
	for _, f := range files {
		for _, h := range whole(exprs(read(t, f))) {
			found = append(found, "  "+f+":"+h)
		}
	}
	check(t, "no computed secrets[...] lookup and no whole secrets context (toJSON(secrets)) in the workflows and actions", len(found) == 0,
		"a secret looked up by a computed name, or the whole secrets context, hands EVERY secret to the runner; use secrets.NAME:\n"+strings.Join(found, "\n"))
}

// declarations: exactly the secrets a reusable workflow names, so a caller that lists its secrets sees which ones
func TestSecretDeclarations(t *testing.T) {
	for _, f := range glob(t, wfDir+"*.yml") {
		wf := load(t, f)
		base := f[strings.LastIndex(f, "/")+1:]
		if at(wf, "on", "workflow_call") == nil || base == "tofu.yml" {
			continue
		}
		eq(t, base+" declares exactly the secrets it uses", declared(wf), named(read(t, f)))
	}
	E := load(t, wfDir+"tofu.yml")
	prefix := regexp.MustCompile(`^denzhel/tfpls/[.]github/workflows/`)
	var callees []string
	for _, j := range keys(at(E, "jobs")) {
		u := orEmpty(at(E, "jobs", j, "uses"))
		if prefix.MatchString(u) {
			u = prefix.ReplaceAllString(u, "")
			if i := strings.Index(u, "@"); i >= 0 {
				u = u[:i]
			}
			callees = append(callees, u)
		}
	}
	callees = uniqSorted(callees)
	eq(t, "tofu.yml calls the reusable workflows", len(callees) >= 5, true)
	var all []string
	for _, c := range callees {
		all = append(all, strings.Fields(named(read(t, wfDir+c)))...)
	}
	want := strings.Join(uniqSorted(all), " ") + " "
	eq(t, "tofu.yml declares exactly the secrets of the workflows it calls (a caller may list them instead of secrets: inherit)", declared(E), want)
	eq(t, "those are the four documented secrets", want, "PLAN_ENCRYPTION_KEY SLACK_WEBHOOK TFPLS_ENV_JSON TFPLS_NOTIFY_WEBHOOKS ")
}

// the jobs that notify pass the two webhook secrets to finish, by name
func TestFinishGetsWebhookSecretsByName(t *testing.T) {
	m := 0
	finish := regexp.MustCompile(`^denzhel/tfpls/actions/finish@`)
	for _, f := range glob(t, wfDir+"*.yml") {
		if !strings.Contains(read(t, f), "uses: denzhel/tfpls/actions/finish@") {
			continue
		}
		m++
		wf := load(t, f)
		var vals []string
		for _, j := range keys(at(wf, "jobs")) {
			for _, s := range stepsOf(wf, j) {
				if u, _ := s["uses"].(string); finish.MatchString(u) {
					vals = append(vals, orEmpty(at(s, "with", "webhook"))+" "+orEmpty(at(s, "with", "webhooks")))
				}
			}
		}
		eq(t, f[strings.LastIndex(f, "/")+1:]+": finish gets the webhook secrets by name", strings.Join(uniqSorted(vals), "|"),
			"${{ secrets.SLACK_WEBHOOK }} ${{ secrets.TFPLS_NOTIFY_WEBHOOKS }}")
	}
	eq(t, "four workflows notify (apply, comment-ops, drift, drift-fix)", m, 4)
}
