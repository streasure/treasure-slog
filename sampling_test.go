package logger

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// TestSamplingHandlerErrorNotSampled 验证 ERROR 级别绕过采样：
// 1. 无论 initial/thereafter 为何值，ERROR 全部放行；
// 2. ERROR 不消耗采样计数，info/debug 的采样密度不受故障日志风暴影响。
func TestSamplingHandlerErrorNotSampled(t *testing.T) {
	cw := &captureWriter{}
	h := NewSamplingHandler(NewFastHandler(cw, nil), SamplingOptions{Initial: 1, Thereafter: 1000})
	ctx := context.Background()

	// initial=1, thereafter=1000：前 14 条 INFO 中应仅第 1 条放行
	for i := range 14 {
		r := slog.NewRecord(time.Now(), slog.LevelInfo, fmt.Sprintf("info-%d", i), 0)
		if err := h.Handle(ctx, r); err != nil {
			t.Fatalf("Handle info: %v", err)
		}
	}
	// 穿插 ERROR：应全部放行
	for i := range 5 {
		r := slog.NewRecord(time.Now(), slog.LevelError, fmt.Sprintf("err-%d", i), 0)
		if err := h.Handle(ctx, r); err != nil {
			t.Fatalf("Handle error: %v", err)
		}
	}

	content := cw.String()
	if got := strings.Count(content, `"msg":"err-`); got != 5 {
		t.Errorf("ERROR 记录应全部保留，got %d/5", got)
	}
	if got := strings.Count(content, `"msg":"info-`); got != 1 {
		t.Errorf("INFO 应按采样仅保留 1 条（ERROR 不占用计数），got %d", got)
	}
}

// TestSamplingHandlerInfoSampled 验证 info 仍按 initial/thereafter 采样（行为不回归）。
func TestSamplingHandlerInfoSampled(t *testing.T) {
	cw := &captureWriter{}
	h := NewSamplingHandler(NewFastHandler(cw, nil), SamplingOptions{Initial: 3, Thereafter: 4})
	ctx := context.Background()

	// 计数 1..3（initial）放行，之后 (count-3)%4==0 即 7,11,15 放行 → 15 条中 6 条
	for i := range 15 {
		r := slog.NewRecord(time.Now(), slog.LevelInfo, fmt.Sprintf("info-%d", i), 0)
		if err := h.Handle(ctx, r); err != nil {
			t.Fatalf("Handle info: %v", err)
		}
	}
	if got := strings.Count(cw.String(), `"msg":"info-`); got != 6 {
		t.Errorf("INFO 采样应保留 6/15 条，got %d", got)
	}
}
