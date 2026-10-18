// basic_workspace 演示 ndrive 最基础的用法：本地单机场景。
//
// 覆盖：
//   - OpenWorkspace 用 SqliteConfig（本地文件存储）
//   - Workspace 生命周期：ID / UserID / Close
//   - ownership 保护：同一 wsID 换 userID 重开会被拒绝
//   - Context：把 wsID/userID 注入 context，供下游代码用 FromContext 取回
//
// 运行：go run ./examples/local/basic_workspace
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/wyx0k/ndrive"
	"github.com/wyx0k/ndrive/meta"
)

func main() {
	ctx := context.Background()
	dbPath := "./ndrive-basic.db"
	_ = os.Remove(dbPath)
	defer os.Remove(dbPath)

	// 本地场景：Meta 用 SqliteConfig，数据落单个本地文件。
	ws, err := ndrive.OpenWorkspace(ctx, "demo", "alice", ndrive.WorkspaceConfig{
		WorkerID: 1,
		Meta:     ndrive.SqliteConfig{Path: dbPath},
	})
	if err != nil {
		panic(err)
	}
	defer ws.Close()

	fmt.Printf("workspace=%s user=%s\n", ws.ID(), ws.UserID())

	// ownership 保护：同一 wsID 换 userID 重开会被拒绝。
	_, err = ndrive.OpenWorkspace(ctx, "demo", "bob", ndrive.WorkspaceConfig{
		WorkerID: 1,
		Meta:     ndrive.SqliteConfig{Path: dbPath},
	})
	if errors.Is(err, meta.ErrWorkspaceExists) {
		fmt.Println("ownership check ok: bob 被拒绝，demo 已属于 alice")
	} else if err != nil {
		panic(err)
	}

	// Context：把 wsID/userID 注入下游 context。
	downstreamCtx := ws.Context(ctx)
	wsID, userID := ndrive.FromContext(downstreamCtx)
	fmt.Printf("from context: wsID=%s userID=%s\n", wsID, userID)
}
