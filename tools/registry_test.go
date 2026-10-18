package tools_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wyx0k/ndrive/tools"
)

// stubPlugin 是测试用的最小 plugin。
type stubPlugin struct {
	id        tools.PluginID
	read      bool
	dependsOn []tools.PluginID
}

func (s *stubPlugin) ID() tools.PluginID          { return s.id }
func (s *stubPlugin) Description() string         { return s.id + " desc" }
func (s *stubPlugin) Schema() map[string]any      { return map[string]any{"type": "object"} }
func (s *stubPlugin) DependsOn() []tools.PluginID { return s.dependsOn }
func (s *stubPlugin) Read() bool                  { return s.read }
func (s *stubPlugin) Call(_ context.Context, _ tools.Context, _ any) (any, error) {
	return nil, nil
}

func newAPI() *tools.ToolsAPI {
	return tools.NewToolsAPI(nil)
}

// --- Register ---

func TestRegisterSuccess(t *testing.T) {
	api := newAPI()
	if err := api.Register(&stubPlugin{id: "util.echo"}, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got := api.Plugins()[`util.echo`]; got == nil {
		t.Fatal("plugin not stored")
	}
}

func TestRegisterNilPlugin(t *testing.T) {
	api := newAPI()
	if err := api.Register(nil, nil); err == nil {
		t.Fatal("want error for nil plugin")
	} else if !errors.Is(err, tools.ErrPluginInvalid) {
		t.Errorf("want ErrPluginInvalid, got %v", err)
	}
}

func TestRegisterInvalidID(t *testing.T) {
	api := newAPI()
	if err := api.Register(&stubPlugin{id: "single"}, nil); err == nil {
		t.Fatal("want error for single-segment id")
	} else if !errors.Is(err, tools.ErrPluginInvalid) {
		t.Errorf("want ErrPluginInvalid, got %v", err)
	}
}

func TestRegisterDuplicate(t *testing.T) {
	api := newAPI()
	_ = api.Register(&stubPlugin{id: "util.echo"}, nil)
	if err := api.Register(&stubPlugin{id: "util.echo"}, nil); err == nil {
		t.Fatal("want duplicate error")
	} else if !errors.Is(err, tools.ErrPluginDuplicate) {
		t.Errorf("want ErrPluginDuplicate, got %v", err)
	}
}

func TestRegisterMissingDep(t *testing.T) {
	api := newAPI()
	// a 依赖 b，但 b 未注册
	err := api.Register(&stubPlugin{
		id:        "a.b",
		dependsOn: []tools.PluginID{"missing.thing"},
	}, nil)
	if err == nil {
		t.Fatal("want missing dep error")
	}
	if !errors.Is(err, tools.ErrPluginNotFound) {
		t.Errorf("want ErrPluginNotFound, got %v", err)
	}
}

func TestRegisterDepRegistered(t *testing.T) {
	api := newAPI()
	_ = api.Register(&stubPlugin{id: "store.db.read"}, nil)
	if err := api.Register(&stubPlugin{
		id:        "func.calc",
		dependsOn: []tools.PluginID{"store.db.read"},
	}, nil); err != nil {
		t.Fatalf("Register with dep: %v", err)
	}
}

func TestRegisterAtomic(t *testing.T) {
	// 失败时 registry 不应被部分修改
	api := newAPI()
	_ = api.Register(&stubPlugin{id: "util.echo"}, nil)
	err := api.Register(&stubPlugin{
		id:        "a.b",
		dependsOn: []tools.PluginID{"missing"},
	}, nil)
	if err == nil {
		t.Fatal("want error")
	}
	if _, exists := api.Plugins()[`a.b`]; exists {
		t.Error("a.b should not be registered after failure")
	}
}

// --- RegisterAll ---

func TestRegisterAllSuccess(t *testing.T) {
	api := newAPI()
	err := api.RegisterAll([]tools.Plugin{
		&stubPlugin{id: "store.db.read"},
		&stubPlugin{id: "func.calc", dependsOn: []tools.PluginID{"store.db.read"}},
		&stubPlugin{id: "func.print", dependsOn: []tools.PluginID{"func.calc"}},
	})
	if err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	if len(api.Plugins()) != 3 {
		t.Errorf("want 3 plugins, got %d", len(api.Plugins()))
	}
}

func TestRegisterAllEmpty(t *testing.T) {
	api := newAPI()
	if err := api.RegisterAll(nil); err != nil {
		t.Errorf("empty batch: %v", err)
	}
}

func TestRegisterAllDuplicateWithinBatch(t *testing.T) {
	api := newAPI()
	err := api.RegisterAll([]tools.Plugin{
		&stubPlugin{id: "util.echo"},
		&stubPlugin{id: "util.echo"},
	})
	if err == nil {
		t.Fatal("want duplicate error")
	}
	if !errors.Is(err, tools.ErrPluginDuplicate) {
		t.Errorf("want ErrPluginDuplicate, got %v", err)
	}
}

func TestRegisterAllMissingDep(t *testing.T) {
	api := newAPI()
	err := api.RegisterAll([]tools.Plugin{
		&stubPlugin{id: "func.calc", dependsOn: []tools.PluginID{"store.db.missing"}},
	})
	if err == nil {
		t.Fatal("want missing dep")
	}
	if !errors.Is(err, tools.ErrPluginNotFound) {
		t.Errorf("want ErrPluginNotFound, got %v", err)
	}
}

func TestRegisterAllDepOnPriorRegister(t *testing.T) {
	api := newAPI()
	_ = api.Register(&stubPlugin{id: "store.db.read"}, nil)
	err := api.RegisterAll([]tools.Plugin{
		&stubPlugin{id: "func.calc", dependsOn: []tools.PluginID{"store.db.read"}},
	})
	if err != nil {
		t.Errorf("RegisterAll with prior dep: %v", err)
	}
}

func TestRegisterAllCycle(t *testing.T) {
	api := newAPI()
	err := api.RegisterAll([]tools.Plugin{
		&stubPlugin{id: "a.b", dependsOn: []tools.PluginID{"b.c"}},
		&stubPlugin{id: "b.c", dependsOn: []tools.PluginID{"a.b"}},
	})
	if err == nil {
		t.Fatal("want cycle error")
	}
	var ce *tools.CycleError
	if !errors.As(err, &ce) {
		t.Errorf("want *CycleError, got %T %v", err, err)
	}
	if !errors.Is(err, tools.ErrPluginCycle) {
		t.Errorf("want errors.Is ErrPluginCycle, got %v", err)
	}
	if len(ce.Cycle) < 2 {
		t.Errorf("cycle path too short: %v", ce.Cycle)
	}
}

func TestRegisterAllAtomic(t *testing.T) {
	// RegisterAll 失败时不应 commit
	api := newAPI()
	err := api.RegisterAll([]tools.Plugin{
		&stubPlugin{id: "a.b"},
		&stubPlugin{id: "b.c", dependsOn: []tools.PluginID{"a.b"}},
		&stubPlugin{id: "d.e", dependsOn: []tools.PluginID{"missing.x"}},
	})
	if err == nil {
		t.Fatal("want error (missing dep)")
	}
	// a.b 和 b.c 不应被 commit
	if len(api.Plugins()) != 0 {
		t.Errorf("registry should be empty, got %d", len(api.Plugins()))
	}
}

// --- Resolve ---

func TestResolveEmpty(t *testing.T) {
	api := newAPI()
	order, err := api.Resolve()
	if err != nil {
		t.Errorf("Resolve empty: %v", err)
	}
	if len(order) != 0 {
		t.Errorf("want empty, got %v", order)
	}
}

func TestResolveSinglePlugin(t *testing.T) {
	api := newAPI()
	_ = api.Register(&stubPlugin{id: "util.echo"}, nil)
	order, err := api.Resolve()
	if err != nil {
		t.Errorf("Resolve: %v", err)
	}
	if len(order) != 1 || order[0] != "util.echo" {
		t.Errorf("want [util.echo], got %v", order)
	}
}

func TestResolveDepsFirst(t *testing.T) {
	api := newAPI()
	_ = api.Register(&stubPlugin{id: "store.db.read"}, nil)
	_ = api.Register(&stubPlugin{id: "func.calc", dependsOn: []tools.PluginID{"store.db.read"}}, nil)
	_ = api.Register(&stubPlugin{id: "func.print", dependsOn: []tools.PluginID{"func.calc"}}, nil)

	order, err := api.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// 期望顺序：store.db.read → func.calc → func.print
	want := []tools.PluginID{"store.db.read", "func.calc", "func.print"}
	if !equalIDs(order, want) {
		t.Errorf("want %v, got %v", want, order)
	}
}

func TestResolveDAG(t *testing.T) {
	api := newAPI()
	// 多依赖汇聚：c → [a, b], d → [c]
	_ = api.Register(&stubPlugin{id: "store.a"}, nil)
	_ = api.Register(&stubPlugin{id: "store.b"}, nil)
	_ = api.Register(&stubPlugin{id: "func.c", dependsOn: []tools.PluginID{"store.a", "store.b"}}, nil)
	_ = api.Register(&stubPlugin{id: "func.d", dependsOn: []tools.PluginID{"func.c"}}, nil)

	order, err := api.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// 校验位置约束：a, b 都在 c 之前；c 在 d 之前
	pos := indexOf(order)
	if !(pos["store.a"] < pos["func.c"] && pos["store.b"] < pos["func.c"]) {
		t.Errorf("a/b should come before c: %v", order)
	}
	if !(pos["func.c"] < pos["func.d"]) {
		t.Errorf("c should come before d: %v", order)
	}
}

func TestResolveCycle(t *testing.T) {
	// Register 会校验"依赖已注册"，所以单独 Register 没法构造环；
	// 环只能通过 RegisterAll 注入（已在前面的 TestRegisterAllCycle 覆盖）。
	// 本测试只验证 Resolve 在合法 registry 上不误报。
	api := newAPI()
	if err := api.RegisterAll([]tools.Plugin{
		&stubPlugin{id: "store.a"},
		&stubPlugin{id: "func.b", dependsOn: []tools.PluginID{"store.a"}},
	}); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	if _, err := api.Resolve(); err != nil {
		t.Errorf("Resolve on valid registry: %v", err)
	}
}

func TestResolveSelfDep(t *testing.T) {
	// 同样，单独 Register 不可能注入 self-dep；本测试用 RegisterAll 验证。
	api := newAPI()
	err := api.RegisterAll([]tools.Plugin{
		&stubPlugin{id: "a.b", dependsOn: []tools.PluginID{"a.b"}},
	})
	if err == nil {
		t.Fatal("want self-dep cycle error from RegisterAll")
	}
	var ce *tools.CycleError
	if !errors.As(err, &ce) {
		t.Errorf("want *CycleError, got %T %v", err, err)
	}
}

func TestResolveDeterministic(t *testing.T) {
	// 多次 Resolve 返相同顺序
	api := newAPI()
	_ = api.RegisterAll([]tools.Plugin{
		&stubPlugin{id: "x.a"},
		&stubPlugin{id: "x.b"},
		&stubPlugin{id: "x.c"},
		&stubPlugin{id: "x.d"},
	})
	o1, _ := api.Resolve()
	o2, _ := api.Resolve()
	if !equalIDs(o1, o2) {
		t.Errorf("Resolve not deterministic: %v vs %v", o1, o2)
	}
	// 期望字典序
	want := []tools.PluginID{"x.a", "x.b", "x.c", "x.d"}
	if !equalIDs(o1, want) {
		t.Errorf("want sorted %v, got %v", want, o1)
	}
}

// --- CycleError 行为 ---

func TestCycleErrorMessage(t *testing.T) {
	ce := &tools.CycleError{Cycle: []tools.PluginID{"a.b", "b.c", "a.b"}}
	msg := ce.Error()
	if !strings.Contains(msg, "a.b") || !strings.Contains(msg, "b.c") {
		t.Errorf("Error() should contain cycle ids, got %q", msg)
	}
	if !errors.Is(ce, tools.ErrPluginCycle) {
		t.Error("CycleError should match ErrPluginCycle via Is")
	}
}

// --- Register 初始配置传递给 StartPlugin ---

// configuredStatefulStub 记录 StartPlugin 收到的 cfg，用于验证 Register(plugin, cfg)
// 把初始配置正确传递到 StartPlugin。
type configuredStatefulStub struct {
	stubPlugin
	startCfg  any
	startErr  error
	startCalls int
	stopCalls int
}

func (c *configuredStatefulStub) StartPlugin(ws tools.Context, cfg any) error {
	c.startCalls++
	c.startCfg = cfg
	return c.startErr
}

func (c *configuredStatefulStub) StopPlugin() error {
	c.stopCalls++
	return nil
}

func TestRegisterPassesInitialConfigToStartPlugin(t *testing.T) {
	api := newAPI()
	sp := &configuredStatefulStub{stubPlugin: stubPlugin{id: "fs.watch"}}
	cfg := map[string]any{"path": "/data"}
	if err := api.Register(sp, cfg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if sp.startCalls != 1 {
		t.Errorf("want StartPlugin called once, got %d", sp.startCalls)
	}
	got, ok := sp.startCfg.(map[string]any)
	if !ok || got["path"] != "/data" {
		t.Errorf("StartPlugin did not receive cfg, got %v", sp.startCfg)
	}
}

func TestRegisterNilConfigStillRegisters(t *testing.T) {
	api := newAPI()
	sp := &configuredStatefulStub{stubPlugin: stubPlugin{id: "fs.watch"}}
	if err := api.Register(sp, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if sp.startCfg != nil {
		t.Errorf("want nil cfg passed through, got %v", sp.startCfg)
	}
}

// --- StatefulPlugin ---

// statefulStub 是测试用的有状态 plugin，记录启停调用次数，可配置启停失败。
type statefulStub struct {
	stubPlugin
	startErr   error
	stopErr    error
	startCalls int
	stopCalls  int
}

func (s *statefulStub) StartPlugin(ws tools.Context, cfg any) error {
	s.startCalls++
	return s.startErr
}

func (s *statefulStub) StopPlugin() error {
	s.stopCalls++
	return s.stopErr
}

func TestRegisterStatefulPluginStarts(t *testing.T) {
	api := newAPI()
	sp := &statefulStub{stubPlugin: stubPlugin{id: "fs.watch"}}
	if err := api.Register(sp, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if sp.startCalls != 1 {
		t.Errorf("want StartPlugin called once, got %d", sp.startCalls)
	}
}

func TestRegisterStatefulPluginStartFailure(t *testing.T) {
	api := newAPI()
	sp := &statefulStub{stubPlugin: stubPlugin{id: "fs.watch"}, startErr: errors.New("boom")}
	if err := api.Register(sp, nil); err == nil {
		t.Fatal("want error from failed StartPlugin")
	}
	if _, exists := api.Plugins()["fs.watch"]; exists {
		t.Error("plugin should not be registered after StartPlugin failure")
	}
}

func TestStopAllPlugins(t *testing.T) {
	api := newAPI()
	sp1 := &statefulStub{stubPlugin: stubPlugin{id: "fs.watch"}}
	sp2 := &statefulStub{stubPlugin: stubPlugin{id: "net.conn"}}
	_ = api.Register(sp1, nil)
	_ = api.Register(sp2, nil)
	_ = api.Register(&stubPlugin{id: "util.echo"}, nil) // 非 stateful，不应受影响

	if err := api.StopAllPlugins(); err != nil {
		t.Fatalf("StopAllPlugins: %v", err)
	}
	if sp1.stopCalls != 1 || sp2.stopCalls != 1 {
		t.Errorf("want both stopped once, got sp1=%d sp2=%d", sp1.stopCalls, sp2.stopCalls)
	}
}

// TestRegisterStatefulPluginDuplicateSkipsStart 验证重名校验失败时
// StartPlugin 完全不会被触发（校验顺序：id/重名/依赖都在 StartPlugin 之前）。
func TestRegisterStatefulPluginDuplicateSkipsStart(t *testing.T) {
	api := newAPI()
	_ = api.Register(&stubPlugin{id: "fs.watch"}, nil)

	sp := &statefulStub{stubPlugin: stubPlugin{id: "fs.watch"}}
	if err := api.Register(sp, nil); err == nil {
		t.Fatal("want duplicate error")
	}
	if sp.startCalls != 0 {
		t.Errorf("StartPlugin should not be called on duplicate-id failure, got %d calls", sp.startCalls)
	}
}

// TestRegisterStatefulPluginMissingDepSkipsStart 验证依赖缺失校验失败时
// StartPlugin 完全不会被触发。
func TestRegisterStatefulPluginMissingDepSkipsStart(t *testing.T) {
	api := newAPI()
	sp := &statefulStub{stubPlugin: stubPlugin{id: "fs.watch", dependsOn: []tools.PluginID{"missing.thing"}}}
	if err := api.Register(sp, nil); err == nil {
		t.Fatal("want missing dep error")
	}
	if sp.startCalls != 0 {
		t.Errorf("StartPlugin should not be called on missing-dep failure, got %d calls", sp.startCalls)
	}
}

func TestStopAllPluginsPartialFailureStillStopsRest(t *testing.T) {
	// 一个 plugin StopPlugin 失败不应阻止其余 plugin 被尝试停止；
	// 两个失败都应被 errors.Join 收集。
	api := newAPI()
	ok := &statefulStub{stubPlugin: stubPlugin{id: "fs.watch"}}
	bad1 := &statefulStub{stubPlugin: stubPlugin{id: "net.conn"}, stopErr: errors.New("boom1")}
	bad2 := &statefulStub{stubPlugin: stubPlugin{id: "net.other"}, stopErr: errors.New("boom2")}
	_ = api.Register(ok, nil)
	_ = api.Register(bad1, nil)
	_ = api.Register(bad2, nil)

	err := api.StopAllPlugins()
	if err == nil {
		t.Fatal("want joined error")
	}
	if ok.stopCalls != 1 || bad1.stopCalls != 1 || bad2.stopCalls != 1 {
		t.Errorf("all plugins should be stopped once regardless of failures, got ok=%d bad1=%d bad2=%d",
			ok.stopCalls, bad1.stopCalls, bad2.stopCalls)
	}
	if !strings.Contains(err.Error(), "boom1") || !strings.Contains(err.Error(), "boom2") {
		t.Errorf("joined error should mention both failures, got %q", err.Error())
	}
}

// --- StatefulPlugin + ReloadablePlugin 组合 ---

// statefulReloadableStub 同时实现 StatefulPlugin 和 ReloadablePlugin，
// 验证两者互不干扰：UpdateConfig 只在运行期由 UpdatePluginConfig 触发，
// 不会被 StartPlugin/StopPlugin 间接调用。
type statefulReloadableStub struct {
	stubPlugin
	startCalls  int
	stopCalls   int
	updateCalls int
	cfg         any
}

func (s *statefulReloadableStub) StartPlugin(ws tools.Context, cfg any) error {
	s.startCalls++
	return nil
}

func (s *statefulReloadableStub) StopPlugin() error {
	s.stopCalls++
	return nil
}

func (s *statefulReloadableStub) UpdateConfig(cfg any) error {
	s.updateCalls++
	s.cfg = cfg
	return nil
}

func TestStatefulAndReloadablePluginIndependent(t *testing.T) {
	api := newAPI()
	sp := &statefulReloadableStub{stubPlugin: stubPlugin{id: "fs.watch"}}
	initialCfg := map[string]any{"path": "/initial"}
	if err := api.Register(sp, initialCfg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if sp.startCalls != 1 {
		t.Errorf("want StartPlugin called once, got %d", sp.startCalls)
	}
	if sp.updateCalls != 0 {
		t.Errorf("UpdateConfig should not be called by Register/StartPlugin, got %d calls", sp.updateCalls)
	}

	newCfg := map[string]any{"path": "/updated"}
	if err := api.UpdatePluginConfig("fs.watch", newCfg); err != nil {
		t.Fatalf("UpdatePluginConfig: %v", err)
	}
	if sp.updateCalls != 1 {
		t.Errorf("want UpdateConfig called once, got %d", sp.updateCalls)
	}
	got, ok := sp.cfg.(map[string]any)
	if !ok || got["path"] != "/updated" {
		t.Errorf("UpdateConfig did not receive new cfg, got %v", sp.cfg)
	}

	if err := api.StopAllPlugins(); err != nil {
		t.Fatalf("StopAllPlugins: %v", err)
	}
	if sp.stopCalls != 1 {
		t.Errorf("want StopPlugin called once, got %d", sp.stopCalls)
	}
}

func TestRegisterAllStatefulRollback(t *testing.T) {
	// batch 中第二个 plugin 启动失败，第一个已启动的应被回滚（Stop）
	api := newAPI()
	sp1 := &statefulStub{stubPlugin: stubPlugin{id: "a.b"}}
	sp2 := &statefulStub{stubPlugin: stubPlugin{id: "c.d"}, startErr: errors.New("boom")}
	err := api.RegisterAll([]tools.Plugin{sp1, sp2})
	if err == nil {
		t.Fatal("want error")
	}
	if sp1.startCalls != 1 || sp1.stopCalls != 1 {
		t.Errorf("sp1 should be started then rolled back, got start=%d stop=%d", sp1.startCalls, sp1.stopCalls)
	}
	if len(api.Plugins()) != 0 {
		t.Errorf("registry should be empty after rollback, got %d", len(api.Plugins()))
	}
}

// --- helpers ---

func equalIDs(a, b []tools.PluginID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func indexOf(ids []tools.PluginID) map[tools.PluginID]int {
	m := make(map[tools.PluginID]int, len(ids))
	for i, id := range ids {
		m[id] = i
	}
	return m
}
