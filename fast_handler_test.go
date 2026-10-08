package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"
)

// serializeWithAttrs 用 FastHandler 序列化单条带属性的记录
func serializeWithAttrs(t *testing.T, attrs ...slog.Attr) string {
	t.Helper()
	var buf bytes.Buffer
	h := NewFastHandler(&buf, nil)
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0)
	r.AddAttrs(attrs...)
	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	return buf.String()
}

// NaN/Inf 输出为字符串，整行必须仍是合法 JSON（回归：曾直接 AppendFloat 产生非法 JSON）
func TestFastHandler_NonFiniteFloats(t *testing.T) {
	cases := []struct {
		name string
		v    float64
		want string
	}{
		{"NaN", math.NaN(), `"NaN"`},
		{"+Inf", math.Inf(1), `"+Inf"`},
		{"-Inf", math.Inf(-1), `"-Inf"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := serializeWithAttrs(t, slog.Float64("f", tc.v))
			if !strings.Contains(out, tc.want) {
				t.Errorf("输出缺少 %s: %s", tc.want, out)
			}
			if !json.Valid([]byte(strings.TrimSuffix(out, "\n"))) {
				t.Errorf("非有限浮点产生非法 JSON: %s", out)
			}
		})
	}
}

// 普通浮点仍为裸数字
func TestFastHandler_FiniteFloat(t *testing.T) {
	out := serializeWithAttrs(t, slog.Float64("f", 1.5))
	if !strings.Contains(out, `"f":1.5`) {
		t.Errorf("有限浮点应为裸数字: %s", out)
	}
	if !json.Valid([]byte(strings.TrimSuffix(out, "\n"))) {
		t.Errorf("非法 JSON: %s", out)
	}
}
