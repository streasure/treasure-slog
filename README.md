# Treasure-Slog

基于 Go 原生 `log/slog` 构建的高性能结构化日志库。

- 零反射 JSON 序列化（FastHandler），纯序列化路径 **0 次内存分配**，吞吐约 **2900 万条/秒**
- 同步模式约 **1800 万条/秒**，异步模式约 **800 万条/秒**，核心路径 0~2 次分配
- 分片环形缓冲区 + 多 worker 批量写入，高并发（8~256 goroutine）下吞吐稳定
- 支持文件轮转、网络输出（TCP/UDP/HTTP）、日志采样、Hook、动态级别、Context 追踪注入

> 以上数据实测于 Intel i5-10400F（12 逻辑核）/ Windows / Go 1.22.5，复现命令见[性能实测](#性能实测)。

## 特性

| 特性 | 说明 |
|------|------|
| 高性能 | FastHandler 手写 JSON 序列化，零反射；对象池复用 logEntry 与序列化缓冲区 |
| 异步管线 | 分片环形缓冲区（每 worker 独占 shard，MPSC）+ worker 批量消费，缓冲区满自动降级同步，不丢日志 |
| 同步模式 | `async.enabled: false` 时直接写入，绕过队列，延迟最低 |
| 多输出 | 控制台、文件、网络（TCP/UDP/HTTP，支持 TLS 与重试）可任意组合 |
| 文件轮转 | 按大小/时间轮转，支持备份保留、过期清理、gzip 异步压缩 |
| 日志采样 | 前 N 条全量记录，其后按频率采样，应对高频日志 |
| 动态级别 | `SetLevel` 运行时调整，`With` 派生的 logger 全链路共享级别状态 |
| Context 注入 | `WithContext` 自动提取 `request_id` / `user_id` / `span_id` / `trace_id` |
| Hook 机制 | 自定义钩子参与每条日志处理（指标上报、告警等），支持链式追加 |
| 堆栈追踪 | Error 级别自动附加堆栈（可配置级别与深度） |
| 诊断统计 | 内置耗时统计 API，可量化各阶段（级别检查/入队/序列化/写入）开销 |

## 环境要求

- Go 1.22+
- 依赖：仅标准库 + `gopkg.in/yaml.v3`

## 安装

```bash
go get github.com/streasure/treasure-slog
```

## 快速开始

### 初始化模型

treasure-slog **没有任何隐式初始化**：import 无副作用，不解析命令行参数，不读取默认配置。

- 必须显式调用 `New(configPath)` 创建 logger
- **首次** `New` 调用会同时设置全局实例（`sync.Once` 保证），此后包级全局函数即可用
- `New` 返回具体类型 `*SLogger`（完整实现 `Logger` 接口），调用方持有具体类型可避免接口分发开销

路径语义：

- `configPath` 按传入值原样使用，找不到配置文件时 `New` 报错
- `log.file.path` 为绝对路径时原样使用；相对路径基于**进程工作目录**（调用工程所在目录）解析为绝对路径，日志目录不存在时自动创建（含多级目录）
- **工作目录异常直接报错**：工作目录获取失败、或位于系统临时目录（编辑器以 tmp 目录启动进程的常见现象）时，`New` 返回错误而不是把日志写进临时目录

### 全局函数（推荐）

```go
package main

import (
    logger "github.com/streasure/treasure-slog"
)

func main() {
    // 显式初始化：首次 New 设置全局实例
    if _, err := logger.New("configs/config.yaml"); err != nil {
        panic(err)
    }
    defer logger.Sync() // 退出前必须调用，确保异步日志落盘

    logger.Info("应用启动", "version", "1.0.0", "env", "production")
    logger.Warn("警告信息", "threshold", 80)
    logger.Error("错误信息", "error", "connection failed")
}
```

### 实例用法

```go
log, err := logger.New("configs/config.yaml")
if err != nil {
    panic(err)
}
defer log.Sync()

// New 返回 *SLogger，可直接调用全部方法
log.Info("使用实例记录日志")

// With 派生带固定字段的 logger（派生实例共享级别/队列/worker 等运行时状态）
userLog := log.With("user_id", "12345", "ip", "192.168.1.1")
userLog.Info("用户登录")
userLog.Info("用户操作", "action", "buy")
// {"level":"INFO","msg":"用户登录","user_id":"12345","ip":"192.168.1.1"}
```

### Context 追踪注入

```go
ctx := context.Background()
ctx = context.WithValue(ctx, "request_id", "req-abc-123")
ctx = context.WithValue(ctx, "trace_id", "trace-xyz-789")

ctxLog := logger.WithContext(ctx)
ctxLog.Info("处理请求")
// {"level":"INFO","msg":"处理请求","request_id":"req-abc-123","trace_id":"trace-xyz-789"}
```

### 运行示例

仓库内置可运行示例（均支持可选位置参数指定配置路径）：

```bash
go run ./cmd                          # 综合演示（默认 configs/config.yaml）
go run ./examples/basic configs/config.dev.yaml
go run ./examples/http_server         # HTTP 服务示例（监听 :8080）
go run ./examples/high_throughput     # 100 万条高吞吐测试
go run ./examples/global              # 全局函数用法
```

Windows 下也可使用 `start.bat` / `start-examples.bat`。

## API 一览

### Logger 接口（`*SLogger` 完整实现）

| 方法 | 说明 |
|------|------|
| `Debug / Info / Warn / Error(msg, args...)` | 基础日志（args 为 key-value 成对，奇数个自动补 `<missing-value>`） |
| `DebugContext / InfoContext / ...` | 携带 context 版本 |
| `With(args...) Logger` | 派生带固定字段的 logger |
| `WithContext(ctx) Logger` | 派生自动注入 context 追踪字段的 logger |
| `AddHook(hook) Logger` | 追加自定义 Hook，返回新 logger |
| `SetLevel(level) / GetLevel()` | 运行时动态级别（派生 logger 共享） |
| `Sync() error` | 刷盘并优雅关闭（幂等，可安全多次/并发调用） |

### 包级全局函数

`logger.Info(...)` 等全部日志方法、`With` / `WithContext` / `AddHook` / `SetLevel` / `GetLevel` / `Sync` / `Recover`，在首次 `New` 之后生效。

### Panic 恢复

```go
func riskyOperation() {
    defer logger.Recover() // 捕获 panic，记录 error 日志与堆栈，程序继续执行
    panic("something went wrong")
}
```

### 自定义 Hook

```go
type MetricsHook struct {
    counter map[string]int
}

// Hook 接口：每条被处理的日志都会回调（级别过滤之后）
func (h *MetricsHook) Run(msg string, level string, args ...any) {
    h.counter[level]++
}

hookedLog := logger.AddHook(&MetricsHook{counter: make(map[string]int)})
```

### 耗时诊断（性能调优）

```go
logger.EnableTiming(true)   // 开启各阶段耗时统计
defer logger.DumpTiming()   // 输出到 stderr：级别检查/入队/序列化/写入等分项耗时
```

## 配置参考

### 完整配置项（含默认值）

```yaml
log:
  # 日志级别: debug | info | warn | error（默认 info）
  level: info
  # 日志格式: json（FastHandler 零反射）| console / text（标准 TextHandler）
  format: json

  # 异步配置（整个 async 块缺省时默认启用，走默认值）
  async:
    enabled: true            # false 时为同步模式
    buffer_size: 10000       # 环形缓冲区容量（向上取整到 2 的幂，按 worker 分片）
    batch_size: 100          # worker 单批处理条数
    flush_interval: 100      # 刷新间隔（毫秒）
    workers: 4               # worker 数量

  # 控制台输出
  console:
    enabled: true            # 默认 false
    format: text             # 缺省时与 log.format 一致；不同时启用独立 handler（如文件 JSON + 控制台 TEXT）

  # 文件输出（目录不存在时自动创建）
  file:
    enabled: true
    path: ./logs/app.log     # 绝对路径原样使用；相对路径基于进程工作目录解析，目录自动创建
    rotate:
      max_size: 100          # 单文件上限（MB），默认 100
      max_backups: 10         # 保留备份数，默认 10
      max_age: 30             # 保留天数，默认 30
      compress: false         # 旧文件 gzip 异步压缩
      interval: 0s            # 时间轮转间隔（如 24h），0 = 禁用

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
    enabled: true
    level: error              # error | warn | info | debug
    depth: 10                 # 堆栈深度，默认 10

  # 日志采样
  sampling:
    enabled: false
    initial: 1000             # 前 N 条全量，默认 1000
    thereafter: 100           # 之后每 N 条记录 1 条，默认 100

  # 性能开关
  performance:
    lock_free: true           # 启用时 worker 数下限提升到 4
    use_pool: true            # logEntry / 序列化缓冲区对象池
    prealloc: true            # 环形缓冲区容量加倍，预分配
```

### 预置环境配置（configs/ 目录）

| 配置文件 | 场景 | 要点 |
|----------|------|------|
| `config.dev.yaml` | 开发 | 同步模式 + 控制台 text + debug 级别 |
| `config.yaml` | 通用 | 异步 + 文件输出 + 采样 + error 堆栈 |
| `config.prod.yaml` | 生产 | 异步 8 worker + 文件轮转压缩 + 采样 |
| `config.highperf.yaml` | 极限吞吐 | 1M 缓冲 16 worker + 全部性能开关 |
| `config.large.yaml` | 大日志（单条 >1MB） | 小缓冲小批量 + 禁压缩 + 采样 |

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

### 多 Logger 实例

```go
businessLog, _ := logger.New("configs/business.yaml")
defer businessLog.Sync()

auditLog, _ := logger.New("configs/audit.yaml")
defer auditLog.Sync()

businessLog.Info("订单创建", "order_id", "123")
auditLog.Info("用户登录", "user_id", "456")
```

> 注意：仅**首次** `New` 设置全局实例；多实例场景建议直接持有各 `*SLogger` 使用。

## 架构

```
业务调用 logger.Info(msg, k1, v1, ...)
        │
        ▼
   级别快速过滤（atomic load，未达级别直接返回）
        │
        ├── 同步模式 ──► processEntryDirect ──► FastHandler 序列化 ──► bufio 批量写入 ──► 控制台/文件/网络
        │
        └── 异步模式 ──► sync.Pool 取 logEntry ──► 分片环形缓冲区（round-robin 选 shard）
                                    │
                                    ▼
                    worker（每 shard 一个消费者）批量弹出
                                    │
                                    ▼
                    Hook 执行 ──► FastHandler 序列化 ──► batchWriter 聚合写入
```

核心组件：

| 组件 | 职责 |
|------|------|
| `SLogger` | 主记录器，持有级别（atomic 指针，派生 logger 共享）、hook、writer、worker |
| `ringBuffer` | 分片环形缓冲区：shard 数 = worker 数，生产者 round-robin 选 shard，单 shard 为 MPSC，锁临界区最小化；缓存行填充防伪共享；缓冲满自动降级同步处理 |
| `worker` | 每 worker 独占一个 shard 消费；批量弹出 + 定时刷新 + 空闲退避（空闲时 CPU 占用趋近 0） |
| `FastHandler` | 零反射 JSON 序列化：手写 appendJSONString / appendInt 等，512B 缓冲池化；纯序列化 0 分配 |
| `batchWriter` | 聚合多条日志一次写入，按大小阈值与时间间隔双触发刷盘 |
| `networkWriter` / `httpWriter` | 网络输出，失败自动重连/重试 |
| `SamplingHandler` | 环绕式采样（前 N 条全量 + 之后按频率） |

可靠性设计：

- 缓冲区满降级同步写入，日志不丢失
- 全链路 recover：log / worker / hook / 序列化各层 panic 均被捕获并记录到 stderr
- `Sync()` 幂等（`sync.Once`），并发调用安全；退出前调用可保证队列中日志全部落盘

## 性能实测

> 环境：Intel i5-10400F @ 2.90GHz（12 逻辑核），Windows，Go 1.22.5，`benchtime=1s`
> 下列 QPS 为 `RunParallel` 12 goroutine 聚合吞吐（io.Discard，纯管线；FullLink 除外）

### 核心指标

| 场景 | 每条耗时 | 吞吐 | 内存/条 | 分配次数 |
|------|---------|------|--------|---------|
| 纯序列化（直接调 FastHandler） | ~42 ns | ~2900 万/s | 0 B | **0** |
| 同步模式（全级别混合） | ~70 ns | ~1750 万/s | 32 B | 1 |
| 极限吞吐（Info 单参数） | ~81 ns | ~1400 万/s | 3 B | **0** |
| 异步模式（全级别混合） | ~157 ns | ~790 万/s | 81 B | 2 |
| 全接口混合（8 种调用方式） | ~150 ns | ~740 万/s | 82 B | 2 |
| 全链路（真实文件轮转 I/O） | ~1.0-1.4 µs | ~950 万/s | — | — |

### 高并发稳定性（异步管线）

| 并发 goroutine | 8 | 16 | 32 | 64 | 128 | 256 |
|----------------|-----|-----|-----|-----|------|-----|
| 吞吐（万/s） | ~800 | ~850 | ~870 | ~900 | ~840 | ~880 |

分片设计下 8~256 并发吞吐波动小于 ±10%，无明显锁竞争衰减。

### Worker 扩展性（异步管线）

| Worker 数 | 1 | 2 | 4 | 8 | 16 | 32 |
|-----------|-----|-----|-----|-----|-----|-----|
| 吞吐（万/s） | ~630 | ~980 | ~1000 | ~990 | ~910 | ~960 |

2~4 worker 即可打满消费能力，更多 worker 用于削峰。

### 复现命令

```bash
go test -run=^$ -bench "BenchmarkPureSerialization|BenchmarkAllLevelsAsync$|BenchmarkAllLevelsSync$|BenchmarkThroughputMax|BenchmarkAllInterfaces10M|BenchmarkFullLink" -benchmem
go test -run=^$ -bench "BenchmarkExtremeConcurrency|BenchmarkWorkerScalability" -benchtime=1s
```

## 性能调优建议

1. **控制字段数量**：字段 ≤5 时序列化与参数传递开销最小
2. **生产环境禁用控制台**：`console.enabled: false`，多路输出有聚合开销
3. **高频日志启用采样**：`sampling.enabled: true`，优先在入口丢弃
4. **异步参数按内存预算配置**：`buffer_size × 单条日志大小 ≈ 峰值内存占用`；大日志场景用小缓冲小批量（参考 `configs/config.large.yaml`）
5. **worker 不必过多**：2~4 worker 通常已打满（见扩展性数据），高并发场景按 CPU 核数 1~2 倍
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
- 确认采样配置：`sampling.thereafter` 过小会丢弃大部分日志
- 退出前调用 `Sync()`，否则异步队列中的日志可能未落盘

**日志文件位置不对？**
- 相对路径基于进程工作目录（调用工程所在目录）解析，检查启动时的 CWD；需要固定位置时使用绝对路径
- 若报错 `working directory ... is inside the system temp dir`：进程是从系统临时目录启动的（常见于编辑器运行配置），请把运行工作目录改为工程目录，或配置文件中改用绝对路径

**性能不达标？**
- 参见[性能调优建议](#性能调优建议)；用 `EnableTiming` 定位慢阶段
- 文件场景检查磁盘 I/O 与轮转/压缩配置（`compress: true` 会消耗 CPU）

**内存占用高？**
- 减小 `buffer_size`；关闭 `prealloc`
- 大日志场景参考 `configs/config.large.yaml` 的小缓冲小批量配置

## 许可证

MIT License
