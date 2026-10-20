package yamlconf

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// parity with yq on every YAML file of the repository (skipped when yq or jq is missing)
func TestParityWithYq(t *testing.T) {
	for _, tool := range []string{"yq", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
	var files []string
	for _, pat := range []string{"../../.github/workflows/*.yml", "../../actions/*/action.yml", "../../examples/*/*.yml", "../../examples/*.yml", "../../.github/*.yml"} {
		m, _ := filepath.Glob(pat)
		files = append(files, m...)
	}
	if len(files) < 10 {
		t.Fatalf("only %d files found", len(files))
	}
	canon := func(in []byte) string {
		c := exec.Command("jq", "-S", "-c", ".")
		c.Stdin = bytes.NewReader(in)
		out, err := c.Output()
		if err != nil {
			t.Fatalf("jq: %v", err)
		}
		return string(out)
	}
	for _, f := range files {
		want, err := exec.Command("yq", "-o=json", "-I0", ".", f).Output()
		if err != nil {
			t.Errorf("%s: yq: %v", f, err)
			continue
		}
		b, _ := os.ReadFile(f)
		got, err := ToJSON(b)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if canon(got) != canon(want) {
			t.Errorf("%s: differs from yq", f)
		}
	}
}
