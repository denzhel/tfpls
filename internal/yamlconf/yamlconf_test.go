package yamlconf

import (
	"strings"
	"testing"
)

func TestToJSON(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"map", "a: 1\nb: [x, y]\n", `{"a":1,"b":["x","y"]}`},
		{"scalars", "t: true\nf: false\nn: null\ns: hi\nfl: 1.5\nstr: \"1\"\n", `{"f":false,"fl":1.5,"n":null,"s":"hi","str":"1","t":true}`},
		{"anchor and merge", "base: &b {x: 1}\nuse:\n  <<: *b\n  y: 2\n", `{"base":{"x":1},"use":{"x":1,"y":2}}`},
		{"empty file", "", `null`},
		{"only a comment", "# nothing\n", `null`},
		{"non-string key", "1: a\ntrue: b\n", `{"1":"a","true":"b"}`},
		{"nested non-string key", "a:\n  2: x\n", `{"a":{"2":"x"}}`},
		{"list of maps", "- a: 1\n- b: 2\n", `[{"a":1},{"b":2}]`},
		{"yes is a string (YAML 1.2)", "k: yes\n", `{"k":"yes"}`},
	}
	for _, c := range cases {
		got, err := ToJSON([]byte(c.in))
		if err != nil || string(got) != c.want {
			t.Errorf("%s: got %s, %v; want %s", c.name, got, err, c.want)
		}
	}
}

func TestErrors(t *testing.T) {
	for name, in := range map[string]string{
		"bad indent":      "a:\n  b: 1\n c: 2\n",
		"tab":             "a:\n\tb: 1\n",
		"two documents":   "a: 1\n---\nb: 2\n",
		"duplicate key":   "a: 1\na: 2\n",
		"unclosed flow":   "a: [1, 2\n",
		"undefined alias": "a: *nope\n",
	} {
		if _, err := ToJSON([]byte(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLoadFileMissing(t *testing.T) {
	if _, err := LoadFile("/nonexistent/x.yml"); err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Errorf("got %v", err)
	}
}
