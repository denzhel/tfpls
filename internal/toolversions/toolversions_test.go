package toolversions

import "testing"

func TestNewerIsNumeric(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"1.9.0", "1.10.0", true}, // a string comparison would say false
		{"1.10.0", "1.9.0", false},
		{"1.2.3", "1.2.3", false},
		{"0.71.1", "0.71.2", true},
		{"2.0.0", "1.99.99", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestLatestSkipsDraftsPreReleasesAndOddTags(t *testing.T) {
	doc := `{"data":{"repository":{"releases":{"nodes":[
	 {"tagName":"v1.0.99","url":"u1"},{"tagName":"v1.10.0","url":"u2"},{"tagName":"v1.9.0","url":"u3"},
	 {"tagName":"v9.9.9","isDraft":true,"url":"d"},{"tagName":"v8.0.0","isPrerelease":true,"url":"p"},
	 {"tagName":"nightly","url":"n"},{"tagName":"v1.2.3-rc1","url":"rc"},null]}}}}`
	v, u, err := Latest([]byte(doc))
	if err != nil || v != "1.10.0" || u != "u2" {
		t.Fatalf("Latest = %q %q %v", v, u, err)
	}
}

func TestLatestFailsLoudly(t *testing.T) {
	for _, doc := range []string{`not json`, `{}`, `{"data":{"repository":{"releases":{"nodes":[{"tagName":"v1.0.0","isPrerelease":true}]}}}}`} {
		if _, _, err := Latest([]byte(doc)); err == nil {
			t.Errorf("Latest(%q) did not fail", doc)
		}
	}
}
