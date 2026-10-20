package finish

import (
	"bytes"
	"strings"
	"testing"
)

func TestPick(t *testing.T) {
	m := `{"ONCALL":"https://h.invalid/o","BAD":"nope","N":5}`
	for _, c := range []struct{ key, def, hooks, url, warn string }{
		{"", "https://h.invalid/d", m, "https://h.invalid/d", ""},
		{"ONCALL", "https://h.invalid/d", m, "https://h.invalid/o", ""},
		{"", "", m, "", ""},
		{"BAD", "", m, "", "not an http(s) URL"},
		{"N", "", m, "", "not an http(s) URL"},
		{"MISSING", "", m, "", "is not a key"},
		{"MISSING", "", "", "", "which is not set"},
		{"ONCALL", "", "[1]", "", "not a JSON object"},
		{"", "ftp://x", "", "", "SLACK_WEBHOOK is not an http(s) URL"},
		{"ONCALL", "", `{"ONCALL":"https://h.invalid/a b"}`, "", "not an http(s) URL"},
	} {
		url, note := Pick(c.key, c.def, c.hooks)
		if url != c.url || (c.warn != "" && !strings.Contains(note, c.warn)) || (c.warn == "" && strings.HasPrefix(note, "::warning::")) {
			t.Errorf("%+v: %q %q", c, url, note)
		}
	}
}

func TestWebhookMasksFirst(t *testing.T) {
	var out bytes.Buffer
	got := map[string]string{}
	e := Env{Getenv: func(k string) string { return map[string]string{"DEFAULT_HOOK": "https://h.invalid/a%b"}[k] }, Stdout: &out,
		SetOutput: func(n, v string) error { got[n] = v; return nil }}
	if Webhook(e) != 0 || !strings.HasPrefix(out.String(), "::add-mask::https://h.invalid/a%25b\n") || got["url"] != "https://h.invalid/a%b" {
		t.Fatal(out.String(), got)
	}
}
