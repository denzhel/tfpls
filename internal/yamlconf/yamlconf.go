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
