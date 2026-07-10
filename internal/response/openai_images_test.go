package response

import (
	"strings"
	"testing"
)

func TestOnlyURLFieldsAreRewritten(t *testing.T) {
	raw := []byte(`{"data":[{"url":"https://cdn/a.png","revised_prompt":"https://leave"}],"other":"https://leave"}`)
	d, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	out, err := d.Rewrite(map[string]string{"https://cdn/a.png": "http://local/image"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "http://local/image") || !strings.Contains(text, "https://leave") {
		t.Fatal(text)
	}
}

func TestBase64DetectedWithoutURL(t *testing.T) {
	d, err := Parse([]byte(`{"data":[{"b64_json":"abc"}]}`))
	if err != nil || !d.HasBase64() || len(d.URLs()) != 0 {
		t.Fatalf("err=%v urls=%v", err, d.URLs())
	}
}
