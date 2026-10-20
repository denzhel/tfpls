package waitfor

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

type seqGH struct {
	outs []string // answer per call; the last one repeats
	n    int
	err  bool
}

func (g *seqGH) Run(args ...string) (string, error) {
	i := g.n
	if i >= len(g.outs) {
		i = len(g.outs) - 1
	}
	g.n++
	if g.err {
		return "", errors.New("boom")
	}
	return g.outs[i], nil
}

func jb(name, status, concl string) string {
	c := "null"
	if concl != "" {
		c = `"` + concl + `"`
	}
	return `{"name":"` + name + `","status":"` + status + `","conclusion":` + c + `}`
}

func run(t *testing.T, g GH, waitFor, timeout string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	clock := time.Unix(0, 0)
	env := map[string]string{"WAIT_FOR": waitFor, "TIMEOUT": timeout, "REPO": "o/r", "RUN_ID": "1", "TFPLS_POLL": "0"}
	rc := Run(Env{Getenv: func(k string) string { return env[k] }, Stdout: &out, GH: g,
		Now: func() time.Time { return clock }, Sleep: func(d time.Duration) { clock = clock.Add(time.Minute) }})
	return rc, out.String()
}

func TestBudget(t *testing.T) {
	for in, want := range map[string]int{"60": 45, "50": 35, "31": 16, "30": 15, "20": 10, "2": 1, "1": 1, "0": 0, "abc": 35, "": 35, "-5": 35} {
		if got := Budget(in); got != want {
			t.Errorf("Budget(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestMatching(t *testing.T) {
	for name, tc := range map[string]struct {
		job  string
		want int
	}{
		"apply / prod": {"apply / prod", 0}, "prod": {"prod", 0}, "apply (prod)": {"apply (prod)", 0},
		"preprod tail": {"apply / preprod", 1}, "apply (preprod)": {"apply (preprod)", 1},
	} {
		rc, _ := run(t, &seqGH{outs: []string{jb(tc.job, "completed", "success")}}, "prod", "0")
		if rc != tc.want {
			t.Errorf("%s: rc %d, want %d", name, rc, tc.want)
		}
	}
}

func TestFailuresStop(t *testing.T) {
	for _, c := range []string{"failure", "cancelled", "skipped"} {
		rc, out := run(t, &seqGH{outs: []string{jb("apply / prod", "completed", c)}}, "prod", "50")
		if rc != 1 || !strings.Contains(out, "has to succeed first and finished as "+c) {
			t.Errorf("%s: rc %d out %q", c, rc, out)
		}
	}
}

func TestWaitsThenContinues(t *testing.T) {
	g := &seqGH{outs: []string{jb("apply / prod", "in_progress", ""), jb("apply / prod", "in_progress", ""), jb("apply / prod", "completed", "success")}}
	rc, out := run(t, g, "prod", "50")
	if rc != 0 || strings.Count(out, "still waiting") != 2 {
		t.Errorf("rc %d out %q", rc, out)
	}
}

func TestOneFailureStopsWithoutWaitingForTheRest(t *testing.T) {
	rc, _ := run(t, &seqGH{outs: []string{jb("a", "completed", "failure") + "\n" + jb("b", "in_progress", "")}}, "a,b", "50")
	if rc != 1 {
		t.Errorf("rc %d", rc)
	}
}

func TestTimeoutAndUnreadable(t *testing.T) {
	rc, out := run(t, &seqGH{outs: []string{jb("prod", "in_progress", "")}}, "prod", "0")
	if rc != 1 || !strings.Contains(out, "gave up") {
		t.Errorf("rc %d out %q", rc, out)
	}
	rc, out = run(t, &seqGH{err: true, outs: []string{""}}, "prod", "0")
	if rc != 1 || !strings.Contains(out, "could not read the jobs") {
		t.Errorf("rc %d out %q", rc, out)
	}
	// garbage is a read failure, never "no jobs and fine"
	rc, out = run(t, &seqGH{outs: []string{"not json"}}, "prod", "0")
	if rc != 1 || !strings.Contains(out, "could not read the jobs") {
		t.Errorf("rc %d out %q", rc, out)
	}
}
