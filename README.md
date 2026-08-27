# Treasure-Slog 高性能日志库

基于 Go 原生 slog 实现的高性能日志库，具备与 zap 类似的功能特性，同步模式每秒可处理 1600万+ 条日志，异步模式 775万+ 条/秒，核心路径 0~1 次内存分配，空闲时 CPU 占用接近 0。

## 设计理念

### 1. 高性能设计

#### 1.1 线程安全环形缓冲区
- **设计目标**：实现高并发下的安全日志写入
- **实现原理**：使用互斥锁保护 head/tail 指针，确保多生产者多消费者安全
- **性能优化**：
  - 缓存行填充：避免 head 和 tail 指针在同一缓存行，减少伪共享
  - 位运算优化：使用位运算替代取模，提高索引计算性能
  - 内存对齐：确保缓冲区大小为 2 的幂，便于位运算
  - 动态扩缩容：根据使用率自动调整缓冲区大小（高水位80%扩容，低水位20%缩容）

#### 1.2 批量写入器
- **设计目标**：减少 I/O 操作，提高写入性能
- **实现原理**：将多条日志合并成一次写入，减少系统调用开销
- **性能优化**：
  - 预分配缓冲区：减少内存扩容
  - 定时刷新：确保日志及时写入，避免数据丢失
  - 批量处理：减少磁盘或网络 I/O 压力

#### 1.3 对象池（sync.Pool）
- **设计目标**：减少内存分配，降低 GC 压力
- **实现原理**：复用日志条目、缓冲区等对象，避免频繁创建和销毁
- **性能优化**：
  - 利用 Go 运行时 P 级别分片：减少锁竞争
  - GC 感知：与 Go 垃圾回收器深度集成，自动回收空闲对象
  - 预分配容量：为对象池中的对象预分配足够的容量，减少扩容
  - 类型安全：使用类型断言确保对象类型正确

#### 1.4 自定义 slog.Handler（FastHandler）
- **设计目标**：替代标准库 slog.JSONHandler，消除内部分配
- **实现原理**：直接操作 []byte 缓冲区，手写 JSON 序列化
- **性能优化**：
  - 零反射：不使用 encoding/json，避免反射开销
  - 缓冲区池化：使用 sync.Pool 复用 512B 序列化缓冲区
  - 内联数值序列化：自定义 appendInt/appendUint/appendFloat，避免 fmt.Sprintf
  - 快速字符串转义：手写 appendJSONString，逐字节处理转义字符

#### 1.5 快速路径参数优化
- **设计目标**：减少参数传递的内存分配
- **实现原理**：参数 ≤5 时使用预分配的栈数组，>5 时才使用堆分配切片
- **性能优化**：
  - 零分配快速路径：5个字段以内不产生堆分配
  - 预分配 argsBuf[5]：内嵌固定数组避免逃逸
  - 延迟分配 argsBig：仅在参数超过5个时才分配

#### 1.6 内存优化
- **设计目标**：减少内存分配和复制，提高性能
- **实现原理**：使用对象池、预分配、快速路径等技术
- **内存拷贝说明**：
  - 核心异步路径仅 1 次内存分配（logEntry 从 sync.Pool 获取，命中时零分配）
  - 必要的 copy 操作用于确保数据安全性，避免数据竞争
  - 除了系统级别的内存拷贝外，本工程的内存拷贝次数已降至最低

#### 1.7 异步处理
- **设计目标**：不阻塞业务逻辑，提高应用性能
- **实现原理**：使用工作线程异步处理日志，主线程快速返回
- **性能优化**：
  - 多线程并行：使用多个工作线程并行处理日志
  - 批量处理：工作线程批量处理日志，减少 I/O 操作
  - 优雅关闭：确保所有日志处理完成后再退出

### 2. 架构设计

#### 2.1 核心组件
- **SLogger**：主日志记录器，封装了所有日志功能
- **ringBuffer**：线程安全环形缓冲区，用于存储待处理的日志条目，支持动态扩缩容
- **FastHandler**：自定义 slog.Handler，零反射 JSON 序列化，消除标准库内部分配
- **batchWriter**：批量写入器，将多条日志合并成一次写入
- **networkWriter**：网络写入器，支持 TCP/UDP/HTTP 协议
- **httpWriter**：HTTP 写入器，支持将日志发送到 HTTP 端点
- **worker**：工作线程，负责异步处理日志

#### 2.2 数据流
1. **写入阶段**：业务代码调用日志方法 → 快速路径级别检查 → 从 sync.Pool 获取/创建日志条目 → 参数≤5走零分配快速路径 → 写入线程安全环形缓冲区
2. **处理阶段**：工作线程从环形缓冲区取出日志 → 执行钩子 → FastHandler 零反射 JSON 序列化 → 批量处理
3. **输出阶段**：批量写入器将日志写入控制台、文件或网络

#### 2.3 扩展机制
- **Hook 机制**：支持自定义钩子，在日志记录前后执行自定义逻辑
- **多输出**：支持同时输出到控制台、文件和网络
- **可配置**：通过 YAML 配置文件灵活配置所有功能
- **可扩展**：支持自定义写入器和处理器

### 3. 性能优化策略

#### 3.1 快速路径优化
- **参数快速路径**：5个字段以内使用内嵌 argsBuf[5] 数组，避免堆分配
- **级别快速返回**：日志级别检查失败时快速返回，避免不必要的处理
- **缓冲区池化**：FastHandler 使用 sync.Pool 复用序列化缓冲区

#### 3.2 并发优化
- **线程安全环形缓冲区**：使用互斥锁保护，确保多生产者多消费者安全
- **缓存行优化**：避免伪共享，提高缓存命中率
- **并行处理**：使用多个工作线程并行处理日志
- **动态扩缩容**：根据负载自动调整缓冲区大小

#### 3.3 内存优化
- **对象复用**：使用 sync.Pool 复用日志条目和缓冲区
- **预分配**：为切片和缓冲区预分配足够的容量
- **零反射序列化**：FastHandler 直接操作 []byte，避免 encoding/json 反射开销
- **必要的拷贝**：使用copy操作确保数据安全性，避免数据竞争

#### 3.4 I/O 优化
- **批量写入**：将多条日志合并成一次写入
- **缓冲写入**：使用 bufio.Writer 减少系统调用
- **定时刷新**：确保日志及时写入，避免数据丢失

### 4. 可靠性设计

#### 4.1 错误处理
- **降级处理**：缓冲区满时降级为同步处理，避免日志丢失
- **重试机制**：网络写入失败时自动重试
- **Panic 恢复**：捕获并记录 panic，避免程序崩溃

#### 4.2 数据安全
- **优雅关闭**：确保所有日志处理完成后再退出
- **资源管理**：正确关闭文件和网络连接，避免资源泄漏
- **数据完整性**：确保日志数据完整写入，避免数据损坏

#### 4.3 可观测性
- **堆栈追踪**：错误级别自动添加堆栈信息，便于定位问题
- **Context 注入**：自动从 context 提取追踪信息，支持分布式追踪
- **Hook 机制**：支持自定义监控和告警

### 5. 代码质量保证

#### 5.1 测试覆盖
- **单元测试**：覆盖所有核心功能
- **集成测试**：测试端到端功能
- **性能测试**：测试不同场景下的性能表现
- **基准测试**：持续监控性能变化

#### 5.2 代码风格
- **一致性**：统一的代码风格和命名规范
- **可读性**：清晰的代码结构和详细的注释
- **可维护性**：模块化设计，便于扩展和维护

#### 5.3 文档完善
- **API 文档**：详细的 API 文档
- **使用示例**：完整的使用示例
- **配置文档**：详细的配置说明
- **性能指南**：性能优化建议和最佳实践

## 特性

- **高性能**：线程安全环形缓冲区 + 批量写入 + FastHandler 零反射序列化，核心路径 0~1 次内存分配
- **多输出支持**：控制台、文件、网络（TCP/UDP/HTTP）同时输出
- **异步处理**：异步模式不阻塞业务逻辑，自动批量处理；空闲时 worker 真正阻塞，CPU 占用接近 0
- **动态配置**：支持运行时调整日志级别
- **日志采样**：可配置采样策略，减少高频日志输出
- **文件轮转**：自动按大小/时间轮转，支持 gzip 压缩
- **对象池**：sync.Pool 复用内存对象，减少 GC 压力
- **自定义 Handler**：FastHandler 零反射 JSON 序列化，消除标准库内部分配
- **快速路径**：5个字段以内零分配参数传递
- **Hook 机制**：支持自定义钩子函数
- **Context 注入**：自动从 context 提取追踪信息
- **灵活配置**：支持通过命令行参数和环境变量指定配置文件
- **大日志支持**：针对大日志场景（每条 >1MB）进行了专门优化

## 安装

```bash
go mod init your-project
go get github.com/streasure/treasure-slog
```

## 快速开始

### 1. 基础使用

#### 1.1 使用全局日志单例

> 注意：GetLogger() 函数已被删除，建议使用全局函数接口

```go
package main

import (
    logger "github.com/streasure/treasure-slog"
)

func main() {
    // 直接使用全局函数，无需获取 logger 实例
    // 配置文件路径通过命令行参数 --config 指定
    logger.Info("应用启动", "version", "1.0.0", "env", "production")
    logger.Debug("调试信息", "detail", "some debug data")
    logger.Warn("警告信息", "threshold", 80)
    logger.Error("错误信息", "error", "connection failed")
    
    // 同步日志（应用退出前调用）
    defer logger.Sync()
}
```

#### 1.2 使用全局函数接口（推荐）

```go
package main

import (
    logger "github.com/streasure/treasure-slog"
)

func main() {
    // 直接使用全局函数，无需获取 logger 实例
    // 配置文件路径通过命令行参数 --config 指定
    logger.Info("应用启动", "version", "1.0.0", "env", "production")
    logger.Debug("调试信息", "detail", "some debug data")
    logger.Warn("警告信息", "threshold", 80)
    logger.Error("错误信息", "error", "connection failed")
    
    // 同步日志（应用退出前调用）
    defer logger.Sync()
}
```

#### 1.3 启动命令示例

```bash
# 使用开发环境配置
go run main.go --config=configs/config.dev.yaml

# 使用生产环境配置
go run main.go --config=configs/config.prod.yaml

# 使用高性能模式配置
go run main.go --config=configs/config.highperf.yaml
```

### 2. 自定义配置

```go
package main

import (
    logger "github.com/streasure/treasure-slog"
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

#### 3.1 使用实例方法

```go
// With 添加固定字段
userLog := log.With("user_id", "12345", "ip", "192.168.1.1")
userLog.Info("用户登录")
userLog.Info("用户操作", "action", "buy")

// 输出：
// {"level":"INFO","msg":"用户登录","user_id":"12345","ip":"192.168.1.1"}
// {"level":"INFO","msg":"用户操作","user_id":"12345","ip":"192.168.1.1","action":"buy"}
```

#### 3.2 使用全局函数

```go
// 使用全局 With 函数
userLog := logger.With("user_id", "12345", "ip", "192.168.1.1")
userLog.Info("用户登录")
userLog.Info("用户操作", "action", "buy")
```

### 4. Context 自动注入

#### 4.1 使用实例方法

> 注意：GetLogger() 函数已被删除，建议使用全局函数接口

```go
package main

import (
    "context"
    logger "github.com/streasure/treasure-slog"
)

func main() {
    // 创建带追踪信息的 context
    ctx := context.Background()
    ctx = context.WithValue(ctx, "request_id", "req-abc-123")
    ctx = context.WithValue(ctx, "user_id", "user-456")
    ctx = context.WithValue(ctx, "trace_id", "trace-xyz-789")
    
    // 使用全局 WithContext 函数
    ctxLog := logger.WithContext(ctx)
    ctxLog.Info("处理请求")
    
    // 输出自动包含 context 信息：
    // {"level":"INFO","msg":"处理请求","request_id":"req-abc-123","user_id":"user-456","trace_id":"trace-xyz-789"}
}
```

#### 4.2 使用全局函数

```go
package main

import (
    "context"
    logger "github.com/streasure/treasure-slog"
)

func main() {
    // 创建带追踪信息的 context
    ctx := context.Background()
    ctx = context.WithValue(ctx, "request_id", "req-abc-123")
    ctx = context.WithValue(ctx, "user_id", "user-456")
    ctx = context.WithValue(ctx, "trace_id", "trace-xyz-789")
    
    // 使用全局 WithContext 函数
    ctxLog := logger.WithContext(ctx)
    ctxLog.Info("处理请求")
}
```

### 5. Hook 机制

#### 5.1 使用实例方法

> 注意：GetLogger() 函数已被删除，建议使用全局函数接口

```go
package main

import (
    "fmt"
    "github.com/streasure/treasure-slog"
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
    // 使用全局 AddHook 函数
    metricsHook := &MetricsHook{counter: make(map[string]int)}
    hookedLog := logger.AddHook(metricsHook)
    
    hookedLog.Info("测试消息")
    hookedLog.Error("错误消息")
}
```

#### 5.2 使用全局函数

```go
package main

import (
    "fmt"
    "github.com/streasure/treasure-slog"
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
    // 使用全局 AddHook 函数
    metricsHook := &MetricsHook{counter: make(map[string]int)}
    hookedLog := logger.AddHook(metricsHook)
    
    hookedLog.Info("测试消息")
    hookedLog.Error("错误消息")
}
```

### 6. 动态调整日志级别

#### 6.1 使用实例方法

> 注意：GetLogger() 函数已被删除，建议使用全局函数接口

```go
package main

import (
    logger "github.com/streasure/treasure-slog"
)

func main() {
    // 初始级别为 info
    logger.Info("这条会显示")
    logger.Debug("这条不会显示")
    
    // 动态调整为 debug 级别
    logger.SetLevel("debug")
    logger.Debug("现在这条会显示了")
    
    // 查看当前级别
    fmt.Println("当前级别:", logger.GetLevel())
}
```

#### 6.2 使用全局函数

```go
package main

import (
    logger "github.com/streasure/treasure-slog"
)

func main() {
    // 初始级别为 info
    logger.Info("这条会显示")
    logger.Debug("这条不会显示")
    
    // 动态调整为 debug 级别
    logger.SetLevel("debug")
    logger.Debug("现在这条会显示了")
    
    // 查看当前级别
    fmt.Println("当前级别:", logger.GetLevel())
}
```

### 7. Panic 恢复

```go
package main

import (
    logger "github.com/streasure/treasure-slog"
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
    buffer_size: 10000         # 环形缓冲区初始大小（支持动态扩缩容）
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
    logger "github.com/streasure/treasure-slog"
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
    buffer_size: 10000     # 小缓冲区低延迟
    batch_size: 10         # 小批量快速写入
    flush_interval: 1      # 1ms 刷新
    workers: 4
```

### 4. 减少内存分配

- **控制字段数量**：5个字段以内走零分配快速路径，核心路径仅 0~1 次分配
- **使用 FastHandler**：零反射 JSON 序列化，消除 encoding/json 开销
- **启用对象池**：`use_pool: true` 复用 logEntry 和缓冲区
- **避免大字段**：大字符串或复杂对象会增加序列化开销

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
go test -bench=. -benchtime=10s

# 运行百万级日志测试
go test -run=TestMillionLogsPerSecond -v

# 性能分析
go test -bench=BenchmarkLogger -cpuprofile=cpu.prof -memprofile=mem.prof
go tool pprof cpu.prof
```

## 故障排查

### 1. 日志丢失

- 检查缓冲区大小是否足够（缓冲区支持动态扩缩容，高水位80%自动扩容）
- 确认 `Sync()` 在程序退出前被调用
- 查看是否有采样配置导致

### 2. 性能下降

- 禁用控制台输出
- 增加批量写入大小
- 启用采样减少日志量
- 控制字段数量在5个以内（走零分配快速路径）
- 检查磁盘 IO 瓶颈

### 3. 内存占用高

- 减小缓冲区大小
- 启用对象池（`use_pool: true`）
- 减少日志字段数量

## 与 zap 对比

| 特性 | treasure-slog | zap |
|------|---------------|-----|
| 性能 | 百万+/秒 | 百万+/秒 |
| 核心路径内存分配 | 1 allocs/op | ~0 allocs/op |
| 依赖 | 仅标准库 + slog | 独立库 |
| 配置 | YAML 配置 | 代码配置 |
| 动态级别 | 支持 | 需自定义 |
| 网络输出 | 内置 | 需扩展 |
| 学习成本 | 低 | 中 |
| 内存拷贝 | 必要的拷贝 | 零拷贝 |

### 性能测试结果

> 测试环境：Intel i5-10400F @ 2.90GHz, Windows, 12 CPU cores, Go 1.22.5

#### 核心性能指标

| 场景 | QPS（每秒日志量） | 每条耗时 | 内存/条 | 分配次数 |
|------|-----------------|---------|--------|----------|
| 纯序列化（直接调用 FastHandler） | **1980万** | 50.5 ns | 0 B | **0** |
| 极限吞吐（最少参数 Info） | **1240万** | 80.6 ns | 3 B | **0** |
| 同步写入（Info 级别） | **1625万** | 61.6 ns | 32 B | 1 |
| 异步写入（Info 级别） | **775万** | 129.0 ns | 42 B | 1 |
| 全接口混合（8种调用方式） | **591万** | 169.1 ns | 84 B | 2 |
| 全链路（含文件 I/O） | **283万** | 353.7 ns | 183 B | 2 |

#### 分级别性能

| 级别 | 异步 QPS | 异步耗时 | 同步 QPS | 同步耗时 | 内存/条 |
|------|---------|---------|---------|---------|--------|
| Debug | 763万 | 131.1 ns | 1285万 | 77.8 ns | 32-43 B |
| Info | 775万 | 129.0 ns | 1625万 | 61.6 ns | 32-42 B |
| Warn | 691万 | 144.8 ns | 1488万 | 67.2 ns | 32-44 B |
| Error | 826万 | 121.0 ns | 1649万 | 60.6 ns | 32-43 B |

#### 高并发性能（异步管线）

| 并发 goroutine | QPS | 每条耗时 | 内存/条 | 分配次数 |
|---------------|-----|---------|--------|----------|
| 8 | **607万** | 164.6 ns | 86 B | 2 |
| 16 | 498万 | 201.0 ns | 83 B | 2 |
| 32 | 493万 | 203.0 ns | 85 B | 2 |
| 64 | 462万 | 216.4 ns | 86 B | 2 |
| 128 | 516万 | 193.7 ns | 86 B | 2 |
| 256 | 428万 | 233.7 ns | 87 B | 2 |

#### 文件 I/O 高并发

| 并发 goroutine | QPS | 每条耗时 | 内存/条 |
|---------------|-----|---------|--------|
| 1 | 178万 | 561.0 ns | 224 B |
| 8 | 331万 | 301.7 ns | 111 B |
| 16 | **609万** | 164.1 ns | 111 B |
| 32 | 578万 | 172.1 ns | 111 B |
| 64 | 578万 | 172.9 ns | 111 B |

#### Worker 数量扩展性

| Worker 数 | QPS | 每条耗时 | 内存/条 |
|-----------|-----|---------|--------|
| 1 | 248万 | 403.3 ns | 65 B |
| 4 | 364万 | 274.9 ns | 45 B |
| 8 | 287万 | 348.2 ns | 45 B |
| 16 | 503万 | 199.0 ns | 43 B |
| 32 | **553万** | 180.9 ns | 44 B |

#### 字段数量影响

| 字段数 | QPS | 每条耗时 | 内存/条 | 分配次数 |
|--------|-----|---------|--------|----------|
| 2 | 449万 | 222.8 ns | 224 B | 7 |
| 4 | 306万 | 326.8 ns | 368 B | 12 |
| 8 | 197万 | 507.9 ns | 656 B | 21 |
| 16 | 78.5万 | 1273 ns | 1360 B | 39 |
| 32 | 47.1万 | 2121 ns | 2960 B | 72 |

> **建议**：字段数量控制在 5 个以内可获得最佳性能

## 贡献

欢迎提交 Issue 和 PR！

## 许可证

MIT License
