package approvals

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Enforce checks every workspace rule of ENTRIES against the reviews of the pull request. Exit 1 = refused.
func Enforce(e Env) int {
	branch := e.Getenv("DEFAULT_BRANCH")
	if branch == "" {
		branch = "the default branch"
	}
	var rules []J
	if json.Unmarshal([]byte(e.Getenv("ENTRIES")), &rules) != nil || len(rules) == 0 {
		fmt.Fprintln(e.Stdout, "::error::no approval rules to evaluate")
		return 1
	}
	repo, pr, sha := e.Getenv("REPO"), e.Getenv("PR"), e.Getenv("SHA")

	// no review is needed anywhere (required: 0): do not look anybody up
	allZero := true
	for _, r := range rules {
		if r["approvals_required"] != "0" {
			allZero = false
		}
	}
	reviews, ignored := []Review{}, []string(nil)
	if !allZero {
		var err error
		if reviews, ignored, err = Reviews(e, repo, pr); err != nil {
			reviews, ignored = []Review{}, nil
		}
	}

	// the repository requires approval of the most recent push by somebody else: the author and committer of the head commit do not count
	if e.Getenv("LAST_PUSH") == "true" {
		pushers := e.Getenv("TFPLS_COMMIT_LOGINS")
		if pushers == "" {
			pushers, _ = e.GH.Run("api", fmt.Sprintf("repos/%s/commits/%s", repo, sha), "--jq", `[.author.login, .committer.login] | map(select(. != null)) | unique | join(" ")`)
		}
		for _, who := range strings.Fields(pushers) {
			approved := false
			for _, r := range reviews {
				if r.User != nil && r.User.Login == who && r.State == "APPROVED" {
					approved = true
				}
			}
			if !approved {
				continue
			}
			ignored = append(ignored, who+" (pushed the latest commit, which the repository does not allow to approve it)")
			kept := reviews[:0:0]
			for _, r := range reviews {
				if r.User == nil || r.User.Login != who {
					kept = append(kept, r)
				}
			}
			reviews = kept
		}
	}

	refusal := func(reason string) int {
		note := ""
		if len(ignored) > 0 {
			note = " Not counted: " + strings.Join(ignored, ", ") + "."
		}
		note += " The number of approvals comes from the repository's ruleset for `" + branch + "` (Settings, Rules; 1 when there is none). In an emergency a repository admin can comment `/tofu apply <workspace> --now`."
		full := "⛔ **apply refused.** " + reason + note
		fmt.Fprintf(e.Stdout, "::error::%s\n", reason)
		if e.Getenv("TFPLS_DRY") != "" {
			fmt.Fprintln(e.Stdout, full)
			return 1
		}
		_, _ = e.GH.Run("pr", "comment", pr, "--repo", repo, "--body", full)
		if id := e.Getenv("COMMENT_ID"); id != "" {
			_, _ = e.GH.Run("api", "-X", "POST", fmt.Sprintf("repos/%s/issues/comments/%s/reactions", repo, id), "-f", "content=-1")
		}
		return 1
	}

	for _, r := range rules {
		name := str(r["name"])
		md, rq := str(r["approvals_mode"]), str(r["approvals_required"])
		if md == "" {
			md = "strict"
		}
		if rq == "" {
			rq = "1"
		}
		need, err := ParseRule(md, rq)
		if err != nil {
			return refusal(fmt.Sprintf("`%s`: %v  _(mode `%s`, %s required)_", name, err, md, rq))
		}
		if ok, why := Check(reviews, sha, md, need); !ok {
			return refusal(fmt.Sprintf("`%s`: %s  _(mode `%s`, %s required)_", name, why, md, rq))
		}
		fmt.Fprintf(e.Stdout, "%s: approvals ok (mode %s, %s required)\n", name, md, rq)
	}
	return 0
}
