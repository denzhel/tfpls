package detect

import (
	"regexp"
	"strings"
	"sync"
)

const (
	phDirs = "\x01"
	phAny  = "\x02"
)

var globCache sync.Map // glob -> *regexp.Regexp (nil when the glob does not compile)

// GlobRegexp turns a glob into an anchored regular expression (gitignore-like): `**/` = any directories (also none),
// `**` = anything, `*` = anything except `/`, `?` = one character except `/`. Everything else matches literally. The result
// matches the WHOLE string (the former shell version let `$` match before a trailing newline). Nil when it does not compile.
func GlobRegexp(g string) *regexp.Regexp {
	if c, ok := globCache.Load(g); ok {
		return c.(*regexp.Regexp)
	}
	var b strings.Builder
	for _, r := range g {
		if strings.ContainsRune(`.+^${}()|[]\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	s := b.String()
	s = strings.ReplaceAll(s, "**/", phDirs)
	s = strings.ReplaceAll(s, "**", phAny)
	s = strings.ReplaceAll(s, "*", "[^/]*")
	s = strings.ReplaceAll(s, "?", "[^/]")
	s = strings.ReplaceAll(s, phDirs, "(.*/)?")
	s = strings.ReplaceAll(s, phAny, ".*")
	re, err := regexp.Compile("^" + s + "$")
	if err != nil {
		re = nil
	}
	globCache.Store(g, re)
	return re
}

// GlobMatch reports whether the glob matches s.
func GlobMatch(g, s string) bool {
	re := GlobRegexp(g)
	return re != nil && re.MatchString(s)
}
