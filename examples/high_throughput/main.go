package main

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	logger "github.com/streasure/treasure-slog"
)

func main() {
	// 显式初始化全局 logger（首次 New 调用会设置全局实例）
	// 配置路径：默认 configs/config.yaml，可通过第一个位置参数指定
	configPath := "configs/config.yaml"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}
	if _, err := logger.New(configPath); err != nil {
		fmt.Println("初始化 logger 失败:", err)
		return
	}

	fmt.Println("=== Treasure-Slog 高吞吐量测试 ===")

	// 直接使用全局函数
	defer logger.Sync()

	// 测试配置
	const numLogs = 1000000
	const concurrency = 64

	fmt.Printf("测试配置: %d 条日志, %d 并发 goroutine\n", numLogs, concurrency)

	// 预热
	fmt.Println("预热中...")
	for i := 0; i < 1000; i++ {
		logger.Info("Warmup", "i", i)
	}
	time.Sleep(100 * time.Millisecond)

	// 开始测试
	fmt.Println("开始测试...")
	start := time.Now()

	// 并发测试
	var wg sync.WaitGroup
	logsPerGoroutine := numLogs / concurrency

	for i := 0; i < concurrency; i++ {
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

	// 等待所有 goroutine 完成
	wg.Wait()

	// 计算结果
	elapsed := time.Since(start)
	logsPerSecond := float64(numLogs) / elapsed.Seconds()

	fmt.Printf("测试完成!\n")
	fmt.Printf("耗时: %v\n", elapsed)
	fmt.Printf("吞吐量: %.2f 日志/秒\n", logsPerSecond)
	fmt.Printf("CPU 核心数: %d\n", runtime.NumCPU())

	// 等待异步写入完成
	time.Sleep(1 * time.Second)
	fmt.Println("=== 测试结束 ===")
}
