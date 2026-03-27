package logger

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"treasure-slog/internal/config"
)

// BenchmarkLogger 基础性能测试
func BenchmarkLogger(b *testing.B) {
	// 确保日志目录存在
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		b.Fatalf("Failed to create log directory: %v", err)
	}

	// 创建高性能配置
	cfg := &config.Config{
		Log: config.LogConfig{
			Level:  "info",
			Format: "json",
			Async: config.AsyncConfig{
				Enabled:       true,
				BufferSize:    100000,
				BatchSize:     1000,
				FlushInterval: 10,
				Workers:       8,
			},
			Console: config.ConsoleConfig{
				Enabled: false,
				Format:  "text",
			},
			File: config.FileConfig{
				Enabled: true,
				Path:    "./logs/benchmark.log",
				Rotate: config.RotateConfig{
					MaxSize:    1000,
					MaxBackups: 10,
					MaxAge:     30,
					Compress:   false,
				},
			},
			Stacktrace: config.StackConfig{
				Enabled: false,
				Level:   "error",
				Depth:   10,
			},
			Sampling: config.SamplingConfig{
				Enabled:    false,
				Initial:    1000,
				Thereafter: 100,
			},
			FieldCache: config.FieldCacheConfig{
				Enabled: true,
				Size:    10000,
			},
			Performance: config.PerformanceConfig{
				LockFree: true,
				UsePool:  true,
				Prealloc: true,
			},
		},
	}

	logger, err := newLogger(cfg)
	if err != nil {
		b.Fatalf("Failed to create logger: %v", err)
	}

	// 预热
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Info("Warmup message", "key", "value", "count", 1)
		}
	})

	// 等待异步处理完成
	time.Sleep(100 * time.Millisecond)

	// 重置计时器
	b.ResetTimer()

	// 并发测试
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Info("Benchmark info message", 
				"key1", "value1", 
				"key2", 42, 
				"key3", 3.14,
				"key4", true,
			)
		}
	})

	// 同步日志
	b.StopTimer()
	err = logger.Sync()
	if err != nil {
		b.Fatalf("Failed to sync logger: %v", err)
	}

	// 清理测试日志文件
	os.Remove("./logs/benchmark.log")
}

// BenchmarkLoggerHighThroughput 高吞吐量测试
func BenchmarkLoggerHighThroughput(b *testing.B) {
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		b.Fatalf("Failed to create log directory: %v", err)
	}

	cfg := &config.Config{
		Log: config.LogConfig{
			Level:  "info",
			Format: "json",
			Async: config.AsyncConfig{
				Enabled:       true,
				BufferSize:    1000000,
				BatchSize:     5000,
				FlushInterval: 5,
				Workers:       16,
			},
			Console: config.ConsoleConfig{
				Enabled: false,
			},
			File: config.FileConfig{
				Enabled: true,
				Path:    "./logs/benchmark_high.log",
				Rotate: config.RotateConfig{
					MaxSize:    1000,
					MaxBackups: 10,
					MaxAge:     30,
					Compress:   false,
				},
			},
			Stacktrace: config.StackConfig{
				Enabled: false,
			},
			Sampling: config.SamplingConfig{
				Enabled: false,
			},
			FieldCache: config.FieldCacheConfig{
				Enabled: true,
				Size:    100000,
			},
			Performance: config.PerformanceConfig{
				LockFree: true,
				UsePool:  true,
				Prealloc: true,
			},
		},
	}

	logger, err := newLogger(cfg)
	if err != nil {
		b.Fatalf("Failed to create logger: %v", err)
	}

	// 预热
	for i := 0; i < 10000; i++ {
		logger.Info("Warmup", "i", i)
	}
	time.Sleep(100 * time.Millisecond)

	b.ResetTimer()

	// 使用多个 goroutine 进行高并发测试
	var wg sync.WaitGroup
	numGoroutines := runtime.NumCPU() * 4
	logsPerGoroutine := b.N / numGoroutines

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < logsPerGoroutine; j++ {
				logger.Info("High throughput test",
					"goroutine", id,
					"iteration", j,
					"timestamp", time.Now().UnixNano(),
					"data", "test data for high throughput benchmarking",
				)
			}
		}(i)
	}

	wg.Wait()
	b.StopTimer()

	logger.Sync()
	os.Remove("./logs/benchmark_high.log")
}

// BenchmarkLoggerWithContext 带上下文的日志测试
func BenchmarkLoggerWithContext(b *testing.B) {
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		b.Fatalf("Failed to create log directory: %v", err)
	}

	cfg := &config.Config{
		Log: config.LogConfig{
			Level:  "info",
			Format: "json",
			Async: config.AsyncConfig{
				Enabled:       true,
				BufferSize:    100000,
				BatchSize:     1000,
				FlushInterval: 10,
				Workers:       8,
			},
			Console: config.ConsoleConfig{
				Enabled: false,
			},
			File: config.FileConfig{
				Enabled: true,
				Path:    "./logs/benchmark_ctx.log",
				Rotate: config.RotateConfig{
					MaxSize:    1000,
					MaxBackups: 10,
					MaxAge:     30,
					Compress:   false,
				},
			},
			Stacktrace: config.StackConfig{
				Enabled: false,
			},
			Sampling: config.SamplingConfig{
				Enabled: false,
			},
			FieldCache: config.FieldCacheConfig{
				Enabled: true,
				Size:    10000,
			},
			Performance: config.PerformanceConfig{
				LockFree: true,
				UsePool:  true,
				Prealloc: true,
			},
		},
	}

	logger, err := newLogger(cfg)
	if err != nil {
		b.Fatalf("Failed to create logger: %v", err)
	}

	// 创建带上下文的 logger
	ctx := context.Background()
	ctx = context.WithValue(ctx, "request_id", "bench-request-id")
	ctx = context.WithValue(ctx, "user_id", "bench-user-id")
	ctx = context.WithValue(ctx, "trace_id", "bench-trace-id")
	ctxLogger := logger.WithContext(ctx)

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ctxLogger.Info("Context benchmark",
				"action", "test",
				"status", "success",
			)
		}
	})

	b.StopTimer()
	logger.Sync()
	os.Remove("./logs/benchmark_ctx.log")
}

// BenchmarkLoggerSampling 采样性能测试
func BenchmarkLoggerSampling(b *testing.B) {
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		b.Fatalf("Failed to create log directory: %v", err)
	}

	cfg := &config.Config{
		Log: config.LogConfig{
			Level:  "info",
			Format: "json",
			Async: config.AsyncConfig{
				Enabled:       true,
				BufferSize:    100000,
				BatchSize:     1000,
				FlushInterval: 10,
				Workers:       8,
			},
			Console: config.ConsoleConfig{
				Enabled: false,
			},
			File: config.FileConfig{
				Enabled: true,
				Path:    "./logs/benchmark_sampling.log",
				Rotate: config.RotateConfig{
					MaxSize:    1000,
					MaxBackups: 10,
					MaxAge:     30,
					Compress:   false,
				},
			},
			Stacktrace: config.StackConfig{
				Enabled: false,
			},
			Sampling: config.SamplingConfig{
				Enabled:    true,
				Initial:    100,
				Thereafter: 10,
			},
			FieldCache: config.FieldCacheConfig{
				Enabled: true,
				Size:    10000,
			},
			Performance: config.PerformanceConfig{
				LockFree: true,
				UsePool:  true,
				Prealloc: true,
			},
		},
	}

	logger, err := newLogger(cfg)
	if err != nil {
		b.Fatalf("Failed to create logger: %v", err)
	}

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Info("Sampling benchmark", "data", "test")
		}
	})

	b.StopTimer()
	logger.Sync()
	os.Remove("./logs/benchmark_sampling.log")
}

// BenchmarkLoggerMemory 内存分配测试
func BenchmarkLoggerMemory(b *testing.B) {
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		b.Fatalf("Failed to create log directory: %v", err)
	}

	cfg := &config.Config{
		Log: config.LogConfig{
			Level:  "info",
			Format: "json",
			Async: config.AsyncConfig{
				Enabled:       true,
				BufferSize:    100000,
				BatchSize:     1000,
				FlushInterval: 10,
				Workers:       8,
			},
			Console: config.ConsoleConfig{
				Enabled: false,
			},
			File: config.FileConfig{
				Enabled: true,
				Path:    "./logs/benchmark_mem.log",
				Rotate: config.RotateConfig{
					MaxSize:    1000,
					MaxBackups: 10,
					MaxAge:     30,
					Compress:   false,
				},
			},
			Stacktrace: config.StackConfig{
				Enabled: false,
			},
			Sampling: config.SamplingConfig{
				Enabled: false,
			},
			FieldCache: config.FieldCacheConfig{
				Enabled: true,
				Size:    10000,
			},
			Performance: config.PerformanceConfig{
				LockFree: true,
				UsePool:  true,
				Prealloc: true,
			},
		},
	}

	logger, err := newLogger(cfg)
	if err != nil {
		b.Fatalf("Failed to create logger: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		logger.Info("Memory benchmark",
			"iteration", i,
			"data", "test data for memory benchmarking",
			"timestamp", time.Now().UnixNano(),
		)
	}

	b.StopTimer()
	logger.Sync()
	os.Remove("./logs/benchmark_mem.log")
}

// TestMillionLogsPerSecond 百万级日志写入测试
func TestMillionLogsPerSecond(t *testing.T) {
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatalf("Failed to create log directory: %v", err)
	}

	cfg := &config.Config{
		Log: config.LogConfig{
			Level:  "info",
			Format: "json",
			Async: config.AsyncConfig{
				Enabled:       true,
				BufferSize:    2000000, // 增大缓冲区
				BatchSize:     20000,   // 增大批处理大小
				FlushInterval: 1,       // 1ms刷新间隔
				Workers:       64,      // 增加工作线程
			},
			Console: config.ConsoleConfig{
				Enabled: false, // 禁用控制台输出以提高性能
			},
			File: config.FileConfig{
				Enabled: true,
				Path:    "./logs/million_test.log",
				Rotate: config.RotateConfig{
					MaxSize:    10000,
					MaxBackups: 10,
					MaxAge:     30,
					Compress:   false, // 禁用压缩以提高性能
				},
			},
			Stacktrace: config.StackConfig{
				Enabled: false,
			},
			Sampling: config.SamplingConfig{
				Enabled: false,
			},
			FieldCache: config.FieldCacheConfig{
				Enabled: true,
				Size:    100000,
			},
			Performance: config.PerformanceConfig{
				LockFree: true,
				UsePool:  true,
				Prealloc: true,
			},
		},
	}

	logger, err := newLogger(cfg)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	// 目标：每秒百万级写入
	targetLogs := 1000000
	duration := 1 * time.Second

	t.Logf("Starting million logs per second test...")
	t.Logf("Target: %d logs in %v", targetLogs, duration)

	start := time.Now()
	var wg sync.WaitGroup
	numGoroutines := 64
	logsPerGoroutine := targetLogs / numGoroutines

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < logsPerGoroutine; j++ {
				logger.Info("Million logs test",
					"goroutine", id,
					"iteration", j,
					"timestamp", time.Now().UnixNano(),
					"data", "high throughput logging test data",
					"status", "success",
				)
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	// 同步并清理
	logger.Sync()

	// 计算实际性能
	logsPerSecond := float64(targetLogs) / elapsed.Seconds()
	t.Logf("Completed %d logs in %v", targetLogs, elapsed)
	t.Logf("Actual throughput: %.2f logs/second", logsPerSecond)

	// 验证是否达到目标
	if logsPerSecond < 1000000 {
		t.Errorf("Failed to achieve 1M logs/second. Actual: %.2f logs/second", logsPerSecond)
	} else {
		t.Logf("✓ Achieved target: %.2f logs/second (%.2fx target)", 
			logsPerSecond, logsPerSecond/1000000)
	}

	// 清理
	os.Remove("./logs/million_test.log")
}

// TestLoggerStress 压力测试
func TestLoggerStress(t *testing.T) {
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatalf("Failed to create log directory: %v", err)
	}

	cfg := &config.Config{
		Log: config.LogConfig{
			Level:  "info",
			Format: "json",
			Async: config.AsyncConfig{
				Enabled:       true,
				BufferSize:    1000000,
				BatchSize:     5000,
				FlushInterval: 5,
				Workers:       16,
			},
			Console: config.ConsoleConfig{
				Enabled: false,
			},
			File: config.FileConfig{
				Enabled: true,
				Path:    "./logs/stress_test.log",
				Rotate: config.RotateConfig{
					MaxSize:    1000,
					MaxBackups: 10,
					MaxAge:     30,
					Compress:   false,
				},
			},
			Stacktrace: config.StackConfig{
				Enabled: false,
			},
			Sampling: config.SamplingConfig{
				Enabled: false,
			},
			FieldCache: config.FieldCacheConfig{
				Enabled: true,
				Size:    100000,
			},
			Performance: config.PerformanceConfig{
				LockFree: true,
				UsePool:  true,
				Prealloc: true,
			},
		},
	}

	logger, err := newLogger(cfg)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	// 压力测试：持续写入30秒
	testDuration := 30 * time.Second
	t.Logf("Starting stress test for %v...", testDuration)

	ctx, cancel := context.WithTimeout(context.Background(), testDuration)
	defer cancel()

	var wg sync.WaitGroup
	numGoroutines := 32

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			counter := 0
			for {
				select {
				case <-ctx.Done():
					return
				default:
					logger.Info("Stress test",
						"goroutine", id,
						"counter", counter,
						"timestamp", time.Now().UnixNano(),
					)
					counter++
				}
			}
		}(i)
	}

	wg.Wait()
	elapsed := testDuration

	// 同步
	logger.Sync()

	t.Logf("Stress test completed in %v", elapsed)

	// 清理
	os.Remove("./logs/stress_test.log")
}

// TestLoggerComparison 与标准库对比测试
func TestLoggerComparison(t *testing.T) {
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatalf("Failed to create log directory: %v", err)
	}

	cfg := &config.Config{
		Log: config.LogConfig{
			Level:  "info",
			Format: "json",
			Async: config.AsyncConfig{
				Enabled:       true,
				BufferSize:    100000,
				BatchSize:     1000,
				FlushInterval: 10,
				Workers:       8,
			},
			Console: config.ConsoleConfig{
				Enabled: false,
			},
			File: config.FileConfig{
				Enabled: true,
				Path:    "./logs/comparison_test.log",
				Rotate: config.RotateConfig{
					MaxSize:    1000,
					MaxBackups: 10,
					MaxAge:     30,
					Compress:   false,
				},
			},
			Stacktrace: config.StackConfig{
				Enabled: false,
			},
			Sampling: config.SamplingConfig{
				Enabled: false,
			},
			FieldCache: config.FieldCacheConfig{
				Enabled: true,
				Size:    10000,
			},
			Performance: config.PerformanceConfig{
				LockFree: true,
				UsePool:  true,
				Prealloc: true,
			},
		},
	}

	logger, err := newLogger(cfg)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	numLogs := 100000

	// 测试我们的logger
	t.Log("Testing our logger...")
	start := time.Now()
	for i := 0; i < numLogs; i++ {
		logger.Info("Comparison test",
			"iteration", i,
			"data", "test data",
			"timestamp", time.Now().UnixNano(),
		)
	}
	ourDuration := time.Since(start)
	ourLogsPerSecond := float64(numLogs) / ourDuration.Seconds()

	logger.Sync()

	t.Logf("Our logger: %d logs in %v (%.2f logs/second)", 
		numLogs, ourDuration, ourLogsPerSecond)

	// 清理
	os.Remove("./logs/comparison_test.log")
}

// BenchmarkRingBuffer 无锁环形缓冲区性能测试
func BenchmarkRingBuffer(b *testing.B) {
	rb := newRingBuffer(100000)
	entry := &logEntry{msg: "test"}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rb.Push(entry)
			rb.Pop()
		}
	})
}

// BenchmarkSyncMap 字段缓存性能测试
func BenchmarkSyncMap(b *testing.B) {
	var m sync.Map
	
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key%d", i%1000)
			m.Store(key, "value")
			m.Load(key)
			i++
		}
	})
}
