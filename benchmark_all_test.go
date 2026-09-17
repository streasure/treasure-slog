package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/streasure/treasure-slog/internal/config"
)

// makeAsyncLogger 构造一个异步模式 logger 写入 io.Discard（纯管线性能测试，无 I/O 开销）
func makeAsyncLogger(b *testing.B, level slog.Level, workers, bufSize int) *SLogger {
	b.Helper()
	s := &SLogger{
		hooks:        []Hook{},
		wg:           &sync.WaitGroup{},
		asyncEnabled: true,
		usePool:      true,
		level:        &atomic.Int32{},
		levelVar:     &slog.LevelVar{},
	}
	s.level.Store(int32(level))
	handler := NewFastHandler(io.Discard, s.level)
	s.logger = slog.New(handler)
	s.handler = handler
	s.ringBuf = newShardedRingBuffer(bufSize, workers)
	s.workers = make([]*worker, workers)
	for i := range s.workers {
		s.workers[i] = &worker{
			id:     i,
			logger: s,
			stopCh: make(chan struct{}),
			wg:     s.wg,
		}
		s.workers[i].start()
	}
	return s
}

// makeSyncLogger 构造一个同步模式 logger 写入 io.Discard
func makeSyncLogger(b *testing.B, level slog.Level) *SLogger {
	b.Helper()
	s := &SLogger{
		hooks:        []Hook{},
		wg:           &sync.WaitGroup{},
		asyncEnabled: false,
		usePool:      false,
		level:        &atomic.Int32{},
		levelVar:     &slog.LevelVar{},
	}
	s.level.Store(int32(level))
	handler := NewFastHandler(io.Discard, s.level)
	s.logger = slog.New(handler)
	s.handler = handler
	return s
}

// makeAsyncLoggerWithBatch 构造指定 batch size 的异步 logger
func makeAsyncLoggerWithBatch(b *testing.B, level slog.Level, workers, bufSize, batchSize int) *SLogger {
	b.Helper()
	s := &SLogger{
		hooks:        []Hook{},
		wg:           &sync.WaitGroup{},
		asyncEnabled: true,
		usePool:      true,
		level:        &atomic.Int32{},
		levelVar:     &slog.LevelVar{},
		cfg: &config.Config{
			Log: config.LogConfig{
				Async: config.AsyncConfig{
					BatchSize:     batchSize,
					FlushInterval: 10,
				},
			},
		},
	}
	s.level.Store(int32(level))
	handler := NewFastHandler(io.Discard, s.level)
	s.logger = slog.New(handler)
	s.handler = handler
	s.ringBuf = newShardedRingBuffer(bufSize, workers)
	s.workers = make([]*worker, workers)
	for i := range s.workers {
		s.workers[i] = &worker{
			id:     i,
			logger: s,
			stopCh: make(chan struct{}),
			wg:     s.wg,
		}
		s.workers[i].start()
	}
	return s
}

// --- 全级别异步压测（io.Discard，纯管线性能） ---

func BenchmarkAllLevelsAsync(b *testing.B) {
	s := makeAsyncLogger(b, slog.LevelDebug, 8, 1000000)
	defer s.Sync()

	ctx := context.Background()

	// 预热
	for i := 0; i < 1000; i++ {
		s.Info(ctx, "warmup i=%d", i)
	}
	time.Sleep(50 * time.Millisecond)
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			switch i % 4 {
			case 0:
				s.Debug(ctx, "debug-msg key=%s val=%s n=%d", "key", "val", i)
			case 1:
				s.Info(ctx, "info-msg key=%s val=%s n=%d", "key", "val", i)
			case 2:
				s.Warn(ctx, "warn-msg key=%s val=%s n=%d", "key", "val", i)
			case 3:
				s.Error(ctx, "error-msg key=%s val=%s n=%d", "key", "val", i)
			}
			i++
		}
	})
	b.StopTimer()
	s.Sync()
}

// --- 全级别同步压测 ---

func BenchmarkAllLevelsSync(b *testing.B) {
	s := makeSyncLogger(b, slog.LevelDebug)
	ctx := context.Background()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			switch i % 4 {
			case 0:
				s.Debug(ctx, "debug-msg key=%s val=%s", "key", "val")
			case 1:
				s.Info(ctx, "info-msg key=%s val=%s", "key", "val")
			case 2:
				s.Warn(ctx, "warn-msg key=%s val=%s", "key", "val")
			case 3:
				s.Error(ctx, "error-msg key=%s val=%s", "key", "val")
			}
			i++
		}
	})
}

// --- 分级别异步压测（io.Discard） ---

func BenchmarkAsyncDebug(b *testing.B) {
	s := makeAsyncLogger(b, slog.LevelDebug, 8, 1000000)
	defer s.Sync()
	ctx := context.Background()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Debug(ctx, "debug key=%s val=%s", "key", "val")
		}
	})
	b.StopTimer()
	s.Sync()
}

func BenchmarkAsyncInfo(b *testing.B) {
	s := makeAsyncLogger(b, slog.LevelDebug, 8, 1000000)
	defer s.Sync()
	ctx := context.Background()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Info(ctx, "info key=%s val=%s", "key", "val")
		}
	})
	b.StopTimer()
	s.Sync()
}

func BenchmarkAsyncWarn(b *testing.B) {
	s := makeAsyncLogger(b, slog.LevelDebug, 8, 1000000)
	defer s.Sync()
	ctx := context.Background()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Warn(ctx, "warn key=%s val=%s", "key", "val")
		}
	})
	b.StopTimer()
	s.Sync()
}

func BenchmarkAsyncError(b *testing.B) {
	s := makeAsyncLogger(b, slog.LevelDebug, 8, 1000000)
	defer s.Sync()
	ctx := context.Background()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Error(ctx, "error key=%s val=%s", "key", "val")
		}
	})
	b.StopTimer()
	s.Sync()
}

// --- 分级别同步压测（io.Discard） ---

func BenchmarkSyncDebug(b *testing.B) {
	s := makeSyncLogger(b, slog.LevelDebug)
	ctx := context.Background()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Debug(ctx, "debug key=%s val=%s", "key", "val")
		}
	})
}

func BenchmarkSyncInfo(b *testing.B) {
	s := makeSyncLogger(b, slog.LevelDebug)
	ctx := context.Background()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Info(ctx, "info key=%s val=%s", "key", "val")
		}
	})
}

func BenchmarkSyncWarn(b *testing.B) {
	s := makeSyncLogger(b, slog.LevelDebug)
	ctx := context.Background()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Warn(ctx, "warn key=%s val=%s", "key", "val")
		}
	})
}

func BenchmarkSyncError(b *testing.B) {
	s := makeSyncLogger(b, slog.LevelDebug)
	ctx := context.Background()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Error(ctx, "error key=%s val=%s", "key", "val")
		}
	})
}

// --- 极限并发压测（128/256 goroutine） ---

func BenchmarkExtremeConcurrency(b *testing.B) {
	for _, conc := range []int{8, 16, 32, 64, 128, 256} {
		b.Run(fmt.Sprintf("C%d", conc), func(b *testing.B) {
			s := makeAsyncLogger(b, slog.LevelDebug, 8, 2000000)
			defer s.Sync()
			ctx := context.Background()
			for i := 0; i < 1000; i++ {
				s.Info(ctx, "warmup i=%d", i)
			}
			time.Sleep(50 * time.Millisecond)
			b.ResetTimer()

			var wg sync.WaitGroup
			perG := b.N / conc
			for i := 0; i < conc; i++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					for j := 0; j < perG; j++ {
						s.Info(ctx, "extreme g=%d j=%d", id, j)
					}
				}(i)
			}
			wg.Wait()
			b.StopTimer()
			s.Sync()
		})
	}
}

// --- 多 worker 扩展性压测 ---

func BenchmarkWorkerScalability(b *testing.B) {
	for _, workers := range []int{1, 2, 4, 8, 16, 32} {
		b.Run(fmt.Sprintf("W%d", workers), func(b *testing.B) {
			s := makeAsyncLogger(b, slog.LevelDebug, workers, 2000000)
			defer s.Sync()
			ctx := context.Background()
			for i := 0; i < 1000; i++ {
				s.Info(ctx, "warmup i=%d", i)
			}
			time.Sleep(50 * time.Millisecond)
			b.ResetTimer()

			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					s.Info(ctx, "scale key=%s val=%s", "key", "val")
				}
			})
			b.StopTimer()
			s.Sync()
		})
	}
}

// --- 纯序列化压测（直接调用 FastHandler.Handle，不经过异步管线） ---

func BenchmarkPureSerialization(b *testing.B) {
	handler := NewFastHandler(io.Discard, nil)
	ctx := context.Background()

	b.Run("Debug", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				r := slog.NewRecord(time.Now(), slog.LevelDebug, "pure-serial key=val", 0)
				_ = handler.Handle(ctx, r)
			}
		})
	})

	b.Run("Info", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				r := slog.NewRecord(time.Now(), slog.LevelInfo, "pure-serial key=val", 0)
				_ = handler.Handle(ctx, r)
			}
		})
	})

	b.Run("Error", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				r := slog.NewRecord(time.Now(), slog.LevelError, "pure-serial key=val", 0)
				_ = handler.Handle(ctx, r)
			}
		})
	})
}

// --- 10M 吞吐挑战：高并发所有接口 ---

func BenchmarkAllInterfaces10M(b *testing.B) {
	s := makeAsyncLogger(b, slog.LevelDebug, 8, 2000000)
	defer s.Sync()

	ctx := context.Background()

	// 预热
	for i := 0; i < 1000; i++ {
		s.Info(ctx, "warmup i=%d", i)
	}
	time.Sleep(50 * time.Millisecond)
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Info(ctx, "i k=%s v=%s", "key", "val")
		}
	})
	b.StopTimer()
	s.Sync()

	if d := s.ringBuf.Dropped(); d > 0 {
		b.Logf("[10M challenge] dropped entries: %d", d)
	}
}

// BenchmarkThroughputMax 极限吞吐：仅 Info，最少参数，纯管线
func BenchmarkThroughputMax(b *testing.B) {
	s := makeAsyncLogger(b, slog.LevelDebug, 8, 2000000)
	defer s.Sync()
	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		s.Info(ctx, "warmup i=%d", i)
	}
	time.Sleep(50 * time.Millisecond)
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Info(ctx, "m")
		}
	})
	b.StopTimer()
	s.Sync()
}

// BenchmarkWorkerBatchSize 不同 batch size 对吞吐的影响
func BenchmarkWorkerBatchSize(b *testing.B) {
	for _, bs := range []int{256, 512, 1024, 2048, 4096} {
		b.Run(fmt.Sprintf("BS%d", bs), func(b *testing.B) {
			s := makeAsyncLoggerWithBatch(b, slog.LevelDebug, 8, 2000000, bs)
			defer s.Sync()
			ctx := context.Background()
			for i := 0; i < 1000; i++ {
				s.Info(ctx, "warmup i=%d", i)
			}
			time.Sleep(50 * time.Millisecond)
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					s.Info(ctx, "bs key=%s val=%s", "k", "v")
				}
			})
			b.StopTimer()
			s.Sync()
		})
	}
}

// BenchmarkShardCount 不同 shard 数量对吞吐的影响（验证锁竞争优化）
func BenchmarkShardCount(b *testing.B) {
	for _, shards := range []int{1, 2, 4, 8, 16, 32} {
		b.Run(fmt.Sprintf("S%d", shards), func(b *testing.B) {
			s := makeAsyncLogger(b, slog.LevelDebug, shards, 2000000)
			defer s.Sync()
			ctx := context.Background()
			for i := 0; i < 1000; i++ {
				s.Info(ctx, "warmup i=%d", i)
			}
			time.Sleep(50 * time.Millisecond)
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					s.Info(ctx, "s key=%s val=%s", "k", "v")
				}
			})
			b.StopTimer()
			s.Sync()
		})
	}
}

// --- Printf 风格接口压测 ---

// BenchmarkPrintfStyleAsync 异步模式 Printf 风格全级别压测
func BenchmarkPrintfStyleAsync(b *testing.B) {
	s := makeAsyncLogger(b, slog.LevelDebug, 8, 1000000)
	defer s.Sync()

	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		s.Info(ctx, "warmup i=%d", i)
	}
	time.Sleep(50 * time.Millisecond)
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			switch i % 4 {
			case 0:
				s.Debug(ctx, "debug msg %d %s", i, "val")
			case 1:
				s.Info(ctx, "info msg %d %s", i, "val")
			case 2:
				s.Warn(ctx, "warn msg %d %s", i, "val")
			case 3:
				s.Error(ctx, "error msg %d %s", i, "val")
			}
			i++
		}
	})
	b.StopTimer()
	s.Sync()
}

// BenchmarkPrintfStyleSync 同步模式 Printf 风格全级别压测
func BenchmarkPrintfStyleSync(b *testing.B) {
	s := makeSyncLogger(b, slog.LevelDebug)
	ctx := context.Background()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			switch i % 4 {
			case 0:
				s.Debug(ctx, "debug msg %d", i)
			case 1:
				s.Info(ctx, "info msg %d", i)
			case 2:
				s.Warn(ctx, "warn msg %d", i)
			case 3:
				s.Error(ctx, "error msg %d", i)
			}
			i++
		}
	})
}

// BenchmarkPrintfLevelFiltered 延迟格式化收益：级别被过滤时避免 Sprintf 开销
func BenchmarkPrintfLevelFiltered(b *testing.B) {
	// 设置 Info 级别，Debug 调用会被过滤
	s := makeAsyncLogger(b, slog.LevelInfo, 8, 1000000)
	defer s.Sync()
	ctx := context.Background()

	b.Run("Debugf_Filtered", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			s.Debug(ctx, "debug msg %d %s", i, "val")
		}
	})

	b.Run("Debug_KeyValue_Filtered", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			s.Debug(ctx, "debug msg key=%d val=%s", i, "val")
		}
	})
}

// BenchmarkPrintfComplexFormat 复杂格式化字符串压测
func BenchmarkPrintfComplexFormat(b *testing.B) {
	s := makeAsyncLogger(b, slog.LevelDebug, 8, 1000000)
	defer s.Sync()

	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		s.Info(ctx, "warmup i=%d", i)
	}
	time.Sleep(50 * time.Millisecond)
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			s.Info(ctx, "request_id=%s user=%s action=%d status=%v latency=%.2fms",
				"abc-123", "user42", i, true, 123.45)
			i++
		}
	})
	b.StopTimer()
	s.Sync()
}

// BenchmarkAllInterfaces10MWithPrintf 10M 吞吐挑战：包含 Printf 风格接口
func BenchmarkAllInterfaces10MWithPrintf(b *testing.B) {
	s := makeAsyncLogger(b, slog.LevelDebug, 8, 2000000)
	defer s.Sync()

	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		s.Info(ctx, "warmup i=%d", i)
	}
	time.Sleep(50 * time.Millisecond)
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			switch i % 4 {
			case 0:
				s.Debug(ctx, "d-%d-v", i)
			case 1:
				s.Info(ctx, "i-%d-v", i)
			case 2:
				s.Warn(ctx, "w-%d-v", i)
			case 3:
				s.Error(ctx, "e-%d-v", i)
			}
			i++
		}
	})
	b.StopTimer()
	s.Sync()

	if d := s.ringBuf.Dropped(); d > 0 {
		b.Logf("[10M+Printf challenge] dropped entries: %d", d)
	}
}

// --- WithContext 性能压测 ---

// BenchmarkWithContext 测试 WithContext 的性能开销
func BenchmarkWithContext(b *testing.B) {
	s := makeAsyncLogger(b, slog.LevelDebug, 8, 2000000)
	defer s.Sync()

	ctx := context.Background()
	ctx = context.WithValue(ctx, "request_id", "test-123")
	ctx = context.WithValue(ctx, "trace_id", "trace-456")

	// 空 context（没有 trace 信息）
	emptyCtx := context.Background()

	b.Run("WithContext_Create_WithData", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = s.WithContext(ctx)
		}
	})

	b.Run("WithContext_Create_EmptyCtx", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = s.WithContext(emptyCtx)
		}
	})

	b.Run("WithContext_ThenLog", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			ctxLog := s.WithContext(ctx)
			ctxLog.Info(ctx, "test message")
		}
	})

	b.Run("DirectLog_WithData", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			s.Info(ctx, "test message")
		}
	})

	b.Run("DirectLog_EmptyCtx", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			s.Info(emptyCtx, "test message")
		}
	})
}

// BenchmarkAsync 异步模式压测
func BenchmarkAsync(b *testing.B) {
	s := makeAsyncLogger(b, slog.LevelDebug, 8, 1000000)
	defer s.Sync()
	ctx := context.Background()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Info(ctx, "async test")
		}
	})
}
