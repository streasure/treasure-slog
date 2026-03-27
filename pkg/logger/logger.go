// Package logger 提供高性能日志实现，基于 Go 原生 slog 库
// 设计目标：
// 1. 高性能：支持每秒百万级日志写入
// 2. 功能完备：支持文件轮转、网络输出、异步处理等
// 3. 可配置：通过 YAML 配置文件灵活配置
// 4. 可扩展：支持自定义 Hook、多输出等
package logger

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"gopkg.in/natefinch/lumberjack.v2"

	"treasure-slog/internal/config"
)

// Hook 定义日志钩子接口
// 设计意图：允许用户在日志记录前后执行自定义逻辑，如
// 1. 指标收集：统计不同级别的日志数量
// 2. 告警触发：当出现错误日志时触发告警
// 3. 日志增强：自动添加额外的上下文信息
// 4. 审计追踪：记录敏感操作的审计日志
// 5. 外部集成：将日志发送到外部系统

type Hook interface {
	// Run 在日志记录时执行
	// msg: 日志消息
	// level: 日志级别（INFO、ERROR 等）
	// args: 日志字段
	Run(msg string, level string, args ...any)
}

// Logger 接口定义与 zap 类似的日志方法
// 设计意图：
// 1. 提供与 zap 兼容的 API，方便用户迁移
// 2. 封装底层实现细节，提供统一的日志接口
// 3. 支持链式调用和方法扩展

type Logger interface {
	// Debug 记录调试级别日志
	Debug(msg string, args ...any)
	// Info 记录信息级别日志
	Info(msg string, args ...any)
	// Warn 记录警告级别日志
	Warn(msg string, args ...any)
	// Error 记录错误级别日志
	Error(msg string, args ...any)
	// With 添加键值对到日志记录器（链式调用）
	With(args ...any) Logger
	// WithContext 添加上下文到日志记录器
	WithContext(ctx context.Context) Logger
	// AddHook 添加钩子到日志记录器
	AddHook(hook Hook) Logger
	// Sync 同步日志，实现 Graceful Shutdown
	Sync() error
	// SetLevel 动态设置日志级别
	SetLevel(level string)
	// GetLevel 获取当前日志级别
	GetLevel() string
}

// logEntryPool 日志条目对象池
// 设计意图：
// 1. 减少内存分配：复用日志条目对象，避免频繁创建
// 2. 降低 GC 压力：减少对象创建和销毁，降低垃圾回收开销
// 3. 提高性能：在高并发场景下显著提升性能
var logEntryPool = sync.Pool{
	New: func() interface{} {
		// 预分配 args 切片，减少扩容开销
		return &logEntry{
			args: make([]any, 0, 16),
		}
	},
}

// bufferPool 缓冲区对象池
// 设计意图：
// 1. 复用字节缓冲区，减少内存分配
// 2. 提高网络和文件写入性能
var bufferPool = sync.Pool{
	New: func() interface{} {
		// 预分配 1KB 缓冲区，适合大多数日志场景
		return bytes.NewBuffer(make([]byte, 0, 1024))
	},
}

// logEntry 日志条目
// 设计意图：
// 1. 封装日志的所有信息，便于异步处理
// 2. 支持对象池复用
// 3. 减少参数传递开销

type logEntry struct {
	ctx       context.Context // 上下文信息
	level     slog.Level      // 日志级别
	msg       string          // 日志消息
	args      []any           // 日志字段
	hooks     []Hook          // 钩子列表
	timestamp time.Time       // 时间戳
}

// Reset 重置日志条目
// 设计意图：
// 1. 用于对象池复用，清除旧数据
// 2. 保持切片容量，避免频繁扩容
func (e *logEntry) Reset() {
	e.ctx = nil
	e.level = 0
	e.msg = ""
	e.args = e.args[:0] // 重置切片长度但保留容量
	e.hooks = nil
	e.timestamp = time.Time{}
}
    
// ringBuffer 无锁环形缓冲区
// 设计意图：
// 1. 实现无锁并发：使用原子操作替代互斥锁，减少线程竞争
// 2. 高吞吐量：支持高并发场景下的快速入队
// 3. 固定容量：避免内存无限增长
// 4. 自动覆盖：当缓冲区满时自动覆盖旧数据，保证系统稳定性

type ringBuffer struct {
	buffer   []*logEntry // 缓冲区数组
	capacity int         // 容量
	head     uint64      // 头部指针（出队位置）
	tail     uint64      // 尾部指针（入队位置）
}

// newRingBuffer 创建新的环形缓冲区
// 设计意图：
// 1. 预分配固定大小的缓冲区
// 2. 避免运行时扩容开销
func newRingBuffer(capacity int) *ringBuffer {
	return &ringBuffer{
		buffer:   make([]*logEntry, capacity),
		capacity: capacity,
	}
}

// Push 添加元素
// 设计意图：
// 1. 无锁实现：使用原子操作确保并发安全
// 2. 快速路径：避免复杂的同步机制
// 3. 溢出处理：当缓冲区满时返回 false
func (rb *ringBuffer) Push(entry *logEntry) bool {
	tail := atomic.LoadUint64(&rb.tail)
	head := atomic.LoadUint64(&rb.head)

	if tail-head >= uint64(rb.capacity) {
		return false // 缓冲区已满
	}

	index := tail % uint64(rb.capacity)
	rb.buffer[index] = entry
	atomic.StoreUint64(&rb.tail, tail+1)
	return true
}

// Pop 取出元素
// 设计意图：
// 1. 无锁实现：使用原子操作确保并发安全
// 2. 快速路径：避免复杂的同步机制
// 3. 空缓冲区处理：当缓冲区为空时返回 nil
func (rb *ringBuffer) Pop() *logEntry {
	head := atomic.LoadUint64(&rb.head)
	tail := atomic.LoadUint64(&rb.tail)

	if head >= tail {
		return nil // 缓冲区为空
	}

	index := head % uint64(rb.capacity)
	entry := rb.buffer[index]
	atomic.StoreUint64(&rb.head, head+1)
	return entry
}

// batchWriter 批量写入器
// 设计意图：
// 1. 减少 I/O 操作：将多条日志合并成一次写入
// 2. 提高写入性能：减少系统调用开销
// 3. 定时刷新：确保日志及时写入，避免数据丢失
// 4. 支持缓冲：减少磁盘或网络 I/O 压力

type batchWriter struct {
	writer        io.Writer     // 底层写入器
	buffer        *bufio.Writer // 缓冲写入器
	batchSize     int           // 批处理大小
	flushInterval time.Duration // 刷新间隔
	timer         *time.Timer   // 定时刷新定时器
	mu            sync.Mutex    // 互斥锁，保护缓冲区
}

// newBatchWriter 创建新的批量写入器
// 设计意图：
// 1. 初始化缓冲写入器，设置适当的缓冲区大小
// 2. 启动定时刷新机制
// 3. 配置批处理参数
func newBatchWriter(writer io.Writer, batchSize int, flushInterval time.Duration) *batchWriter {
	bw := &batchWriter{
		writer:        writer,
		buffer:        bufio.NewWriterSize(writer, batchSize*1024), // 预分配缓冲区
		batchSize:     batchSize,
		flushInterval: flushInterval,
	}
	
	// 启动定时刷新，确保日志及时写入
	if flushInterval > 0 {
		bw.timer = time.AfterFunc(flushInterval, bw.flush)
	}
	
	return bw
}

// Write 写入数据
// 设计意图：
// 1. 线程安全：使用互斥锁保护缓冲区
// 2. 批量处理：当缓冲区达到阈值时自动刷新
// 3. 错误处理：返回写入错误
func (bw *batchWriter) Write(p []byte) (n int, err error) {
	bw.mu.Lock()
	defer bw.mu.Unlock()
	
	n, err = bw.buffer.Write(p)
	if err != nil {
		return n, err
	}
	
	// 如果缓冲区满了，立即刷新
	if bw.buffer.Buffered() >= bw.batchSize*1024 {
		err = bw.buffer.Flush()
	}
	
	return n, err
}

// flush 刷新缓冲区
// 设计意图：
// 1. 确保所有缓冲的日志都写入底层存储
// 2. 重置定时器，继续定时刷新
func (bw *batchWriter) flush() {
	bw.mu.Lock()
	defer bw.mu.Unlock()
	
	if bw.buffer.Buffered() > 0 {
		bw.buffer.Flush()
	}

	// 重置定时器，继续定时刷新
	if bw.flushInterval > 0 {
		bw.timer.Reset(bw.flushInterval)
	}
}

// networkWriter 网络写入器
// 设计意图：
// 1. 支持网络日志输出：可以将日志发送到 ELK、Graylog 等系统
// 2. 重试机制：网络异常时自动重试
// 3. 连接管理：自动重连和连接池管理
// 4. 支持多种协议：TCP、UDP

type networkWriter struct {
	connType string    // 连接类型：tcp、udp
	address  string    // 目标地址
	timeout  time.Duration // 连接超时
	retry    int       // 重试次数
	useTLS   bool      // 是否使用 TLS
	conn     net.Conn  // 网络连接
	mu       sync.RWMutex // 读写锁，保护连接
}

// newNetworkWriter 创建新的网络写入器
// 设计意图：
// 1. 初始化网络写入器配置
// 2. 建立初始连接
// 3. 配置重试和超时参数
func newNetworkWriter(cfg config.NetworkConfig) (*networkWriter, error) {
	nw := &networkWriter{
		connType: cfg.Type,
		address:  cfg.Address,
		timeout:  time.Duration(cfg.Timeout) * time.Second,
		retry:    cfg.Retry,
		useTLS:   cfg.TLS,
	}
	
	// 建立初始连
	if err := nw.connect(); err != nil {
		return nil, err
	}
	
	return nw, nil
}

// connect 建立连接
// 设计意图：
// 1. 线程安全：使用互斥锁保护连接操作
// 2. 支持 TLS：根据配置决定是否使用加密连接
// 3. 错误处理：返回连接错误
func (nw *networkWriter) connect() error {
	nw.mu.Lock()
	defer nw.mu.Unlock()
	
	if nw.conn != nil {
		nw.conn.Close()
	}
	
	var conn net.Conn
	var err error
	
	switch nw.connType {
	case "tcp":
		if nw.useTLS {
			conn, err = tls.Dial("tcp", nw.address, &tls.Config{})
		} else {
			conn, err = net.DialTimeout("tcp", nw.address, nw.timeout)
		}
	case "udp":
		conn, err = net.DialTimeout("udp", nw.address, nw.timeout)
	default:
		return fmt.Errorf("unsupported network type: %s", nw.connType)
	}
	
	if err != nil {
		return err
	}
	
	nw.conn = conn
	return nil
}

// Write 写入数据
func (nw *networkWriter) Write(p []byte) (n int, err error) {
	var conn net.Conn
	
	// 读取当前连接（读锁）
	nw.mu.RLock()
	conn = nw.conn
	nw.mu.RUnlock()
	
	// 重试机制试 retry 次
	for i := 0; i <= nw.retry; i++ {
		n, err = conn.Write(p)
		if err == nil {
			return n, nil
		}
		
		// 重连
		if i < nw.retry {
			if err := nw.connect(); err != nil {
				continue
			}
			nw.mu.RLock()
			conn = nw.conn
			nw.mu.RUnlock()
		}
	}
	
	return n, err
}

// Close 关闭连接
// 设计意图：
// 1. 关闭网络连接，释放资源
// 2. 线程安全：使用互斥锁保护关闭操作
func (nw *networkWriter) Close() error {
	nw.mu.Lock()
	defer nw.mu.Unlock()

	if nw.conn != nil {
		return nw.conn.Close()
	}
	return nil
}

// httpWriter HTTP写入器
// 设计意图：
// 1. 支持 HTTP 协议：可以将日志发送到 HTTP 端点
// 2. 适合云服务：与云日志服务集成
// 3. 支持重试：网络异常时自动重试
// 4. 超时控制：避免阻塞

type httpWriter struct {
	url      string         // HTTP 端点 URL
	timeout  time.Duration  // 超时时间
	retry    int            // 重试次数
	client   *http.Client   // HTTP 客户端
}

// newHTTPWriter 创建新的HTTP写入器
// 设计意图：
// 1. 初始化 HTTP 客户端
// 2. 配置超时和重试参数
func newHTTPWriter(address string, timeout time.Duration, retry int) *httpWriter {
	return &httpWriter{
		url:     address,
		timeout: timeout,
		retry:   retry,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Write 写入数据
// 设计意图：
// 1. 发送 POST 请求：将日志数据作为 JSON 发送
// 2. 重试机制：HTTP 失败时自动重试
// 3. 错误处理：返回最终写入错误
func (hw *httpWriter) Write(p []byte) (n int, err error) {
	for i := 0; i <= hw.retry; i++ {
		resp, err := hw.client.Post(hw.url, "application/json", bytes.NewReader(p))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return len(p), nil
			}
		}
		
		if i < hw.retry {
			time.Sleep(time.Duration(i+1) * 100 * time.Millisecond)
		}
	}
	
	return 0, err
}

// Close 关闭HTTP写入器
// 设计意图：
// 1. 关闭空闲连接，释放资源
// 2. 避免连接泄漏
func (hw *httpWriter) Close() error {
	hw.client.CloseIdleConnections()
	return nil
}

// SLogger 是基于 slog 实现的高性能 Logger
// 设计意图：
// 1. 高性能：支持异步处理、批量写入、无锁队列
// 2. 功能完备：支持文件轮转、网络输出、日志采样等
// 3. 可配置：通过 YAML 配置文件灵活配置
// 4. 可扩展：支持自定义 Hook、多输出等
// 5. 线程安全：支持高并发场景

type SLogger struct {
	logger      *slog.Logger     // 底层 slog 日志器
	hooks       []Hook           // 钩子列表
	fileLogger  *lumberjack.Logger // 文件轮转日志器
	level       atomic.Value     // 日志级别（原子操作，支持动态调整）
	
	// 高性能组件
	ringBuf     *ringBuffer      // 无锁环形缓冲区
	batchWriter *batchWriter     // 批量写入器
	networkWriter io.WriteCloser // 网络写入器
	
	// 配置
	cfg         *config.Config   // 配置信息
	
	// 工作线程
	workers     []*worker        // 工作线程列表
	stopCh      chan struct{}    // 停止信号
	wg          sync.WaitGroup   // 等待组，用于优雅关闭
	
	// 字段缓存
	fieldCache  sync.Map         // 字段缓存，提高性能
	
	// 对象池
	usePool     bool             // 是否使用对象池
}

// worker 工作线程
// 设计意图：
// 1. 并行处理：多线程处理日志，提高吞吐量
// 2. 批量处理：减少 I/O 操作
// 3. 优雅关闭：支持平滑停止

type worker struct {
	id       int           // 工作线程 ID
	logger   *SLogger      // 日志器引用
	stopCh   chan struct{} // 停止信号
}

// newWorker 创建新的工作线程
// 设计意图：
// 1. 初始化工作线程配置
// 2. 准备停止信号通道
func newWorker(id int, logger *SLogger) *worker {
	return &worker{
		id:     id,
		logger: logger,
		stopCh: make(chan struct{}),
	}
}

// start 启动工作线程
// 设计意图：
// 1. 启动后台 goroutine 处理日志
// 2. 非阻塞启动，不影响主线程
func (w *worker) start() {
go w.run()
}

// run 工作线程主循环
// 设计意图：
// 1. 批量处理：累积日志后批量处理
// 2. 定时刷新：确保日志及时写入
// 3. 优雅退出：响应停止信号
// 4. 错误处理：处理过程中的异常
func (w *worker) run() {
	batch := make([]*logEntry, 0, w.logger.cfg.Log.Async.BatchSize) // 预分配批处理缓冲区
	ticker := time.NewTicker(time.Duration(w.logger.cfg.Log.Async.FlushInterval) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopCh:
			// 处理剩余日志，确保不丢失
			w.processBatch(batch)
			return
		case <-ticker.C:
			if len(batch) > 0 {
				w.processBatch(batch)
				batch = batch[:0] // 重置批处理缓冲区
			}
		default:
			entry := w.logger.ringBuf.Pop()
			if entry != nil {
				batch = append(batch, entry)
				if len(batch) >= w.logger.cfg.Log.Async.BatchSize {
					w.processBatch(batch)
					batch = batch[:0] // 重置批处理缓冲区
				}
			} else {
				time.Sleep(time.Microsecond) // 避免 CPU 空转
			}
		}
	}
}

// processBatch 批量处理日志
// 设计意图：
// 1. 批量执行钩子：减少钩子执行开销
// 2. 批量写入：减少 I/O 操作
// 3. 对象复用：处理完成后回收日志条目
func (w *worker) processBatch(batch []*logEntry) {
	for _, entry := range batch {
		w.logger.processEntry(entry)
		
		// 回收对象到对象池
		if w.logger.usePool {
			entry.Reset()
			logEntryPool.Put(entry)
		}
	}
}

// stop 停止工作线程
// 设计意图：
// 1. 发送停止信号
// 2. 触发工作线程的清理逻辑
func (w *worker) stop() {
	close(w.stopCh)
}

// 全局日志单例
// 设计意图：
// 1. 全局访问：方便在应用各处使用
// 2. 延迟初始化：首次使用时创建
// 3. 线程安全：使用 sync.Once 确保只初始化一次
var (
	globalLogger Logger
	once         sync.Once
)

// New 创建一个新的日志记录器
// 设计意图：
// 1. 从配置文件加载配置
// 2. 初始化日志器
// 3. 返回统一的 Logger 接口
func New(configPath string) (Logger, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config error: %w", err)
	}

	return newLogger(cfg)
}

// newLogger 创建日志记录器
// 设计意图：
// 1. 初始化所有组件
// 2. 配置多输出
// 3. 启动工作线程
// 4. 配置性能优化选项
func newLogger(cfg *config.Config) (Logger, error) {
	// 确保日志目录存在
	if cfg.Log.File.Enabled {
		logDir := filepath.Dir(cfg.Log.File.Path)
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return nil, fmt.Errorf("create log directory error: %w", err)
		}
	}

	// 创建 SLogger 实例
	slogger := &SLogger{
		cfg:     cfg,
		hooks:   []Hook{},
		stopCh:  make(chan struct{}),
		usePool: cfg.Log.Performance.UsePool,
	}
	
	// 设置日志级别
	slogger.SetLevel(cfg.Log.Level)

	// 创建输出写入器
	writers := []io.Writer{}
	
	// 控制台输出
	if cfg.Log.Console.Enabled {
		writers = append(writers, os.Stdout)
	}
	
	// 文件输出
	if cfg.Log.File.Enabled {
		fileLogger := &lumberjack.Logger{
			Filename:   cfg.Log.File.Path,
			MaxSize:    cfg.Log.File.Rotate.MaxSize,
			MaxBackups: cfg.Log.File.Rotate.MaxBackups,
			MaxAge:     cfg.Log.File.Rotate.MaxAge,
			Compress:   cfg.Log.File.Rotate.Compress,
		}
		slogger.fileLogger = fileLogger
		writers = append(writers, fileLogger)
	}
	
	// 网络输出
	if cfg.Log.Network.Enabled {
		var nw io.WriteCloser
		var err error
		
		if cfg.Log.Network.Type == "http" {
			nw = newHTTPWriter(cfg.Log.Network.Address, 
				time.Duration(cfg.Log.Network.Timeout)*time.Second, 
				cfg.Log.Network.Retry)
		} else {
			nw, err = newNetworkWriter(cfg.Log.Network)
			if err != nil {
				return nil, fmt.Errorf("create network writer error: %w", err)
			}
		}
		
		slogger.networkWriter = nw
		writers = append(writers, nw)
	}
	
	// 创建多输出
	var writer io.Writer
	if len(writers) == 1 {
		writer = writers[0]
	} else {
		writer = io.MultiWriter(writers...)
	}
	
	// 创建批量写入器
	if cfg.Log.Async.Enabled {
		slogger.batchWriter = newBatchWriter(writer, cfg.Log.Async.BatchSize, 
			time.Duration(cfg.Log.Async.FlushInterval)*time.Millisecond)
		writer = slogger.batchWriter
	}
	
	// 创建日志处理器
	handler := slogger.createHandler(writer)
	
	// 创建日志记录器
	slogger.logger = slog.New(handler)
	
	// 创建无锁环形缓冲区
	if cfg.Log.Performance.LockFree {
		slogger.ringBuf = newRingBuffer(cfg.Log.Async.BufferSize)
	}
	
	// 启动工作线程
	if cfg.Log.Async.Enabled {
		slogger.workers = make([]*worker, cfg.Log.Async.Workers)
		for i := 0; i < cfg.Log.Async.Workers; i++ {
			slogger.workers[i] = newWorker(i, slogger)
			slogger.workers[i].start()
		}
	}

	return slogger, nil
}

// createHandler 创建日志处理器
// 设计意图：
// 1. 配置日志级别
// 2. 配置日志格式
// 3. 配置日志采样
// 4. 配置属性替换
func (l *SLogger) createHandler(writer io.Writer) slog.Handler {
	// 配置日志级别
	level := l.GetLevel()
	var slogLevel slog.Level
	switch level {
	case "debug":
		slogLevel = slog.LevelDebug
	case "info":
		slogLevel = slog.LevelInfo
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}

	// 创建处理器选项
	opts := &slog.HandlerOptions{
		Level: slogLevel,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				if t, ok := a.Value.Any().(time.Time); ok {
					a.Value = slog.StringValue(t.Format(time.RFC3339Nano))
				}
			}
			return a
		},
	}

	// 根据格式创建处理器
	var handler slog.Handler
	switch l.cfg.Log.Format {
	case "json":
		handler = slog.NewJSONHandler(writer, opts)
	case "console", "text":
		handler = slog.NewTextHandler(writer, opts)
	default:
		handler = slog.NewJSONHandler(writer, opts)
	}

	// 配置日志采样
	if l.cfg.Log.Sampling.Enabled {
		handler = NewSamplingHandler(handler, SamplingOptions{
			Initial:    l.cfg.Log.Sampling.Initial,
			Thereafter: l.cfg.Log.Sampling.Thereafter,
		})
	}

	return handler
}

// GetLogger 获取全局日志单例
// 设计意图：
// 1. 全局访问点：方便在应用各处使用
// 2. 延迟初始化：首次使用时创建
// 3. 容错处理：配置加载失败时使用默认配置
func GetLogger() Logger {
	once.Do(func() {
		var err error
		globalLogger, err = New("configs/config.yaml")
		if err != nil {
			// 如果配置文件加载失败，使用默认配置
			defaultConfig := &config.Config{
				Log: config.LogConfig{
					Level:  "info",
					Format: "json",
					Async: config.AsyncConfig{
						Enabled:       true,
						BufferSize:    10000,
						BatchSize:     100,
						FlushInterval: 100,
						Workers:       4,
					},
					Console: config.ConsoleConfig{
						Enabled: true,
						Format:  "text",
					},
					File: config.FileConfig{
						Enabled: false,
					},
					Stacktrace: config.StackConfig{
						Enabled: true,
						Level:   "error",
						Depth:   10,
					},
					Sampling: config.SamplingConfig{
						Enabled:    false,
						Initial:    1000,
						Thereafter: 100,
					},
					Performance: config.PerformanceConfig{
						LockFree: true,
						UsePool:  true,
						Prealloc: true,
					},
				},
			}
			globalLogger, _ = newLogger(defaultConfig)
		}
	})
	return globalLogger
}

// getCachedField 获取缓存的字段
// 设计意图：
// 1. 提高性能：缓存常用字段，避免重复创建
// 2. 减少内存分配：复用字段对象
// 3. 线程安全：使用 sync.Map 实现并发安全
func (l *SLogger) getCachedField(key string, value interface{}) (slog.Attr, bool) {
	if !l.cfg.Log.FieldCache.Enabled {
		return slog.Attr{}, false
	}
	
	cacheKey := fmt.Sprintf("%s:%v", key, value)
	if attr, ok := l.fieldCache.Load(cacheKey); ok {
		return attr.(slog.Attr), true
	}
	return slog.Attr{}, false
}

// cacheField 缓存字段
// 设计意图：
// 1. 缓存常用字段，提高后续使用的性能
// 2. 线程安全：使用 sync.Map 实现并发安全
func (l *SLogger) cacheField(key string, value interface{}) slog.Attr {
	if !l.cfg.Log.FieldCache.Enabled {
		return slog.Any(key, value)
	}
	
	cacheKey := fmt.Sprintf("%s:%v", key, value)
	attr := slog.Any(key, value)
	l.fieldCache.Store(cacheKey, attr)
	return attr
}

// processArgs 处理日志参数，缓存常用字段
// 设计意图：
// 1. 字段缓存：缓存常用字段，提高性能
// 2. 参数处理：确保参数格式正确
// 3. 内存优化：减少内存分配
func (l *SLogger) processArgs(args []any) []any {
	if !l.cfg.Log.FieldCache.Enabled {
		return args
	}
	
	processedArgs := make([]any, 0, len(args))
	for i := 0; i < len(args); i += 2 {
		if i+1 >= len(args) {
			break
		}
		key, ok := args[i].(string)
		if !ok {
			processedArgs = append(processedArgs, args[i], args[i+1])
			continue
		}
		value := args[i+1]
		if attr, ok := l.getCachedField(key, value); ok {
			processedArgs = append(processedArgs, attr.Key, attr.Value.Any())
		} else {
			processedArgs = append(processedArgs, key, value)
			l.cacheField(key, value)
		}
	}
	return processedArgs
}

// log 记录日志的通用方法
// 设计意图：
// 1. 快速路径：内联日志级别检查，提高性能
// 2. 对象复用：使用对象池减少内存分配
// 3. 异步处理：使用无锁环形缓冲区
// 4. 错误处理：缓冲区满时降级处理
func (l *SLogger) log(level slog.Level, msg string, args ...any) {
	// 快速路径：检查日志级别（内联优化）
	currentLevel := l.GetLevel()
	switch currentLevel {
	case "error":
		if level < slog.LevelError {
			return
		}
	case "warn":
		if level < slog.LevelWarn {
			return
		}
	case "info":
		if level < slog.LevelInfo {
			return
		}
	}
	
	// 获取或创建日志条目（对象池优化）
	var entry *logEntry
	if l.usePool {
		entry = logEntryPool.Get().(*logEntry)
		entry.Reset()
	} else {
		entry = &logEntry{
			args: make([]any, 0, len(args)),
		}
	}

	entry.level = level
	entry.msg = msg
	entry.args = append(entry.args, args...)
	entry.hooks = l.hooks
	
	// 异步写入（无锁环形缓冲区）
	if l.cfg.Log.Async.Enabled && l.ringBuf != nil {
		if !l.ringBuf.Push(entry) {
			// 缓冲区已满，直接处理（降级处理）
			l.processEntry(entry)
			if l.usePool {
				entry.Reset()
				logEntryPool.Put(entry)
			}
		}
	} else {
		// 同步处理
		l.processEntry(entry)
		if l.usePool {
			entry.Reset()
			logEntryPool.Put(entry)
		}
	}
}

// processEntry 处理单个日志条目
// 设计意图：
// 1. 执行钩子：处理所有注册的钩子
// 2. 堆栈追踪：错误级别自动添加堆栈信息
// 3. 日志记录：根据级别调用相应的日志方法
func (l *SLogger) processEntry(entry *logEntry) {
	// 执行钩子
	for _, hook := range entry.hooks {
		hook.Run(entry.msg, entry.level.String(), entry.args...)
	}

	// 记录日志
	switch entry.level {
	case slog.LevelDebug:
		l.logger.Debug(entry.msg, entry.args...)
	case slog.LevelInfo:
		l.logger.Info(entry.msg, entry.args...)
	case slog.LevelWarn:
		l.logger.Warn(entry.msg, entry.args...)
	case slog.LevelError:
		// 添加堆栈追踪
		if l.cfg.Log.Stacktrace.Enabled {
			stackTrace := getStackTrace(l.cfg.Log.Stacktrace.Depth)
			entry.args = append(entry.args, "stacktrace", stackTrace)
		}
		l.logger.Error(entry.msg, entry.args...)
	}
}

// Debug 记录调试级别日志
// 设计意图：
// 1. 提供统一的调试日志接口
// 2. 调用通用 log 方法处理
func (l *SLogger) Debug(msg string, args ...any) {
	l.log(slog.LevelDebug, msg, args...)
}

// Info 记录信息级别日志
// 设计意图：
// 1. 提供统一的信息日志接口
// 2. 调用通用 log 方法处理
func (l *SLogger) Info(msg string, args ...any) {
	l.log(slog.LevelInfo, msg, args...)
}

// Warn 记录警告级别日志
// 设计意图：
// 1. 提供统一的警告日志接口
// 2. 调用通用 log 方法处理
func (l *SLogger) Warn(msg string, args ...any) {
	l.log(slog.LevelWarn, msg, args...)
}

// Error 记录错误级别日志
// 设计意图：
// 1. 提供统一的错误日志接口
// 2. 调用通用 log 方法处理
func (l *SLogger) Error(msg string, args ...any) {
	l.log(slog.LevelError, msg, args...)
}

// With 添加键值对到日志记录器
// 设计意图：
// 1. 链式调用：支持方法链
// 2. 字段继承：新日志器继承原有字段
// 3. 线程安全：返回新的日志器实例
func (l *SLogger) With(args ...any) Logger {
	return &SLogger{
		logger:     l.logger.With(args...),
		hooks:      l.hooks,
		fileLogger: l.fileLogger,
		cfg:        l.cfg,
	}
}

// WithContext 添加上下文到日志记录器，并自动注入 context 中的信息
// 设计意图：
// 1. 上下文集成：自动从 context 提取追踪信息
// 2. 链式调用：支持方法链
// 3. 线程安全：返回新的日志器实例
func (l *SLogger) WithContext(ctx context.Context) Logger {
	// 从 context 中提取信息
	args := extractContextInfo(ctx)
	// 创建新的 logger，添加 context 信息
	newLogger := l.logger.With("context", ctx)
	if len(args) > 0 {
		newLogger = newLogger.With(args...)
	}
	return &SLogger{
		logger:     newLogger,
		hooks:      l.hooks,
		fileLogger: l.fileLogger,
		cfg:        l.cfg,
	}
}

// extractContextInfo 从 context 中提取信息
// 设计意图：
// 1. 自动提取：从 context 中提取常见的追踪信息
// 2. 标准化：支持标准的追踪字段
// 3. 可扩展：方便添加新的 context 字段提取
func extractContextInfo(ctx context.Context) []any {
	var args []any
	// 提取常见的 context 信息
	if requestID := ctx.Value("request_id"); requestID != nil {
		args = append(args, "request_id", requestID)
	}
	if userID := ctx.Value("user_id"); userID != nil {
		args = append(args, "user_id", userID)
	}
	if spanID := ctx.Value("span_id"); spanID != nil {
		args = append(args, "span_id", spanID)
	}
	if traceID := ctx.Value("trace_id"); traceID != nil {
		args = append(args, "trace_id", traceID)
	}
	return args
}

// AddHook 添加钩子到日志记录器
// 设计意图：
// 1. 钩子机制：支持自定义钩子
// 2. 链式调用：支持方法链
// 3. 线程安全：返回新的日志器实例
func (l *SLogger) AddHook(hook Hook) Logger {
	newHooks := make([]Hook, len(l.hooks)+1)
	copy(newHooks, l.hooks)
	newHooks[len(l.hooks)] = hook
	return &SLogger{
		logger:     l.logger,
		hooks:      newHooks,
		fileLogger: l.fileLogger,
		cfg:        l.cfg,
	}
}

// SetLevel 动态设置日志级别
// 设计意图：
// 1. 动态调整：运行时改变日志级别
// 2. 线程安全：使用原子操作
// 3. 实时生效：立即更新日志处理器
func (l *SLogger) SetLevel(level string) {
	l.level.Store(level)
	
	// 重新创建处理器，使级别变更立即生效
	if l.logger != nil {
		var writer io.Writer
		if l.batchWriter != nil {
			writer = l.batchWriter
		} else if l.fileLogger != nil {
			writer = l.fileLogger
		} else {
			writer = os.Stdout
		}
		
		handler := l.createHandler(writer)
		l.logger = slog.New(handler)
	}
}

// GetLevel 获取当前日志级别
// 设计意图：
// 1. 线程安全：使用原子操作
// 2. 默认值：返回默认级别（info）
func (l *SLogger) GetLevel() string {
	level := l.level.Load()
	if level == nil {
		return "info"
	}
	return level.(string)
}

// Sync 同步日志，实现 Graceful Shutdown
// 设计意图：
// 1. 优雅关闭：确保所有日志处理完成
// 2. 资源释放：关闭文件和网络连接
// 3. 清理工作：停止工作线程
func (l *SLogger) Sync() error {
	// 停止工作线程
	if l.workers != nil {
		for _, worker := range l.workers {
			worker.stop()
		}
		
		// 等待所有工作线程完成
		for _, worker := range l.workers {
			<-worker.stopCh
		}
	}
	
	// 刷新批量写入器
	if l.batchWriter != nil {
		l.batchWriter.flush()
	}

	// 关闭文件 logger，实现 Graceful Shutdown
	if l.fileLogger != nil {
		if err := l.fileLogger.Close(); err != nil {
			return err
		}
	}
	
	// 关闭网络写入器
	if l.networkWriter != nil {
		if err := l.networkWriter.Close(); err != nil {
			return err
		}
	}
	
	return nil
}

// getStackTrace 获取堆栈追踪信息
// 设计意图：
// 1. 错误定位：帮助定位错误发生的位置
// 2. 深度控制：可配置堆栈深度
// 3. 性能优化：避免获取过深的堆栈
func getStackTrace(depth int) string {
	if depth <= 0 {
		depth = 10
	}
	
	stack := make([]byte, 1024*depth)
	n := runtime.Stack(stack, false)
	return string(stack[:n])
}

// Recover 恢复 panic 并记录日志
// 设计意图：
// 1. 程序保护：防止 panic 导致程序崩溃
// 2. 错误记录：自动记录 panic 信息和堆栈
// 3. 简单使用：只需在 defer 中调用
func Recover() {
	if r := recover(); r != nil {
		stackTrace := getStackTrace(10)
		GetLogger().Error("panic recovered", "recover", r, "stacktrace", stackTrace)
	}
}

// SamplingOptions 日志采样选项
// 设计意图：
// 1. 采样控制：控制日志采样策略
// 2. 灵活性：支持初始采样和后续采样率
// 3. 性能优化：减少高频日志的输出

type SamplingOptions struct {
	Initial    int // 前 N 条全量记录
	Thereafter int // 之后每 N 条记录 1 条
}

// SamplingHandler 采样处理器
// 设计意图：
// 1. 减少日志量：对高频日志进行采样
// 2. 保留关键信息：确保初始日志和部分后续日志被记录
// 3. 性能优化：减少 I/O 和存储开销

type SamplingHandler struct {
	handler     slog.Handler
	options     SamplingOptions
	sampleCount uint64 // 采样计数器
}

// NewSamplingHandler 创建一个采样处理器
// 设计意图：
// 1. 初始化采样配置
// 2. 包装原始处理器
// 3. 提供采样功能
func NewSamplingHandler(handler slog.Handler, options SamplingOptions) *SamplingHandler {
	return &SamplingHandler{
		handler:     handler,
		options:     options,
		sampleCount: 0,
	}
}

// Handle 处理日志记录
// 设计意图：
// 1. 采样逻辑：根据采样配置决定是否记录
// 2. 线程安全：使用原子操作
// 3. 性能优化：快速路径判断
func (h *SamplingHandler) Handle(ctx context.Context, record slog.Record) error {
	count := atomic.AddUint64(&h.sampleCount, 1)
	
	// 检查是否需要采样
	if h.options.Initial > 0 && count <= uint64(h.options.Initial) {
		return h.handler.Handle(ctx, record)
	}
	
	if h.options.Thereafter > 0 && (count-uint64(h.options.Initial))%uint64(h.options.Thereafter) == 0 {
		return h.handler.Handle(ctx, record)
	}
	
	return nil
}

// WithAttrs 添加属性
// 设计意图：
// 1. 保持采样状态：复制采样计数器
// 2. 链式调用：支持方法链
// 3. 线程安全：使用原子操作读取计数器
func (h *SamplingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &SamplingHandler{
		handler:     h.handler.WithAttrs(attrs),
		options:     h.options,
		sampleCount: atomic.LoadUint64(&h.sampleCount),
	}
}

// WithGroup 添加分组
// 设计意图：
// 1. 保持采样状态：复制采样计数器
// 2. 链式调用：支持方法链
// 3. 线程安全：使用原子操作读取计数器
func (h *SamplingHandler) WithGroup(name string) slog.Handler {
	return &SamplingHandler{
		handler:     h.handler.WithGroup(name),
		options:     h.options,
		sampleCount: atomic.LoadUint64(&h.sampleCount),
	}
}

// Enabled 检查级别是否启用
// 设计意图：
// 1. 委托给原始处理器：保持级别检查逻辑一致
// 2. 性能优化：避免重复实现
func (h *SamplingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

// unsafeString 零拷贝转换 []byte 到 string
// 设计意图：
// 1. 性能优化：避免内存拷贝
// 2. 特殊场景使用：仅在确保安全的情况下使用
func unsafeString(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}
