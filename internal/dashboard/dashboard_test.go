package dashboard

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) string {
	d := t.TempDir()
	os.MkdirAll(filepath.Join(d, "drift"), 0o755)
	os.MkdirAll(filepath.Join(d, "audit"), 0o755)
	os.WriteFile(filepath.Join(d, "drift", "a_b.json"), []byte(`{"name":"a/b|x","state":"drift","issue":7,"summary":"p|q","checked":"2026-10-10T09:00:00Z"}`), 0o644)
	os.WriteFile(filepath.Join(d, "drift", "bad.json"), []byte(`[1,2]`), 0o644)
	os.WriteFile(filepath.Join(d, "audit", "2026-10.jsonl"), []byte("{\"ts\":\"2026-10-10T08:00:00Z\",\"kind\":\"apply\",\"status\":\"applied\",\"workspace\":\"a/b|x\",\"actor\":\"al\"}\n7\n[]\nnot json\n"), 0o644)
	return d
}

func TestBuildEscapesAndSkips(t *testing.T) {
	out := t.TempDir()
	w, e, err := Build(fixture(t), out, Options{Repo: "o/r", Events: 50, Now: "2026-10-10T10:00:00Z"})
	if err != nil || w != 1 || e != 1 {
		t.Fatal(w, e, err)
	}
	sum, _ := os.ReadFile(filepath.Join(out, "summary.md"))
	if !strings.Contains(string(sum), "| `a/b/x` | 🟠 drift | p/q |") {
		t.Fatal(string(sum))
	}
	page, _ := os.ReadFile(filepath.Join(out, "index.html"))
	if !bytes.Contains(page, []byte(`"issue_url":"https://github.com/o/r/issues/7"`)) {
		t.Fatal("issue url")
	}
}

func TestBuildBadInput(t *testing.T) {
	if _, _, err := Build(t.TempDir(), t.TempDir(), Options{Events: 1, Now: "yesterday"}); err == nil {
		t.Fatal("bad time accepted")
	}
	if _, err := ParseEvents("abc"); err == nil {
		t.Fatal("bad events accepted")
	}
	if _, err := ParseEvents("-1"); err == nil {
		t.Fatal("negative events accepted")
	}
}

// the data block of the page (the one script element with id="data") is valid JSON with the repository, the
// workspaces and the events, and nothing in it can close the script element
func TestDataBlockIsJSON(t *testing.T) {
	out := t.TempDir()
	if _, _, err := Build(fixture(t), out, Options{Repo: "o/r", Events: 50, Now: "2026-10-10T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	page, _ := os.ReadFile(filepath.Join(out, "index.html"))
	s := string(page)
	i := strings.Index(s, `<script id="data"`)
	if i < 0 {
		t.Fatal("no data block")
	}
	s = s[i:]
	s = s[strings.Index(s, ">")+1:]
	s = s[:strings.Index(s, "</script>")]
	if strings.Contains(s, "<") {
		t.Errorf("a raw < in the data block: %s", s)
	}
	var data struct {
		Repo       string           `json:"repo"`
		Workspaces []map[string]any `json:"workspaces"`
		Events     []map[string]any `json:"events"`
	}
	if err := json.Unmarshal([]byte(s), &data); err != nil {
		t.Fatalf("the data block is not JSON: %v\n%s", err, s)
	}
	if data.Repo != "o/r" || len(data.Workspaces) != 1 || len(data.Events) != 1 {
		t.Errorf("unexpected data: %+v", data)
	}
}

func TestActionRefusesOutsideOut(t *testing.T) {
	for _, out := range []string{"../x", "/etc", ""} {
		var se bytes.Buffer
		env := Env{Getenv: func(k string) string { return map[string]string{"OUT": out, "REPO": "o/r"}[k] }, Stdout: &se, Stderr: &se}
		if Main([]string{"action"}, env) == 0 {
			t.Fatal(out)
		}
	}
}
