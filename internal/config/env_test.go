package config

import (
	"path/filepath"
	"testing"
)

// TestLoadEnvProviderAndKey verifies the DMED_PROVIDER / DMED_API_KEY
// overrides on top of defaults (home dir is isolated so a machine-wide
// ~/.dmed.conf cannot interfere).
func TestLoadEnvProviderAndKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("USERPROFILE", dir)
	t.Setenv("HOME", dir)
	t.Setenv("DMED_PROVIDER", "deepseek")
	t.Setenv("DMED_API_KEY", "sk-env")

	cfg := Load("")
	if cfg.AI.Provider != "deepseek" {
		t.Fatalf("provider = %q, want deepseek", cfg.AI.Provider)
	}
	if cfg.AI.APIKey != "sk-env" {
		t.Fatalf("api_key = %q, want sk-env", cfg.AI.APIKey)
	}
	if filepath.Base(cfg.AI.OllamaURL) == "" {
		t.Fatal("unreachable")
	}
}
