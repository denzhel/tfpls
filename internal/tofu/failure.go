package tofu

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	ansiRe     = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	boxLead    = regexp.MustCompile(`^[│╷╵ ]+`)
	boxAny     = strings.NewReplacer("│", "", "╷", "", "╵", "")
	errLineRe  = regexp.MustCompile(`^ *Error:`)
	credRe     = regexp.MustCompile(`No valid credential sources|InvalidClientTokenId|ExpiredToken|AccessDenied|SignatureDoesNotMatch|dial tcp|connection refused|no such host`)
	lockFieldR = func(name string) *regexp.Regexp { return regexp.MustCompile(`^ *` + name + `: *(.*)$`) }
	idRe       = lockFieldR("ID")
	whoRe      = lockFieldR("Who")
	opRe       = lockFieldR("Operation")
	createdRe  = lockFieldR("Created")
)

// cleanLines removes colour codes and the box-drawing characters tofu puts around its errors.
func cleanLines(text string) []string {
	var out []string
	for _, l := range strings.Split(ansiRe.ReplaceAllString(text, ""), "\n") {
		out = append(out, boxAny.Replace(boxLead.ReplaceAllString(l, "")))
	}
	return out
}

func firstField(clean []string, re *regexp.Regexp) string {
	for _, l := range clean {
		if m := re.FindStringSubmatch(l); m != nil {
			return m[1]
		}
	}
	return ""
}

func contains(clean []string, sub string) bool {
	for _, l := range clean {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func cutChars(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// Failure is Markdown for a failed init/plan: the first errors and, for the common causes, what to do. "" when the output has no
// "Error:" line. actionRepo is the repository of the action (for the runbook link).
func Failure(text, label, actionRepo string) string {
	clean := cleanLines(text)
	var errs []string
	seen := map[string]bool{}
	for _, l := range clean {
		if errLineRe.MatchString(l) {
			l = strings.TrimLeft(l, " ")
			if !seen[l] {
				seen[l] = true
				errs = append(errs, l)
			}
		}
	}
	if len(errs) == 0 {
		return ""
	}
	if len(errs) > 3 {
		errs = errs[:3]
	}
	if label == "" {
		label = "<workspace>"
	}
	if actionRepo == "" {
		actionRepo = "denzhel/tfpls"
	}
	var b strings.Builder
	for _, e := range errs {
		b.WriteString("- " + cutChars(e, 300) + "\n")
	}
	switch {
	case contains(clean, "Error acquiring the state lock"):
		id := strings.Join(strings.Fields(firstField(clean, idRe)), "")
		who := firstField(clean, whoRe)
		holder := ""
		if id != "" {
			// the Bash version forgot the closing parenthesis when the lock had an ID but no holder
			holder = " (lock `" + id + "`"
			if who != "" {
				holder += ", held by `" + who + "`"
			}
			holder += ")"
		}
		fmt.Fprintf(&b, "\n🔒 The state is locked%s. If no run is working on `%s`, release it with `/tofu unlock %s`; otherwise wait for that run.\n", holder, label, label)
	case credRe.MatchString(strings.Join(clean, "\n")):
		fmt.Fprintf(&b, "\n🔑 Looks like a credentials or connection problem: check the `TFPLS_ROLE_ARN` variable (or the `TFPLS_ENV_JSON` secret) and the backend endpoint ([runbook](https://github.com/%s/blob/main/docs/runbooks.md)).\n", actionRepo)
	case contains(clean, "Backend configuration changed") || contains(clean, "Backend initialization required"):
		b.WriteString("\n🧱 The backend configuration changed: run `tofu init -reconfigure` locally against the new backend (or `-migrate-state` to move the state) before this pipeline can use it.\n")
	}
	return b.String()
}

// Lock is what a tofu "Error acquiring the state lock" message says about the holder.
type Lock struct{ ID, Who, Op, Created string }

var lockIDRe = regexp.MustCompile(`^[0-9A-Za-z-]{8,64}$`)

// ParseLock reads the lock block of a failed plan. found is false when there is no lock error; valid is false when its ID is
// not a plausible lock ID (the other fields are still filled).
func ParseLock(text string) (l Lock, found, valid bool) {
	clean := cleanLines(text)
	if !contains(clean, "Error acquiring the state lock") {
		return Lock{}, false, false
	}
	l.ID = strings.Join(strings.Fields(firstField(clean, idRe)), "")
	l.Who = firstField(clean, whoRe)
	l.Op = firstField(clean, opRe)
	l.Created = firstField(clean, createdRe)
	return l, true, lockIDRe.MatchString(l.ID)
}
