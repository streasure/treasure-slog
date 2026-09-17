package config

import (
	"os"
	"runtime"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 日志配置结构体
type Config struct {
	Log LogConfig `yaml:"log"`
}

// LogConfig 日志配置
type LogConfig struct {
	Level       string            `yaml:"level"`
	Format      string            `yaml:"format"`
	Async       AsyncConfig       `yaml:"async"`
	Console     ConsoleConfig     `yaml:"console"`
	File        FileConfig        `yaml:"file"`
	Network     NetworkConfig     `yaml:"network"`
	Stacktrace  StackConfig       `yaml:"stacktrace"`
	Sampling    SamplingConfig    `yaml:"sampling"`
	FieldCache  FieldCacheConfig  `yaml:"field_cache"`
	Performance PerformanceConfig `yaml:"performance"`
}

// AsyncConfig 异步配置
type AsyncConfig struct {
	Enabled          bool `yaml:"enabled"`
	BufferSize       int  `yaml:"buffer_size"`
	BatchSize        int  `yaml:"batch_size"`
	FlushInterval    int  `yaml:"flush_interval"`
	WorkerMultiplier int  `yaml:"worker_multiplier"` // worker 数 = CPU 核数 × 倍数，默认 1
	Workers          int  `yaml:"-"`                  // 内部计算：CPU 核数 × 倍数
}

// ConsoleConfig 控制台输出配置
type ConsoleConfig struct {
	Enabled bool   `yaml:"enabled"`
	Format  string `yaml:"format"`
}

// FileConfig 文件输出配置
type FileConfig struct {
	Enabled bool         `yaml:"enabled"`
	Path    string       `yaml:"path"`
	Rotate  RotateConfig `yaml:"rotate"`
}

// RotateConfig 轮转配置
type RotateConfig struct {
	MaxSize    int           `yaml:"max_size"`
	MaxBackups int           `yaml:"max_backups"`
	MaxAge     int           `yaml:"max_age"`
	Compress   bool          `yaml:"compress"`
	Interval   time.Duration `yaml:"interval"` // 时间轮转间隔（如 24h），0=禁用
}

// NetworkConfig 网络输出配置
type NetworkConfig struct {
	Enabled bool   `yaml:"enabled"`
	Type    string `yaml:"type"`
	Address string `yaml:"address"`
	Timeout int    `yaml:"timeout"`
	Retry   int    `yaml:"retry"`
	TLS     bool   `yaml:"tls"`
}

// StackConfig 堆栈追踪配置
type StackConfig struct {
	Enabled bool   `yaml:"enabled"`
	Level   string `yaml:"level"`
	Depth   int    `yaml:"depth"`
}

// SamplingConfig 日志采样配置
type SamplingConfig struct {
	Enabled    bool `yaml:"enabled"`
	Initial    int  `yaml:"initial"`
	Thereafter int  `yaml:"thereafter"`
}

// FieldCacheConfig 字段缓存配置（已废弃，保留用于向后兼容旧配置文件）
type FieldCacheConfig struct {
	Enabled bool `yaml:"enabled"`
	Size    int  `yaml:"size"`
}

// PerformanceConfig 性能优化配置
// LockFree: 启用时确保最少 4 个 worker，减少锁竞争（非真正无锁实现）
// UsePool: 启用 sync.Pool 复用 logEntry 对象，减少 GC 压力
// Prealloc: 启用时将 ring buffer 容量加倍，减少动态扩容开销
type PerformanceConfig struct {
	LockFree bool `yaml:"lock_free"`
	UsePool  bool `yaml:"use_pool"`
	Prealloc bool `yaml:"prealloc"`
}

// LoadConfig 加载配置文件
func LoadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var config Config
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(false) // 允许未知字段，提高兼容性
	err = decoder.Decode(&config)
	if err != nil {
		return nil, err
	}

	// 设置默认值
	setDefaults(&config)

	return &config, nil
}

// setDefaults 设置默认值
func setDefaults(cfg *Config) {
	// async.enabled 默认为 true：如果配置中没有 async 块或 enabled 字段为零值，
	// 且其他 async 字段也为零值，视为未配置 async，默认启用异步
	if !cfg.Log.Async.Enabled && cfg.Log.Async.WorkerMultiplier == 0 &&
		cfg.Log.Async.BufferSize == 0 && cfg.Log.Async.BatchSize == 0 &&
		cfg.Log.Async.FlushInterval == 0 {
		cfg.Log.Async.Enabled = true
	}
	if cfg.Log.Async.BufferSize == 0 {
		cfg.Log.Async.BufferSize = 10000
	}
	if cfg.Log.Async.BatchSize == 0 {
		cfg.Log.Async.BatchSize = 100
	}
	if cfg.Log.Async.FlushInterval == 0 {
		cfg.Log.Async.FlushInterval = 100
	}

	// worker 数 = CPU 核数 × 倍数
	multiplier := cfg.Log.Async.WorkerMultiplier
	if multiplier <= 0 {
		multiplier = 1
	}
	workers := runtime.NumCPU() * multiplier
	// 限制范围：1~32
	if workers < 1 {
		workers = 1
	}
	if workers > 32 {
		workers = 32
	}
	cfg.Log.Async.Workers = workers

	if cfg.Log.File.Rotate.MaxSize == 0 {
		cfg.Log.File.Rotate.MaxSize = 100
	}
	if cfg.Log.File.Rotate.MaxBackups == 0 {
		cfg.Log.File.Rotate.MaxBackups = 10
	}
	if cfg.Log.File.Rotate.MaxAge == 0 {
		cfg.Log.File.Rotate.MaxAge = 30
	}
	if cfg.Log.Stacktrace.Depth == 0 {
		cfg.Log.Stacktrace.Depth = 10
	}
	if cfg.Log.Sampling.Initial == 0 {
		cfg.Log.Sampling.Initial = 1000
	}
	if cfg.Log.Sampling.Thereafter == 0 {
		cfg.Log.Sampling.Thereafter = 100
	}
	if cfg.Log.FieldCache.Size == 0 {
		cfg.Log.FieldCache.Size = 1000
	}
	if cfg.Log.Network.Timeout == 0 {
		cfg.Log.Network.Timeout = 5
	}
	if cfg.Log.Network.Retry == 0 {
		cfg.Log.Network.Retry = 3
	}
}
