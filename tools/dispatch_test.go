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

func TestCallPluginNotFound(t *testing.T) {
	api := tools.NewToolsAPI(&stubContext{bus: bus.New()})
	_, err := api.Call(context.Background(), "missing.x", nil)
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, tools.ErrPluginNotFound) {
		t.Errorf("want ErrPluginNotFound, got %v", err)
	}
}

func TestCallSuccess(t *testing.T) {
	b := bus.New()
	api := tools.NewToolsAPI(&stubContext{bus: b})
	if err := api.Register(&stubPlugin{id: "util.echo"}, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	v, err := api.Call(context.Background(), "util.echo", "in")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	// stubPlugin.Call 返 nil
	_ = v
}

// TestCallPublishEvents 验证 start / done 事件按序发到 bus。
func TestCallPublishEvents(t *testing.T) {
	b := bus.New()
	api := tools.NewToolsAPI(&stubContext{bus: b})
	if err := api.Register(&stubPlugin{id: "util.echo"}, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}

	var got []tools.CallEvent
	_, unsubStart := b.Subscribe("plugin.util.echo.start", func(p any) {
		got = append(got, p.(tools.CallEvent))
	})
	defer unsubStart()
	_, unsubDone := b.Subscribe("plugin.util.echo.done", func(p any) {
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
	if got[0].PluginID != "util.echo" {
		t.Errorf("event PluginID: %q", got[0].PluginID)
	}
}

// TestCallPublishErrorEvent 验证 plugin 返错时发 error 事件。
func TestCallPublishErrorEvent(t *testing.T) {
	b := bus.New()
	api := tools.NewToolsAPI(&stubContext{bus: b})
	want := errors.New("plugin boom")
	errDef := tools.SimplePluginDef{
		ID_: "x.y",
		Fn_: func(_ context.Context, _ tools.Context, _ any) (any, error) {
			return nil, want
		},
	}
	if err := api.Register(errDef, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}

	var got tools.CallEvent
	_, unsub := b.Subscribe("plugin.x.y.error", func(p any) {
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

// TestCallReadPlugin 验证 Read=true 走 CheckRead。
func TestCallReadPlugin(t *testing.T) {
	checker := &stubChecker{}
	api := tools.NewToolsAPI(&stubContext{bus: bus.New(), checker: checker})
	if err := api.Register(&stubPlugin{id: "store.userdb.read", read: true}, nil); err != nil {
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

// TestCallWritePlugin 验证 Read=false 走 CheckWrite。
func TestCallWritePlugin(t *testing.T) {
	checker := &stubChecker{}
	api := tools.NewToolsAPI(&stubContext{bus: bus.New(), checker: checker})
	if err := api.Register(&stubPlugin{id: "user.draft.send", read: false}, nil); err != nil {
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
	if err := api.Register(&stubPlugin{id: "user.draft.send"}, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}

	started := atomic.Int32{}
	_, unsub := b.Subscribe("plugin.user.draft.send.start", func(any) {
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
	if err := api.Register(&stubPlugin{id: "util.echo"}, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := api.Call(context.Background(), "util.echo", nil); err != nil {
		t.Errorf("Call without checker: %v", err)
	}
}

// TestCallNoContext 整个 ws 为 nil：不发事件，但 plugin 仍能跑。
func TestCallNoContext(t *testing.T) {
	api := tools.NewToolsAPI(nil)
	if err := api.Register(&stubPlugin{id: "util.echo"}, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := api.Call(context.Background(), "util.echo", nil); err != nil {
		t.Errorf("Call without ws: %v", err)
	}
}

// TestCallPluginReceivesContext 验证 plugin 收到的 ws 是 ToolsAPI 注入的同一个。
// 注意 stubContext 在 api 构造后填 api 字段（运行时绑定，Go 允许）。
func TestCallPluginReceivesContext(t *testing.T) {
	ctx := &stubContext{bus: bus.New()}
	api := tools.NewToolsAPI(ctx)
	ctx.api = api // 后置绑定，模拟 ndrive.Workspace 自引用

	receivedWS := tools.Context(nil)
	if err := api.Register(tools.SimplePluginDef{
		ID_: "x.y",
		Fn_: func(_ context.Context, ws tools.Context, _ any) (any, error) {
			receivedWS = ws
			return nil, nil
		},
	}, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := api.Call(context.Background(), "x.y", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if receivedWS == nil {
		t.Fatal("plugin did not receive ws")
	}
	if receivedWS.API() != api {
		t.Error("plugin's ws.API() should equal the ToolsAPI")
	}
}

// --- UpdatePluginConfig ---

// configurableStub 是测试用的 ReloadablePlugin，记录最后一次配置并可配置更新失败。
type configurableStub struct {
	stubPlugin
	cfg         any
	updateErr   error
	updateCalls int
}

func (c *configurableStub) UpdateConfig(cfg any) error {
	c.updateCalls++
	if c.updateErr != nil {
		return c.updateErr
	}
	c.cfg = cfg
	return nil
}

func TestUpdatePluginConfigSuccess(t *testing.T) {
	b := bus.New()
	api := tools.NewToolsAPI(&stubContext{bus: b})
	cp := &configurableStub{stubPlugin: stubPlugin{id: "fs.watch"}}
	if err := api.Register(cp, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}

	var got tools.CallEvent
	_, unsub := b.Subscribe("plugin.fs.watch.config_updated", func(p any) {
		got = p.(tools.CallEvent)
	})
	defer unsub()

	newCfg := map[string]any{"path": "/data"}
	if err := api.UpdatePluginConfig("fs.watch", newCfg); err != nil {
		t.Fatalf("UpdatePluginConfig: %v", err)
	}
	if cp.updateCalls != 1 {
		t.Errorf("want UpdateConfig called once, got %d", cp.updateCalls)
	}
	if got.Op != "config_updated" || got.PluginID != "fs.watch" {
		t.Errorf("event: %+v", got)
	}
}

func TestUpdatePluginConfigNotFound(t *testing.T) {
	api := tools.NewToolsAPI(&stubContext{bus: bus.New()})
	err := api.UpdatePluginConfig("missing.x", nil)
	if !errors.Is(err, tools.ErrPluginNotFound) {
		t.Errorf("want ErrPluginNotFound, got %v", err)
	}
}

func TestUpdatePluginConfigNotConfigurable(t *testing.T) {
	api := tools.NewToolsAPI(&stubContext{bus: bus.New()})
	_ = api.Register(&stubPlugin{id: "util.echo"}, nil)
	err := api.UpdatePluginConfig("util.echo", nil)
	if err == nil {
		t.Fatal("want error for non-configurable plugin")
	}
}

// statefulOnlyStub 只实现 StatefulPlugin，不实现 ReloadablePlugin；
// 用于验证 UpdatePluginConfig 对只有启停钩子、没有热更新能力的 plugin 正确拒绝。
type statefulOnlyStub struct {
	stubPlugin
}

func (s *statefulOnlyStub) StartPlugin(ws tools.Context, cfg any) error { return nil }
func (s *statefulOnlyStub) StopPlugin() error                           { return nil }

func TestUpdatePluginConfigStatefulOnlyNotReloadable(t *testing.T) {
	api := tools.NewToolsAPI(&stubContext{bus: bus.New()})
	sp := &statefulOnlyStub{stubPlugin: stubPlugin{id: "fs.watch"}}
	if err := api.Register(sp, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	err := api.UpdatePluginConfig("fs.watch", "cfg")
	if err == nil {
		t.Fatal("want error: StatefulPlugin alone does not satisfy ReloadablePlugin")
	}
}

func TestUpdatePluginConfigFailureNoEvent(t *testing.T) {
	b := bus.New()
	api := tools.NewToolsAPI(&stubContext{bus: b})
	cp := &configurableStub{stubPlugin: stubPlugin{id: "fs.watch"}, updateErr: errors.New("boom")}
	_ = api.Register(cp, nil)

	fired := false
	_, unsub := b.Subscribe("plugin.fs.watch.config_updated", func(any) {
		fired = true
	})
	defer unsub()

	if err := api.UpdatePluginConfig("fs.watch", "bad"); err == nil {
		t.Fatal("want error")
	}
	if fired {
		t.Error("config_updated event should not fire on failed update")
	}
}

// --- Service ---

// servicePluginStub 实现 ServicePlugin，Service() 返回一个固定值。
type servicePluginStub struct {
	stubPlugin
	svc any
}

func (s *servicePluginStub) Service() any { return s.svc }

func TestServiceReturnsNativeValue(t *testing.T) {
	api := tools.NewToolsAPI(nil)
	native := func() string { return "hello" }
	sp := &servicePluginStub{stubPlugin: stubPlugin{id: "store.localfs"}, svc: native}
	if err := api.Register(sp, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, err := api.Service("store.localfs")
	if err != nil {
		t.Fatalf("Service: %v", err)
	}
	fn, ok := got.(func() string)
	if !ok || fn() != "hello" {
		t.Errorf("Service() = %v, want native func", got)
	}
}

func TestServiceNotFound(t *testing.T) {
	api := tools.NewToolsAPI(nil)
	_, err := api.Service("missing.x")
	if !errors.Is(err, tools.ErrPluginNotFound) {
		t.Errorf("want ErrPluginNotFound, got %v", err)
	}
}

func TestServiceNotImplemented(t *testing.T) {
	api := tools.NewToolsAPI(nil)
	_ = api.Register(&stubPlugin{id: "util.echo"}, nil)
	_, err := api.Service("util.echo")
	if !errors.Is(err, tools.ErrPluginNoService) {
		t.Errorf("want ErrPluginNoService, got %v", err)
	}
}

// --- Tools ---

func TestToolsSorted(t *testing.T) {
	api := tools.NewToolsAPI(nil)
	_ = api.RegisterAll([]tools.Plugin{
		&stubPlugin{id: "x.c"},
		&stubPlugin{id: "x.a"},
		&stubPlugin{id: "x.b"},
	})
	ts := api.Tools()
	if len(ts) != 3 {
		t.Fatalf("want 3 tools, got %d", len(ts))
	}
	want := []string{"x.a", "x.b", "x.c"}
	for i, w := range want {
		if ts[i].ID != tools.PluginID(w) {
			t.Errorf("tools[%d].ID = %q, want %q", i, ts[i].ID, w)
		}
	}
}

func TestToolsSnapshot(t *testing.T) {
	api := tools.NewToolsAPI(nil)
	_ = api.Register(&stubPlugin{id: "util.echo"}, nil)
	ts := api.Tools()
	if ts[0].Description != "util.echo desc" {
		t.Errorf("Description: %q", ts[0].Description)
	}
	if _, ok := ts[0].Schema["type"]; !ok {
		t.Error("Schema should be passed through")
	}
}

// --- Funcs ---

func TestFuncsSorted(t *testing.T) {
	api := tools.NewToolsAPI(nil)
	_ = api.RegisterAll([]tools.Plugin{
		&stubPlugin{id: "x.b"},
		&stubPlugin{id: "x.a"},
	})
	fs := api.Funcs()
	if len(fs) != 2 {
		t.Fatalf("want 2 funcs, got %d", len(fs))
	}
	if fs[0].ID != "x.a" || fs[1].ID != "x.b" {
		t.Errorf("IDs: %v %v", fs[0].ID, fs[1].ID)
	}
}

// TestFuncsBound 验证 Func.Call 走完整 Call 流程（permission + events）。
func TestFuncsBound(t *testing.T) {
	b := bus.New()
	checker := &stubChecker{}
	ctx := &stubContext{bus: b, checker: checker}
	api := tools.NewToolsAPI(ctx)
	_ = api.Register(&stubPlugin{id: "util.echo", read: true}, nil)

	fs := api.Funcs()
	if len(fs) != 1 {
		t.Fatalf("want 1 func, got %d", len(fs))
	}
	v, err := fs[0].Call(context.Background(), "arg")
	if err != nil {
		t.Errorf("Func.Call: %v", err)
	}
	_ = v
	if checker.readOps.Load() != 1 {
		t.Errorf("CheckRead should be invoked via Func.Call: %d", checker.readOps.Load())
	}
}

// --- Internal tool 可见性（多 tool 容器） ---

// newListOpenContainer 构造一个真正的多 tool 容器：list 导出，open 是
// Internal_ == true，模拟 store.localfs 的 list/open 场景，但不依赖
// plugins/localfs 包——tools 包自身需要对这个核心特性有回归保护。
func newListOpenContainer(cid tools.PluginID) *multiToolStub {
	return &multiToolStub{
		cid: cid,
		toolList: []tools.Tool{
			&stubTool{id: "list", read: true},
			&stubTool{id: "open", read: true, internal: true, fn: func(_ context.Context, _ tools.Context, args any) (any, error) {
				return "opened:" + fmt.Sprint(args), nil
			}},
		},
	}
}

func TestToolsFiltersInternal(t *testing.T) {
	api := tools.NewToolsAPI(nil)
	if err := api.Register(newListOpenContainer("store.fs"), nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	ts := api.Tools()
	if len(ts) != 1 {
		t.Fatalf("want 1 visible tool (open filtered), got %d: %v", len(ts), ts)
	}
	if ts[0].ID != "store.fs.list" {
		t.Errorf("visible tool = %q, want store.fs.list", ts[0].ID)
	}
}

func TestFuncsFiltersInternal(t *testing.T) {
	api := tools.NewToolsAPI(nil)
	if err := api.Register(newListOpenContainer("store.fs"), nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	fs := api.Funcs()
	if len(fs) != 1 {
		t.Fatalf("want 1 visible func (open filtered), got %d", len(fs))
	}
	if fs[0].ID != "store.fs.list" {
		t.Errorf("visible func = %q, want store.fs.list", fs[0].ID)
	}
}

// TestCallInternalToolStillCallable 验证 Internal tool 仍可通过完整 id Call，
// 只是不出现在 Tools()/Funcs() 投影里——Internal 是可见性开关，不是调用权限开关。
func TestCallInternalToolStillCallable(t *testing.T) {
	api := tools.NewToolsAPI(nil)
	if err := api.Register(newListOpenContainer("store.fs"), nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, err := api.Call(context.Background(), "store.fs.open", "path.txt")
	if err != nil {
		t.Fatalf("Call internal tool: %v", err)
	}
	if got != "opened:path.txt" {
		t.Errorf("Call result = %v, want opened:path.txt", got)
	}
}

// --- UpdatePluginConfig / Service 按容器 id（多 tool 容器） ---

// TestUpdatePluginConfigMultiToolContainer 验证一个两 tool 容器的配置更新是
// 按容器 id 操作、整个容器共享同一份状态，而不是绑定在某个具体 tool 上。
func TestUpdatePluginConfigMultiToolContainer(t *testing.T) {
	api := tools.NewToolsAPI(nil)
	m := newListOpenContainer("store.fs")
	if err := api.Register(m, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := api.UpdatePluginConfig("store.fs", "newcfg"); err != nil {
		t.Fatalf("UpdatePluginConfig: %v", err)
	}
	if m.updateCalls != 1 || m.cfg != "newcfg" {
		t.Errorf("want UpdateConfig called once with newcfg, got calls=%d cfg=%v", m.updateCalls, m.cfg)
	}
	// 按某个具体 tool 的完整 id 调用应该失败：UpdatePluginConfig 只认容器 id。
	if err := api.UpdatePluginConfig("store.fs.list", "x"); err == nil {
		t.Error("want error when using tool full id instead of container id")
	}
}

// TestServiceMultiToolContainer 验证 Service() 按容器 id 查找，返回整个
// 容器共享的原生能力，和具体调用哪个 tool 无关。
func TestServiceMultiToolContainer(t *testing.T) {
	api := tools.NewToolsAPI(nil)
	native := "the native fs handle"
	m := newListOpenContainer("store.fs")
	m.svc = native
	if err := api.Register(m, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, err := api.Service("store.fs")
	if err != nil {
		t.Fatalf("Service: %v", err)
	}
	if got != native {
		t.Errorf("Service() = %v, want %v", got, native)
	}
	if _, err := api.Service("store.fs.list"); err == nil {
		t.Error("want error when using tool full id instead of container id")
	}
}
