package tools_test

import (
	"context"
	"errors"
	"testing"

	"github.com/wyx0k/ndrive/tools"
)

// 编译期断言：SimplePluginDef 同时实现 Tool（单个可调用单元）和 Plugin
// （单 tool 容器，容器 id = ID_）。
var _ tools.Tool = tools.SimplePluginDef{}
var _ tools.Plugin = tools.SimplePluginDef{}

// --- PluginID.Validate ---

func TestPluginIDValidate(t *testing.T) {
	cases := []struct {
		id    tools.PluginID
		valid bool
	}{
		{"", false},               // 空
		{"foo", false},            // 单段
		{"foo.bar", true},         // 2 段
		{"store.userdb.read", true}, // 3 段
		{"util.echo", true},       // 2 段
		{"foo..bar", false},       // 中间有空段
		{".foo", false},           // 开头空段
		{"foo.", false},           // 结尾空段
		{"a.b.c.d.e", true},       // 多段
	}
	for _, c := range cases {
		err := tools.ValidatePluginID(c.id)
		if c.valid && err != nil {
			t.Errorf("id %q: want valid, got err=%v", c.id, err)
		}
		if !c.valid && err == nil {
			t.Errorf("id %q: want invalid, got nil", c.id)
		}
	}
}

// --- SimplePluginDef implements Tool ---

func TestSimplePluginDefImplementsPlugin(t *testing.T) {
	// 用一个最小 def 跑一遍 7 个 getter
	def := tools.SimplePluginDef{
		ID_:          "util.echo",
		Description_: "echo back",
		Schema_:      map[string]any{"type": "object"},
		DependsOn_:   []tools.PluginID{"store.userdb.read"},
		Read_:        true,
		Fn_: func(ctx context.Context, ws tools.Context, args any) (any, error) {
			return args, nil
		},
	}
	var p tools.Tool = def
	if p.ID() != "util.echo" {
		t.Errorf("ID: %q", p.ID())
	}
	if p.Description() != "echo back" {
		t.Errorf("Description: %q", p.Description())
	}
	if p.Read() != true {
		t.Error("Read: false")
	}
	if p.Internal() != false {
		t.Error("Internal: want false (default exported)")
	}
	if len(p.DependsOn()) != 1 || p.DependsOn()[0] != "store.userdb.read" {
		t.Errorf("DependsOn: %v", p.DependsOn())
	}
	if p.Schema()["type"] != "object" {
		t.Errorf("Schema: %v", p.Schema())
	}
}

// --- SimplePluginDef.Call ---

func TestSimplePluginDefCallSuccess(t *testing.T) {
	def := tools.SimplePluginDef{
		ID_: "util.echo",
		Fn_: func(ctx context.Context, ws tools.Context, args any) (any, error) {
			return args, nil
		},
	}
	got, err := def.Call(context.Background(), nil, "hello")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got != "hello" {
		t.Errorf("want hello, got %v", got)
	}
}

func TestSimplePluginDefCallNilFn(t *testing.T) {
	def := tools.SimplePluginDef{ID_: "x"} // Fn_ 留空
	_, err := def.Call(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("want error for nil Fn_")
	}
}

// --- 字段下划线命名：避免和 getter 冲突 ---

func TestSimplePluginDefFieldNames(t *testing.T) {
	// 直接对字段赋值是允许的（不通过 getter）
	def := tools.SimplePluginDef{
		ID_:          "a.b",
		Description_: "d",
		Read_:        false,
	}
	// getter 取到的是带下划线的字段值
	if def.ID() != "a.b" {
		t.Errorf("ID getter wrong: %q", def.ID())
	}
	if def.Description() != "d" {
		t.Errorf("Description getter wrong: %q", def.Description())
	}
	if def.Read() != false {
		t.Error("Read getter wrong")
	}
}

// --- 用户也可以手写 Tool 接口（不一定用 SimplePluginDef）；Register 会自动
// 把裸 Tool 包装成单 tool Plugin 容器 ---

type manualPlugin struct {
	id    tools.PluginID
	read  bool
	calls int
}

func (m *manualPlugin) ID() tools.PluginID          { return m.id }
func (m *manualPlugin) Description() string         { return "manual" }
func (m *manualPlugin) Schema() map[string]any      { return nil }
func (m *manualPlugin) DependsOn() []tools.PluginID { return nil }
func (m *manualPlugin) Read() bool                  { return m.read }
func (m *manualPlugin) Internal() bool              { return false }
func (m *manualPlugin) Call(ctx context.Context, ws tools.Context, args any) (any, error) {
	m.calls++
	return nil, nil
}

func TestManualPlugin(t *testing.T) {
	// 编译期：*manualPlugin 实现 Tool
	var _ tools.Tool = (*manualPlugin)(nil)
	p := &manualPlugin{id: "custom.thing", read: true}
	if p.ID() != "custom.thing" {
		t.Errorf("ID: %q", p.ID())
	}
	_, err := p.Call(context.Background(), nil, nil)
	if err != nil {
		t.Errorf("Call: %v", err)
	}
	if p.calls != 1 {
		t.Errorf("calls: %d", p.calls)
	}
}

// --- 错误透传 ---

func TestSimplePluginDefCallError(t *testing.T) {
	want := errors.New("boom")
	def := tools.SimplePluginDef{
		ID_: "x.y",
		Fn_: func(ctx context.Context, ws tools.Context, args any) (any, error) {
			return nil, want
		},
	}
	_, err := def.Call(context.Background(), nil, nil)
	if !errors.Is(err, want) {
		t.Errorf("want err %v, got %v", want, err)
	}
}

// --- ToolInfo / Func 投影类型是值类型（不可 nil） ---

func TestToolProjection(t *testing.T) {
	info := tools.ToolInfo{
		ID:          "util.echo",
		Description: "echo",
		Schema:      map[string]any{"type": "object"},
	}
	if info.ID != "util.echo" || info.Description != "echo" {
		t.Error("ToolInfo fields wrong")
	}
}

func TestFuncProjection(t *testing.T) {
	called := false
	fn := tools.Func{
		ID: "util.echo",
		Call: func(ctx context.Context, args any) (any, error) {
			called = true
			return "ok", nil
		},
	}
	v, err := fn.Call(context.Background(), "in")
	if err != nil {
		t.Fatalf("Func.Call: %v", err)
	}
	if v != "ok" {
		t.Errorf("Func.Call returned %v", v)
	}
	if !called {
		t.Error("Func.Call did not invoke handler")
	}
}
