package main

// ============================================================
// swap —— 后台并发写盘（SwapManager）
// ============================================================
//
// 编排“脏数据异步刷入 .swp 文件”的后台机制，核心原则：
//
//  1. 绝不阻塞主线程（UI）：bubbletea 的 Update/View 都在同一个 goroutine 里，
//     任何磁盘 IO 都不能出现在它上面。因此系统拆成两段——
//     【生产者·UI 线程】采集快照（深拷贝，快）→ 塞入缓冲 channel；
//     【消费者·后台线程】从 channel 取快照 → 写 .swp（慢）。
//
//  2. 无线程竞争：Buffer 的唯一写者与唯一快照采集者都是 UI 线程，后台线程
//     只接触“已经拷贝出来的独立快照”，因此根本不存在共享可变状态。
//
//  3. 节流：time.Ticker 驱动，配合原子 dirty 位，只有“自上次写盘后有改动”
//     才真正排空快照；后台忙时非阻塞 channel 发送直接丢本轮，永不卡 UI。
//
//  4. 优雅退出：context.Context 取消 -> 后台线程收尾 -> 正常退出删除 .swp，
//     崩溃（进程被杀/断电）则 .swp 残留，供下次启动时恢复。

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// SnapSource 是 SwapManager 获取内存快照的抽象。实现方必须是安全的：
// SwapSnapshot 只在编辑线程（UI）调用（经 channel 交给后台），不得在后台线程直接调用它。
type SnapSource interface {
	// SwapSnapshot 返回编辑缓冲区内容的深拷贝。
	SwapSnapshot() [][]byte
}

// swapJob 是从 UI 线程交给后台写盘线程的一次快照任务。
type swapJob struct {
	meta  SwapMeta
	lines [][]byte
}

// SwapManager 管理一个后台写盘 goroutine。每实例对应一个正文文件的 .swp。
type SwapManager struct {
	src      SnapSource    // 快照来源（编辑缓冲区）
	swapPath string        // .swp 绝对路径
	filePath string        // 正文路径（写入元数据）
	interval time.Duration // 节流检查间隔

	dirty   atomic.Bool  // 自上次写盘后是否有改动（无锁，O(1)）
	snapCh  chan swapJob // UI线程 -> 后台线程 的快照队列（有缓冲）
	request func()       // 触发一次采集（应用中=向 UI 线程投递 swapTick 消息）

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex // 保护一次性生命周期（Start/Stop 互斥）
	started bool
}

// NewSwapManager 构造管理器。
//   - src：实现 SnapSource 的编辑缓冲区（如 *Buffer）；
//   - swapPath：.swp 文件绝对路径；
//   - filePath：正文文件绝对路径（写入元数据用于恢复提示）；
//   - interval：节流检查周期，如 2*time.Second。
func NewSwapManager(src SnapSource, swapPath, filePath string, interval time.Duration) *SwapManager {
	return &SwapManager{
		src:      src,
		swapPath: swapPath,
		filePath: filePath,
		interval: interval,
		snapCh:   make(chan swapJob, 4), // 有缓冲：写盘期间仍可再接 4 个快照，不阻塞 UI
	}
}

// SetRequest 注入“触发 UI 线程采集快照”的函数。
// 应用中传 func(){ program.Send(swapTickMsg{}) }，使采集回到 UI 线程完成。
// 若为 nil（如单线程测试），run 的 tick 会直接在本 goroutine 调用 Produce。
func (sm *SwapManager) SetRequest(fn func()) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.request = fn
}

// MarkDirty 由 Buffer 的内容变更钩子调用：仅置位，不触触发写盘。
// 这是唯一从 UI 线程频繁调用的入口，开销为一个原子写。
func (sm *SwapManager) MarkDirty() {
	sm.dirty.Store(true)
}

// Start 启动后台写盘。会先同步写入首份 .swp（把“启动即创建交换文件”做掉，
// 确保从最早编纂起就有崩溃恢复点），随后启动 goroutine。
func (sm *SwapManager) Start() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.started {
		return nil
	}
	// 首份快照：编辑尚未开始、缓冲区稳定，可在当前 goroutine 安全采集。
	lines := sm.src.SwapSnapshot()
	meta := newSwapMeta(sm.filePath, len(lines))
	if err := WriteSwapFile(sm.swapPath, lines, meta); err != nil {
		return err
	}
	sm.ctx, sm.cancel = context.WithCancel(context.Background())
	sm.started = true
	sm.wg.Add(1)
	go sm.run()
	return nil
}

// Produce 采集当前脏状态并交给后台写盘（必须在编辑线程/UI线程调用）。
// 非阻塞：后台繁忙时丢弃本轮（dirty 会重新置回，待下轮再采集），绝不阻塞 UI。
func (sm *SwapManager) Produce() {
	if !sm.dirty.Swap(false) {
		return // 无改动，跳过
	}
	// 深拷贝在 UI 线程完成：快照与缓冲区完全独立，才可无锁交给后台。
	lines := sm.src.SwapSnapshot()
	meta := newSwapMeta(sm.filePath, len(lines))
	select {
	case sm.snapCh <- swapJob{meta: meta, lines: lines}:
		// 成功入队
	default:
		// 通道已满（后台仍在写上一份）：放弃本轮，保留脏位待下轮。
		sm.dirty.Store(true)
	}
}

// run 是后台写盘 goroutine：ticker 节流 + 消费快照。
func (sm *SwapManager) run() {
	defer sm.wg.Done()
	ticker := time.NewTicker(sm.interval)
	defer ticker.Stop()

	for {
		select {
		case <-sm.ctx.Done():
			// 优雅退出：不再接受新快照（Stop 会负责最终清理）。
			return
		case <-ticker.C:
			if sm.dirty.Load() {
				// 有改动待写：触发一次采集。
				// 应用场景 request 会把消息投递到 UI 线程，由那里的 Produce 完成
				// 采集；单线程测试时 request==nil，直接就地采集（同样安全）。
				if sm.request != nil {
					sm.request()
				} else {
					sm.Produce()
				}
			}
		case job := <-sm.snapCh:
			_ = WriteSwapFile(sm.swapPath, job.lines, job.meta)
		}
	}
}

// Stop 优雅停止：取消 context -> 等待后台 goroutine 收尾 -> 删除 .swp。
// 适用于正常退出（Ctrl+C / :q）。删除 .swp 表示本次是干净退出，无需恢复。
func (sm *SwapManager) Stop() {
	sm.mu.Lock()
	if !sm.started {
		sm.mu.Unlock()
		return
	}
	sm.cancel()
	sm.started = false
	sm.mu.Unlock()

	sm.wg.Wait() // 等后台把已入队的快照写完（若有）
	_ = RemoveSwapFile(sm.swapPath)
}

// swapPathFor 由正文文件路径推导 .swp 路径（同目录 + ".swp" 后缀，仿 vim）。
func swapPathFor(filePath string) string {
	return filePath + ".swp"
}
