package aliyun

import "strings"

// IsThrottlingError 检查是否是阿里云限流错误
func IsThrottlingError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	// 阿里云限流错误码
	return strings.Contains(errMsg, "Throttling") ||
		strings.Contains(errMsg, "QpsLimitExceeded") ||
		strings.Contains(errMsg, "FlowControl")
}

// IsNotFoundError 检查是否是资源不存在错误
func IsNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	return strings.Contains(errMsg, "EntityNotExist") ||
		strings.Contains(errMsg, "NotFound")
}

// IsPermissionError 检查是否是权限错误
func IsPermissionError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	return strings.Contains(errMsg, "Forbidden") ||
		strings.Contains(errMsg, "NoPermission") ||
		strings.Contains(errMsg, "AccessDenied")
}
