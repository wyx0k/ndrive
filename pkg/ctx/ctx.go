package ctx

import "context"

type ctxKey int

const (
	ownerKey     ctxKey = iota
	workspaceKey ctxKey = iota
)

func WithOwner(ctx context.Context, ownerID string) context.Context {
	return context.WithValue(ctx, ownerKey, ownerID)
}

func Owner(ctx context.Context) string {
	v, _ := ctx.Value(ownerKey).(string)
	return v
}

func WithWorkspace(ctx context.Context, wsID string) context.Context {
	return context.WithValue(ctx, workspaceKey, wsID)
}

func Workspace(ctx context.Context) string {
	v, _ := ctx.Value(workspaceKey).(string)
	return v
}
