package logger

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// newTestBufSLogger 构造写入 buf 的同步 SLogger
func newTestBufSLogger(buf *bytes.Buffer) *SLogger {
	level := &atomic.Int32{}
	level.Store(int32(slog.LevelInfo))
	h := NewFastHandler(buf, level)
	return &SLogger{
		logger:   slog.New(h),
		handler:  h,
		level:    level,
		levelVar: &slog.LevelVar{},
	}
}

// dedupCaptureHook 捕获最近一次收到的 Hook 事件（嵌入 HookFuncs，只需 BeforeWrite）
type dedupCaptureHook struct {
	HookFuncs
	got Event
}

func (h *dedupCaptureHook) BeforeWrite(e *Event) {
	h.got = *e
	h.got.Attrs = append([]slog.Attr(nil), e.Attrs...)
}

func hasAttr(attrs []slog.Attr, key, want string) bool {
	for _, a := range attrs {
		if a.Key == key && fmt.Sprint(a.Value.Any()) == want {
			return true
		}
	}
	return false
}

// 基础 logger：context 字段正常提取（无预注入时不受去重影响）
func TestContextExtract_BaseLogger(t *testing.T) {
	var buf bytes.Buffer
	s := newTestBufSLogger(&buf)
	ctx := context.WithValue(context.Background(), ContextKeyRequestID, "req-1")
	s.Info(ctx, "hello")
	out := buf.String()
	if strings.Count(out, `"request_id"`) != 1 {
		t.Errorf("request_id 应出现 1 次: %s", out)
	}
	if !strings.Contains(out, `"req-1"`) {
		t.Errorf("request_id 值丢失: %s", out)
	}
}

// 派生 logger + 同一 ctx：同一 key 至多输出一次（回归：曾输出重复 key），
// 且 hook 仍收到完整 context 字段（With 预注入 + record 合并视图）
func TestContextExtract_DerivedNoDuplicate(t *testing.T) {
	var buf bytes.Buffer
	s := newTestBufSLogger(&buf)
	hook := &dedupCaptureHook{}
	s = s.AddHook(hook).(*SLogger)

	ctx := context.WithValue(context.Background(), ContextKeyRequestID, "req-1")
	ctx = context.WithValue(ctx, ContextKeyTraceID, "trace-1")

	derived := s.WithContext(ctx)
	derived.Info(ctx, "hello")

	out := buf.String()
	if strings.Count(out, `"request_id"`) != 1 {
		t.Errorf("request_id 出现 %d 次（应为 1）: %s", strings.Count(out, `"request_id"`), out)
	}
	if strings.Count(out, `"trace_id"`) != 1 {
		t.Errorf("trace_id 出现 %d 次（应为 1）: %s", strings.Count(out, `"trace_id"`), out)
	}
	if !hasAttr(hook.got.Attrs, "request_id", "req-1") || !hasAttr(hook.got.Attrs, "trace_id", "trace-1") {
		t.Errorf("hook 应收到完整 context 字段: %+v", hook.got.Attrs)
	}
}

// 派生 logger + 空 ctx：预注入字段仍输出一次
func TestContextExtract_DerivedEmptyCtx(t *testing.T) {
	var buf bytes.Buffer
	s := newTestBufSLogger(&buf)
	ctx := context.WithValue(context.Background(), ContextKeyRequestID, "req-1")
	derived := s.WithContext(ctx)
	derived.Info(context.Background(), "hello")
	out := buf.String()
	if strings.Count(out, `"request_id"`) != 1 {
		t.Errorf("request_id 应出现 1 次: %s", out)
	}
}

// With() 自带同名 key 与 ctx 提取冲突时，预注入值保留且不重复输出
func TestContextExtract_WithKeyConflict(t *testing.T) {
	var buf bytes.Buffer
	s := newTestBufSLogger(&buf)
	withLog := s.With("request_id", "pinned")
	ctx := context.WithValue(context.Background(), ContextKeyRequestID, "req-ctx")
	withLog.Info(ctx, "hello")
	out := buf.String()
	if strings.Count(out, `"request_id"`) != 1 {
		t.Errorf("request_id 出现 %d 次（应为 1）: %s", strings.Count(out, `"request_id"`), out)
	}
	if !strings.Contains(out, `"pinned"`) {
		t.Errorf("预注入值应保留: %s", out)
	}
}

// 异步路径：派生 logger + 同一 ctx，文件输出无重复 key
func TestContextExtract_AsyncDerivedNoDuplicate(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")
	logPath := filepath.ToSlash(filepath.Join(dir, "out.log"))
	yaml := fmt.Sprintf(`log:
  level: info
  async:
    enabled: true
    buffer_size: 1000
  console:
    enabled: false
  file:
    enabled: true
    path: %s
`, logPath)
	if err := os.WriteFile(cfgPath, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	l, err := New(cfgPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.WithValue(context.Background(), ContextKeyRequestID, "req-async")
	derived := l.WithContext(ctx)
	const n = 10
	for i := range n {
		derived.Info(ctx, "msg %d", i)
	}
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	data, err := os.ReadFile(filepath.FromSlash(logPath))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	out := string(data)
	if got := strings.Count(out, `"request_id"`); got != n {
		t.Errorf("request_id 出现 %d 次（应为 %d，每行恰好 1 次）", got, n)
	}
	if got := strings.Count(out, "\n"); got != n {
		t.Errorf("日志行数 %d，want %d", got, n)
	}
}
