package main

import "testing"

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
