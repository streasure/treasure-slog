package main

import (
	"context"
	"fmt"
	"os"

	logger "github.com/streasure/treasure-slog"
)

func main() {
	// 显式初始化全局 logger（首次 New 调用会设置全局实例）
	// 配置路径：默认 configs/tlog.yaml，可通过第一个位置参数指定
	configPath := "configs/tlog.yaml"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}
	if _, err := logger.New(configPath); err != nil {
		fmt.Println("初始化 logger 失败:", err)
		return
	}

	ctx := context.Background()

	// 直接使用全局函数记录日志
	logger.Info(ctx, "Hello from global Info function")
	logger.Debug(ctx, "Hello from global Debug function")
	logger.Warn(ctx, "Hello from global Warn function")
	logger.Error(ctx, "Hello from global Error function")

	// 使用 With 函数添加字段
	userLogger := logger.With("user", "john", "age", 30)
	userLogger.Info(ctx, "User info")

	// 使用 WithContext 函数添加上下文
	ctx = context.WithValue(ctx, logger.ContextKeyRequestID, "req-123")
	ctxLogger := userLogger.WithContext(ctx)
	ctxLogger.Info(ctx, "WithContext example")

	// 动态设置日志级别
	logger.SetLevel("debug")
	level := logger.GetLevel()
	logger.Info(ctx, "Current log level=%s", level)

	// 同步日志
	logger.Sync()
}
