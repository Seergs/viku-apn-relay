package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAPNsKeyFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.p8")
	if err := os.WriteFile(path, []byte("file-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APNS_PRIVATE_KEY_FILE", path)
	t.Setenv("APNS_PRIVATE_KEY", "env-key")

	got, err := loadAPNsKey()
	if err != nil || string(got) != "file-key" {
		t.Fatalf("loadAPNsKey = %q, %v; want the file contents", got, err)
	}
}

func TestLoadAPNsKeyFromEnv(t *testing.T) {
	t.Setenv("APNS_PRIVATE_KEY_FILE", "")
	t.Setenv("APNS_PRIVATE_KEY", "env-key")

	got, err := loadAPNsKey()
	if err != nil || string(got) != "env-key" {
		t.Fatalf("loadAPNsKey = %q, %v; want the env value", got, err)
	}
}

func TestLoadAPNsKeyRequiresOne(t *testing.T) {
	t.Setenv("APNS_PRIVATE_KEY_FILE", "")
	t.Setenv("APNS_PRIVATE_KEY", "")

	if _, err := loadAPNsKey(); err == nil {
		t.Fatal("loadAPNsKey returned no error without a key")
	}
}
