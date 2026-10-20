package gh

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stub writes a fake gh that prints its arguments (or fails) and returns a Client for it.
func stub(t *testing.T, body string) Client {
	t.Helper()
	p := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return Client{Bin: p}
}

func TestRun(t *testing.T) {
	c := stub(t, `echo "$@"`)
	got, err := c.Run("api", "repos/o/r")
	if err != nil || got != "api repos/o/r\n" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestErrorCarriesStderrAndCode(t *testing.T) {
	c := stub(t, `echo nope >&2; exit 4`)
	_, err := c.Run("api", "x")
	var ge *Error
	if !errors.As(err, &ge) || ge.Code != 4 || !strings.Contains(ge.Stderr, "nope") {
		t.Errorf("got %v", err)
	}
}

func TestMissingBinary(t *testing.T) {
	_, err := Client{Bin: "/nonexistent/gh"}.Run("x")
	if err == nil {
		t.Error("a missing gh must be an error")
	}
}

func TestStdinIsNotATerminal(t *testing.T) {
	c := stub(t, `cat`)
	got, err := c.Run("x")
	if err != nil || got != "" {
		t.Errorf("a nil stdin must read as empty: %q, %v", got, err)
	}
	got, err = c.RunInput(strings.NewReader("in"), "x")
	if err != nil || got != "in" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestEnv(t *testing.T) {
	c := stub(t, `echo "$TFPLS_T"`)
	c.Env = []string{"TFPLS_T=v"}
	if got, _ := c.Run("x"); got != "v\n" {
		t.Errorf("got %q", got)
	}
}

func TestJSONAndPages(t *testing.T) {
	c := stub(t, `echo '{"a":1}'`)
	var v struct{ A int }
	if err := c.JSON(&v); err != nil || v.A != 1 {
		t.Errorf("got %v, %v", v, err)
	}
	c = stub(t, `echo 'nope'`)
	if err := c.JSON(&v); err == nil {
		t.Error("invalid JSON must be an error")
	}
	c = stub(t, `printf '{"n":1}\n{"n":2}\n'`)
	sum := 0
	err := c.Pages(func(d *json.Decoder) error {
		var p struct{ N int }
		if err := d.Decode(&p); err != nil {
			return err
		}
		sum += p.N
		return nil
	}, "repos/o/r/x")
	if err != nil || sum != 3 {
		t.Errorf("sum %d, %v", sum, err)
	}
}
