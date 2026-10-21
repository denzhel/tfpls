// Package yamlconf reads YAML into plain JSON-compatible values: what `yq -o=json '.' file` gave the shell scripts.
package yamlconf

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"go.yaml.in/yaml/v3"
)

// Parse decodes the first YAML document: maps become map[string]any, lists []any, scalars string, bool, int, float64 or nil.
// Anchors, aliases and merge keys are resolved. An empty file is nil. More than one document is an error (a config is one).
func Parse(data []byte) (any, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var v any
	if err := dec.Decode(&v); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return nil, errors.New("more than one YAML document")
	} else if !errors.Is(err, io.EOF) {
		return nil, err
	}
	return normalize(v)
}

// ToJSON is Parse followed by compact JSON (`null` for an empty file).
func ToJSON(data []byte) ([]byte, error) {
	v, err := Parse(data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// LoadFile reads and parses a file.
func LoadFile(path string) (any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// KeyOrder returns, for every mapping of the first document, its keys in document order. The map key is the path of the
// mapping as a slash-joined list of keys and list indexes ("" for the root, "stacks/0/overrides"). Parse returns Go maps, which
// lose the order; callers that need it (the later pattern of an overrides map wins) look it up here. Aliases are followed; keys
// that come in through a merge key are not listed (the caller appends what is missing, sorted).
func KeyOrder(data []byte) (map[string][]string, error) {
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return map[string][]string{}, nil
		}
		return nil, err
	}
	out := map[string][]string{}
	walkOrder(&doc, "", out, 0)
	return out, nil
}

func walkOrder(n *yaml.Node, path string, out map[string][]string, depth int) {
	if n == nil || depth > 64 {
		return
	}
	join := func(p, k string) string {
		if p == "" {
			return k
		}
		return p + "/" + k
	}
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			walkOrder(c, path, out, depth+1)
		}
	case yaml.AliasNode:
		walkOrder(n.Alias, path, out, depth+1)
	case yaml.SequenceNode:
		for i, c := range n.Content {
			walkOrder(c, join(path, fmt.Sprint(i)), out, depth+1)
		}
	case yaml.MappingNode:
		keys := []string{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Tag == "!!merge" {
				continue
			}
			keys = append(keys, k.Value)
			walkOrder(n.Content[i+1], join(path, k.Value), out, depth+1)
		}
		out[path] = keys
	}
}

// normalize makes every map key a string (YAML allows other keys; JSON does not) and every timestamp its text.
func normalize(v any) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			n, err := normalize(x)
			if err != nil {
				return nil, err
			}
			t[k] = n
		}
		return t, nil
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			n, err := normalize(x)
			if err != nil {
				return nil, err
			}
			ks := fmt.Sprint(k)
			if _, dup := out[ks]; dup {
				return nil, fmt.Errorf("two keys become %q", ks)
			}
			out[ks] = n
		}
		return out, nil
	case []any:
		for i, x := range t {
			n, err := normalize(x)
			if err != nil {
				return nil, err
			}
			t[i] = n
		}
		return t, nil
	case time.Time:
		return t.Format(time.RFC3339), nil
	}
	return v, nil
}
