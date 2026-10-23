package repocheck

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// repoFiles are the text files of the repository (binary files skipped, like grep -I), without .git and without the other
// worktrees of the maintainer's checkout under .claude, and without the test file that names the patterns.
func repoFiles(t *testing.T, skip string) map[string][]byte {
	t.Helper()
	r := root(t)
	out := map[string][]byte{}
	err := filepath.WalkDir(r, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(r, p)
		if d.IsDir() {
			if rel == ".git" || rel == ".claude" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == skip || !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil || bytes.IndexByte(b, 0) >= 0 {
			return nil
		}
		out[filepath.ToSlash(rel)] = b
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Names this project defines never start with TF_: that prefix belongs to OpenTofu/Terraform (TF_LOG, TF_VAR_*, TF_WORKSPACE ...).
func TestNames(t *testing.T) {
	files := repoFiles(t, "names_test.go")
	// OpenTofu's own variables may be read or set; everything else with this prefix is a clash waiting to happen
	allowed := regexp.MustCompile(`^TF_(LOG|LOG_PATH|LOG_CORE|LOG_PROVIDER|INPUT|WORKSPACE|DATA_DIR|IN_AUTOMATION|PLUGIN_CACHE_DIR|CLI_ARGS[A-Z_]*|CLI_CONFIG_FILE|REATTACH_PROVIDERS|VAR_[A-Za-z0-9_]+|TOFU_[A-Z_]*)$`)
	tf := regexp.MustCompile(`\bTF_[A-Z0-9_]+`)
	set := map[string]bool{}
	for _, b := range files {
		for _, m := range tf.FindAllString(string(b), -1) {
			if !allowed.MatchString(m) {
				set[m] = true
			}
		}
	}
	hits := make([]string, 0, len(set))
	for h := range set {
		hits = append(hits, h)
	}
	sort.Strings(hits)
	check(t, "no TF_ name of ours", len(hits) == 0, "TF_ names that may clash with OpenTofu: "+strings.Join(hits, " "))
	check(t, "the allowed list rejects a name of ours (fixture)", !allowed.MatchString("TF_PLS_TOKEN") && allowed.MatchString("TF_VAR_x") && allowed.MatchString("TF_LOG"),
		"the allow-list is too wide or too narrow")

	// the product prefix is used instead
	check(t, "the documented variable is TFPLS_ROLE_ARN", strings.Contains(read(t, "README.md"), "TFPLS_ROLE_ARN"), "TFPLS_ROLE_ARN not documented")

	// nothing of the old names is left
	old := regexp.MustCompile(`tf-pipelines|\bTFP_|(?:^|[^A-Za-z])tfp[-_]`)
	var in []string
	for name, b := range files {
		for _, l := range strings.Split(string(b), "\n") {
			if old.MatchString(l) {
				in = append(in, name)
				break
			}
		}
	}
	sort.Strings(in)
	check(t, "no trace of the old names", len(in) == 0, "old names in: "+strings.Join(in, " "))
	check(t, "the old-name pattern finds each form (fixture)", old.MatchString("tf-pipelines") && old.MatchString("export TFP_X=1") && old.MatchString("x tfp_y") && !old.MatchString("tfpls"),
		"the old-name pattern does not work")
}
