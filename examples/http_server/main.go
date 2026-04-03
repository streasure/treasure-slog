package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	logger "github.com/streasure/treasure-slog"
)

func main() {
	fmt.Println("=== Treasure-Slog HTTP 服务器示例 ===")

	// 直接使用全局函数
	defer logger.Sync()

	// 注册路由
	http.HandleFunc("/", handleRoot)
	http.HandleFunc("/api", handleAPI)
	http.HandleFunc("/error", handleError)

	// 启动服务器
	addr := ":8080"
	fmt.Printf("服务器启动在 http://localhost%s\n", addr)
	fmt.Println("按 Ctrl+C 停止服务器")

	// 启动 HTTP 服务器
	if err := http.ListenAndServe(addr, nil); err != nil && err != http.ErrServerClosed {
		logger.Error("服务器启动失败", "error", err)
	}

	fmt.Println("服务器已停止")
}

// handleRoot 处理根路径请求
func handleRoot(w http.ResponseWriter, r *http.Request) {
	// 创建带上下文的日志记录器
	ctx := r.Context()
	ctx = context.WithValue(ctx, "request_id", generateRequestID())
	ctx = context.WithValue(ctx, "client_ip", r.RemoteAddr)
	ctxLog := logger.WithContext(ctx)

	// 记录请求
	ctxLog.Info("HTTP 请求",
		"method", r.Method,
		"path", r.URL.Path,
		"user_agent", r.UserAgent(),
	)

	// 模拟处理时间
	time.Sleep(10 * time.Millisecond)

	// 返回响应
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Hello, Treasure-Slog!\n")
	fmt.Fprintf(w, "请求 ID: %s\n", ctx.Value("request_id"))

	// 记录响应
	ctxLog.Info("HTTP 响应",
		"status", http.StatusOK,
		"method", r.Method,
		"path", r.URL.Path,
	)
}

// handleAPI 处理 API 请求
func handleAPI(w http.ResponseWriter, r *http.Request) {
	// 创建带上下文的日志记录器
	ctx := r.Context()
	ctx = context.WithValue(ctx, "request_id", generateRequestID())
	ctxLog := logger.WithContext(ctx)

	// 记录请求
	ctxLog.Info("API 请求",
		"method", r.Method,
		"path", r.URL.Path,
	)

	// 模拟 API 处理
	time.Sleep(50 * time.Millisecond)

	// 记录处理信息
	ctxLog.Debug("API 处理中",
		"param1", r.URL.Query().Get("param1"),
		"param2", r.URL.Query().Get("param2"),
	)

	// 返回 JSON 响应
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"success","message":"API 调用成功","request_id":"%s"}`, ctx.Value("request_id"))

	// 记录响应
	ctxLog.Info("API 响应",
		"status", http.StatusOK,
	)
}

// handleError 处理错误请求
func handleError(w http.ResponseWriter, r *http.Request) {
	// 创建带上下文的日志记录器
	ctx := r.Context()
	ctx = context.WithValue(ctx, "request_id", generateRequestID())
	ctxLog := logger.WithContext(ctx)

	// 记录请求
	ctxLog.Info("错误请求",
		"method", r.Method,
		"path", r.URL.Path,
	)

	// 模拟错误
	time.Sleep(20 * time.Millisecond)

	// 记录错误
	err := fmt.Errorf("模拟的服务器错误")
	ctxLog.Error("处理请求时出错",
		"error", err,
		"path", r.URL.Path,
	)

	// 返回错误响应
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(w, `{"status":"error","message":"服务器内部错误","request_id":"%s"}`, ctx.Value("request_id"))
}

// generateRequestID 生成请求 ID
func generateRequestID() string {
	return fmt.Sprintf("req-%d", time.Now().UnixNano())
}
