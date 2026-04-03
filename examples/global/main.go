package main

import (
	"context"
	logger "github.com/streasure/treasure-slog"
)

func main() {
	// 直接使用全局函数记录日志
	logger.Info("Hello from global Info function")
	logger.Debug("Hello from global Debug function")
	logger.Warn("Hello from global Warn function")
	logger.Error("Hello from global Error function")

	// 使用 With 函数添加字段
	logger := logger.With("user", "john", "age", 30)
	logger.Info("User info")

	// 使用 WithContext 函数添加上下文
	ctx := context.Background()
	ctxLogger := logger.WithContext(ctx)
	ctxLogger.Info("WithContext example")

	// 动态设置日志级别
	logger.SetLevel("debug")
	level := logger.GetLevel()
	logger.Info("Current log level", "level", level)

	// 同步日志
	logger.Sync()
}
