package volcano

import (
	"strings"
)

// IsThrottlingError 检查是否是限流错误
func IsThrottlingError(err error) bool {
	if err == nil {
		return false
	}

	// TODO: 实现火山云限流错误检测
	// 需要根据火山云 SDK 的错误类型判断
	// 示例:
	// if volcErr, ok := err.(*volcengine.Error); ok {
	//     return volcErr.Code == "Throttling" || volcErr.Code == "RequestLimitExceeded"
	// }

	// 通用错误消息检测
	errMsg := err.Error()
	return strings.Contains(errMsg, "throttling") ||
		strings.Contains(errMsg, "rate limit") ||
		strings.Contains(errMsg, "too many requests") ||
		strings.Contains(errMsg, "请求过于频繁")
}

// IsNotFoundError 检查是否是资源不存在错误
func IsNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	// TODO: 实现火山云资源不存在错误检测
	errMsg := err.Error()
	return strings.Contains(errMsg, "not found") ||
		strings.Contains(errMsg, "does not exist") ||
		strings.Contains(errMsg, "不存在")
}

// IsConflictError 检查是否是冲突错误（资源已存在）
func IsConflictError(err error) bool {
	if err == nil {
		return false
	}

	// TODO: 实现火山云冲突错误检测
	errMsg := err.Error()
	return strings.Contains(errMsg, "already exists") ||
		strings.Contains(errMsg, "duplicate") ||
		strings.Contains(errMsg, "conflict") ||
		strings.Contains(errMsg, "已存在")
}
