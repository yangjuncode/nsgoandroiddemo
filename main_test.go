package main

import (
	"testing"
	"time"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/nsgotest"
)

func TestAppAndBackgroundServiceLifecycle(t *testing.T) {
	host := core.NewHost()
	finishCalled := make(chan struct{}, 1)
	host.Handle("finish_activity", func(call *core.HostCall) {
		select {
		case finishCalled <- struct{}{}:
		default:
		}
		call.Resolve(true)
	})
	host.Handle("service_log", func(call *core.HostCall) {
		call.Resolve(true)
	})

	a := nsgotest.Start(t, App, nsgotest.Host(host))
	a.Find("text:NSGo 前台常驻 Go Service")

	if !bgService.IsUIAttached() {
		t.Fatalf("expected UI to be attached after Start")
	}

	// 1. 测试点击触发后台异步任务
	a.Press("id:btn-trigger")
	a.Find("text:⚡ 触发后台异步任务 (已点 1 次)")

	// 2. 模拟 Android MainActivity 被关闭 (onDestroy -> 发送 activity_lifecycle = detached)
	host.Send("activity_lifecycle", "detached")
	a.Advance(10 * time.Millisecond)
	if bgService.IsUIAttached() {
		t.Fatalf("expected bgService.IsUIAttached() == false after Activity detached")
	}

	// 等待 1.2 秒，验证 Activity 关闭期间后台 Service 依然独立运行并累加 OfflineTicks
	beforeSnap := bgService.GetSnapshot()
	time.Sleep(1200 * time.Millisecond)
	afterSnap := bgService.GetSnapshot()
	if afterSnap.Tick <= beforeSnap.Tick || afterSnap.OfflineTicks <= beforeSnap.OfflineTicks {
		t.Fatalf("expected background service to keep ticking while UI is closed: before=%+v, after=%+v", beforeSnap, afterSnap)
	}

	// 3. 模拟 Android MainActivity 重新打开 (onCreate -> 发送 activity_lifecycle = attached)
	host.Send("activity_lifecycle", "attached")
	a.Advance(10 * time.Millisecond)
	if !bgService.IsUIAttached() {
		t.Fatalf("expected bgService.IsUIAttached() == true after Activity re-attached")
	}
	a.Find("text:UI 生命周期状态: 🟢 已连接 (Attached) (第 2 次打开 Activity)")
}
