package response

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type Document struct {
	root    map[string]any
	entries []map[string]any
}

func Parse(raw []byte) (*Document, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return nil, err
	}
	data, ok := root["data"].([]any)
	if !ok {
		return &Document{root: root}, nil
	}
	d := &Document{root: root}
	for _, item := range data {
		if m, ok := item.(map[string]any); ok {
			d.entries = append(d.entries, m)
		}
	}
	return d, nil
}
func (d *Document) URLs() []string {
	var result []string
	for _, e := range d.entries {
		if u, ok := e["url"].(string); ok && u != "" {
			result = append(result, u)
		}
	}
	return result
}
func (d *Document) HasBase64() bool {
	for _, e := range d.entries {
		if v, ok := e["b64_json"].(string); ok && v != "" {
			return true
		}
	}
	return false
}
func (d *Document) Rewrite(mapping map[string]string) ([]byte, error) {
	for _, e := range d.entries {
		if u, ok := e["url"].(string); ok {
			if local, found := mapping[u]; found {
				e["url"] = local
			}
		}
	}
	out, err := json.Marshal(d.root)
	if err != nil {
		return nil, fmt.Errorf("marshal rewritten response: %w", err)
	}
	return out, nil
}
