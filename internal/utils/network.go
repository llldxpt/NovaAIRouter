package utils

import (
	"fmt"
	"net"
	"strings"
)

// DefaultBusinessPort 默认业务端口
const DefaultBusinessPort = 15050

// GetHost 从地址中提取主机名
func GetHost(addr string) string {
	if idx := strings.LastIndex(addr, ":"); idx > 0 {
		host := addr[:idx]
		if host == "" || host == "0.0.0.0" {
			return "127.0.0.1"
		}
		return host
	}
	return "127.0.0.1"
}

// ExtractPort 从地址中提取端口号，如果失败返回默认端口
func ExtractPort(addr string) int {
	if idx := strings.LastIndex(addr, ":"); idx >= 0 && idx < len(addr)-1 {
		var port int
		if n, err := fmt.Sscanf(addr[idx+1:], "%d", &port); err == nil && n == 1 {
			if port > 0 && port <= 65535 {
				return port
			}
		}
	}
	return DefaultBusinessPort
}

// GetPort 从地址中提取端口号（保留原有函数以兼容，调用 ExtractPort）
func GetPort(addr string) int {
	return ExtractPort(addr)
}

// GetLocalIP 获取本地IP地址
func GetLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return "127.0.0.1"
}

// ValidateAddress 验证地址格式是否正确
func ValidateAddress(addr string) bool {
	if addr == "" {
		return false
	}
	if !strings.Contains(addr, ":") {
		return false
	}
	host := GetHost(addr)
	port := GetPort(addr)
	return host != "" && port > 0
}
