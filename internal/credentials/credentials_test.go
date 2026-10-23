package credentials

import "testing"

func TestParseMapKeepsOrderAndStringifies(t *testing.T) {
	p, err := parseMap(`{"B":"x","A":1.50,"C":{"k": [1, 2]},"D":null,"E":true}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []pair{{"B", "x"}, {"A", "1.50"}, {"C", `{"k":[1,2]}`}, {"D", "null"}, {"E", "true"}}
	if len(p) != len(want) {
		t.Fatal(p)
	}
	for i := range want {
		if p[i] != want[i] {
			t.Errorf("%d: %v != %v", i, p[i], want[i])
		}
	}
}

func TestParseMapRejects(t *testing.T) {
	for _, s := range []string{`[1]`, `"x"`, `{"a":1} {"b":2}`, `{"a":`, `{"a":1}x`, ``} {
		if _, err := parseMap(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}

func TestFirstOfSkipsNullAndFalse(t *testing.T) {
	p := []pair{{"AWS_DEFAULT_REGION", "null"}, {"AWS_REGION", "eu-west-1"}}
	if got := firstOf(p, "AWS_DEFAULT_REGION", "AWS_REGION"); got != "eu-west-1" {
		t.Fatal(got)
	}
}

func TestSubjectAndXML(t *testing.T) {
	if got := subject("h.eyJzdWIiOiJyZXBvOmE6YiJ9.s"); got != "repo:a:b" {
		t.Fatal(got)
	}
	if subject("nope") != "" || subject("a.!!.c") != "" {
		t.Fatal("garbage")
	}
	f := xmlFields([]byte(`<R><Error><Code>AccessDenied</Code><Message>no</Message></Error></R>`))
	if f["Code"] != "AccessDenied" || f["Message"] != "no" {
		t.Fatal(f)
	}
}
