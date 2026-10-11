package ntd_test

import (
	"context"
	"testing"

	"ntd"
	"ntd/pkg/policy"
)

func openTestWorkspace(t *testing.T) *ntd.Workspace {
	t.Helper()
	ctx := context.Background()
	ws, err := ntd.OpenWorkspace(ctx, t.Name(), "owner-test", ntd.Config{
		Meta: ntd.SqliteConfig{Path: ":memory:"},
	})
	if err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	t.Cleanup(func() { ws.Close() })
	return ws
}

func TestBusPublishSubscribe(t *testing.T) {
	ws := openTestWorkspace(t)
	bus := ws.Bus()

	received := make(chan any, 1)
	_, unsub := bus.Subscribe("test.event", func(payload any) {
		received <- payload
	})
	defer unsub()

	bus.Publish("test.event", "hello")

	select {
	case got := <-received:
		if got != "hello" {
			t.Fatalf("want %q got %v", "hello", got)
		}
	default:
		t.Fatal("no event received")
	}
}

func TestBusUnsubscribe(t *testing.T) {
	ws := openTestWorkspace(t)
	bus := ws.Bus()

	count := 0
	_, unsub := bus.Subscribe("test.unsub", func(any) { count++ })
	bus.Publish("test.unsub", nil)
	unsub()
	bus.Publish("test.unsub", nil)

	if count != 1 {
		t.Fatalf("want 1 call after unsub, got %d", count)
	}
}

func TestBusRequestNoHandler(t *testing.T) {
	ws := openTestWorkspace(t)
	_, err := ws.Bus().Request(context.Background(), "no.handler", "ping")
	if err == nil {
		t.Fatal("expected ErrNoSubscriber")
	}
}

func TestBusRequestReply(t *testing.T) {
	ws := openTestWorkspace(t)
	bus := ws.Bus()

	unsub := bus.Handle("echo", func(ctx context.Context, req any) (any, error) {
		return req, nil
	})
	defer unsub()

	resp, err := bus.Request(context.Background(), "echo", "ping")
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if resp != "ping" {
		t.Fatalf("want ping got %v", resp)
	}
}

func TestBusPolicyAskApproved(t *testing.T) {
	ctx := context.Background()
	ws, err := ntd.OpenWorkspace(ctx, t.Name(), "owner-test", ntd.Config{
		Meta: ntd.SqliteConfig{Path: ":memory:"},
	})
	if err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	defer ws.Close()

	if err := ws.SetPolicy(ctx, ntd.WAsk); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}

	bm, err := ws.CreateBizmodel(ctx, ntd.BizmodelOpts{
		ID:       "notes",
		TypeName: ntd.BackendLocalFile,
		Config:   ntd.LocalFileConfig{RootDir: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("CreateBizmodel: %v", err)
	}

	// 注册自动批准的 policy.ask handler
	unsub := ws.Bus().Handle("policy.ask", func(ctx context.Context, req any) (any, error) {
		return policy.AskResponse{Approved: true}, nil
	})
	defer unsub()

	ownerCtx := ws.Context(ctx)
	if _, err := bm.Call(ownerCtx, "dir-mkdir", "/test", nil); err != nil {
		t.Fatalf("Call dir-mkdir (approved): %v", err)
	}
}

func TestBusPolicyAskDenied(t *testing.T) {
	ctx := context.Background()
	ws, err := ntd.OpenWorkspace(ctx, t.Name(), "owner-test", ntd.Config{
		Meta: ntd.SqliteConfig{Path: ":memory:"},
	})
	if err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	defer ws.Close()

	if err := ws.SetPolicy(ctx, ntd.WAsk); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}

	bm, err := ws.CreateBizmodel(ctx, ntd.BizmodelOpts{
		ID:       "notes",
		TypeName: ntd.BackendLocalFile,
		Config:   ntd.LocalFileConfig{RootDir: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("CreateBizmodel: %v", err)
	}

	// 注册自动拒绝的 handler
	unsub := ws.Bus().Handle("policy.ask", func(ctx context.Context, req any) (any, error) {
		return policy.AskResponse{Approved: false}, nil
	})
	defer unsub()

	ownerCtx := ws.Context(ctx)
	_, err = bm.Call(ownerCtx, "dir-mkdir", "/test", nil)
	if err == nil {
		t.Fatal("expected denial")
	}
}
