package main

import (
	"flag"
	"fmt"
	"os"

	logger "treasure-slog"
)

func main() {
	// 定义命令行参数
	configPath := flag.String("config", "", "配置文件路径")
	flag.Parse()

	// 如果指定了命令行参数，设置环境变量
	if *configPath != "" {
		os.Setenv("LOG_CONFIG_PATH", *configPath)
		fmt.Printf("使用命令行指定的配置文件: %s\n", *configPath)
	} else {
		fmt.Println("使用默认配置文件或环境变量指定的配置文件")
	}

	// 获取全局日志器
	log := logger.GetLogger()
	defer log.Sync()

	// 测试日志
	log.Info("应用启动", "version", "1.0.0", "env", "production")
	log.Debug("调试信息", "detail", "some debug data")
	log.Warn("警告信息", "threshold", 80)
	log.Error("错误信息", "error", "connection failed")

	fmt.Println("日志测试完成")
}
