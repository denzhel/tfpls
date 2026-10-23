package yamlconf

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Golden parity with the YAML tool the shell scripts used (mikefarah's, converting a file to JSON): testdata/<name>.yml
// are representative YAML files (copies of the repository's workflows, actions, issue forms and configuration, plus
// anchor, merge and scalar cases); testdata/<name>.json is what that tool printed for each, as compact JSON with the
// keys sorted, generated once. The test needs no such tool: it compares the parsed documents of ToJSON and of the
// golden file. (Cases where that tool is known to differ from the YAML 1.2 spec, such as a merge key with a list of
// maps that includes a literal map, or the float form 1E+3, are left out of the fixtures.)
func TestParityWithYq(t *testing.T) {
	files, _ := filepath.Glob("testdata/*.yml")
	if len(files) < 10 {
		t.Fatalf("only %d fixtures found", len(files))
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			golden, err := os.ReadFile(strings.TrimSuffix(f, ".yml") + ".json")
			if err != nil {
				t.Fatalf("missing golden file: %v", err)
			}
			b, _ := os.ReadFile(f)
			got, err := ToJSON(b)
			if err != nil {
				t.Fatal(err)
			}
			var g, w any
			if err := json.Unmarshal(got, &g); err != nil {
				t.Fatalf("ToJSON output is not JSON: %v", err)
			}
			if err := json.Unmarshal(golden, &w); err != nil {
				t.Fatalf("golden is not JSON: %v", err)
			}
			if !reflect.DeepEqual(g, w) {
				t.Errorf("differs from the golden output\n got: %s\nwant: %s", got, golden)
			}
		})
	}
}
