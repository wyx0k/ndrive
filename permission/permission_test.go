package permission_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/wyx0k/ndrive/bus"
	"github.com/wyx0k/ndrive/permission"
)

// memStore 是 WorkspaceStore 的内存实现，专门用于测试。
type memStore struct {
	mu       sync.RWMutex
	bindings map[string]permission.Binding // key = wsID + "\x00" + userID
}

func newMemStore() *memStore {
	return &memStore{bindings: make(map[string]permission.Binding)}
}

func storeKey(wsID, userID string) string { return wsID + "\x00" + userID }

func (m *memStore) GetBinding(_ context.Context, wsID, userID string) (permission.Binding, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.bindings[storeKey(wsID, userID)]
	if !ok {
		return permission.Binding{}, errors.New("permission: binding not found")
	}
	return b, nil
}

func (m *memStore) SetBinding(_ context.Context, b permission.Binding) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bindings[storeKey(b.WorkspaceID, b.UserID)] = b
	return nil
}

func (m *memStore) DeleteBinding(_ context.Context, wsID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.bindings, storeKey(wsID, userID))
	return nil
}

func (m *memStore) ListBindings(_ context.Context, wsID string) ([]permission.Binding, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []permission.Binding
	for _, b := range m.bindings {
		if b.WorkspaceID == wsID {
			out = append(out, b)
		}
	}
	return out, nil
}

// helper：构造一个 ws，并预置一个 binding
func newChecker(t *testing.T, mode permission.Mode) (permission.Checker, *memStore, bus.Bus) {
	t.Helper()
	store := newMemStore()
	if err := store.SetBinding(context.Background(), permission.Binding{
		WorkspaceID: "ws1", UserID: "u1", Mode: mode,
	}); err != nil {
		t.Fatalf("SetBinding: %v", err)
	}
	b := bus.New()
	return permission.NewChecker(store, "ws1", "u1", b), store, b
}

// --- Mode ---

func TestModeString(t *testing.T) {
	cases := []struct {
		m    permission.Mode
		want string
	}{
		{permission.RAsk, "RAsk"},
		{permission.R, "R"},
		{permission.WAsk, "WAsk"},
		{permission.W, "W"},
	}
	for _, c := range cases {
		if got := c.m.String(); got != c.want {
			t.Errorf("Mode(%d).String() = %q, want %q", c.m, got, c.want)
		}
	}
}

func TestModeJSONRoundtrip(t *testing.T) {
	cases := []permission.Mode{permission.RAsk, permission.R, permission.WAsk, permission.W}
	for _, m := range cases {
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("Marshal(%d): %v", m, err)
		}
		var got permission.Mode
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("Unmarshal(%s): %v", data, err)
		}
		if got != m {
			t.Errorf("roundtrip: want %d, got %d", m, got)
		}
	}
}

func TestBindingJSON(t *testing.T) {
	b := permission.Binding{WorkspaceID: "ws", UserID: "u", Mode: permission.WAsk}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"workspace_id":"ws","user_id":"u","mode":2}`
	if string(data) != want {
		t.Errorf("Marshal = %s, want %s", data, want)
	}
}

// --- 决策矩阵：4 mode × 2 op 类型 ---

func TestMatrixR(t *testing.T) {
	c, _, _ := newChecker(t, permission.R)
	if err := c.CheckRead(context.Background(), "r"); err != nil {
		t.Errorf("R+read: want nil, got %v", err)
	}
	if err := c.CheckWrite(context.Background(), "w"); err == nil {
		t.Error("R+write: want error, got nil")
	} else if !errors.Is(err, permission.ErrPermissionDenied) {
		t.Errorf("R+write: want ErrPermissionDenied, got %v", err)
	}
}

func TestMatrixRAsk(t *testing.T) {
	c, _, _ := newChecker(t, permission.RAsk)
	if err := c.CheckRead(context.Background(), "r"); err != nil {
		t.Errorf("RAsk+read: want nil, got %v", err)
	}
	// RAsk + write 应该走 ask；没注册 handler → ErrPermissionDenied
	if err := c.CheckWrite(context.Background(), "w"); err == nil {
		t.Error("RAsk+write: want error (no subscriber), got nil")
	} else if !errors.Is(err, permission.ErrPermissionDenied) {
		t.Errorf("RAsk+write: want ErrPermissionDenied, got %v", err)
	}
}

func TestMatrixW(t *testing.T) {
	c, _, _ := newChecker(t, permission.W)
	if err := c.CheckWrite(context.Background(), "w"); err != nil {
		t.Errorf("W+write: want nil, got %v", err)
	}
	if err := c.CheckRead(context.Background(), "r"); err == nil {
		t.Error("W+read: want error, got nil")
	} else if !errors.Is(err, permission.ErrPermissionDenied) {
		t.Errorf("W+read: want ErrPermissionDenied, got %v", err)
	}
}

func TestMatrixWAsk(t *testing.T) {
	c, _, _ := newChecker(t, permission.WAsk)
	// WAsk 对读也走 ask
	if err := c.CheckRead(context.Background(), "r"); err == nil {
		t.Error("WAsk+read: want error (no subscriber), got nil")
	} else if !errors.Is(err, permission.ErrPermissionDenied) {
		t.Errorf("WAsk+read: want ErrPermissionDenied, got %v", err)
	}
	if err := c.CheckWrite(context.Background(), "w"); err == nil {
		t.Error("WAsk+write: want error (no subscriber), got nil")
	} else if !errors.Is(err, permission.ErrPermissionDenied) {
		t.Errorf("WAsk+write: want ErrPermissionDenied, got %v", err)
	}
}

// --- ask 路径：成功 / 拒绝 / 无订阅者 / 超时 / bad response ---

// 装一个 ask handler 返 AskResponse{Approved: true}
func installAskApprover(b bus.Bus) (func(), error) {
	return b.Handle("permission.ask", func(ctx context.Context, req any) (any, error) {
		// 校验 payload 是 AskPayload
		if _, ok := req.(permission.AskPayload); !ok {
			return nil, errors.New("bad payload type")
		}
		return permission.AskResponse{Approved: true}, nil
	})
}

func TestAskApproved(t *testing.T) {
	c, _, b := newChecker(t, permission.RAsk)
	unsub, err := installAskApprover(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	defer unsub()

	if err := c.CheckWrite(context.Background(), "w"); err != nil {
		t.Errorf("CheckWrite with approver: want nil, got %v", err)
	}
}

func TestAskDenied(t *testing.T) {
	c, _, b := newChecker(t, permission.RAsk)
	unsub, err := b.Handle("permission.ask", func(ctx context.Context, req any) (any, error) {
		return permission.AskResponse{Approved: false}, nil
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	defer unsub()

	if err := c.CheckWrite(context.Background(), "w"); err == nil {
		t.Fatal("want ErrPermissionDenied, got nil")
	} else if !errors.Is(err, permission.ErrPermissionDenied) {
		t.Errorf("want ErrPermissionDenied, got %v", err)
	}
}

func TestAskNoSubscriber(t *testing.T) {
	c, _, _ := newChecker(t, permission.RAsk) // no handler installed
	if err := c.CheckWrite(context.Background(), "w"); err == nil {
		t.Fatal("want ErrPermissionDenied (no subscriber), got nil")
	} else if !errors.Is(err, permission.ErrPermissionDenied) {
		t.Errorf("want ErrPermissionDenied, got %v", err)
	}
}

// handler 内部 listen ctx 取消 → permission 收到 ErrRequestTimeout → 转 ErrPermissionDenied
func TestAskTimeout(t *testing.T) {
	c, _, b := newChecker(t, permission.RAsk)
	unsub, err := b.Handle("permission.ask", func(ctx context.Context, req any) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	defer unsub()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := c.CheckWrite(ctx, "w"); err == nil {
		t.Fatal("want ErrPermissionDenied (timeout), got nil")
	} else if !errors.Is(err, permission.ErrPermissionDenied) {
		t.Errorf("want ErrPermissionDenied, got %v", err)
	}
}

// handler 返非 AskResponse → 转 ErrPermissionDenied
func TestAskBadResponseType(t *testing.T) {
	c, _, b := newChecker(t, permission.RAsk)
	unsub, err := b.Handle("permission.ask", func(ctx context.Context, req any) (any, error) {
		return "not an AskResponse", nil
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	defer unsub()

	if err := c.CheckWrite(context.Background(), "w"); err == nil {
		t.Fatal("want ErrPermissionDenied (bad type), got nil")
	} else if !errors.Is(err, permission.ErrPermissionDenied) {
		t.Errorf("want ErrPermissionDenied, got %v", err)
	}
}

// handler 返错（不是 ctx cancel）→ permission 透传为 ErrPermissionDenied
func TestAskHandlerError(t *testing.T) {
	c, _, b := newChecker(t, permission.RAsk)
	want := errors.New("ui crash")
	unsub, err := b.Handle("permission.ask", func(ctx context.Context, req any) (any, error) {
		return nil, want
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	defer unsub()

	if err := c.CheckWrite(context.Background(), "w"); err == nil {
		t.Fatal("want error, got nil")
	} else if !errors.Is(err, permission.ErrPermissionDenied) {
		t.Errorf("want ErrPermissionDenied, got %v", err)
	}
}

// v5 关键：AskPayload.Preview 允许空（permission 包传空串，handler 不报错）
func TestAskPayloadPreviewEmpty(t *testing.T) {
	c, _, b := newChecker(t, permission.RAsk)
	var got permission.AskPayload
	unsub, err := b.Handle("permission.ask", func(ctx context.Context, req any) (any, error) {
		got = req.(permission.AskPayload)
		return permission.AskResponse{Approved: true}, nil
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	defer unsub()

	if err := c.CheckWrite(context.Background(), "my.op"); err != nil {
		t.Fatalf("CheckWrite: %v", err)
	}
	if got.Preview != "" {
		t.Errorf("Preview want empty, got %q", got.Preview)
	}
	if got.Op != "my.op" {
		t.Errorf("Op want my.op, got %q", got.Op)
	}
	if got.UserID != "u1" || got.WorkspaceID != "ws1" {
		t.Errorf("AskPayload fields wrong: %+v", got)
	}
}

// --- 隔离：不同 user 不同 mode ---

func TestMultipleUsers(t *testing.T) {
	store := newMemStore()
	_ = store.SetBinding(context.Background(), permission.Binding{WorkspaceID: "ws", UserID: "alice", Mode: permission.W})
	_ = store.SetBinding(context.Background(), permission.Binding{WorkspaceID: "ws", UserID: "bob", Mode: permission.R})
	b := bus.New()

	alice := permission.NewChecker(store, "ws", "alice", b)
	bob := permission.NewChecker(store, "ws", "bob", b)

	if err := alice.CheckWrite(context.Background(), "w"); err != nil {
		t.Errorf("alice W+write: want nil, got %v", err)
	}
	if err := bob.CheckWrite(context.Background(), "w"); err == nil {
		t.Error("bob R+write: want error, got nil")
	}
	if err := bob.CheckRead(context.Background(), "r"); err != nil {
		t.Errorf("bob R+read: want nil, got %v", err)
	}
}

// --- WorkspaceStore 行为 ---

func TestWorkspaceStoreList(t *testing.T) {
	store := newMemStore()
	_ = store.SetBinding(context.Background(), permission.Binding{WorkspaceID: "ws", UserID: "a", Mode: permission.R})
	_ = store.SetBinding(context.Background(), permission.Binding{WorkspaceID: "ws", UserID: "b", Mode: permission.W})
	_ = store.SetBinding(context.Background(), permission.Binding{WorkspaceID: "other", UserID: "c", Mode: permission.WAsk})

	bs, err := store.ListBindings(context.Background(), "ws")
	if err != nil {
		t.Fatalf("ListBindings: %v", err)
	}
	if len(bs) != 2 {
		t.Errorf("want 2 bindings, got %d", len(bs))
	}
}

func TestWorkspaceStoreDelete(t *testing.T) {
	store := newMemStore()
	_ = store.SetBinding(context.Background(), permission.Binding{WorkspaceID: "ws", UserID: "u", Mode: permission.R})
	if err := store.DeleteBinding(context.Background(), "ws", "u"); err != nil {
		t.Fatalf("DeleteBinding: %v", err)
	}
	_, err := store.GetBinding(context.Background(), "ws", "u")
	if err == nil {
		t.Error("want error after delete, got nil")
	}
}
