package gate

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	reCommand   = regexp.MustCompile(`^[[:space:]]*/[A-Za-z]+[[:space:]]*`)
	reDetails   = regexp.MustCompile(`^[ \t]*<details`)
	reCodeFence = regexp.MustCompile("^[ \t]*(```|~~~)")
	reHeading   = regexp.MustCompile(`^#+[ \t]`)
	reHeadLead  = regexp.MustCompile(`^#+[ \t]+`)
	reHeadTrail = regexp.MustCompile(`[ \t]+#+$`)
	reBold      = regexp.MustCompile(`^\*\*[^*]+\*\*:?$`)
	reBox       = regexp.MustCompile(`^([-*+]|[0-9]+[.)])[ \t]+\[[ xX]\]`)
	reLabel     = regexp.MustCompile(`^[^:]+:`)
)

var sections = []string{"outcome", "tasks", "acceptance criteria", "test criteria"}

func trimBlank(s string) string { return strings.Trim(s, " \t\r") }

// empty: no real text. The form's "_No response_", TBD, TODO, TBA, or fewer than two characters that are not space or punctuation.
func empty(s string) bool {
	t := strings.ToLower(trimBlank(s))
	t = strings.NewReplacer("_", "", "*", "", "`", "", "~", "").Replace(t)
	t = trimBlank(t)
	switch t {
	case "no response", "tbd", "todo", "tba":
		return true
	}
	t = strings.Map(func(r rune) rune {
		if r < 128 && (strings.ContainsRune(" \t\n\v\f\r", r) || isASCIIPunct(r)) {
			return -1
		}
		return r
	}, t)
	return utf8.RuneCountInString(t) < 2
}

func isASCIIPunct(r rune) bool {
	return (r >= '!' && r <= '/') || (r >= ':' && r <= '@') || (r >= '[' && r <= '`') || (r >= '{' && r <= '~')
}

// placeholder: a box text that is empty, or only a short label ending in a colon (the template's "Local:")
func placeholder(s string) bool {
	t := trimBlank(s)
	// the label and its colon are at most 41 characters (runes, not bytes: the awk of the Bash gate counted bytes in some locales)
	if loc := reLabel.FindStringIndex(t); loc != nil && utf8.RuneCountInString(t[:loc[1]]) <= 41 {
		t = t[loc[1]:]
	}
	return empty(t)
}

// elaborated: the four template sections, real text under Outcome, and at least one real box under Tasks, Acceptance criteria and
// Test criteria. A section title is a heading (### Outcome) or a bold line (**Outcome**); code blocks and everything from <details>
// on do not count.
func elaborated(body string) bool {
	seen := map[string]bool{}
	real := map[string]int{}
	sec, code := "", false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if reDetails.MatchString(line) {
			break
		}
		if reCodeFence.MatchString(line) {
			code = !code
			continue
		}
		if code {
			continue
		}
		t := trimBlank(line)
		title := ""
		if reHeading.MatchString(t) {
			title = reHeadTrail.ReplaceAllString(reHeadLead.ReplaceAllString(t, ""), "")
		} else if reBold.MatchString(t) {
			title = strings.ReplaceAll(t, "*", "")
		}
		if title != "" {
			title = trimBlank(strings.TrimSuffix(trimBlank(strings.ToLower(title)), ":"))
			sec = ""
			for _, s := range sections {
				if title == s {
					sec = s
					seen[s] = true
				}
			}
			continue
		}
		if sec == "" {
			continue
		}
		if loc := reBox.FindStringIndex(t); loc != nil {
			if !placeholder(t[loc[1]:]) {
				real[sec]++
			}
			continue
		}
		if sec == "outcome" && !empty(t) {
			real[sec]++
		}
	}
	for _, s := range sections {
		if !seen[s] || real[s] == 0 {
			return false
		}
	}
	return true
}
