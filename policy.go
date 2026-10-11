package ntd

import pkgpolicy "ntd/pkg/policy"

// PolicyMode 控制 owner 在 workspace 内的读写权限
type PolicyMode int

const (
	RAsk PolicyMode = iota // 读也询问（最低权限）
	R                      // 只读，写全拒
	WAsk                   // 写时询问，读直通
	W                      // 读写全直通（最高权限）
)

// AskPayload policy.ask 请求的 payload，由引擎发出，下游 handler 接收
type AskPayload = pkgpolicy.AskPayload

// AskResponse policy.ask 的回复，handler 返回此类型
type AskResponse = pkgpolicy.AskResponse
