package approvals

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

// GH is what the package needs from the gh CLI: the stdout of `gh <args>`.
type GH interface {
	Run(args ...string) (string, error)
}

// Env is everything the commands read from the outside, injected so that they can be tested.
type Env struct {
	Getenv func(string) string
	Stdout io.Writer
	Stderr io.Writer
	GH     GH
	// Validate checks a parsed config and returns the first error message (ok=false when there is an error).
	Validate func(cfg any) (firstError string, ok bool)
}

var loginRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// decodeStream decodes concatenated JSON documents (one per page of `gh api --paginate`).
func decodeStream(s string) ([]json.RawMessage, error) {
	var docs []json.RawMessage
	dec := json.NewDecoder(strings.NewReader(s))
	for dec.More() {
		var d json.RawMessage
		if err := dec.Decode(&d); err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}
	return docs, nil
}

// loadReviews reads the reviews of a pull request (TFPLS_REVIEWS_FILE instead of the API for tests).
func loadReviews(e Env, repo, pr string) ([]Review, error) {
	var docs []json.RawMessage
	if f := e.Getenv("TFPLS_REVIEWS_FILE"); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		docs = []json.RawMessage{b}
	} else {
		out, err := e.GH.Run("api", "--paginate", fmt.Sprintf("repos/%s/pulls/%s/reviews", repo, pr))
		if err != nil {
			return nil, err
		}
		if docs, err = decodeStream(out); err != nil {
			return nil, err
		}
	}
	var all []Review
	for _, d := range docs {
		var page []Review
		if err := json.Unmarshal(d, &page); err != nil {
			return nil, err
		}
		all = append(all, page...)
	}
	return all, nil
}

// Reviews returns the reviews that may count: only people (not bots, not deleted users) with admin or write access to
// the repository. Who was left out is returned as "login (reason)" lines. A failed lookup means not counted.
func Reviews(e Env, repo, pr string) (counted []Review, ignored []string, err error) {
	all, err := loadReviews(e, repo, pr)
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	for _, r := range all {
		if r.User != nil && r.User.Type == "User" && (r.State == "APPROVED" || r.State == "CHANGES_REQUESTED" || r.State == "DISMISSED") {
			seen[r.User.Login] = true
		}
	}
	logins := make([]string, 0, len(seen))
	for l := range seen {
		logins = append(logins, l)
	}
	sort.Strings(logins)
	ok := map[string]bool{}
	for _, l := range logins {
		if !loginRE.MatchString(l) {
			ignored = append(ignored, l+" (unusual login)")
			continue
		}
		perm, err := e.GH.Run("api", fmt.Sprintf("repos/%s/collaborators/%s/permission", repo, l), "--jq", ".permission")
		perm = strings.TrimSpace(perm)
		if err != nil || perm == "" {
			ignored = append(ignored, l+" (permission could not be checked)")
			continue
		}
		if perm == "admin" || perm == "write" {
			ok[l] = true
		} else {
			ignored = append(ignored, l+" (no write access)")
		}
	}
	for _, r := range all {
		if r.User != nil && r.User.Type == "User" && ok[r.User.Login] {
			counted = append(counted, r)
		}
	}
	return counted, ignored, nil
}
