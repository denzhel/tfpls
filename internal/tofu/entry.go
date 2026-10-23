package tofu

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Settings are the per-workspace settings of one run.
type Settings struct {
	Name, Label, Dir, Workspace, VarFile, Parallelism, PlanLock, LockTimeout string
	AutoApply, PolicyDir, PolicyPacks, PolicyParams                          string
}

// settingKeys maps each setting to its variable in the environment and its key in the matrix entry.
var settingKeys = []struct {
	env, key string
	field    func(*Settings) *string
}{
	{"NAME", "name", func(s *Settings) *string { return &s.Name }},
	{"LABEL", "label", func(s *Settings) *string { return &s.Label }},
	{"DIR", "dir", func(s *Settings) *string { return &s.Dir }},
	{"WS", "workspace", func(s *Settings) *string { return &s.Workspace }},
	{"VAR_FILE", "var_file", func(s *Settings) *string { return &s.VarFile }},
	{"PARALLELISM", "parallelism", func(s *Settings) *string { return &s.Parallelism }},
	{"PLAN_LOCK", "plan_lock", func(s *Settings) *string { return &s.PlanLock }},
	{"LOCK_TIMEOUT", "lock_timeout", func(s *Settings) *string { return &s.LockTimeout }},
	{"AUTO_APPLY", "auto_apply", func(s *Settings) *string { return &s.AutoApply }},
	{"POLICY_DIR", "policy_dir", func(s *Settings) *string { return &s.PolicyDir }},
	{"POLICY_PACKS", "policy_packs", func(s *Settings) *string { return &s.PolicyPacks }},
	{"POLICY_PARAMS", "policy_params", func(s *Settings) *string { return &s.PolicyParams }},
}

// entryText is `.[$k] // "" | tostring` of the former shell version: null and false are "", a string is itself, a number or true its JSON text and a
// structure its compact JSON.
func entryText(raw json.RawMessage) string {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&v) != nil {
		return ""
	}
	switch t := v.(type) {
	case nil:
		return ""
	case bool:
		if !t {
			return ""
		}
		return "true"
	case string:
		return t
	case json.Number:
		return t.String()
	}
	return compact(v)
}

// LoadEntry fills the settings: a value set in the environment wins (drift passes its own auto_apply); every empty one gets its
// value from the matrix entry (the JSON of the entry from detect-workspaces). An entry that is not a JSON object is an error:
// the Bash version ignored it silently and went on with empty settings (an empty directory is the repository root).
func LoadEntry(getenv func(string) string, entry string) (Settings, error) {
	var s Settings
	var m map[string]json.RawMessage
	if entry != "" {
		if err := json.Unmarshal([]byte(entry), &m); err != nil || m == nil {
			return s, fmt.Errorf("the entry is not a JSON object")
		}
	}
	for _, k := range settingKeys {
		p := k.field(&s)
		if *p = getenv(k.env); *p != "" {
			continue
		}
		if raw, ok := m[k.key]; ok {
			*p = entryText(raw)
		}
	}
	return s, nil
}
