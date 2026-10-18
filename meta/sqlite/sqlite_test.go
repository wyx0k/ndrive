package sqlite_test

import (
	"context"
	"testing"

	"github.com/wyx0k/ndrive/meta"
	"github.com/wyx0k/ndrive/meta/sqlite"
)

func openStore(t *testing.T) meta.Store {
	t.Helper()
	s, err := sqlite.New(context.Background(), sqlite.Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestPutGet(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	if err := s.Put(ctx, "k1", []byte("v1")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	rec, err := s.Get(ctx, "k1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(rec.Value) != "v1" {
		t.Errorf("got %q, want v1", rec.Value)
	}
}

func TestGetNotFound(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	rec, err := s.Get(ctx, "missing")
	if err != nil {
		t.Fatalf("Get missing: %v", err)
	}
	if rec.Value != nil {
		t.Errorf("missing should have nil Value, got %v", rec.Value)
	}
}

func TestPutOverwrite(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	_ = s.Put(ctx, "k", []byte("v1"))
	_ = s.Put(ctx, "k", []byte("v2"))
	rec, _ := s.Get(ctx, "k")
	if string(rec.Value) != "v2" {
		t.Errorf("overwrite failed: got %q, want v2", rec.Value)
	}
}

func TestDelete(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	_ = s.Put(ctx, "k", []byte("v"))
	if err := s.Delete(ctx, "k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	rec, _ := s.Get(ctx, "k")
	if rec.Value != nil {
		t.Error("after delete, value should be nil")
	}
}

func TestListPrefix(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	_ = s.Put(ctx, "user/1", []byte("a"))
	_ = s.Put(ctx, "user/2", []byte("b"))
	_ = s.Put(ctx, "post/1", []byte("c"))
	recs, err := s.List(ctx, "user/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(recs) != 2 {
		t.Errorf("want 2 user records, got %d", len(recs))
	}
}

func TestListAll(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	_ = s.Put(ctx, "a", []byte("1"))
	_ = s.Put(ctx, "b", []byte("2"))
	recs, err := s.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(recs) != 2 {
		t.Errorf("want 2, got %d", len(recs))
	}
}
