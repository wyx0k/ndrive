package ntd

// PolicyMode 控制 owner 在 workspace 内的读写权限
type PolicyMode int

const (
	RAsk PolicyMode = iota // 读也询问（最低权限）
	R                      // 只读，写全拒
	WAsk                   // 写时询问，读直通
	W                      // 读写全直通（最高权限）
)
