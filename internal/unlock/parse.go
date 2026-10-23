// Package unlock releases a stuck state lock (/tofu unlock) and sweeps the locks of every workspace (the manual `locks` task).
// It finds the lock ID itself by asking tofu, then force-unlocks exactly that ID. Fails closed: any doubt => refuse and say why.
package unlock

import (
	"regexp"
	"strings"
	"time"
)

// Lock is what tofu prints in the "Lock Info" block of the state lock error.
type Lock struct{ ID, Who, Op, Created string }

var (
	idRE      = regexp.MustCompile(`^[0-9A-Za-z-]{8,64}$`)
	idLine    = regexp.MustCompile(`^ *ID: *(.*)$`)
	whoLine   = regexp.MustCompile(`^ *Who: *(.*)$`)
	opLine    = regexp.MustCompile(`^ *Operation: *(.*)$`)
	madeLine  = regexp.MustCompile(`^ *Created: *(.*)$`)
	lockError = "Error acquiring the state lock"
)

func first(re *regexp.Regexp, lines []string) string {
	for _, l := range lines {
		if m := re.FindStringSubmatch(l); m != nil {
			return m[1]
		}
	}
	return ""
}

// ParseLock reads the lock block. rc is 0 for a lock with a safe ID, 1 when the text is no lock error, 2 when the ID is missing or
// unsafe (it ends up in a command line).
func ParseLock(out string) (l Lock, rc int) {
	if !strings.Contains(out, lockError) {
		return l, 1
	}
	lines := strings.Split(out, "\n")
	l.ID = strings.Join(strings.Fields(first(idLine, lines)), "")
	l.Who, l.Op, l.Created = first(whoLine, lines), first(opLine, lines), first(madeLine, lines)
	if !idRE.MatchString(l.ID) {
		return l, 2
	}
	return l, 0
}

// AgeMinutes is the age of a "Created" text (2026-10-09 10:00:00.123 +0000 UTC) in whole minutes; ok is false when it cannot be read.
// Only the plain date and time of the first 19 characters count (UTC): free text such as "yesterday" is not a time.
func AgeMinutes(created string, now time.Time) (int, bool) {
	r := []rune(created)
	if len(r) < 19 {
		return 0, false
	}
	t, err := time.Parse("2006-01-02 15:04:05", string(r[:19]))
	if err != nil {
		return 0, false
	}
	return int(now.Unix()-t.Unix()) / 60, true
}
