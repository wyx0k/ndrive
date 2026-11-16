// builtin.go：ndrive 的极简内置工具。
//
// 目前只提供 util.echo 一个示例：把 args 原样返回。
// 全部 opt-in：用户显式 `api.Register(tools.Echo())` 才会注入；不自动注册。
package tools

import (
	"context"
)

// Echo 返回一个 "util.echo" 工具：把 args 原样返回。
// 用于 smoke test / 调试 / placeholder 场景。
// 标记为 Read=true，permission 门控走 CheckRead（默认放行）。
func Echo() Tool {
	return ToolDef{
		ID_:          "util.echo",
		Description_: "返回 args 原样（仅用于调试 / 占位）",
		Schema_: map[string]any{
			"type": "object",
		},
		Read_: true,
		Fn_: func(_ context.Context, _ Context, args any) (any, error) {
			return args, nil
		},
	}
}
