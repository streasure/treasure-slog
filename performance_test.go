package logger

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// BenchmarkFullLink 全链路压测
func BenchmarkFullLink(b *testing.B) {
	// 确保日志目录存在
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		b.Fatalf("Failed to create log directory: %v", err)
	}

	// 创建临时配置文件
	configContent := `
log:
  level: info
  format: json
  async:
    enabled: true
    buffer_size: 1000000
    batch_size: 1000
    flush_interval: 10
    workers: 8
  console:
    enabled: false
  file:
    enabled: true
    path: ./logs/benchmark.log
    rotate:
      max_size: 1000
      max_backups: 10
      max_age: 30
      compress: false
  stacktrace:
    enabled: false
  sampling:
    enabled: false
  field_cache:
    enabled: true
    size: 100000
  performance:
    lock_free: true
    use_pool: true
    prealloc: true
`

	// 写入临时配置文件
	configPath := "./logs/benchmark_config.yaml"
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		b.Fatalf("Failed to write config file: %v", err)
	}
	defer os.Remove(configPath)
	defer os.Remove("./logs/benchmark.log")

	// 使用 New 函数创建日志记录器
	logger, err := New(configPath)
	if err != nil {
		b.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

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
				"timestamp", time.Now().UnixNano(),
			)
		}
	})

	// 同步日志
	b.StopTimer()
	err = logger.Sync()
	if err != nil {
		b.Fatalf("Failed to sync logger: %v", err)
	}
}

// BenchmarkAsync 异步性能测试
func BenchmarkAsync(b *testing.B) {
	// 确保日志目录存在
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		b.Fatalf("Failed to create log directory: %v", err)
	}

	// 测试异步模式
	// 创建异步配置文件
	configContent := `
log:
  level: info
  format: json
  async:
    enabled: true
    buffer_size: 1000000
    batch_size: 1000
    flush_interval: 10
    workers: 8
  console:
    enabled: false
  file:
    enabled: true
    path: ./logs/async_benchmark.log
    rotate:
      max_size: 1000
      max_backups: 10
      max_age: 30
      compress: false
  performance:
    lock_free: true
    use_pool: true
    prealloc: true
`

	// 写入临时配置文件
	configPath := "./logs/async_config.yaml"
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		b.Fatalf("Failed to write config file: %v", err)
	}
	defer os.Remove(configPath)
	defer os.Remove("./logs/async_benchmark.log")

	// 创建日志记录器
	logger, err := New(configPath)
	if err != nil {
		b.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	// 预热
	for i := 0; i < 1000; i++ {
		logger.Info("Warmup", "i", i)
	}
	time.Sleep(100 * time.Millisecond)

	// 测试
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Info("Async log message", "key", "value")
		}
	})
	b.StopTimer()
	logger.Sync()
}

// BenchmarkDifferentLevels 不同日志级别性能测试
func BenchmarkDifferentLevels(b *testing.B) {
	// 确保日志目录存在
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		b.Fatalf("Failed to create log directory: %v", err)
	}

	// 创建配置文件
	configContent := `
log:
  level: debug
  format: json
  async:
    enabled: true
    buffer_size: 1000000
    batch_size: 1000
    flush_interval: 10
    workers: 8
  console:
    enabled: false
  file:
    enabled: true
    path: ./logs/levels_benchmark.log
    rotate:
      max_size: 1000
      max_backups: 10
      max_age: 30
      compress: false
`

	// 写入临时配置文件
	configPath := "./logs/levels_config.yaml"
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		b.Fatalf("Failed to write config file: %v", err)
	}
	defer os.Remove(configPath)
	defer os.Remove("./logs/levels_benchmark.log")

	// 创建日志记录器
	logger, err := New(configPath)
	if err != nil {
		b.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	// 测试不同级别
	levels := []struct {
		name string
		fn   func(string, ...any)
	}{
		{"Debug", logger.Debug},
		{"Info", logger.Info},
		{"Warn", logger.Warn},
		{"Error", logger.Error},
	}

	for _, level := range levels {
		b.Run(level.name, func(b *testing.B) {
			// 预热
			for i := 0; i < 1000; i++ {
				level.fn("Warmup", "i", i)
			}
			time.Sleep(100 * time.Millisecond)

			// 测试
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					level.fn(level.name+" log message", "key", "value")
				}
			})
			b.StopTimer()
			logger.Sync()
		})
	}
}

// BenchmarkHighConcurrency 高并发测试
func BenchmarkHighConcurrency(b *testing.B) {
	// 确保日志目录存在
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		b.Fatalf("Failed to create log directory: %v", err)
	}

	// 创建高并发配置文件
	configContent := `
log:
  level: info
  format: json
  async:
    enabled: true
    buffer_size: 2000000
    batch_size: 2000
    flush_interval: 5
    workers: 16
  console:
    enabled: false
  file:
    enabled: true
    path: ./logs/concurrency_benchmark.log
    rotate:
      max_size: 1000
      max_backups: 10
      max_age: 30
      compress: false
  performance:
    lock_free: true
    use_pool: true
    prealloc: true
`

	// 写入临时配置文件
	configPath := "./logs/concurrency_config.yaml"
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		b.Fatalf("Failed to write config file: %v", err)
	}
	defer os.Remove(configPath)
	defer os.Remove("./logs/concurrency_benchmark.log")

	// 创建日志记录器
	logger, err := New(configPath)
	if err != nil {
		b.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	// 预热
	for i := 0; i < 1000; i++ {
		logger.Info("Warmup", "i", i)
	}
	time.Sleep(100 * time.Millisecond)

	// 测试不同并发数
	concurrencies := []int{1, 2, 4, 8, 16, 32, 64}

	for _, concurrency := range concurrencies {
		b.Run(fmt.Sprintf("Concurrency_%d", concurrency), func(b *testing.B) {
			var wg sync.WaitGroup
			logsPerGoroutine := b.N / concurrency

			b.ResetTimer()
			for i := 0; i < concurrency; i++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					for j := 0; j < logsPerGoroutine; j++ {
						logger.Info("High concurrency log",
							"goroutine", id,
							"iteration", j,
							"timestamp", time.Now().UnixNano(),
						)
					}
				}(i)
			}
			wg.Wait()
			b.StopTimer()
			logger.Sync()
		})
	}
}

// BenchmarkFieldCount 不同字段数量性能测试
func BenchmarkFieldCount(b *testing.B) {
	// 确保日志目录存在
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		b.Fatalf("Failed to create log directory: %v", err)
	}

	// 创建配置文件
	configContent := `
log:
  level: info
  format: json
  async:
    enabled: true
    buffer_size: 1000000
    batch_size: 1000
    flush_interval: 10
    workers: 8
  console:
    enabled: false
  file:
    enabled: true
    path: ./logs/fields_benchmark.log
    rotate:
      max_size: 1000
      max_backups: 10
      max_age: 30
      compress: false
  field_cache:
    enabled: true
    size: 100000
`

	// 写入临时配置文件
	configPath := "./logs/fields_config.yaml"
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		b.Fatalf("Failed to write config file: %v", err)
	}
	defer os.Remove(configPath)
	defer os.Remove("./logs/fields_benchmark.log")

	// 创建日志记录器
	logger, err := New(configPath)
	if err != nil {
		b.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	// 测试不同字段数量
	fieldCounts := []int{2, 4, 8, 16, 32}

	for _, count := range fieldCounts {
		b.Run(fmt.Sprintf("Fields_%d", count), func(b *testing.B) {
			// 预热
			for i := 0; i < 1000; i++ {
				args := []any{"Warmup"}
				for j := 0; j < count/2; j++ {
					args = append(args, fmt.Sprintf("key%d", j), fmt.Sprintf("value%d", j))
				}
				if len(args) == 1 {
					logger.Info(args[0].(string))
				} else {
					logger.Info(args[0].(string), args[1:]...)
				}
			}
			time.Sleep(100 * time.Millisecond)

			// 测试
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					args := []any{"Log message with many fields"}
					for j := 0; j < count/2; j++ {
						args = append(args, fmt.Sprintf("key%d", j), fmt.Sprintf("value%d", j))
					}
					if len(args) == 1 {
						logger.Info(args[0].(string))
					} else {
						logger.Info(args[0].(string), args[1:]...)
					}
				}
			})
			b.StopTimer()
			logger.Sync()
		})
	}
}
