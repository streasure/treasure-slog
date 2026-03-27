package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"

	"treasure-slog/internal/config"
)

// Hook 定义日志钩子接口
type Hook interface {
	// Run 在记录日志前执行
	Run(msg string, level string, args ...any)
}

// Logger 接口定义与 zap 类似的日志方法
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
	With(args ...any) Logger
	WithContext(ctx context.Context) Logger
	AddHook(hook Hook) Logger
	Sync() error
}

// SLogger 是基于 slog 实现的 Logger
type SLogger struct {
	logger *slog.Logger
	hooks  []Hook
	// 用于文件轮转的 logger，用于 Graceful Shutdown
	fileLogger *lumberjack.Logger
}

// 全局日志单例
var (
	globalLogger Logger
	once         sync.Once
)

// New 创建一个新的日志记录器
func New(configPath string) (Logger, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config error: %w", err)
	}

	return newLogger(cfg)
}

// newLogger 创建日志记录器
func newLogger(cfg *config.Config) (Logger, error) {
	// 确保日志目录存在
	if cfg.Log.File.Enabled {
		logDir := filepath.Dir(cfg.Log.File.Path)
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return nil, fmt.Errorf("create log directory error: %w", err)
		}
	}

	// 配置输出
	var writers []io.Writer

	// 控制台输出
	writers = append(writers, os.Stdout)

	// 文件输出
	var fileLogger *lumberjack.Logger
	if cfg.Log.File.Enabled {
		fileLogger = &lumberjack.Logger{
			Filename:   cfg.Log.File.Path,
			MaxSize:    cfg.Log.File.Rotate.MaxSize,
			MaxBackups: cfg.Log.File.Rotate.MaxBackups,
			MaxAge:     cfg.Log.File.Rotate.MaxAge,
			Compress:   true,
		}
		writers = append(writers, fileLogger)
	}

	// 创建多输出
	multiWriter := io.MultiWriter(writers...)

	// 配置日志级别
	var slogLevel slog.Level
	switch cfg.Log.Level {
	case "debug":
		slogLevel = slog.LevelDebug
	case "info":
		slogLevel = slog.LevelInfo
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}

	// 创建日志处理器
	handler := slog.NewJSONHandler(multiWriter, &slog.HandlerOptions{
		Level: slogLevel,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				if t, ok := a.Value.Any().(time.Time); ok {
					a.Value = slog.StringValue(t.Format(time.RFC3339))
				}
			}
			return a
		},
	})

	// 配置日志采样（当前 Go 版本不支持 slog 内置采样，暂时跳过）
	// if cfg.Log.Sampling.Enabled {
	// 	handler = slog.NewSamplingHandler(handler, &slog.SamplingOptions{
	// 		Initial:    cfg.Log.Sampling.Initial,
	// 		Thereafter: cfg.Log.Sampling.Thereafter,
	// 	})
	// }

	// 创建日志记录器
	logger := slog.New(handler)

	return &SLogger{logger: logger, hooks: []Hook{}, fileLogger: fileLogger}, nil
}

// GetLogger 获取全局日志单例
func GetLogger() Logger {
	once.Do(func() {
		var err error
		globalLogger, err = New("configs/config.yaml")
		if err != nil {
			// 如果配置文件加载失败，使用默认配置
			defaultConfig := &config.Config{
				Log: config.LogConfig{
					Level:  "info",
					Format: "json",
					File: config.FileConfig{
						Enabled: false,
					},
					Stacktrace: config.StackConfig{
						Enabled: true,
						Level:   "error",
					},
					Sampling: config.SamplingConfig{
						Enabled:    false,
						Initial:    100,
						Thereafter: 10,
					},
				},
			}
			globalLogger, _ = newLogger(defaultConfig)
		}
	})
	return globalLogger
}

// Debug 记录调试级别日志
func (l *SLogger) Debug(msg string, args ...any) {
	// 执行钩子
	for _, hook := range l.hooks {
		hook.Run(msg, "debug", args...)
	}
	l.logger.Debug(msg, args...)
}

// Info 记录信息级别日志
func (l *SLogger) Info(msg string, args ...any) {
	// 执行钩子
	for _, hook := range l.hooks {
		hook.Run(msg, "info", args...)
	}
	l.logger.Info(msg, args...)
}

// Warn 记录警告级别日志
func (l *SLogger) Warn(msg string, args ...any) {
	// 执行钩子
	for _, hook := range l.hooks {
		hook.Run(msg, "warn", args...)
	}
	l.logger.Warn(msg, args...)
}

// Error 记录错误级别日志
func (l *SLogger) Error(msg string, args ...any) {
	// 执行钩子
	for _, hook := range l.hooks {
		hook.Run(msg, "error", args...)
	}

	// 添加堆栈追踪
	if len(args) > 0 {
		if _, ok := args[len(args)-1].(error); ok {
			stackTrace := getStackTrace()
			args = append(args, "stacktrace", stackTrace)
		}
	} else {
		stackTrace := getStackTrace()
		args = append(args, "stacktrace", stackTrace)
	}
	l.logger.Error(msg, args...)
}

// With 添加键值对到日志记录器
func (l *SLogger) With(args ...any) Logger {
	return &SLogger{logger: l.logger.With(args...), hooks: l.hooks, fileLogger: l.fileLogger}
}

// WithContext 添加上下文到日志记录器，并自动注入 context 中的信息
func (l *SLogger) WithContext(ctx context.Context) Logger {
	// 从 context 中提取信息
	args := extractContextInfo(ctx)
	// 创建新的 logger，添加 context 信息
	newLogger := l.logger.With("context", ctx)
	if len(args) > 0 {
		newLogger = newLogger.With(args...)
	}
	return &SLogger{logger: newLogger, hooks: l.hooks, fileLogger: l.fileLogger}
}

// extractContextInfo 从 context 中提取信息
func extractContextInfo(ctx context.Context) []any {
	var args []any
	// 提取常见的 context 信息
	if requestID := ctx.Value("request_id"); requestID != nil {
		args = append(args, "request_id", requestID)
	}
	if userID := ctx.Value("user_id"); userID != nil {
		args = append(args, "user_id", userID)
	}
	if spanID := ctx.Value("span_id"); spanID != nil {
		args = append(args, "span_id", spanID)
	}
	return args
}

// AddHook 添加钩子到日志记录器
func (l *SLogger) AddHook(hook Hook) Logger {
	newHooks := make([]Hook, len(l.hooks)+1)
	copy(newHooks, l.hooks)
	newHooks[len(l.hooks)] = hook
	return &SLogger{logger: l.logger, hooks: newHooks, fileLogger: l.fileLogger}
}

// Sync 同步日志，实现 Graceful Shutdown
func (l *SLogger) Sync() error {
	// 关闭文件 logger，实现 Graceful Shutdown
	if l.fileLogger != nil {
		return l.fileLogger.Close()
	}
	// slog 没有 Sync 方法，这里返回 nil
	return nil
}

// getStackTrace 获取堆栈追踪信息
func getStackTrace() string {
	stack := make([]byte, 1024*1024)
	n := runtime.Stack(stack, false)
	return string(stack[:n])
}

// Recover 恢复 panic 并记录日志
func Recover() {
	if r := recover(); r != nil {
		stackTrace := getStackTrace()
		GetLogger().Error("panic recovered", "recover", r, "stacktrace", stackTrace)
	}
}
