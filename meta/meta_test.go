package meta_test

import (
	"context"
	"errors"
	"testing"

	"github.com/wyx0k/ndrive/meta"
	"github.com/wyx0k/ndrive/permission"
)

// memStore 是 meta.Store 的内存实现，仅用于 facade 测试。
type memStore struct {
	data map[string][]byte
}

func newMemStore() *memStore { return &memStore{data: make(map[string][]byte)} }

func (m *memStore) Get(_ context.Context, key string) (meta.Record, error) {
	v, ok := m.data[key]
	if !ok {
		return meta.Record{Key: key}, nil
	}
	return meta.Record{Key: key, Value: v}, nil
}
func (m *memStore) Put(_ context.Context, key string, value []byte) error {
	m.data[key] = value
	return nil
}
func (m *memStore) Delete(_ context.Context, key string) error {
	delete(m.data, key)
	return nil
}
func (m *memStore) List(_ context.Context, prefix string) ([]meta.Record, error) {
	var out []meta.Record
	for k, v := range m.data {
		if len(prefix) == 0 || (len(k) >= len(prefix) && k[:len(prefix)] == prefix) {
			out = append(out, meta.Record{Key: k, Value: v})
		}
	}
	return out, nil
}
func (m *memStore) Close() error { return nil }

// --- keys ---

func TestKeys(t *testing.T) {
	cases := []struct {
		got, want string
	}{
		{meta.WorkspaceKey("ws1"), "workspace/ws1"},
		{meta.PermissionKey("ws1", "u1"), "permission_binding/ws1/u1"},
		{meta.PermissionPrefix("ws1"), "permission_binding/ws1/"},
		{meta.PluginKey("util.echo"), "plugin/util.echo"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

// --- WorkspaceStore ---

func TestWorkspaceStoreRegister(t *testing.T) {
	s := newMemStore()
	ws := meta.NewWorkspaceStore(s)
	ctx := context.Background()

	// 首次注册
	if err := ws.Register(ctx, "ws1", "alice"); err != nil {
		t.Fatalf("first register: %v", err)
	}
	// 二次同 userID：成功（幂等）
	if err := ws.Register(ctx, "ws1", "alice"); err != nil {
		t.Errorf("same userID: %v", err)
	}
	// 不同 userID：返 ErrWorkspaceExists
	err := ws.Register(ctx, "ws1", "bob")
	if err == nil {
		t.Fatal("want error for userID mismatch")
	}
	if !errors.Is(err, meta.ErrWorkspaceExists) {
		t.Errorf("want ErrWorkspaceExists, got %v", err)
	}
}

func TestWorkspaceStorePersistsRawUserID(t *testing.T) {
	s := newMemStore()
	ws := meta.NewWorkspaceStore(s)
	ctx := context.Background()
	if err := ws.Register(ctx, "ws", "alice"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// 验证 value 就是 userID 字符串（不是 JSON）
	rec, err := s.Get(ctx, meta.WorkspaceKey("ws"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(rec.Value) != "alice" {
		t.Errorf("value should be raw userID, got %q", rec.Value)
	}
}

// --- PermissionStore ---

func TestPermissionStoreRoundtrip(t *testing.T) {
	s := newMemStore()
	ps := meta.NewPermissionStore(s)
	ctx := context.Background()

	b := permission.Binding{WorkspaceID: "ws1", UserID: "u1", Mode: permission.W}
	if err := ps.SetBinding(ctx, b); err != nil {
		t.Fatalf("SetBinding: %v", err)
	}
	got, err := ps.GetBinding(ctx, "ws1", "u1")
	if err != nil {
		t.Fatalf("GetBinding: %v", err)
	}
	if got != b {
		t.Errorf("got %+v, want %+v", got, b)
	}
}

func TestPermissionStoreGetNotFound(t *testing.T) {
	s := newMemStore()
	ps := meta.NewPermissionStore(s)
	_, err := ps.GetBinding(context.Background(), "ws", "ghost")
	if !errors.Is(err, meta.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestPermissionStoreDelete(t *testing.T) {
	s := newMemStore()
	ps := meta.NewPermissionStore(s)
	ctx := context.Background()
	_ = ps.SetBinding(ctx, permission.Binding{WorkspaceID: "ws", UserID: "u", Mode: permission.R})
	if err := ps.DeleteBinding(ctx, "ws", "u"); err != nil {
		t.Fatalf("DeleteBinding: %v", err)
	}
	if _, err := ps.GetBinding(ctx, "ws", "u"); !errors.Is(err, meta.ErrNotFound) {
		t.Errorf("after delete: want ErrNotFound, got %v", err)
	}
}

func TestPermissionStoreList(t *testing.T) {
	s := newMemStore()
	ps := meta.NewPermissionStore(s)
	ctx := context.Background()
	_ = ps.SetBinding(ctx, permission.Binding{WorkspaceID: "ws", UserID: "a", Mode: permission.R})
	_ = ps.SetBinding(ctx, permission.Binding{WorkspaceID: "ws", UserID: "b", Mode: permission.W})
	_ = ps.SetBinding(ctx, permission.Binding{WorkspaceID: "other", UserID: "c", Mode: permission.RAsk})

	bs, err := ps.ListBindings(ctx, "ws")
	if err != nil {
		t.Fatalf("ListBindings: %v", err)
	}
	if len(bs) != 2 {
		t.Errorf("want 2, got %d", len(bs))
	}
}

func TestPermissionStoreImplementsInterface(t *testing.T) {
	// 编译期：PermissionStore 必须实现 permission.WorkspaceStore
	var _ permission.WorkspaceStore = (*meta.PermissionStore)(nil)
	// 运行时：NewPermissionStore 返非 nil
	var ps permission.WorkspaceStore = meta.NewPermissionStore(newMemStore())
	_ = ps // 类型断言已保证非 nil，调用方按需使用
}
