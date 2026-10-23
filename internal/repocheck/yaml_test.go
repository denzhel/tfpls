package repocheck

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/denzhel/tfpls/internal/yamlconf"
)

// every YAML file of the repository (workflows, actions, examples, .github) parses; a failure names the file
func TestYAMLFilesParse(t *testing.T) {
	r := root(t)
	n := 0
	err := filepath.WalkDir(r, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".claude", "testdata", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if ext := filepath.Ext(p); ext != ".yml" && ext != ".yaml" {
			return nil
		}
		rel, _ := filepath.Rel(r, p)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			return nil
		}
		n++
		if _, err := yamlconf.Parse(b); err != nil {
			t.Errorf("%s: not valid YAML: %v", rel, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 20 {
		t.Fatalf("only %d YAML files found", n)
	}
}
