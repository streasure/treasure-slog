// Package writer 提供高性能并发安全的文件写入器
// 支持按大小/时间轮转、自动目录创建、过期清理
package writer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var errFileWriterClosed = errors.New("file writer closed")

// FileWriterConfig 文件写入器配置
type FileWriterConfig struct {
	Dir        string        // 日志目录
	BaseName   string        // 文件基础名（不含扩展名）
	Ext        string        // 扩展名（如 ".log"），为空时默认 ".log"
	MaxSize    int64         // 单文件最大字节数（0=不限制，仅按时间轮转）
	MaxBackups int           // 最大保留备份数（0=不限制）
	MaxAge     time.Duration // 最大保留时长（0=不限制）
	Interval   time.Duration // 时间轮转间隔（0=禁用时间轮转）
	DirPerm    os.FileMode   // 目录权限（默认 0755）
	FilePerm   os.FileMode   // 文件权限（默认 0644）
}

// FileWriter 高性能并发安全的文件写入器
// 实现 io.WriteCloser 接口，支持按大小/时间轮转、过期清理
type FileWriter struct {
	cfg FileWriterConfig

	// --- 运行时状态（由 mu 保护）---
	mu          sync.Mutex
	file        *os.File // 当前写入的文件
	currentSize int64    // 当前文件已写字节数
	createdAt   time.Time
	closed      bool

	// --- 后台协程 ---
	ticker *time.Ticker
	done   chan struct{}   // 关闭信号
	bgWg   sync.WaitGroup // 等待后台协程退出
	once   sync.Once
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

	// 自动创建目录
	if err := os.MkdirAll(cfg.Dir, cfg.DirPerm); err != nil {
		return nil, fmt.Errorf("create log directory %s: %w", cfg.Dir, err)
	}

	fw := &FileWriter{
		cfg:  cfg,
		done: make(chan struct{}),
	}

	// 打开或创建当前日志文件
	if err := fw.openFile(); err != nil {
		return nil, err
	}

	// 启动后台协程：时间轮转
	fw.bgWg.Add(1)
	go fw.background()

	return fw, nil
}

// openFile 打开或创建日志文件
func (fw *FileWriter) openFile() error {
	path := filepath.Join(fw.cfg.Dir, fw.cfg.BaseName+fw.cfg.Ext)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, fw.cfg.FilePerm)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", path, err)
	}
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

	if fw.needRotateBySize() {
		if rotateErr := fw.rotate(); rotateErr != nil {
			fmt.Fprintf(os.Stderr, "[tlog] rotate failed: %v\n", rotateErr)
		}
	}

	n, err = fw.file.Write(p)
	fw.currentSize += int64(n)

	if fw.needRotateBySize() {
		if rotateErr := fw.rotate(); rotateErr != nil {
			fmt.Fprintf(os.Stderr, "[tlog] rotate failed: %v\n", rotateErr)
		}
	}

	return n, err
}

// Close 关闭写入器
func (fw *FileWriter) Close() error {
	var closeErr error
	fw.once.Do(func() {
		fw.mu.Lock()
		fw.closed = true
		fw.mu.Unlock()

		if fw.ticker != nil {
			fw.ticker.Stop()
		}

		if fw.file != nil {
			closeErr = fw.file.Close()
		}

		close(fw.done)
		fw.bgWg.Wait()
		fw.cleanup()
	})
	return closeErr
}

// --- 轮转逻辑 ---

func (fw *FileWriter) needRotate() bool {
	return fw.needRotateBySize() || fw.needRotateByTime()
}

func (fw *FileWriter) needRotateBySize() bool {
	return fw.cfg.MaxSize > 0 && fw.currentSize >= fw.cfg.MaxSize
}

func (fw *FileWriter) needRotateByTime() bool {
	return fw.cfg.Interval > 0 && time.Since(fw.createdAt) >= fw.cfg.Interval
}

// rotate 执行文件轮转
// 1. 关闭当前文件
// 2. 重命名为带时间戳的轮转文件
// 3. 打开新文件
// 4. 清理过期文件
func (fw *FileWriter) rotate() error {
	if fw.file != nil {
		fw.file.Close()
		fw.file = nil
	}

	oldPath := filepath.Join(fw.cfg.Dir, fw.cfg.BaseName+fw.cfg.Ext)
	ts := time.Now().Format("2006-01-02T15-04-05.000000")
	rotatedName := fmt.Sprintf("%s-%s%s", fw.cfg.BaseName, ts, fw.cfg.Ext)
	rotatedPath := filepath.Join(fw.cfg.Dir, rotatedName)

	if err := os.Rename(oldPath, rotatedPath); err != nil {
		openErr := fw.openFile()
		if openErr != nil {
			return fmt.Errorf("rename failed and reopen also failed: rename=%v reopen=%v", err, openErr)
		}
		return fmt.Errorf("rename %s to %s: %w", oldPath, rotatedPath, err)
	}

	if err := fw.openFile(); err != nil {
		return err
	}

	fw.cleanup()
	return nil
}

// --- 清理逻辑 ---

// cleanup 清理过期的轮转文件
func (fw *FileWriter) cleanup() {
	pattern := filepath.Join(fw.cfg.Dir, fw.cfg.BaseName+"-*"+fw.cfg.Ext)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[tlog] glob %s: %v\n", pattern, err)
		return
	}

	if len(matches) == 0 {
		return
	}

	type fileInfo struct {
		path    string
		modTime time.Time
	}
	files := make([]fileInfo, 0, len(matches))
	for _, path := range matches {
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

	if fw.cfg.MaxBackups > 0 && len(files) > fw.cfg.MaxBackups {
		excess := len(files) - fw.cfg.MaxBackups
		for i := 0; i < excess; i++ {
			fw.removeFile(files[i].path)
		}
		files = files[excess:]
	}

	if fw.cfg.MaxAge > 0 {
		cutoff := now.Add(-fw.cfg.MaxAge)
		for _, f := range files {
			if f.modTime.Before(cutoff) {
				fw.removeFile(f.path)
			}
		}
	}
}

func (fw *FileWriter) isFileActive(path string) bool {
	if fw.file == nil {
		return false
	}
	activePath := filepath.Join(fw.cfg.Dir, fw.cfg.BaseName+fw.cfg.Ext)
	return path == activePath
}

// removeFile 删除文件
func (fw *FileWriter) removeFile(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "[tlog] remove old file %s: %v\n", path, err)
	}
}

// --- 后台协程 ---

// background 后台协程：处理时间轮转
func (fw *FileWriter) background() {
	defer fw.bgWg.Done()

	if fw.cfg.Interval > 0 {
		fw.ticker = time.NewTicker(fw.cfg.Interval)
	}

	for {
		select {
		case <-fw.done:
			return
		case <-tickerC(fw.ticker):
			fw.mu.Lock()
			if !fw.closed && fw.needRotate() {
				if err := fw.rotate(); err != nil {
					fmt.Fprintf(os.Stderr, "[tlog] time-based rotate: %v\n", err)
				}
			}
			fw.mu.Unlock()
		}
	}
}

func tickerC(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
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
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}
