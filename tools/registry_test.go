package tools_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wyx0k/ndrive/tools"
)

// stubPlugin 是测试用的最小 tool；同时实现 Tool 和 Plugin（单 tool 容器，
// 容器 id 就是 s.id），这样既可以直接传给 Register（自动识别为 Plugin），
// 也可以放进 []tools.Plugin{...} 批量注册的切片字面量里。
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
func (s *stubPlugin) Internal() bool              { return false }
func (s *stubPlugin) Call(_ context.Context, _ tools.Context, _ any) (any, error) {
	return nil, nil
}

// Tools 让 stubPlugin 同时满足 Plugin：返回只含自己的单 tool 列表，tool 侧
// ID() 置空，这样完整 id = 容器 id = s.id，不做额外拼接（和 SimplePluginDef
// 的 Tools() 实现同一思路）。
func (s *stubPlugin) Tools() []tools.Tool { return []tools.Tool{stubToolView{s}} }

// stubToolView 把 *stubPlugin 包成 tool 视图，只覆盖 ID()。
type stubToolView struct {
	*stubPlugin
}

func (stubToolView) ID() string { return "" }

func newAPI() *tools.ToolsAPI {
	return tools.NewToolsAPI(nil)
}

// stubTool 是测试用的最小 Tool，供 multiToolStub 这种真正的多 tool 容器使用。
type stubTool struct {
	id       string
	internal bool
	read     bool
	fn       func(ctx context.Context, ws tools.Context, args any) (any, error)
}

func (t *stubTool) ID() string             { return t.id }
func (t *stubTool) Description() string    { return t.id + " desc" }
func (t *stubTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *stubTool) Read() bool             { return t.read }
func (t *stubTool) Internal() bool         { return t.internal }
func (t *stubTool) Call(ctx context.Context, ws tools.Context, args any) (any, error) {
	if t.fn != nil {
		return t.fn(ctx, ws, args)
	}
	return nil, nil
}

// multiToolStub 是测试用的真正多 tool 容器（和 stubPlugin 的单 tool 自包装不同），
// 用于验证容器级别的 Internal 过滤、UpdatePluginConfig/Service 按容器 id 操作、
// StatefulPlugin 挂在容器而不是某个 tool 上的场景。
type multiToolStub struct {
	cid         tools.PluginID
	toolList    []tools.Tool
	dependsOn   []tools.PluginID
	cfg         any
	updateErr   error
	updateCalls int
	svc         any
	startCalls  int
	stopCalls   int
	startErr    error
	stopErr     error
}

func (m *multiToolStub) ID() tools.PluginID { return m.cid }
func (m *multiToolStub) Tools() []tools.Tool {
	return m.toolList
}

// DependsOn 让 multiToolStub 可选实现 DependentPlugin：验证容器级别依赖
// 覆盖多 tool 容器（所有 tool 共享同一份依赖声明）的场景。
func (m *multiToolStub) DependsOn() []tools.PluginID { return m.dependsOn }

func (m *multiToolStub) UpdateConfig(cfg any) error {
	m.updateCalls++
	if m.updateErr != nil {
		return m.updateErr
	}
	m.cfg = cfg
	return nil
}

func (m *multiToolStub) Service() any { return m.svc }

func (m *multiToolStub) StartPlugin(ws tools.Context, cfg any) error {
	m.startCalls++
	return m.startErr
}

func (m *multiToolStub) StopPlugin() error {
	m.stopCalls++
	return m.stopErr
}

// bareStatefulTool 是测试用的裸 Tool（不实现 Plugin），自己直接挂
// StartPlugin/StopPlugin——模拟 greeterPlugin 这种手写类型通过 AsPlugin
// 自动包装注册、但 StatefulPlugin 断言必须落在原始 Tool 值上，而不是合成出来
// 的 singleToolPlugin 容器上。
type bareStatefulTool struct {
	stubTool
	startCalls int
	stopCalls  int
	startErr   error
	stopErr    error
}

func (b *bareStatefulTool) StartPlugin(ws tools.Context, cfg any) error {
	b.startCalls++
	return b.startErr
}

func (b *bareStatefulTool) StopPlugin() error {
	b.stopCalls++
	return b.stopErr
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

// TestRegisterContainerLevelDepMultiTool 验证 DependsOn 是容器级别的：一个
// 真正的多 tool 容器（store.fs，有 read/write 两个 tool，谁都没有单独声明
// 依赖）整体依赖 store.db.read；缺依赖时注册失败，满足依赖后两个 tool 都注册成功。
func TestRegisterContainerLevelDepMultiTool(t *testing.T) {
	api := newAPI()
	fs := &multiToolStub{
		cid: "store.fs",
		toolList: []tools.Tool{
			&stubTool{id: "read", read: true},
			&stubTool{id: "write", read: false},
		},
		dependsOn: []tools.PluginID{"store.db.read"},
	}

	if err := api.Register(fs, nil); !errors.Is(err, tools.ErrPluginNotFound) {
		t.Fatalf("want ErrPluginNotFound (dep missing), got %v", err)
	}

	_ = api.Register(&stubPlugin{id: "store.db.read"}, nil)
	if err := api.Register(fs, nil); err != nil {
		t.Fatalf("Register after dep satisfied: %v", err)
	}

	if _, err := api.Call(context.Background(), "store.fs.read", nil); err != nil {
		t.Errorf("store.fs.read should be callable: %v", err)
	}
	if _, err := api.Call(context.Background(), "store.fs.write", nil); err != nil {
		t.Errorf("store.fs.write should be callable: %v", err)
	}
}

// TestResolveContainerLevelMultiTool 验证 Resolve 按容器 id（不是 tool 完整
// id）返回拓扑顺序：多 tool 容器的依赖只需要出现一次，不会按 tool 数量重复。
func TestResolveContainerLevelMultiTool(t *testing.T) {
	api := newAPI()
	_ = api.Register(&stubPlugin{id: "store.db.read"}, nil)
	_ = api.Register(&multiToolStub{
		cid: "store.fs",
		toolList: []tools.Tool{
			&stubTool{id: "read", read: true},
			&stubTool{id: "write", read: false},
		},
		dependsOn: []tools.PluginID{"store.db.read"},
	}, nil)

	order, err := api.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(order) != 2 {
		t.Fatalf("want 2 container ids in order, got %v", order)
	}
	dbIdx, fsIdx := -1, -1
	for i, id := range order {
		switch id {
		case "store.db.read":
			dbIdx = i
		case "store.fs":
			fsIdx = i
		}
	}
	if dbIdx == -1 || fsIdx == -1 {
		t.Fatalf("want both container ids in order, got %v", order)
	}
	if dbIdx > fsIdx {
		t.Errorf("want store.db.read before store.fs, got order %v", order)
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

// TestRegisterAllStatefulRollbackMultipleFailMiddle 用 3 个 stateful plugin，
// 第 3 个（拓扑顺序中间，因为 b.c 依赖 a.b）启动失败；验证已启动的前两个
// 按逆序被回滚（先停最后启动的），且整批不 commit。
func TestRegisterAllStatefulRollbackMultipleFailMiddle(t *testing.T) {
	api := newAPI()
	var stopOrder []tools.PluginID

	mk := func(id tools.PluginID, dep tools.PluginID, startErr error) *statefulStub {
		s := &statefulStub{stubPlugin: stubPlugin{id: id}, startErr: startErr}
		return s
	}
	sp1 := mk("a.b", "", nil)
	sp2 := mk("b.c", "a.b", nil)
	sp2.stubPlugin.dependsOn = []tools.PluginID{"a.b"}
	sp3 := mk("c.d", "b.c", errors.New("boom"))
	sp3.stubPlugin.dependsOn = []tools.PluginID{"b.c"}

	// 包装 StopPlugin 记录停止顺序，不改变 statefulStub 本身的计数逻辑。
	wrap := func(id tools.PluginID, s *statefulStub) tools.Plugin {
		return &stopOrderStub{statefulStub: s, id: id, order: &stopOrder}
	}

	err := api.RegisterAll([]tools.Plugin{wrap("a.b", sp1), wrap("b.c", sp2), wrap("c.d", sp3)})
	if err == nil {
		t.Fatal("want error from sp3 StartPlugin failure")
	}
	if sp1.startCalls != 1 || sp2.startCalls != 1 {
		t.Errorf("sp1/sp2 should have started, got sp1=%d sp2=%d", sp1.startCalls, sp2.startCalls)
	}
	if sp3.startCalls != 1 {
		t.Errorf("sp3 StartPlugin should have been attempted once, got %d", sp3.startCalls)
	}
	if sp1.stopCalls != 1 || sp2.stopCalls != 1 {
		t.Errorf("sp1/sp2 should be rolled back, got sp1.stop=%d sp2.stop=%d", sp1.stopCalls, sp2.stopCalls)
	}
	if sp3.stopCalls != 0 {
		t.Errorf("sp3 never started, should not be stopped, got %d", sp3.stopCalls)
	}
	want := []tools.PluginID{"b.c", "a.b"} // 逆序：最后启动的先停
	if !equalIDs(stopOrder, want) {
		t.Errorf("stop order = %v, want %v (reverse of start order)", stopOrder, want)
	}
	if len(api.Plugins()) != 0 {
		t.Errorf("registry should be empty after rollback, got %d", len(api.Plugins()))
	}
}

// stopOrderStub 包装 *statefulStub，只用于记录 StopPlugin 的调用顺序；
// Register/RegisterAll 走的是 lifecycleTarget == plugin 本身这条路径，所以
// 这里直接嵌入 *statefulStub 作为真正的 Plugin，外层只加一层顺序记录。
type stopOrderStub struct {
	*statefulStub
	id    tools.PluginID
	order *[]tools.PluginID
}

func (s *stopOrderStub) StopPlugin() error {
	*s.order = append(*s.order, s.id)
	return s.statefulStub.StopPlugin()
}

// --- 裸 Tool 自动包装场景下的 StatefulPlugin（lifecycleTarget） ---

// TestRegisterBareToolStatefulPlugin 验证手写裸 Tool（不实现 Plugin，像
// greeterPlugin 那样）直接把 StartPlugin/StopPlugin 挂在自己身上时，
// Register 通过 AsPlugin 自动包装成单 tool 容器后，StatefulPlugin 断言仍然
// 落在原始 Tool 值上（lifecycleTarget），能正确触发启停。这正是修复
// singleToolPlugin 能力泄漏 bug 的核心场景，此前没有专门的回归测试。
func TestRegisterBareToolStatefulPlugin(t *testing.T) {
	api := newAPI()
	bt := &bareStatefulTool{stubTool: stubTool{id: "fs.watch"}}
	if err := api.Register(bt, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if bt.startCalls != 1 {
		t.Errorf("want StartPlugin called once, got %d", bt.startCalls)
	}
	if err := api.StopAllPlugins(); err != nil {
		t.Fatalf("StopAllPlugins: %v", err)
	}
	if bt.stopCalls != 1 {
		t.Errorf("want StopPlugin called once, got %d", bt.stopCalls)
	}
}

// TestRegisterBareToolWithoutStatefulDoesNotLeakCapability 验证普通裸 Tool
// （不实现 StartPlugin/StopPlugin）被 AsPlugin 自动包装后，合成出来的
// singleToolPlugin 容器本身绝不会意外满足 StatefulPlugin——否则所有普通
// tool 的注册都会被当成 stateful 来处理。
func TestRegisterBareToolWithoutStatefulDoesNotLeakCapability(t *testing.T) {
	api := newAPI()
	plain := &stubTool{id: "util.plain"}
	if err := api.Register(plain, nil); err != nil {
		t.Fatalf("Register plain tool: %v", err)
	}
	// 没有 StartPlugin/StopPlugin，StopAllPlugins 应该什么都不做、不报错。
	if err := api.StopAllPlugins(); err != nil {
		t.Fatalf("StopAllPlugins on non-stateful registry: %v", err)
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
