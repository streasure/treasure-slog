package main

import (
	"context"
	"github.com/streasure/treasure-slog"
)

func main() {
	// 直接使用全局函数记录日志
	treasure_slog.Info("Hello from global Info function")
	treasure_slog.Debug("Hello from global Debug function")
	treasure_slog.Warn("Hello from global Warn function")
	treasure_slog.Error("Hello from global Error function")

	// 使用 With 函数添加字段
	logger := treasure_slog.With("user", "john", "age", 30)
	logger.Info("User info")

	// 使用 WithContext 函数添加上下文
	ctx := context.Background()
	ctxLogger := treasure_slog.WithContext(ctx)
	ctxLogger.Info("WithContext example")

	// 动态设置日志级别
	treasure_slog.SetLevel("debug")
	level := treasure_slog.GetLevel()
	treasure_slog.Info("Current log level", "level", level)

	// 同步日志
	treasure_slog.Sync()
}
