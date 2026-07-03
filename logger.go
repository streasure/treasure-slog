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

	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/streasure/treasure-slog/internal/config"
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

// --- logEntry ---

type logEntry struct {
	msg   string
	level slog.Level
	ctx   context.Context
	args  []any
	hooks []Hook
}

func (e *logEntry) Reset() {
	e.ctx = nil
	e.level = 0
	e.msg = ""
	e.args = nil
	e.hooks = nil
}

// --- ringBuffer 环形缓冲区 ---

type ringBuffer struct {
	buf  []*logEntry
	cap  uint64
	mask uint64
	head uint64
	tail uint64
	mu   sync.Mutex
	drop atomic.Int64
}

func newRingBuffer(capacity int) *ringBuffer {
	c := uint64(capacity)
	if c < 1024 {
		c = 1024
	}
	// 向上取整到2的幂
	if c&(c-1) != 0 {
		c--
		c |= c >> 1
		c |= c >> 2
		c |= c >> 4
		c |= c >> 8
		c |= c >> 16
		c++
	}
	return &ringBuffer{
		buf:  make([]*logEntry, c),
		cap:  c,
		mask: c - 1,
	}
}

func (rb *ringBuffer) Push(entry *logEntry) bool {
	rb.mu.Lock()
	if rb.tail-rb.head >= rb.cap {
		rb.drop.Add(1)
		rb.mu.Unlock()
		return false
	}
	rb.buf[rb.tail&rb.mask] = entry
	rb.tail++
	rb.mu.Unlock()
	return true
}

func (rb *ringBuffer) Pop() *logEntry {
	rb.mu.Lock()
	if rb.head >= rb.tail {
		rb.mu.Unlock()
		return nil
	}
	entry := rb.buf[rb.head&rb.mask]
	rb.head++
	rb.mu.Unlock()
	return entry
}

func (rb *ringBuffer) Dropped() int64 { return rb.drop.Load() }

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
	hooks         []Hook
	fileLogger    *lumberjack.Logger
	level         atomic.Int32
	levelVar      slog.LevelVar // 用于TextHandler动态级别
	ringBuf       *ringBuffer
	batchWriter   *batchWriter
	networkWriter io.WriteCloser
	cfg           *config.Config
	workers       []*worker
	wg            *sync.WaitGroup
	usePool       bool
}

// --- worker ---

type worker struct {
	id     int
	logger *SLogger
	stopCh chan struct{}
	wg     *sync.WaitGroup
}

func (w *worker) start() {
	w.wg.Add(1)
	go w.run()
}

func (w *worker) run() {
	defer func() {
		if w.wg != nil {
			w.wg.Done()
		}
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[treasure-slog] worker %d panic recovered: %v\n", w.id, r)
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
			default:
				time.Sleep(time.Millisecond)
				continue
			}
		}

		processed := false
		for i := 0; i < 256; i++ {
			entry := w.logger.ringBuf.Pop()
			if entry == nil {
				break
			}
			batch = append(batch, entry)
			processed = true
			if len(batch) >= batchSize {
				w.processBatch(batch)
				batch = batch[:0]
			}
		}

		select {
		case <-w.stopCh:
			if len(batch) > 0 {
				w.processBatch(batch)
			}
			return
		case <-ticker.C:
			if len(batch) > 0 {
				w.processBatch(batch)
				batch = batch[:0]
			}
		default:
			if !processed {
				time.Sleep(100 * time.Nanosecond)
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
	select {
	case <-w.stopCh:
		// already closed
	default:
		close(w.stopCh)
	}
}

// --- 全局实例 ---

var (
	globalLogger Logger
	once         sync.Once
)

// New 创建日志记录器
func New(configPath string) (Logger, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config error: %w", err)
	}

	if cfg.Log.File.Enabled {
		logDir := filepath.Dir(cfg.Log.File.Path)
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return nil, fmt.Errorf("create log directory error: %w", err)
		}
	}

	slogger := &SLogger{
		cfg:     cfg,
		hooks:   []Hook{},
		wg:      &sync.WaitGroup{},
		usePool: cfg.Log.Performance.UsePool,
	}

	slogger.SetLevel(cfg.Log.Level)

	writers := []io.Writer{}
	if cfg.Log.Console.Enabled {
		writers = append(writers, os.Stdout)
	}
	if cfg.Log.File.Enabled {
		fl := &lumberjack.Logger{
			Filename:   cfg.Log.File.Path,
			MaxSize:    cfg.Log.File.Rotate.MaxSize,
			MaxBackups: cfg.Log.File.Rotate.MaxBackups,
			MaxAge:     cfg.Log.File.Rotate.MaxAge,
			Compress:   cfg.Log.File.Rotate.Compress,
		}
		slogger.fileLogger = fl
		writers = append(writers, fl)
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

	slogger.batchWriter = newBatchWriter(writer, cfg.Log.Async.BatchSize,
		time.Duration(cfg.Log.Async.FlushInterval)*time.Millisecond)
	writer = slogger.batchWriter

	handler := slogger.createHandler(writer)
	slogger.logger = slog.New(handler)

	slogger.ringBuf = newRingBuffer(cfg.Log.Async.BufferSize)

	slogger.workers = make([]*worker, cfg.Log.Async.Workers)
	for i := 0; i < cfg.Log.Async.Workers; i++ {
		slogger.workers[i] = &worker{
			id:     i,
			logger: slogger,
			stopCh: make(chan struct{}),
			wg:     slogger.wg,
		}
		slogger.workers[i].start()
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
			Level: &l.levelVar,
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
		handler = NewFastHandler(writer, &l.level)
	}

	if l.cfg.Log.Sampling.Enabled {
		handler = NewSamplingHandler(handler, SamplingOptions{
			Initial:    l.cfg.Log.Sampling.Initial,
			Thereafter: l.cfg.Log.Sampling.Thereafter,
		})
	}
	return handler
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

	// 全局recover保护
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[treasure-slog] log panic recovered: %v\n", r)
		}
	}()

	// 级别过滤
	currentLevel := slog.Level(l.level.Load())
	if currentLevel == 0 {
		currentLevel = slog.LevelInfo
	}
	if level < currentLevel {
		return
	}

	// 获取日志条目
	var entry *logEntry
	if v := logEntryPool.Get(); v != nil {
		if e, ok := v.(*logEntry); ok {
			entry = e
		}
	}
	if entry == nil {
		entry = &logEntry{}
	}

	entry.msg = msg
	entry.level = level
	entry.ctx = ctx
	entry.args = safeArgs(args)
	entry.hooks = l.hooks

	// 异步写入
	if l.ringBuf != nil {
		if !l.ringBuf.Push(entry) {
			l.processEntry(entry)
			entry.Reset()
			logEntryPool.Put(entry)
		}
	} else {
		l.processEntry(entry)
		entry.Reset()
		logEntryPool.Put(entry)
	}
}

func (l *SLogger) processEntry(entry *logEntry) {
	if l == nil || l.logger == nil {
		return
	}

	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[treasure-slog] processEntry panic recovered: %v\n", r)
		}
	}()

	// 执行钩子
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

	ctx := entry.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	args := entry.args
	// 再次确保偶数对齐（hook可能修改args）
	if len(args)%2 != 0 {
		args = safeArgs(args)
	}

	switch entry.level {
	case slog.LevelDebug:
		l.logger.DebugContext(ctx, entry.msg, args...)
	case slog.LevelInfo:
		l.logger.InfoContext(ctx, entry.msg, args...)
	case slog.LevelWarn:
		l.logger.WarnContext(ctx, entry.msg, args...)
	case slog.LevelError:
		if l.cfg != nil && l.cfg.Log.Stacktrace.Enabled {
			stackTrace := getStackTrace(l.cfg.Log.Stacktrace.Depth)
			args = append(args, "stacktrace", stackTrace)
		}
		l.logger.ErrorContext(ctx, entry.msg, args...)
	default:
		l.logger.InfoContext(ctx, entry.msg, args...)
	}
}

// safeLevelString 安全获取level字符串
func safeLevelString(level slog.Level) string {
	defer func() {
		recover()
	}()
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
	return &SLogger{
		logger:        l.logger.With(safeArgs(args)...),
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
	}
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
	newHooks := make([]Hook, len(l.hooks)+1)
	copy(newHooks, l.hooks)
	newHooks[len(l.hooks)] = hook
	return &SLogger{
		logger:        l.logger,
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
	}
}

func (l *SLogger) SetLevel(level string) {
	if l == nil {
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
	// FastHandler 通过 &l.level 引用动态感知变化
	// TextHandler 通过 &l.levelVar 引用动态感知变化
	// 无需重建 handler
}

func (l *SLogger) GetLevel() string {
	if l == nil {
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
	if l.workers != nil {
		for _, w := range l.workers {
			if w != nil {
				w.stop()
			}
		}
		if l.wg != nil {
			l.wg.Wait()
		}
		l.workers = nil
	}
	if l.batchWriter != nil {
		l.batchWriter.Close()
	}
	if l.fileLogger != nil {
		l.fileLogger.Close()
	}
	if l.networkWriter != nil {
		l.networkWriter.Close()
	}
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
