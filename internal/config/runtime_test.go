package config

import (
	"context"
	"path/filepath"
	"testing"

	"image-gateway-middleware/internal/persistence"
)

func TestRuntimeUpdateIsVersioned(t *testing.T) {
	ctx := context.Background()
	s, err := persistence.Open(ctx, filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before, err := LoadRuntime(ctx, s.DB)
	if err != nil {
		t.Fatal(err)
	}
	after, err := UpdateRuntime(ctx, s.DB, map[string]string{"download_attempts": "4", "retry_base_delay": "0s"})
	if err != nil {
		t.Fatal(err)
	}
	if after.Version != before.Version+1 || after.DownloadAttempts != 4 {
		t.Fatalf("unexpected snapshot: %+v", after)
	}
	if _, err := UpdateRuntime(ctx, s.DB, map[string]string{"unknown": "1"}); err == nil {
		t.Fatal("expected unsupported key error")
	}
}
