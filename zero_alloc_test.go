package logger

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

// TestFastHandler_ZeroAlloc 守护“纯序列化 0 分配”声明：
// FastHandler.Handle 在典型记录（消息 + 基础类型属性）上不得产生堆分配。
func TestFastHandler_ZeroAlloc(t *testing.T) {
	if raceEnabled {
		t.Skip("race 模式下分配数测量不可靠")
	}
	h := NewFastHandler(io.Discard, nil)
	ctx := context.Background()

	msgOnly := slog.NewRecord(time.Now(), slog.LevelInfo, "pure serial key=val", 0)
	withAttrs := slog.NewRecord(time.Now(), slog.LevelWarn, "with attrs", 0)
	withAttrs.AddAttrs(
		slog.String("k", "v"),
		slog.Int("n", 42),
		slog.Bool("b", true),
		slog.Float64("f", 3.14),
	)

	cases := []struct {
		name string
		rec  slog.Record
	}{
		{"msg-only", msgOnly},
		{"basic-attrs", withAttrs},
	}
	for _, tc := range cases {
		allocs := testing.AllocsPerRun(1000, func() {
			_ = h.Handle(ctx, tc.rec)
		})
		if allocs != 0 {
			t.Errorf("%s: allocs = %v, want 0", tc.name, allocs)
		}
	}
}
