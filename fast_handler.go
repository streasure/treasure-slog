package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"
)

// FastHandler 高性能JSON日志处理器，零反射序列化
// 使用 *atomic.Int32 持有 level 引用，支持动态级别变更
type FastHandler struct {
	w           io.Writer
	levelRef    *atomic.Int32 // 指向 SLogger.level，动态感知级别变化
	preAttrs    []slog.Attr
	groups      []string
	groupPrefix string // 缓存：避免每次 Handle() 重建
}

func NewFastHandler(w io.Writer, levelRef *atomic.Int32) *FastHandler {
	if levelRef == nil {
		v := atomic.Int32{}
		v.Store(int32(slog.LevelInfo))
		levelRef = &v
	}
	return &FastHandler{w: w, levelRef: levelRef}
}

func (h *FastHandler) Enabled(_ context.Context, level slog.Level) bool {
	currentLevel := slog.Level(h.levelRef.Load())
	if currentLevel == 0 {
		currentLevel = slog.LevelInfo
	}
	return level >= currentLevel
}

func (h *FastHandler) Handle(_ context.Context, r slog.Record) error {
	if h.w == nil {
		return nil
	}

	// 全局recover保护：确保任何序列化或写入panic都不会传播
	var handleErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				handleErr = fmt.Errorf("[treasure-slog] FastHandler.Handle panic: %v", r)
			}
		}()

		endHandle := timing.startTimer(&timing.handlerHandleNS)
		defer endHandle()

		// 安全获取buffer
		var bp *[]byte
		if v := fastBufPool.Get(); v != nil {
			if p, ok := v.(*[]byte); ok {
				bp = p
			}
		}
		if bp == nil {
			tmp := make([]byte, 0, 512)
			bp = &tmp
		}
		buf := (*bp)[:0]
		defer func() {
			*bp = buf
			fastBufPool.Put(bp)
		}()

		buf = append(buf, `{"time":"`...)
		buf = r.Time.AppendFormat(buf, time.RFC3339Nano)
		buf = append(buf, `","level":"`...)
		buf = appendLevel(buf, r.Level)
		buf = append(buf, `","msg":`...)
		buf = appendJSONString(buf, r.Message)

		for _, a := range h.preAttrs {
			if a.Key == "" {
				continue
			}
			buf = append(buf, `,"`...)
			buf = append(buf, h.groupPrefix...)
			buf = append(buf, a.Key...)
			buf = append(buf, `":`...)
			buf = appendAttrValue(buf, a.Value)
		}

		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "" {
				return true
			}
			buf = append(buf, `,"`...)
			buf = append(buf, h.groupPrefix...)
			buf = append(buf, a.Key...)
			buf = append(buf, `":`...)
			buf = appendAttrValue(buf, a.Value)
			return true
		})

		buf = append(buf, '}', '\n')

		if _, err := h.w.Write(buf); err != nil {
			handleErr = err
		}
	}()
	return handleErr
}

func (h *FastHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	var existingAttrs []slog.Attr
	if len(h.groups) > 0 && len(h.preAttrs) > 0 {
		prefix := h.groupPrefix
		existingAttrs = make([]slog.Attr, len(h.preAttrs))
		for i, a := range h.preAttrs {
			existingAttrs[i] = slog.Attr{Key: prefix + a.Key, Value: a.Value}
		}
	} else {
		existingAttrs = h.preAttrs
	}
	newAttrs := make([]slog.Attr, len(existingAttrs)+len(attrs))
	copy(newAttrs, existingAttrs)
	copy(newAttrs[len(existingAttrs):], attrs)
	return &FastHandler{w: h.w, levelRef: h.levelRef, preAttrs: newAttrs, groups: h.groups, groupPrefix: h.groupPrefix}
}

func (h *FastHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	newGroups := make([]string, len(h.groups)+1)
	copy(newGroups, h.groups)
	newGroups[len(h.groups)] = name
	return &FastHandler{w: h.w, levelRef: h.levelRef, preAttrs: h.preAttrs, groups: newGroups, groupPrefix: buildGroupPrefix(newGroups)}
}

// --- 序列化辅助函数 ---

// buildGroupPrefix builds a dot-separated prefix from group names.
// Returns empty string if no groups, e.g. "grpc." or "grpc.server."
func buildGroupPrefix(groups []string) string {
	if len(groups) == 0 {
		return ""
	}
	n := 0
	for _, g := range groups {
		n += len(g) + 1 // +1 for dot separator
	}
	b := make([]byte, 0, n)
	for _, g := range groups {
		b = append(b, g...)
		b = append(b, '.')
	}
	return string(b)
}

func appendLevel(buf []byte, level slog.Level) []byte {
	switch {
	case level <= slog.LevelDebug:
		buf = append(buf, "DEBUG"...)
	case level <= slog.LevelInfo:
		buf = append(buf, "INFO"...)
	case level <= slog.LevelWarn:
		buf = append(buf, "WARN"...)
	case level <= slog.LevelError:
		buf = append(buf, "ERROR"...)
	default:
		buf = append(buf, "ERROR+"...)
		buf = appendInt(buf, int64(level-slog.LevelError))
	}
	return buf
}

func appendAttrValue(buf []byte, v slog.Value) []byte {
	switch v.Kind() {
	case slog.KindString:
		return appendJSONString(buf, v.String())
	case slog.KindInt64:
		return appendInt(buf, v.Int64())
	case slog.KindUint64:
		return appendUint(buf, v.Uint64())
	case slog.KindFloat64:
		return appendFloat(buf, v.Float64())
	case slog.KindBool:
		if v.Bool() {
			return append(buf, "true"...)
		}
		return append(buf, "false"...)
	case slog.KindDuration:
		return appendJSONString(buf, v.Duration().String())
	case slog.KindTime:
		return appendJSONString(buf, v.Time().Format(time.RFC3339Nano))
	case slog.KindAny:
		return appendJSONString(buf, fmt.Sprintf("%v", v.Any()))
	default:
		return appendJSONString(buf, v.String())
	}
}

func appendJSONString(buf []byte, s string) []byte {
	buf = append(buf, '"')
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			buf = append(buf, '\\', '"')
		case '\\':
			buf = append(buf, '\\', '\\')
		case '\n':
			buf = append(buf, '\\', 'n')
		case '\r':
			buf = append(buf, '\\', 'r')
		case '\t':
			buf = append(buf, '\\', 't')
		default:
			if s[i] < 0x20 {
				buf = append(buf, "\\u00"...)
				buf = appendHex(buf, s[i])
			} else {
				buf = append(buf, s[i])
			}
		}
	}
	return append(buf, '"')
}

func appendHex(buf []byte, b byte) []byte {
	hi, lo := b>>4, b&0x0f
	if hi < 10 {
		buf = append(buf, hi+'0')
	} else {
		buf = append(buf, hi-10+'a')
	}
	if lo < 10 {
		buf = append(buf, lo+'0')
	} else {
		buf = append(buf, lo-10+'a')
	}
	return buf
}

func appendInt(buf []byte, v int64) []byte {
	if v < 0 {
		buf = append(buf, '-')
		v = -v
	}
	if v == 0 {
		return append(buf, '0')
	}
	var tmp [20]byte
	pos := len(tmp)
	for v > 0 {
		pos--
		tmp[pos] = byte(v%10) + '0'
		v /= 10
	}
	return append(buf, tmp[pos:]...)
}

func appendUint(buf []byte, v uint64) []byte {
	if v == 0 {
		return append(buf, '0')
	}
	var tmp [20]byte
	pos := len(tmp)
	for v > 0 {
		pos--
		tmp[pos] = byte(v%10) + '0'
		v /= 10
	}
	return append(buf, tmp[pos:]...)
}

func appendFloat(buf []byte, f float64) []byte {
	return append(buf, strconv.FormatFloat(f, 'g', -1, 64)...)
}

var _ slog.Handler = (*FastHandler)(nil)
