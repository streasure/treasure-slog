package logger

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// captureWriter 捕获写入内容用于测试验证
type captureWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (cw *captureWriter) Write(p []byte) (int, error) {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	return cw.buf.Write(p)
}

func (cw *captureWriter) String() string {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	return cw.buf.String()
}

func (cw *captureWriter) Reset() {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	cw.buf.Reset()
}

func (cw *captureWriter) CountLines() int {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	return bytes.Count(cw.buf.Bytes(), []byte{'\n'})
}

// makeTestLogger 创建用于重定向测试的 logger
// 使用同步模式（asyncEnabled=false）确保日志立即写入，便于验证
func makeTestLogger(t *testing.T, level string) (*SLogger, *captureWriter) {
	t.Helper()
	cw := &captureWriter{}
	// 直接构造一个同步模式的 SLogger，使用 FastHandler
	s := &SLogger{
		cfg:          nil, // 不需要配置文件
		hooks:        []Hook{},
		wg:           &sync.WaitGroup{},
		asyncEnabled: false, // 同步模式
		usePool:      false,
		level:        &atomic.Int32{},
		levelVar:     &slog.LevelVar{},
		syncOnce:     &sync.Once{},
	}
	s.level.Store(int32(parseLevelString(level)))
	handler := NewFastHandler(cw, s.level)
	s.logger = slog.New(handler)
	return s, cw
}

func parseLevelString(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// TestRedirectFailure_SetLevelDynamic 验证 SetLevel 动态切换后日志是否真正输出
// 这是重定向失效的核心场景：SetLevel 后 handler 的 Enabled() 是否能感知到新级别
func TestRedirectFailure_SetLevelDynamic(t *testing.T) {
	tests := []struct {
		name      string
		initLevel string
		switchTo  string
		testLevel slog.Level
		shouldLog bool
	}{
		{"InfoToDebug_DebugShouldAppear", "info", "debug", slog.LevelDebug, true},
		{"InfoToError_InfoShouldDisappear", "info", "error", slog.LevelInfo, false},
		{"DebugToError_DebugShouldDisappear", "debug", "error", slog.LevelDebug, false},
		{"ErrorToDebug_AllShouldAppear", "error", "debug", slog.LevelDebug, true},
		{"WarnToInfo_WarnShouldAppear", "warn", "info", slog.LevelWarn, true},
		{"WarnToInfo_InfoShouldAppear", "warn", "info", slog.LevelInfo, true},
		{"InfoToWarn_DebugShouldDisappear", "info", "warn", slog.LevelDebug, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, cw := makeTestLogger(t, tt.initLevel)
			cw.Reset()

			ctx := context.Background()

			// 切换级别
			s.SetLevel(tt.switchTo)
			// 等待级别变更生效
			time.Sleep(10 * time.Millisecond)

			// 发送测试日志
			switch tt.testLevel {
			case slog.LevelDebug:
				s.Debug(ctx, "test-debug-msg key=%s", "value")
			case slog.LevelInfo:
				s.Info(ctx, "test-info-msg key=%s", "value")
			case slog.LevelWarn:
				s.Warn(ctx, "test-warn-msg key=%s", "value")
			case slog.LevelError:
				s.Error(ctx, "test-error-msg key=%s", "value")
			}

			// 同步模式，日志应立即写入
			content := cw.String()
			if tt.shouldLog && !strings.Contains(content, "test-") {
				t.Errorf("期望日志输出但未写入: level=%v, content=%q", tt.testLevel, content)
			}
			if !tt.shouldLog && strings.Contains(content, "test-") {
				t.Errorf("期望日志被过滤但已写入: level=%v, content=%q", tt.testLevel, content)
			}
		})
	}
}

// TestRedirectFailure_RapidSetLevel 快速连续切换级别，验证不会有竞态导致日志丢失或多余
func TestRedirectFailure_RapidSetLevel(t *testing.T) {
	s, cw := makeTestLogger(t, "info")

	ctx := context.Background()

	// 快速连续切换
	levels := []string{"debug", "error", "info", "warn", "debug", "error", "info"}
	for _, lvl := range levels {
		s.SetLevel(lvl)
	}

	// 最终级别是 info，debug 应被过滤
	s.SetLevel("info")
	cw.Reset()
	s.Debug(ctx, "should-not-appear")
	s.Info(ctx, "should-appear")
	s.Error(ctx, "should-appear")

	content := cw.String()
	if strings.Contains(content, "should-not-appear") {
		t.Errorf("debug 日志未被正确过滤: %s", content)
	}
	if !strings.Contains(content, "should-appear") {
		t.Errorf("info/error 日志未输出: %s", content)
	}
}

// TestRedirectFailure_DerivedLogger 验证 With() 派生的 logger 是否能感知到级别变更
func TestRedirectFailure_DerivedLogger(t *testing.T) {
	s, cw := makeTestLogger(t, "error")

	ctx := context.Background()

	// 派生 logger
	derived := s.With("component", "test-service")
	cw.Reset()

	// 初始级别 error，info 应被过滤
	derived.Info(ctx, "should-not-appear")
	if strings.Contains(cw.String(), "should-not-appear") {
		t.Errorf("派生 logger 的 info 未被过滤")
	}

	// 切换到 debug
	s.SetLevel("debug")
	time.Sleep(10 * time.Millisecond)
	cw.Reset()

	derived.Debug(ctx, "should-appear-now")
	if !strings.Contains(cw.String(), "should-appear-now") {
		t.Errorf("派生 logger 的 SetLevel 的 debug 日志未输出，重定向失败")
	}
	if !strings.Contains(cw.String(), "test-service") {
		t.Errorf("派生 logger 的固定字段未输出")
	}
}

// TestRedirectFailure_WithContext 验证 WithContext 派生的 logger 级别变更
func TestRedirectFailure_WithContext(t *testing.T) {
	s, cw := makeTestLogger(t, "warn")

	ctx := context.WithValue(context.Background(), ContextKeyRequestID, "req-123")
	derived := s.WithContext(ctx)
	cw.Reset()

	// 初始级别 warn，info 应被过滤
	derived.Info(context.Background(), "should-not-appear")
	if strings.Contains(cw.String(), "should-not-appear") {
		t.Errorf("WithContext 派生 logger 的 info 未被过滤")
	}

	// 切换到 debug
	s.SetLevel("debug")
	time.Sleep(10 * time.Millisecond)
	cw.Reset()

	derived.Debug(context.Background(), "should-appear")
	content := cw.String()
	if !strings.Contains(content, "should-appear") {
		t.Errorf("WithContext 派生 logger 的 SetLevel 的 debug 未输出")
	}
	if !strings.Contains(content, "req-123") {
		t.Errorf("WithContext 的 context 值未注入")
	}
}

// TestRedirectFailure_ConcurrentSetLevelAndLog 并发场景下 SetLevel + 日志写入
// 只在 debug/info 之间切换，确保 Info 日志始终能通过级别过滤
func TestRedirectFailure_ConcurrentSetLevelAndLog(t *testing.T) {
	s, cw := makeTestLogger(t, "info")

	ctx := context.Background()
	var wg sync.WaitGroup
	// 一个 goroutine 持续切换级别（只在 debug/info 间切换，确保 Info 始终通过）
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() { recover() }()
		levels := []string{"debug", "info"}
		for i := 0; i < 1000; i++ {
			s.SetLevel(levels[i%2])
		}
	}()

	// 多个 goroutine 持续写日志
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			defer func() { recover() }()
			for j := 0; j < 500; j++ {
				s.Info(ctx, "log-%d-%d worker=%d", id, j, id)
			}
		}(i)
	}

	wg.Wait()

	// 验证没有 panic，且有日志输出
	content := cw.String()
	if !strings.Contains(content, "log-") {
		t.Errorf("并发场景下无日志输出，可能重定向失效")
	}
	// 确保输出的是合法 JSON（每行以 { 开头）
	lines := strings.Split(strings.TrimSpace(content), "\n")
	for i, line := range lines {
		if len(line) > 0 && line[0] != '{' {
			t.Errorf("第%d 行不是合法JSON: %s", i, line[:min(50, len(line))])
		}
	}
}

// TestRedirectFailure_TextHandlerLevel 验证 TextHandler 模式下的级别动态切换
func TestRedirectFailure_TextHandlerLevel(t *testing.T) {
	cw := &captureWriter{}
	s := &SLogger{
		hooks:        []Hook{},
		wg:           &sync.WaitGroup{},
		asyncEnabled: false,
		usePool:      false,
		level:        &atomic.Int32{},
		levelVar:     &slog.LevelVar{},
		syncOnce:     &sync.Once{},
	}
	s.level.Store(int32(slog.LevelError))
	s.levelVar.Set(slog.LevelError)

	handler := slog.NewTextHandler(cw, &slog.HandlerOptions{
		Level: s.levelVar,
	})
	s.logger = slog.New(handler)

	ctx := context.Background()
	cw.Reset()
	s.Info(ctx, "should-not-appear")
	if strings.Contains(cw.String(), "should-not-appear") {
		t.Errorf("TextHandler 模式 info 未被过滤")
	}

	s.SetLevel("info")
	time.Sleep(10 * time.Millisecond)
	cw.Reset()
	s.Info(ctx, "should-appear")
	if !strings.Contains(cw.String(), "should-appear") {
		t.Errorf("TextHandler 模式 SetLevel 的 info 未输出，重定向失败")
	}
}

// TestRedirectFailure_AsyncMode 异步模式下验证级别切换
func TestRedirectFailure_AsyncMode(t *testing.T) {
	cw := &captureWriter{}

	// 构造异步模式 logger
	s := &SLogger{
		hooks:        []Hook{},
		wg:           &sync.WaitGroup{},
		asyncEnabled: true,
		usePool:      true,
		level:        &atomic.Int32{},
		levelVar:     &slog.LevelVar{},
		syncOnce:     &sync.Once{},
	}
	s.level.Store(int32(slog.LevelError))

	handler := NewFastHandler(cw, s.level)
	s.logger = slog.New(handler)
	s.ringBuf = newRingBuffer(10000)

	// 启动 worker
	s.workers = make([]*worker, 1)
	for i := range s.workers {
		s.workers[i] = &worker{
			id:     i,
			logger: s,
			stopCh: make(chan struct{}),
			wg:     s.wg,
		}
		s.workers[i].start()
	}

	ctx := context.Background()

	// 初始级别 error，info 应被过滤
	s.Info(ctx, "should-not-appear")
	time.Sleep(50 * time.Millisecond)
	if strings.Contains(cw.String(), "should-not-appear") {
		t.Errorf("异步模式 info 未被过滤")
	}

	// 切换到 debug
	s.SetLevel("debug")
	time.Sleep(50 * time.Millisecond)

	s.Debug(ctx, "should-appear-async")
	time.Sleep(100 * time.Millisecond)

	content := cw.String()
	if !strings.Contains(content, "should-appear-async") {
		t.Errorf("异步模式 SetLevel 的 debug 未输出，重定向失败")
	}

	// 清理
	s.Sync()
}

// TestRedirectFailure_SamplingHandler 验证 SamplingHandler 包装下级别切换
func TestRedirectFailure_SamplingHandler(t *testing.T) {
	cw := &captureWriter{}
	s := &SLogger{
		hooks:        []Hook{},
		wg:           &sync.WaitGroup{},
		asyncEnabled: false,
		usePool:      false,
		level:        &atomic.Int32{},
		levelVar:     &slog.LevelVar{},
		syncOnce:     &sync.Once{},
	}
	s.level.Store(int32(slog.LevelError))

	// 先用 FastHandler 再包 SamplingHandler
	fast := NewFastHandler(cw, s.level)
	handler := NewSamplingHandler(fast, SamplingOptions{
		Initial:    10000, // 足够大，不会采样
		Thereafter: 10000,
	})
	s.logger = slog.New(handler)

	ctx := context.Background()
	cw.Reset()
	s.Info(ctx, "should-not-appear")
	if strings.Contains(cw.String(), "should-not-appear") {
		t.Errorf("SamplingHandler 模式 info 未被过滤")
	}

	s.SetLevel("info")
	time.Sleep(10 * time.Millisecond)
	cw.Reset()
	s.Info(ctx, "should-appear")
	if !strings.Contains(cw.String(), "should-appear") {
		t.Errorf("SamplingHandler 模式 SetLevel 的 info 未输出，重定向失败")
	}
}

// TestRedirectFailure_NilSLogger nil logger 的 panic
func TestRedirectFailure_NilSLogger(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("nil SLogger 导致 panic: %v", r)
		}
	}()

	var s *SLogger
	s.SetLevel("debug")
	s.Info(context.Background(), "test")
	s.Debug(context.Background(), "test")
	s.Error(context.Background(), "test")
	_ = s.GetLevel()
	_ = s.Sync()
}

// TestRedirectFailure_MultiOutput 验证多输出场景下级别过滤
func TestRedirectFailure_MultiOutput(t *testing.T) {
	cw1 := &captureWriter{}
	cw2 := &captureWriter{}

	s := &SLogger{
		hooks:        []Hook{},
		wg:           &sync.WaitGroup{},
		asyncEnabled: false,
		usePool:      false,
		level:        &atomic.Int32{},
		levelVar:     &slog.LevelVar{},
		syncOnce:     &sync.Once{},
	}
	s.level.Store(int32(slog.LevelWarn))

	// 创建 multiWriter
	mw := io.MultiWriter(cw1, cw2)
	handler := NewFastHandler(mw, s.level)
	s.logger = slog.New(handler)

	ctx := context.Background()
	cw1.Reset()
	cw2.Reset()
	s.Info(ctx, "should-not-appear")
	if cw1.String() != "" || cw2.String() != "" {
		t.Errorf("多输出模式 info 未被过滤")
	}

	s.SetLevel("info")
	time.Sleep(10 * time.Millisecond)
	s.Info(ctx, "should-appear")
	if !strings.Contains(cw1.String(), "should-appear") {
		t.Errorf("多输出 writer1 未输出")
	}
	if !strings.Contains(cw2.String(), "should-appear") {
		t.Errorf("多输出 writer2 未输出")
	}
}
