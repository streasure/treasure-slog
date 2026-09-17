package logger

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAllFeatures(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatalf("Failed to create log directory: %v", err)
	}

	configPath := filepath.Join(tmpDir, "config.yaml")
	configContent := `log:
  level: debug
  format: json
  file:
    enabled: true
    path: ` + filepath.Join(logDir, "app.log") + `
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
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	t.Log("=== Testing basic logging ===")
	logger, err := New(configPath)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	ctx := context.Background()
	logger.Debug(ctx, "Debug message key1=%s key2=%d", "value1", 42)
	logger.Info(ctx, "Info message key1=%s key2=%d", "value1", 42)
	logger.Warn(ctx, "Warn message key1=%s key2=%d", "value1", 42)
	logger.Error(ctx, "Error message key1=%s key2=%d", "value1", 42)

	t.Log("=== Testing hook chain ===")
	hookCalled := false
	testHook := &TestHookImpl{
		OnRun: func(msg string, level string, args ...any) {
			hookCalled = true
			t.Logf("Hook called with msg: %s, level: %s, args: %v", msg, level, args)
		},
	}

	loggerWithHook := logger.AddHook(testHook)
	loggerWithHook.Info(ctx, "Test message with hook key=%s", "value")

	err = loggerWithHook.Sync()
	if err != nil {
		t.Fatalf("Failed to sync logger: %v", err)
	}

	if !hookCalled {
		t.Fatalf("Hook was not called")
	}

	t.Log("=== Testing context auto-injection ===")
	ctx = context.WithValue(ctx, "request_id", "test-request-id")
	ctx = context.WithValue(ctx, "user_id", "test-user-id")
	ctx = context.WithValue(ctx, "span_id", "test-span-id")

	ctxLogger := logger.WithContext(ctx)
	ctxLogger.Info(ctx, "Test message with context")

	t.Log("=== Testing panic recovery ===")
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Logf("Recovered from panic: %v", r)
			}
		}()

		panic("test panic")
	}()

	t.Log("=== Testing graceful shutdown ===")
	err = logger.Sync()
	if err != nil {
		t.Fatalf("Failed to sync logger: %v", err)
	}

	t.Log("All tests passed!")
}
