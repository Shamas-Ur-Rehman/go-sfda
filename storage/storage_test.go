package storage

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestJSONCheckpointStoreRoundTripAndReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "checkpoint.json")
	store, err := NewJSONCheckpointStore(path)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := NewCheckpoint()
	checkpoint.QueryFingerprint = "query-hash"
	inProgress := 4
	checkpoint.InProgressPage = &inProgress
	checkpoint.FailedPages = []int{4, 2}
	if err := store.Save(checkpoint); err != nil {
		t.Fatal(err)
	}

	checkpoint.LastCompletedPage = 3
	checkpoint.InProgressPage = nil
	checkpoint.FailedPages = []int{4}
	if err := store.Save(checkpoint); err != nil {
		t.Fatalf("replace existing checkpoint: %v", err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.LastCompletedPage != 3 || got.QueryFingerprint != "query-hash" ||
		got.InProgressPage != nil || !reflect.DeepEqual(got.FailedPages, []int{4}) || got.UpdatedAt.IsZero() {
		t.Fatalf("unexpected checkpoint: %+v", got)
	}
}

func TestJSONCheckpointStoreMissingFileStartsFresh(t *testing.T) {
	store, err := NewJSONCheckpointStore(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Version != 1 || checkpoint.LastCompletedPage != -1 {
		t.Fatalf("unexpected initial checkpoint: %+v", checkpoint)
	}
}

func TestJSONCheckpointStoreReportsCorruptionAndUnsupportedVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	store, err := NewJSONCheckpointStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, contents := range []string{
		`{"version":`,
		`{"version":99,"last_completed_page":0}`,
	} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Load(); err == nil {
			t.Fatalf("expected checkpoint contents %q to fail validation", contents)
		}
	}
}

func TestJSONCheckpointStoreRejectsInvalidPageValues(t *testing.T) {
	store, err := NewJSONCheckpointStore(filepath.Join(t.TempDir(), "checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := NewCheckpoint()
	checkpoint.FailedPages = []int{1, 1}
	if err := store.Save(checkpoint); err == nil {
		t.Fatal("expected duplicate failed pages to be rejected")
	}
}
