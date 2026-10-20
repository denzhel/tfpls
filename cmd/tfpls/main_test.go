package main

import (
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	cases := []struct {
		args []string
		want int
	}{
		{nil, 2},
		{[]string{"version"}, 0},
		{[]string{"--help"}, 0},
		{[]string{"nope"}, 2},
		{[]string{"onboard", "--nope"}, 2},
	}
	for _, c := range cases {
		if got := run(c.args); got != c.want {
			t.Errorf("run(%v) = %d, want %d", c.args, got, c.want)
		}
	}
}

func TestUsageListsEveryRegisteredCommand(t *testing.T) {
	u := usage()
	for name, c := range commands {
		if !strings.Contains(u, "tfpls "+name) || !strings.Contains(u, c.help) {
			t.Errorf("usage does not list %q", name)
		}
	}
	if len(commands) < 2 {
		t.Errorf("expected at least onboard and result, got %d commands", len(commands))
	}
}
