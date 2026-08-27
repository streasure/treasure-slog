// Package logger 提供高性能日志实现，基于 Go 原生 slog 库
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

	"github.com/streasure/treasure-slog/internal/config"
	"github.com/streasure/treasure-slog/internal/writer"
)

// Hook 定义日志钩子接口
type Hook interface {
	Run(msg string, level string, args ...any)
}

// Logger 接口定义日志方法
type Logger interface {
	Debug(msg string, args ...any)
	DebugContext(ctx context.Context, msg string, args ...any)
	Info(msg string, args ...any)
	InfoContext(ctx context.Context, msg string, args ...any)
	Warn(msg string, args ...any)
	WarnContext(ctx context.Context, msg string, args ...any)
	Error(msg string, args ...any)
	ErrorContext(ctx context.Context, msg string, args ...any)
	With(args ...any) Logger
	WithContext(ctx context.Context) Logger
	AddHook(hook Hook) Logger
	Sync() error
	SetLevel(level string)
	GetLevel() string
}

// --- 对象池 ---

var logEntryPool = sync.Pool{
	New: func() any { return &logEntry{} },
}

var fastBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 0, 512)
		return &buf
	},
}

// --- timingStats 核心路径耗时统计 ---

var timing = newTimingStats()

type timingStats struct {
	enabled atomic.Bool

	// 计数器
	logCalls       atomic.Int64 // 总 log() 调用次数
	logFiltered    atomic.Int64 // 级别过滤丢弃次数
	logSyncPath    atomic.Int64 // 同步路径处理次数
	logAsyncPush   atomic.Int64 // 异步推入环形缓冲区次数
	logFallback    atomic.Int64 // 缓冲区满降级同步次数
	batchProcessed atomic.Int64 // 批量处理次数
	entriesHandled atomic.Int64 // 总处理日志条目数

	// 耗时累加（纳秒）
	logTotalNS      atomic.Int64 // log() 总耗时（含级别检查到推入/处理）
	levelCheckNS    atomic.Int64 // 级别检查耗时
	poolGetNS       atomic.Int64 // 对象池获取耗时
	ringBufPushNS   atomic.Int64 // 环形缓冲区 Push 耗时
	syncProcessNS   atomic.Int64 // 同步 processEntryDirect 耗时
	asyncProcessNS  atomic.Int64 // 异步 processEntry 耗时
	hookExecNS      atomic.Int64 // Hook 执行耗时
	slogCallNS      atomic.Int64 // slog.DebugContext/InfoContext 等调用耗时
	handlerHandleNS atomic.Int64 // FastHandler.Handle 序列化+写入耗时
	batchProcessNS  atomic.Int64 // worker.processBatch 总耗时
}

func newTimingStats() *timingStats {
	return &timingStats{}
}

// EnableTiming 开启或关闭耗时统计
func EnableTiming(enable bool) {
	timing.enabled.Store(enable)
}

// ResetTiming 重置所有统计计数器（不影响 enabled 状态）
func ResetTiming() {
	timing.logCalls.Store(0)
	timing.logFiltered.Store(0)
	timing.logSyncPath.Store(0)
	timing.logAsyncPush.Store(0)
	timing.logFallback.Store(0)
	timing.batchProcessed.Store(0)
	timing.entriesHandled.Store(0)
	timing.logTotalNS.Store(0)
	timing.levelCheckNS.Store(0)
	timing.poolGetNS.Store(0)
	timing.ringBufPushNS.Store(0)
	timing.syncProcessNS.Store(0)
	timing.asyncProcessNS.Store(0)
	timing.hookExecNS.Store(0)
	timing.slogCallNS.Store(0)
	timing.handlerHandleNS.Store(0)
	timing.batchProcessNS.Store(0)
}

// DumpTiming 输出耗时统计到 stderr
func DumpTiming() {
	if !timing.enabled.Load() {
		fmt.Fprintln(os.Stderr, "[treasure-slog] timing disabled, call EnableTiming(true) first")
		return
	}

	logCalls := timing.logCalls.Load()
	if logCalls == 0 {
		fmt.Fprintln(os.Stderr, "[treasure-slog] no log calls recorded")
		return
	}

	filtered := timing.logFiltered.Load()
	syncPath := timing.logSyncPath.Load()
	asyncPush := timing.logAsyncPush.Load()
	fallback := timing.logFallback.Load()
	batches := timing.batchProcessed.Load()
	handled := timing.entriesHandled.Load()

	fmt.Fprintf(os.Stderr, "\n=== treasure-slog 耗时统计 ===\n")
	fmt.Fprintf(os.Stderr, "总调用:           %d\n", logCalls)
	fmt.Fprintf(os.Stderr, "级别过滤丢弃:     %d (%.1f%%)\n", filtered, pct(filtered, logCalls))
	fmt.Fprintf(os.Stderr, "同步路径处理:     %d\n", syncPath)
	fmt.Fprintf(os.Stderr, "异步推入:         %d\n", asyncPush)
	fmt.Fprintf(os.Stderr, "降级同步(缓冲满): %d\n", fallback)
	fmt.Fprintf(os.Stderr, "批量处理次数:     %d\n", batches)
	fmt.Fprintf(os.Stderr, "处理条目总数:     %d\n", handled)

	printAvg("log() 总耗时", timing.logTotalNS.Load(), logCalls)
	printAvg("级别检查", timing.levelCheckNS.Load(), logCalls)
	printAvg("对象池获取", timing.poolGetNS.Load(), asyncPush)
	printAvg("环形缓冲区Push", timing.ringBufPushNS.Load(), asyncPush)
	printAvg("同步processEntry", timing.syncProcessNS.Load(), syncPath)
	printAvg("异步processEntry", timing.asyncProcessNS.Load(), handled)
	printAvg("Hook执行", timing.hookExecNS.Load(), handled)
	printAvg("slog调用", timing.slogCallNS.Load(), handled)
	printAvg("Handler.Handle", timing.handlerHandleNS.Load(), handled)
	printAvg("批量处理", timing.batchProcessNS.Load(), batches)
	fmt.Fprintln(os.Stderr, "=================================")
}

func printAvg(name string, totalNS int64, count int64) {
	if count == 0 {
		fmt.Fprintf(os.Stderr, "%-22s 0 (count=0)\n", name+":")
		return
	}
	avg := float64(totalNS) / float64(count)
	fmt.Fprintf(os.Stderr, "%-22s avg=%.0f ns  total=%d ns\n", name+":", avg, totalNS)
}

func pct(part, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}

// --- 辅助：带耗时的计时器 ---

// timingNoop 预分配的 no-op 函数，避免每次 startTimer 在禁用时分配闭包到堆
var timingNoop = func() {}

func (t *timingStats) startTimer(acc *atomic.Int64) (end func()) {
	if !t.enabled.Load() {
		return timingNoop
	}
	start := time.Now()
	return func() {
		acc.Add(int64(time.Since(start)))
	}
}

// --- logEntry ---

type logEntry struct {
	msg   string
	level slog.Level
	ctx   context.Context
	args  []any
	hooks []Hook
	ts    time.Time // 日志时间戳（避免 slog 内部再次调用 time.Now）
}

func (e *logEntry) Reset() {
	e.ctx = nil
	e.level = 0
	e.msg = ""
	e.args = nil
	e.hooks = nil
	e.ts = time.Time{}
}

// --- ringBuffer 分片环形缓冲区 ---

// ringBuffer 分片设计：每个 worker 拥有一个独立 shard
// 生产者通过 round-robin 选择 shard，单 shard 仅多生产者单消费者（MPSC）
// MPSC 下 mutex 临界区最小化（仅 head/tail 推进），低竞争时性能优于无锁 CAS 链
type ringBuffer struct {
	shards []*rbShard
	next   atomic.Uint64 // round-robin 计数器
	ns     int           // shard 数量
}

type rbShard struct {
	buf  []*logEntry
	cap  uint64
	mask uint64
	head uint64
	tail uint64
	mu   sync.Mutex
	drop atomic.Int64
	_    [40]byte // cache line padding，防止 false sharing
}

func newRingBuffer(capacity int) *ringBuffer {
	return newShardedRingBuffer(capacity, 0)
}

func newShardedRingBuffer(capacity, shards int) *ringBuffer {
	c := uint64(capacity)
	if c < 1024 {
		c = 1024
	}
	if c&(c-1) != 0 { // 向上取整到2的幂
		c--
		c |= c >> 1
		c |= c >> 2
		c |= c >> 4
		c |= c >> 8
		c |= c >> 16
		c++
	}
	if shards <= 0 {
		shards = 1
	}
	perShard := c / uint64(shards)
	if perShard < 256 {
		perShard = 256
	}
	if perShard&(perShard-1) != 0 {
		perShard--
		perShard |= perShard >> 1
		perShard |= perShard >> 2
		perShard |= perShard >> 4
		perShard |= perShard >> 8
		perShard |= perShard >> 16
		perShard++
	}
	rb := &ringBuffer{ns: shards}
	rb.shards = make([]*rbShard, shards)
	for i := 0; i < shards; i++ {
		rb.shards[i] = &rbShard{
			buf:  make([]*logEntry, perShard),
			cap:  perShard,
			mask: perShard - 1,
		}
	}
	return rb
}

func (rb *ringBuffer) Push(entry *logEntry) bool {
	if rb.ns <= 1 {
		return rb.shards[0].push(entry)
	}
	// round-robin 选 shard，减少单 shard 锁竞争
	idx := rb.next.Add(1) % uint64(rb.ns)
	return rb.shards[idx].push(entry)
}

func (s *rbShard) push(entry *logEntry) bool {
	s.mu.Lock()
	if s.tail-s.head >= s.cap {
		s.drop.Add(1)
		s.mu.Unlock()
		return false
	}
	s.buf[s.tail&s.mask] = entry
	s.tail++
	s.mu.Unlock()
	return true
}

// Pop 从指定 shard 弹出（worker i 消费 shard i）
func (rb *ringBuffer) Pop(shardIdx int) *logEntry {
	if shardIdx < 0 || shardIdx >= rb.ns {
		return nil
	}
	return rb.shards[shardIdx].pop()
}

func (s *rbShard) pop() *logEntry {
	s.mu.Lock()
	if s.head >= s.tail {
		s.mu.Unlock()
		return nil
	}
	entry := s.buf[s.head&s.mask]
	s.head++
	s.mu.Unlock()
	return entry
}

// PopBatch 批量弹出（减少锁次数）：单消费者，连续推进 head
func (rb *ringBuffer) PopBatch(shardIdx int, batch []*logEntry) int {
	if shardIdx < 0 || shardIdx >= rb.ns {
		return 0
	}
	s := rb.shards[shardIdx]
	s.mu.Lock()
	n := 0
	for n < len(batch) && s.head < s.tail {
		batch[n] = s.buf[s.head&s.mask]
		s.head++
		n++
	}
	s.mu.Unlock()
	return n
}

func (rb *ringBuffer) Dropped() int64 {
	var total int64
	for _, s := range rb.shards {
		total += s.drop.Load()
	}
	return total
}

// --- batchWriter 批量写入器 ---

type batchWriter struct {
	writer    io.Writer
	buffer    *bufio.Writer
	batchSize int
	interval  time.Duration
	timer     *time.Timer
	mu        sync.Mutex
	closed    bool
}

func newBatchWriter(writer io.Writer, batchSize int, interval time.Duration) *batchWriter {
	if batchSize <= 0 {
		batchSize = 1024
	}
	if interval <= 0 {
		interval = time.Second
	}
	bufSize := batchSize * 1024
	if bufSize < 65536 {
		bufSize = 65536
	}
	bw := &batchWriter{
		writer:    writer,
		buffer:    bufio.NewWriterSize(writer, bufSize),
		batchSize: batchSize,
		interval:  interval,
	}
	bw.timer = time.AfterFunc(interval, bw.flush)
	return bw
}

func (bw *batchWriter) Write(p []byte) (n int, err error) {
	bw.mu.Lock()
	if bw.closed {
		bw.mu.Unlock()
		return 0, io.EOF
	}
	n, err = bw.buffer.Write(p)
	if err != nil {
		bw.mu.Unlock()
		return n, err
	}
	if bw.buffer.Buffered() >= bw.batchSize*1024 {
		err = bw.buffer.Flush()
	}
	bw.mu.Unlock()
	return n, err
}

func (bw *batchWriter) flush() {
	bw.mu.Lock()
	defer bw.mu.Unlock()
	if bw.closed {
		return
	}
	if bw.buffer.Buffered() > 0 {
		bw.buffer.Flush()
	}
	if bw.timer != nil {
		bw.timer.Reset(bw.interval)
	}
}

func (bw *batchWriter) Close() error {
	bw.mu.Lock()
	defer bw.mu.Unlock()
	if bw.closed {
		return nil
	}
	if bw.buffer.Buffered() > 0 {
		bw.buffer.Flush()
	}
	if bw.timer != nil {
		bw.timer.Stop()
	}
	if closer, ok := bw.writer.(io.Closer); ok {
		closer.Close()
	}
	bw.closed = true
	return nil
}

// --- networkWriter 网络写入器 ---

type networkWriter struct {
	connType string
	address  string
	timeout  time.Duration
	retry    int
	useTLS   bool
	conn     net.Conn
	mu       sync.RWMutex
}

func newNetworkWriter(cfg config.NetworkConfig) (*networkWriter, error) {
	nw := &networkWriter{
		connType: cfg.Type,
		address:  cfg.Address,
		timeout:  time.Duration(cfg.Timeout) * time.Second,
		retry:    cfg.Retry,
		useTLS:   cfg.TLS,
	}
	if err := nw.connect(); err != nil {
		return nil, err
	}
	return nw, nil
}

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

func (nw *networkWriter) Write(p []byte) (n int, err error) {
	for i := 0; i <= nw.retry; i++ {
		nw.mu.RLock()
		conn := nw.conn
		nw.mu.RUnlock()
		if conn == nil {
			if i < nw.retry {
				nw.connect()
				continue
			}
			return 0, fmt.Errorf("network writer: connection is nil")
		}
		n, err = conn.Write(p)
		if err == nil {
			return n, nil
		}
		if i < nw.retry {
			nw.connect()
		}
	}
	return n, err
}

func (nw *networkWriter) Close() error {
	nw.mu.Lock()
	defer nw.mu.Unlock()
	if nw.conn != nil {
		return nw.conn.Close()
	}
	return nil
}

// --- httpWriter HTTP写入器 ---

type httpWriter struct {
	url    string
	client *http.Client
	retry  int
}

func newHTTPWriter(address string, timeout time.Duration, retry int) *httpWriter {
	return &httpWriter{
		url:   address,
		retry: retry,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (hw *httpWriter) Write(p []byte) (int, error) {
	var lastErr error
	for i := 0; i <= hw.retry; i++ {
		resp, err := hw.client.Post(hw.url, "application/json", bytes.NewReader(p))
		if err != nil {
			lastErr = err
			if i < hw.retry {
				time.Sleep(time.Duration(i+1) * 100 * time.Millisecond)
			}
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return len(p), nil
		}
		lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
		if i < hw.retry {
			time.Sleep(time.Duration(i+1) * 100 * time.Millisecond)
		}
	}
	return 0, lastErr
}

func (hw *httpWriter) Close() error {
	hw.client.CloseIdleConnections()
	return nil
}

// --- SLogger ---

type SLogger struct {
	logger        *slog.Logger
	handler       slog.Handler // 缓存：避免每次 log 都调 l.logger.Handler()
	hooks         []Hook
	fileLogger    *writer.FileWriter
	level         *atomic.Int32  // 指针：With/WithContext 派生 logger 共享同一级别
	levelVar      *slog.LevelVar // 指针：TextHandler 动态级别共享
	ringBuf       *ringBuffer
	batchWriter   *batchWriter
	networkWriter io.WriteCloser
	cfg           *config.Config
	workers       []*worker
	wg            *sync.WaitGroup
	usePool       bool
	asyncEnabled  bool      // 是否启用异步模式
	lockFree      bool      // 是否启用无锁优化
	prealloc      bool      // 是否启用预分配
	syncOnce      sync.Once // 保证 Sync 只执行一次关闭逻辑，消除并发双关闭竞态
}

// --- worker ---

type worker struct {
	id       int
	logger   *SLogger
	stopCh   chan struct{}
	wg       *sync.WaitGroup
	stopOnce sync.Once // 保证 close(stopCh) 只执行一次，消除双关闭竞态
}

func (w *worker) start() {
	w.wg.Add(1)
	go w.run()
}

func (w *worker) run() {
	defer func() {
		// 先 recover 原始 panic，再 Done；若 Done 自身异常也能被外层 defer 兜底
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[treasure-slog] worker %d panic recovered: %v\n", w.id, r)
		}
		if w.wg != nil {
			w.wg.Done()
		}
	}()

	batchSize := 1000
	flushMs := 10
	if w.logger != nil && w.logger.cfg != nil {
		if w.logger.cfg.Log.Async.BatchSize > 0 {
			batchSize = w.logger.cfg.Log.Async.BatchSize
		}
		if w.logger.cfg.Log.Async.FlushInterval > 0 {
			flushMs = w.logger.cfg.Log.Async.FlushInterval
		}
	}

	batch := make([]*logEntry, 0, batchSize)
	interval := time.Duration(flushMs) * time.Millisecond
	if interval <= 0 {
		interval = 10 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		// 安全检查
		if w.logger == nil || w.logger.ringBuf == nil {
			select {
			case <-w.stopCh:
				return
			case <-ticker.C:
				continue
			}
		}

		// 尝试从 ring buffer 弹出条目
		if cap(batch) > 0 && len(batch) < cap(batch) {
			n := w.logger.ringBuf.PopBatch(w.id, batch[len(batch):cap(batch)])
			if n > 0 {
				batch = batch[:len(batch)+n]
				// 批次满了立即处理，然后继续尝试弹出（不阻塞）
				if len(batch) >= batchSize {
					w.processBatch(batch)
					batch = batch[:0]
				}
				continue
			}
		}

		// 无数据时阻塞在 select，避免忙等烧 CPU
		// ticker.C 保证即使无新数据也能定期处理残留 batch
		select {
		case <-w.stopCh:
			// 先处理已弹出到 batch 但未达批量的条目
			if len(batch) > 0 {
				w.processBatch(batch)
				batch = batch[:0]
			}
			// 排空本 shard 的剩余条目
			for {
				n := w.logger.ringBuf.PopBatch(w.id, batch[:cap(batch)])
				if n == 0 {
					break
				}
				w.processBatch(batch[:n])
				batch = batch[:0]
			}
			return
		case <-ticker.C:
			if len(batch) > 0 {
				w.processBatch(batch)
				batch = batch[:0]
			}
		}
	}
}

func (w *worker) processBatch(batch []*logEntry) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[treasure-slog] processBatch panic recovered: %v\n", r)
		}
	}()
	endBatch := timing.startTimer(&timing.batchProcessNS)
	defer endBatch()
	timing.batchProcessed.Add(1)
	if w.logger == nil {
		return
	}
	for _, entry := range batch {
		if entry == nil {
			continue
		}
		w.logger.processEntry(entry)
		if w.logger.usePool {
			entry.Reset()
			logEntryPool.Put(entry)
		}
	}
}

func (w *worker) stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
}

// --- 全局实例 ---

var (
	globalLogger Logger
	once         sync.Once
)

// exeDir 是启动时缓存的可执行文件所在目录，避免重复调用 os.Executable()
var exeDir string

// init caches the executable directory for path resolution
func init() {
	if path, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(path)
	}
}

// resolveExistingPath 将相对路径解析为绝对路径，适用于已存在的文件（如配置文件）。
// 优先基于 exe 所在目录解析，若文件不存在则 fallback 到基于当前工作目录的路径。
func resolveExistingPath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	if exeDir != "" {
		abs := filepath.Join(exeDir, p)
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}
	return p
}

// resolvePath 将相对路径解析为绝对路径，适用于待创建的文件（如日志文件）。
// 直接基于 exe 所在目录解析，无需检查文件是否存在。
func resolvePath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	if exeDir != "" {
		return filepath.Join(exeDir, p)
	}
	return p
}

// init parses --config flag and initializes globalLogger (must run after exeDir init)
func init() {
	// 解析 --config 命令行参数，自动初始化全局 logger
	if configPath := parseConfigFlag(); configPath != "" {
		if l, err := New(configPath); err == nil {
			globalLogger = l
		}
	}
}

// parseConfigFlag 解析 --config 命令行参数
func parseConfigFlag() string {
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == "--config" || arg == "-config" {
			if i+1 < len(os.Args) {
				return os.Args[i+1]
			}
		}
		if len(arg) > 9 && arg[:9] == "--config=" {
			return arg[9:]
		}
		if len(arg) > 8 && arg[:8] == "-config=" {
			return arg[8:]
		}
	}
	// 尝试默认配置文件（优先 exe 目录）
	if _, err := os.Stat(resolveExistingPath("configs/config.yaml")); err == nil {
		return resolveExistingPath("configs/config.yaml")
	}
	return ""
}

// New 创建日志记录器
func New(configPath string) (Logger, error) {
	cfg, err := config.LoadConfig(resolveExistingPath(configPath))
	if err != nil {
		return nil, fmt.Errorf("load config error: %w", err)
	}

	// async.enabled 由 config.setDefaults 处理默认值
	asyncEnabled := cfg.Log.Async.Enabled

	slogger := &SLogger{
		cfg:          cfg,
		hooks:        []Hook{},
		wg:           &sync.WaitGroup{},
		usePool:      cfg.Log.Performance.UsePool,
		asyncEnabled: asyncEnabled,
		lockFree:     cfg.Log.Performance.LockFree,
		prealloc:     cfg.Log.Performance.Prealloc,
		level:        &atomic.Int32{},
		levelVar:     &slog.LevelVar{},
	}

	slogger.SetLevel(cfg.Log.Level)

	// 构建多输出 writer
	writers := []io.Writer{}

	// 控制台输出：支持独立 format
	// 当 console.format 与 log.format 不同时，控制台使用独立 handler
	consoleFormat := cfg.Log.Console.Format
	if consoleFormat == "" {
		consoleFormat = cfg.Log.Format // 默认与主格式一致
	}
	consoleIndependent := cfg.Log.Console.Enabled && consoleFormat != cfg.Log.Format

	if cfg.Log.Console.Enabled && !consoleIndependent {
		writers = append(writers, os.Stdout)
	}
	if cfg.Log.File.Enabled {
		logPath := resolvePath(cfg.Log.File.Path)
		cfg.Log.File.Path = logPath
		fw, err := writer.NewFileWriter(writer.FileWriterConfig{
			Dir:         filepath.Dir(logPath),
			BaseName:    filepath.Base(logPath[:len(logPath)-len(filepath.Ext(logPath))]),
			Ext:         filepath.Ext(logPath),
			MaxSize:     int64(cfg.Log.File.Rotate.MaxSize) * 1024 * 1024, // MB -> bytes
			MaxBackups:  cfg.Log.File.Rotate.MaxBackups,
			MaxAge:      time.Duration(cfg.Log.File.Rotate.MaxAge) * 24 * time.Hour, // 天 -> duration
			Compress:    cfg.Log.File.Rotate.Compress,
			Interval:    cfg.Log.File.Rotate.Interval,
		})
		if err != nil {
			return nil, fmt.Errorf("create file writer error: %w", err)
		}
		slogger.fileLogger = fw
		writers = append(writers, fw)
	}
	if cfg.Log.Network.Enabled {
		var nw io.WriteCloser
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

	var writer io.Writer
	if len(writers) == 1 {
		writer = writers[0]
	} else if len(writers) > 1 {
		writer = io.MultiWriter(writers...)
	} else {
		writer = io.Discard
	}

	// batchWriter 仅在异步模式下使用
	if asyncEnabled {
		slogger.batchWriter = newBatchWriter(writer, cfg.Log.Async.BatchSize,
			time.Duration(cfg.Log.Async.FlushInterval)*time.Millisecond)
		writer = slogger.batchWriter
	}

	// 创建主 handler
	handler := slogger.createHandler(writer)

	// 控制台独立格式：当 console.format 与 log.format 不同时，创建独立控制台 handler
	if consoleIndependent {
		consoleWriter := newBatchWriter(os.Stdout, cfg.Log.Async.BatchSize,
			time.Duration(cfg.Log.Async.FlushInterval)*time.Millisecond)
		consoleHandler := slogger.createConsoleHandler(consoleWriter, consoleFormat)
		handler = newMultiHandler(handler, consoleHandler)
	}

	slogger.logger = slog.New(handler)
	slogger.handler = handler // 缓存 handler，processEntry 可直接调用

	// 异步模式：创建环形缓冲区和工作线程
	if asyncEnabled {
		bufSize := cfg.Log.Async.BufferSize
		if slogger.prealloc {
			bufSize = bufSize * 2
		}
		workerCount := cfg.Log.Async.Workers
		if slogger.lockFree && workerCount < 4 {
			workerCount = 4
		}
		// 分片环形缓冲区：shard 数 = worker 数，每 worker 独占一个 shard
		slogger.ringBuf = newShardedRingBuffer(bufSize, workerCount)
		slogger.workers = make([]*worker, workerCount)
		for i := 0; i < workerCount; i++ {
			slogger.workers[i] = &worker{
				id:     i,
				logger: slogger,
				stopCh: make(chan struct{}),
				wg:     slogger.wg,
			}
			slogger.workers[i].start()
		}
	}

	once.Do(func() { globalLogger = slogger })
	return slogger, nil
}

func (l *SLogger) createHandler(writer io.Writer) slog.Handler {
	var handler slog.Handler
	switch l.cfg.Log.Format {
	case "console", "text":
		l.levelVar.Set(slog.Level(l.level.Load()))
		handler = slog.NewTextHandler(writer, &slog.HandlerOptions{
			Level: l.levelVar,
			ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				if a.Key == slog.TimeKey {
					if t, ok := a.Value.Any().(time.Time); ok {
						a.Value = slog.StringValue(t.Format(time.RFC3339Nano))
					}
				}
				return a
			},
		})
	default:
		handler = NewFastHandler(writer, l.level)
	}

	if l.cfg.Log.Sampling.Enabled {
		handler = NewSamplingHandler(handler, SamplingOptions{
			Initial:    l.cfg.Log.Sampling.Initial,
			Thereafter: l.cfg.Log.Sampling.Thereafter,
		})
	}
	return handler
}

// createConsoleHandler 创建控制台专用 handler，支持独立格式
func (l *SLogger) createConsoleHandler(writer io.Writer, format string) slog.Handler {
	switch format {
	case "json":
		return NewFastHandler(writer, l.level)
	case "console", "text":
		l.levelVar.Set(slog.Level(l.level.Load()))
		return slog.NewTextHandler(writer, &slog.HandlerOptions{
			Level: l.levelVar,
			ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				if a.Key == slog.TimeKey {
					if t, ok := a.Value.Any().(time.Time); ok {
						a.Value = slog.StringValue(t.Format(time.RFC3339Nano))
					}
				}
				return a
			},
		})
	default:
		return NewFastHandler(writer, l.level)
	}
}

// --- multiHandler 多handler分发 ---

type multiHandler struct {
	handlers []slog.Handler
}

func newMultiHandler(handlers ...slog.Handler) slog.Handler {
	return &multiHandler{handlers: handlers}
}

func (h *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, handler := range h.handlers {
		if !handler.Enabled(ctx, r.Level) {
			continue
		}
		if err := handler.Handle(ctx, r.Clone()); err != nil {
			fmt.Fprintf(os.Stderr, "[treasure-slog] multiHandler Handle error: %v\n", err)
		}
	}
	return nil
}

func (h *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newHandlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		newHandlers[i] = handler.WithAttrs(attrs)
	}
	return &multiHandler{handlers: newHandlers}
}

func (h *multiHandler) WithGroup(name string) slog.Handler {
	newHandlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		newHandlers[i] = handler.WithGroup(name)
	}
	return &multiHandler{handlers: newHandlers}
}

// --- 核心日志方法 ---

// safeArgs 确保args为偶数个（slog要求key-value成对），奇数时补齐
func safeArgs(args []any) []any {
	if len(args)%2 != 0 {
		newArgs := make([]any, len(args)+1)
		copy(newArgs, args)
		newArgs[len(args)] = "<missing-value>"
		return newArgs
	}
	return args
}

func (l *SLogger) log(ctx context.Context, level slog.Level, msg string, args ...any) {
	// 安全检查：防止nil SLogger
	if l == nil || l.logger == nil {
		return
	}

	// 级别过滤（热路径：先过滤再进 defer，避免无谓 defer 开销）
	var currentLevel slog.Level
	if l.level != nil {
		currentLevel = slog.Level(l.level.Load())
	}
	if currentLevel == 0 {
		currentLevel = slog.LevelInfo
	}
	if level < currentLevel {
		if timing.enabled.Load() {
			timing.logFiltered.Add(1)
			timing.logCalls.Add(1)
		}
		return
	}

	// 合并 defer：timing + recover，减少 defer 开销
	endTotal := timing.startTimer(&timing.logTotalNS)
	defer func() {
		endTotal()
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[treasure-slog] log panic recovered: %v\n", r)
		}
	}()
	if timing.enabled.Load() {
		timing.logCalls.Add(1)
	}

	// 同步模式：直接处理，不走环形缓冲区
	if !l.asyncEnabled || l.ringBuf == nil {
		if timing.enabled.Load() {
			timing.logSyncPath.Add(1)
		}
		l.processEntryDirect(ctx, level, msg, args)
		return
	}

	// 异步模式：获取日志条目，推入环形缓冲区
	var entry *logEntry
	if l.usePool {
		if v := logEntryPool.Get(); v != nil {
			if e, ok := v.(*logEntry); ok {
				entry = e
			}
		}
	}
	if entry == nil {
		entry = &logEntry{}
	}

	entry.msg = msg
	entry.level = level
	entry.ctx = ctx
	// 内联 safeArgs：偶数参数直接赋值（避免函数调用开销与堆分配）
	if len(args)%2 == 0 {
		entry.args = args
	} else {
		entry.args = safeArgs(args)
	}
	entry.hooks = l.hooks
	entry.ts = time.Now() // 生产者设置时间戳，worker 直接使用避免重复调用

	pushed := l.ringBuf.Push(entry)
	if timing.enabled.Load() {
		timing.logAsyncPush.Add(1)
	}

	if !pushed {
		// 缓冲区满：降级为同步处理
		if timing.enabled.Load() {
			timing.logFallback.Add(1)
		}
		l.processEntry(entry)
		entry.Reset()
		if l.usePool {
			logEntryPool.Put(entry)
		}
	}
}

// processEntryDirect 同步模式直接处理日志（绕过 slog.Logger，直接调用 handler.Handle）
func (l *SLogger) processEntryDirect(ctx context.Context, level slog.Level, msg string, args []any) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[treasure-slog] processEntryDirect panic recovered: %v\n", r)
		}
	}()

	// 执行钩子
	endHook := timing.startTimer(&timing.hookExecNS)
	for _, hook := range l.hooks {
		if hook == nil {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "[treasure-slog] hook panic recovered: %v\n", r)
				}
			}()
			levelStr := safeLevelString(level)
			hook.Run(msg, levelStr, args...)
		}()
	}
	endHook()

	if ctx == nil {
		ctx = context.Background()
	}

	safeArgsVal := safeArgs(args)

	// Error 级别追加堆栈
	if level == slog.LevelError {
		if l.cfg != nil && l.cfg.Log.Stacktrace.Enabled && l.shouldAddStacktrace(level) {
			stackTrace := getStackTrace(l.cfg.Log.Stacktrace.Depth)
			safeArgsVal = append(safeArgsVal, "stacktrace", stackTrace)
		}
	}

	endSlogCall := timing.startTimer(&timing.slogCallNS)
	timing.entriesHandled.Add(1)

	// 直接调用 handler.Handle，绕过 slog.Logger 内部的 time.Now + Enabled 检查
	record := slog.NewRecord(time.Now(), level, msg, 0)
	record.Add(safeArgsVal...)
	if l.handler != nil {
		_ = l.handler.Handle(ctx, record)
	} else {
		_ = l.logger.Handler().Handle(ctx, record)
	}
	endSlogCall()
}

// shouldAddStacktrace 根据配置的 stacktrace.level 判断是否需要添加堆栈
func (l *SLogger) shouldAddStacktrace(level slog.Level) bool {
	if l.cfg == nil {
		return true // 默认仅 error 级别
	}
	cfgLevel := l.cfg.Log.Stacktrace.Level
	if cfgLevel == "" {
		return level >= slog.LevelError // 默认仅 error
	}
	switch cfgLevel {
	case "debug":
		return level >= slog.LevelDebug
	case "info":
		return level >= slog.LevelInfo
	case "warn":
		return level >= slog.LevelWarn
	case "error":
		return level >= slog.LevelError
	default:
		return level >= slog.LevelError
	}
}

func (l *SLogger) processEntry(entry *logEntry) {
	if l == nil || l.logger == nil {
		return
	}

	endProc := timing.startTimer(&timing.asyncProcessNS)
	defer endProc()
	timing.entriesHandled.Add(1)

	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[treasure-slog] processEntry panic recovered: %v\n", r)
		}
	}()

	// 执行钩子
	endHook := timing.startTimer(&timing.hookExecNS)
	for _, hook := range entry.hooks {
		if hook == nil {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "[treasure-slog] hook panic recovered: %v\n", r)
				}
			}()
			levelStr := safeLevelString(entry.level)
			hook.Run(entry.msg, levelStr, entry.args...)
		}()
	}
	endHook()

	ctx := entry.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	args := entry.args
	if len(args)%2 != 0 {
		args = safeArgs(args)
	}

	// Error 级别追加堆栈
	if entry.level == slog.LevelError {
		if l.cfg != nil && l.cfg.Log.Stacktrace.Enabled && l.shouldAddStacktrace(entry.level) {
			stackTrace := getStackTrace(l.cfg.Log.Stacktrace.Depth)
			args = append(args, "stacktrace", stackTrace)
		}
	}

	endSlogCall := timing.startTimer(&timing.slogCallNS)

	// 直接调用 handler.Handle，使用 entry.ts 避免重复 time.Now
	ts := entry.ts
	if ts.IsZero() {
		ts = time.Now()
	}
	record := slog.NewRecord(ts, entry.level, entry.msg, 0)
	record.Add(args...)
	if l.handler != nil {
		_ = l.handler.Handle(ctx, record)
	} else {
		_ = l.logger.Handler().Handle(ctx, record)
	}
	endSlogCall()
}

// safeLevelString 安全获取level字符串
func safeLevelString(level slog.Level) string {
	switch level {
	case slog.LevelDebug:
		return "DEBUG"
	case slog.LevelInfo:
		return "INFO"
	case slog.LevelWarn:
		return "WARN"
	case slog.LevelError:
		return "ERROR"
	default:
		return fmt.Sprintf("LEVEL(%d)", int(level))
	}
}

// --- 公开方法 ---

func (l *SLogger) Debug(msg string, args ...any) {
	l.log(context.Background(), slog.LevelDebug, msg, args...)
}
func (l *SLogger) DebugContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelDebug, msg, args...)
}
func (l *SLogger) Info(msg string, args ...any) {
	l.log(context.Background(), slog.LevelInfo, msg, args...)
}
func (l *SLogger) InfoContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelInfo, msg, args...)
}
func (l *SLogger) Warn(msg string, args ...any) {
	l.log(context.Background(), slog.LevelWarn, msg, args...)
}
func (l *SLogger) WarnContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelWarn, msg, args...)
}
func (l *SLogger) Error(msg string, args ...any) {
	l.log(context.Background(), slog.LevelError, msg, args...)
}
func (l *SLogger) ErrorContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelError, msg, args...)
}

func (l *SLogger) With(args ...any) Logger {
	if l == nil || l.logger == nil {
		return l
	}
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[treasure-slog] With panic recovered: %v\n", r)
		}
	}()
	newSlogLogger := l.logger.With(safeArgs(args)...)
	newLogger := &SLogger{
		logger:        newSlogLogger,
		handler:       newSlogLogger.Handler(), // 关键：用带新属性的 handler，否则 With 的字段会丢失
		hooks:         l.hooks,
		fileLogger:    l.fileLogger,
		level:         l.level,
		levelVar:      l.levelVar,
		ringBuf:       l.ringBuf,
		batchWriter:   l.batchWriter,
		networkWriter: l.networkWriter,
		cfg:           l.cfg,
		workers:       l.workers,
		wg:            l.wg,
		usePool:       l.usePool,
		asyncEnabled:  l.asyncEnabled,
		lockFree:      l.lockFree,
		prealloc:      l.prealloc,
	}
	return newLogger
}

func (l *SLogger) WithContext(ctx context.Context) Logger {
	if l == nil || l.logger == nil {
		return l
	}
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[treasure-slog] WithContext panic recovered: %v\n", r)
		}
	}()
	args := extractContextInfo(ctx)
	newLogger := l.logger.With(safeArgs(args)...)
	return &SLogger{
		logger:        newLogger,
		handler:       newLogger.Handler(),
		hooks:         l.hooks,
		fileLogger:    l.fileLogger,
		level:         l.level,
		levelVar:      l.levelVar,
		ringBuf:       l.ringBuf,
		batchWriter:   l.batchWriter,
		networkWriter: l.networkWriter,
		cfg:           l.cfg,
		workers:       l.workers,
		wg:            l.wg,
		usePool:       l.usePool,
		asyncEnabled:  l.asyncEnabled,
		lockFree:      l.lockFree,
		prealloc:      l.prealloc,
	}
}

func extractContextInfo(ctx context.Context) []any {
	if ctx == nil {
		return nil
	}
	args := make([]any, 0, 8)
	if v := ctx.Value("request_id"); v != nil {
		args = append(args, "request_id", v)
	}
	if v := ctx.Value("user_id"); v != nil {
		args = append(args, "user_id", v)
	}
	if v := ctx.Value("span_id"); v != nil {
		args = append(args, "span_id", v)
	}
	if v := ctx.Value("trace_id"); v != nil {
		args = append(args, "trace_id", v)
	}
	return args
}

func (l *SLogger) AddHook(hook Hook) Logger {
	if l == nil {
		return l
	}
	if hook == nil {
		return l // 忽略 nil hook，避免后续遍历无意义
	}
	newHooks := make([]Hook, len(l.hooks)+1)
	copy(newHooks, l.hooks)
	newHooks[len(l.hooks)] = hook
	return &SLogger{
		logger:        l.logger,
		handler:       l.handler,
		hooks:         newHooks,
		fileLogger:    l.fileLogger,
		level:         l.level,
		levelVar:      l.levelVar,
		ringBuf:       l.ringBuf,
		batchWriter:   l.batchWriter,
		networkWriter: l.networkWriter,
		cfg:           l.cfg,
		workers:       l.workers,
		wg:            l.wg,
		usePool:       l.usePool,
		asyncEnabled:  l.asyncEnabled,
		lockFree:      l.lockFree,
		prealloc:      l.prealloc,
	}
}

func (l *SLogger) SetLevel(level string) {
	if l == nil || l.level == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[treasure-slog] SetLevel panic recovered: %v\n", r)
		}
	}()
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
	l.level.Store(int32(slogLevel))
	l.levelVar.Set(slogLevel)
	// FastHandler 通过 l.level 指针引用动态感知变化
	// TextHandler 通过 l.levelVar 指针引用动态感知变化
	// With/WithContext 派生的 logger 共享同一指针，级别变更全链路生效
}

func (l *SLogger) GetLevel() string {
	if l == nil || l.level == nil {
		return "info"
	}
	level := slog.Level(l.level.Load())
	switch level {
	case slog.LevelDebug:
		return "debug"
	case slog.LevelInfo:
		return "info"
	case slog.LevelWarn:
		return "warn"
	case slog.LevelError:
		return "error"
	default:
		return "info"
	}
}

func (l *SLogger) Sync() error {
	if l == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[treasure-slog] Sync panic recovered: %v\n", r)
		}
	}()
	l.syncOnce.Do(func() {
		// workers 字段只在 Sync 中置 nil，配合 sync.Once 保证并发 Sync 只执行一次
		for _, w := range l.workers {
			if w != nil {
				w.stop()
			}
		}
		if l.wg != nil {
			l.wg.Wait()
		}
		l.workers = nil
		if l.batchWriter != nil {
			l.batchWriter.Close()
		}
		if l.fileLogger != nil {
			l.fileLogger.Close()
		}
		if l.networkWriter != nil {
			l.networkWriter.Close()
		}
	})
	return nil
}

// --- getStackTrace ---

func getStackTrace(depth int) string {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[treasure-slog] getStackTrace panic recovered: %v\n", r)
		}
	}()
	if depth <= 0 {
		depth = 10
	}
	stack := make([]byte, 1024*depth)
	n := runtime.Stack(stack, false)
	return string(stack[:n])
}

// --- 全局函数 ---

func Debug(msg string, args ...any) {
	if globalLogger != nil {
		globalLogger.Debug(msg, args...)
	}
}
func DebugContext(ctx context.Context, msg string, args ...any) {
	if globalLogger != nil {
		globalLogger.DebugContext(ctx, msg, args...)
	}
}
func Info(msg string, args ...any) {
	if globalLogger != nil {
		globalLogger.Info(msg, args...)
	}
}
func InfoContext(ctx context.Context, msg string, args ...any) {
	if globalLogger != nil {
		globalLogger.InfoContext(ctx, msg, args...)
	}
}
func Warn(msg string, args ...any) {
	if globalLogger != nil {
		globalLogger.Warn(msg, args...)
	}
}
func WarnContext(ctx context.Context, msg string, args ...any) {
	if globalLogger != nil {
		globalLogger.WarnContext(ctx, msg, args...)
	}
}
func Error(msg string, args ...any) {
	if globalLogger != nil {
		globalLogger.Error(msg, args...)
	}
}
func ErrorContext(ctx context.Context, msg string, args ...any) {
	if globalLogger != nil {
		globalLogger.ErrorContext(ctx, msg, args...)
	}
}
func With(args ...any) Logger {
	if globalLogger != nil {
		return globalLogger.With(args...)
	}
	return nil
}
func WithContext(ctx context.Context) Logger {
	if globalLogger != nil {
		return globalLogger.WithContext(ctx)
	}
	return nil
}
func AddHook(hook Hook) Logger {
	if globalLogger != nil {
		return globalLogger.AddHook(hook)
	}
	return nil
}
func Sync() error {
	if globalLogger != nil {
		return globalLogger.Sync()
	}
	return nil
}
func SetLevel(level string) {
	if globalLogger != nil {
		globalLogger.SetLevel(level)
	}
}
func GetLevel() string {
	if globalLogger != nil {
		return globalLogger.GetLevel()
	}
	return ""
}
func Recover() {
	if r := recover(); r != nil {
		stackTrace := getStackTrace(10)
		if globalLogger != nil {
			globalLogger.Error("panic recovered", "recover", r, "stacktrace", stackTrace)
		}
	}
}

// --- SamplingHandler ---

type SamplingOptions struct {
	Initial    int
	Thereafter int
}

type SamplingHandler struct {
	handler slog.Handler
	options SamplingOptions
	count   atomic.Uint64
}

func NewSamplingHandler(handler slog.Handler, options SamplingOptions) *SamplingHandler {
	return &SamplingHandler{handler: handler, options: options}
}

func (h *SamplingHandler) Handle(ctx context.Context, record slog.Record) error {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[treasure-slog] SamplingHandler.Handle panic recovered: %v\n", r)
		}
	}()
	count := h.count.Add(1)
	if h.options.Initial > 0 && count <= uint64(h.options.Initial) {
		return h.handler.Handle(ctx, record)
	}
	if h.options.Thereafter > 0 && (count-uint64(h.options.Initial))%uint64(h.options.Thereafter) == 0 {
		return h.handler.Handle(ctx, record)
	}
	return nil
}

func (h *SamplingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &SamplingHandler{handler: h.handler.WithAttrs(attrs), options: h.options}
}

func (h *SamplingHandler) WithGroup(name string) slog.Handler {
	return &SamplingHandler{handler: h.handler.WithGroup(name), options: h.options}
}

func (h *SamplingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}
