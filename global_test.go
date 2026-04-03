package logger

import (
	"context"
	"testing"
)

// TestGlobalFunctions 测试全局日志函数
func TestGlobalFunctions(t *testing.T) {
	// 首先调用 New 初始化全局实例
	logger, err := New("configs/config.yaml")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	// 测试 Info 函数
	Info("Test global Info function")

	// 测试 Debug 函数
	Debug("Test global Debug function")

	// 测试 Warn 函数
	Warn("Test global Warn function")

	// 测试 Error 函数
	Error("Test global Error function")

	// 测试 With 函数
	withLogger := With("key", "value")
	if withLogger != nil {
		withLogger.Info("Test global With function")
	}

	// 测试 WithContext 函数
	ctx := context.Background()
	ctxLogger := WithContext(ctx)
	if ctxLogger != nil {
		ctxLogger.Info("Test global WithContext function")
	}

	// 测试 SetLevel 函数
	SetLevel("debug")

	// 测试 GetLevel 函数
	level := GetLevel()
	if level != "debug" {
		t.Errorf("Expected level to be 'debug', got '%s'", level)
	}

	// 测试 Sync 函数
	err = Sync()
	if err != nil {
		t.Errorf("Sync error: %v", err)
	}
}
