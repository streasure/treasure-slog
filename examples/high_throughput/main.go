package main

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"treasure-slog/pkg/logger"
)

func main() {
	fmt.Println("=== Treasure-Slog 高吞吐量测试示例 ===\n")

	// 使用高性能配置
	log, err := logger.New("configs/config.highperf.yaml")
	if err != nil {
		panic(err)
	}
	defer log.Sync()

	// 测试参数
	numGoroutines := runtime.NumCPU() * 4
	logsPerGoroutine := 100000
	totalLogs := numGoroutines * logsPerGoroutine

	fmt.Printf("测试配置:\n")
	fmt.Printf("  Goroutines: %d\n", numGoroutines)
	fmt.Printf("  每 Goroutine 日志数: %d\n", logsPerGoroutine)
	fmt.Printf("  总日志数: %d\n", totalLogs)
	fmt.Printf("  CPU 核心数: %d\n\n", runtime.NumCPU())

	// 预热
	fmt.Println("预热中...")
	for i := 0; i < 10000; i++ {
		log.Info("预热日志", "index", i)
	}
	time.Sleep(500 * time.Millisecond)

	// 开始测试
	fmt.Println("开始高吞吐量测试...")
	start := time.Now()

	var wg sync.WaitGroup
	var counter int64

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < logsPerGoroutine; j++ {
				log.Info("高吞吐量测试日志",
					"goroutine_id", id,
					"iteration", j,
					"timestamp", time.Now().UnixNano(),
					"data", "high throughput logging test data",
					"status", "success",
					"counter", atomic.AddInt64(&counter, 1),
				)
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	// 等待异步写入完成
	log.Sync()

	// 计算性能指标
	logsPerSecond := float64(totalLogs) / elapsed.Seconds()
	nsPerLog := float64(elapsed.Nanoseconds()) / float64(totalLogs)

	fmt.Printf("\n测试结果:\n")
	fmt.Printf("  总耗时: %v\n", elapsed)
	fmt.Printf("  吞吐量: %.2f 日志/秒\n", logsPerSecond)
	fmt.Printf("  平均延迟: %.2f ns/日志\n", nsPerLog)
	fmt.Printf("  目标: 1,000,000 日志/秒\n")
	
	if logsPerSecond >= 1000000 {
		fmt.Printf("  结果: ✓ 达标 (%.2fx 目标)\n", logsPerSecond/1000000)
	} else {
		fmt.Printf("  结果: ✗ 未达标 (%.2f%% 目标)\n", logsPerSecond/10000)
	}

	// 打印内存统计
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("\n内存统计:\n")
	fmt.Printf("  分配内存: %.2f MB\n", float64(m.Alloc)/1024/1024)
	fmt.Printf("  总分配: %.2f MB\n", float64(m.TotalAlloc)/1024/1024)
	fmt.Printf("  GC 次数: %d\n", m.NumGC)

	fmt.Println("\n=== 测试结束 ===")
}
