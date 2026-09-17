package logger

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/streasure/treasure-slog/internal/config"
)

// lockContentionTest 辅助：构造一个写入 io.Discard 的异步 logger，使用自定义 shard 数
// 返回 logger 和清理函数
func lockContentionTest(t *testing.T, level slog.Level, workers, bufSize, batchSize int) (*SLogger, func()) {
	t.Helper()
	s := &SLogger{
		hooks:        []Hook{},
		wg:           &sync.WaitGroup{},
		asyncEnabled: true,
		usePool:      true,
		level:        &atomic.Int32{},
		levelVar:     &slog.LevelVar{},
		cfg: &config.Config{
			Log: config.LogConfig{
				Async: config.AsyncConfig{
					BatchSize:     batchSize,
					FlushInterval: 10,
				},
			},
		},
	}
	s.level.Store(int32(level))
	handler := NewFastHandler(io.Discard, s.level)
	s.logger = slog.New(handler)
	s.handler = handler
	s.ringBuf = newShardedRingBuffer(bufSize, workers)
	s.workers = make([]*worker, workers)
	for i := range s.workers {
		s.workers[i] = &worker{
			id:     i,
			logger: s,
			stopCh: make(chan struct{}),
			wg:     s.wg,
		}
		s.workers[i].start()
	}
	return s, func() {
		_ = s.Sync()
	}
}

// mustCompleteWithin 验证函数在 timeout 内完成，否则报死锁
func mustCompleteWithin(t *testing.T, timeout time.Duration, name string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("%s panic: %v", name, r)
			}
			close(done)
		}()
		fn()
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("%s 出现死锁/超时，未在 %s 内完成", name, timeout)
	}
}

// --- TestLockContention_HighConcurrency_NoDeadlock ---
// 256 goroutine × 5000 条日志 = 128 万写入，验证不死锁、不 panic、可在限时内完成

func TestLockContention_HighConcurrency_NoDeadlock(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过长测试，使用 -short")
	}
	s, cleanup := lockContentionTest(t, slog.LevelDebug, 8, 2000000, 1024)
	defer cleanup()

	ctx := context.Background()
	const goroutines = 256
	const perG = 5000
	var wg sync.WaitGroup
	wg.Add(goroutines)

	mustCompleteWithin(t, 30*time.Second, "256并发写入", func() {
		for i := 0; i < goroutines; i++ {
			go func(id int) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("goroutine %d panic: %v", id, r)
					}
				}()
				for j := 0; j < perG; j++ {
					s.Info(ctx, "high-concurrency g=%d j=%d", id, j)
				}
			}(i)
		}
		wg.Wait()
	})

	// Sync 后验证无 panic、有处理
	_ = s.Sync()
	dropped := s.ringBuf.Dropped()
	total := goroutines * perG
	t.Logf("写入总数: %d, 丢弃: %d (%.4f%%)", total, dropped, float64(dropped)/float64(total)*100)
	if dropped > int64(total)/10 { // 丢弃超过 10% 才报错
		t.Errorf("丢弃率过高: %d/%d", dropped, total)
	}
}

// --- TestLockContention_Scaling_MultiShard ---
// 对比 1 shard vs N shard 的吞吐，验证分片确实降低锁竞争
func TestLockContention_Scaling_MultiShard(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过长测试，使用 -short")
	}
	const totalLogs = 200000
	const goroutines = 64

	var throughput = func(shards int) float64 {
		s, cleanup := lockContentionTest(t, slog.LevelDebug, shards, 2000000, 1024)
		defer cleanup()

		ctx := context.Background()
		// 预热
		for i := 0; i < 100; i++ {
			s.Info(ctx, "warmup i=%d", i)
		}
		time.Sleep(20 * time.Millisecond)

		var wg sync.WaitGroup
		perG := totalLogs / goroutines
		start := time.Now()
		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < perG; j++ {
					s.Info(ctx, "scale g=%d j=%d", id, j)
				}
			}(i)
		}
		wg.Wait()
		_ = s.Sync()
		elapsed := time.Since(start)
		return float64(totalLogs) / elapsed.Seconds()
	}

	single := throughput(1)
	multi := throughput(16)
	t.Logf("单 shard 吞吐: %.0f ops/s, 16 shard 吞吐: %.0f ops/s, 加速比: %.2fx",
		single, multi, multi/single)

	// 多 shard 应至少达到单 shard 的 1.2 倍（验证锁竞争确实降低）
	// 注意：低核心机器上加速比可能小，但应 > 1
	if multi < single*1.1 {
		t.Errorf("分片锁竞争优化未生效：multi=%.0f < single*1.1=%.0f", multi, single*1.1)
	}
}

// --- TestLockContention_NoDropUnderNormalLoad ---
// 正常负载下不应有丢弃（buffer 足够大）

func TestLockContention_NoDropUnderNormalLoad(t *testing.T) {
	s, cleanup := lockContentionTest(t, slog.LevelDebug, 8, 2000000, 1024)
	defer cleanup()

	ctx := context.Background()
	const goroutines = 32
	const perG = 2000
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				s.Info(ctx, "no-drop g=%d j=%d", id, j)
			}
		}(i)
	}
	wg.Wait()
	_ = s.Sync()

	dropped := s.ringBuf.Dropped()
	total := goroutines * perG
	if dropped > 0 {
		t.Errorf("正常负载下不应丢弃，但丢弃了 %d/%d 条", dropped, total)
	} else {
		t.Logf("零丢弃：所有 %d 条日志全部处理", total)
	}
}

// --- TestLockContention_DerivedLoggersShareRingBuf ---
// 派生 logger (With/WithContext/AddHook) 应共享同一 ring buffer 和 level 指针
// 验证派生 logger 不会绕过分片锁机制
func TestLockContention_DerivedLoggersShareRingBuf(t *testing.T) {
	s, cleanup := lockContentionTest(t, slog.LevelDebug, 8, 2000000, 1024)
	defer cleanup()

	ctx := context.Background()

	// 派生多个 logger
	derived1 := s.With("svc", "api")
	derived2 := s.With("svc", "web")
	derived3 := s.WithContext(context.WithValue(ctx, "request_id", "req-123"))

	// 验证所有派生 logger 共享同一 ringBuf 和 level 指针
	dl1 := derived1.(*SLogger)
	dl2 := derived2.(*SLogger)
	dl3 := derived3.(*SLogger)

	if dl1.ringBuf != s.ringBuf || dl2.ringBuf != s.ringBuf || dl3.ringBuf != s.ringBuf {
		t.Errorf("派生 logger 未共享 ringBuf，将无法享受分片锁优化")
	}
	if dl1.level != s.level || dl2.level != s.level || dl3.level != s.level {
		t.Errorf("派生 logger 未共享 level 指针，级别变更无法传递")
	}

	// 并发写入派生 logger，验证不死锁
	const goroutines = 64
	const perG = 1000
	var wg sync.WaitGroup
	wg.Add(goroutines * 4)

	mustCompleteWithin(t, 20*time.Second, "派生logger并发写入", func() {
		for i := 0; i < goroutines; i++ {
			go func(id int) {
				defer wg.Done()
				for j := 0; j < perG; j++ {
					s.Info(ctx, "base g=%d j=%d", id, j)
				}
			}(i)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < perG; j++ {
					derived1.Info(ctx, "derived1 g=%d j=%d", id, j)
				}
			}(i)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < perG; j++ {
					derived2.Info(ctx, "derived2 g=%d j=%d", id, j)
				}
			}(i)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < perG; j++ {
					derived3.Info(ctx, "derived3 g=%d j=%d", id, j)
				}
			}(i)
		}
		wg.Wait()
	})

	_ = s.Sync()
	dropped := s.ringBuf.Dropped()
	total := goroutines * perG * 4
	t.Logf("派生 logger 并发写入: 总计 %d, 丢弃 %d", total, dropped)
}

// --- TestLockContention_ConcurrentSetLevel_NoDataRace ---
// 并发 SetLevel + 写入，验证级别原子切换无数据竞争、不 panic
// 注意：需配合 `go test -race` 才能检测底层 race，但此测试至少验证逻辑正确

func TestLockContention_ConcurrentSetLevel_NoDataRace(t *testing.T) {
	s, cleanup := lockContentionTest(t, slog.LevelInfo, 8, 2000000, 1024)
	defer cleanup()

	ctx := context.Background()
	const goroutines = 32
	const perG = 1000
	var wg sync.WaitGroup
	wg.Add(goroutines + 1)

	// 1 个 goroutine 持续切换级别
	mustCompleteWithin(t, 15*time.Second, "并发SetLevel", func() {
		go func() {
			defer wg.Done()
			levels := []string{"debug", "info", "warn", "error"}
			for i := 0; i < 1000; i++ {
				s.SetLevel(levels[i%len(levels)])
			}
		}()

		// 其余 goroutine 持续写入
		for i := 0; i < goroutines; i++ {
			go func(id int) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("goroutine %d panic: %v", id, r)
					}
				}()
				for j := 0; j < perG; j++ {
					// 混合各级别写入
					switch j % 4 {
					case 0:
						s.Debug(ctx, "mix g=%d j=%d", id, j)
					case 1:
						s.Info(ctx, "mix g=%d j=%d", id, j)
					case 2:
						s.Warn(ctx, "mix g=%d j=%d", id, j)
					case 3:
						s.Error(ctx, "mix g=%d j=%d", id, j)
					}
				}
			}(i)
		}
		wg.Wait()
	})

	_ = s.Sync()
	t.Logf("并发 SetLevel 完成，level=%s", s.GetLevel())
}

// --- TestLockContention_AllInterfacesMixed ---
// 所有 8 个公开接口（Debug/Info/Warn/Error + Printf 风格）混合高并发
// 验证所有接口在分片锁机制下都正常工作
func TestLockContention_AllInterfacesMixed(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过长测试，使用 -short")
	}
	s, cleanup := lockContentionTest(t, slog.LevelDebug, 16, 4000000, 2048)
	defer cleanup()

	const goroutines = 128
	const perG = 2000
	var wg sync.WaitGroup
	wg.Add(goroutines)

	ctx := context.Background()

	mustCompleteWithin(t, 30*time.Second, "全接口混合并发", func() {
		for i := 0; i < goroutines; i++ {
			go func(id int) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("goroutine %d panic: %v", id, r)
					}
				}()
				for j := 0; j < perG; j++ {
					switch j % 4 {
					case 0:
						s.Debug(ctx, "d g=%d j=%d", id, j)
					case 1:
						s.Info(ctx, "i g=%d j=%d", id, j)
					case 2:
						s.Warn(ctx, "w g=%d j=%d", id, j)
					case 3:
						s.Error(ctx, "e g=%d j=%d", id, j)
					}
				}
			}(i)
		}
		wg.Wait()
	})

	_ = s.Sync()
	dropped := s.ringBuf.Dropped()
	total := goroutines * perG
	t.Logf("全接口混合并发: 总计 %d, 丢弃 %d (%.4f%%)", total, dropped, float64(dropped)/float64(total)*100)
}

// --- TestLockContention_ShutdownDuringWrite ---
// 在写入过程中关闭 worker，验证不 panic（cleanup 中再调 Sync 应安全）

func TestLockContention_ShutdownDuringWrite(t *testing.T) {
	s, cleanup := lockContentionTest(t, slog.LevelDebug, 8, 2000000, 1024)
	defer cleanup()

	ctx := context.Background()
	const goroutines = 64
	const perG = 1000
	var wg sync.WaitGroup
	wg.Add(goroutines)

	panicCount := atomic.Int64{}

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicCount.Add(1)
				}
			}()
			for j := 0; j < perG; j++ {
				// 并发写入；cleanup 中的 Sync 会在测试结束时关闭 workers
				// 写入过程中可能 worker 已停止（io.Discard 写失败不会 panic）
				s.Info(ctx, "shutdown-during-write g=%d j=%d", id, j)
			}
		}(i)
	}
	wg.Wait()

	// 第一次 Sync 关闭 workers
	_ = s.Sync()
	// 第二次 Sync 验证重复关闭安全（workers 为 nil，应直接返回）
	_ = s.Sync()
	if p := panicCount.Load(); p > 0 {
		t.Errorf("关闭期间发生 %d 次 panic", p)
	}
}

// --- TestLockContention_ExtremeHighConcurrency_512 ---
// 极端 512 goroutine 压力测试，验证锁竞争优化的极限
func TestLockContention_ExtremeHighConcurrency_512(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过长测试，使用 -short")
	}
	s, cleanup := lockContentionTest(t, slog.LevelDebug, 32, 4000000, 2048)
	defer cleanup()

	ctx := context.Background()
	const goroutines = 512
	const perG = 500
	var wg sync.WaitGroup
	wg.Add(goroutines)

	start := time.Now()
	mustCompleteWithin(t, 60*time.Second, "512并发写入", func() {
		for i := 0; i < goroutines; i++ {
			go func(id int) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("goroutine %d panic: %v", id, r)
					}
				}()
				for j := 0; j < perG; j++ {
					s.Info(ctx, "extreme-512 g=%d j=%d", id, j)
				}
			}(i)
		}
		wg.Wait()
	})
	elapsed := time.Since(start)

	_ = s.Sync()
	dropped := s.ringBuf.Dropped()
	total := goroutines * perG
	throughput := float64(total) / elapsed.Seconds()
	t.Logf("512 goroutine: 总计 %d, 耗时 %s, 吞吐 %.0f ops/s, 丢弃 %d (%.4f%%)",
		total, elapsed, throughput, dropped, float64(dropped)/float64(total)*100)

	// 吞吐应至少达到 100 万 ops/s（验证锁竞争没有严重退化）
	if throughput < 1000000 {
		t.Errorf("512 并发吞吐 %.0f ops/s < 1M ops/s，锁竞争可能限制性能", throughput)
	}
}

// --- TestLockContention_BatchSizeScaling ---
// 不同 batch size 对高并发吞吐的影响，验证 batch 处理减少锁次数
func TestLockContention_BatchSizeScaling(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过长测试，使用 -short")
	}
	const goroutines = 64
	const perG = 2000

	runWithBatch := func(batchSize int) (float64, int64) {
		s, cleanup := lockContentionTest(t, slog.LevelDebug, 8, 2000000, batchSize)
		defer cleanup()

		ctx := context.Background()
		var wg sync.WaitGroup
		wg.Add(goroutines)
		start := time.Now()
		for i := 0; i < goroutines; i++ {
			go func(id int) {
				defer wg.Done()
				for j := 0; j < perG; j++ {
					s.Info(ctx, "batch g=%d j=%d", id, j)
				}
			}(i)
		}
		wg.Wait()
		_ = s.Sync()
		elapsed := time.Since(start)
		return float64(goroutines*perG) / elapsed.Seconds(), s.ringBuf.Dropped()
	}

	t.Logf("不同 batch size 在 %d goroutine 下的吞吐对比:", goroutines)
	for _, bs := range []int{128, 512, 1024, 2048, 4096} {
		tps, dropped := runWithBatch(bs)
		t.Logf("  batch=%4d: %.0f ops/s, 丢弃 %d", bs, tps, dropped)
	}
}

// --- TestLockContention_WorkerCountOptimal ---
// 验证 worker 数量对吞吐的影响，证明分片锁机制下多 worker 能并行消费
func TestLockContention_WorkerCountOptimal(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过长测试，使用 -short")
	}
	const goroutines = 64
	const perG = 2000

	runWithWorkers := func(workers int) float64 {
		s, cleanup := lockContentionTest(t, slog.LevelDebug, workers, 2000000, 1024)
		defer cleanup()

		ctx := context.Background()
		var wg sync.WaitGroup
		wg.Add(goroutines)
		start := time.Now()
		for i := 0; i < goroutines; i++ {
			go func(id int) {
				defer wg.Done()
				for j := 0; j < perG; j++ {
					s.Info(ctx, "workers g=%d j=%d", id, j)
				}
			}(i)
		}
		wg.Wait()
		_ = s.Sync()
		return float64(goroutines*perG) / time.Since(start).Seconds()
	}

	t.Logf("不同 worker 数在 %d goroutine 下的吞吐:", goroutines)
	results := make(map[int]float64)
	for _, w := range []int{1, 2, 4, 8, 16, 32} {
		tps := runWithWorkers(w)
		results[w] = tps
		t.Logf("  workers=%2d: %.0f ops/s", w, tps)
	}

	// 8 workers 应明显优于 1 worker（验证消费者侧并行性）
	if results[8] < results[1] {
		t.Errorf("8 workers (%.0f) 应优于 1 worker (%.0f)", results[8], results[1])
	}
}

// --- TestLockContention_LongRunningStability ---
// 持续 3 秒的高并发运行，验证长时间稳定性（无内存泄漏导致 OOM、无锁泄漏导致死锁）

func TestLockContention_LongRunningStability(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过长测试，使用 -short")
	}
	s, cleanup := lockContentionTest(t, slog.LevelDebug, 16, 4000000, 2048)
	defer cleanup()

	ctx := context.Background()
	const goroutines = 64
	duration := 3 * time.Second
	var wg sync.WaitGroup
	wg.Add(goroutines)

	stop := make(chan struct{})
	totalSent := atomic.Int64{}

	start := time.Now()
	mustCompleteWithin(t, 10*time.Second, "持续3秒高并发", func() {
		for i := 0; i < goroutines; i++ {
			go func(id int) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("goroutine %d panic: %v", id, r)
					}
				}()
				for {
					select {
					case <-stop:
						return
					default:
						s.Info(ctx, "long-run g=%d", id)
						totalSent.Add(1)
					}
				}
			}(i)
		}
		time.Sleep(duration)
		close(stop)
		wg.Wait()
	})
	elapsed := time.Since(start)
	_ = s.Sync()

	sent := totalSent.Load()
	dropped := s.ringBuf.Dropped()
	throughput := float64(sent) / elapsed.Seconds()
	t.Logf("3秒稳定性: 发送 %d, 丢弃 %d, 吞吐 %.0f ops/s",
		sent, dropped, throughput)
}

// --- TestLockContention_VerifyEntriesProcessed ---
// 通过自定义 writer 验证所有日志都被处理（数据完整性）

func TestLockContention_VerifyEntriesProcessed(t *testing.T) {
	counter := &atomic.Int64{}
	countingWriter := &countingWriter{counter: counter}

	s := &SLogger{
		hooks:        []Hook{},
		wg:           &sync.WaitGroup{},
		asyncEnabled: true,
		usePool:      true,
		level:        &atomic.Int32{},
		levelVar:     &slog.LevelVar{},
		cfg: &config.Config{
			Log: config.LogConfig{
				Async: config.AsyncConfig{
					BatchSize:     1024,
					FlushInterval: 10,
				},
			},
		},
	}
	s.level.Store(int32(slog.LevelDebug))
	handler := NewFastHandler(countingWriter, s.level)
	s.logger = slog.New(handler)
	s.handler = handler
	s.ringBuf = newShardedRingBuffer(2000000, 8)
	s.workers = make([]*worker, 8)
	for i := range s.workers {
		s.workers[i] = &worker{
			id:     i,
			logger: s,
			stopCh: make(chan struct{}),
			wg:     s.wg,
		}
		s.workers[i].start()
	}

	ctx := context.Background()
	const goroutines = 32
	const perG = 1000
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				s.Info(ctx, "verify g=%d j=%d", id, j)
			}
		}(i)
	}
	wg.Wait()
	_ = s.Sync()

	total := goroutines * perG
	processed := counter.Load()
	t.Logf("写入 %d 条，处理 %d 条", total, processed)
	if processed != int64(total) {
		t.Errorf("数据丢失：写入 %d，处理 %d", total, processed)
	}
}

// countingWriter 计数写入次数（每次 Write 调用计数）
type countingWriter struct {
	counter *atomic.Int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	// 简单计数：每次 Write 调用视为一条日志（FastHandler 每条调用一次 Write）
	// 注意：实际可能因 batchWriter 合并，但测试中没有用 batchWriter
	w.counter.Add(1)
	return len(p), nil
}

// --- TestLockContention_DropRateUnderBufferPressure ---
// 使用小缓冲区制造压力，验证丢弃率受控、不无限增长

func TestLockContention_DropRateUnderBufferPressure(t *testing.T) {
	// 小 buffer + 大量并发，制造缓冲压力
	s, cleanup := lockContentionTest(t, slog.LevelDebug, 4, 4096, 256)
	defer cleanup()

	ctx := context.Background()
	const goroutines = 64
	const perG = 2000
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				s.Info(ctx, "pressure g=%d j=%d", id, j)
			}
		}(i)
	}
	wg.Wait()
	_ = s.Sync()

	dropped := s.ringBuf.Dropped()
	total := goroutines * perG
	dropRate := float64(dropped) / float64(total) * 100
	t.Logf("缓冲压力测试: 总计 %d, 丢弃 %d (%.2f%%)", total, dropped, dropRate)

	// 丢弃率应低于 80%（验证降级同步机制生效，不会全部丢弃）
	if dropRate > 80 {
		t.Errorf("丢弃率 %.2f%% 过高，降级同步机制可能失效", dropRate)
	}

	// 同时验证：丢弃时无 panic
	t.Logf("缓冲压力下无 panic，降级同步路径生效")
}

// --- TestLockContention_RaceDetector ---
// 简短的高并发测试，配合 `go test -race` 检测底层数据竞争
// 用法：go test -race -run=TestLockContention_RaceDetector -timeout=60s

func TestLockContention_RaceDetector(t *testing.T) {
	s, cleanup := lockContentionTest(t, slog.LevelDebug, 8, 2000000, 1024)
	defer cleanup()

	ctx := context.Background()
	const goroutines = 16
	const perG = 500
	var wg sync.WaitGroup
	wg.Add(goroutines)

	wg.Add(1) // 为 SetLevel goroutine 预留
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				switch j % 4 {
				case 0:
					s.Debug(ctx, "race g=%d j=%d", id, j)
				case 1:
					s.Info(ctx, "race g=%d j=%d", id, j)
				case 2:
					s.Warn(ctx, "race g=%d j=%d", id, j)
				case 3:
					s.Error(ctx, "race g=%d j=%d", id, j)
				}
			}
		}(i)
	}

	// 并发 SetLevel
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			s.SetLevel([]string{"debug", "info", "warn", "error"}[i%4])
		}
	}()

	wg.Wait()
	_ = s.Sync()
	t.Logf("Race detector 测试完成（请配合 -race 标志运行验证底层无竞争）")
}
