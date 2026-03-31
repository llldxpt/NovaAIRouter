package utils

import (
	"time"
)

// FormatTime 格式化时间为标准格式
func FormatTime(t time.Time) string {
	return t.Format(time.RFC3339)
}

// ParseTime 解析标准格式的时间字符串
func ParseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}

// IsExpired 检查时间是否已过期
func IsExpired(t time.Time, timeout time.Duration) bool {
	return time.Since(t) > timeout
}

// GetTimestamp 获取当前时间戳（秒）
func GetTimestamp() int64 {
	return time.Now().Unix()
}

// GetTimestampMs 获取当前时间戳（毫秒）
func GetTimestampMs() int64 {
	return time.Now().UnixMilli()
}
