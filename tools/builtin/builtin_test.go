package builtin_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/wyx0k/ndrive/bus"
	"github.com/wyx0k/ndrive/permission"
	"github.com/wyx0k/ndrive/tools"
	"github.com/wyx0k/ndrive/tools/builtin"
)

func TestEchoMetadata(t *testing.T) {
	p := builtin.Echo()
	if p.ID() != "util.echo" {
		t.Errorf("ID: %q", p.ID())
	}
	tl := p.Tools()
	if len(tl) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tl))
	}
	tool := tl[0]
	if tool.Description() == "" {
		t.Error("Description should be non-empty")
	}
	if !tool.Read() {
		t.Error("Echo should be Read=true")
	}
	if tool.Schema() == nil {
		t.Error("Schema should be non-nil")
	}
}

func TestEchoBehavior(t *testing.T) {
	api := tools.NewToolsAPI(&busBusContext{bus: bus.New()})
	if err := api.Register(builtin.Echo(), nil); err != nil {
		t.Fatalf("Register: %v", err)
	}

	cases := []any{
		"hello",
		42,
		[]any{"a", "b"},
		map[string]any{"k": "v"},
		nil,
	}
	for _, c := range cases {
		got, err := api.Call(context.Background(), "util.echo", c)
		if err != nil {
			t.Errorf("Call(%v): %v", c, err)
		}
		if !reflect.DeepEqual(got, c) {
			t.Errorf("Call(%v) = %v, want %v", c, got, c)
		}
	}
}

// TestEchoOptIn 验证 Echo 必须显式 Register 才会被找到（不自动注入）。
func TestEchoOptIn(t *testing.T) {
	api := tools.NewToolsAPI(nil)
	_, err := api.Call(context.Background(), "util.echo", "x")
	if err == nil {
		t.Fatal("Echo should not be auto-registered")
	}
}

// 极简 Context 实现，只给 Bus；Echo 不需要 Checker。
type busBusContext struct {
	bus bus.Bus
}

func (b *busBusContext) Bus() bus.Bus                { return b.bus }
func (b *busBusContext) Checker() permission.Checker { return nil }
func (b *busBusContext) API() *tools.ToolsAPI        { return nil }
