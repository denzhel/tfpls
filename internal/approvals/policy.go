// Package approvals decides, for /tofu apply, which approval rules apply to a pull request and whether its reviews
// satisfy them. The rules come from the DEFAULT BRANCH, never from the pull request's own config.
package approvals

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/denzhel/tfpls/internal/detect"
)

// J is a JSON object.
type J = map[string]any

// alt is jq's `a // b`: b when a is null or false.
func alt(a, b any) any {
	if a == nil || a == false {
		return b
	}
	return a
}

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func merge(objs ...J) J {
	out := J{}
	for _, o := range objs {
		for k, v := range o {
			out[k] = v
		}
	}
	return out
}

func obj(v any) J {
	if m, ok := v.(J); ok {
		return m
	}
	return J{}
}

func sortedKeys(m J) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// globMatch is detect.GlobMatch: a glob that does not compile matches nothing.
func globMatch(g, s string) bool { return detect.GlobMatch(g, s) }

// normDir normalizes a directory: no empty or "." parts, ".." pops, "." when nothing is left.
func normDir(v any) string {
	var parts []string
	for _, p := range strings.Split(str(alt(v, ".")), "/") {
		switch p {
		case "", ".":
		case "..":
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		default:
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return "."
	}
	return strings.Join(parts, "/")
}

// Result is what Compute returns: rules (one per head entry x matching base stack), entries (the head entries with the
// policy settings of the base rule) and owner_entries (rules plus head entries).
type Result struct {
	Rules        []J
	Entries      []J
	OwnerEntries []J
}

// kv is one entry of an overrides map, kept in document order (the later pattern wins when several match).
type kv struct {
	key string
	val J
}

// orderedOverrides lists an overrides map in document order (keys: yamlconf.KeyOrder of that map); keys the order does
// not know follow, sorted.
func orderedOverrides(m J, keys []string) []kv {
	out := []kv{}
	seen := map[string]bool{}
	for _, k := range append(keys, sortedKeys(m)...) {
		if v, ok := m[k].(J); ok && !seen[k] {
			out = append(out, kv{k, v})
			seen[k] = true
		}
	}
	return out
}

// mergeOverrides is jq's `$top + $own`: the keys of top first, a key of own replaces the value in place, new keys follow.
func mergeOverrides(top, own []kv) []kv {
	out := append([]kv{}, top...)
	for _, o := range own {
		replaced := false
		for i := range out {
			if out[i].key == o.key {
				out[i].val, replaced = o.val, true
			}
		}
		if !replaced {
			out = append(out, o)
		}
	}
	return out
}

// Compute is policy.jq. heads are the pull request's entries, base the config of the default branch ({} when none).
// order is yamlconf.KeyOrder of the config file (nil: sorted order).
func Compute(heads []J, base J, order map[string][]string) Result {
	defaults := obj(base["defaults"])
	top := orderedOverrides(obj(base["overrides"]), order["overrides"])
	var stacks []J
	var ovs [][]kv // overrides per stack, parallel to stacks
	if raw, ok := base["stacks"].([]any); ok && base["stacks"] != nil {
		for i, s := range raw {
			sm := obj(s)
			stacks = append(stacks, sm)
			own := orderedOverrides(obj(alt(sm["overrides"], J{})), order[fmt.Sprintf("stacks/%d/overrides", i)])
			ovs = append(ovs, mergeOverrides(top, own))
		}
	} else {
		stacks = []J{{"name": "terraform", "dir": ".", "workspaces_glob": "workspaces/*", "workspace_paths": []any{"workspaces/{workspace}/"}}}
		ovs = [][]kv{top}
	}

	rule := func(t, s J, ov []kv) J {
		w := str(alt(t["workspace"], ""))
		o := J{}
		for _, e := range ov {
			if globMatch(e.key, w) || globMatch(e.key, str(alt(s["name"], ""))) {
				o = merge(o, e.val)
			}
		}
		m := merge(defaults, s, o)
		var paths []string
		if wp, ok := alt(m["workspace_paths"], nil).([]any); ok {
			for _, p := range wp {
				paths = append(paths, strings.ReplaceAll(str(p), "{workspace}", w))
			}
		} else {
			paths = []string{strings.ReplaceAll(str(alt(m["dir"], ".")), "{workspace}", w)}
		}
		packs := alt(m["policy_packs"], []any{})
		if arr, ok := packs.([]any); ok {
			var ss []string
			for _, p := range arr {
				ss = append(ss, str(p))
			}
			packs = strings.Join(ss, ",")
		}
		params := ""
		if pm := obj(alt(m["policy_params"], J{})); len(pm) > 0 {
			b, _ := json.Marshal(pm)
			params = string(b)
		}
		return J{
			"name":          alt(t["label"], t["name"]),
			"owner_paths":   strings.Join(paths, "\n"),
			"policy_dir":    alt(m["policy_dir"], ""),
			"policy_packs":  packs,
			"policy_params": params,
			"notify_secret": alt(m["notify_secret"], ""),
		}
	}
	rulesFor := func(t J) []J {
		d := normDir(t["dir"])
		w := str(alt(t["workspace"], ""))
		var cand []J
		var candOv [][]kv
		for si, s := range stacks {
			if normDir(s["dir"]) != d {
				continue
			}
			if ws, ok := s["workspaces"].([]any); ok {
				found := false
				for _, x := range ws {
					if x == any(w) {
						found = true
					}
				}
				if !found {
					continue
				}
			} else if s["workspaces"] != nil {
				continue
			}
			cand = append(cand, s)
			candOv = append(candOv, ovs[si])
		}
		if len(cand) == 0 {
			return []J{rule(t, J{"dir": t["dir"]}, top)}
		}
		var out []J
		for i, s := range cand {
			out = append(out, rule(t, s, candOv[i]))
		}
		return out
	}

	var res Result
	for _, t := range heads {
		rs := rulesFor(t)
		res.Rules = append(res.Rules, rs...)
		first := rs[0]
		res.Entries = append(res.Entries, merge(t, J{"policy_dir": first["policy_dir"], "policy_packs": first["policy_packs"], "policy_params": first["policy_params"], "notify_secret": first["notify_secret"]}))
	}
	res.OwnerEntries = append(append([]J{}, res.Rules...), heads...)
	return res
}
