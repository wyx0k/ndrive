// fsevents.go：把文件系统的变更事件桥接到 bus。
//
// fs 包不依赖 bus（存储层不该知道消息总线存在），变更事件通过可选的
// fs.Watcher 接口以 Go 原生 channel 暴露。根包在这里做一次桥接：把事件
// 发到 bus 的 fs.TopicChange（"fs.change"）上。
//
// 桥接是单向的、fire-and-forget：
//   - fs 实现不实现 fs.Watcher（如只读后端）→ 什么都不做，工作区照常工作；
//   - 事件 channel 关闭（fs.Close 之后）→ 桥接 goroutine 自然退出。
package ndrive

import (
	"github.com/wyx0k/ndrive/bus"
	"github.com/wyx0k/ndrive/fs"
)

// bridgeFSEvents 把 fs 的变更事件转发到 bus。
//
// fs 实现不实现 fs.Watcher 时直接返回（不启 goroutine）。
func bridgeFSEvents(b bus.Bus, svc fs.FileService) {
	if b == nil || svc == nil {
		return
	}
	w, ok := svc.(fs.Watcher)
	if !ok {
		return
	}
	go func() {
		for ev := range w.Events() {
			b.Publish(fs.TopicChange, ev)
		}
	}()
}
