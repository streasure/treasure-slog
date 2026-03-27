package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config 日志配置结构体
type Config struct {
	Log LogConfig `yaml:"log"`
}

// LogConfig 日志配置
type LogConfig struct {
	Level     string      `yaml:"level"`
	Format    string      `yaml:"format"`
	File      FileConfig  `yaml:"file"`
	Stacktrace StackConfig `yaml:"stacktrace"`
	Sampling  SamplingConfig `yaml:"sampling"`
}

// FileConfig 文件输出配置
type FileConfig struct {
	Enabled bool       `yaml:"enabled"`
	Path    string     `yaml:"path"`
	Rotate  RotateConfig `yaml:"rotate"`
}

// RotateConfig 轮转配置
type RotateConfig struct {
	MaxSize    int `yaml:"max_size"`
	MaxBackups int `yaml:"max_backups"`
	MaxAge     int `yaml:"max_age"`
}

// StackConfig 堆栈追踪配置
type StackConfig struct {
	Enabled bool   `yaml:"enabled"`
	Level   string `yaml:"level"`
}

// SamplingConfig 日志采样配置
type SamplingConfig struct {
	Enabled     bool `yaml:"enabled"`
	Initial     int  `yaml:"initial"`
	Thereafter  int  `yaml:"thereafter"`
}

// LoadConfig 加载配置文件
func LoadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var config Config
	err = yaml.NewDecoder(file).Decode(&config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}
