package tools_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/wyx0k/ndrive/bus"
	"github.com/wyx0k/ndrive/permission"
	"github.com/wyx0k/ndrive/tools"
)

// --- stub helpers ---

// stubContext 实现 tools.Context，用于 dispatch 测试。
type stubContext struct {
	bus     bus.Bus
	checker permission.Checker
	api     *tools.ToolsAPI
}

func (s *stubContext) Bus() bus.Bus                { return s.bus }
func (s *stubContext) Checker() permission.Checker { return s.checker }
func (s *stubContext) API() *tools.ToolsAPI        { return s.api }

// stubChecker 实现 permission.Checker，按字段返错。
type stubChecker struct {
	readErr  error
	writeErr error
	readOps  atomic.Int32
	writeOps atomic.Int32
}

func (s *stubChecker) CheckRead(_ context.Context, _ string) error {
	s.readOps.Add(1)
	return s.readErr
}
func (s *stubChecker) CheckWrite(_ context.Context, _ string) error {
	s.writeOps.Add(1)
	return s.writeErr
}

// --- Call ---

func TestCallToolNotFound(t *testing.T) {
	api := tools.NewToolsAPI(&stubContext{bus: bus.New()})
	_, err := api.Call(context.Background(), "missing.x", nil)
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, tools.ErrToolNotFound) {
		t.Errorf("want ErrToolNotFound, got %v", err)
	}
}

func TestCallSuccess(t *testing.T) {
	b := bus.New()
	api := tools.NewToolsAPI(&stubContext{bus: b})
	if err := api.Register(&stubTool{id: "util.echo"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	v, err := api.Call(context.Background(), "util.echo", "in")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	// stubTool.Call 返 nil
	_ = v
}

// TestCallPublishEvents 验证 start / done 事件按序发到 bus。
func TestCallPublishEvents(t *testing.T) {
	b := bus.New()
	api := tools.NewToolsAPI(&stubContext{bus: b})
	if err := api.Register(&stubTool{id: "util.echo"}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	var got []tools.CallEvent
	_, unsubStart := b.Subscribe("tool.util.echo.start", func(p any) {
		got = append(got, p.(tools.CallEvent))
	})
	defer unsubStart()
	_, unsubDone := b.Subscribe("tool.util.echo.done", func(p any) {
		got = append(got, p.(tools.CallEvent))
	})
	defer unsubDone()

	if _, err := api.Call(context.Background(), "util.echo", "in"); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 events, got %d", len(got))
	}
	if got[0].Op != "start" {
		t.Errorf("first op: %q", got[0].Op)
	}
	if got[1].Op != "done" {
		t.Errorf("second op: %q", got[1].Op)
	}
	if got[0].Args != "in" {
		t.Errorf("start.Args: %v", got[0].Args)
	}
	if got[0].ToolID != "util.echo" {
		t.Errorf("event ToolID: %q", got[0].ToolID)
	}
}

// TestCallPublishErrorEvent 验证 tool 返错时发 error 事件。
func TestCallPublishErrorEvent(t *testing.T) {
	b := bus.New()
	api := tools.NewToolsAPI(&stubContext{bus: b})
	want := errors.New("tool boom")
	errDef := tools.ToolDef{
		ID_: "x.y",
		Fn_: func(_ context.Context, _ tools.Context, _ any) (any, error) {
			return nil, want
		},
	}
	if err := api.Register(errDef); err != nil {
		t.Fatalf("Register: %v", err)
	}

	var got tools.CallEvent
	_, unsub := b.Subscribe("tool.x.y.error", func(p any) {
		got = p.(tools.CallEvent)
	})
	defer unsub()

	_, err := api.Call(context.Background(), "x.y", nil)
	if !errors.Is(err, want) {
		t.Errorf("want err %v, got %v", want, err)
	}
	if got.Err == "" {
		t.Error("error event should have Err set")
	}
	if got.Op != "error" {
		t.Errorf("op: %q", got.Op)
	}
}

// TestCallReadTool 验证 Read=true 走 CheckRead。
func TestCallReadTool(t *testing.T) {
	checker := &stubChecker{}
	api := tools.NewToolsAPI(&stubContext{bus: bus.New(), checker: checker})
	if err := api.Register(&stubTool{id: "store.userdb.read", read: true}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := api.Call(context.Background(), "store.userdb.read", nil); err != nil {
		t.Errorf("Call: %v", err)
	}
	if checker.readOps.Load() != 1 {
		t.Errorf("CheckRead calls: %d", checker.readOps.Load())
	}
	if checker.writeOps.Load() != 0 {
		t.Errorf("CheckWrite should not be called: %d", checker.writeOps.Load())
	}
}

// TestCallWriteTool 验证 Read=false 走 CheckWrite。
func TestCallWriteTool(t *testing.T) {
	checker := &stubChecker{}
	api := tools.NewToolsAPI(&stubContext{bus: bus.New(), checker: checker})
	if err := api.Register(&stubTool{id: "user.draft.send", read: false}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := api.Call(context.Background(), "user.draft.send", nil); err != nil {
		t.Errorf("Call: %v", err)
	}
	if checker.writeOps.Load() != 1 {
		t.Errorf("CheckWrite calls: %d", checker.writeOps.Load())
	}
	if checker.readOps.Load() != 0 {
		t.Errorf("CheckRead should not be called: %d", checker.readOps.Load())
	}
}

// TestCallPermissionDenied 不发 start 事件（permission 在 start 前 fail）。
func TestCallPermissionDenied(t *testing.T) {
	b := bus.New()
	checker := &stubChecker{writeErr: permission.ErrPermissionDenied}
	api := tools.NewToolsAPI(&stubContext{bus: b, checker: checker})
	if err := api.Register(&stubTool{id: "user.draft.send"}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	started := atomic.Int32{}
	_, unsub := b.Subscribe("tool.user.draft.send.start", func(any) {
		started.Add(1)
	})
	defer unsub()

	_, err := api.Call(context.Background(), "user.draft.send", nil)
	if !errors.Is(err, permission.ErrPermissionDenied) {
		t.Errorf("want ErrPermissionDenied, got %v", err)
	}
	if started.Load() != 0 {
		t.Error("start event should not be published when permission denied")
	}
}

// TestCallNoChecker 不报错（permission 门控跳过）。
func TestCallNoChecker(t *testing.T) {
	api := tools.NewToolsAPI(&stubContext{bus: bus.New(), checker: nil})
	if err := api.Register(&stubTool{id: "util.echo"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := api.Call(context.Background(), "util.echo", nil); err != nil {
		t.Errorf("Call without checker: %v", err)
	}
}

// TestCallNoContext 整个 ws 为 nil：不发事件，但 tool 仍能跑。
func TestCallNoContext(t *testing.T) {
	api := tools.NewToolsAPI(nil)
	if err := api.Register(&stubTool{id: "util.echo"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := api.Call(context.Background(), "util.echo", nil); err != nil {
		t.Errorf("Call without ws: %v", err)
	}
}

// TestCallToolReceivesContext 验证 tool 收到的 ws 是 ToolsAPI 注入的同一个。
// 注意 stubContext 在 api 构造后填 api 字段（运行时绑定，Go 允许）。
func TestCallToolReceivesContext(t *testing.T) {
	ctx := &stubContext{bus: bus.New()}
	api := tools.NewToolsAPI(ctx)
	ctx.api = api // 后置绑定，模拟 ndrive.Workspace 自引用

	receivedWS := tools.Context(nil)
	if err := api.Register(tools.ToolDef{
		ID_: "x.y",
		Fn_: func(_ context.Context, ws tools.Context, _ any) (any, error) {
			receivedWS = ws
			return nil, nil
		},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := api.Call(context.Background(), "x.y", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if receivedWS == nil {
		t.Fatal("tool did not receive ws")
	}
	if receivedWS.API() != api {
		t.Error("tool's ws.API() should equal the ToolsAPI")
	}
}

// --- Tools ---

func TestToolsSorted(t *testing.T) {
	api := tools.NewToolsAPI(nil)
	_ = api.RegisterAll([]tools.Tool{
		&stubTool{id: "x.c"},
		&stubTool{id: "x.a"},
		&stubTool{id: "x.b"},
	})
	ts := api.Tools()
	if len(ts) != 3 {
		t.Fatalf("want 3 tools, got %d", len(ts))
	}
	want := []string{"x.a", "x.b", "x.c"}
	for i, w := range want {
		if ts[i].ID != tools.ToolID(w) {
			t.Errorf("tools[%d].ID = %q, want %q", i, ts[i].ID, w)
		}
	}
}

func TestToolsSnapshot(t *testing.T) {
	api := tools.NewToolsAPI(nil)
	_ = api.Register(&stubTool{id: "util.echo"})
	ts := api.Tools()
	if ts[0].Description != "util.echo desc" {
		t.Errorf("Description: %q", ts[0].Description)
	}
	if _, ok := ts[0].Schema["type"]; !ok {
		t.Error("Schema should be passed through")
	}
}

// --- 多 tool 平铺：每个 tool 独立 id ---

// TestToolsListsEveryTool 验证多个 tool 平铺注册后都被投影出来（没有容器层）。
func TestToolsListsEveryTool(t *testing.T) {
	api := tools.NewToolsAPI(nil)
	if err := api.RegisterAll([]tools.Tool{
		&stubTool{id: "store.fs.list", read: true},
		&stubTool{id: "store.fs.open", read: true, fn: func(_ context.Context, _ tools.Context, args any) (any, error) {
			return "opened:" + fmt.Sprint(args), nil
		}},
	}); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	ts := api.Tools()
	if len(ts) != 2 {
		t.Fatalf("want 2 tools, got %d: %v", len(ts), ts)
	}
	if ts[0].ID != "store.fs.list" || ts[1].ID != "store.fs.open" {
		t.Errorf("tool ids = %q %q, want store.fs.list store.fs.open", ts[0].ID, ts[1].ID)
	}
}

// TestCallEachToolIndependently 验证每个 tool 都能按自己的 id 调用。
func TestCallEachToolIndependently(t *testing.T) {
	api := tools.NewToolsAPI(nil)
	if err := api.RegisterAll([]tools.Tool{
		&stubTool{id: "store.fs.list", read: true},
		&stubTool{id: "store.fs.open", read: true, fn: func(_ context.Context, _ tools.Context, args any) (any, error) {
			return "opened:" + fmt.Sprint(args), nil
		}},
	}); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	got, err := api.Call(context.Background(), "store.fs.open", "path.txt")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got != "opened:path.txt" {
		t.Errorf("Call result = %v, want opened:path.txt", got)
	}
}
