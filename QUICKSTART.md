# Treasure-Slog 快速入门指南

## 5 分钟快速上手

### 1. 安装

```bash
# 克隆项目
git clone https://github.com/yourusername/treasure-slog.git
cd treasure-slog

# 安装依赖
go mod tidy
```

### 2. 基础使用（最简单的方式）

```go
package main

import (
    "treasure-slog/pkg/logger"
)

func main() {
    // 获取全局日志实例（自动加载 configs/config.yaml）
    log := logger.GetLogger()
    defer log.Sync() // 程序退出前必须调用
    
    // 记录日志
    log.Info("Hello", "name", "World")
    log.Error("Something wrong", "error", "connection failed")
}
```

### 3. 运行示例

```bash
# 基础示例
go run examples/basic/main.go

# HTTP 服务器示例
go run examples/http_server/main.go

# 高吞吐量测试
go run examples/high_throughput/main.go
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

### 高性能模式（150万+ 日志/秒）

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

## 核心功能速查

### 基础日志

```go
log.Debug("调试信息")
log.Info("普通信息", "key", "value")
log.Warn("警告信息", "count", 42)
log.Error("错误信息", "error", err)
```

### 添加固定字段

```go
userLog := log.With("user_id", "123", "ip", "1.2.3.4")
userLog.Info("登录")  // 自动包含 user_id 和 ip
```

### Context 追踪

```go
ctx := context.WithValue(ctx, "request_id", "abc-123")
ctxLog := log.WithContext(ctx)
ctxLog.Info("处理请求")  // 自动包含 request_id
```

### 动态调整级别

```go
log.SetLevel("debug")  // 切换到 debug 级别
current := log.GetLevel()  // 获取当前级别
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
go test -bench=. -benchtime=10s ./pkg/logger

# 百万级日志测试
go test -run=TestMillionLogsPerSecond -v ./pkg/logger

# 压力测试
go test -run=TestLoggerStress -v ./pkg/logger
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
- 阅读源码：`pkg/logger/logger.go`

## 获取帮助

- GitHub Issues: https://github.com/yourusername/treasure-slog/issues
- 文档: https://github.com/yourusername/treasure-slog/wiki
