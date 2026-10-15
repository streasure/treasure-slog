package logger

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/streasure/treasure-slog/config"
)

// syncBufCfg 构造全同步、无内置输出的内存配置
func syncBufCfg() *config.Config {
	cfg := &config.Config{}
	cfg.Log.Async.Enabled = false
	cfg.Log.Async.EnabledSet = true // 显式同步
	cfg.Log.Sampling.Enabled = false
	return cfg
}

// errWriter 恒定写失败的 writer
type errWriter struct{}

func (errWriter) Write(p []byte) (int, error) { return 0, errors.New("write failed") }

// NewFromConfig nil 配置等价于全默认，不 panic
func TestNewFromConfig_NilDefaults(t *testing.T) {
	l, err := NewFromConfig(nil, WithErrorHandler(func(error) {}))
	if err != nil {
		t.Fatalf("NewFromConfig(nil): %v", err)
	}
	l.Info(context.Background(), "hello")
	_ = l.Sync()
}

// WithWriter 追加自定义输出
func TestWithWriter(t *testing.T) {
	var buf bytes.Buffer
	l, err := NewFromConfig(syncBufCfg(), WithWriter(&buf))
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	l.Info(context.Background(), "custom writer msg")
	if !strings.Contains(buf.String(), "custom writer msg") {
		t.Errorf("自定义 writer 未收到输出: %q", buf.String())
	}
}

// WithHooks：Before/After 触发、Event 内容、AfterWrite err 为 nil
func TestWithHooks_BeforeAfter(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	var before, after int
	var got Event
	var gotErr error
	hook := HookFuncs{
		OnBefore: func(e *Event) {
			mu.Lock()
			before++
			got = *e
			got.Attrs = append([]slog.Attr(nil), e.Attrs...)
			mu.Unlock()
		},
		OnAfter: func(e *Event, err error) {
			mu.Lock()
			after++
			gotErr = err
			mu.Unlock()
		},
	}
	l, err := NewFromConfig(syncBufCfg(), WithWriter(&buf), WithHooks(hook))
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	l.With("k", "v").Info(context.Background(), "hooked msg")
	if before != 1 || after != 1 {
		t.Errorf("hook 调用次数 before=%d after=%d, want 1/1", before, after)
	}
	if got.Message != "hooked msg" || got.Level != slog.LevelInfo {
		t.Errorf("Event 内容错误: %+v", got)
	}
	if !hasAttr(got.Attrs, "k", "v") {
		t.Errorf("Event.Attrs 缺少 With 注入参数: %+v", got.Attrs)
	}
	if gotErr != nil {
		t.Errorf("AfterWrite err 应为 nil, got %v", gotErr)
	}
	if !strings.Contains(buf.String(), "hooked msg") {
		t.Errorf("日志未写出: %q", buf.String())
	}
}

// Hook 仅对通过采样的日志触发（采样全丢时 hook 不触发）
func TestHooks_PostSampling(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	var calls int
	hook := HookFuncs{OnBefore: func(e *Event) {
		mu.Lock()
		calls++
		mu.Unlock()
	}}

	cfg := syncBufCfg()
	cfg.Log.Sampling.Enabled = true
	cfg.Log.Sampling.Initial = 0
	cfg.Log.Sampling.InitialSet = true // 显式 0：不预放行
	cfg.Log.Sampling.Thereafter = 0
	cfg.Log.Sampling.ThereafterSet = true // 超出后全丢

	l, err := NewFromConfig(cfg, WithWriter(&buf), WithHooks(hook))
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	for i := 0; i < 10; i++ {
		l.Info(context.Background(), "dropped msg")
	}
	if calls != 0 {
		t.Errorf("采样全丢时 hook 不应触发, calls=%d", calls)
	}
	if buf.Len() != 0 {
		t.Errorf("采样全丢时不应有输出: %q", buf.String())
	}

	sampled, dropped := l.SamplingStats()
	if dropped == 0 || sampled != 0 {
		t.Errorf("采样统计错误 sampled=%d dropped=%d", sampled, dropped)
	}
}

// AddHook 重建链后 With 预注入属性仍可见（withArgs 重放）
func TestAddHook_RebuildPreservesWithAttrs(t *testing.T) {
	var buf bytes.Buffer
	l, err := NewFromConfig(syncBufCfg(), WithWriter(&buf))
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	withLog := l.With("user_id", "u-1")
	hook := &dedupCaptureHook{}
	hooked := withLog.AddHook(hook)

	hooked.Info(context.Background(), "after rebuild")
	if !hasAttr(hook.got.Attrs, "user_id", "u-1") {
		t.Errorf("AddHook 重建后 With 属性丢失: %+v", hook.got.Attrs)
	}
	if !strings.Contains(buf.String(), "u-1") {
		t.Errorf("重建后输出丢失 With 属性: %q", buf.String())
	}
}

// WithErrorHandler 替换默认 stderr 写错误处理
func TestWithErrorHandler(t *testing.T) {
	var got error
	var mu sync.Mutex
	l, err := NewFromConfig(syncBufCfg(),
		WithWriter(errWriter{}),
		WithErrorHandler(func(e error) {
			mu.Lock()
			got = e
			mu.Unlock()
		}),
	)
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	l.Info(context.Background(), "will fail")
	mu.Lock()
	defer mu.Unlock()
	if got == nil || !strings.Contains(got.Error(), "write failed") {
		t.Errorf("ErrorHandler 未收到写错误: %v", got)
	}
}

// Decorate 装饰器：Handle 自定义逻辑 + WithAttrs 转发
func TestDecorate(t *testing.T) {
	var buf bytes.Buffer
	var seen []string
	dec := WithHandlerDecorator(func(next slog.Handler) slog.Handler {
		return Decorate(next, func(ctx context.Context, r slog.Record, next slog.Handler) error {
			seen = append(seen, r.Message)
			return next.Handle(ctx, r)
		})
	})
	l, err := NewFromConfig(syncBufCfg(), WithWriter(&buf), dec)
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	derived := l.With("a", 1) // WithAttrs 转发不应破坏链
	derived.Info(context.Background(), "decorated")
	if len(seen) != 1 || seen[0] != "decorated" {
		t.Errorf("管线装饰器未捕获记录: %v", seen)
	}
	if !strings.Contains(buf.String(), "decorated") {
		t.Errorf("装饰后输出丢失: %q", buf.String())
	}
}

// 管线级装饰器在采样之前（全量可见），输出级装饰器在采样之后
func TestDecoratorSamplingPlacement(t *testing.T) {
	var buf bytes.Buffer
	var pipelineCalls, coreCalls int
	cfg := syncBufCfg()
	cfg.Log.Sampling.Enabled = true
	cfg.Log.Sampling.Initial = 0
	cfg.Log.Sampling.InitialSet = true
	cfg.Log.Sampling.Thereafter = 0
	cfg.Log.Sampling.ThereafterSet = true

	l, err := NewFromConfig(cfg,
		WithWriter(&buf),
		WithHandlerDecorator(func(next slog.Handler) slog.Handler {
			return Decorate(next, func(ctx context.Context, r slog.Record, next slog.Handler) error {
				pipelineCalls++
				return next.Handle(ctx, r)
			})
		}),
		WithCoreDecorator(func(next slog.Handler) slog.Handler {
			return Decorate(next, func(ctx context.Context, r slog.Record, next slog.Handler) error {
				coreCalls++
				return next.Handle(ctx, r)
			})
		}),
	)
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	l.Info(context.Background(), "dropped by sampling")
	if pipelineCalls != 1 {
		t.Errorf("管线装饰器应看到全部日志: calls=%d", pipelineCalls)
	}
	if coreCalls != 0 {
		t.Errorf("输出级装饰器不应看到采样丢弃的日志: calls=%d", coreCalls)
	}
}

// Handler() 返回完整链，可外部复用
func TestHandlerAccessor(t *testing.T) {
	var buf bytes.Buffer
	l, err := NewFromConfig(syncBufCfg(), WithWriter(&buf))
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	h := l.Handler()
	if h == nil {
		t.Fatal("Handler() 不应为 nil")
	}
	if !h.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Handler().Enabled 应放行 info")
	}
	// 外部直接经 Handler 记录（slog 兼容）
	slog.New(h).Info("external slog")
	if !strings.Contains(buf.String(), "external slog") {
		t.Errorf("Handler() 外部复用失败: %q", buf.String())
	}
}

// HookFuncs 零值安全
func TestHookFuncs_ZeroValue(t *testing.T) {
	var h HookFuncs
	h.BeforeWrite(&Event{}) // 不 panic
	h.AfterWrite(&Event{}, nil)
}
