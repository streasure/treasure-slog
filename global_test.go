package logger

import (
	"context"
	"testing"
)

// TestGlobalFunctions 测试全局日志函数
func TestGlobalFunctions(t *testing.T) {
	// 测试 Info 函数
	Info("Test global Info function")

	// 测试 Debug 函数
	Debug("Test global Debug function")

	// 测试 Warn 函数
	Warn("Test global Warn function")

	// 测试 Error 函数
	Error("Test global Error function")

	// 测试 With 函数
	logger := With("key", "value")
	logger.Info("Test global With function")

	// 测试 WithContext 函数
	ctx := context.Background()
	ctxLogger := WithContext(ctx)
	ctxLogger.Info("Test global WithContext function")

	// 测试 SetLevel 函数
	SetLevel("debug")

	// 测试 GetLevel 函数
	level := GetLevel()
	if level != "debug" {
		t.Errorf("Expected level to be 'debug', got '%s'", level)
	}

	// 测试 Sync 函数
	err := Sync()
	if err != nil {
		t.Errorf("Sync error: %v", err)
	}
}
