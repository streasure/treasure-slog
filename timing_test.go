package logger

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestTimingStats 验证耗时统计功能正确工作（同步路径）
func TestTimingStats(t *testing.T) {
	ResetTiming()
	EnableTiming(true)
	defer EnableTiming(false)

	cw := &captureWriter{}
	s := &SLogger{
		hooks:        []Hook{},
		asyncEnabled: newAtomicBool(false),
		usePool:      false,
		level:        &atomic.Int32{},
		levelVar:     &slog.LevelVar{},
		syncOnce:     &sync.Once{},
	}
	s.level.Store(int32(slog.LevelInfo))
	handler := NewFastHandler(cw, s.level)
	s.logger = slog.New(handler)

	ctx := context.Background()
	for i := range 100 {
		s.Info(ctx, "timing-test-msg index=%d", i)
	}
	for i := range 50 {
		s.Debug(ctx, "should-be-filtered index=%d", i)
	}

	if timing.logCalls.Load() != 150 {
		t.Errorf("logCalls = %d, want 150", timing.logCalls.Load())
	}
	if timing.logFiltered.Load() != 50 {
		t.Errorf("logFiltered = %d, want 50", timing.logFiltered.Load())
	}
	if timing.logSyncPath.Load() != 100 {
		t.Errorf("logSyncPath = %d, want 100", timing.logSyncPath.Load())
	}
	if timing.entriesHandled.Load() != 100 {
		t.Errorf("entriesHandled = %d, want 100", timing.entriesHandled.Load())
	}
	// 耗时统计验证（>= 0；本机 time.Since 分辨率不足以断言快速阶段 >0，
	// 接线正确性由 TestTimingStageWiring 用慢写入器守护）
	if timing.logTotalNS.Load() < 0 {
		t.Errorf("logTotalNS should be >= 0, got %d", timing.logTotalNS.Load())
	}
	if timing.slogCallNS.Load() < 0 {
		t.Errorf("slogCallNS should be >= 0, got %d", timing.slogCallNS.Load())
	}
	if timing.levelCheckNS.Load() < 0 {
		t.Errorf("levelCheckNS should be >= 0, got %d", timing.levelCheckNS.Load())
	}
	if timing.syncProcessNS.Load() < 0 {
		t.Errorf("syncProcessNS should be >= 0, got %d", timing.syncProcessNS.Load())
	}
	if timing.handlerHandleNS.Load() < 0 {
		t.Errorf("handlerHandleNS should be >= 0, got %d", timing.handlerHandleNS.Load())
	}
	DumpTiming()
}

// TestTimingDisabled 验证关闭时零开销
func TestTimingDisabled(t *testing.T) {
	ResetTiming()
	EnableTiming(false)

	cw := &captureWriter{}
	s := &SLogger{
		hooks:        []Hook{},
		asyncEnabled: newAtomicBool(false),
		usePool:      false,
		level:        &atomic.Int32{},
		levelVar:     &slog.LevelVar{},
		syncOnce:     &sync.Once{},
	}
	s.level.Store(int32(slog.LevelInfo))
	handler := NewFastHandler(cw, s.level)
	s.logger = slog.New(handler)

	ctx := context.Background()
	for i := range 10 {
		s.Info(ctx, "disabled-test i=%d", i)
	}
	if timing.logTotalNS.Load() != 0 {
		t.Errorf("logTotalNS should be 0 when disabled, got %d", timing.logTotalNS.Load())
	}
	EnableTiming(true)
}

// TestTimingAsyncPath 验证异步路径的耗时统计
func TestTimingAsyncPath(t *testing.T) {
	ResetTiming()
	EnableTiming(true)
	defer EnableTiming(false)

	cw := &captureWriter{}
	s := &SLogger{
		hooks:        []Hook{},
		wg:           &sync.WaitGroup{},
		asyncEnabled: newAtomicBool(true),
		usePool:      true,
		level:        &atomic.Int32{},
		levelVar:     &slog.LevelVar{},
		syncOnce:     &sync.Once{},
	}
	s.level.Store(int32(slog.LevelInfo))
	handler := NewFastHandler(cw, s.level)
	s.logger = slog.New(handler)
	s.ringBuf = newRingBuffer(10000)

	s.workers = make([]*worker, 1)
	for i := range s.workers {
		s.workers[i] = &worker{
			id:     i,
			logger: s,
			stopCh: make(chan struct{}),
			wg:     s.wg,
		}
		s.workers[i].start()
	}

	ctx := context.Background()
	for i := range 100 {
		s.Info(ctx, "async-timing-test index=%d", i)
	}
	s.Sync()

	// 验证计数器（核心验证点）
	if timing.logCalls.Load() != 100 {
		t.Errorf("logCalls = %d, want 100", timing.logCalls.Load())
	}
	if timing.logAsyncPush.Load() != 100 {
		t.Errorf("logAsyncPush = %d, want 100", timing.logAsyncPush.Load())
	}
	if timing.batchProcessed.Load() == 0 {
		t.Error("batchProcessed should be > 0")
	}
	if timing.entriesHandled.Load() != 100 {
		t.Errorf("entriesHandled = %d, want 100", timing.entriesHandled.Load())
	}
	if timing.logSyncPath.Load() != 0 {
		t.Errorf("logSyncPath should be 0 in async mode, got %d", timing.logSyncPath.Load())
	}
	if timing.logFiltered.Load() != 0 {
		t.Errorf("logFiltered should be 0, got %d", timing.logFiltered.Load())
	}
	// 耗时统计验证（>= 0，Windows 上极快操作可能为 0）
	if timing.asyncProcessNS.Load() < 0 {
		t.Errorf("asyncProcessNS should be >= 0, got %d", timing.asyncProcessNS.Load())
	}
	if timing.batchProcessNS.Load() < 0 {
		t.Errorf("batchProcessNS should be >= 0, got %d", timing.batchProcessNS.Load())
	}
	if timing.poolGetNS.Load() < 0 {
		t.Errorf("poolGetNS should be >= 0, got %d", timing.poolGetNS.Load())
	}
	if timing.ringBufPushNS.Load() < 0 {
		t.Errorf("ringBufPushNS should be >= 0, got %d", timing.ringBufPushNS.Load())
	}
	DumpTiming()
}

// TestStartTimer_Records 守护计时机制本身：快速阶段（级别检查/池获取/入队）
// 单次跨度低于本机计时器分辨率时计量为 0，无法用 >0 断言；
// 此处通过睡眠保证跨度超过分辨率，验证 startTimer 真实累计。
func TestStartTimer_Records(t *testing.T) {
	EnableTiming(true)
	defer EnableTiming(false)

	var acc atomic.Int64
	end := timing.startTimer(&acc)
	time.Sleep(2 * time.Millisecond)
	end()
	if acc.Load() <= 0 {
		t.Errorf("startTimer should record elapsed time when enabled, got %d", acc.Load())
	}

	EnableTiming(false)
	var acc2 atomic.Int64
	end2 := timing.startTimer(&acc2)
	time.Sleep(2 * time.Millisecond)
	end2()
	if acc2.Load() != 0 {
		t.Errorf("startTimer should be no-op when disabled, got %d", acc2.Load())
	}
	EnableTiming(true)
}

// slowWriter 写入前睡眠，把所在阶段跨度拉到毫秒级，绕开本机计时器分辨率限制
type slowWriter struct{}

func (slowWriter) Write(p []byte) (int, error) {
	time.Sleep(2 * time.Millisecond)
	return len(p), nil
}

// TestTimingStageWiring 守护处理链阶段计时的接线：
// 同步 processEntry 与 FastHandler.Handle 跨度被慢写入器拉到毫秒级，
// 必然超过计时器分辨率；若记录点被移除，对应累加值将为 0。
func TestTimingStageWiring(t *testing.T) {
	ResetTiming()
	EnableTiming(true)
	defer EnableTiming(false)

	s := &SLogger{
		hooks:        []Hook{},
		asyncEnabled: newAtomicBool(false),
		usePool:      false,
		level:        &atomic.Int32{},
		levelVar:     &slog.LevelVar{},
		syncOnce:     &sync.Once{},
	}
	s.level.Store(int32(slog.LevelInfo))
	s.logger = slog.New(NewFastHandler(slowWriter{}, s.level))

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		s.Info(ctx, "wiring-test i=%d", i)
	}

	if timing.syncProcessNS.Load() <= 0 {
		t.Errorf("syncProcessNS not wired into processEntryDirect, got %d", timing.syncProcessNS.Load())
	}
	if timing.handlerHandleNS.Load() <= 0 {
		t.Errorf("handlerHandleNS not wired into FastHandler.Handle, got %d", timing.handlerHandleNS.Load())
	}
}
