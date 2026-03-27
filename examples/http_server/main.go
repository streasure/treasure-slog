package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"treasure-slog/pkg/logger"
)

// 全局日志实例
var log logger.Logger

func main() {
	// 初始化日志
	log = logger.GetLogger()
	defer log.Sync()

	log.Info("HTTP 服务器启动", "port", 8080)

	// 设置路由
	http.HandleFunc("/", homeHandler)
	http.HandleFunc("/api/users", usersHandler)
	http.HandleFunc("/api/orders", ordersHandler)

	// 启动服务器
	server := &http.Server{
		Addr:         ":8080",
		Handler:      loggingMiddleware(http.DefaultServeMux),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Info("服务器监听", "address", "http://localhost:8080")
	if err := server.ListenAndServe(); err != nil {
		log.Error("服务器错误", "error", err)
	}
}

// loggingMiddleware 日志中间件
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// 创建带追踪信息的 context
		ctx := r.Context()
		ctx = context.WithValue(ctx, "request_id", generateRequestID())
		ctx = context.WithValue(ctx, "user_id", r.Header.Get("X-User-ID"))
		ctx = context.WithValue(ctx, "trace_id", r.Header.Get("X-Trace-ID"))

		// 创建带 context 的 logger
		requestLog := log.WithContext(ctx)
		requestLog.Info("请求开始",
			"method", r.Method,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
			"user_agent", r.UserAgent(),
		)

		// 包装 ResponseWriter 以获取状态码
		wrapped := &responseWriter{ResponseWriter: w, statusCode: 200}

		// 执行下一个处理器
		next.ServeHTTP(wrapped, r.WithContext(ctx))

		// 记录请求完成
		duration := time.Since(start)
		requestLog.Info("请求完成",
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.statusCode,
			"duration_ms", duration.Milliseconds(),
		)
	})
}

// responseWriter 包装 http.ResponseWriter 以捕获状态码
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// 处理器函数
func homeHandler(w http.ResponseWriter, r *http.Request) {
	log := logger.GetLogger().WithContext(r.Context())
	
	log.Info("首页访问")
	
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"message": "Welcome to Treasure-Slog HTTP Server"}`))
}

func usersHandler(w http.ResponseWriter, r *http.Request) {
	log := logger.GetLogger().WithContext(r.Context())
	
	switch r.Method {
	case http.MethodGet:
		log.Info("获取用户列表")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"users": [{"id": 1, "name": "Alice"}, {"id": 2, "name": "Bob"}]}`))
		
	case http.MethodPost:
		log.Info("创建用户")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id": 3, "name": "New User"}`))
		
	default:
		log.Warn("不支持的 HTTP 方法", "method", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func ordersHandler(w http.ResponseWriter, r *http.Request) {
	log := logger.GetLogger().WithContext(r.Context())
	
	// 模拟业务逻辑
	orderID := r.URL.Query().Get("id")
	if orderID == "" {
		log.Error("缺少订单 ID")
		http.Error(w, "Missing order ID", http.StatusBadRequest)
		return
	}
	
	log.Info("查询订单", "order_id", orderID)
	
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"order_id": "%s", "status": "completed", "amount": 99.99}`, orderID)
}

// generateRequestID 生成请求 ID
func generateRequestID() string {
	return fmt.Sprintf("req-%d", time.Now().UnixNano())
}
