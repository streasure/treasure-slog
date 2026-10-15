package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"
)

// ===== 构造选项 =====

// sloggerOptions 收集 Option 注入的扩展配置
type sloggerOptions struct {
	writers       []io.Writer                       // 追加的自定义输出
	pipelineDecos []func(slog.Handler) slog.Handler // 管线级装饰（采样之前）
	coreDecos     []func(slog.Handler) slog.Handler // 输出级装饰（采样之后、序列化之前）
	hooks         []Hook                            // Hook 插件
	errHandler    func(error)                       // 写错误处理；nil = 默认打印 stderr
}

// Option 以函数选项方式扩展 logger.New / logger.NewFromConfig
type Option func(*sloggerOptions)

// WithWriter 追加自定义输出目标，与配置文件中的输出（文件/控制台/网络）并行写入
func WithWriter(w io.Writer) Option {
	return func(o *sloggerOptions) {
		if w != nil {
			o.writers = append(o.writers, w)
		}
	}
}

// WithHandlerDecorator 追加管线级装饰器：包装整条处理链（在采样之前），
// 适合全量日志的指标统计等。多个装饰器按注册顺序从外到内依次执行。
// 装饰逻辑可基于 Decorate 快速实现。
func WithHandlerDecorator(f func(slog.Handler) slog.Handler) Option {
	return func(o *sloggerOptions) {
		if f != nil {
			o.pipelineDecos = append(o.pipelineDecos, f)
		}
	}
}

// WithCoreDecorator 追加输出级装饰器：只包装主输出 handler（在采样、Hook 之外侧），
// 适合脱敏、字段重写等针对最终输出的处理。多个装饰器按注册顺序从外到内依次执行。
func WithCoreDecorator(f func(slog.Handler) slog.Handler) Option {
	return func(o *sloggerOptions) {
		if f != nil {
			o.coreDecos = append(o.coreDecos, f)
		}
	}
}

// WithHooks 注册 Hook 插件（可多个）。Hook 仅对通过采样的日志触发，语义见 Hook 文档
func WithHooks(hooks ...Hook) Option {
	return func(o *sloggerOptions) {
		for _, h := range hooks {
			if h != nil {
				o.hooks = append(o.hooks, h)
			}
		}
	}
}

// WithErrorHandler 设置写错误处理函数（默认打印到 stderr）。传 nil 恢复默认行为
func WithErrorHandler(f func(error)) Option {
	return func(o *sloggerOptions) {
		o.errHandler = f
	}
}

// ===== Hook 插件 =====

// Event 是 Hook 收到的日志事件快照，仅在 Hook 调用期间有效，不得保留引用后复用
type Event struct {
	Time    time.Time       // 日志时间戳
	Level   slog.Level      // 日志级别
	Message string          // 日志消息
	Attrs   []slog.Attr     // 本条日志的 record 属性（With/WithContext 注入 + 调用点参数，已按 key 去重），不含 WithGroup 分组前缀
	Ctx     context.Context // 同步路径为调用方 ctx；异步路径为入队时捕获的 ctx
}

// Hook 日志插件接口。注册后仅对通过采样、即将序列化写入的日志触发：
//   - BeforeWrite：写入前
//   - AfterWrite：写入后，err 为序列化/写入结果（写失败时非 nil）
//
// 每个 Hook 调用独立 panic 隔离，单个 Hook 崩溃不影响日志输出与其他 Hook。
// 同步模式下 Hook 在调用方 goroutine 执行；异步模式下在 worker goroutine 执行。
// 实现方通常嵌入 HookFuncs，按需覆写。
type Hook interface {
	BeforeWrite(e *Event)
	AfterWrite(e *Event, err error)
}

// HookFuncs 是 Hook 的函数适配器：嵌入后按需赋值 OnBefore/OnAfter，零值安全
type HookFuncs struct {
	OnBefore func(*Event)
	OnAfter  func(*Event, error)
}

func (h HookFuncs) BeforeWrite(e *Event) {
	if h.OnBefore != nil {
		h.OnBefore(e)
	}
}

func (h HookFuncs) AfterWrite(e *Event, err error) {
	if h.OnAfter != nil {
		h.OnAfter(e, err)
	}
}

// ===== Decorate 通用装饰器 =====

// HandleFunc 是 Decorate 的处理函数：执行任意逻辑后调用 next.Handle 继续管线
type HandleFunc func(ctx context.Context, r slog.Record, next slog.Handler) error

// Decorate 以自定义 Handle 逻辑包装 next：WithAttrs/WithGroup/Enabled 自动转发到 next。
// 脱敏、指标、日志转发、写后回调等二次开发逻辑都可基于它实现，无需修改本库。
// 典型用法：注册为 WithHandlerDecorator（采样之前）或 WithCoreDecorator（采样之后），
// 也可直接包装任意 slog.Handler（含本库 Handler() 返回的完整处理链）。
func Decorate(next slog.Handler, fn HandleFunc) slog.Handler {
	return &decoratedHandler{next: next, fn: fn}
}

type decoratedHandler struct {
	next slog.Handler
	fn   HandleFunc
}

func (h *decoratedHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *decoratedHandler) Handle(ctx context.Context, r slog.Record) error {
	if h.fn == nil {
		return h.next.Handle(ctx, r)
	}
	return h.fn(ctx, r, h.next)
}

func (h *decoratedHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &decoratedHandler{next: h.next.WithAttrs(attrs), fn: h.fn}
}

func (h *decoratedHandler) WithGroup(name string) slog.Handler {
	return &decoratedHandler{next: h.next.WithGroup(name), fn: h.fn}
}

// ===== hooksHandler / writeErrorHandler =====

// hooksHandler 位于采样内侧、序列化外侧：仅通过采样的日志触发 Hook（写前/写后）。
// 累积 With/WithContext 经 WithAttrs 注入的属性，Hook 可见完整字段视图。
type hooksHandler struct {
	next     slog.Handler
	hooks    []Hook
	preAttrs []slog.Attr // With/WithContext 注入的属性（链上依次累积）
}

func newHooksHandler(next slog.Handler, hooks []Hook) slog.Handler {
	if len(hooks) == 0 || next == nil {
		return next
	}
	hs := make([]Hook, len(hooks))
	copy(hs, hooks)
	return &hooksHandler{next: next, hooks: hs}
}

func (h *hooksHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *hooksHandler) Handle(ctx context.Context, r slog.Record) error {
	e := &Event{
		Time:    r.Time,
		Level:   r.Level,
		Message: r.Message,
		Ctx:     ctx,
	}
	e.Attrs = make([]slog.Attr, 0, len(h.preAttrs)+4)
	e.Attrs = append(e.Attrs, h.preAttrs...)
	r.Attrs(func(a slog.Attr) bool {
		e.Attrs = append(e.Attrs, a)
		return true
	})
	endHook := timing.startTimer(&timing.hookExecNS)
	for _, hk := range h.hooks {
		if hk == nil {
			continue
		}
		callHookBefore(hk, e)
	}
	err := h.next.Handle(ctx, r)
	for _, hk := range h.hooks {
		if hk == nil {
			continue
		}
		callHookAfter(hk, e, err)
	}
	endHook()
	return err
}

func (h *hooksHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	nh := &hooksHandler{next: h.next.WithAttrs(attrs), hooks: h.hooks}
	nh.preAttrs = make([]slog.Attr, 0, len(h.preAttrs)+len(attrs))
	nh.preAttrs = append(nh.preAttrs, h.preAttrs...)
	nh.preAttrs = append(nh.preAttrs, attrs...)
	return nh
}

func (h *hooksHandler) WithGroup(name string) slog.Handler {
	return &hooksHandler{next: h.next.WithGroup(name), hooks: h.hooks, preAttrs: h.preAttrs}
}

func callHookBefore(h Hook, e *Event) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[treasure-slog] hook BeforeWrite panic recovered: %v\n", r)
		}
	}()
	h.BeforeWrite(e)
}

func callHookAfter(h Hook, e *Event, err error) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[treasure-slog] hook AfterWrite panic recovered: %v\n", r)
		}
	}()
	h.AfterWrite(e, err)
}

// writeErrorHandler 将下游写错误交给 onError（nil 时打印 stderr），保持“错误打到 stderr、不向上冒泡”的行为
type writeErrorHandler struct {
	handler slog.Handler
	onError func(error)
}

func newWriteErrorHandler(h slog.Handler, onError func(error)) slog.Handler {
	return &writeErrorHandler{handler: h, onError: onError}
}

func (h *writeErrorHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

func (h *writeErrorHandler) Handle(ctx context.Context, r slog.Record) error {
	if err := h.handler.Handle(ctx, r); err != nil {
		if h.onError != nil {
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						fmt.Fprintf(os.Stderr, "[treasure-slog] error handler panic recovered: %v\n", rec)
					}
				}()
				h.onError(err)
			}()
		} else {
			fmt.Fprintf(os.Stderr, "[treasure-slog] handler error: %v\n", err)
		}
	}
	return nil
}

func (h *writeErrorHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &writeErrorHandler{handler: h.handler.WithAttrs(attrs), onError: h.onError}
}

func (h *writeErrorHandler) WithGroup(name string) slog.Handler {
	return &writeErrorHandler{handler: h.handler.WithGroup(name), onError: h.onError}
}
