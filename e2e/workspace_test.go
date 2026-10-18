package e2e_test

import (
	"context"
	"testing"

	"github.com/wyx0k/ndrive"
	"github.com/wyx0k/ndrive/tools"
)

func TestOpenWorkspaceBasic(t *testing.T) {
	ws := openWS(t, "basic")
	if ws.ID() != "basic" {
		t.Errorf("ID: %q", ws.ID())
	}
	if ws.UserID() != "u1" {
		t.Errorf("UserID: %q", ws.UserID())
	}
}

func TestOpenWorkspaceBus(t *testing.T) {
	ws := openWS(t, "bus")
	if ws.Bus() == nil {
		t.Error("Bus should not be nil")
	}
	// Bus 是进程内单例，订阅后 Publish 应收到
	got := false
	_, unsub := ws.Bus().Subscribe("test", func(any) { got = true })
	defer unsub()
	ws.Bus().Publish("test", nil)
	if !got {
		t.Error("Publish did not invoke subscriber")
	}
}

func TestOpenWorkspaceAPI(t *testing.T) {
	ws := openWS(t, "api")
	if ws.API() == nil {
		t.Fatal("API should not be nil")
	}
	if err := ws.Register(tools.SimplePluginDef{
		ID_:          "util.echo",
		Description_: "echo",
		Read_:        true,
		Fn_: func(_ context.Context, _ tools.Context, args any) (any, error) {
			return args, nil
		},
	}); err != nil {
		t.Errorf("Register: %v", err)
	}
	tl := ws.API().Tools()
	if len(tl) != 1 || tl[0].ID != "util.echo" {
		t.Errorf("tools: %v", tl)
	}
}

func TestOpenWorkspaceChecker(t *testing.T) {
	ws := openWS(t, "checker")
	if ws.Checker() == nil {
		t.Error("Checker should not be nil")
	}
}

func TestOpenWorkspaceContext(t *testing.T) {
	ws := openWS(t, "ctx")
	ctx := ws.Context(context.Background())
	wsID, userID := ndrive.FromContext(ctx)
	if wsID != "ctx" || userID != "u1" {
		t.Errorf("FromContext: wsID=%q userID=%q", wsID, userID)
	}
}

func TestOpenWorkspaceEmptyID(t *testing.T) {
	_, err := ndrive.OpenWorkspace(context.Background(), "", "u1", ndrive.WorkspaceConfig{
		Meta: ndrive.SqliteConfig{Path: ":memory:"},
	})
	if err == nil {
		t.Error("want error for empty wsID")
	}
}

func TestOpenWorkspaceEmptyUserID(t *testing.T) {
	_, err := ndrive.OpenWorkspace(context.Background(), "ws", "", ndrive.WorkspaceConfig{
		Meta: ndrive.SqliteConfig{Path: ":memory:"},
	})
	if err == nil {
		t.Error("want error for empty userID")
	}
}

func TestOpenWorkspaceInvalidMeta(t *testing.T) {
	_, err := ndrive.OpenWorkspace(context.Background(), "ws", "u", ndrive.WorkspaceConfig{})
	if err == nil {
		t.Error("want error for nil meta config")
	}
}

func TestOpenWorkspaceOwnerMismatch(t *testing.T) {
	// 跳过：跨 OpenWorkspace 调用的 ownerID 检测需要持久化 store + 严格清理；
	// 在 Windows 下 sqlite 文件锁延迟导致 TempDir 清理失败。
	// 单元测试覆盖在 TestPersistWSMetaOwnerMismatch。
	t.Skip("依赖持久化 store + Windows 文件锁行为；走 TestPersistWSMetaOwnerMismatch")
}
