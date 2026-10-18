// keys.go：所有 meta key 命名空间集中管理。
//
// 上层（根 ndrive 包 / 其他 facade）只通过本文件的函数构造 key，
// 不直接拼字符串。这样改 key 格式时只动本文件。
package meta

// key 种类。
const (
	KindWorkspace         = "workspace"
	KindPermissionBinding = "permission_binding"
	KindPlugin            = "plugin"
)

// WorkspaceKey 构造 workspace/<id> key。
func WorkspaceKey(wsID string) string {
	return KindWorkspace + "/" + wsID
}

// PermissionKey 构造 permission_binding/<wsID>/<userID> key。
func PermissionKey(wsID, userID string) string {
	return KindPermissionBinding + "/" + wsID + "/" + userID
}

// PermissionPrefix 构造 permission_binding/<wsID>/ 前缀（用于 List）。
func PermissionPrefix(wsID string) string {
	return KindPermissionBinding + "/" + wsID + "/"
}

// PluginKey 构造 plugin/<id> key（预留）。
func PluginKey(id string) string {
	return KindPlugin + "/" + id
}
