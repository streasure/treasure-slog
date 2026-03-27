# treasure-slog

基于 golang 原生 slog 实现的日志库，具有与 zap 类似的功能。

## 功能特性

- **日志级别**：支持 debug、info、warn、error 四个级别
- **结构化日志**：使用 JSON 格式输出
- **堆栈追踪**：在错误日志中自动添加堆栈追踪信息
- **日志采样**：支持日志采样功能，减少日志量
- **文件轮转**：支持日志文件轮转，自动管理日志文件大小和数量
- **Panic Recovery**：捕获并记录 panic 信息
- **全局日志单例**：提供全局日志单例，方便在整个应用中使用
- **配置文件**：通过 YAML 配置文件配置日志行为

## 目录结构

```
treasure-slog/
├── cmd/              # 命令行工具
├── configs/          # 配置文件
│   └── config.yaml   # 日志配置文件
├── internal/         # 内部包
│   └── config/       # 配置解析
├── pkg/              # 可导出的包
│   └── logger/       # 日志实现
├── go.mod            # Go 模块文件
└── README.md         # 项目说明
```

## 安装

```bash
go get github.com/yourusername/treasure-slog
```

## 使用示例

### 基本使用

```go
package main

import (
    "github.com/yourusername/treasure-slog/pkg/logger"
)

func main() {
    // 初始化日志（可选，默认会使用 configs/config.yaml）
    log, err := logger.New("configs/config.yaml")
    if err != nil {
        panic(err)
    }

    // 或者使用全局日志单例
    log = logger.GetLogger()

    // 记录日志
    log.Debug("Debug message", "key1", "value1")
    log.Info("Info message", "key1", "value1")
    log.Warn("Warn message", "key1", "value1")
    log.Error("Error message", "key1", "value1")

    // 带上下文的日志
    withLog := log.With("context", "test")
    withLog.Info("Info message with context")

    // Panic Recovery
    defer logger.Recover()
    // 触发 panic 会被捕获并记录
}
```

### 配置文件

配置文件 `configs/config.yaml` 示例：

```yaml
# 日志配置
log:
  # 日志级别: debug, info, warn, error
  level: info
  # 日志格式: json, console
  format: json
  # 文件输出配置
  file:
    # 是否启用文件输出
    enabled: true
    # 日志文件路径
    path: ./logs/app.log
    # 轮转配置
    rotate:
      # 最大文件大小 (MB)
      max_size: 10
      # 最大保留文件数
      max_backups: 5
      # 最大保留时间 (天)
      max_age: 7
  # 堆栈追踪配置
  stacktrace:
    # 是否启用堆栈追踪
    enabled: true
    # 堆栈追踪级别: error, warn
    level: error
  # 日志采样配置
  sampling:
    # 是否启用采样
    enabled: true
    # 采样率
    initial: 100
    # 之后的采样率
    thereafter: 10
```

## 测试

运行测试：

```bash
go test ./pkg/logger/...
```

## 依赖

- `gopkg.in/natefinch/lumberjack.v2` - 用于日志文件轮转
- `gopkg.in/yaml.v3` - 用于解析 YAML 配置文件
