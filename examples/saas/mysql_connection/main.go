// mysql_connection 演示 ndrive 在 SaaS（远端共享存储）场景下的用法。
//
// 和本地场景（见 examples/local/*）相比，唯一的区别是 WorkspaceConfig.Meta
// 换成 MysqlConfig：元信息（workspace 归属、permission binding）落到共享
// MySQL 实例，而不是本机 SQLite 文件。
//
// tool 注册 / 事件总线 / 权限模型等其它能力和本地场景完全一致，
// 用法参见 examples/local 下的各个示例，这里不重复演示。
//
// 本示例额外演示 SaaS 场景下典型的多租户隔离：
// 同一个 MySQL 实例上，不同 wsID 的数据互不干扰，且每个 workspace
// 有独立的 ownership 和 permission binding。
//
// 运行前需要有一个可访问的 MySQL 实例，并设置以下环境变量：
//
//	NDRIVE_MYSQL_HOST / NDRIVE_MYSQL_PORT / NDRIVE_MYSQL_USER
//	NDRIVE_MYSQL_PASSWORD / NDRIVE_MYSQL_DATABASE
//
// 运行：go run ./examples/saas/mysql_connection
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/wyx0k/ndrive"
	"github.com/wyx0k/ndrive/permission"
)

func main() {
	ctx := context.Background()

	meta := ndrive.MysqlConfig{
		Host:         envOr("NDRIVE_MYSQL_HOST", "127.0.0.1"),
		Port:         envInt("NDRIVE_MYSQL_PORT", 3306),
		User:         envOr("NDRIVE_MYSQL_USER", "root"),
		Password:     envOr("NDRIVE_MYSQL_PASSWORD", ""),
		Database:     envOr("NDRIVE_MYSQL_DATABASE", "ndrive"),
		MaxOpenConns: 10,
		MaxIdleConns: 5,
	}

	// 租户 A：workspace "tenant-a"，owner 是 "alice"。
	wsA, err := ndrive.OpenWorkspace(ctx, "tenant-a", "alice", ndrive.WorkspaceConfig{
		WorkerID: 1, // 每个进程/实例分配不同的 WorkerID，避免 snowflake id 冲突
		Meta:     meta,
	})
	if err != nil {
		panic(err)
	}
	defer wsA.Close()

	// 租户 B：同一个 MySQL 实例，workspace "tenant-b"，owner 是 "bob"。
	wsB, err := ndrive.OpenWorkspace(ctx, "tenant-b", "bob", ndrive.WorkspaceConfig{
		WorkerID: 2,
		Meta:     meta,
	})
	if err != nil {
		panic(err)
	}
	defer wsB.Close()

	// 两个 workspace 的 permission binding 互相隔离。
	must(wsA.SetPolicy(ctx, "alice", permission.W))
	must(wsB.SetPolicy(ctx, "bob", permission.R))

	modeA, _ := wsA.GetPolicy(ctx, "alice")
	modeB, _ := wsB.GetPolicy(ctx, "bob")
	fmt.Printf("tenant-a alice mode=%s\n", modeA)
	fmt.Printf("tenant-b bob   mode=%s\n", modeB)

	// tenant-a 看不到 tenant-b 的 binding，反之亦然。
	bindingsA, _ := wsA.ListPolicies(ctx)
	bindingsB, _ := wsB.ListPolicies(ctx)
	fmt.Printf("tenant-a bindings: %d, tenant-b bindings: %d\n", len(bindingsA), len(bindingsB))
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "FATAL:", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
