package storage

import (
	"errors"
	"os"
	"testing"
)

func TestFailedUpdateLeavesPersistedStateUntouched(t *testing.T) {
	path := t.TempDir() + "/state.json"
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(state *State) error {
		state.SelectedNodeID = "partial-change"
		return errors.New("abort")
	})
	if err == nil {
		t.Fatal("expected update error")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) || store.Snapshot().SelectedNodeID != "" {
		t.Fatal("failed update modified in-memory or persisted state")
	}
}

func TestStateFileHasPrivatePermissions(t *testing.T) {
	path := t.TempDir() + "/state.json"
	if _, err := Open(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %o, want 600", info.Mode().Perm())
	}
}
