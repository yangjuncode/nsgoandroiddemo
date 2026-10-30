package service

import (
	"fmt"
	"log"
	"sync"
	"time"
)

const maxRecentLogs = 8

// ServiceEvent 代表后台 Service 推送给当前活跃 UI 会话的状态事件
type ServiceEvent struct {
	Tick         int
	OfflineTicks int
	UIAttached   bool
	Status       string
	RecentLogs   []string
}

// ServiceSnapshot 代表 UI 重新打开并绑定时立即获取到的当前后台状态快照
type ServiceSnapshot struct {
	Tick         int
	OfflineTicks int
	AttachCount  int
	UIAttached   bool
	Status       string
	RecentLogs   []string
}

// UISession 代表一次 UI Activity 生命周期的订阅会话（替代原先单一裸 Channel）
type UISession struct {
	ID       uint64
	Name     string
	Events   <-chan ServiceEvent
	Snapshot ServiceSnapshot
}

type subscriber struct {
	id   uint64
	name string
	ch   chan ServiceEvent
}

// BackgroundService 模拟常驻后台的前台 Go Service
// 采用 Session Hub（观察者订阅 + 状态快照）替代单一裸 Channel：
//  1. 当 UI Activity 被关闭时，通过 DetachUI 立即感知 UI 离线，不向死 Channel 阻塞写入；
//  2. UI 关闭期间，Service 继续在后台独立运行并打印到 Logcat，同时将日志保存在环形缓冲区；
//  3. 当 UI Activity 重新打开时，通过 AttachUI 立即感知重连，并同步最新快照与离线期间日志。
type BackgroundService struct {
	mu                 sync.RWMutex
	nextSessionID      uint64
	subscribers        map[uint64]*subscriber
	currentTick        int
	offlineTicks       int
	lastSessionOffline int
	attachCount        int
	latestStatus       string
	recentLogs         []string
	logHook            func(msg string)
	stopChan           chan struct{}
}

// NewBackgroundService 创建并启动后台常驻服务
func NewBackgroundService() *BackgroundService {
	s := &BackgroundService{
		subscribers:  make(map[uint64]*subscriber),
		latestStatus: "正在初始化前台常驻 Service...",
		recentLogs:   make([]string, 0, maxRecentLogs),
		stopChan:     make(chan struct{}),
	}
	s.logcat("🚀 [ForegroundService] 后台常驻 Go Service 已启动 (PID 存活于前台服务进程)")
	go s.workerLoop()
	return s
}

// SetLogHook 注册宿主（如 Android ForegroundService）的日志与通知栏同步回调
func (s *BackgroundService) SetLogHook(fn func(msg string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logHook = fn
}

func (s *BackgroundService) logcat(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	// 标准错误流会被 Android 端日志线程直接桥接到 Logcat (I/nsgo & I/NSGoService)
	log.Printf("[NSGoService] %s", msg)
	s.mu.RLock()
	hook := s.logHook
	s.mu.RUnlock()
	if hook != nil {
		hook(msg)
	}
}

func (s *BackgroundService) appendLogLocked(entry string) {
	ts := time.Now().Format("15:04:05")
	line := fmt.Sprintf("[%s] %s", ts, entry)
	if len(s.recentLogs) >= maxRecentLogs {
		s.recentLogs = append(s.recentLogs[1:], line)
	} else {
		s.recentLogs = append(s.recentLogs, line)
	}
}

func (s *BackgroundService) cloneLogsLocked() []string {
	cp := make([]string, len(s.recentLogs))
	copy(cp, s.recentLogs)
	return cp
}

// AttachUI 当 UI Activity 创建或重新打开时调用，建立订阅会话并返回最新快照
func (s *BackgroundService) AttachUI(name string) *UISession {
	s.mu.Lock()
	s.nextSessionID++
	id := s.nextSessionID
	s.attachCount++
	ch := make(chan ServiceEvent, 16)
	s.subscribers[id] = &subscriber{
		id:   id,
		name: name,
		ch:   ch,
	}

	offlineRun := s.lastSessionOffline
	s.lastSessionOffline = 0

	var attachMsg string
	if s.attachCount == 1 {
		attachMsg = fmt.Sprintf("🟢 感知到 UI Activity 首次打开并连接 (session#%d, %s)", id, name)
	} else {
		attachMsg = fmt.Sprintf("🟢 感知到 UI Activity 重新打开并重连 (session#%d, 第%d次连接, UI关闭期间后台已独立运行 %d Tick)",
			id, s.attachCount, offlineRun)
	}
	s.latestStatus = attachMsg
	s.appendLogLocked(attachMsg)

	snap := ServiceSnapshot{
		Tick:         s.currentTick,
		OfflineTicks: s.offlineTicks,
		AttachCount:  s.attachCount,
		UIAttached:   true,
		Status:       s.latestStatus,
		RecentLogs:   s.cloneLogsLocked(),
	}
	s.mu.Unlock()

	s.logcat("%s | 当前总 Tick=%d", attachMsg, snap.Tick)
	return &UISession{
		ID:       id,
		Name:     name,
		Events:   ch,
		Snapshot: snap,
	}
}

// DetachUI 当 UI Activity 被关闭 (onDestroy) 时调用，Service 立即感知 UI 离线但继续存活
func (s *BackgroundService) DetachUI(sessionID uint64) {
	s.mu.Lock()
	sub, ok := s.subscribers[sessionID]
	if !ok {
		s.mu.Unlock()
		return
	}
	delete(s.subscribers, sessionID)
	close(sub.ch)

	remaining := len(s.subscribers)
	detachMsg := fmt.Sprintf("🔴 感知到 UI Activity 已关闭 (session#%d, %s)! Service 继续在后台存活运行", sessionID, sub.name)
	s.latestStatus = detachMsg
	s.appendLogLocked(detachMsg)
	currTick := s.currentTick
	s.mu.Unlock()

	s.logcat("%s | 当前 Tick=%d | 剩余活跃 UI 会话=%d", detachMsg, currTick, remaining)
}

// IsUIAttached 返回当前是否有活跃的 UI Activity 连接
func (s *BackgroundService) IsUIAttached() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.subscribers) > 0
}

// GetSnapshot 获取后台服务当前状态快照
func (s *BackgroundService) GetSnapshot() ServiceSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return ServiceSnapshot{
		Tick:         s.currentTick,
		OfflineTicks: s.offlineTicks,
		AttachCount:  s.attachCount,
		UIAttached:   len(s.subscribers) > 0,
		Status:       s.latestStatus,
		RecentLogs:   s.cloneLogsLocked(),
	}
}

func (s *BackgroundService) workerLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			s.currentTick++
			uiOnline := len(s.subscribers) > 0
			var msg string
			if uiOnline {
				msg = fmt.Sprintf("⚡ [前台Service] 后台运行中... Tick: %d (UI状态: 在线 Attached)", s.currentTick)
			} else {
				s.offlineTicks++
				s.lastSessionOffline++
				msg = fmt.Sprintf("🛡️ [前台Service] UI已关闭，后台独立存活中... Tick: %d (本次离线已运行 %d 秒)", s.currentTick, s.lastSessionOffline)
			}
			s.latestStatus = msg
			s.appendLogLocked(msg)
			ev := ServiceEvent{
				Tick:         s.currentTick,
				OfflineTicks: s.offlineTicks,
				UIAttached:   uiOnline,
				Status:       msg,
				RecentLogs:   s.cloneLogsLocked(),
			}
			// 仅向当前活跃的 UI 会话非阻塞广播；若 UI 已关闭则 subscribers 为空，零阻塞
			for _, sub := range s.subscribers {
				select {
				case sub.ch <- ev:
				default:
				}
			}
			s.mu.Unlock()

			// 无论 UI 是否存活，每一秒的 Tick 都打印到 Android Logcat
			s.logcat("%s", msg)

		case <-s.stopChan:
			s.logcat("🛑 [ForegroundService] 收到停止信号，后台服务退出")
			return
		}
	}
}

// DoSomething 供 UI 直接调用的同进程方法，触发后台异步任务
func (s *BackgroundService) DoSomething(action string) {
	s.logcat("📥 [ForegroundService] 收到 UI 动作指令: %s", action)
	go func() {
		time.Sleep(500 * time.Millisecond)

		s.mu.Lock()
		msg := fmt.Sprintf("✅ 动作 [%s] 处理完成! (Tick: %d)", action, s.currentTick)
		s.latestStatus = msg
		s.appendLogLocked(msg)
		ev := ServiceEvent{
			Tick:         s.currentTick,
			OfflineTicks: s.offlineTicks,
			UIAttached:   len(s.subscribers) > 0,
			Status:       msg,
			RecentLogs:   s.cloneLogsLocked(),
		}
		for _, sub := range s.subscribers {
			select {
			case sub.ch <- ev:
			default:
			}
		}
		s.mu.Unlock()

		s.logcat("📤 [ForegroundService] %s", msg)
	}()
}

// Stop 停止后台服务（仅用于测试或彻底退出）
func (s *BackgroundService) Stop() {
	select {
	case <-s.stopChan:
	default:
		close(s.stopChan)
	}
}
