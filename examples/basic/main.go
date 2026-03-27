package main

import (
	"context"
	"fmt"
	"time"

	"treasure-slog/pkg/logger"
)

func main() {
	fmt.Println("=== Treasure-Slog 基础使用示例 ===\n")

	// 1. 基础使用 - 全局日志单例
	fmt.Println("1. 基础日志记录")
	log := logger.GetLogger()
	
	log.Info("应用启动", "version", "1.0.0", "env", "production")
	log.Debug("调试信息", "detail", "some debug data")
	log.Warn("警告信息", "threshold", 80)
	log.Error("错误信息", "error", "connection failed")
	fmt.Println()

	// 2. With 添加固定字段
	fmt.Println("2. 使用 With 添加固定字段")
	userLog := log.With("user_id", "12345", "ip", "192.168.1.1")
	userLog.Info("用户登录")
	userLog.Info("用户操作", "action", "buy", "item", "product-001")
	fmt.Println()

	// 3. Context 自动注入
	fmt.Println("3. Context 自动注入追踪信息")
	ctx := context.Background()
	ctx = context.WithValue(ctx, "request_id", "req-abc-123")
	ctx = context.WithValue(ctx, "user_id", "user-456")
	ctx = context.WithValue(ctx, "trace_id", "trace-xyz-789")
	
	ctxLog := log.WithContext(ctx)
	ctxLog.Info("处理请求", "endpoint", "/api/users", "method", "GET")
	fmt.Println()

	// 4. 动态调整日志级别
	fmt.Println("4. 动态调整日志级别")
	fmt.Printf("当前日志级别: %s\n", log.GetLevel())
	
	log.Debug("这条 debug 日志不会显示（当前级别 info）")
	
	log.SetLevel("debug")
	fmt.Printf("调整后级别: %s\n", log.GetLevel())
	log.Debug("现在这条 debug 日志会显示了")
	
	log.SetLevel("info") // 恢复
	fmt.Println()

	// 5. Hook 机制
	fmt.Println("5. Hook 机制示例")
	metricsHook := &MetricsHook{counter: make(map[string]int)}
	hookedLog := log.AddHook(metricsHook)
	
	hookedLog.Info("带 Hook 的日志")
	hookedLog.Warn("警告日志")
	hookedLog.Error("错误日志")
	fmt.Printf("统计: %+v\n\n", metricsHook.counter)

	// 6. Panic 恢复
	fmt.Println("6. Panic 恢复示例")
	riskyOperation()
	fmt.Println("程序继续执行（Panic 被捕获）\n")

	// 等待异步日志写入完成
	time.Sleep(100 * time.Millisecond)
	
	// 同步日志（应用退出前必须调用）
	log.Sync()
	
	fmt.Println("=== 示例结束 ===")
}

// MetricsHook 自定义 Hook 示例
type MetricsHook struct {
	counter map[string]int
}

func (h *MetricsHook) Run(msg string, level string, args ...any) {
	h.counter[level]++
}

// riskyOperation 可能触发 panic 的操作
func riskyOperation() {
	defer logger.Recover() // 自动捕获 panic 并记录
	
	panic("模拟的 panic 错误")
}
