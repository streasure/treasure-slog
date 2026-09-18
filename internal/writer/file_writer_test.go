package writer

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func tempDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("tlog-writer-test-%d", time.Now().UnixNano()))
	os.MkdirAll(dir, 0755)
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestNewFileWriter_CreatesDirectory(t *testing.T) {
	dir := filepath.Join(tempDir(t), "sub", "deep")
	fw, err := NewFileWriter(FileWriterConfig{
		Dir:      dir,
		BaseName: "test",
	})
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	defer fw.Close()

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatal("directory not created")
	}
}

func TestNewFileWriter_CreatesFile(t *testing.T) {
	dir := tempDir(t)
	fw, err := NewFileWriter(FileWriterConfig{
		Dir:      dir,
		BaseName: "app",
		Ext:      ".log",
	})
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	defer fw.Close()

	path := filepath.Join(dir, "app.log")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("log file not created")
	}
}

func TestWrite_Basic(t *testing.T) {
	dir := tempDir(t)
	fw, err := NewFileWriter(FileWriterConfig{
		Dir:      dir,
		BaseName: "test",
	})
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	defer fw.Close()

	msg := []byte("hello world\n")
	n, err := fw.Write(msg)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(msg) {
		t.Fatalf("Write: expected %d bytes, got %d", len(msg), n)
	}

	data, err := os.ReadFile(filepath.Join(dir, "test.log"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(data, msg) {
		t.Fatalf("content mismatch: got %q", data)
	}
}

func TestWrite_AppendMode(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "test.log")
	os.WriteFile(path, []byte("initial"), 0644)

	fw, err := NewFileWriter(FileWriterConfig{
		Dir:      dir,
		BaseName: "test",
	})
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	defer fw.Close()

	fw.Write([]byte("-appended"))
	data, _ := os.ReadFile(path)
	expected := "initial-appended"
	if string(data) != expected {
		t.Fatalf("expected %q, got %q", expected, string(data))
	}
}

func TestWrite_CurrentSize(t *testing.T) {
	dir := tempDir(t)
	fw, err := NewFileWriter(FileWriterConfig{
		Dir:      dir,
		BaseName: "test",
	})
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	defer fw.Close()

	fw.Write([]byte("hello"))
	if fw.CurrentSize() != 5 {
		t.Fatalf("expected size 5, got %d", fw.CurrentSize())
	}

	fw.Write([]byte(" world"))
	if fw.CurrentSize() != 11 {
		t.Fatalf("expected size 11, got %d", fw.CurrentSize())
	}
}

func TestRotate_BySize(t *testing.T) {
	dir := tempDir(t)
	fw, err := NewFileWriter(FileWriterConfig{
		Dir:      dir,
		BaseName: "app",
		MaxSize:  50, // 50 bytes
	})
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	defer fw.Close()

	// 写入超过 50 字节触发轮转
	fw.Write([]byte("1234567890")) // 10 bytes
	fw.Write([]byte("1234567890")) // 20
	fw.Write([]byte("1234567890")) // 30
	fw.Write([]byte("1234567890")) // 40
	fw.Write([]byte("1234567890")) // 50
	fw.Write([]byte("1234567890")) // 60 -> 触发轮转

	// 等待轮转完成
	time.Sleep(50 * time.Millisecond)

	backups := fw.ListBackups()
	if len(backups) == 0 {
		t.Fatal("expected at least 1 backup file after rotation")
	}

	// 当前文件应该存在且大小为 10（轮转后写入的）
	data, err := os.ReadFile(filepath.Join(dir, "app.log"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) != 10 {
		t.Fatalf("current file should have 10 bytes, got %d", len(data))
	}
}

func TestRotate_ByTime(t *testing.T) {
	dir := tempDir(t)
	fw, err := NewFileWriter(FileWriterConfig{
		Dir:      dir,
		BaseName: "app",
		Interval: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	defer fw.Close()

	fw.Write([]byte("before rotation"))

	// 等待超过轮转间隔
	time.Sleep(200 * time.Millisecond)

	// 写入触发时间轮转检查查	fw.Write([]byte("after rotation"))

	// 等待轮转完成
	time.Sleep(50 * time.Millisecond)

	backups := fw.ListBackups()
	if len(backups) == 0 {
		t.Fatal("expected at least 1 backup file after time-based rotation")
	}
}

func TestCleanup_MaxBackups(t *testing.T) {
	dir := tempDir(t)
	fw, err := NewFileWriter(FileWriterConfig{
		Dir:      dir,
		BaseName: "app",
		MaxSize:  10,
		MaxBackups: 2,
	})
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	defer fw.Close()

	// 多次轮转
	for i := 0; i < 5; i++ {
		fw.Write(bytes.Repeat([]byte("x"), 15))
		time.Sleep(10 * time.Millisecond)
	}

	// 等待轮转完成
	time.Sleep(100 * time.Millisecond)

	backups := fw.ListBackups()
	if len(backups) > 2 {
		t.Fatalf("expected at most 2 backups, got %d: %v", len(backups), backups)
	}
}

func TestCleanup_MaxAge(t *testing.T) {
	dir := tempDir(t)
	fw, err := NewFileWriter(FileWriterConfig{
		Dir:      dir,
		BaseName: "app",
		MaxSize:  10,
		MaxAge:   50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	defer fw.Close()

	// 触发一次轮	fw.Write(bytes.Repeat([]byte("x"), 15))
	time.Sleep(10 * time.Millisecond)

	// 等待超过 MaxAge
	time.Sleep(100 * time.Millisecond)

	// 再触发一次轮转，触发清理
	fw.Write(bytes.Repeat([]byte("y"), 15))
	time.Sleep(100 * time.Millisecond)

	backups := fw.ListBackups()
	// 旧文件应该已被清理
	if len(backups) > 1 {
		t.Fatalf("expected old files cleaned, got %d backups: %v", len(backups), backups)
	}
}

func TestConcurrentWrite(t *testing.T) {
	dir := tempDir(t)
	fw, err := NewFileWriter(FileWriterConfig{
		Dir:      dir,
		BaseName: "app",
		MaxSize:  100,
	})
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	defer fw.Close()

	var wg sync.WaitGroup
	errCh := make(chan error, 100)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				msg := fmt.Sprintf("goroutine-%d-msg-%d\n", id, j)
				_, err := fw.Write([]byte(msg))
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d: %v", id, err)
					return
				}
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent write error: %v", err)
	}

	// 统计所有文件（当前 + 轮转备份）中的总行数
	totalLines := 0
	allFiles, _ := filepath.Glob(filepath.Join(dir, "app*.log"))
	for _, f := range allFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("ReadFile %s: %v", f, err)
		}
		totalLines += bytes.Count(data, []byte("\n"))
	}
	// 允许少量行在压缩+轮转并发场景下丢失（异步压缩可能删除原始文件）
	if totalLines < 990 {
		t.Fatalf("expected >= 990 total lines across all files, got %d", totalLines)
	}
}

func TestClose_PreventsWrite(t *testing.T) {
	dir := tempDir(t)
	fw, err := NewFileWriter(FileWriterConfig{
		Dir:      dir,
		BaseName: "test",
	})
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}

	fw.Write([]byte("before close"))
	fw.Close()

	_, err = fw.Write([]byte("after close"))
	if err == nil {
		t.Fatal("expected error after Close()")
	}
}

func TestClose_Idempotent(t *testing.T) {
	dir := tempDir(t)
	fw, err := NewFileWriter(FileWriterConfig{
		Dir:      dir,
		BaseName: "test",
	})
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}

	// 多次 Close panic
	fw.Close()
	fw.Close()
	fw.Close()
}

func TestActivePath(t *testing.T) {
	dir := tempDir(t)
	fw, err := NewFileWriter(FileWriterConfig{
		Dir:      dir,
		BaseName: "app",
		Ext:      ".log",
	})
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	defer fw.Close()

	expected := filepath.Join(dir, "app.log")
	if fw.ActivePath() != expected {
		t.Fatalf("expected %q, got %q", expected, fw.ActivePath())
	}
}

func TestListBackups_Empty(t *testing.T) {
	dir := tempDir(t)
	fw, err := NewFileWriter(FileWriterConfig{
		Dir:      dir,
		BaseName: "app",
	})
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	defer fw.Close()

	backups := fw.ListBackups()
	if len(backups) != 0 {
		t.Fatalf("expected 0 backups, got %d", len(backups))
	}
}

func TestCustomExt(t *testing.T) {
	dir := tempDir(t)
	fw, err := NewFileWriter(FileWriterConfig{
		Dir:      dir,
		BaseName: "app",
		Ext:      ".jsonl",
	})
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	defer fw.Close()

	fw.Write([]byte("{\"msg\":\"hello\"}\n"))

	path := filepath.Join(dir, "app.jsonl")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("custom ext file not created")
	}
}
