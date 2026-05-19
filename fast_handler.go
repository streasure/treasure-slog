package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

type FastHandler struct {
	w        io.Writer
	level    slog.Leveler
	preAttrs []slog.Attr
	groups   []string
}

var fastBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 0, 512)
		return &buf
	},
}

func NewFastHandler(w io.Writer, level slog.Leveler) *FastHandler {
	if level == nil {
		level = slog.LevelInfo
	}
	return &FastHandler{
		w:     w,
		level: level,
	}
}

func (h *FastHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

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

	buf = append(buf, '{')

	buf = append(buf, `"time":"`...)
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
		buf = append(buf, a.Key...)
		buf = append(buf, `":`...)
		buf = appendAttrValue(buf, a.Value)
	}

	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "" {
			return true
		}
		buf = append(buf, `,"`...)
		buf = append(buf, a.Key...)
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

func (h *FastHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	newAttrs := make([]slog.Attr, len(h.preAttrs)+len(attrs))
	copy(newAttrs, h.preAttrs)
	copy(newAttrs[len(h.preAttrs):], attrs)
	return &FastHandler{
		w:        h.w,
		level:    h.level,
		preAttrs: newAttrs,
		groups:   h.groups,
	}
}

func (h *FastHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	newGroups := make([]string, len(h.groups)+1)
	copy(newGroups, h.groups)
	newGroups[len(h.groups)] = name
	return &FastHandler{
		w:        h.w,
		level:    h.level,
		preAttrs: h.preAttrs,
		groups:   newGroups,
	}
}

func appendLevel(buf []byte, level slog.Level) []byte {
	switch {
	case level < slog.LevelDebug:
		buf = append(buf, "DEBUG-1"...)
	case level == slog.LevelDebug:
		buf = append(buf, "DEBUG"...)
	case level < slog.LevelInfo:
		buf = append(buf, "INFO-1"...)
	case level == slog.LevelInfo:
		buf = append(buf, "INFO"...)
	case level < slog.LevelWarn:
		buf = append(buf, "WARN-1"...)
	case level == slog.LevelWarn:
		buf = append(buf, "WARN"...)
	case level < slog.LevelError:
		buf = append(buf, "ERROR-1"...)
	case level == slog.LevelError:
		buf = append(buf, "ERROR"...)
	default:
		buf = append(buf, "ERROR+"...)
		l := level - slog.LevelError
		buf = appendInt(buf, int64(l))
	}
	return buf
}

func appendAttrValue(buf []byte, v slog.Value) []byte {
	switch v.Kind() {
	case slog.KindString:
		return appendJSONString(buf, v.String())
	case slog.KindInt64:
		buf = appendInt(buf, v.Int64())
		return buf
	case slog.KindUint64:
		buf = appendUint(buf, v.Uint64())
		return buf
	case slog.KindFloat64:
		buf = appendFloat(buf, v.Float64(), 64)
		return buf
	case slog.KindBool:
		if v.Bool() {
			buf = append(buf, `true`...)
		} else {
			buf = append(buf, `false`...)
		}
		return buf
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
		c := s[i]
		switch c {
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
			if c < 0x20 {
				buf = append(buf, "\\u00"...)
				buf = appendHex(buf, c)
			} else {
				buf = append(buf, c)
			}
		}
	}
	buf = append(buf, '"')
	return buf
}

func appendHex(buf []byte, b byte) []byte {
	hi := b >> 4
	lo := b & 0x0f
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
	buf = append(buf, tmp[pos:]...)
	return buf
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
	buf = append(buf, tmp[pos:]...)
	return buf
}

func appendFloat(buf []byte, f float64, bits int) []byte {
	var tmp [64]byte
	n := formatFloat(tmp[:], f, bits)
	buf = append(buf, tmp[:n]...)
	return buf
}

func formatFloat(dst []byte, f float64, bits int) int {
	abs := f
	neg := false
	if f < 0 {
		neg = true
		abs = -f
	}

	exp := 0
	if abs >= 1e15 {
		for abs >= 10 {
			abs /= 10
			exp++
		}
	} else if abs > 0 && abs < 1e-6 {
		for abs < 1 {
			abs *= 10
			exp--
		}
	}

	pos := 0
	if neg {
		dst[pos] = '-'
		pos++
	}

	intPart := int64(abs)
	fracPart := abs - float64(intPart)

	pos += formatInt64(dst[pos:], intPart)

	if fracPart > 1e-10 {
		dst[pos] = '.'
		pos++
		for fracPart > 1e-10 && pos < len(dst)-1 {
			fracPart *= 10
			digit := int(fracPart)
			dst[pos] = byte(digit) + '0'
			pos++
			fracPart -= float64(digit)
		}
	}

	if exp != 0 {
		dst[pos] = 'e'
		pos++
		if exp > 0 {
			dst[pos] = '+'
		} else {
			dst[pos] = '-'
			exp = -exp
		}
		pos++
		pos += formatInt64(dst[pos:], int64(exp))
	}

	return pos
}

func formatInt64(dst []byte, v int64) int {
	if v == 0 {
		dst[0] = '0'
		return 1
	}
	pos := 0
	var tmp [20]byte
	n := 0
	for v > 0 {
		tmp[n] = byte(v%10) + '0'
		v /= 10
		n++
	}
	for i := n - 1; i >= 0; i-- {
		dst[pos] = tmp[i]
		pos++
	}
	return pos
}

var _ slog.Handler = (*FastHandler)(nil)
