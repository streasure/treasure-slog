// Package writer 提供高性能并发安全的文件写入器
// 支持按大小/时间轮转、自动目录创建、异步压缩、过期清理
package writer

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var errFileWriterClosed = errors.New("file writer closed")

// FileWriterConfig 文件写入器配置
type FileWriterConfig struct {
	Dir         string        // 日志目录
	BaseName    string        // 文件基础名（不含扩展名）
	Ext         string        // 扩展名（如 ".log"），为空时默认 ".log"
	MaxSize     int64         // 单文件最大字节数（0=不限制，仅按时间轮转）
	MaxBackups  int           // 最大保留备份数（0=不限制）
	MaxAge      time.Duration // 最大保留时长（0=不限制）
	Compress    bool          // 是否压缩旧文件
	Interval    time.Duration // 时间轮转间隔（0=禁用时间轮转）
	DirPerm     os.FileMode   // 目录权限（默认 0755）
	FilePerm    os.FileMode   // 文件权限（默认 0644）
	CompressBuf int           // 压缩通道缓冲大小（默认 64）
}

// FileWriter 高性能并发安全的文件写入器
// 实现 io.WriteCloser 接口，支持按大小/时间轮转、异步压缩、过期清理
type FileWriter struct {
	cfg FileWriterConfig

	// --- 运行时状态（由 mu 保护）---
	mu          sync.Mutex
	file        *os.File // 当前写入的文件
	currentSize int64    // 当前文件已写字节数
	createdAt   time.Time
	closed      bool

	// --- 后台协程 ---
	ticker     *time.Ticker
	compressCh chan string     // 待压缩文件路径队列
	done       chan struct{}   // 关闭信号
	bgWg       sync.WaitGroup // 等待后台协程退出
	once       sync.Once
}

// NewFileWriter 创建文件写入器
// 自动创建目录、打开/创建日志文件、启动后台轮转协程
func NewFileWriter(cfg FileWriterConfig) (*FileWriter, error) {
	// 设置默认值
	if cfg.Ext == "" {
		cfg.Ext = ".log"
	}
	if cfg.DirPerm == 0 {
		cfg.DirPerm = 0755
	}
	if cfg.FilePerm == 0 {
		cfg.FilePerm = 0644
	}
	if cfg.CompressBuf <= 0 {
		cfg.CompressBuf = 64
	}

	// 自动创建目录
	if err := os.MkdirAll(cfg.Dir, cfg.DirPerm); err != nil {
		return nil, fmt.Errorf("create log directory %s: %w", cfg.Dir, err)
	}

	fw := &FileWriter{
		cfg:        cfg,
		compressCh: make(chan string, cfg.CompressBuf),
		done:       make(chan struct{}),
	}

	// 打开或创建当前日志文件
	if err := fw.openFile(); err != nil {
		return nil, err
	}

	// 启动后台协程：时间轮转 + 异步压缩
	fw.bgWg.Add(1)
	go fw.background()

	return fw, nil
}

// openFile 打开或创建日志文件
// 如果文件不存在则创建，如果目录不存在则自动创建
func (fw *FileWriter) openFile() error {
	path := filepath.Join(fw.cfg.Dir, fw.cfg.BaseName+fw.cfg.Ext)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, fw.cfg.FilePerm)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", path, err)
	}
	// 获取当前文件大小（支持追加模式续写）
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("stat log file %s: %w", path, err)
	}
	fw.file = f
	fw.currentSize = info.Size()
	fw.createdAt = info.ModTime()
	return nil
}

// Write 实现 io.Writer 接口
// 热路径：仅做大小检查（int64 比较），避免 time.Now 系统调用
// 时间轮转由 background goroutine 的 ticker 处理
func (fw *FileWriter) Write(p []byte) (n int, err error) {
	fw.mu.Lock()
	defer fw.mu.Unlock()

	if fw.closed {
		return 0, errFileWriterClosed
	}

	// 写入前检查已经达到限制的文件
	if fw.needRotateBySize() {
		if rotateErr := fw.rotate(); rotateErr != nil {
			fmt.Fprintf(os.Stderr, "[tlog] rotate failed: %v\n", rotateErr)
		}
	}

	n, err = fw.file.Write(p)
	fw.currentSize += int64(n)

	// 写入后立即轮转，保持超过大小限制的单次写入也能触发轮转
	if fw.needRotateBySize() {
		if rotateErr := fw.rotate(); rotateErr != nil {
			fmt.Fprintf(os.Stderr, "[tlog] rotate failed: %v\n", rotateErr)
		}
	}

	return n, err
}

// Close 关闭写入器
// 停止后台协程、flush 文件、压缩剩余、清理旧文件
func (fw *FileWriter) Close() error {
	var closeErr error
	fw.once.Do(func() {
		fw.mu.Lock()
		fw.closed = true
		fw.mu.Unlock()

		// 停止时间轮转定时器
		if fw.ticker != nil {
			fw.ticker.Stop()
		}

		// 关闭文件
		if fw.file != nil {
			closeErr = fw.file.Close()
		}

		// 关闭 done 通道，通知后台协程退出
		close(fw.done)

		// 等待后台协程完全退出
		fw.bgWg.Wait()

		// 关闭压缩通道（协程已退出，安全关闭）
		close(fw.compressCh)

		// 最终清理
		fw.cleanup()
	})
	return closeErr
}

// --- 轮转逻辑 ---

// needRotate 判断是否需要轮转（完整检查：大小 + 时间，供 background goroutine 使用）
func (fw *FileWriter) needRotate() bool {
	return fw.needRotateBySize() || fw.needRotateByTime()
}

// needRotateBySize 仅检查大小轮转（热路径使用，避免 time.Now 系统调用）
func (fw *FileWriter) needRotateBySize() bool {
	return fw.cfg.MaxSize > 0 && fw.currentSize >= fw.cfg.MaxSize
}

// needRotateByTime 仅检查时间轮转（由 background goroutine 使用）
func (fw *FileWriter) needRotateByTime() bool {
	return fw.cfg.Interval > 0 && time.Since(fw.createdAt) >= fw.cfg.Interval
}

// rotate 执行文件轮转
// 1. 关闭当前文件
// 2. 重命名为带时间戳的轮转文件
// 3. 打开新文件
// 4. 异步压缩旧文件
// 5. 清理过期文件
func (fw *FileWriter) rotate() error {
	if fw.file != nil {
		fw.file.Close()
		fw.file = nil
	}

	// 生成轮转文件名：app-2026-08-27T10-30-45.log
	oldPath := filepath.Join(fw.cfg.Dir, fw.cfg.BaseName+fw.cfg.Ext)
	ts := time.Now().Format("2006-01-02T15-04-05.000000")
	rotatedName := fmt.Sprintf("%s-%s%s", fw.cfg.BaseName, ts, fw.cfg.Ext)
	rotatedPath := filepath.Join(fw.cfg.Dir, rotatedName)

	// 重命名当前文件为轮转文件
	if err := os.Rename(oldPath, rotatedPath); err != nil {
		// 重命名失败时尝试重新打开原文件，保证写入不中断
		openErr := fw.openFile()
		if openErr != nil {
			return fmt.Errorf("rename failed and reopen also failed: rename=%v reopen=%v", err, openErr)
		}
		return fmt.Errorf("rename %s to %s: %w", oldPath, rotatedPath, err)
	}

	// 异步压缩轮转文件
	if fw.cfg.Compress {
		select {
		case fw.compressCh <- rotatedPath:
		default:
			// 压缩通道满，跳过压缩（降级为不压缩）
			fmt.Fprintf(os.Stderr, "[tlog] compress channel full, skip compressing %s\n", rotatedPath)
		}
	}

	// 打开新的当前日志文件
	if err := fw.openFile(); err != nil {
		return err
	}

	// 清理旧文件
	fw.cleanup()

	return nil
}

// --- 清理逻辑 ---

// cleanup 清理过期的轮转文件
// 按修改时间排序，删除超过 maxBackups 和 maxAge 的文件
func (fw *FileWriter) cleanup() {
	// 收集所有轮转文件（匹配 pattern: base-*.ext，排除当前文件）
	pattern := filepath.Join(fw.cfg.Dir, fw.cfg.BaseName+"-*"+fw.cfg.Ext)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[tlog] glob %s: %v\n", pattern, err)
		return
	}

	if len(matches) == 0 {
		return
	}

	// 按修改时间排序（最旧的在前）
	type fileInfo struct {
		path    string
		modTime time.Time
	}
	files := make([]fileInfo, 0, len(matches))
	for _, path := range matches {
		// 跳过当前正在写入的文件（通过比较大小判断）
		if fw.isFileActive(path) {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		files = append(files, fileInfo{path: path, modTime: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})

	now := time.Now()

	// 按 maxBackups 删除：保留最新的 N 个
	if fw.cfg.MaxBackups > 0 && len(files) > fw.cfg.MaxBackups {
		excess := len(files) - fw.cfg.MaxBackups
		for i := 0; i < excess; i++ {
			fw.removeFile(files[i].path)
		}
		// 更新 files 列表
		files = files[excess:]
	}

	// 按 maxAge 删除：删除超过保留天数的文件
	if fw.cfg.MaxAge > 0 {
		cutoff := now.Add(-fw.cfg.MaxAge)
		for _, f := range files {
			if f.modTime.Before(cutoff) {
				fw.removeFile(f.path)
			}
		}
	}
}

// isFileActive 判断文件是否是当前正在写入的文件
func (fw *FileWriter) isFileActive(path string) bool {
	if fw.file == nil {
		return false
	}
	activePath := filepath.Join(fw.cfg.Dir, fw.cfg.BaseName+fw.cfg.Ext)
	return path == activePath
}

// removeFile 删除文件，忽略错误（清理失败不阻塞主流程）
func (fw *FileWriter) removeFile(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "[tlog] remove old file %s: %v\n", path, err)
	}
	// 同时尝试删除对应的 .gz 文件
	gzPath := path + ".gz"
	if err := os.Remove(gzPath); err != nil && !os.IsNotExist(err) {
		// 忽略：可能没有压缩文件
	}
}

// --- 异步压缩 ---

// background 后台协程：处理时间轮转和异步压缩
func (fw *FileWriter) background() {
	defer fw.bgWg.Done()

	// 启动时间轮转定时器
	if fw.cfg.Interval > 0 {
		fw.ticker = time.NewTicker(fw.cfg.Interval)
	}

	for {
		select {
		case <-fw.done:
			// 非阻塞排空压缩通道
			for {
				select {
				case path := <-fw.compressCh:
					if path != "" {
						fw.doCompress(path)
					}
				default:
					return
				}
			}
		case <-tickerC(fw.ticker):
			// 时间轮转触发
			fw.mu.Lock()
			if !fw.closed && fw.needRotate() {
				if err := fw.rotate(); err != nil {
					fmt.Fprintf(os.Stderr, "[tlog] time-based rotate: %v\n", err)
				}
			}
			fw.mu.Unlock()
		case path := <-fw.compressCh:
			// 异步压缩
			fw.doCompress(path)
		}
	}
}

// tickerC 安全获取 ticker 的 channel（ticker 为 nil 时返回 nil channel，永远阻塞）
func tickerC(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// doCompress 执行 gzip 压缩
func (fw *FileWriter) doCompress(srcPath string) {
	gzPath := srcPath + ".gz"

	srcFile, err := os.Open(srcPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[tlog] compress open %s: %v\n", srcPath, err)
		return
	}
	defer srcFile.Close()

	dstFile, err := os.Create(gzPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[tlog] compress create %s: %v\n", gzPath, err)
		return
	}
	defer dstFile.Close()

	gzWriter, err := gzip.NewWriterLevel(dstFile, gzip.BestSpeed)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[tlog] compress gzip: %v\n", err)
		return
	}
	defer gzWriter.Close()

	if _, err := io.Copy(gzWriter, srcFile); err != nil {
		fmt.Fprintf(os.Stderr, "[tlog] compress copy: %v\n", err)
		return
	}

	// 确保数据写入磁盘
	if err := gzWriter.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "[tlog] compress close gzip: %v\n", err)
		return
	}
	if err := dstFile.Sync(); err != nil {
		fmt.Fprintf(os.Stderr, "[tlog] compress sync: %v\n", err)
		return
	}

	// 压缩成功，删除原始文件
	if err := os.Remove(srcPath); err != nil {
		fmt.Fprintf(os.Stderr, "[tlog] compress remove original %s: %v\n", srcPath, err)
	}
}

// --- 辅助方法 ---

// ActivePath 返回当前正在写入的文件路径
func (fw *FileWriter) ActivePath() string {
	return filepath.Join(fw.cfg.Dir, fw.cfg.BaseName+fw.cfg.Ext)
}

// CurrentSize 返回当前文件已写字节数
func (fw *FileWriter) CurrentSize() int64 {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return fw.currentSize
}

// ListBackups 列出所有轮转文件（不含当前文件），按时间从旧到新排序
func (fw *FileWriter) ListBackups() []string {
	pattern := filepath.Join(fw.cfg.Dir, fw.cfg.BaseName+"-*"+fw.cfg.Ext)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}

	activePath := fw.ActivePath()
	result := make([]string, 0, len(matches))
	for _, path := range matches {
		if path == activePath {
			continue
		}
		// 排除 .gz 文件
		if strings.HasSuffix(path, ".gz") {
			continue
		}
		result = append(result, path)
	}
	sort.Strings(result) // 按名称排序（时间戳名称天然有序）
	return result
}
