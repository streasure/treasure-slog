package logger

import (
	"context"
	"os"
	"testing"
)

func TestLogger(t *testing.T) {
	// 创建不同级别的日志记录器
	testCases := []struct {
		level string
		name  string
	}{
		{"debug", "Debug Logger"},
		{"info", "Info Logger"},
		{"warn", "Warn Logger"},
		{"error", "Error Logger"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 临时修改配置文件
			originalConfig, err := os.ReadFile("../../configs/config.yaml")
			if err != nil {
				t.Fatalf("Failed to read config file: %v", err)
			}
			defer os.WriteFile("../../configs/config.yaml", originalConfig, 0644)

			// 修改配置文件中的日志级别
			configContent := `log:
  level: ` + tc.level + `
  format: json
  file:
    enabled: false
  stacktrace:
    enabled: true
    level: error
  sampling:
    enabled: false
`
			err = os.WriteFile("../../configs/config.yaml", []byte(configContent), 0644)
			if err != nil {
				t.Fatalf("Failed to write config file: %v", err)
			}

			logger, err := New("../../configs/config.yaml")
			if err != nil {
				t.Fatalf("Failed to create logger: %v", err)
			}

			// 测试基本日志方法
			logger.Debug("Debug message", "key1", "value1", "key2", 42)
			logger.Info("Info message", "key1", "value1", "key2", 42)
			logger.Warn("Warn message", "key1", "value1", "key2", 42)
			logger.Error("Error message", "key1", "value1", "key2", 42)

			// 测试 With 方法
			withLogger := logger.With("context", "test")
			withLogger.Debug("Debug message with context", "key", "value")
			withLogger.Info("Info message with context", "key", "value")
			withLogger.Warn("Warn message with context", "key", "value")
			withLogger.Error("Error message with context", "key", "value")

			// 测试 WithContext 方法
			ctx := context.WithValue(context.Background(), "test-key", "test-value")
			ctxLogger := logger.WithContext(ctx)
			ctxLogger.Debug("Debug message with context object", "key", "value")
			ctxLogger.Info("Info message with context object", "key", "value")
			ctxLogger.Warn("Warn message with context object", "key", "value")
			ctxLogger.Error("Error message with context object", "key", "value")

			// 测试 Sync 方法
			err = logger.Sync()
			if err != nil {
				t.Fatalf("Failed to sync logger: %v", err)
			}
		})
	}
}

func TestLoggerWithDefaultLevel(t *testing.T) {
	// 测试默认日志级别
	logger, err := New("../../configs/config.yaml")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	logger.Info("Info message with default level", "key", "value")
}

func TestGlobalLogger(t *testing.T) {
	// 测试全局日志单例
	logger1 := GetLogger()
	logger2 := GetLogger()
	if logger1 != logger2 {
		t.Fatalf("Global logger is not a singleton")
	}

	// 测试全局日志的使用
	logger1.Info("Global logger info message", "key", "value")
	logger1.Error("Global logger error message", "key", "value")
}

func TestPanicRecovery(t *testing.T) {
	// 测试 Panic Recovery
	defer Recover()

	// 触发 panic
	panic("test panic")
}

func TestHookFunction(t *testing.T) {
	// 测试 Hook 功能
	logger, err := New("../../configs/config.yaml")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	// 创建一个测试 Hook
	hookCalled := false
	testHook := &TestHookImpl{
		OnRun: func(msg string, level string, args ...any) {
			hookCalled = true
			t.Logf("Hook called with msg: %s, level: %s, args: %v", msg, level, args)
		},
	}

	// 添加 Hook
	loggerWithHook := logger.AddHook(testHook)

	// 记录日志，触发 Hook
	loggerWithHook.Info("Test message", "key", "value")

	// 等待异步日志处理完成
	err = loggerWithHook.Sync()
	if err != nil {
		t.Fatalf("Failed to sync logger: %v", err)
	}

	// 验证 Hook 被调用
	if !hookCalled {
		t.Fatalf("Hook was not called")
	}
}

func TestContextAutoInject(t *testing.T) {
	// 测试 Context 自动注入功能
	logger, err := New("../../configs/config.yaml")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	// 创建一个带有信息的 context
	ctx := context.Background()
	ctx = context.WithValue(ctx, "request_id", "test-request-id")
	ctx = context.WithValue(ctx, "user_id", "test-user-id")
	ctx = context.WithValue(ctx, "span_id", "test-span-id")

	// 创建带有 context 的 logger
	ctxLogger := logger.WithContext(ctx)

	// 记录日志，验证 context 信息被注入
	ctxLogger.Info("Test message with context")
}

func TestSync(t *testing.T) {
	// 测试 Graceful Shutdown 功能
	logger, err := New("../../configs/config.yaml")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	// 调用 Sync 方法
	err = logger.Sync()
	if err != nil {
		t.Fatalf("Failed to sync logger: %v", err)
	}
}

// TestHookImpl 测试用的 Hook 实现
type TestHookImpl struct {
	OnRun func(msg string, level string, args ...any)
}

func (h *TestHookImpl) Run(msg string, level string, args ...any) {
	if h.OnRun != nil {
		h.OnRun(msg, level, args...)
	}
}
