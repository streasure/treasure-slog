# Treasure-Slog 快速入门指南

## 5 分钟快速上手

### 1. 安装

```bash
# 克隆项目
git clone https://github.com/streasure/treasure-slog.git
cd treasure-slog

# 安装依赖
go mod tidy
```

### 2. 基础使用（最简单的方式）

#### 2.1 使用全局日志实例

> 注意：GetLogger() 函数已被删除，建议使用全局函数接口

```go
package main

import (
    logger "github.com/streasure/treasure-slog"
)

func main() {
    // 直接使用全局函数，无需获取日志实例
    // 配置文件路径通过命令行参数 --config 指定
    defer logger.Sync() // 程序退出前必须调用
    
    // 记录日志
    logger.Info("Hello", "name", "World")
    logger.Error("Something wrong", "error", "connection failed")
}
```

#### 2.2 使用全局函数接口（推荐）

```go
package main

import (
    logger "github.com/streasure/treasure-slog"
)

func main() {
    // 直接使用全局函数，无需获取日志实例
    // 配置文件路径通过命令行参数 --config 指定
    defer logger.Sync() // 程序退出前必须调用
    
    // 记录日志
    logger.Info("Hello", "name", "World")
    logger.Error("Something wrong", "error", "connection failed")
}
```

#### 2.3 启动命令示例

```bash
# 使用开发环境配置
go run main.go --config=configs/config.dev.yaml

# 使用生产环境配置
go run main.go --config=configs/config.prod.yaml

# 使用高性能模式配置
go run main.go --config=configs/config.highperf.yaml
```

### 3. 通过命令行参数指定配置文件

```go
package main

import (
    logger "github.com/streasure/treasure-slog"
)

func main() {
    // 配置文件路径通过命令行参数 --config 指定
    // 例如：go run main.go --config=configs/config.yaml
    defer logger.Sync()
    
    logger.Info("应用启动")
}
```

### 4. 运行示例

```bash
# 基础示例
go run examples/basic/main.go

# HTTP 服务器示例
go run examples/http_server/main.go

# 高吞吐量测试
go run examples/high_throughput/main.go

# 命令行参数示例
go run cmd/main.go --config=configs/config.dev.yaml
```

## 常用配置

### 开发环境（控制台输出）

创建 `configs/config.yaml`：

```yaml
log:
  level: debug
  format: console
  console:
    enabled: true
    format: text
  file:
    enabled: false
```

### 生产环境（文件 + JSON）

```yaml
log:
  level: info
  format: json
  console:
    enabled: false
  file:
    enabled: true
    path: ./logs/app.log
    rotate:
      max_size: 100
      max_backups: 10
      max_age: 30
      compress: true
  async:
    enabled: true
    workers: 8
```

### 高性能模式（核心路径仅 1 次内存分配）

```yaml
log:
  level: info
  format: json
  console:
    enabled: false
  file:
    enabled: true
    path: ./logs/app.log
  async:
    enabled: true
    buffer_size: 2000000
    batch_size: 20000
    workers: 64
  performance:
    lock_free: true
    use_pool: true
```

> **性能提示**：字段数量控制在5个以内可获得最佳性能（核心路径仅1次内存分配）。FastHandler 零反射 JSON 序列化消除了标准库的内部分配开销。

### 大日志场景（每条 >1MB）

```yaml
log:
  level: info
  format: json
  async:
    enabled: true
    buffer_size: 500
    batch_size: 5
    flush_interval: 100
    workers: 16
  console:
    enabled: false
  file:
    enabled: true
    path: ./logs/large.log
    rotate:
      max_size: 1000
      max_backups: 3
      compress: false
  sampling:
    enabled: true
  performance:
    lock_free: true
    use_pool: true
```

## 核心功能速查

### 基础日志

#### 使用实例方法

```go
log.Debug("调试信息")
log.Info("普通信息", "key", "value")
log.Warn("警告信息", "count", 42)
log.Error("错误信息", "error", err)
```

#### 使用全局函数

```go
logger.Debug("调试信息")
logger.Info("普通信息", "key", "value")
logger.Warn("警告信息", "count", 42)
logger.Error("错误信息", "error", err)
```

### 添加固定字段

#### 使用实例方法

```go
userLog := log.With("user_id", "123", "ip", "1.2.3.4")
userLog.Info("登录")  // 自动包含 user_id 和 ip
```

#### 使用全局函数

```go
userLog := logger.With("user_id", "123", "ip", "1.2.3.4")
userLog.Info("登录")  // 自动包含 user_id 和 ip
```

### Context 追踪

#### 使用实例方法

```go
ctx := context.WithValue(ctx, "request_id", "abc-123")
ctxLog := log.WithContext(ctx)
ctxLog.Info("处理请求")  // 自动包含 request_id
```

#### 使用全局函数

```go
ctx := context.WithValue(ctx, "request_id", "abc-123")
ctxLog := logger.WithContext(ctx)
ctxLog.Info("处理请求")  // 自动包含 request_id
```

### 动态调整级别

#### 使用实例方法

```go
log.SetLevel("debug")  // 切换到 debug 级别
current := log.GetLevel()  // 获取当前级别
```

#### 使用全局函数

```go
logger.SetLevel("debug")  // 切换到 debug 级别
current := logger.GetLevel()  // 获取当前级别
```

### Panic 恢复

```go
func risky() {
    defer logger.Recover()  // 捕获 panic
    panic("oops")
}
```

## 性能测试

```bash
# 运行基准测试
go test -bench=. -benchtime=10s

# 百万级日志测试
go test -run=TestMillionLogsPerSecond -v

# 压力测试
go test -run=TestLoggerStress -v
```

## 常见问题

### Q: 日志没有输出到文件？

A: 检查配置：
- `file.enabled: true`
- 目录权限正确
- 调用 `log.Sync()` 在退出前

### Q: 性能不达标？

A: 优化建议：
- 禁用控制台输出
- 增大 `batch_size`
- 增加 `workers` 数量
- 启用 `performance.lock_free: true`

### Q: 内存占用高？

A: 调整配置：
- 减小 `buffer_size`
- 启用 `use_pool: true`
- 启用日志采样

## 下一步

- 查看完整文档：[README.md](README.md)
- 运行更多示例：`examples/` 目录
- 阅读源码：`logger.go`
- 尝试大日志场景：`configs/config.large.yaml`

## 获取帮助

- GitHub Issues: https://github.com/streasure/treasure-slog/issues
- 文档: https://github.com/streasure/treasure-slog/wiki
