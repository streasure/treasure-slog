package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// loadYAML 写入临时配置并 LoadConfig
func loadYAML(t *testing.T, content string) *Config {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cfg.yaml")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

// 无 async 块：默认启用异步并套用全部默认值
func TestSetDefaults_AsyncOmitted(t *testing.T) {
	cfg := loadYAML(t, "log:\n  level: info\n")
	if !cfg.Log.Async.Enabled {
		t.Error("async 块缺省时应默认启用异步")
	}
	if cfg.Log.Async.BufferSize != 10000 || cfg.Log.Async.BatchSize != 100 || cfg.Log.Async.FlushInterval != 100 {
		t.Errorf("异步默认值错误: buffer=%d batch=%d flush=%d",
			cfg.Log.Async.BufferSize, cfg.Log.Async.BatchSize, cfg.Log.Async.FlushInterval)
	}
	want := min(max(runtime.NumCPU(), 1), 32)
	if cfg.Log.Async.Workers != want {
		t.Errorf("Workers = %d, want %d", cfg.Log.Async.Workers, want)
	}
}

// 显式 enabled: false 必须保持同步（回归：旧实现按零值推断会翻转为 true）
func TestSetDefaults_AsyncExplicitFalse(t *testing.T) {
	cfg := loadYAML(t, "log:\n  async:\n    enabled: false\n")
	if cfg.Log.Async.Enabled {
		t.Error("显式 enabled: false 应保持同步模式（tlog.dev.yaml 依赖此行为）")
	}
}

// 显式 false + 其他字段：保持同步，字段保留
func TestSetDefaults_AsyncExplicitFalseWithFields(t *testing.T) {
	cfg := loadYAML(t, "log:\n  async:\n    enabled: false\n    buffer_size: 500\n")
	if cfg.Log.Async.Enabled {
		t.Error("显式 enabled: false 应保持同步模式")
	}
	if cfg.Log.Async.BufferSize != 500 {
		t.Errorf("BufferSize = %d, want 500", cfg.Log.Async.BufferSize)
	}
}

// 只写调优字段、未写 enabled 键：视为未显式配置，默认启用异步
func TestSetDefaults_AsyncFieldsWithoutEnabledKey(t *testing.T) {
	cfg := loadYAML(t, "log:\n  async:\n    buffer_size: 500\n")
	if !cfg.Log.Async.Enabled {
		t.Error("未写 enabled 键时应默认启用异步")
	}
	if cfg.Log.Async.BufferSize != 500 {
		t.Errorf("BufferSize = %d, want 500", cfg.Log.Async.BufferSize)
	}
}

// 显式 true + 全部字段
func TestSetDefaults_AsyncExplicitTrue(t *testing.T) {
	cfg := loadYAML(t, "log:\n  async:\n    enabled: true\n    buffer_size: 500\n    batch_size: 50\n    flush_interval: 10\n    worker_multiplier: 2\n")
	a := cfg.Log.Async
	if !a.Enabled || a.BufferSize != 500 || a.BatchSize != 50 || a.FlushInterval != 10 {
		t.Errorf("async 字段错误: %+v", a)
	}
	want := min(max(runtime.NumCPU()*2, 1), 32)
	if a.Workers != want {
		t.Errorf("Workers = %d, want %d", a.Workers, want)
	}
}

// worker_multiplier 上限 32
func TestSetDefaults_WorkerMultiplierCap(t *testing.T) {
	cfg := loadYAML(t, "log:\n  async:\n    enabled: true\n    worker_multiplier: 100\n")
	if cfg.Log.Async.Workers != 32 {
		t.Errorf("Workers = %d, want cap 32", cfg.Log.Async.Workers)
	}
}

// 轮转默认值
func TestSetDefaults_RotateDefaults(t *testing.T) {
	cfg := loadYAML(t, "log:\n  file:\n    enabled: true\n    path: ./logs/x.log\n")
	r := cfg.Log.File.Rotate
	if r.MaxSize != 100 || r.MaxBackups != 10 || r.MaxAge != 30 {
		t.Errorf("轮转默认值错误: %+v", r)
	}
}

// 轮转显式值原样保留
func TestSetDefaults_RotateExplicit(t *testing.T) {
	cfg := loadYAML(t, "log:\n  file:\n    enabled: true\n    path: ./logs/x.log\n    rotate:\n      max_size: 7\n      max_backups: 3\n      max_age: 5\n      interval: 60\n")
	r := cfg.Log.File.Rotate
	if r.MaxSize != 7 || r.MaxBackups != 3 || r.MaxAge != 5 || r.Interval != 60 {
		t.Errorf("轮转显式值错误: %+v", r)
	}
}

// stacktrace 启用时 depth 缺省为 10；未启用不补默认值
func TestSetDefaults_StacktraceDepth(t *testing.T) {
	on := loadYAML(t, "log:\n  stacktrace:\n    enabled: true\n")
	if on.Log.Stacktrace.Depth != 10 {
		t.Errorf("Depth = %d, want 10", on.Log.Stacktrace.Depth)
	}
	off := loadYAML(t, "log:\n  stacktrace:\n    enabled: false\n")
	if off.Log.Stacktrace.Depth != 0 {
		t.Errorf("未启用时 Depth = %d, want 0", off.Log.Stacktrace.Depth)
	}
}

// sampling 启用时补 1000/100；未启用不补
func TestSetDefaults_Sampling(t *testing.T) {
	on := loadYAML(t, "log:\n  sampling:\n    enabled: true\n")
	if on.Log.Sampling.Initial != 1000 || on.Log.Sampling.Thereafter != 100 {
		t.Errorf("采样默认值错误: %+v", on.Log.Sampling)
	}
	off := loadYAML(t, "log:\n  sampling:\n    enabled: false\n")
	if off.Log.Sampling.Initial != 0 || off.Log.Sampling.Thereafter != 0 {
		t.Errorf("未启用时采样应保持零值: %+v", off.Log.Sampling)
	}
}

// 显式 initial: 0 / thereafter: 0 必须保持 0（回归：旧实现把显式 0 强制改写为默认值，
// 导致 README 承诺的 "thereafter: 0 = 超出后全丢" 配置层不可达）
func TestSetDefaults_SamplingExplicitZero(t *testing.T) {
	cfg := loadYAML(t, "log:\n  sampling:\n    enabled: true\n    initial: 0\n    thereafter: 0\n")
	s := cfg.Log.Sampling
	if s.Initial != 0 || s.Thereafter != 0 {
		t.Errorf("显式 0 被改写: initial=%d thereafter=%d, want 0/0", s.Initial, s.Thereafter)
	}
	if s.Tick != 1 {
		t.Errorf("Tick = %d, want 1", s.Tick)
	}
}

// 只显式写 thereafter: 0：thereafter 保持 0，initial 缺省仍取 1000
func TestSetDefaults_SamplingThereafterZeroOnly(t *testing.T) {
	cfg := loadYAML(t, "log:\n  sampling:\n    enabled: true\n    thereafter: 0\n")
	if cfg.Log.Sampling.Initial != 1000 {
		t.Errorf("Initial = %d, want 1000", cfg.Log.Sampling.Initial)
	}
	if cfg.Log.Sampling.Thereafter != 0 {
		t.Errorf("Thereafter = %d, want 0", cfg.Log.Sampling.Thereafter)
	}
}

// network timeout/retry 缺省 5/3
func TestSetDefaults_Network(t *testing.T) {
	cfg := loadYAML(t, "log:\n  network:\n    enabled: false\n")
	if cfg.Log.Network.Timeout != 5 || cfg.Log.Network.Retry != 3 {
		t.Errorf("网络默认值错误: timeout=%d retry=%d", cfg.Log.Network.Timeout, cfg.Log.Network.Retry)
	}
}

// 未知字段被容忍（KnownFields(false)）
func TestLoadConfig_UnknownFieldsIgnored(t *testing.T) {
	cfg := loadYAML(t, "log:\n  level: info\n  not_a_field: 1\n  async:\n    weird_key: 2\n")
	if cfg.Log.Level != "info" {
		t.Errorf("Level = %q, want info", cfg.Log.Level)
	}
	if !cfg.Log.Async.Enabled {
		t.Error("未知字段不应影响 async 默认启用")
	}
}
