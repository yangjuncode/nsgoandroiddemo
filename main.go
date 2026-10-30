package main

import (
	"fmt"
	"strings"
	"sync"

	"nsgodemo/service"

	"github.com/oneo-me/nsgo"
	"github.com/oneo-me/nsgo/core"
)

var bgService *service.BackgroundService

func init() {
	// 随 Go 进程启动前台常驻后台服务（整个进程生命周期仅启动一次）
	bgService = service.NewBackgroundService()
}

func main() {
	nsgo.Run(App)
}

// App 为 NSGo 根组件函数（Render runs once under root scope, signals keep ContentFunc up to date）
func App(cx *core.Scope) core.Node {
	ctx := cx.Context()
	initSnap := bgService.GetSnapshot()
	statusSignal := core.NewSignal(cx, initSnap.Status)
	tickSignal := core.NewSignal(cx, initSnap.Tick)
	offlineTickSignal := core.NewSignal(cx, initSnap.OfflineTicks)
	attachCountSignal := core.NewSignal(cx, initSnap.AttachCount)
	uiAttachedSignal := core.NewSignal(cx, true)
	logSignal := core.NewSignal(cx, formatLogs(initSnap.RecentLogs))
	clickCountSignal := core.NewSignal(cx, 0)

	// 将 Go Service 的每次日志同步给 Android 外层 ForegroundGoService (打印到 Logcat NSGoService 并更新常驻通知栏)
	bgService.SetLogHook(func(msg string) {
		cx.Post(func() {
			cx.PostHost("service_log", msg)
		})
	})

	var sessionMu sync.Mutex
	var activeSession *service.UISession

	applySnapshot := func(snap service.ServiceSnapshot) {
		statusSignal.Set(snap.Status)
		tickSignal.Set(snap.Tick)
		offlineTickSignal.Set(snap.OfflineTicks)
		attachCountSignal.Set(snap.AttachCount)
		uiAttachedSignal.Set(snap.UIAttached)
		logSignal.Set(formatLogs(snap.RecentLogs))
	}

	detachCurrent := func() {
		sessionMu.Lock()
		sess := activeSession
		activeSession = nil
		sessionMu.Unlock()
		if sess != nil {
			bgService.DetachUI(sess.ID)
			uiAttachedSignal.Set(false)
		}
	}

	attachNew := func(source string) {
		sessionMu.Lock()
		if activeSession != nil {
			sessionMu.Unlock()
			return
		}
		sess := bgService.AttachUI(source)
		activeSession = sess
		sessionMu.Unlock()

		// 立即应用重连时的后台状态快照（含 Activity 关闭期间后台跑的 Tick 与日志）
		applySnapshot(sess.Snapshot)

		go func(s *service.UISession) {
			for {
				select {
				case <-ctx.Done():
					return
				case ev, ok := <-s.Events:
					if !ok {
						return
					}
					cx.Post(func() {
						statusSignal.Set(ev.Status)
						tickSignal.Set(ev.Tick)
						offlineTickSignal.Set(ev.OfflineTicks)
						uiAttachedSignal.Set(ev.UIAttached)
						logSignal.Set(formatLogs(ev.RecentLogs))
					})
				}
			}
		}(sess)
	}

	// 初始挂载 UI 会话
	attachNew("MainActivity")

	// 监听外层 Android MainActivity 的生命周期事件（打开 attached / 关闭 detached）
	cx.OnHostEvent("activity_lifecycle", func(data any) {
		state, _ := data.(string)
		switch state {
		case "detached":
			detachCurrent()
		case "attached":
			attachNew("MainActivity")
		}
	})

	// 当 Scope 销毁时自动解绑 UI 会话
	cx.Cleanup(func() {
		bgService.SetLogHook(nil)
		detachCurrent()
	})

	return core.View{
		Class: "flex-1 flex-col bg-slate-900 p-5 gap-3",
		Children: []core.Node{
			// 顶部标题栏（含自定义服务图标）
			core.View{
				Class: "flex-row items-center bg-slate-800 p-3.5 rounded-2xl gap-3",
				Children: []core.Node{
					core.Image{
						Source: core.Src("service_icon.png"),
						Class:  "w-12 h-12 rounded-xl",
					},
					core.View{
						Class: "flex-1 flex-col gap-1",
						Children: []core.Node{
							core.Text{
								Content: "NSGo 前台常驻 Go Service",
								Class:   "text-lg font-bold text-slate-50",
							},
							core.Text{
								Content: "外层 Android 宿主工程 (-embed) · 状态栏带图标前台服务",
								Class:   "text-xs text-emerald-400",
							},
						},
					},
				},
			},

			// 后台服务状态与生命周期感知仪表盘
			core.View{
				Class: "flex-col bg-slate-800 p-3.5 rounded-2xl gap-1.5",
				Children: []core.Node{
					core.Text{
						Class: "text-xs font-bold text-sky-400",
						ContentFunc: func() string {
							return fmt.Sprintf("前台服务总运行: %d Ticks | Activity 关闭期间后台跑了: %d Ticks",
								tickSignal.Get(), offlineTickSignal.Get())
						},
					},
					core.Text{
						Class: "text-xs text-emerald-300",
						ContentFunc: func() string {
							badge := "🟢 已连接 (Attached)"
							if !uiAttachedSignal.Get() {
								badge = "🔴 已解绑 (Detached)"
							}
							return fmt.Sprintf("UI 生命周期状态: %s (第 %d 次打开 Activity)",
								badge, attachCountSignal.Get())
						},
					},
					core.Text{
						Class: "text-sm font-medium text-slate-100",
						ContentFunc: func() string {
							return statusSignal.Get()
						},
					},
				},
			},

			// 交互按钮区：1. 触发后台任务；2. 关闭 Activity 测试后台存活
			core.View{
				Class: "flex-col gap-2.5",
				Children: []core.Node{
					core.View{
						Class:  "bg-blue-600 py-3 px-4 rounded-xl items-center justify-center",
						Role:   core.RoleButton,
						TestID: "btn-trigger",
						OnPress: func(core.PressEvent) {
							next := clickCountSignal.Get() + 1
							clickCountSignal.Set(next)
							bgService.DoSomething(fmt.Sprintf("用户点击任务 #%d", next))
						},
						Children: []core.Node{
							core.Text{
								Class: "text-sm font-bold text-white",
								ContentFunc: func() string {
									return fmt.Sprintf("⚡ 触发后台异步任务 (已点 %d 次)", clickCountSignal.Get())
								},
							},
						},
					},
					core.View{
						Class:  "bg-red-600 py-3 px-4 rounded-xl items-center justify-center",
						Role:   core.RoleButton,
						TestID: "btn-close-activity",
						OnPress: func(core.PressEvent) {
							// 调用外层 Android MainActivity.finish() 关闭 Activity，验证后台 Service 独立存活与感知
							cx.PostHost("finish_activity", nil)
						},
						Children: []core.Node{
							core.Text{
								Content: "🚪 关闭 UI Activity (测试 Service 后台存活与感知)",
								Class:   "text-sm font-bold text-white",
							},
						},
					},
				},
			},

			// 实时同步与离线缓存日志面板
			core.View{
				Class: "flex-1 flex-col bg-slate-950 p-3 rounded-xl gap-1",
				Children: []core.Node{
					core.Text{
						Content: "实时 Logcat 同步日志 (含 Activity 关闭期间后台记录):",
						Class:   "text-xs font-bold text-slate-400",
					},
					core.Text{
						Class: "text-xs text-slate-300",
						ContentFunc: func() string {
							return logSignal.Get()
						},
					},
				},
			},
		},
	}
}

func formatLogs(logs []string) string {
	if len(logs) == 0 {
		return "等待后台服务日志..."
	}
	return strings.Join(logs, "\n")
}
