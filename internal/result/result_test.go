package result

import (
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	cases := []struct {
		name, in string
		want     int
	}{
		{"all green", `{"detect":{"result":"success"},"plan":{"result":"success"}}`, 0},
		{"nothing to plan", `{"detect":{"result":"success"},"plan":{"result":"skipped"}}`, 0},
		{"no needs", `{}`, 0},
		{"failed", `{"detect":{"result":"success"},"plan":{"result":"failure"}}`, 1},
		{"cancelled", `{"plan":{"result":"cancelled"}}`, 1},
		{"detect failed", `{"detect":{"result":"failure"},"plan":{"result":"skipped"}}`, 1},
		{"missing result", `{"plan":{}}`, 1},
		{"not json", `nope`, 1},
		{"empty", ``, 1},
		{"array", `[]`, 1},
		{"truncated", `{"plan":{"result":"success"}`, 1},
		{"trailing data", `{"plan":{"result":"success"}} {}`, 1},
	}
	for _, c := range cases {
		var out strings.Builder
		if got := Run(c.in, &out); got != c.want {
			t.Errorf("%s: rc %d, want %d (%s)", c.name, got, c.want, out.String())
		}
	}
}

func TestOrderAndLines(t *testing.T) {
	var out strings.Builder
	Run(`{"zeta":{"result":"success"},"alpha":{"result":"success"}}`, &out)
	if !strings.HasPrefix(out.String(), "zeta: success\nalpha: success\n") {
		t.Errorf("job lines must keep the input order:\n%s", out.String())
	}
}
