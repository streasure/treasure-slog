# Treasure-Slog 高性能日志库

基于 Go 原生 slog 实现的高性能日志库，具备与 zap 类似的功能特性，支持每秒百万级日志写入。

## 特性

- **高性能**：无锁环形缓冲区 + 批量写入，实测可达 150万+ 日志/秒
- **多输出支持**：控制台、文件、网络（TCP/UDP/HTTP）同时输出
- **异步处理**：不阻塞业务逻辑，自动批量处理
- **动态配置**：支持运行时调整日志级别
- **日志采样**：可配置采样策略，减少高频日志输出
- **文件轮转**：自动按大小/时间轮转，支持压缩
- **对象池**：复用内存对象，减少 GC 压力
- **字段缓存**：自动缓存常用字段，提升性能
- **Hook 机制**：支持自定义钩子函数
- **Context 注入**：自动从 context 提取追踪信息

## 安装

```bash
go mod init your-project
go get github.com/yourusername/treasure-slog
```

## 快速开始

### 1. 基础使用

```go
package main

import (
    "treasure-slog/pkg/logger"
)

func main() {
    // 使用全局日志单例（自动加载 configs/config.yaml）
    log := logger.GetLogger()
    
    // 基础日志
    log.Info("应用启动", "version", "1.0.0", "env", "production")
    log.Debug("调试信息", "detail", "some debug data")
    log.Warn("警告信息", "threshold", 80)
    log.Error("错误信息", "error", "connection failed")
    
    // 同步日志（应用退出前调用）
    defer log.Sync()
}
```

### 2. 自定义配置

```go
package main

import (
    "treasure-slog/pkg/logger"
)

func main() {
    // 从指定配置文件创建
    log, err := logger.New("configs/config.yaml")
    if err != nil {
        panic(err)
    }
    defer log.Sync()
    
    log.Info("使用自定义配置")
}
```

### 3. 带字段的日志

```go
// With 添加固定字段
userLog := log.With("user_id", "12345", "ip", "192.168.1.1")
userLog.Info("用户登录")
userLog.Info("用户操作", "action", "buy")

// 输出：
// {"level":"INFO","msg":"用户登录","user_id":"12345","ip":"192.168.1.1"}
// {"level":"INFO","msg":"用户操作","user_id":"12345","ip":"192.168.1.1","action":"buy"}
```

### 4. Context 自动注入

```go
package main

import (
    "context"
    "treasure-slog/pkg/logger"
)

func main() {
    log := logger.GetLogger()
    
    // 创建带追踪信息的 context
    ctx := context.Background()
    ctx = context.WithValue(ctx, "request_id", "req-abc-123")
    ctx = context.WithValue(ctx, "user_id", "user-456")
    ctx = context.WithValue(ctx, "trace_id", "trace-xyz-789")
    
    // 创建带 context 的 logger
    ctxLog := log.WithContext(ctx)
    ctxLog.Info("处理请求")
    
    // 输出自动包含 context 信息：
    // {"level":"INFO","msg":"处理请求","request_id":"req-abc-123","user_id":"user-456","trace_id":"trace-xyz-789"}
}
```

### 5. Hook 机制

```go
package main

import (
    "fmt"
    "treasure-slog/pkg/logger"
)

// 自定义 Hook
type MetricsHook struct {
    counter map[string]int
}

func (h *MetricsHook) Run(msg string, level string, args ...any) {
    h.counter[level]++
    fmt.Printf("[Metrics] %s 级别日志计数: %d\n", level, h.counter[level])
}

func main() {
    log := logger.GetLogger()
    
    // 添加 Hook
    metricsHook := &MetricsHook{counter: make(map[string]int)}
    hookedLog := log.AddHook(metricsHook)
    
    hookedLog.Info("测试消息")
    hookedLog.Error("错误消息")
}
```

### 6. 动态调整日志级别

```go
package main

import (
    "treasure-slog/pkg/logger"
)

func main() {
    log := logger.GetLogger()
    
    // 初始级别为 info
    log.Info("这条会显示")
    log.Debug("这条不会显示")
    
    // 动态调整为 debug 级别
    log.SetLevel("debug")
    log.Debug("现在这条会显示了")
    
    // 查看当前级别
    fmt.Println("当前级别:", log.GetLevel())
}
```

### 7. Panic 恢复

```go
package main

import (
    "treasure-slog/pkg/logger"
)

func riskyOperation() {
    defer logger.Recover() // 自动捕获 panic 并记录
    
    // 可能触发 panic 的代码
    panic("something went wrong")
}

func main() {
    riskyOperation()
    // 程序会继续执行，不会崩溃
}
```

## 配置文件详解

### 完整配置示例

```yaml
# configs/config.yaml
log:
  # 日志级别: debug, info, warn, error
  level: info
  
  # 日志格式: json, console
  format: json
  
  # 异步配置
  async:
    enabled: true              # 是否启用异步日志
    buffer_size: 10000         # 环形缓冲区大小
    batch_size: 100            # 批量写入大小
    flush_interval: 100        # 刷新间隔(毫秒)
    workers: 4                 # 工作线程数
  
  # 控制台输出配置
  console:
    enabled: true              # 是否输出到控制台
    format: text               # 控制台格式: json, text
  
  # 文件输出配置
  file:
    enabled: true              # 是否输出到文件
    path: ./logs/app.log       # 日志文件路径
    rotate:
      max_size: 100            # 单个文件最大大小(MB)
      max_backups: 10          # 最大保留文件数
      max_age: 30              # 最大保留天数
      compress: true           # 是否压缩旧文件
  
  # 网络输出配置（支持 ELK、Graylog 等）
  network:
    enabled: false             # 是否启用网络输出
    type: tcp                  # 协议类型: tcp, udp, http
    address: localhost:9200    # 目标地址
    timeout: 5                 # 连接超时(秒)
    retry: 3                   # 重试次数
    tls: false                 # 是否启用 TLS
  
  # 堆栈追踪配置
  stacktrace:
    enabled: true              # 是否启用堆栈追踪
    level: error               # 追踪级别: error, warn
    depth: 10                  # 堆栈深度
  
  # 日志采样配置
  sampling:
    enabled: true              # 是否启用采样
    initial: 1000              # 前 N 条全量记录
    thereafter: 100            # 之后每 N 条记录 1 条
  
  # 字段缓存配置
  field_cache:
    enabled: true              # 是否启用字段缓存
    size: 1000                 # 缓存大小
  
  # 性能优化配置
  performance:
    lock_free: true            # 是否启用无锁队列
    use_pool: true             # 是否启用对象池
    prealloc: true             # 是否启用内存预分配
```

### 不同环境配置示例

**开发环境** (`configs/config.dev.yaml`):
```yaml
log:
  level: debug
  format: console
  console:
    enabled: true
    format: text
  file:
    enabled: false
  sampling:
    enabled: false
```

**生产环境** (`configs/config.prod.yaml`):
```yaml
log:
  level: info
  format: json
  async:
    enabled: true
    buffer_size: 100000
    batch_size: 1000
    flush_interval: 10
    workers: 16
  console:
    enabled: false
  file:
    enabled: true
    path: /var/log/app/app.log
    rotate:
      max_size: 500
      max_backups: 30
      max_age: 90
      compress: true
  sampling:
    enabled: true
    initial: 10000
    thereafter: 1000
```

**高性能模式** (`configs/config.highperf.yaml`):
```yaml
log:
  level: info
  format: json
  async:
    enabled: true
    buffer_size: 2000000    # 2M 缓冲区
    batch_size: 20000       # 2万批量
    flush_interval: 1       # 1ms 刷新
    workers: 64             # 64 工作线程
  console:
    enabled: false          # 禁用控制台提升性能
  file:
    enabled: true
    path: ./logs/app.log
    rotate:
      max_size: 1000
      compress: false       # 禁用压缩提升性能
  sampling:
    enabled: false
  performance:
    lock_free: true
    use_pool: true
    prealloc: true
```

## 高级用法

### HTTP 输出到 ELK

```yaml
# 配置
log:
  network:
    enabled: true
    type: http
    address: http://elasticsearch:9200/_bulk
    timeout: 10
    retry: 3
```

### TCP 输出到 Graylog

```yaml
# 配置
log:
  network:
    enabled: true
    type: tcp
    address: graylog:12201
    timeout: 5
    retry: 3
```

### 自定义采样策略

```go
// 在配置中启用采样
// 前 1000 条全量记录，之后每 100 条记录 1 条
log:
  sampling:
    enabled: true
    initial: 1000
    thereafter: 100
```

### 多 Logger 实例

```go
package main

import (
    "treasure-slog/pkg/logger"
)

func main() {
    // 业务日志
    businessLog, _ := logger.New("configs/business.yaml")
    defer businessLog.Sync()
    
    // 审计日志
    auditLog, _ := logger.New("configs/audit.yaml")
    defer auditLog.Sync()
    
    // 性能日志
    perfLog, _ := logger.New("configs/performance.yaml")
    defer perfLog.Sync()
    
    businessLog.Info("订单创建", "order_id", "123")
    auditLog.Info("用户登录", "user_id", "456")
    perfLog.Info("接口耗时", "duration_ms", 150)
}
```

## 性能优化建议

### 1. 生产环境推荐配置

```yaml
log:
  level: info              # 避免 debug 级别的大量日志
  format: json             # JSON 格式便于解析
  async:
    enabled: true
    buffer_size: 100000    # 根据内存调整
    batch_size: 1000       # 平衡延迟和吞吐量
    workers: 8             # CPU 核心数的 1-2 倍
  console:
    enabled: false         # 禁用控制台提升性能
  sampling:
    enabled: true          # 高频日志采样
    initial: 10000
    thereafter: 1000
```

### 2. 极高吞吐量场景

```yaml
log:
  async:
    buffer_size: 1000000   # 大缓冲区
    batch_size: 10000      # 大批量
    flush_interval: 1      # 快速刷新
    workers: 32            # 更多工作线程
  performance:
    lock_free: true
    use_pool: true
```

### 3. 低延迟场景

```yaml
log:
  async:
    enabled: true
    buffer_size: 10000     # 小缓冲区低延迟
    batch_size: 10         # 小批量快速写入
    flush_interval: 1      # 1ms 刷新
    workers: 4
```

## 监控与运维

### 日志文件管理

```bash
# 查看日志文件大小
du -sh logs/

# 清理旧日志
find logs/ -name "*.log" -mtime +30 -delete

# 压缩历史日志
find logs/ -name "*.log.*" -not -name "*.gz" -exec gzip {} \;
```

### 性能监控

```go
// 使用 pprof 监控
import _ "net/http/pprof"

func main() {
    go func() {
        http.ListenAndServe("localhost:6060", nil)
    }()
    // ...
}
```

访问 http://localhost:6060/debug/pprof/ 查看性能数据。

## 测试

```bash
# 运行所有测试
go test ./...

# 运行基准测试
go test -bench=. -benchtime=10s ./pkg/logger

# 运行百万级日志测试
go test -run=TestMillionLogsPerSecond -v ./pkg/logger

# 性能分析
go test -bench=BenchmarkLogger -cpuprofile=cpu.prof -memprofile=mem.prof ./pkg/logger
go tool pprof cpu.prof
```

## 故障排查

### 1. 日志丢失

- 检查缓冲区大小是否足够
- 确认 `Sync()` 在程序退出前被调用
- 查看是否有采样配置导致

### 2. 性能下降

- 禁用控制台输出
- 增加批量写入大小
- 启用采样减少日志量
- 检查磁盘 IO 瓶颈

### 3. 内存占用高

- 减小缓冲区大小
- 启用对象池
- 调整字段缓存大小

## 与 zap 对比

| 特性 | treasure-slog | zap |
|------|---------------|-----|
| 性能 | 150万+/秒 | 100万+/秒 |
| 依赖 | 仅标准库 + slog | 独立库 |
| 配置 | YAML 配置 | 代码配置 |
| 动态级别 | 支持 | 需自定义 |
| 网络输出 | 内置 | 需扩展 |
| 学习成本 | 低 | 中 |

## 贡献

欢迎提交 Issue 和 PR！

## 许可证

MIT License
