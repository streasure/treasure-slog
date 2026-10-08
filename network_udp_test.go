package logger

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/streasure/treasure-slog/internal/config"
)

// TestNetworkWriter_UDPLineFraming 验证 UDP 按行分帧：
// 聚合缓冲一次 Write 包含多条记录时，必须拆成逐条数据报发送
// （单数据报上限 65507 字节，且接收端按行分帧）
func TestNetworkWriter_UDPLineFraming(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket: %v", err)
	}
	defer pc.Close()

	nw, err := newNetworkWriter(config.NetworkConfig{
		Type:    "udp",
		Address: pc.LocalAddr().String(),
		Timeout: 2,
		Retry:   1,
	})
	if err != nil {
		t.Fatalf("newNetworkWriter: %v", err)
	}
	defer nw.Close()

	const records = 10
	payload := strings.Repeat(`{"msg":"hello"}`+"\n", records)
	n, err := nw.Write([]byte(payload))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(payload) {
		t.Fatalf("Write n = %d, want %d", n, len(payload))
	}

	pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 65535)
	got := 0
	for {
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			break // 超时即认为数据报读完
		}
		got++
		dgram := string(buf[:n])
		if !strings.HasSuffix(dgram, "\n") {
			t.Fatalf("datagram %d does not end with newline: %q", got, dgram)
		}
		if c := strings.Count(dgram, "\n"); c != 1 {
			t.Fatalf("datagram %d contains %d records, want 1: %q", got, c, dgram)
		}
		if got == records {
			break
		}
	}
	if got != records {
		t.Fatalf("received %d datagrams, want %d", got, records)
	}
}
