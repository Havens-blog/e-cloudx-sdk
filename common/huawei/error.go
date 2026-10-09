package huawei

import (
	"strings"

	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/sdkerr"
)

// IsThrottlingError 检查是否是限流错误
func IsThrottlingError(err error) bool {
	if err == nil {
		return false
	}

	// 检查是否是华为云 SDK 错误
	if sdkErr, ok := err.(*sdkerr.ServiceResponseError); ok {
		// 华为云限流错误码
		errorCode := sdkErr.ErrorCode
		if errorCode == "IAM.0101" || // 请求过于频繁
			errorCode == "IAM.0102" || // 超过配额限制
			strings.Contains(errorCode, "Throttling") ||
			strings.Contains(errorCode, "RateLimit") ||
			strings.Contains(errorCode, "TooManyRequests") {
			return true
		}
	}

	// 检查错误消息
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

	if sdkErr, ok := err.(*sdkerr.ServiceResponseError); ok {
		errorCode := sdkErr.ErrorCode
		// 华为云资源不存在错误码
		if errorCode == "IAM.0003" || // 用户不存在
			errorCode == "IAM.0004" || // 用户组不存在
			errorCode == "IAM.0005" || // 策略不存在
			strings.Contains(errorCode, "NotFound") ||
			strings.Contains(errorCode, "NotExist") {
			return true
		}
	}

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

	if sdkErr, ok := err.(*sdkerr.ServiceResponseError); ok {
		errorCode := sdkErr.ErrorCode
		// 华为云资源已存在错误码
		if errorCode == "IAM.0002" || // 用户已存在
			strings.Contains(errorCode, "AlreadyExists") ||
			strings.Contains(errorCode, "Duplicate") ||
			strings.Contains(errorCode, "Conflict") {
			return true
		}
	}

	errMsg := err.Error()
	return strings.Contains(errMsg, "already exists") ||
		strings.Contains(errMsg, "duplicate") ||
		strings.Contains(errMsg, "conflict") ||
		strings.Contains(errMsg, "已存在")
}
