package setup

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	versionFileRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	requiredRe    = regexp.MustCompile(`^[ \t\r\n\v\f]*required_version[ \t\r\n\v\f]*=[ \t\r\n\v\f]*"[ \t\r\n\v\f]*=?[ \t\r\n\v\f]*v?([0-9]+\.[0-9]+\.[0-9]+)[ \t\r\n\v\f]*"`)
)

const blanks = " \t\r\n\v\f"

// DetectVersion returns the OpenTofu version a repository directory asks for, or "".
// Order: .opentofu-version, .tofu-version, .terraform-version (the first non-empty line, comments after #,
// optional leading v, exactly X.Y.Z), then an EXACT required_version ("1.11.4" or "= 1.11.4") in a *.tf file
// of the directory itself (ranges such as ">= 1.6" are ignored).
func DetectVersion(dir string) string {
	for _, f := range []string{".opentofu-version", ".tofu-version", ".terraform-version"} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			continue
		}
		v := ""
		for _, l := range strings.Split(string(b), "\n") {
			if i := strings.Index(l, "#"); i >= 0 {
				l = l[:i]
			}
			if l = strings.Trim(l, blanks); l != "" {
				v = l
				break
			}
		}
		if v = strings.TrimPrefix(v, "v"); versionFileRe.MatchString(v) {
			return v
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.tf"))
	sort.Strings(files)
	for _, f := range files {
		fi, err := os.Stat(f)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(b), "\n") {
			if m := requiredRe.FindStringSubmatch(l); m != nil {
				return m[1]
			}
		}
	}
	return ""
}
