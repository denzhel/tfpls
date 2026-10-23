package tofu

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// Counts is the number of planned changes; a replacement counts as one add and one destroy.
type Counts struct {
	Add     int `json:"add"`
	Change  int `json:"change"`
	Destroy int `json:"destroy"`
	Replace int `json:"replace"`
}

// Resource is one changed resource: its address and what happens to it (create, update, delete, replace, other). Never values.
type Resource struct {
	Address string `json:"address"`
	Action  string `json:"action"`
}

// Summary is what the run reads from a plan: counts, the header line and the changed addresses.
type Summary struct {
	Counts    Counts     `json:"counts"`
	Header    string     `json:"header"`
	Resources []Resource `json:"resources"`
}

type planJSON struct {
	ResourceChanges []struct {
		Address string `json:"address"`
		Change  struct {
			Actions []string `json:"actions"`
		} `json:"change"`
	} `json:"resource_changes"`
}

func is(acts []string, want ...string) bool {
	if len(acts) != len(want) {
		return false
	}
	for i := range want {
		if acts[i] != want[i] {
			return false
		}
	}
	return true
}

func actionOf(acts []string) string {
	s := append([]string(nil), acts...)
	sort.Strings(s)
	switch {
	case is(acts, "create"):
		return "create"
	case is(acts, "update"):
		return "update"
	case is(acts, "delete"):
		return "delete"
	case is(s, "create", "delete"):
		return "replace"
	}
	return "other"
}

// Summarize turns `tofu show -json` output into counts and the changed addresses. A plan without resource_changes is an empty
// plan; anything that is not a JSON object is an error.
func Summarize(planDoc []byte) (Summary, error) {
	var p planJSON
	if err := json.Unmarshal(planDoc, &p); err != nil {
		return Summary{}, fmt.Errorf("the plan is not valid JSON: %w", err)
	}
	s := Summary{Resources: []Resource{}}
	for _, rc := range p.ResourceChanges {
		acts := rc.Change.Actions
		if is(acts, "no-op") || is(acts, "read") {
			continue
		}
		k := actionOf(acts)
		switch k {
		case "create":
			s.Counts.Add++
		case "update":
			s.Counts.Change++
		case "delete":
			s.Counts.Destroy++
		case "replace":
			s.Counts.Add++
			s.Counts.Destroy++
			s.Counts.Replace++
		}
		s.Resources = append(s.Resources, Resource{rc.Address, k})
	}
	s.Header = fmt.Sprintf("+%d ~%d -%d", s.Counts.Add, s.Counts.Change, s.Counts.Destroy)
	if s.Counts.Replace > 0 {
		s.Header += fmt.Sprintf(" (%d replace)", s.Counts.Replace)
	}
	return s, nil
}

// additive reports whether every change of the plan is a create (a no-op or a read does not count).
func additive(planDoc []byte) bool {
	var p planJSON
	if json.Unmarshal(planDoc, &p) != nil {
		return false
	}
	for _, rc := range p.ResourceChanges {
		a := rc.Change.Actions
		if !is(a, "create") && !is(a, "no-op") && !is(a, "read") {
			return false
		}
	}
	return true
}

// compact is jq -c: JSON without spaces and without HTML escaping.
func compact(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return ""
	}
	return string(bytes.TrimRight(b.Bytes(), "\n"))
}

// Change is one entry of the `changes` output: the action as callers know it (added, changed, deleted, replaced) and the address.
type Change struct {
	Action  string `json:"action"`
	Address string `json:"address"`
}

var changeNames = map[string]string{"create": "added", "update": "changed", "delete": "deleted", "replace": "replaced"}

// Changes lists at most limit changed addresses for the `changes` output and how many more there were.
func (s Summary) Changes(limit int) (list []Change, more int) {
	list = []Change{}
	for i, r := range s.Resources {
		if i < limit {
			name, ok := changeNames[r.Action]
			if !ok {
				name = r.Action
			}
			list = append(list, Change{name, r.Address})
		}
	}
	if more = len(s.Resources) - limit; more < 0 {
		more = 0
	}
	return list, more
}
