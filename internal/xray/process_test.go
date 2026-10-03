package xray

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestApplyRollsBackWhenXrayCannotStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a small POSIX test executable")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "xray-stub")
	script := "#!/bin/sh\nif [ \"$2\" = \"-test\" ]; then exit 0; fi\nexit 1\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "xray.json")
	controller := NewController(binary, configPath)
	err := controller.Apply(context.Background(), []byte(`{"log":{"loglevel":"warning"}}`))
	if err == nil || !strings.Contains(err.Error(), "previous config restored") {
		t.Fatalf("Apply() error = %v, want startup rollback error", err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("failed first apply left an active config file: %v", err)
	}
}

func TestApplyRestoresPreviousRunningConfigAfterFailedReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a small POSIX test executable")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "xray-stub")
	script := "#!/bin/sh\nif [ \"$2\" = \"-test\" ]; then exit 0; fi\nif grep -q '\"fail\":true' \"$3\"; then exit 1; fi\nexec sleep 30\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "xray.json")
	controller := NewController(binary, configPath)
	previous := []byte(`{"ok":true}`)
	if err := controller.Apply(context.Background(), previous); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}
	t.Cleanup(func() { _ = controller.Stop() })
	if !controller.Status().Running {
		t.Fatal("initial Xray process is not running")
	}
	err := controller.Apply(context.Background(), []byte(`{"fail":true}`))
	if err == nil {
		t.Fatal("expected replacement startup failure")
	}
	installed, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(installed) != string(previous) {
		t.Fatalf("active config = %s, want restored prior config %s", installed, previous)
	}
	if !controller.Status().Running {
		t.Fatal("prior Xray process was not restored after failed replacement")
	}
}
