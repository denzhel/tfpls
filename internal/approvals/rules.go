package approvals

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/denzhel/tfpls/internal/gh"
	"github.com/denzhel/tfpls/internal/ghactions"
	"github.com/denzhel/tfpls/internal/yamlconf"
)

// Settings are the approval settings of the repository, taken from the active rulesets of the default branch.
type Settings struct {
	Required   int
	Mode       string
	CodeOwners bool
	LastPush   bool
}

// SettingsFromRuleset reads the pull_request rules. No such rule: 1 approval of the current commit (the safe default).
func SettingsFromRuleset(rs []any) Settings {
	var ps []J
	for _, r := range rs {
		if m := obj(r); m["type"] == "pull_request" {
			ps = append(ps, obj(alt(m["parameters"], J{})))
		}
	}
	if len(ps) == 0 {
		return Settings{Required: 1, Mode: "strict"}
	}
	s := Settings{Mode: "basic"}
	for _, p := range ps {
		if n, ok := p["required_approving_review_count"].(float64); ok && int(n) > s.Required {
			s.Required = int(n)
		}
		if p["dismiss_stale_reviews_on_push"] == true {
			s.Mode = "strict"
		}
		if p["require_code_owner_review"] == true {
			s.CodeOwners = true
		}
		if p["require_last_push_approval"] == true {
			s.LastPush = true
		}
	}
	return s
}

func asObjects(v any) ([]J, bool) {
	switch t := v.(type) {
	case []any:
		out := make([]J, 0, len(t))
		for _, x := range t {
			m, ok := x.(J)
			if !ok {
				return nil, false
			}
			out = append(out, m)
		}
		return out, true
	case J:
		keys := sortedKeys(t)
		out := make([]J, 0, len(t))
		for _, k := range keys {
			m, ok := t[k].(J)
			if !ok {
				return nil, false
			}
			out = append(out, m)
		}
		return out, true
	}
	return nil, false
}

// Rules works out the approval rules of the pull request from the default branch (the command `tfpls approvals rules`).
func Rules(e Env) int {
	repo, cfgPath, branch := e.Getenv("REPO"), e.Getenv("CONFIG"), e.Getenv("DEFAULT_BRANCH")
	fail := func(msg string) int {
		fmt.Fprintf(e.Stdout, "::error::%s\n", msg)
		if e.Getenv("TFPLS_DRY") != "" {
			fmt.Fprintln(e.Stdout, msg)
			return 1
		}
		if pr := e.Getenv("PR"); pr != "" {
			_, _ = e.GH.Run("pr", "comment", pr, "--repo", repo, "--body", "⛔ **apply refused.** "+msg)
		}
		if id := e.Getenv("COMMENT_ID"); id != "" {
			_, _ = e.GH.Run("api", "-X", "POST", fmt.Sprintf("repos/%s/issues/comments/%s/reactions", repo, id), "-f", "content=-1")
		}
		return 1
	}

	// the config of the default branch, read at its tip (a stale base commit or another base branch cannot weaken it)
	var raw []byte
	have := false
	if f := e.Getenv("TFPLS_BASE_CONFIG_FILE"); f != "" {
		if f != "missing" {
			b, err := os.ReadFile(f)
			if err != nil {
				return fail("could not read `" + cfgPath + "` on the default branch (`" + branch + "`): approval rules cannot be checked.")
			}
			raw, have = b, true
		}
	} else {
		out, err := e.GH.Run("api", fmt.Sprintf("repos/%s/contents/%s?ref=%s", repo, cfgPath, url.QueryEscape(branch)))
		var ge *gh.Error
		switch {
		case err == nil:
			var c struct {
				Type    string `json:"type"`
				Content string `json:"content"`
			}
			if json.Unmarshal([]byte(out), &c) != nil || c.Type != "file" {
				return fail("the config `" + cfgPath + "` on the default branch (`" + branch + "`) is not a regular file.")
			}
			b, derr := base64.StdEncoding.DecodeString(strings.NewReplacer("\n", "", "\r", "").Replace(c.Content))
			if derr != nil {
				return fail("could not decode `" + cfgPath + "` on the default branch.")
			}
			raw, have = b, true
		case errors.As(err, &ge) && strings.Contains(ge.Stderr, "HTTP 404"):
		default:
			return fail("could not read `" + cfgPath + "` on the default branch (`" + branch + "`): approval rules cannot be checked.")
		}
	}
	base := J{}
	if have {
		cfg, err := yamlconf.Parse(raw)
		if err != nil {
			return fail("the config on the default branch is not valid YAML; merge a fix there before `/tofu apply` can check approvals.")
		}
		if cfg != nil && cfg != false {
			if m, ok := cfg.(J); ok {
				base = m
			}
		}
		if msg, ok := e.Validate(cfg); !ok {
			return fail("the config on the default branch has errors (" + msg + "); merge a fix there before `/tofu apply` can check approvals.")
		}
	}

	var headsAny any
	if err := json.Unmarshal([]byte(e.Getenv("HEAD_ENTRIES")), &headsAny); err != nil {
		return fail("could not read the pull request's workspaces.")
	}
	heads, ok := asObjects(headsAny)
	if !ok {
		return fail("could not read the pull request's workspaces.")
	}
	res := Compute(heads, base)

	rs, ok := loadRuleset(e, repo, branch)
	if !ok {
		return fail("could not read the rulesets of `" + branch + "`: the number of approvals cannot be determined.")
	}
	st := SettingsFromRuleset(rs)
	for _, list := range [][]J{res.Rules, res.Entries} {
		for _, m := range list {
			m["approvals_mode"] = st.Mode
			m["approvals_required"] = strconv.Itoa(st.Required)
		}
	}

	entries := J{}
	for _, m := range res.Entries {
		entries[str(m["label"])] = m
	}
	if out := e.Getenv("GITHUB_OUTPUT"); out != "" {
		r := &ghactions.Runner{Getenv: e.Getenv, Stdout: e.Stdout}
		for _, kv := range []struct {
			k string
			v any
		}{{"rules", res.Rules}, {"entries", entries}, {"owner_entries", res.OwnerEntries}} {
			b, _ := json.Marshal(kv.v)
			_ = r.SetOutput(kv.k, string(b))
		}
		_ = r.SetOutput("codeowners", strconv.FormatBool(st.CodeOwners))
		_ = r.SetOutput("last_push", strconv.FormatBool(st.LastPush))
	}
	if e.Getenv("TFPLS_DRY") != "" {
		b, _ := json.Marshal(J{"rules": res.Rules, "entries": res.Entries, "owner_entries": res.OwnerEntries})
		fmt.Fprintln(e.Stdout, string(b))
	}
	return 0
}

// loadRuleset reads the active rules of the default branch (TFPLS_RULESET_FILE instead of the API for tests).
func loadRuleset(e Env, repo, branch string) ([]any, bool) {
	var docs []json.RawMessage
	if f := e.Getenv("TFPLS_RULESET_FILE"); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, false
		}
		docs = []json.RawMessage{b}
	} else {
		out, err := e.GH.Run("api", "--paginate", fmt.Sprintf("repos/%s/rules/branches/%s?per_page=100", repo, url.PathEscape(branch)))
		if err != nil {
			return nil, false
		}
		if docs, err = decodeStream(out); err != nil {
			return nil, false
		}
	}
	all := []any{}
	for _, d := range docs {
		var page []any
		if json.Unmarshal(d, &page) != nil {
			return nil, false
		}
		all = append(all, page...)
	}
	return all, true
}
