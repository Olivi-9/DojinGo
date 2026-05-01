package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadNewConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
bot:
  token: token
  admins: [1, 2]
telegraph:
  tokens: [tg-token]
storage:
  type: memory
whitelist:
  enabled: true
  ids: [1]
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Bot.Token != "token" {
		t.Fatalf("expected bot token to be normalized, got %q", cfg.Bot.Token)
	}
	if cfg.Storage.Path == "" {
		t.Fatal("expected storage path default to be set")
	}
	if !cfg.IsAllowedUser(1) || cfg.IsAllowedUser(99) {
		t.Fatal("whitelist evaluation did not match config")
	}
}

func TestLoadLegacyConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
base:
  bot_token: token
  admins: [123]
  telegraph:
    tokens: [tg-token]
http:
  ipv6_prefix: "2001:db8::/64"
exhentai:
  ipb_pass_hash: a
  ipb_member_id: b
  igneous: c
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Bot.Token != "token" {
		t.Fatalf("expected legacy bot token to be mapped, got %q", cfg.Bot.Token)
	}
	if cfg.IPv6.Prefix != "2001:db8::/64" {
		t.Fatalf("expected ipv6 prefix mapping, got %q", cfg.IPv6.Prefix)
	}
	if cfg.Collectors.Exhentai.Igneous != "c" {
		t.Fatalf("expected legacy exhentai config to be mapped, got %#v", cfg.Collectors.Exhentai)
	}
}
