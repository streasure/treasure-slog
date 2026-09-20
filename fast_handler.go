package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// fastBufPool 复用 []byte，减少 GC 压力
var fastBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 0, 512)
		return &buf
	},
}

// FastHandler 高性能JSON日志处理器，零反射序列化
// 使用 *atomic.Int32 持有 level 引用，支持动态级别变化
type FastHandler struct {
	w           io.Writer
	levelRef    *atomic.Int32 // 指向 SLogger.level，动态感知级别变化
	preAttrs    []preAttr     // WithAttrs/WithGroup 时预存的属性，每个属性独立记录添加时的 group 前缀
	groups      []string
	groupPrefix string // 缓存：避免每次 Handle() 重建
}

// preAttr 记录一个预存属性及其添加时的 group 前缀
type preAttr struct {
	attr   slog.Attr
	prefix string // 添加时的 groupPrefix
}

// NewFastHandler 创建 FastHandler
// levelRef 指向 SLogger.level，动态感知级别变化
func NewFastHandler(w io.Writer, levelRef *atomic.Int32) *FastHandler {
	h := &FastHandler{
		w:        w,
		levelRef: levelRef,
		preAttrs: make([]preAttr, 0, 8),
		groups:   make([]string, 0, 4),
	}
	return h
}

// Enabled 实现 slog.Handler 接口（预检：atomic load 避免无效格式化）
func (h *FastHandler) Enabled(_ context.Context, level slog.Level) bool {
	if h.levelRef == nil {
		return true
	}
	return int32(level) >= h.levelRef.Load()
}

// Handle 实现 slog.Handler 接口（核心：零反射 JSON 序列化）
func (h *FastHandler) Handle(_ context.Context, r slog.Record) error {
	if h.w == nil {
		return nil
	}

	bp := fastBufPool.Get().(*[]byte)
	buf := (*bp)[:0]
	defer func() {
		*bp = buf
		fastBufPool.Put(bp)
	}()

	buf = append(buf, `{"time":"`...)
	if !r.Time.IsZero() {
		buf = r.Time.AppendFormat(buf, time.RFC3339Nano)
	}
	buf = append(buf, `","level":"`...)
	buf = appendLevel(buf, r.Level)
	buf = append(buf, `","msg":"`...)
	buf = appendJSONString(buf, r.Message)
	buf = append(buf, '"')

	for _, pa := range h.preAttrs {
		if pa.attr.Key == "" {
			continue
		}
		buf = append(buf, `,"`...)
		if pa.prefix != "" {
			buf = appendJSONString(buf, pa.prefix)
		}
		buf = appendJSONString(buf, pa.attr.Key)
		buf = append(buf, `":`...)
		buf = appendAttrValue(buf, pa.attr.Value)
	}

	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "" {
			return true
		}
		buf = append(buf, `,"`...)
		if h.groupPrefix != "" {
			buf = appendJSONString(buf, h.groupPrefix)
		}
		buf = appendJSONString(buf, a.Key)
		buf = append(buf, `":`...)
		buf = appendAttrValue(buf, a.Value)
		return true
	})

	buf = append(buf, '}', '\n')

	var writeErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				writeErr = fmt.Errorf("[treasure-slog] FastHandler write panic: %v", r)
			}
		}()
		_, writeErr = h.w.Write(buf)
	}()
	return writeErr
}

// WithAttrs 实现 slog.Handler 接口
func (h *FastHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	newHandler := *h
	newHandler.preAttrs = make([]preAttr, len(h.preAttrs)+len(attrs))
	copy(newHandler.preAttrs, h.preAttrs)
	for i, a := range attrs {
		newHandler.preAttrs[len(h.preAttrs)+i] = preAttr{attr: a, prefix: h.groupPrefix}
	}
	return &newHandler
}

// WithGroup 实现 slog.Handler 接口
func (h *FastHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	newHandler := *h
	newHandler.groups = make([]string, len(h.groups)+1)
	copy(newHandler.groups, h.groups)
	newHandler.groups = append(newHandler.groups, name)
	newHandler.groupPrefix = joinGroups(newHandler.groups) + "."
	return &newHandler
}

func appendLevel(buf []byte, level slog.Level) []byte {
	switch {
	case level >= slog.LevelError:
		buf = append(buf, "ERROR"...)
	case level >= slog.LevelWarn:
		buf = append(buf, "WARN"...)
	case level >= slog.LevelInfo:
		buf = append(buf, "INFO"...)
	default:
		buf = append(buf, "DEBUG"...)
	}
	return buf
}

func appendAttrValue(buf []byte, v slog.Value) []byte {
	switch v.Kind() {
	case slog.KindString:
		buf = append(buf, '"')
		buf = appendJSONString(buf, v.String())
		buf = append(buf, '"')
	case slog.KindBool:
		if v.Bool() {
			buf = append(buf, "true"...)
		} else {
			buf = append(buf, "false"...)
		}
	case slog.KindInt64:
		buf = strconv.AppendInt(buf, v.Int64(), 10)
	case slog.KindUint64:
		buf = strconv.AppendUint(buf, v.Uint64(), 10)
	case slog.KindFloat64:
		buf = strconv.AppendFloat(buf, v.Float64(), 'f', -1, 64)
	case slog.KindTime:
		buf = append(buf, '"')
		buf = v.Time().AppendFormat(buf, time.RFC3339Nano)
		buf = append(buf, '"')
	case slog.KindDuration:
		buf = append(buf, '"')
		buf = append(buf, v.Duration().String()...)
		buf = append(buf, '"')
	case slog.KindAny:
		if obj, ok := v.Any().(interface{ MarshalJSON() ([]byte, error) }); ok {
			if data, err := obj.MarshalJSON(); err == nil {
				buf = append(buf, data...)
			} else {
				buf = append(buf, `"unsupported"`...)
			}
		} else {
			buf = append(buf, '"')
			buf = appendJSONString(buf, fmt.Sprintf("%v", v.Any()))
			buf = append(buf, '"')
		}
	case slog.KindGroup:
		groupAttrs := v.Group()
		if len(groupAttrs) > 0 {
			buf = append(buf, '{')
			for i, ga := range groupAttrs {
				if i > 0 {
					buf = append(buf, ',')
				}
				buf = append(buf, `,"`...)
				buf = appendJSONString(buf, ga.Key)
				buf = append(buf, `":`...)
				buf = appendAttrValue(buf, ga.Value)
			}
			buf = append(buf, '}')
		}
	case slog.KindLogValuer:
		buf = append(buf, '"')
		buf = appendJSONString(buf, fmt.Sprintf("%v", v.Resolve().Any()))
		buf = append(buf, '"')
	default:
		buf = append(buf, '"')
		buf = appendJSONString(buf, v.String())
		buf = append(buf, '"')
	}
	return buf
}

// appendJSONString 将字符串转义为 JSON 字符串并追加到 buf
func appendJSONString(buf []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			buf = append(buf, `\"`...)
		case '\\':
			buf = append(buf, `\\`...)
		case '\n':
			buf = append(buf, `\n`...)
		case '\r':
			buf = append(buf, `\r`...)
		case '\t':
			buf = append(buf, `\t`...)
		default:
			if c < 0x20 {
				buf = append(buf, `\u`...)
				buf = appendHexUint16(buf, uint16(c))
			} else {
				buf = append(buf, c)
			}
		}
	}
	return buf
}

// appendHexUint16 追加 16 位无符号整数的十六进制表示
func appendHexUint16(buf []byte, v uint16) []byte {
	const hexDigits = "0123456789abcdef"
	buf = append(buf, hexDigits[v>>12])
	buf = append(buf, hexDigits[(v>>8)&0x0f])
	buf = append(buf, hexDigits[(v>>4)&0x0f])
	buf = append(buf, hexDigits[v&0x0f])
	return buf
}

// joinGroups 连接 group 前缀
func joinGroups(groups []string) string {
	result := ""
	for _, g := range groups {
		if result != "" {
			result += "."
		}
		result += g
	}
	return result
}
