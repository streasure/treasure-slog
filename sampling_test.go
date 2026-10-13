package logger

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func samplingRecord(level slog.Level, msg string) slog.Record {
	return slog.NewRecord(time.Now(), level, msg, 0)
}

// TestSamplingPerMessageIndependence 验证计数按 (级别, 消息) 独立：
// info 洪水不影响 error 额度（zap 语义下"被无关日志饿死"不可能发生）。
func TestSamplingPerMessageIndependence(t *testing.T) {
	cw := &captureWriter{}
	h := NewSamplingHandler(NewFastHandler(cw, nil), SamplingOptions{
		Initial:    2,
		Thereafter: 5,
		Tick:       time.Hour, // 测试期间不重置窗口
	})
	ctx := context.Background()

	// info-a：c1,c2（initial）放行，c5,c10（%5==0）放行 → 10 条保留 4
	for range 10 {
		if err := h.Handle(ctx, samplingRecord(slog.LevelInfo, "info-a")); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	}
	// err-b：独立计数，c1,c2（initial）放行，c3 丢弃 → 3 条保留 2
	for range 3 {
		if err := h.Handle(ctx, samplingRecord(slog.LevelError, "err-b")); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	}

	content := cw.String()
	if got := strings.Count(content, `"msg":"info-a"`); got != 4 {
		t.Errorf("info-a 应保留 4/10 条，got %d", got)
	}
	if got := strings.Count(content, `"msg":"err-b"`); got != 2 {
		t.Errorf("err-b 独立计数应保留 2/3 条，got %d", got)
	}
	if h.Sampled() != 6 || h.Dropped() != 7 {
		t.Errorf("stats: sampled=%d dropped=%d, want 6/7", h.Sampled(), h.Dropped())
	}
}

// TestSamplingTickReset 验证窗口到期后重新放行 initial 条（持续洪水每 tick 保留代表样本）。
func TestSamplingTickReset(t *testing.T) {
	cw := &captureWriter{}
	h := NewSamplingHandler(NewFastHandler(cw, nil), SamplingOptions{
		Initial:    1,
		Thereafter: 0, // 超出 initial 后窗口内全丢
		Tick:       20 * time.Millisecond,
	})
	ctx := context.Background()

	for range 3 {
		_ = h.Handle(ctx, samplingRecord(slog.LevelInfo, "flood"))
	}
	time.Sleep(50 * time.Millisecond) // 跨过至少两个窗口
	for range 3 {
		_ = h.Handle(ctx, samplingRecord(slog.LevelInfo, "flood"))
	}

	if got := strings.Count(cw.String(), `"msg":"flood"`); got != 2 {
		t.Errorf("跨窗口后应每窗口保留 1 条共 2 条，got %d", got)
	}
}

// TestSamplingThereafterZero 验证 thereafter=0 时窗口内超出 initial 全丢（zap 语义）。
func TestSamplingThereafterZero(t *testing.T) {
	cw := &captureWriter{}
	h := NewSamplingHandler(NewFastHandler(cw, nil), SamplingOptions{
		Initial:    1,
		Thereafter: 0,
		Tick:       time.Hour,
	})
	ctx := context.Background()

	for range 10 {
		_ = h.Handle(ctx, samplingRecord(slog.LevelInfo, "once"))
	}
	if got := strings.Count(cw.String(), `"msg":"once"`); got != 1 {
		t.Errorf("thereafter=0 应仅保留 initial 1 条，got %d", got)
	}
}

// TestSamplingLevelIndependence 验证同名消息在不同级别下计数独立（级别参与计数维度）。
func TestSamplingLevelIndependence(t *testing.T) {
	cw := &captureWriter{}
	h := NewSamplingHandler(NewFastHandler(cw, nil), SamplingOptions{
		Initial:    1,
		Thereafter: 0,
		Tick:       time.Hour,
	})
	ctx := context.Background()

	_ = h.Handle(ctx, samplingRecord(slog.LevelInfo, "boom"))
	_ = h.Handle(ctx, samplingRecord(slog.LevelError, "boom"))

	content := cw.String()
	if !strings.Contains(content, `"level":"INFO","msg":"boom"`) {
		t.Error("info boom 未保留")
	}
	if !strings.Contains(content, `"level":"ERROR","msg":"boom"`) {
		t.Error("error boom 未保留（与 info 计数应独立）")
	}
}

// TestSamplingRepresentativeRetention 验证同文洪水按 thereafter 保留代表样本（行为与 zap 对齐）。
func TestSamplingRepresentativeRetention(t *testing.T) {
	cw := &captureWriter{}
	h := NewSamplingHandler(NewFastHandler(cw, nil), SamplingOptions{
		Initial:    1,
		Thereafter: 5,
		Tick:       time.Hour,
	})
	ctx := context.Background()

	// c1（initial）放行；c5,c10,c15,c20（%5==0）放行 → 20 条保留 5
	for range 20 {
		_ = h.Handle(ctx, samplingRecord(slog.LevelWarn, "repeated"))
	}
	if got := strings.Count(cw.String(), `"msg":"repeated"`); got != 5 {
		t.Errorf("同文洪水应保留 5/20 条代表样本，got %d", got)
	}
}
