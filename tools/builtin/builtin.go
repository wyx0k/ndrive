// Package builtin 提供 ndrive 的极简内置 plugin。
//
// v5 起步只提供 util.echo 一个示例：返回 args 原样。
// 全部 opt-in：用户显式 `api.Register(builtin.Echo(), nil)` 才会注入；不自动注册。
package builtin

import (
	"context"

	"github.com/wyx0k/ndrive/tools"
)

// Echo 返回一个 "util.echo" plugin：把 args 原样返回。
// 用于 smoke test / 调试 / placeholder 场景。
// 标记为 Read=true，permission 门控走 CheckRead（默认放行）。
func Echo() tools.Plugin {
	return tools.SimplePluginDef{
		ID_:          "util.echo",
		Description_: "返回 args 原样（仅用于调试 / 占位）",
		Schema_: map[string]any{
			"type": "object",
		},
		Read_: true,
		Fn_: func(_ context.Context, _ tools.Context, args any) (any, error) {
			return args, nil
		},
	}
}
