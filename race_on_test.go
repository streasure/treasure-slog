//go:build race

package logger

// raceEnabled 标识测试二进制在 -race 下构建（检测器带来约 5~10 倍减速）
const raceEnabled = true
