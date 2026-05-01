package storage

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStoreTTL(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore(8)
	ctx := context.Background()
	if err := store.Set(ctx, "key", "value", 20*time.Millisecond); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	value, ok, err := store.Get(ctx, "key")
	if err != nil || !ok || value != "value" {
		t.Fatalf("Get() before expiration = (%q, %v, %v)", value, ok, err)
	}

	time.Sleep(35 * time.Millisecond)
	_, ok, err = store.Get(ctx, "key")
	if err != nil {
		t.Fatalf("Get() after expiration error = %v", err)
	}
	if ok {
		t.Fatal("expected entry to expire")
	}
}

func TestFileStoreRoundTrip(t *testing.T) {
	t.Parallel()

	store, err := NewFileStore(t.TempDir(), 8)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	ctx := context.Background()
	if err := store.Set(ctx, "key", "value", time.Minute); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	value, ok, err := store.Get(ctx, "key")
	if err != nil || !ok || value != "value" {
		t.Fatalf("Get() = (%q, %v, %v)", value, ok, err)
	}
	if err := store.Delete(ctx, "key"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	_, ok, err = store.Get(ctx, "key")
	if err != nil {
		t.Fatalf("Get() after delete error = %v", err)
	}
	if ok {
		t.Fatal("expected entry to be deleted")
	}
}
