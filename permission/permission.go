// Package permission 提供 ndrive 的 4 档权限门控。
//
// v5 设计要点：
//   - 改名 policy → permission（全仓库统一）
//   - owner_id → user_id
//   - Checker 拆成 CheckRead / CheckWrite（plugin.Read() 路由）
//   - 每 user 一个 mode（Binding 按 (wsID, userID) 唯一索引）
//   - AskPayload.Preview 允许空字符串
//   - 无订阅者返 ErrPermissionDenied
//   - ctx 超时由 permission.ask handler 内部 listen，permission 收到 ErrRequestTimeout 转译为 ErrPermissionDenied
//   - WorkspaceStore 接口注入给 NewChecker（封装 Set/Get/List/Delete）
package permission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wyx0k/ndrive/bus"
)

// ErrPermissionDenied 是 permission 拒绝时的统一错误。
var ErrPermissionDenied = errors.New("permission denied")

// Mode 权限档位。
type Mode int

const (
	// RAsk 只读模式，所有读 op 直接放行；写 op 发 permission.ask 询问
	RAsk Mode = iota
	// R 严格只读，写 op 一律拒绝
	R
	// WAsk 默认写模式，所有 op（读/写）都发 permission.ask 询问
	WAsk
	// W 全权限写模式，所有写 op 直接放行；读 op 拒绝
	W
)

// String 实现 fmt.Stringer；用于日志和持久化展示。
func (m Mode) String() string {
	switch m {
	case RAsk:
		return "RAsk"
	case R:
		return "R"
	case WAsk:
		return "WAsk"
	case W:
		return "W"
	}
	return fmt.Sprintf("Mode(%d)", int(m))
}

// MarshalJSON / UnmarshalJSON 用数字（iota 值）持久化，避免 iota 顺序变动破坏历史数据。
func (m Mode) MarshalJSON() ([]byte, error) {
	return json.Marshal(int(m))
}

func (m *Mode) UnmarshalJSON(data []byte) error {
	var n int
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*m = Mode(n)
	return nil
}

// Binding 是 (wsID, userID) → Mode 的绑定关系。
type Binding struct {
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id"`
	Mode        Mode   `json:"mode"`
}

// AskPayload 是 permission.ask 请求的 payload。
type AskPayload struct {
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id"`
	Op          string `json:"op"`
	Preview     string `json:"preview"` // v5: 允许空字符串
}

// AskResponse 是 UI 端 Handle("permission.ask") 的返值。
type AskResponse struct {
	Approved bool `json:"approved"`
}

// WorkspaceStore 是 permission 包对 binding 持久化的最小接口。
// 实现方：ndrive 根包通过 internal/meta 暴露。
type WorkspaceStore interface {
	GetBinding(ctx context.Context, wsID, userID string) (Binding, error)
	SetBinding(ctx context.Context, b Binding) error
	DeleteBinding(ctx context.Context, wsID, userID string) error
	ListBindings(ctx context.Context, wsID string) ([]Binding, error)
}

// Checker 是 permission 门控的核心接口。
// v5 拆成 CheckRead / CheckWrite 两个方法（plugin.Read() 路由）。
type Checker interface {
	CheckRead(ctx context.Context, op string) error
	CheckWrite(ctx context.Context, op string) error
}

// checker 默认实现。
type checker struct {
	store  WorkspaceStore
	wsID   string
	userID string
	bus    bus.Bus
}

// NewChecker 构造一个 Checker 实例。
// 每次 NewChecker 绑定 (wsID, userID)；同 ws 多 user 调多次 NewChecker。
func NewChecker(store WorkspaceStore, wsID, userID string, b bus.Bus) Checker {
	return &checker{store: store, wsID: wsID, userID: userID, bus: b}
}

// CheckRead 走 mode 对 read 的判定。
func (c *checker) CheckRead(ctx context.Context, op string) error {
	return c.check(ctx, op, true)
}

// CheckWrite 走 mode 对 write 的判定。
func (c *checker) CheckWrite(ctx context.Context, op string) error {
	return c.check(ctx, op, false)
}

// check 统一逻辑：read=true 是读，read=false 是写。
func (c *checker) check(ctx context.Context, op string, read bool) error {
	b, err := c.store.GetBinding(ctx, c.wsID, c.userID)
	if err != nil {
		return err
	}
	write := !read
	switch b.Mode {
	case W:
		if write {
			return nil
		}
		return fmt.Errorf("%w: op=%s mode=W (read op not allowed)", ErrPermissionDenied, op)
	case WAsk:
		if !write {
			// WAsk 对读也走 ask
		}
		return c.ask(ctx, op)
	case R:
		if write {
			return fmt.Errorf("%w: op=%s mode=R (write op not allowed)", ErrPermissionDenied, op)
		}
		return nil
	case RAsk:
		if !write {
			return nil
		}
		return c.ask(ctx, op)
	}
	return nil
}

// ask 发 permission.ask 请求到 bus，等 UI 端 AskResponse。
// 错误处理：
//   - 无订阅者 → ErrPermissionDenied
//   - 超时（ErrRequestTimeout）→ ErrPermissionDenied
//   - handler 返非 AskResponse / Approved=false → ErrPermissionDenied
func (c *checker) ask(ctx context.Context, op string) error {
	resp, err := c.bus.Request(ctx, "permission.ask", AskPayload{
		WorkspaceID: c.wsID,
		UserID:      c.userID,
		Op:          op,
		Preview:     "",
	})
	if errors.Is(err, bus.ErrNoSubscriber) {
		return fmt.Errorf("%w: op=%s (no subscriber)", ErrPermissionDenied, op)
	}
	if errors.Is(err, bus.ErrRequestTimeout) {
		return fmt.Errorf("%w: op=%s (timeout)", ErrPermissionDenied, op)
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPermissionDenied, err)
	}
	ar, ok := resp.(AskResponse)
	if !ok || !ar.Approved {
		return fmt.Errorf("%w: op=%s denied by user", ErrPermissionDenied, op)
	}
	return nil
}
