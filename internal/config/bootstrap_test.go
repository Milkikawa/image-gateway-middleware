package config

import "testing"

func TestEnvPort(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("TEST_PORT", "")
		got, err := envPort("TEST_PORT", 15880)
		if err != nil {
			t.Fatal(err)
		}
		if got != "15880" {
			t.Fatalf("got %q, want %q", got, "15880")
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Setenv("TEST_PORT", "24567")
		got, err := envPort("TEST_PORT", 15880)
		if err != nil {
			t.Fatal(err)
		}
		if got != "24567" {
			t.Fatalf("got %q, want %q", got, "24567")
		}
	})

	for _, value := range []string{"0", "65536", "not-a-port"} {
		t.Run("invalid_"+value, func(t *testing.T) {
			t.Setenv("TEST_PORT", value)
			if _, err := envPort("TEST_PORT", 15880); err == nil {
				t.Fatalf("expected %q to be rejected", value)
			}
		})
	}
}
