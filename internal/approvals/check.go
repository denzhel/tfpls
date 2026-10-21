package approvals

import (
	"fmt"
	"strconv"
)

// Review is the part of a GitHub pull request review that the policy reads.
type Review struct {
	User        *User  `json:"user"`
	State       string `json:"state"`
	CommitID    string `json:"commit_id"`
	SubmittedAt string `json:"submitted_at"`
}

// User is a review author.
type User struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// Check decides whether the reviews satisfy the policy for the head commit sha. mode is strict (only approvals of the
// current head count) or basic (an approval counts whatever commit it was given on). In both modes a person's latest
// review decides and any outstanding "changes requested" blocks. It returns ok and the refusal reason when not.
func Check(reviews []Review, sha, mode string, need int) (bool, string) {
	if need == 0 {
		return true, ""
	}
	latest := map[string]Review{}
	for _, r := range reviews {
		if r.State != "APPROVED" && r.State != "CHANGES_REQUESTED" && r.State != "DISMISSED" {
			continue
		}
		login := ""
		if r.User != nil {
			login = r.User.Login
		}
		if cur, ok := latest[login]; !ok || r.SubmittedAt >= cur.SubmittedAt {
			latest[login] = r
		}
	}
	var head, old, changes int
	for _, r := range latest {
		switch {
		case r.State == "APPROVED" && r.CommitID == sha:
			head++
		case r.State == "APPROVED":
			old++
		case r.State == "CHANGES_REQUESTED":
			changes++
		}
	}
	if mode == "strict" {
		if changes > 0 {
			return false, fmt.Sprintf("a reviewer requested changes; apply needs %d approving review(s) of the current commit", need)
		}
		if head < need {
			if old > 0 {
				return false, fmt.Sprintf("the approval is for an older commit: new commits were pushed after the review. Ask for a new review of %s", short(sha))
			}
			return false, fmt.Sprintf("apply needs %d approving review(s) of the current commit (%s); found %d", need, short(sha), head)
		}
		return true, ""
	}
	if changes > 0 {
		return false, fmt.Sprintf("a reviewer requested changes; apply needs %d approving review(s)", need)
	}
	if total := head + old; total < need {
		return false, fmt.Sprintf("apply needs %d approving review(s); found %d", need, total)
	}
	return true, ""
}

// ParseRule validates the mode and the required count of a rule.
func ParseRule(mode, required string) (int, error) {
	if mode != "strict" && mode != "basic" {
		return 0, fmt.Errorf("unknown approval mode %q", mode)
	}
	n, err := strconv.Atoi(required)
	if err != nil || n < 0 || strconv.Itoa(n) != required {
		return 0, fmt.Errorf("invalid number of required approvals %q", required)
	}
	return n, nil
}
