package router

import "errors"

// 路由相关错误
var (
	ErrNoHealthyNode = errors.New("no healthy node")
	ErrEndpointNotFound = errors.New("endpoint not found")
	ErrRequestFailed = errors.New("request failed")
)
