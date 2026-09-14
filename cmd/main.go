package main

import (
	"fmt"
	"time"

	logger "github.com/streasure/treasure-slog"
)

func main() {
	// 显式初始化全局 logger（首次 New 调用会设置全局实例）
	if _, err := logger.New("configs/config.yaml"); err != nil {
		fmt.Println("初始化 logger 失败:", err)
		return
	}

	fmt.Println("=== Treasure-Slog 示例程序 ===")

	// 基础日志记录
	fmt.Println("1. 基础日志记录")
	logger.Info("应用启动", "version", "1.0.0", "env", "development")
	logger.Debug("调试信息", "module", "main", "status", "initialized")
	logger.Warn("警告信息", "threshold", 90, "action", "monitor")
	logger.Error("错误信息", "error", "database connection failed", "retry", true)
	fmt.Println()

	// 带固定字段的日志
	fmt.Println("2. 带固定字段的日志")
	userLog := logger.With("user_id", "12345", "role", "admin")
	userLog.Info("用户登录", "ip", "192.168.1.1", "browser", "Chrome")
	userLog.Info("用户操作", "action", "create", "resource", "user")
	fmt.Println()

	// 动态调整日志级别
	fmt.Println("3. 动态调整日志级别")
	fmt.Printf("当前日志级别: %s\n", logger.GetLevel())
	logger.Debug("这条 debug 日志不会显示")

	logger.SetLevel("debug")
	fmt.Printf("调整后级别: %s\n", logger.GetLevel())
	logger.Debug("现在这条 debug 日志会显示")

	logger.SetLevel("info") // 恢复默认级别
	fmt.Println()

	// 测试性能
	fmt.Println("4. 性能测试")
	start := time.Now()
	for i := 0; i < 10000; i++ {
		logger.Info("性能测试", "iteration", i, "timestamp", time.Now().UnixNano())
	}
	elapsed := time.Since(start)
	fmt.Printf("10000 条日志耗时: %v\n", elapsed)
	fmt.Printf("平均速度: %.2f 条/秒\n", float64(10000)/elapsed.Seconds())
	fmt.Println()

	// 测试 Panic 恢复
	fmt.Println("5. Panic 恢复测试")
	testPanic()
	fmt.Println("程序继续执行（Panic 被捕获）")
	fmt.Println()

	// 等待异步日志写入完成
	time.Sleep(500 * time.Millisecond)

	// 同步日志（应用退出前必须调用）
	logger.Sync()

	fmt.Println("=== 示例结束 ===")
}

// testPanic 测试 Panic 恢复
func testPanic() {
	defer logger.Recover()
	panic("模拟的 panic 错误")
}
