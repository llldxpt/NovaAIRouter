package utils

import (
	"strconv"
	"strings"
)

// SplitAndTrim 分割字符串并去除空格
func SplitAndTrim(s, sep string) []string {
	var result []string
	for _, part := range strings.Split(s, sep) {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// IsEmpty 检查字符串是否为空或只包含空格
func IsEmpty(s string) bool {
	return strings.TrimSpace(s) == ""
}

// SanitizeString 清理字符串，去除特殊字符
func SanitizeString(s string) string {
	return strings.TrimSpace(s)
}

// ExtractPortFromServicePath 从 service_path 中提取端口号
// service_path 格式: "18001/v1/chat" -> 18001
func ExtractPortFromServicePath(servicePath string) int {
	idx := strings.Index(servicePath, "/")
	var portStr string
	if idx == -1 {
		portStr = servicePath
	} else {
		portStr = servicePath[:idx]
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0
	}
	return port
}

// ExtractBaseFromServicePath 从 service_path 中提取基础路径
// service_path 格式: "18001/v1/chat" -> "/v1/chat"
func ExtractBaseFromServicePath(servicePath string) string {
	idx := strings.Index(servicePath, "/")
	if idx == -1 {
		return ""
	}
	return servicePath[idx:]
}
