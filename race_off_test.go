//go:build !race

package logger

// raceEnabled 标识测试二进制未启用 -race
const raceEnabled = false
