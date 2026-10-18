package e2e_test

import (
	"context"
	"testing"

	"github.com/wyx0k/ndrive/permission"
	"github.com/wyx0k/ndrive/tools"
)

func TestSetGetPolicy(t *testing.T) {
	ws := openWS(t, "policy-setget")
	ctx := context.Background()

	if err := ws.SetPolicy(ctx, "alice", permission.W); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}
	m, err := ws.GetPolicy(ctx, "alice")
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}
	if m != permission.W {
		t.Errorf("got mode %v, want W", m)
	}
}

func TestSetPolicyOverwrite(t *testing.T) {
	ws := openWS(t, "policy-overwrite")
	ctx := context.Background()

	_ = ws.SetPolicy(ctx, "alice", permission.R)
	_ = ws.SetPolicy(ctx, "alice", permission.W)

	m, _ := ws.GetPolicy(ctx, "alice")
	if m != permission.W {
		t.Errorf("after overwrite: got %v, want W", m)
	}
}

func TestGetPolicyNotFound(t *testing.T) {
	ws := openWS(t, "policy-notfound")
	ctx := context.Background()

	_, err := ws.GetPolicy(ctx, "ghost")
	if err == nil {
		t.Error("want error for missing userID")
	}
}

func TestListPolicies(t *testing.T) {
	ws := openWS(t, "policy-list")
	ctx := context.Background()

	_ = ws.SetPolicy(ctx, "alice", permission.R)
	_ = ws.SetPolicy(ctx, "bob", permission.W)
	_ = ws.SetPolicy(ctx, "carol", permission.RAsk)

	bs, err := ws.ListPolicies(ctx)
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}
	if len(bs) != 3 {
		t.Errorf("want 3 bindings, got %d", len(bs))
	}
	users := map[string]permission.Mode{}
	for _, b := range bs {
		users[b.UserID] = b.Mode
	}
	if users["alice"] != permission.R {
		t.Errorf("alice: %v", users["alice"])
	}
	if users["bob"] != permission.W {
		t.Errorf("bob: %v", users["bob"])
	}
	if users["carol"] != permission.RAsk {
		t.Errorf("carol: %v", users["carol"])
	}
}

func TestDeletePolicy(t *testing.T) {
	ws := openWS(t, "policy-delete")
	ctx := context.Background()

	_ = ws.SetPolicy(ctx, "alice", permission.W)
	if err := ws.DeletePolicy(ctx, "alice"); err != nil {
		t.Fatalf("DeletePolicy: %v", err)
	}
	if _, err := ws.GetPolicy(ctx, "alice"); err == nil {
		t.Error("after delete, GetPolicy should fail")
	}
}

func TestSetPolicyEmptyUserID(t *testing.T) {
	ws := openWS(t, "policy-empty")
	if err := ws.SetPolicy(context.Background(), "", permission.W); err == nil {
		t.Error("want error for empty userID")
	}
}

// TestPolicyEndToEnd 验证 permission 包 + ndrive 集成的 end-to-end flow。
func TestPolicyEndToEnd(t *testing.T) {
	ws := openWS(t, "policy-e2e") // ws 的 userID 是 "u1"
	ctx := context.Background()

	// 设置 u1 为 RAsk
	if err := ws.SetPolicy(ctx, "u1", permission.RAsk); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}

	// 装一个 permission.ask handler 返 Approved
	_, err := ws.Bus().Handle("permission.ask", func(ctx context.Context, req any) (any, error) {
		p, ok := req.(permission.AskPayload)
		if !ok {
			t.Errorf("bad payload type: %T", req)
		}
		if p.UserID != "u1" || p.WorkspaceID != "policy-e2e" {
			t.Errorf("ask payload wrong: %+v", p)
		}
		return permission.AskResponse{Approved: true}, nil
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// 注册一个写 plugin
	if err := ws.Register(tools.SimplePluginDef{
		ID_:          "user.send",
		Description_: "send",
		Read_:        false, // 写
		Fn_: func(_ context.Context, _ tools.Context, args any) (any, error) {
			return args, nil
		},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// 调用：u1 是 RAsk + 写 op → 走 ask → handler approve → 成功
	if _, err := ws.API().Call(ctx, "user.send", "payload"); err != nil {
		t.Errorf("Call: %v", err)
	}
}
