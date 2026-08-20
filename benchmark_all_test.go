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

	// 预热
	for i := 0; i < 1000; i++ {
		s.Info("warmup", "i", i)
	}
	time.Sleep(50 * time.Millisecond)
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			switch i % 4 {
			case 0:
				s.Debug("debug-msg", "key", "val", "n", i)
			case 1:
				s.Info("info-msg", "key", "val", "n", i)
			case 2:
				s.Warn("warn-msg", "key", "val", "n", i)
			case 3:
				s.Error("error-msg", "key", "val", "n", i)
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

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			switch i % 4 {
			case 0:
				s.Debug("debug-msg", "key", "val")
			case 1:
				s.Info("info-msg", "key", "val")
			case 2:
				s.Warn("warn-msg", "key", "val")
			case 3:
				s.Error("error-msg", "key", "val")
			}
			i++
		}
	})
}

// --- 分级别异步压测（io.Discard） ---

func BenchmarkAsyncDebug(b *testing.B) {
	s := makeAsyncLogger(b, slog.LevelDebug, 8, 1000000)
	defer s.Sync()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Debug("debug", "key", "val")
		}
	})
	b.StopTimer()
	s.Sync()
}

func BenchmarkAsyncInfo(b *testing.B) {
	s := makeAsyncLogger(b, slog.LevelDebug, 8, 1000000)
	defer s.Sync()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Info("info", "key", "val")
		}
	})
	b.StopTimer()
	s.Sync()
}

func BenchmarkAsyncWarn(b *testing.B) {
	s := makeAsyncLogger(b, slog.LevelDebug, 8, 1000000)
	defer s.Sync()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Warn("warn", "key", "val")
		}
	})
	b.StopTimer()
	s.Sync()
}

func BenchmarkAsyncError(b *testing.B) {
	s := makeAsyncLogger(b, slog.LevelDebug, 8, 1000000)
	defer s.Sync()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Error("error", "key", "val")
		}
	})
	b.StopTimer()
	s.Sync()
}

// --- 分级别同步压测（io.Discard） ---

func BenchmarkSyncDebug(b *testing.B) {
	s := makeSyncLogger(b, slog.LevelDebug)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Debug("debug", "key", "val")
		}
	})
}

func BenchmarkSyncInfo(b *testing.B) {
	s := makeSyncLogger(b, slog.LevelDebug)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Info("info", "key", "val")
		}
	})
}

func BenchmarkSyncWarn(b *testing.B) {
	s := makeSyncLogger(b, slog.LevelDebug)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Warn("warn", "key", "val")
		}
	})
}

func BenchmarkSyncError(b *testing.B) {
	s := makeSyncLogger(b, slog.LevelDebug)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Error("error", "key", "val")
		}
	})
}

// --- 极限并发压测（128/256 goroutine） ---

func BenchmarkExtremeConcurrency(b *testing.B) {
	for _, conc := range []int{8, 16, 32, 64, 128, 256} {
		b.Run(fmt.Sprintf("C%d", conc), func(b *testing.B) {
			s := makeAsyncLogger(b, slog.LevelDebug, 8, 2000000)
			defer s.Sync()
			for i := 0; i < 1000; i++ {
				s.Info("warmup", "i", i)
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
						s.Info("extreme", "g", id, "j", j)
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
			for i := 0; i < 1000; i++ {
				s.Info("warmup", "i", i)
			}
			time.Sleep(50 * time.Millisecond)
			b.ResetTimer()

			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					s.Info("scale", "key", "val")
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
				r := slog.NewRecord(time.Now(), slog.LevelDebug, "pure-serial", 0)
				r.Add("key", "val")
				_ = handler.Handle(ctx, r)
			}
		})
	})

	b.Run("Info", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				r := slog.NewRecord(time.Now(), slog.LevelInfo, "pure-serial", 0)
				r.Add("key", "val")
				_ = handler.Handle(ctx, r)
			}
		})
	})

	b.Run("Error", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				r := slog.NewRecord(time.Now(), slog.LevelError, "pure-serial", 0)
				r.Add("key", "val")
				_ = handler.Handle(ctx, r)
			}
		})
	})
}

// --- 10M 吞吐挑战：高并发所有接口 + Context 版本 ---

// BenchmarkAllInterfaces10M 覆盖所有公开接口（Debug/Info/Warn/Error + Context 版本）
// 目标：验证所有接口在 8 核心机器上吞吐量达到千万级别
func BenchmarkAllInterfaces10M(b *testing.B) {
	s := makeAsyncLogger(b, slog.LevelDebug, 8, 2000000)
	defer s.Sync()

	// 预热
	for i := 0; i < 1000; i++ {
		s.Info("warmup", "i", i)
	}
	time.Sleep(50 * time.Millisecond)
	b.ResetTimer()

	ctx := context.Background()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			// 8 个接口轮流调用（Debug/Info/Warn/Error + 对应 Context 版本）
			switch i % 8 {
			case 0:
				s.Debug("d", "k", "v", "n", i)
			case 1:
				s.DebugContext(ctx, "dc", "k", "v", "n", i)
			case 2:
				s.Info("i", "k", "v", "n", i)
			case 3:
				s.InfoContext(ctx, "ic", "k", "v", "n", i)
			case 4:
				s.Warn("w", "k", "v", "n", i)
			case 5:
				s.WarnContext(ctx, "wc", "k", "v", "n", i)
			case 6:
				s.Error("e", "k", "v", "n", i)
			case 7:
				s.ErrorContext(ctx, "ec", "k", "v", "n", i)
			}
			i++
		}
	})
	b.StopTimer()
	s.Sync()

	// 输出丢弃统计，便于排查缓冲区是否成为瓶颈
	if d := s.ringBuf.Dropped(); d > 0 {
		b.Logf("[10M challenge] dropped entries: %d", d)
	}
}

// BenchmarkThroughputMax 极限吞吐：仅 Info，最少参数，纯管线
func BenchmarkThroughputMax(b *testing.B) {
	s := makeAsyncLogger(b, slog.LevelDebug, 8, 2000000)
	defer s.Sync()
	for i := 0; i < 1000; i++ {
		s.Info("warmup", "i", i)
	}
	time.Sleep(50 * time.Millisecond)
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Info("m")
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
			for i := 0; i < 1000; i++ {
				s.Info("warmup", "i", i)
			}
			time.Sleep(50 * time.Millisecond)
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					s.Info("bs", "k", "v")
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
			for i := 0; i < 1000; i++ {
				s.Info("warmup", "i", i)
			}
			time.Sleep(50 * time.Millisecond)
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					s.Info("s", "k", "v")
				}
			})
			b.StopTimer()
			s.Sync()
		})
	}
}
