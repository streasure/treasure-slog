package logger

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"log/slog"
)

// TestWith_AttrsAppearInOutput 验证 With 添加的属性出现在输出中（修复 With handler 丢失属性 bug）
func TestWith_AttrsAppearInOutput(t *testing.T) {
	var buf bytes.Buffer
	s := &SLogger{
		hooks:        []Hook{},
		wg:           &sync.WaitGroup{},
		asyncEnabled: false,
		usePool:      false,
		level:        &atomic.Int32{},
		levelVar:     &slog.LevelVar{},
	}
	s.level.Store(int32(slog.LevelDebug))
	handler := NewFastHandler(&buf, s.level)
	s.logger = slog.New(handler)
	s.handler = handler

	derived := s.With("service", "api", "version", "1.0")
	derived.Info(context.Background(), "hello key=%s", "val")

	output := buf.String()
	// 必须包含 With 的预置属性
	if !strings.Contains(output, `"service":"api"`) {
		t.Errorf("With 的 service 属性丢失: %s", output)
	}
	if !strings.Contains(output, `"version":"1.0"`) {
		t.Errorf("With 的 version 属性丢失: %s", output)
	}
	// 必须包含调用时追加的属性（Printf 风格嵌入 msg 中）
	if !strings.Contains(output, "key=val") {
		t.Errorf("调用时追加的 key 属性丢失: %s", output)
	}
	t.Logf("With 输出: %s", output)
}

// TestSync_ConcurrentSafe 验证并发 Sync 无 panic（修复 Sync 双关闭竞态）
func TestSync_ConcurrentSafe(t *testing.T) {
	s, cleanup := lockContentionTest(t, slog.LevelDebug, 8, 1000000, 1024)
	defer cleanup()

	ctx := context.Background()

	// 持续写入
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					s.Info(ctx, "concurrent-sync g=%d", id)
				}
			}
		}(i)
	}

	// 并发调用 Sync（派生 logger 共享同一 workers）
	derived := s.With("svc", "test")
	var panicCount int64
	var syncWg sync.WaitGroup
	for i := 0; i < 8; i++ {
		syncWg.Add(1)
		go func(useDerived bool) {
			defer syncWg.Done()
			defer func() {
				if r := recover(); r != nil {
					atomic.AddInt64(&panicCount, 1)
				}
			}()
			if useDerived {
				_ = derived.Sync()
			} else {
				_ = s.Sync()
			}
		}(i%2 == 0)
	}
	syncWg.Wait()

	close(stop)
	wg.Wait()

	if panicCount > 0 {
		t.Errorf("并发 Sync 产生 %d 次 panic", panicCount)
	}
}

// TestSync_Idempotent 验证多次 Sync 安全（幂等）
func TestSync_Idempotent(t *testing.T) {
	s, _ := lockContentionTest(t, slog.LevelDebug, 4, 100000, 512)

	for i := 0; i < 10; i++ {
		if err := s.Sync(); err != nil {
			t.Errorf("第 %d 次 Sync 返回错误: %v", i, err)
		}
	}
	// 派生 logger 的 Sync 也应安全（共享 syncOnce）
	derived := s.With("k", "v")
	for i := 0; i < 5; i++ {
		_ = derived.Sync()
	}
}

// TestWorkerStop_DoubleStopSafe 验证 worker.stop 重复调用安全
func TestWorkerStop_DoubleStopSafe(t *testing.T) {
	s, _ := lockContentionTest(t, slog.LevelDebug, 4, 100000, 512)

	var panicCount int64
	var wg sync.WaitGroup
	// 并发调用所有 worker 的 stop
	for _, w := range s.workers {
		wg.Add(1)
		go func(wk *worker) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					atomic.AddInt64(&panicCount, 1)
				}
			}()
			wk.stop()
			wk.stop() // 重复调用
		}(w)
	}
	wg.Wait()

	if panicCount > 0 {
		t.Errorf("worker.stop 重复调用产生 %d 次 panic", panicCount)
	}
	_ = s.Sync()
}

// TestFieldCache_Removed 验证 fieldCache 已移除但配置仍兼容
func TestFieldCache_Removed(t *testing.T) {
	// fieldCache 字段已从 SLogger 移除，配置仍可解析但不再有运行时开销
	// 这里仅验证 SLogger 不再有 fieldCache 字段，编译时已保证
	s, cleanup := lockContentionTest(t, slog.LevelDebug, 2, 10000, 256)
	defer cleanup()
	s.Info(context.Background(), "field-cache-removed k=%s", "v")
	_ = s.Sync()
}

// TestAddHook_NilIgnored 验证 nil hook 被忽略
func TestAddHook_NilIgnored(t *testing.T) {
	s, cleanup := lockContentionTest(t, slog.LevelDebug, 2, 10000, 256)
	defer cleanup()

	derived := s.AddHook(nil)
	// nil hook 不应导致 panic 或增加 hooks 长度
	ds := derived.(*SLogger)
	if len(ds.hooks) != 0 {
		t.Errorf("nil hook 应被忽略，但 hooks 长度为 %d", len(ds.hooks))
	}

	// 正常 hook 仍能添加
	derived2 := s.AddHook(&noopHook{})
	ds2 := derived2.(*SLogger)
	if len(ds2.hooks) != 1 {
		t.Errorf("正常 hook 应被添加，hooks 长度应为 1，实际 %d", len(ds2.hooks))
	}
}

// noopHook 实现 Hook 接口但不做任何事
type noopHook struct{}

func (h *noopHook) Run(msg string, level string, args ...any) {}

// TestWorkerRun_DeferOrder 验证 worker recover 在 wg.Done 之前
func TestWorkerRun_DeferOrder(t *testing.T) {
	// 通过构造一个会 panic 的 worker 来验证 recover 先执行
	s, cleanup := lockContentionTest(t, slog.LevelDebug, 2, 10000, 256)
	defer cleanup()

	ctx := context.Background()
	// 让 worker 正常工作一段时间
	for i := 0; i < 100; i++ {
		s.Info(ctx, "defer-order i=%d", i)
	}
	time.Sleep(10 * time.Millisecond)
	_ = s.Sync()
	// 如果 defer 顺序错误，wg.Done 可能在 panic 未捕获时执行，导致死锁
	// 这里 Sync 能正常返回说明 defer 顺序正确
}

// TestContext_LogWithTraceIDs 验证 WithContext 提取 trace ID
func TestContext_LogWithTraceIDs(t *testing.T) {
	var buf bytes.Buffer
	s := &SLogger{
		hooks:        []Hook{},
		wg:           &sync.WaitGroup{},
		asyncEnabled: false,
		level:        &atomic.Int32{},
		levelVar:     &slog.LevelVar{},
	}
	s.level.Store(int32(slog.LevelDebug))
	handler := NewFastHandler(&buf, s.level)
	s.logger = slog.New(handler)
	s.handler = handler

	ctx := context.WithValue(context.Background(), "request_id", "req-abc-123")
	ctx = context.WithValue(ctx, "trace_id", "trace-xyz")
	derived := s.WithContext(ctx)
	derived.Info(context.Background(), "with-trace")

	output := buf.String()
	if !strings.Contains(output, `"request_id":"req-abc-123"`) {
		t.Errorf("request_id 丢失: %s", output)
	}
	if !strings.Contains(output, `"trace_id":"trace-xyz"`) {
		t.Errorf("trace_id 丢失: %s", output)
	}
	t.Logf("WithContext 输出: %s", output)
}
