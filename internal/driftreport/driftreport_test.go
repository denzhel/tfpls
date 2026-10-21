package driftreport

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFileName(t *testing.T) {
	for in, want := range map[string]string{
		"terraform/dev": "drift/terraform_dev.json",
		"a b/c":         "drift/a_b_c.json",
		"../x\n;rm":     "drift/.._xrm.json",
		"é/ü":           "drift/_.json",
	} {
		if got := fileName(in); got != want {
			t.Errorf("%q: %q want %q", in, got, want)
		}
	}
}

func TestPlanLine(t *testing.T) {
	if planLine("a\nPlan: 1 to add.\nPlan: 2 to add.\nxPlan: 3\n") != "2 to add." || planLine("none") != "" {
		t.Fatal("plan line")
	}
}

func TestClipPlan(t *testing.T) {
	short := "é plan\n"
	if clipPlan(short, 45000) != short {
		t.Fatal("short plan changed")
	}
	big := strings.Repeat("é", 40000) + "\nPlan: 1 to add.\n" // 80,000+ bytes
	got := clipPlan(big, 45000)
	if !utf8.ValidString(got) || !strings.Contains(got, "bytes omitted") || !strings.HasSuffix(got, "Plan: 1 to add.\n") || len(got) > 45300 {
		t.Fatal(len(got), utf8.ValidString(got))
	}
}
