package config

import (
	"os"
	"runtime"

	"gopkg.in/yaml.v3"
)

// Config 顶层配置结构体
type Config struct {
	Log LogConfig `yaml:"log"` // 日志模块配置
}

// LogConfig 日志总配置，包含所有子模块的配置项
type LogConfig struct {
	Level       string            `yaml:"level"`       // 全局日志级别：debug / info / warn / error，低于该级别的日志将被丢弃
	Format      string            `yaml:"format"`      // 日志输出格式：json（结构化 JSON）/ text（人类可读纯文本）
	Async       AsyncConfig       `yaml:"async"`       // 异步写入配置
	Console     ConsoleConfig     `yaml:"console"`     // 控制台输出配置
	File        FileConfig        `yaml:"file"`        // 文件输出配置
	Network     NetworkConfig     `yaml:"network"`     // 网络输出配置（TCP/UDP 远程日志收集）
	Stacktrace  StackConfig       `yaml:"stacktrace"`  // 堆栈追踪配置
	Sampling    SamplingConfig    `yaml:"sampling"`    // 日志采样配置（高并发下降低日志量）
	FieldCache  FieldCacheConfig  `yaml:"field_cache"` // 字段缓存配置（已废弃，保留用于向后兼容）
	Performance PerformanceConfig `yaml:"performance"` // 性能优化开关配置
}

// AsyncConfig 异步写入配置，将日志写入与业务逻辑解耦，提升吞吐量
type AsyncConfig struct {
	Enabled          bool `yaml:"enabled"`           // 是否启用异步写入，未配置时默认启用
	BufferSize       int  `yaml:"buffer_size"`       // 异步缓冲区大小（条数），积压超过此值时新日志可能被丢弃，默认 10000
	BatchSize        int  `yaml:"batch_size"`        // 每批刷盘的日志条数，攒够后批量写入磁盘，默认 100
	FlushInterval    int  `yaml:"flush_interval"`    // 批量刷盘超时（毫秒），即使没攒够 batch_size 也在此时间后强制写入，默认 100
	WorkerMultiplier int  `yaml:"worker_multiplier"` // 异步 worker 数 = CPU 核数 × 此倍数，用于并发压缩等后台任务，默认 1
	Workers          int  `yaml:"-"`                 // 内部计算字段：实际 worker 数，限制在 1~32 之间，不由用户直接配置
}

// ConsoleConfig 控制台（标准输出/标准错误）日志输出配置
type ConsoleConfig struct {
	Enabled bool   `yaml:"enabled"` // 是否启用控制台输出
	Format  string `yaml:"format"`  // 控制台输出格式：text（人类可读）/ json（结构化），为空时跟随全局 format
}

// FileConfig 文件日志输出配置，支持按大小/时间轮转和异步压缩
type FileConfig struct {
	Enabled bool         `yaml:"enabled"` // 是否启用文件输出
	Path    string       `yaml:"path"`    // 日志文件路径，如 ./logs/app.log，目录不存在时自动创建
	Rotate  RotateConfig `yaml:"rotate"`  // 文件轮转策略配置
}

// RotateConfig 文件轮转配置，控制日志文件的切割和保留策略
type RotateConfig struct {
	MaxSize    int  `yaml:"max_size"`    // 单文件最大体积（MB），超过后切割新文件，默认 100
	MaxBackups int  `yaml:"max_backups"` // 最大保留历史文件数，超出后删除最旧的文件，默认 10
	MaxAge     int  `yaml:"max_age"`     // 文件最长保留天数，超出后删除，默认 30
	Interval   int  `yaml:"interval"`    // 时间轮转间隔（秒），0=禁用时间轮转仅按大小切割，如 86400 表示每天切割
}

// NetworkConfig 网络日志输出配置，将日志通过 TCP/UDP 发送到远程日志收集服务
type NetworkConfig struct {
	Enabled bool   `yaml:"enabled"` // 是否启用网络输出
	Type    string `yaml:"type"`    // 网络协议类型：tcp / udp
	Address string `yaml:"address"` // 远程服务地址，格式为 host:port，如 127.0.0.1:9000
	Timeout int    `yaml:"timeout"` // 连接/读写超时时间（秒），默认 5
	Retry   int    `yaml:"retry"`   // 发送失败后的重试次数，默认 3
	TLS     bool   `yaml:"tls"`     // 是否启用 TLS 加密连接（仅 tcp 生效）
}

// StackConfig 堆栈追踪配置，在指定级别及以上的日志中自动附加调用堆栈
type StackConfig struct {
	Enabled bool   `yaml:"enabled"` // 是否启用堆栈追踪
	Level   string `yaml:"level"`   // 触发堆栈抓取的最低日志级别：debug / info / warn / error
	Depth   int    `yaml:"depth"`   // 堆栈最大抓取深度（帧数），默认 10，过深会影响性能
}

// SamplingConfig 日志采样配置，在高并发场景下按比例丢弃重复日志，降低 IO 和 CPU 开销
type SamplingConfig struct {
	Enabled    bool `yaml:"enabled"`     // 是否启用采样
	Initial    int  `yaml:"initial"`     // 前 N 条日志全部保留（不采样），默认 1000
	Thereafter int  `yaml:"thereafter"`  // 之后每 N 条日志保留 1 条，默认 100
}

// FieldCacheConfig 字段缓存配置（已废弃，保留用于向后兼容旧配置文件）
type FieldCacheConfig struct {
	Enabled bool `yaml:"enabled"` // 是否启用字段缓存
	Size    int  `yaml:"size"`    // 缓存容量（条数），默认 1000
}

// PerformanceConfig 性能优化配置，针对高吞吐场景的底层调优开关
type PerformanceConfig struct {
	LockFree bool `yaml:"lock_free"` // 启用时确保最少 4 个 worker，减少锁竞争（非真正无锁实现）
	UsePool  bool `yaml:"use_pool"`  // 启用 sync.Pool 复用 logEntry 对象，减少 GC 压力
	Prealloc bool `yaml:"prealloc"`  // 启用时将 ring buffer 容量加倍，减少动态扩容开销
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
