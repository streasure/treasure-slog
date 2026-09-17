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

			ctx := context.Background()
			logger.Debug(ctx, "Debug message key1=%s key2=%d", "value1", 42)
			logger.Info(ctx, "Info message key1=%s key2=%d", "value1", 42)
			logger.Warn(ctx, "Warn message key1=%s key2=%d", "value1", 42)
			logger.Error(ctx, "Error message key1=%s key2=%d", "value1", 42)

			withLogger := logger.With("context", "test")
			withLogger.Debug(ctx, "Debug message with context key=%s", "value")
			withLogger.Info(ctx, "Info message with context key=%s", "value")
			withLogger.Warn(ctx, "Warn message with context key=%s", "value")
			withLogger.Error(ctx, "Error message with context key=%s", "value")

			ctx = context.WithValue(ctx, "test-key", "test-value")
			ctxLogger := logger.WithContext(ctx)
			ctxLogger.Debug(ctx, "Debug message with context object key=%s", "value")
			ctxLogger.Info(ctx, "Info message with context object key=%s", "value")
			ctxLogger.Warn(ctx, "Warn message with context object key=%s", "value")
			ctxLogger.Error(ctx, "Error message with context object key=%s", "value")

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
	ctx := context.Background()
	logger.Info(ctx, "Info message with default level key=%s", "value")
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

	ctx := context.Background()
	Info(ctx, "Global Info function key=%s", "value")
	Debug(ctx, "Global Debug function key=%s", "value")
	Warn(ctx, "Global Warn function key=%s", "value")
	Error(ctx, "Global Error function key=%s", "value")

	withLogger := With("context", "test")
	if withLogger != nil {
		withLogger.Info(ctx, "Global With function")
	}

	ctxLogger := WithContext(ctx)
	if ctxLogger != nil {
		ctxLogger.Info(ctx, "Global WithContext function")
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
	ctx := context.Background()
	loggerWithHook.Info(ctx, "Test message key=%s", "value")

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
	ctxLogger.Info(ctx, "Test message with context")
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
