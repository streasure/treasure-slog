# Treasure-Slog

基于 Go 原生 `log/slog` 构建的高性能结构化日志库。

- 零反射 JSON 序列化（FastHandler），纯序列化路径 **0 次内存分配**，吞吐约 **2200 万条/秒**
- 同步模式约 **710 万条/秒**，异步模式约 **440 万条/秒**，核心路径 0~2 次分配
- 分片环形缓冲区 + 多 worker 批量写入，高并发（8~256 goroutine）下吞吐稳定
- 支持文件轮转、网络输出（TCP/UDP/HTTP）、日志采样、Hook、动态级别、Context 追踪注入

> 以上数据实测于 Intel i5-10400F（12 逻辑核）/ Windows / Go 1.26.5，复现命令见[性能实测](#性能实测)。

## 特性

| 特性 | 说明 |
|------|------|
| 高性能 | FastHandler 手写 JSON 序列化，零反射；对象池复用 logEntry 与序列化缓冲区 |
| 异步管线 | 分片环形缓冲区（每 worker 独占 shard，MPSC）+ worker 批量消费，缓冲区满自动降级同步，不丢日志 |
| 同步模式 | `async.enabled: false` 时直接写入，绕过队列，延迟最低 |
| 多输出 | 控制台、文件、网络（TCP/UDP/HTTP，支持 TLS 与重试）可任意组合 |
| 文件轮转 | 按大小/时间轮转，支持备份保留、过期清理 |
| 日志采样 | 对齐 zap：按 (级别, 消息) + 时间窗口限速重复日志，不同消息计数近似独立（4096 桶，哈希冲突会合并计数）、每窗口重置 |
| 动态级别 | `SetLevel` 运行时调整，`With` 派生的 logger 全链路共享级别状态 |
| Context 注入 | `WithContext` 自动提取 `request_id` / `user_id` / `span_id` / `trace_id` |
| Hook 机制 | 插件参与日志写入生命周期（写前/写后），仅对通过采样的日志触发，支持链式追加 |
| 堆栈追踪 | Error 级别自动附加堆栈（可配置级别与深度） |
| 诊断统计 | 内置耗时统计 API，可量化各阶段（级别检查/入队/序列化/写入）开销 |

## 环境要求

- Go 1.26.5+
- 依赖：仅标准库 + `gopkg.in/yaml.v3`

## 安装

```bash
go get github.com/streasure/treasure-slog
```

## 快速开始

### 初始化

treasure-slog **没有任何隐式初始化**：import 无副作用，不解析命令行参数，不读取默认配置。

- 必须显式调用 `New(configPath)` 创建 logger
- **首次** `New` 调用会同时设置全局实例（`sync.Once` 保证），此后包级全局函数即可用
- `New` 返回具体类型 `*SLogger`（完整实现 `Logger` 接口）

### API 设计

统一接口：**必须传入 context，Printf 格式化**

```go
Debug(ctx context.Context, format string, args ...any)
Info(ctx context.Context, format string, args ...any)
Warn(ctx context.Context, format string, args ...any)
Error(ctx context.Context, format string, args ...any)
```

## 接口详细说明

### 1. 基础日志方法

所有日志方法必须传入 `context.Context`，使用 Printf 风格格式化。

```go
package main

import (
    "context"
    "fmt"
    
    logger "github.com/streasure/treasure-slog"
)

func main() {
    if _, err := logger.New("configs/tlog.yaml"); err != nil {
        panic(err)
    }
    defer logger.Sync()
    
    ctx := context.Background()
    
    // 基础使用
    logger.Debug(ctx, "调试信息 key=%s", "value")
    logger.Info(ctx, "应用启动 version=%s env=%s", "1.0.0", "production")
    logger.Warn(ctx, "警告信息 threshold=%d", 80)
    logger.Error(ctx, "错误信息 error=%v", fmt.Errorf("connection failed"))
    
    // 多参数格式化
    logger.Info(ctx, "用户登录 user=%s ip=%s browser=%s", "admin", "192.168.1.1", "Chrome")
}
```

**输出示例：**
```json
{"time":"2024-01-01T10:00:00Z","level":"INFO","msg":"应用启动 version=1.0.0 env=production"}
```

### 2. With 方法 - 添加固定字段

返回派生的 logger，自动添加固定字段到后续所有日志。

```go
package main

import (
    "context"
    
    logger "github.com/streasure/treasure-slog"
)

func main() {
    if _, err := logger.New("configs/tlog.yaml"); err != nil {
        panic(err)
    }
    defer logger.Sync()
    
    ctx := context.Background()
    
    // 基础 With
    userLog := logger.With("user_id", "12345", "ip", "192.168.1.1")
    userLog.Info(ctx, "用户登录成功")
    userLog.Info(ctx, "执行操作 action=%s", "buy")
    
    // 链式 With
    orderLog := logger.With("user_id", "12345").With("order_id", "ORD-001")
    orderLog.Info(ctx, "创建订单")
    
    // 配合 context
    ctxLog := logger.WithContext(ctx).With("module", "payment")
    ctxLog.Info(ctx, "处理支付")
}
```

**输出示例：**
```json
{"time":"2024-01-01T10:00:00Z","level":"INFO","msg":"用户登录成功","user_id":"12345","ip":"192.168.1.1"}
{"time":"2024-01-01T10:00:01Z","level":"INFO","msg":"执行操作 action=buy","user_id":"12345","ip":"192.168.1.1"}
```

### 3. WithContext 方法 - 链路追踪

自动从 context 中提取 `request_id`、`user_id`、`span_id`、`trace_id` 四个追踪字段。

```go
package main

import (
    "context"
    
    logger "github.com/streasure/treasure-slog"
)

func main() {
    if _, err := logger.New("configs/tlog.yaml"); err != nil {
        panic(err)
    }
    defer logger.Sync()
    
    // 模拟 HTTP 请求 context（必须使用 logger.ContextKey* 类型化 key）
    ctx := context.Background()
    ctx = context.WithValue(ctx, logger.ContextKeyRequestID, "req-abc-123")
    ctx = context.WithValue(ctx, logger.ContextKeyTraceID, "trace-xyz-789")
    ctx = context.WithValue(ctx, logger.ContextKeyUserID, "user-456")
    
    // 方式1：每次调用时传入 ctx（自动提取 trace 信息）
    logger.Info(ctx, "处理请求")
    
    // 方式2：派生 logger，后续自动携带 trace 信息
    ctxLog := logger.WithContext(ctx)
    ctxLog.Info(ctx, "开始处理")
    ctxLog.Info(ctx, "处理完成 latency=%dms", 45)
}
```

> **注意**：提取只认导出的 `logger.ContextKeyRequestID` / `ContextKeyUserID` / `ContextKeySpanID` / `ContextKeyTraceID` 类型化 key；裸字符串 key（如 `context.WithValue(ctx, "request_id", ...)`）不会被提取，也不会出现在输出中。

**输出示例：**
```json
{"time":"2024-01-01T10:00:00Z","level":"INFO","msg":"处理请求","request_id":"req-abc-123","user_id":"user-456","trace_id":"trace-xyz-789"}
```

### 4. AddHook 方法 - 自定义钩子

在每条**通过采样、实际写入**的日志上执行自定义回调（写前/写后），用于指标统计、告警、日志转发等。

```go
package main

import (
    "context"
    "sync"
    
    logger "github.com/streasure/treasure-slog"
)

// MetricsHook 统计各级别日志数量（嵌入 HookFuncs 适配器，只需实现关心的方法）
type MetricsHook struct {
    logger.HookFuncs
    mu      sync.Mutex
    counter map[string]int64
}

func (h *MetricsHook) BeforeWrite(e *logger.Event) {
    h.mu.Lock()
    defer h.mu.Unlock()
    h.counter[e.Level.String()]++
}

func main() {
    if _, err := logger.New("configs/tlog.yaml"); err != nil {
        panic(err)
    }
    defer logger.Sync()
    
    // 创建 hook 并添加到 logger（返回新实例，原 logger 不受影响）
    hook := &MetricsHook{counter: make(map[string]int64)}
    hookedLog := logger.AddHook(hook)
    
    ctx := context.Background()
    
    // 使用带 hook 的 logger
    hookedLog.Info(ctx, "用户登录")
    hookedLog.Warn(ctx, "性能警告")
    hookedLog.Error(ctx, "连接失败")
    
    // 查看统计
    hook.mu.Lock()
    for level, count := range hook.counter {
        println(level, count)
    }
    hook.mu.Unlock()
}
```

也可在构造时通过选项注入（见「扩展与二次开发」）：`logger.New(path, logger.WithHooks(hook))`。

### 5. SetLevel / GetLevel 方法 - 动态调整级别

运行时动态调整日志级别，所有派生 logger 共享级别状态。

```go
package main

import (
    "context"
    
    logger "github.com/streasure/treasure-slog"
)

func main() {
    if _, err := logger.New("configs/tlog.yaml"); err != nil {
        panic(err)
    }
    defer logger.Sync()
    
    ctx := context.Background()
    
    // 获取当前级别（configs/tlog.yaml: level: debug）
    println(logger.GetLevel()) // "debug"
    
    // 切到 info（对所有后续日志生效）
    logger.SetLevel("info")
    logger.Debug(ctx, "这条 debug 日志不会显示")
    
    // 恢复到 debug
    logger.SetLevel("debug")
    logger.Debug(ctx, "这条 debug 日志会显示")
}
```

### 6. Recover 方法 - Panic 恢复

自动捕获 panic 并记录错误日志与堆栈信息，随后**重新抛出**（不吞掉 panic，进程仍会崩溃，此 API 用于自动补齐崩溃现场日志）。

```go
package main

import (
    "context"
    
    logger "github.com/streasure/treasure-slog"
)

func main() {
    if _, err := logger.New("configs/tlog.yaml"); err != nil {
        panic(err)
    }
    defer logger.Sync()
    
    // 记录 panic 现场后重新抛出（进程仍会崩溃）
    defer logger.Recover()
    
    // 模拟 panic
    panic("something went wrong")
}
```

**输出示例：**
```json
{"time":"2024-01-01T10:00:00Z","level":"ERROR","msg":"panic recovered: something went wrong, stacktrace: goroutine 1 [running]:..."}
```

### 7. Sync 方法 - 刷盘关闭

优雅关闭 logger，确保异步队列中的日志全部落盘。

```go
package main

import (
    "context"
    
    logger "github.com/streasure/treasure-slog"
)

func main() {
    if _, err := logger.New("configs/tlog.yaml"); err != nil {
        panic(err)
    }
    
    ctx := context.Background()
    logger.Info(ctx, "应用启动")
    
    // 退出前必须调用 Sync（幂等，可多次调用）
    if err := logger.Sync(); err != nil {
        println("sync error:", err)
    }
}
```

### 8. EnableTiming / DumpTiming 方法 - 性能诊断

内置耗时统计，用于定位性能瓶颈。

```go
package main

import (
    "context"
    
    logger "github.com/streasure/treasure-slog"
)

func main() {
    if _, err := logger.New("configs/tlog.yaml"); err != nil {
        panic(err)
    }
    defer logger.Sync()
    
    // 开启耗时统计
    logger.EnableTiming(true)
    defer logger.DumpTiming() // 输出到 stderr
    
    ctx := context.Background()
    
    // 执行日志调用
    for i := 0; i < 1000; i++ {
        logger.Info(ctx, "test message i=%d", i)
    }
}
```

**输出示例（节选）：**
```
=== treasure-slog 耗时统计 ===
总调用:           1000
级别过滤丢弃:     0 (0.0%)
同步路径处理:     1000
log() 总耗时:     avg=150 ns  total=150000 ns
级别检查:         avg=1 ns    total=1000 ns
slog调用:         avg=50 ns   total=50000 ns
=================================
```
> 实际输出还包括异步推入 / 缓冲满降级 / 批量处理 / 对象池 / 环形缓冲区 Push / Hook 执行 / Handler.Handle 等完整分项统计（约 17 行）。

### 9. Hook 接口 - 插件生命周期

```go
// Event 是 Hook 收到的日志事件快照
type Event struct {
    Time    time.Time       // 日志时间戳
    Level   slog.Level      // 日志级别
    Message string          // 日志消息
    Attrs   []slog.Attr     // 完整字段视图（With/WithContext 注入 + context 提取，已按 key 去重）
    Ctx     context.Context // 同步为调用方 ctx；异步为入队时捕获的 ctx
}

// Hook 插件接口
type Hook interface {
    BeforeWrite(e *Event)              // 写入前
    AfterWrite(e *Event, err error)    // 写入后，err 为序列化/写入结果
}

// HookFuncs 函数适配器：嵌入后按需赋值，零值安全
type HookFuncs struct {
    OnBefore func(*Event)
    OnAfter  func(*Event, error)
}
```

> **语义说明**：
> - Hook 插入**采样内侧**：仅对通过采样、实际写入的日志触发，采样丢弃的日志不会调用 Hook。
> - `Event.Attrs` 是完整字段视图：`With`/`WithContext` 预注入属性 + context 提取字段（同 key 已去重）。
>   `Info(ctx, "格式 %s", 参数)` 的格式化参数已内嵌进 msg，不作为结构化属性传入。
> - 每个 Hook 调用独立 panic 隔离；同步模式在调用方 goroutine 执行，异步模式在 worker 执行。
> - `AfterWrite` 可感知写失败（如磁盘满、网络断开），适合做降级告警。

```go
// AlertHook 示例：Error 级别时发送告警（嵌入 HookFuncs 只实现 BeforeWrite）
type AlertHook struct {
    logger.HookFuncs
    AlertFunc func(msg string)
}

func (h *AlertHook) BeforeWrite(e *logger.Event) {
    if e.Level >= slog.LevelError {
        h.AlertFunc(e.Message)
    }
}

// 使用
alertHook := &AlertHook{
    AlertFunc: func(msg string) {
        // 发送告警（邮件、钉钉、Slack 等）
        println("ALERT:", msg)
    },
}
hookedLog := logger.AddHook(alertHook)
```

### 10. 扩展与二次开发

所有扩展入口在构造函数的可变参数 `Option` 与 `Decorate` 装饰器上，无需修改本库源码。

#### 构造入口

```go
// 从配置文件构建（可叠加 Option）
l, err := logger.New("configs/tlog.yaml",
    logger.WithWriter(extraSink),          // 追加自定义输出（与配置输出并行）
    logger.WithHooks(metricsHook),         // 注入 Hook 插件
    logger.WithErrorHandler(func(err error) { /* 写失败降级处理 */ }),
)

// 从内存 Config 程序化构建（不依赖 YAML；nil 等价全默认）
cfg := &config.Config{}
cfg.Log.Async.Enabled = false
cfg.Log.Async.EnabledSet = true // 程序化构造时表达“显式关闭异步”需同时置 EnabledSet
l, err := logger.NewFromConfig(cfg, logger.WithWriter(extraSink))

// 获取完整处理链，供外部以 slog 方式复用或测试
h := l.Handler()
slog.New(h).Info("external slog")
```

#### 装饰器（Handler 包装）

管线结构：`输出(coreDecos) → Hook → 采样 → 写错误 → 管线装饰(pipelineDecos)`。

| 选项 | 位置 | 典型用途 |
|------|------|---------|
| `WithCoreDecorator(f)` | 采样之后、序列化附近（包主输出） | 脱敏、字段重写、主输出定制 |
| `WithHandlerDecorator(f)` | 整条链最外侧（采样之前） | 全量日志指标、审计计数 |

`Decorate` 快速实现自定义 `Handle` 逻辑，`WithAttrs`/`WithGroup`/`Enabled` 自动转发：

```go
import "log/slog"

// 示例：脱敏装饰器（在最终输出前替换敏感字段）
redact := func(next slog.Handler) slog.Handler {
    return logger.Decorate(next, func(ctx context.Context, r slog.Record, next slog.Handler) error {
        r.Attrs(func(a slog.Attr) bool {
            if a.Key == "password" {
                a.Value = slog.StringValue("***")
            }
            return true
        })
        return next.Handle(ctx, r)
    })
}

l, _ := logger.NewFromConfig(cfg, logger.WithCoreDecorator(redact))
```

多个同类型装饰器按注册顺序**从外到内**依次执行。装饰器也可直接包装 `Handler()` 返回的完整链。

### 11. 与标准 slog JSONHandler 的输出差异

FastHandler 为零分配手写序列化，以下语义与标准库 `slog.JSONHandler` 不同（均为有意取舍）：

| 场景 | 本库输出 | slog.JSONHandler |
|------|---------|------------------|
| `WithGroup("a")` 后的属性 | 拍平为点号 key `"a.k"` | 嵌套对象 `{"a":{"k":...}}` |
| Duration 属性 | 字符串 `"1.5s"` | 纳秒数 `1500000000` |
| `[]byte` 属性 | `%v` 文本字符串 | base64 字符串 |
| NaN / ±Inf 浮点 | 字符串 `"NaN"` / `"+Inf"` | 返回编码错误 |

与本库自有的 `KindGroup`（分组 key）嵌套输出不冲突：`WithGroup` 是"预设 key 前缀"语义，始终拍平；`slog.Group` 属性才产生嵌套。

## 运行示例

仓库内置可运行示例（均支持可选位置参数指定配置路径）：

```bash
go run ./cmd                          # 综合演示（默认 configs/tlog.yaml）
go run ./examples/basic configs/tlog.dev.yaml
go run ./examples/http_server         # HTTP 服务示例（监听 :8080）
go run ./examples/high_throughput     # 100 万条高吞吐测试
go run ./examples/global              # 全局函数用法
```

## 配置参考

### 完整配置项（含默认值）

```yaml
log:
  # 日志级别: debug | info | warn | error（默认 info）
  level: info
  # 日志格式: json（FastHandler 零反射）| console / text（标准 TextHandler）
  # 非法值: stderr 打印警告并回退 json
  format: json

  # 异步配置（async 块缺省或未写 enabled 键时默认启用；显式 enabled: false 才是同步模式）
  async:
    enabled: true            # 默认 true；false 时为同步模式
    buffer_size: 10000       # 环形缓冲区总容量（条，向上取整到 2 的幂，按 worker 分片均分）
    batch_size: 100          # 双重语义：worker 单批按条弹出（不满也处理，超时兜底）+ 刷盘字节阈值（batch_size×1KB）
    flush_interval: 100      # 刷新间隔（毫秒）
    worker_multiplier: 1     # worker 数 = CPU 核数 × 倍数（默认 1，最大 32）

  # 控制台输出
  console:
    enabled: false           # 默认 false
    format: text             # 缺省时与 log.format 一致；不同时启用独立 handler（如文件 JSON + 控制台 TEXT）

  # 文件输出（目录不存在时自动创建）
  file:
    enabled: false           # 默认 false
    path: ./logs/app.log     # 绝对路径原样使用；相对路径基于进程工作目录解析，目录自动创建
    rotate:
      max_size: 100          # 单文件上限（MB），默认 100
      max_backups: 10         # 保留备份数，默认 10
      max_age: 30             # 保留天数，默认 30
      interval: 0             # 时间轮转间隔（秒），如 86400 = 每天切割；0 = 禁用

  # 网络输出（ELK / Graylog 等）
  network:
    enabled: false            # 默认 false
    type: tcp                 # tcp | udp | http
    address: localhost:9200
    timeout: 5                # 秒，默认 5
    retry: 3                  # 重试次数，默认 3
    tls: false                # TCP TLS

  # 堆栈追踪
  stacktrace:
    enabled: false           # 默认 false
    level: error              # error | warn | info | debug
    depth: 10                 # 堆栈深度，默认 10

  # 日志采样：按 (级别, 消息) + 时间窗口限速（对齐 zap 语义），
  # 不同消息计数近似独立（4096 桶，哈希冲突会合并；info 洪水一般不影响 error 额度），每窗口重置后重新放行 initial 条
  sampling:
    enabled: false
    tick: 1                     # 采样窗口时长（秒），默认 1
    initial: 1000               # 每窗口每种 (级别, 消息) 前 N 条全量，默认 1000；显式 0 = 不预放行
    thereafter: 100             # 窗口内超出后每 N 条保留 1 条，默认 100；0 = 超出后全丢

  # 性能开关（默认全为 false）
  performance:
    lock_free: false           # 启用时 worker 数下限提升到 4
    use_pool: false            # logEntry / 序列化缓冲区对象池
    prealloc: false            # 环形缓冲区容量加倍，预分配
```

### 网络输出示例

```yaml
# 输出到 ELK（HTTP）
log:
  network:
    enabled: true
    type: http
    address: http://elasticsearch:9200/_bulk
    timeout: 10
    retry: 3

# 输出到 Graylog（TCP/TLS）
log:
  network:
    enabled: true
    type: tcp
    address: graylog:12201
    tls: true
```

### Worker 配置

Worker 数量根据 CPU 核数自动计算：`worker 数 = CPU 核数 × 倍数`

```yaml
log:
  async:
    worker_multiplier: 1     # 默认值：worker 数 = CPU 核数
    # worker_multiplier: 2   # 高并发时可改为 2：worker 数 = CPU 核数 × 2（仅支持整数倍数）
```

**计算规则：**
- `worker 数 = runtime.NumCPU() × worker_multiplier`
- 最小值：1
- 最大值：32

**示例（12 核 CPU）：**
| 倍数 | Worker 数 |
|------|----------|
| 1 | 12 |
| 2 | 24 |
| 3 | 32（上限） |

### 预置配置文件

仓库内置 4 套配置，位于 `configs/` 目录：

| 文件 | 场景 | 要点 |
|------|------|------|
| `tlog.dev.yaml` | 开发 | 同步模式 + 控制台 text + debug 级别 |
| `tlog.yaml` | 通用 | 异步 + 文件输出 + 采样 + error 堆栈 |
| `tlog.prod.yaml` | 生产 | 异步 worker_multiplier=1 + 文件轮转 + 采样 |
| `tlog.highperf.yaml` | 极限吞吐 | 1M 缓冲 + worker_multiplier=2 + 全部性能开关 |

**使用方式：**

```go
// 开发环境
logger.New("configs/tlog.dev.yaml")

// 生产环境
logger.New("configs/tlog.prod.yaml")
```

## 架构

```
业务调用 logger.Info(ctx, msg, args...)
        │
        ▼
   级别快速过滤（atomic load，未达级别直接返回）
        │
        ├── 同步模式 ──► processEntryDirect ─────────────────────────┐
        │                                                          │
        └── 异步模式 ──► sync.Pool 取 logEntry ──► 分片环形缓冲区     │
                                    │                               │
                                    ▼                               ▼
                    worker（每 shard 一个消费者）批量弹出 ──► processEntry
                                                                │
        ════════════ 处理链（handler chain）═════════════════════╧═════════════
        管线装饰(pipelineDecos，采样前全量可见)
              │
              ▼
        写错误处理（默认 stderr / WithErrorHandler）
              │
              ▼
        采样 SamplingHandler（按 级别+消息 限速，丢弃即短路）
              │
              ▼
        Hook 写前/写后（仅通过采样的日志触发）
              │
              ▼
        输出级装饰(coreDecos) ──► 扇出（文件/控制台/网络/自定义 Writer）
              │
              ▼
        FastHandler 序列化 ──► batchWriter 聚合写入（异步模式）
```

核心组件：

| 组件 | 职责 |
|------|------|
| `SLogger` | 主记录器，持有级别（atomic 指针，派生 logger 共享）、hook、writer、worker |
| `ringBuffer` | 分片环形缓冲区：shard 数 = worker 数，生产者 round-robin 选 shard，单 shard 为 MPSC，锁临界区最小化；缓存行填充防伪共享；缓冲满自动降级同步处理 |
| `worker` | 每 worker 独占一个 shard 消费；批量弹出 + 定时刷新 + 阻塞等待生产者唤醒（空闲时无轮询，CPU 占用趋近 0） |
| `FastHandler` | 零反射 JSON 序列化：手写 appendJSONString / appendInt 等，512B 缓冲池化；纯序列化 0 分配 |
| `batchWriter` | 聚合多条日志一次写入，按大小阈值与时间间隔双触发刷盘 |
| `networkWriter` / `httpWriter` | 网络输出，失败自动重连/重试 |
| `SamplingHandler` | 环绕式采样（对齐 zapcore.Sampler）：per-(级别, 消息) 计数 + tick 窗口重置；`Dropped()` / `Sampled()` 暴露决策计数 |
| `hooksHandler` / `Decorate` | 插件与装饰器扩展点：Hook 写前/写后、任意 Handler 包装（见「扩展与二次开发」） |

可靠性设计：

- 缓冲区满降级同步写入，日志不丢失
- 全链路 recover：log / worker / hook / 序列化各层 panic 均被捕获并记录到 stderr
- `Sync()` 幂等（`sync.Once`），并发调用安全；退出前调用可保证队列中日志全部落盘

## 性能实测

> 环境：Intel i5-10400F @ 2.90GHz（12 逻辑核），Windows，Go 1.26.5，`benchtime=1s`，`-count=3` 取中位数
> 下列 QPS 为 `RunParallel` 12 goroutine 聚合吞吐（io.Discard，纯管线；FullLink 与高并发表除外——后者为显式 8~256 goroutine）

### 核心指标

| 场景 | 每条耗时 | 吞吐 | 内存/条 | 分配次数 |
|------|---------|------|--------|---------|
| 纯序列化（直接调 FastHandler） | ~44-46 ns | ~2160-2240 万/s | 0 B | **0** |
| 同步模式（全级别混合） | ~140 ns | ~710 万/s | 28 B | 1 |
| 极限吞吐（Info 单参数） | ~138 ns | ~720 万/s | 2 B | 0 |
| 异步模式（全级别混合） | ~229 ns | ~440 万/s | 67 B | 2 |
| 10M 吞吐（Info + 格式参数，8 worker） | ~172 ns | ~580 万/s | 23 B | 1 |
| 全链路（真实文件轮转 I/O） | ~356 ns | ~280 万/s | 121 B | 2 |

### 高并发稳定性（异步管线）

| 并发 goroutine | 8 | 16 | 32 | 64 | 128 | 256 |
|----------------|-----|-----|-----|-----|------|-----|
| 吞吐（万/s） | ~621 | ~610 | ~592 | ~514 | ~525 | ~476 |

8~256 并发吞吐维持在 ~476-621 万/s，无锁竞争导致的明显衰减。

### Worker 扩展性（异步管线）

| Worker 数 | 1 | 2 | 4 | 8 | 16 | 32 |
|-----------|-----|-----|-----|-----|-----|-----|
| 吞吐（万/s） | ~408 | ~442 | ~478 | ~513 | ~490 | ~485 |

8 worker 附近吞吐最高（~513 万/s），16~32 worker 因唤醒与切换开销略降；1~4 worker 已达约八至九成水平。

### 复现命令

```bash
go test -run=^$ -bench "BenchmarkPureSerialization|BenchmarkAllLevelsAsync$|BenchmarkAllLevelsSync$|BenchmarkThroughputMax|BenchmarkAllInterfaces10M|BenchmarkFullLink" -benchmem -count=3
go test -run=^$ -bench "BenchmarkExtremeConcurrency|BenchmarkWorkerScalability" -benchtime=1s -count=3
```

## 性能调优建议

1. **控制字段数量**：字段 ≤5 时序列化与参数传递开销最小
2. **生产环境禁用控制台**：`console.enabled: false`，多路输出有聚合开销
3. **高频日志启用采样**：`sampling.enabled: true`，优先在入口丢弃
4. **异步参数按内存预算配置**：实际容量因 2 的幂取整与每 shard 最小 256 上取整，约为 `buffer_size` 的 2~3 倍（`prealloc: true` 再 ×2），乘单条日志大小即峰值内存；大日志场景调小 `buffer_size` 与 `batch_size`
5. **worker 不必过多**：4~8 worker 吞吐最高，16 以上因唤醒与切换开销反而下降（见扩展性数据）
6. **定位瓶颈**：`EnableTiming(true)` + `DumpTiming()` 查看各阶段耗时分布

## 测试

```bash
go test ./...                          # 全量测试（功能 / 并发 / panic 健壮性 / 锁竞争 / 时序）
go test -race ./...                    # 竞态检测
go test -run TestPanicRobustness -v    # 全链路 panic 压测
go test -run "TestLockContention" -v   # 锁竞争与高并发稳定性
```

## 故障排查

**日志没有输出？**
- 确认已调用 `New`（全局函数在首次 `New` 前为 no-op）
- 确认级别配置：`level: info` 时 `Debug` 不输出
- 确认采样配置：`sampling.thereafter` 过小会把同一条刷屏日志压得过低（不同消息计数近似独立——4096 桶，哈希冲突会合并，info 洪水一般不影响 error 额度；可用 `logger.SamplingStats()` 观测采样/丢弃量）
- 退出前调用 `Sync()`，否则异步队列中的日志可能未落盘

**日志文件位置不对？**
- 相对路径基于进程工作目录（调用工程所在目录）解析，检查启动时的 CWD；需要固定位置时使用绝对路径
- 若报错 `working directory ... is inside the system temp dir`：进程是从系统临时目录启动的（常见于编辑器运行配置），请把运行工作目录改为工程目录，或配置文件中改用绝对路径

**性能不达标？**
- 参见[性能调优建议](#性能调优建议)；用 `EnableTiming` 定位慢阶段
- 文件场景检查磁盘 I/O 与轮转配置（`max_size` 过小会频繁轮转）

**内存占用高？**
- 减小 `buffer_size`；关闭 `prealloc`
- 大日志场景调小 `buffer_size` 与 `batch_size`，减少单条日志在内存中的滞留

## 许可证

MIT License
