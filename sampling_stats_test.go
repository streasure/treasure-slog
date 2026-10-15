package logger

import (
	"bytes"
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

// 未启用采样时统计为 0
func TestSamplingStats_Disabled(t *testing.T) {
	s := &SLogger{}
	sampled, dropped := s.SamplingStats()
	if sampled != 0 || dropped != 0 {
		t.Errorf("未启用采样时应为 0,0，got %d,%d", sampled, dropped)
	}
}

// initial:0 + thereafter:0 → 全丢，统计与输出一致
func TestSamplingStats_AllDrop(t *testing.T) {
	var buf bytes.Buffer
	level := &atomic.Int32{}
	level.Store(int32(slog.LevelInfo))
	fh := NewFastHandler(&buf, level)
	sh := NewSamplingHandler(fh, SamplingOptions{Initial: 0, Thereafter: 0, Tick: time.Hour})
	s := &SLogger{
		logger:          slog.New(sh),
		handler:         sh,
		level:           level,
		levelVar:        &slog.LevelVar{},
		samplingHandler: sh,
	}
	for i := range 5 {
		s.Info(context.Background(), "flood %d", i)
	}
	sampled, dropped := s.SamplingStats()
	if sampled != 0 || dropped != 5 {
		t.Errorf("全丢模式 sampled=%d dropped=%d, want 0/5", sampled, dropped)
	}
	if buf.Len() != 0 {
		t.Errorf("全丢模式仍写出: %s", buf.String())
	}
}

// 前 initial 条放行、之后计数
func TestSamplingStats_Counting(t *testing.T) {
	var buf bytes.Buffer
	level := &atomic.Int32{}
	level.Store(int32(slog.LevelInfo))
	fh := NewFastHandler(&buf, level)
	sh := NewSamplingHandler(fh, SamplingOptions{Initial: 2, Thereafter: 10, Tick: time.Hour})
	s := &SLogger{
		logger:          slog.New(sh),
		handler:         sh,
		level:           level,
		levelVar:        &slog.LevelVar{},
		samplingHandler: sh,
	}
	for range 3 {
		s.Info(context.Background(), "msg")
	}
	sampled, dropped := s.SamplingStats()
	if sampled != 2 || dropped != 1 {
		t.Errorf("sampled=%d dropped=%d, want 2/1", sampled, dropped)
	}
}

// 包级 SamplingStats 经 globalLogger 委派
func TestSamplingStats_PackageLevel(t *testing.T) {
	orig := globalLogger
	defer func() { globalLogger = orig }()

	// globalLogger 为 nil：返回 0,0
	globalLogger = nil
	if s, d := SamplingStats(); s != 0 || d != 0 {
		t.Errorf("globalLogger nil 时应为 0,0，got %d,%d", s, d)
	}

	sh := NewSamplingHandler(nil, SamplingOptions{Initial: 1, Thereafter: 1, Tick: time.Hour})
	globalLogger = &SLogger{samplingHandler: sh}
	if s, d := SamplingStats(); s != 0 || d != 0 {
		t.Errorf("未记录时应为 0,0，got %d,%d", s, d)
	}
	sh.stats.sampled.Add(3)
	sh.stats.dropped.Add(7)
	if s, d := SamplingStats(); s != 3 || d != 7 {
		t.Errorf("got %d,%d, want 3/7", s, d)
	}
}
