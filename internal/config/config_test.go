package config

import (
	"strings"
	"testing"
)

func TestServerSecret(t *testing.T) {
	for _, secret := range []string{"", "change-me-in-production", "short", strings.Repeat(" ", 64), "change-me-in-production-change-me-in-production"} {
		t.Setenv("SESSION_SECRET", secret)
		if Load().ValidateServer() == nil {
			t.Errorf("accepted unsafe secret %q", secret)
		}
	}
	t.Setenv("SESSION_SECRET", "96ffbda71aa643a4aee8907ef1f7140d94ae9ed782f004298b959fa787f34084")
	if err := Load().ValidateServer(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDoesNotRequireServerSecret(t *testing.T) {
	t.Setenv("SESSION_SECRET", "")
	cfg := Load()
	if cfg.SessionSecret != "" || cfg.DatabaseDSN == "" {
		t.Fatal("rate importers must load database configuration without a session secret")
	}
}
