package requestbody

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func ReadJSON(body io.Reader, maxBytes int64) (Audit, error) {
	raw, err := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if err != nil {
		return Audit{}, err
	}
	if int64(len(raw)) > maxBytes {
		return Audit{}, fmt.Errorf("JSON request exceeds %d bytes", maxBytes)
	}
	var value struct {
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return Audit{}, fmt.Errorf("invalid JSON request: %w", err)
	}
	return Audit{Raw: raw, Model: value.Model, Prompt: value.Prompt, Fields: map[string][]string{}}, nil
}

func Reader(a Audit) io.Reader { return bytes.NewReader(a.Raw) }
