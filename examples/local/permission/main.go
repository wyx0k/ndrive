// permission 演示 ndrive 的 4 档权限模型（只读 / 只写 / 写询问 / 全询问）。
//
// 覆盖：
//   - SetPolicy / GetPolicy / ListPolicies / DeletePolicy
//   - 4 档 mode 对读写操作的放行/拒绝行为
//   - 询问流程：bus.Handle("permission.ask", ...) + Request 端到端
//
// 注意：4 档 mode **不是一条严格度链** —— 它们是"读"和"写"两个维度上的
// 四象限（只读只写正交，谁更严取决于操作）。
//
// 运行：go run ./examples/local/permission
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/wyx0k/ndrive"
	"github.com/wyx0k/ndrive/fs/local"
	"github.com/wyx0k/ndrive/meta/sqlite"
	"github.com/wyx0k/ndrive/permission"
	"github.com/wyx0k/ndrive/tools"
)

func main() {
	ctx := context.Background()
	dbPath := "./ndrive-permission.db"
	_ = os.Remove(dbPath)
	defer os.Remove(dbPath)
	wsRoot := "./ndrive-permission-ws"
	_ = os.RemoveAll(wsRoot)
	defer os.RemoveAll(wsRoot)

	ws, err := ndrive.OpenWorkspace(ctx, "demo", "alice", ndrive.WorkspaceConfig{
		Meta: sqlite.Config{Path: dbPath},
		FS:   local.Config{Path: wsRoot},
	})
	if err != nil {
		panic(err)
	}
	defer ws.Close()

	// 注册一个只读工具和一个写工具，用来演示门控差异。
	must(ws.Register(tools.ToolDef{
		ID_: "util.echo", Description_: "echo", Read_: true,
		Fn_: func(_ context.Context, _ tools.Context, args any) (any, error) { return args, nil },
	}))
	must(ws.Register(tools.ToolDef{
		ID_: "func.write", Description_: "write op", Read_: false,
		Fn_: func(_ context.Context, _ tools.Context, args any) (any, error) { return args, nil },
	}))

	// 1. 4 档 mode 对读写操作的放行/拒绝行为。
	demo := func(mode permission.Mode) {
		must(ws.SetPolicy(ctx, "alice", mode))
		fmt.Printf("mode=%-5s", mode)

		_, err := ws.API().Call(ctx, "util.echo", "x")
		fmt.Printf("  read:%-7s", status(err))

		_, err = ws.API().Call(ctx, "func.write", "x")
		fmt.Printf("  write:%s\n", status(err))
	}
	demo(permission.ModeReadOnly)  // 只读：读放行，写拒绝
	demo(permission.ModeWriteOnly) // 全写：写放行，读拒绝
	demo(permission.ModeWriteAsk)  // 读放行，写询问（无订阅者时直接拒绝）
	demo(permission.ModeAskAll)    // 全部询问（无订阅者时直接拒绝）

	// 2. 询问流程端到端：UI 端订阅 permission.ask，自动批准。
	fmt.Println("\n--- 全询问（ModeAskAll）流程 ---")
	unsubAsk, err := ws.Bus().Handle("permission.ask", func(_ context.Context, req any) (any, error) {
		payload, _ := req.(permission.AskPayload)
		fmt.Printf("[UI 收到询问] op=%s user=%s\n", payload.Op, payload.UserID)
		time.Sleep(20 * time.Millisecond) // 模拟用户决策耗时
		return permission.AskResponse{Approved: true}, nil
	})
	must(err)
	defer unsubAsk()

	must(ws.SetPolicy(ctx, "alice", permission.ModeAskAll))
	v, err := ws.API().Call(ctx, "func.write", "payload")
	must(err)
	fmt.Println("ModeAskAll + 订阅者批准 → 调用成功:", v)

	// 3. Policy 管理方法：List / Delete。
	fmt.Println("\n--- Policy 管理 ---")
	must(ws.SetPolicy(ctx, "bob", permission.ModeReadOnly))
	must(ws.SetPolicy(ctx, "carol", permission.ModeWriteAsk))

	bindings, err := ws.ListPolicies(ctx)
	must(err)
	for _, b := range bindings {
		fmt.Printf("binding: user=%s mode=%s\n", b.UserID, b.Mode)
	}

	must(ws.DeletePolicy(ctx, "bob"))
	_, err = ws.GetPolicy(ctx, "bob")
	fmt.Println("after delete, GetPolicy(bob):", err)
}

func status(err error) string {
	if err == nil {
		return "ok"
	}
	return "denied"
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "FATAL:", err)
		os.Exit(1)
	}
}
