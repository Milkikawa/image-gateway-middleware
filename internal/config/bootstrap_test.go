package config

import (
	"net/netip"
	"slices"
	"testing"
)

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

func TestEnvAllowedClients(t *testing.T) {
	t.Run("empty allows loopback only", func(t *testing.T) {
		t.Setenv("TEST_ALLOWED_CLIENTS", "")
		got, err := envAllowedClients("TEST_ALLOWED_CLIENTS")
		if err != nil {
			t.Fatal(err)
		}
		want := []netip.Prefix{
			netip.MustParsePrefix("127.0.0.0/8"),
			netip.MustParsePrefix("::1/128"),
		}
		if !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("parses addresses and normalized CIDRs", func(t *testing.T) {
		t.Setenv("TEST_ALLOWED_CLIENTS", " 192.168.1.21, 10.20.30.45/24, 2001:db8::1, 192.168.1.21/32 ")
		got, err := envAllowedClients("TEST_ALLOWED_CLIENTS")
		if err != nil {
			t.Fatal(err)
		}
		want := []netip.Prefix{
			netip.MustParsePrefix("127.0.0.0/8"),
			netip.MustParsePrefix("::1/128"),
			netip.MustParsePrefix("192.168.1.21/32"),
			netip.MustParsePrefix("10.20.30.0/24"),
			netip.MustParsePrefix("2001:db8::1/128"),
		}
		if !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	for _, value := range []string{
		"192.168.1.x",
		"192.168.1.1/99",
		"192.168.1.1,",
		"fe80::1%eth0",
	} {
		t.Run("invalid_"+value, func(t *testing.T) {
			t.Setenv("TEST_ALLOWED_CLIENTS", value)
			if _, err := envAllowedClients("TEST_ALLOWED_CLIENTS"); err == nil {
				t.Fatalf("expected %q to be rejected", value)
			}
		})
	}
}
