package logger

import (
	"context"
	"math/rand"
	"testing"
	"time"
)

// TestAppendRFC3339NanoMatchesStdlib 验证手写格式化器与
// t.Format(time.RFC3339Nano) 在各类时间边界下逐字节一致
func TestAppendRFC3339NanoMatchesStdlib(t *testing.T) {
	locSH, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		locSH = time.FixedZone("CST", 8*3600)
	}
	locNY, err := time.LoadLocation("America/New_York")
	if err != nil {
		locNY = time.FixedZone("EST", -5*3600)
	}

	cases := []time.Time{
		time.Unix(0, 0).UTC(),                                                         // 纳秒为 0（无小数段）
		time.Unix(1700000000, 0),                                                      // 本地时区，纳秒 0
		time.Unix(1700000000, 405000000),                                              // 尾零裁剪 .405
		time.Unix(1700000000, 123456789),                                              // 9 位满精度
		time.Unix(1700000000, 100000000),                                              // 仅首位非零 .1
		time.Unix(1700000000, 999999999),                                              // 最大纳秒
		time.Date(2024, 2, 29, 23, 59, 59, 999999999, time.UTC),                       // 闰日 + 日末
		time.Date(2024, 1, 1, 0, 0, 0, 1, time.UTC),                                   // 年初 + 最小纳秒
		time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC),                                      // 最小年份
		time.Unix(1700000000, 123456789).In(locSH),                                    // +08:00
		time.Unix(1700000000, 123456789).In(locNY),                                    // 美东（可能夏令时）
		time.Date(2024, 7, 15, 12, 0, 0, 500000000, locNY),                            // 夏令时期间
		time.Date(2024, 1, 15, 12, 0, 0, 500000000, locNY),                            // 标准时间期间
		time.Unix(1700000000, 123456789).In(time.FixedZone("NPT", 5*3600+45*60)),      // +05:45
		time.Unix(1700000000, 123456789).In(time.FixedZone("NST", -(3*3600 + 30*60))), // -03:30
	}

	// 确定性随机样本
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 500; i++ {
		sec := rng.Int63n(253402300799) // ~9999-12-31
		nsec := rng.Int63n(1e9)
		cases = append(cases, time.Unix(sec, nsec).UTC())
	}

	for i, ts := range cases {
		want := ts.Format(time.RFC3339Nano)
		got := string(appendRFC3339Nano(make([]byte, 0, 64), ts))
		if got != want {
			t.Fatalf("case %d: ts=%v got=%q want=%q", i, ts, got, want)
		}
	}
}

// TestAppendJSONStringEscapes 验证查表整段拷贝版转义输出与规范一致
func TestAppendJSONStringEscapes(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"plain", "plain"},
		{"中文消息", "中文消息"},
		{"say \"hi\"", "say \\\"hi\\\""},
		{"back\\slash", "back\\\\slash"},
		{"line1\nline2", "line1\\nline2"},
		{"tab\there", "tab\\there"},
		{"cr\rhere", "cr\\rhere"},
		{"ctl\x00\x01\x1f", "ctl\\u0000\\u0001\\u001f"},
		{"mix\"a\nb中\\c", "mix\\\"a\\nb中\\\\c"},
		{"trailing\"", "trailing\\\""},
		{"\u2028line", "\u2028line"}, // U+2028 不需转义（JSON 原样）
	}
	for _, tt := range tests {
		got := string(appendJSONString(make([]byte, 0, len(tt.in)+8), tt.in))
		if got != tt.want {
			t.Errorf("appendJSONString(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestExtractContextInfoZeroAlloc 无追踪字段时零分配
func TestExtractContextInfoZeroAlloc(t *testing.T) {
	allocs := testing.AllocsPerRun(100, func() {
		if got := extractContextInfo(nil); got != nil {
			t.Fatal("nil ctx want nil")
		}
		if got := extractContextInfo(context.Background()); got != nil {
			t.Fatal("background ctx want nil")
		}
	})
	if allocs != 0 {
		t.Fatalf("extractContextInfo allocs = %v, want 0", allocs)
	}
}
