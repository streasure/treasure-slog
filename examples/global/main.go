package main

import (
	"context"
	"fmt"
	"os"

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

	// 直接使用全局函数记录日志
	logger.Info("Hello from global Info function")
	logger.Debug("Hello from global Debug function")
	logger.Warn("Hello from global Warn function")
	logger.Error("Hello from global Error function")

	// 使用 With 函数添加字段
	userLogger := logger.With("user", "john", "age", 30)
	userLogger.Info("User info")

	// 使用 WithContext 函数添加上下文
	ctx := context.Background()
	ctxLogger := userLogger.WithContext(ctx)
	ctxLogger.Info("WithContext example")

	// 动态设置日志级别
	logger.SetLevel("debug")
	level := logger.GetLevel()
	logger.Info("Current log level", "level", level)

	// 同步日志
	logger.Sync()
}
