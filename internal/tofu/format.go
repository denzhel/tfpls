// Package tofu is the plan/apply engine of the tofu action: it runs one workspace through init, plan, policy, the reviewed-plan
// store and apply, and posts the sticky pull request comment. This file holds the text helpers of that comment.
package tofu

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// ClipPlan returns the file's text; when it is longer than max bytes it keeps the beginning and the end (the "Plan: N to add..."
// summary is at the end) with a note in between. The result is always valid UTF-8 (a cut in the middle of a character is
// dropped). A missing file gives "".
func ClipPlan(file string, max int) string {
	b, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	return clip(b, max)
}

func clip(b []byte, max int) string {
	if len(b) <= max {
		return strings.ToValidUTF8(string(b), "")
	}
	head := max * 6 / 10
	tail := max - head
	s := string(b[:head]) + fmt.Sprintf("\n\n[... %d bytes omitted: the plan is longer than a pull request comment allows; the full text is in the run logs ...]\n\n", len(b)-head-tail) + string(b[len(b)-tail:])
	return strings.ToValidUTF8(s, "")
}

var refreshRe = regexp.MustCompile(`: (Refreshing state\.\.\.|Reading\.\.\.|Read complete after )`)

// lines splits text like a line reader: no empty element after a final newline.
func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// StripRefresh drops the per-resource "Refreshing state...", "Reading..." and "Read complete after" lines.
func StripRefresh(s string) string {
	var b strings.Builder
	for _, l := range lines(s) {
		if !refreshRe.MatchString(l) {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

// lastAfter is `grep -E '^<prefix>' | tail -1 | sed 's/^<prefix>//'` over the text of a file ("" when none or no file).
func lastAfter(file, prefix string) string {
	b, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	last := ""
	for _, l := range lines(string(b)) {
		if strings.HasPrefix(l, prefix) {
			last = strings.TrimPrefix(l, prefix)
		}
	}
	return last
}

// PlanLine is "N to add, N to change, N to destroy." from the plan's own "Plan: ..." line.
func PlanLine(file string) string { return lastAfter(file, "Plan: ") }

// ApplyLine is "N added, N changed, N destroyed." from the apply's own "Apply complete! Resources: ..." line.
func ApplyLine(file string) string { return lastAfter(file, "Apply complete! Resources: ") }

// ApplyView is the output of tofu apply without the refresh/read lines and without the "Outputs:" block. Output values are
// never put into a pull request comment (a plan shows them as "known after apply"); the run log has them.
func ApplyView(s string) string {
	var b strings.Builder
	for _, l := range lines(StripRefresh(s)) {
		if strings.HasPrefix(l, "Outputs:") {
			break
		}
		b.WriteString(l + "\n")
	}
	return b.String()
}

// CommentBody is the sticky plan comment: marker, hidden status, summary line, heading linking to the run, notices, the plan in
// one collapsed section, and the apply hint. A clean "no changes" has no collapsed section. noHint leaves the hint out (no plan
// was saved). The text is laid out exactly as the printf of the Bash version did (it ends with a newline unless a hint follows).
func CommentBody(marker, icon, status, label, url, file, summary, extra string, limit int, noHint bool) string {
	verb := "plan"
	if status == "applied" {
		verb = "apply"
	}
	planLine := PlanLine(file)
	first := ""
	switch status {
	case "planned", "policy-failed":
		if planLine != "" {
			first = "**Plan:** " + planLine
		}
	case "applied":
		if al := ApplyLine(file); al != "" {
			first = "**Applied:** " + al
		} else if planLine != "" {
			first = "**Plan:** " + planLine
		}
	}
	if first == "" {
		first = "**" + summary + "**"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n<!-- tfpls-status: %s -->\n%s\n### %s [%s](%s) %s logs\n", marker, status, first, icon, label, url, verb)
	if extra != "" {
		fmt.Fprintf(&b, "\n%s\n", extra)
	}
	if status != "no-changes" {
		fmt.Fprintf(&b, "\n<details><summary>Click here to expand</summary>\n\n```terraform\n%s\n```\n</details>\n", strings.TrimRight(ClipPlan(file, limit), "\n"))
	}
	if status == "planned" && !noHint {
		fmt.Fprintf(&b, "\n`/tofu apply %s` to apply", label)
	}
	return b.String()
}

// AlreadyAppliedBody is the comment of the apply after a merge when `/tofu apply` had already applied the workspace and the plan
// made to confirm it found nothing left: one line, no plan and no hint. The status stays `no-changes`.
func AlreadyAppliedBody(marker, label, runURL, applyURL string) string {
	return fmt.Sprintf("%s\n<!-- tfpls-status: no-changes -->\n🟢 **[%s](%s)**: Already applied by `/tofu apply` ([run](%s)); nothing changed since.\n", marker, label, runURL, applyURL)
}

// UnencryptedWarns reports whether a plan saved without a key still carries the "not encrypted" warning. The opt-in
// (allow_unencrypted_plans) only silences the warning; whether an unencrypted plan may be saved at all is decided by the
// public-repository guard of the engine, which does not look at the opt-in.
func UnencryptedWarns(allow string) bool { return allow != "true" }
