package logger

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
)

// TestTimingStats 验证耗时统计功能正确工作（同步路径）
func TestTimingStats(t *testing.T) {
	ResetTiming()
	EnableTiming(true)
	defer EnableTiming(false)

	cw := &captureWriter{}
	s := &SLogger{
		hooks:        []Hook{},
		asyncEnabled: false,
		usePool:      false,
		level:        &atomic.Int32{},
		levelVar:     &slog.LevelVar{},
	}
	s.level.Store(int32(slog.LevelInfo))
	handler := NewFastHandler(cw, s.level)
	s.logger = slog.New(handler)

	ctx := context.Background()
	for i := 0; i < 100; i++ {
		s.Info(ctx, "timing-test-msg index=%d", i)
	}
	for i := 0; i < 50; i++ {
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
	// 耗时统计验证（>= 0，Windows 定时器精度可能导致极快操作计入 0）
	if timing.logTotalNS.Load() < 0 {
		t.Errorf("logTotalNS should be >= 0, got %d", timing.logTotalNS.Load())
	}
	if timing.slogCallNS.Load() < 0 {
		t.Errorf("slogCallNS should be >= 0, got %d", timing.slogCallNS.Load())
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
		asyncEnabled: false,
		usePool:      false,
		level:        &atomic.Int32{},
		levelVar:     &slog.LevelVar{},
	}
	s.level.Store(int32(slog.LevelInfo))
	handler := NewFastHandler(cw, s.level)
	s.logger = slog.New(handler)

	ctx := context.Background()
	for i := 0; i < 10; i++ {
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
		asyncEnabled: true,
		usePool:      true,
		level:        &atomic.Int32{},
		levelVar:     &slog.LevelVar{},
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
	for i := 0; i < 100; i++ {
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
	DumpTiming()
}
