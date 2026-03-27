package logger

import (
	"context"
	"os"
	"testing"
)

func TestAllFeatures(t *testing.T) {
	// 确保日志目录存在
	logDir := "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatalf("Failed to create log directory: %v", err)
	}

	// 临时修改配置文件，启用文件输出
	originalConfig, err := os.ReadFile("../../configs/config.yaml")
	if err != nil {
		t.Fatalf("Failed to read config file: %v", err)
	}
	defer os.WriteFile("../../configs/config.yaml", originalConfig, 0644)

	// 修改配置文件，启用文件输出
	configContent := `log:
  level: debug
  format: json
  file:
    enabled: true
    path: ./logs/app.log
    rotate:
      max_size: 10
      max_backups: 5
      max_age: 7
  stacktrace:
    enabled: true
    level: error
  sampling:
    enabled: true
    initial: 100
    thereafter: 10
`
	err = os.WriteFile("../../configs/config.yaml", []byte(configContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	// 1. 测试基本日志功能
	t.Log("=== Testing basic logging ===")
	logger, err := New("../../configs/config.yaml")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	// 测试不同级别的日志
	logger.Debug("Debug message", "key1", "value1", "key2", 42)
	logger.Info("Info message", "key1", "value1", "key2", 42)
	logger.Warn("Warn message", "key1", "value1", "key2", 42)
	logger.Error("Error message", "key1", "value1", "key2", 42)

	// 2. 测试全局日志单例
	t.Log("=== Testing global logger ===")
	globalLogger := GetLogger()
	globalLogger.Info("Global logger info message", "key", "value")
	globalLogger.Error("Global logger error message", "key", "value")

	// 3. 测试 Hook 链
	t.Log("=== Testing hook chain ===")
	hookCalled := false
	testHook := &TestHookImpl{
		OnRun: func(msg string, level string, args ...any) {
			hookCalled = true
			t.Logf("Hook called with msg: %s, level: %s, args: %v", msg, level, args)
		},
	}

	loggerWithHook := logger.AddHook(testHook)
	loggerWithHook.Info("Test message with hook", "key", "value")

	// 等待异步日志处理完成
	err = loggerWithHook.Sync()
	if err != nil {
		t.Fatalf("Failed to sync logger: %v", err)
	}

	if !hookCalled {
		t.Fatalf("Hook was not called")
	}

	// 4. 测试 Context 自动注入
	t.Log("=== Testing context auto-injection ===")
	ctx := context.Background()
	ctx = context.WithValue(ctx, "request_id", "test-request-id")
	ctx = context.WithValue(ctx, "user_id", "test-user-id")
	ctx = context.WithValue(ctx, "span_id", "test-span-id")

	ctxLogger := logger.WithContext(ctx)
	ctxLogger.Info("Test message with context")

	// 5. 测试 Panic Recovery
	t.Log("=== Testing panic recovery ===")
	func() {
		defer Recover()

		// 触发 panic
		panic("test panic")
	}()

	// 6. 测试 Graceful Shutdown
	t.Log("=== Testing graceful shutdown ===")
	err = logger.Sync()
	if err != nil {
		t.Fatalf("Failed to sync logger: %v", err)
	}

	t.Log("All tests passed!")
}
