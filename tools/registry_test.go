package tools_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wyx0k/ndrive/tools"
)

// stubTool 是测试用的最小 Tool，同时实现 DependentTool（依赖声明可选，
// 未实现 DependentTool 的 tool 见 plainTool）。
type stubTool struct {
	id        tools.ToolID
	read      bool
	dependsOn []tools.ToolID
	fn        func(ctx context.Context, ws tools.Context, args any) (any, error)
}

func (s *stubTool) ID() tools.ToolID          { return s.id }
func (s *stubTool) Description() string       { return s.id + " desc" }
func (s *stubTool) Schema() map[string]any    { return map[string]any{"type": "object"} }
func (s *stubTool) DependsOn() []tools.ToolID { return s.dependsOn }
func (s *stubTool) Read() bool                { return s.read }
func (s *stubTool) Call(ctx context.Context, ws tools.Context, args any) (any, error) {
	if s.fn != nil {
		return s.fn(ctx, ws, args)
	}
	return nil, nil
}

// plainTool 不实现 DependentTool——最"普通"的 tool，
// 用来验证没有可选接口时的注册路径。
type plainTool struct {
	id   tools.ToolID
	read bool
}

func (p *plainTool) ID() tools.ToolID       { return p.id }
func (p *plainTool) Description() string    { return p.id + " desc" }
func (p *plainTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (p *plainTool) Read() bool             { return p.read }
func (p *plainTool) Call(context.Context, tools.Context, any) (any, error) {
	return nil, nil
}

func newAPI() *tools.ToolsAPI {
	return tools.NewToolsAPI(nil)
}

// --- Register ---

func TestRegisterSuccess(t *testing.T) {
	api := newAPI()
	if err := api.Register(&stubTool{id: "util.echo"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !registeredIDs(api)["util.echo"] {
		t.Fatal("tool not stored")
	}
}

func TestRegisterNilTool(t *testing.T) {
	api := newAPI()
	if err := api.Register(nil); err == nil {
		t.Fatal("want error for nil tool")
	} else if !errors.Is(err, tools.ErrToolInvalid) {
		t.Errorf("want ErrToolInvalid, got %v", err)
	}
}

func TestRegisterInvalidID(t *testing.T) {
	api := newAPI()
	if err := api.Register(&stubTool{id: "single"}); err == nil {
		t.Fatal("want error for single-segment id")
	} else if !errors.Is(err, tools.ErrToolInvalid) {
		t.Errorf("want ErrToolInvalid, got %v", err)
	}
}

func TestRegisterDuplicate(t *testing.T) {
	api := newAPI()
	_ = api.Register(&stubTool{id: "util.echo"})
	if err := api.Register(&stubTool{id: "util.echo"}); err == nil {
		t.Fatal("want duplicate error")
	} else if !errors.Is(err, tools.ErrToolDuplicate) {
		t.Errorf("want ErrToolDuplicate, got %v", err)
	}
}

func TestRegisterMissingDep(t *testing.T) {
	api := newAPI()
	// a 依赖 b，但 b 未注册
	err := api.Register(&stubTool{
		id:        "a.b",
		dependsOn: []tools.ToolID{"missing.thing"},
	})

	if err == nil {
		t.Fatal("want missing dep error")
	}
	if !errors.Is(err, tools.ErrToolNotFound) {
		t.Errorf("want ErrToolNotFound, got %v", err)
	}
}

func TestRegisterDepRegistered(t *testing.T) {
	api := newAPI()
	_ = api.Register(&stubTool{id: "store.db.read"})
	if err := api.Register(&stubTool{
		id:        "func.calc",
		dependsOn: []tools.ToolID{"store.db.read"},
	}); err != nil {
		t.Fatalf("Register with dep: %v", err)
	}
}

// TestRegisterPlainTool 验证不实现任何可选接口的 tool 也能正常注册与调用。
func TestRegisterPlainTool(t *testing.T) {
	api := newAPI()
	if err := api.Register(&plainTool{id: "util.plain"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := api.Call(context.Background(), "util.plain", nil); err != nil {
		t.Errorf("Call: %v", err)
	}
}

// TestRegisterMultipleTools 验证多个 tool 平铺共存（没有容器层）。
func TestRegisterMultipleTools(t *testing.T) {
	api := newAPI()
	_ = api.Register(&stubTool{id: "store.fs.read", read: true})
	_ = api.Register(&stubTool{id: "store.fs.write", read: false})
	if len(registeredIDs(api)) != 2 {
		t.Fatalf("want 2 tools, got %d", len(registeredIDs(api)))
	}
	for _, id := range []string{"store.fs.read", "store.fs.write"} {
		if _, err := api.Call(context.Background(), id, nil); err != nil {
			t.Errorf("Call(%s): %v", id, err)
		}
	}
}

func TestRegisterAtomic(t *testing.T) {
	// 失败时注册表不应被部分修改
	api := newAPI()
	_ = api.Register(&stubTool{id: "util.echo"})
	err := api.Register(&stubTool{
		id:        "a.b",
		dependsOn: []tools.ToolID{"missing"},
	})

	if err == nil {
		t.Fatal("want error")
	}
	if registeredIDs(api)["a.b"] {
		t.Error("a.b should not be registered after failure")
	}
}

// --- RegisterAll ---

func TestRegisterAllSuccess(t *testing.T) {
	api := newAPI()
	err := api.RegisterAll([]tools.Tool{
		&stubTool{id: "store.db.read"},
		&stubTool{id: "func.calc", dependsOn: []tools.ToolID{"store.db.read"}},
		&stubTool{id: "func.print", dependsOn: []tools.ToolID{"func.calc"}},
	})
	if err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	if len(registeredIDs(api)) != 3 {
		t.Errorf("want 3 tools, got %d", len(registeredIDs(api)))
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
	err := api.RegisterAll([]tools.Tool{
		&stubTool{id: "util.echo"},
		&stubTool{id: "util.echo"},
	})
	if err == nil {
		t.Fatal("want duplicate error")
	}
	if !errors.Is(err, tools.ErrToolDuplicate) {
		t.Errorf("want ErrToolDuplicate, got %v", err)
	}
}

func TestRegisterAllMissingDep(t *testing.T) {
	api := newAPI()
	err := api.RegisterAll([]tools.Tool{
		&stubTool{id: "func.calc", dependsOn: []tools.ToolID{"store.db.missing"}},
	})
	if err == nil {
		t.Fatal("want missing dep")
	}
	if !errors.Is(err, tools.ErrToolNotFound) {
		t.Errorf("want ErrToolNotFound, got %v", err)
	}
}

func TestRegisterAllDepOnPriorRegister(t *testing.T) {
	api := newAPI()
	_ = api.Register(&stubTool{id: "store.db.read"})
	err := api.RegisterAll([]tools.Tool{
		&stubTool{id: "func.calc", dependsOn: []tools.ToolID{"store.db.read"}},
	})
	if err != nil {
		t.Errorf("RegisterAll with prior dep: %v", err)
	}
}

func TestRegisterAllCycle(t *testing.T) {
	api := newAPI()
	err := api.RegisterAll([]tools.Tool{
		&stubTool{id: "a.b", dependsOn: []tools.ToolID{"b.c"}},
		&stubTool{id: "b.c", dependsOn: []tools.ToolID{"a.b"}},
	})
	if err == nil {
		t.Fatal("want cycle error")
	}
	var ce *tools.CycleError
	if !errors.As(err, &ce) {
		t.Errorf("want *CycleError, got %T %v", err, err)
	}
	if !errors.Is(err, tools.ErrToolCycle) {
		t.Errorf("want errors.Is ErrToolCycle, got %v", err)
	}
	if len(ce.Cycle) < 2 {
		t.Errorf("cycle path too short: %v", ce.Cycle)
	}
}

func TestRegisterAllAtomic(t *testing.T) {
	// RegisterAll 失败时不应 commit
	api := newAPI()
	err := api.RegisterAll([]tools.Tool{
		&stubTool{id: "a.b"},
		&stubTool{id: "b.c", dependsOn: []tools.ToolID{"a.b"}},
		&stubTool{id: "d.e", dependsOn: []tools.ToolID{"missing.x"}},
	})
	if err == nil {
		t.Fatal("want error (missing dep)")
	}
	// a.b 和 b.c 不应被 commit
	if len(registeredIDs(api)) != 0 {
		t.Errorf("registry should be empty, got %d", len(registeredIDs(api)))
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

func TestResolveSingleTool(t *testing.T) {
	api := newAPI()
	_ = api.Register(&stubTool{id: "util.echo"})
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
	_ = api.Register(&stubTool{id: "store.db.read"})
	_ = api.Register(&stubTool{id: "func.calc", dependsOn: []tools.ToolID{"store.db.read"}})
	_ = api.Register(&stubTool{id: "func.print", dependsOn: []tools.ToolID{"func.calc"}})

	order, err := api.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// 期望顺序：store.db.read → func.calc → func.print
	want := []tools.ToolID{"store.db.read", "func.calc", "func.print"}
	if !equalIDs(order, want) {
		t.Errorf("want %v, got %v", want, order)
	}
}

func TestResolveDAG(t *testing.T) {
	api := newAPI()
	// 多依赖汇聚：c → [a, b], d → [c]
	_ = api.Register(&stubTool{id: "store.a"})
	_ = api.Register(&stubTool{id: "store.b"})
	_ = api.Register(&stubTool{id: "func.c", dependsOn: []tools.ToolID{"store.a", "store.b"}})
	_ = api.Register(&stubTool{id: "func.d", dependsOn: []tools.ToolID{"func.c"}})

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
	// 本测试只验证 Resolve 在合法注册表上不误报。
	api := newAPI()
	if err := api.RegisterAll([]tools.Tool{
		&stubTool{id: "store.a"},
		&stubTool{id: "func.b", dependsOn: []tools.ToolID{"store.a"}},
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
	err := api.RegisterAll([]tools.Tool{
		&stubTool{id: "a.b", dependsOn: []tools.ToolID{"a.b"}},
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
	_ = api.RegisterAll([]tools.Tool{
		&stubTool{id: "x.a"},
		&stubTool{id: "x.b"},
		&stubTool{id: "x.c"},
		&stubTool{id: "x.d"},
	})
	o1, _ := api.Resolve()
	o2, _ := api.Resolve()
	if !equalIDs(o1, o2) {
		t.Errorf("Resolve not deterministic: %v vs %v", o1, o2)
	}
	// 期望字典序
	want := []tools.ToolID{"x.a", "x.b", "x.c", "x.d"}
	if !equalIDs(o1, want) {
		t.Errorf("want sorted %v, got %v", want, o1)
	}
}

// --- CycleError 行为 ---

func TestCycleErrorMessage(t *testing.T) {
	ce := &tools.CycleError{Cycle: []tools.ToolID{"a.b", "b.c", "a.b"}}
	msg := ce.Error()
	if !strings.Contains(msg, "a.b") || !strings.Contains(msg, "b.c") {
		t.Errorf("Error() should contain cycle ids, got %q", msg)
	}
	if !errors.Is(ce, tools.ErrToolCycle) {
		t.Error("CycleError should match ErrToolCycle via Is")
	}
}

// --- helpers ---

// registeredIDs 返回当前已注册的 tool id 集合。
// Resolve 返回的是拓扑顺序，正好用作"哪些 tool 已注册"的只读视图。
func registeredIDs(api *tools.ToolsAPI) map[tools.ToolID]bool {
	ids, err := api.Resolve()
	if err != nil {
		return nil
	}
	m := make(map[tools.ToolID]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func equalIDs(a, b []tools.ToolID) bool {
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

func indexOf(ids []tools.ToolID) map[tools.ToolID]int {
	m := make(map[tools.ToolID]int, len(ids))
	for i, id := range ids {
		m[id] = i
	}
	return m
}
