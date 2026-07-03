package logger

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// TestPanicRobustness 全面测试日志库在各种极端情况下的panic鲁棒性
// 确保除了物理机宕机外，任何情况都不会导致外部调用panic
func TestPanicRobustness(t *testing.T) {
	tests := []struct {
		name string
		fn   func() error
	}{
		// --- nil/零值 SLogger ---
		{
			name: "NilSLogger",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("NilSLogger panic: %v", r)
					}
				}()
				var l *SLogger
				l.log(context.Background(), 0, "test")
				return nil
			},
		},
		{
			name: "NilSLoggerAllMethods",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("NilSLoggerAllMethods panic: %v", r)
					}
				}()
				var l *SLogger
				l.Debug("debug")
				l.Info("info")
				l.Warn("warn")
				l.Error("error")
				l.With("k", "v")
				l.WithContext(context.Background())
				l.AddHook(nil)
				l.SetLevel("info")
				l.GetLevel()
				l.Sync()
				return nil
			},
		},
		{
			name: "UninitializedSLogger",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("UninitializedSLogger panic: %v", r)
					}
				}()
				l := &SLogger{}
				l.Error("test error", "key", "value")
				l.Sync()
				return nil
			},
		},

		// --- Error路径核心测试 ---
		{
			name: "ErrorWithStacktrace",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("ErrorWithStacktrace panic: %v", r)
					}
				}()
				logger, err := New("configs/config.yaml")
				if err != nil {
					return nil
				}
				defer logger.Sync()
				logger.Error("test error with stacktrace", "key", "value")
				return nil
			},
		},
		{
			name: "ErrorWithManyArgs",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("ErrorWithManyArgs panic: %v", r)
					}
				}()
				logger, err := New("configs/config.yaml")
				if err != nil {
					return nil
				}
				defer logger.Sync()
				args := []any{"k1", "v1", "k2", "v2", "k3", "v3", "k4", "v4", "k5", "v5", "k6", "v6"}
				logger.Error("test error with many args", args...)
				return nil
			},
		},
		{
			name: "ErrorWithZeroArgs",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("ErrorWithZeroArgs panic: %v", r)
					}
				}()
				logger, err := New("configs/config.yaml")
				if err != nil {
					return nil
				}
				defer logger.Sync()
				logger.Error("test error with zero args")
				return nil
			},
		},
		{
			name: "ErrorWithNilContext",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("ErrorWithNilContext panic: %v", r)
					}
				}()
				logger, err := New("configs/config.yaml")
				if err != nil {
					return nil
				}
				defer logger.Sync()
				logger.ErrorContext(nil, "test error with nil context", "key", "value")
				return nil
			},
		},

		// --- 奇数参数（slog key-value对齐问题）---
		{
			name: "ErrorWithOddArgs_1",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("ErrorWithOddArgs_1 panic: %v", r)
					}
				}()
				logger, err := New("configs/config.yaml")
				if err != nil {
					return nil
				}
				defer logger.Sync()
				logger.Error("test", "k1")
				return nil
			},
		},
		{
			name: "ErrorWithOddArgs_3",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("ErrorWithOddArgs_3 panic: %v", r)
					}
				}()
				logger, err := New("configs/config.yaml")
				if err != nil {
					return nil
				}
				defer logger.Sync()
				logger.Error("test", "k1", "v1", "k2")
				return nil
			},
		},
		{
			name: "ErrorWithOddArgs_5",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("ErrorWithOddArgs_5 panic: %v", r)
					}
				}()
				logger, err := New("configs/config.yaml")
				if err != nil {
					return nil
				}
				defer logger.Sync()
				logger.Error("test", "k1", "v1", "k2", "v2", "k3")
				return nil
			},
		},
		{
			name: "AllLevelsWithOddArgs",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("AllLevelsWithOddArgs panic: %v", r)
					}
				}()
				logger, err := New("configs/config.yaml")
				if err != nil {
					return nil
				}
				defer logger.Sync()
				logger.Debug("debug", "k1")
				logger.Info("info", "k1", "v1", "k2")
				logger.Warn("warn", "k1", "v1", "k2", "v2", "k3")
				logger.Error("error", "k1")
				return nil
			},
		},

		// --- atomic level 安全性 ---
		{
			name: "SetLevelWithInvalidValue",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("SetLevelWithInvalidValue panic: %v", r)
					}
				}()
				logger, err := New("configs/config.yaml")
				if err != nil {
					return nil
				}
				defer logger.Sync()
				logger.SetLevel("error")
				logger.Error("test after set level", "key", "value")
				return nil
			},
		},
		{
			name: "SetLevelWithEmptyString",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("SetLevelWithEmptyString panic: %v", r)
					}
				}()
				logger, err := New("configs/config.yaml")
				if err != nil {
					return nil
				}
				defer logger.Sync()
				logger.SetLevel("")
				logger.Info("test after empty level", "key", "value")
				return nil
			},
		},
		{
			name: "SetLevelWithRandomString",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("SetLevelWithRandomString panic: %v", r)
					}
				}()
				logger, err := New("configs/config.yaml")
				if err != nil {
					return nil
				}
				defer logger.Sync()
				logger.SetLevel("notalevel")
				logger.Info("test after random level", "key", "value")
				return nil
			},
		},

		// --- 并发安全 ---
		{
			name: "ConcurrentErrorCalls",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("ConcurrentErrorCalls panic: %v", r)
					}
				}()
				logger, err := New("configs/config.yaml")
				if err != nil {
					return nil
				}
				defer logger.Sync()
				var wg sync.WaitGroup
				for i := 0; i < 100; i++ {
					wg.Add(1)
					go func(id int) {
						defer wg.Done()
						logger.Error(fmt.Sprintf("concurrent error %d", id), "goroutine", id)
					}(i)
				}
				wg.Wait()
				return nil
			},
		},
		{
			name: "ConcurrentMixedLevelCalls",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("ConcurrentMixedLevelCalls panic: %v", r)
					}
				}()
				logger, err := New("configs/config.yaml")
				if err != nil {
					return nil
				}
				defer logger.Sync()
				var wg sync.WaitGroup
				for i := 0; i < 200; i++ {
					wg.Add(1)
					go func(id int) {
						defer wg.Done()
						switch id % 4 {
						case 0:
							logger.Debug("debug", "id", id)
						case 1:
							logger.Info("info", "id", id)
						case 2:
							logger.Warn("warn", "id", id)
						case 3:
							logger.Error("error", "id", id)
						}
					}(i)
				}
				wg.Wait()
				return nil
			},
		},
		{
			name: "ConcurrentSetLevelAndLog",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("ConcurrentSetLevelAndLog panic: %v", r)
					}
				}()
				logger, err := New("configs/config.yaml")
				if err != nil {
					return nil
				}
				defer logger.Sync()
				var wg sync.WaitGroup
				for i := 0; i < 50; i++ {
					wg.Add(2)
					go func(id int) {
						defer wg.Done()
						logger.Error("error", "id", id)
					}(i)
					go func(id int) {
						defer wg.Done()
						levels := []string{"debug", "info", "warn", "error"}
						logger.SetLevel(levels[id%4])
					}(i)
				}
				wg.Wait()
				return nil
			},
		},

		// --- Hook panic保护 ---
		{
			name: "ErrorWithPanicHook",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("ErrorWithPanicHook panic: %v", r)
					}
				}()
				logger, err := New("configs/config.yaml")
				if err != nil {
					return nil
				}
				defer logger.Sync()
				panicHook := &TestHookImpl{
					OnRun: func(msg string, level string, args ...any) {
						panic("intentional hook panic")
					},
				}
				loggerWithHook := logger.AddHook(panicHook)
				loggerWithHook.Error("test error with panic hook", "key", "value")
				return nil
			},
		},
		{
			name: "MultipleHooksWithPanic",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("MultipleHooksWithPanic panic: %v", r)
					}
				}()
				logger, err := New("configs/config.yaml")
				if err != nil {
					return nil
				}
				defer logger.Sync()
				panicHook1 := &TestHookImpl{
					OnRun: func(msg string, level string, args ...any) {
						panic("hook1 panic")
					},
				}
				normalHook := &TestHookImpl{
					OnRun: func(msg string, level string, args ...any) {
						// 正常hook
					},
				}
				panicHook2 := &TestHookImpl{
					OnRun: func(msg string, level string, args ...any) {
						panic("hook2 panic")
					},
				}
				l := logger.AddHook(panicHook1).AddHook(normalHook).AddHook(panicHook2)
				l.Error("test with multiple hooks", "key", "value")
				return nil
			},
		},
		{
			name: "AddNilHook",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("AddNilHook panic: %v", r)
					}
				}()
				logger, err := New("configs/config.yaml")
				if err != nil {
					return nil
				}
				defer logger.Sync()
				l := logger.AddHook(nil)
				l.Error("test with nil hook", "key", "value")
				return nil
			},
		},

		// --- With/WithContext 安全性 ---
		{
			name: "WithOddArgs",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("WithOddArgs panic: %v", r)
					}
				}()
				logger, err := New("configs/config.yaml")
				if err != nil {
					return nil
				}
				defer logger.Sync()
				l := logger.With("k1")
				l.Error("test with odd args", "key", "value")
				return nil
			},
		},
		{
			name: "WithContextNil",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("WithContextNil panic: %v", r)
					}
				}()
				logger, err := New("configs/config.yaml")
				if err != nil {
					return nil
				}
				defer logger.Sync()
				l := logger.WithContext(nil)
				l.Error("test with nil context", "key", "value")
				return nil
			},
		},

		// --- Sync安全性 ---
		{
			name: "SyncMultipleTimes",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("SyncMultipleTimes panic: %v", r)
					}
				}()
				logger, err := New("configs/config.yaml")
				if err != nil {
					return nil
				}
				logger.Sync()
				logger.Sync()
				logger.Sync()
				return nil
			},
		},

		// --- FastHandler panic保护 ---
		{
			name: "FastHandlerWithNilWriter",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("FastHandlerWithNilWriter panic: %v", r)
					}
				}()
				h := NewFastHandler(nil, nil)
				r := slog.NewRecord(time.Now(), slog.LevelInfo, "test", 0)
				err := h.Handle(context.Background(), r)
				_ = err
				return nil
			},
		},

		// --- GetLevel安全性 ---
		{
			name: "GetLevelOnUninitialized",
			fn: func() error {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("GetLevelOnUninitialized panic: %v", r)
					}
				}()
				l := &SLogger{}
				level := l.GetLevel()
				if level != "info" {
					t.Errorf("Expected 'info', got '%s'", level)
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.fn(); err != nil {
				t.Errorf("%s failed: %v", tt.name, err)
			}
		})
	}
}

// --- 测试辅助 ---

// BenchmarkErrorPanicStress Error路径panic压力测试
// 确保在高并发Error调用下绝对不会panic
func BenchmarkErrorPanicStress(b *testing.B) {
	logger, err := New("configs/config.yaml")
	if err != nil {
		b.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	// 预热
	for i := 0; i < 1000; i++ {
		logger.Error("warmup", "i", i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		defer func() {
			if r := recover(); r != nil {
				b.Fatalf("BenchmarkErrorPanicStress panic: %v", r)
			}
		}()

		i := 0
		for pb.Next() {
			// 测试各种参数数量的Error调用
			switch i % 6 {
			case 0:
				logger.Error("error with no args")
			case 1:
				logger.Error("error with 1 arg", "k1")
			case 2:
				logger.Error("error with 2 args", "k1", "v1")
			case 3:
				logger.Error("error with 3 args", "k1", "v1", "k2")
			case 4:
				logger.Error("error with 4 args", "k1", "v1", "k2", "v2")
			case 5:
				logger.Error("error with many args", "k1", "v1", "k2", "v2", "k3", "v3", "k4", "v4")
			}
			i++
		}
	})
	b.StopTimer()
	logger.Sync()
}

// BenchmarkErrorPanicStressWithContext Error路径带context的panic压力测试
func BenchmarkErrorPanicStressWithContext(b *testing.B) {
	logger, err := New("configs/config.yaml")
	if err != nil {
		b.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	ctx := context.Background()
	ctx = context.WithValue(ctx, "request_id", "test-req-id")

	for i := 0; i < 1000; i++ {
		logger.ErrorContext(ctx, "warmup", "i", i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		defer func() {
			if r := recover(); r != nil {
				b.Fatalf("BenchmarkErrorPanicStressWithContext panic: %v", r)
			}
		}()

		for pb.Next() {
			logger.ErrorContext(ctx, "error with context", "key", "value")
		}
	})
	b.StopTimer()
	logger.Sync()
}

// BenchmarkConcurrentMixedStress 混合级别并发压力测试
func BenchmarkConcurrentMixedStress(b *testing.B) {
	logger, err := New("configs/config.yaml")
	if err != nil {
		b.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		defer func() {
			if r := recover(); r != nil {
				b.Fatalf("BenchmarkConcurrentMixedStress panic: %v", r)
			}
		}()

		i := 0
		for pb.Next() {
			switch i % 8 {
			case 0:
				logger.Debug("debug", "k1")
			case 1:
				logger.Info("info")
			case 2:
				logger.Warn("warn", "k1", "v1", "k2")
			case 3:
				logger.Error("error", "k1", "v1")
			case 4:
				logger.Info("info ctx", "k1", "v1", "k2", "v2", "k3")
			case 5:
				logger.Error("error odd", "k1", "v1", "k2")
			case 6:
				logger.Warn("warn many", "k1", "v1", "k2", "v2", "k3", "v3", "k4", "v4")
			case 7:
				logger.Debug("debug none")
			}
			i++
		}
	})
	b.StopTimer()
	logger.Sync()
}
