package logger

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLogger(t *testing.T) {
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
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.yaml")

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
			if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
				t.Fatalf("Failed to write config file: %v", err)
			}

			logger, err := New(configPath)
			if err != nil {
				t.Fatalf("Failed to create logger: %v", err)
			}

			logger.Debug("Debug message", "key1", "value1", "key2", 42)
			logger.Info("Info message", "key1", "value1", "key2", 42)
			logger.Warn("Warn message", "key1", "value1", "key2", 42)
			logger.Error("Error message", "key1", "value1", "key2", 42)

			withLogger := logger.With("context", "test")
			withLogger.Debug("Debug message with context", "key", "value")
			withLogger.Info("Info message with context", "key", "value")
			withLogger.Warn("Warn message with context", "key", "value")
			withLogger.Error("Error message with context", "key", "value")

			ctx := context.WithValue(context.Background(), "test-key", "test-value")
			ctxLogger := logger.WithContext(ctx)
			ctxLogger.Debug("Debug message with context object", "key", "value")
			ctxLogger.Info("Info message with context object", "key", "value")
			ctxLogger.Warn("Warn message with context object", "key", "value")
			ctxLogger.Error("Error message with context object", "key", "value")

			err = logger.Sync()
			if err != nil {
				t.Fatalf("Failed to sync logger: %v", err)
			}
		})
	}
}

func TestLoggerWithDefaultLevel(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `log:
  level: info
  format: json
  file:
    enabled: false
  stacktrace:
    enabled: false
  sampling:
    enabled: false
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	logger, err := New(configPath)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	logger.Info("Info message with default level", "key", "value")
}

func TestGlobalLogger(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `log:
  level: debug
  format: json
  file:
    enabled: false
  stacktrace:
    enabled: false
  sampling:
    enabled: false
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	originalGlobal := globalLogger
	defer func() { globalLogger = originalGlobal }()

	l, err := New(configPath)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	globalLogger = l

	Info("Global Info function", "key", "value")
	Debug("Global Debug function", "key", "value")
	Warn("Global Warn function", "key", "value")
	Error("Global Error function", "key", "value")

	withLogger := With("context", "test")
	if withLogger != nil {
		withLogger.Info("Global With function")
	}

	ctx := context.Background()
	ctxLogger := WithContext(ctx)
	if ctxLogger != nil {
		ctxLogger.Info("Global WithContext function")
	}

	SetLevel("debug")
	level := GetLevel()
	if level != "debug" {
		t.Errorf("Expected level to be 'debug', got '%s'", level)
	}

	err = Sync()
	if err != nil {
		t.Errorf("Sync error: %v", err)
	}
}

func TestPanicRecovery(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Logf("Recovered from panic: %v", r)
		}
	}()

	panic("test panic")
}

func TestHookFunction(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `log:
  level: info
  format: json
  file:
    enabled: false
  stacktrace:
    enabled: false
  sampling:
    enabled: false
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	logger, err := New(configPath)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	hookCalled := false
	testHook := &TestHookImpl{
		OnRun: func(msg string, level string, args ...any) {
			hookCalled = true
			t.Logf("Hook called with msg: %s, level: %s, args: %v", msg, level, args)
		},
	}

	loggerWithHook := logger.AddHook(testHook)
	loggerWithHook.Info("Test message", "key", "value")

	err = loggerWithHook.Sync()
	if err != nil {
		t.Fatalf("Failed to sync logger: %v", err)
	}

	if !hookCalled {
		t.Fatalf("Hook was not called")
	}
}

func TestContextAutoInject(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `log:
  level: info
  format: json
  file:
    enabled: false
  stacktrace:
    enabled: false
  sampling:
    enabled: false
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	logger, err := New(configPath)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	ctx := context.Background()
	ctx = context.WithValue(ctx, "request_id", "test-request-id")
	ctx = context.WithValue(ctx, "user_id", "test-user-id")
	ctx = context.WithValue(ctx, "span_id", "test-span-id")

	ctxLogger := logger.WithContext(ctx)
	ctxLogger.Info("Test message with context")
}

func TestSync(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `log:
  level: info
  format: json
  file:
    enabled: false
  stacktrace:
    enabled: false
  sampling:
    enabled: false
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	logger, err := New(configPath)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

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
