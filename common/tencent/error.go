package tencent

import (
	"strings"

	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
)

// IsThrottlingError 检查是否是限流错误
func IsThrottlingError(err error) bool {
	if err == nil {
		return false
	}

	// 检查是否是腾讯云 SDK 错误
	if sdkErr, ok := err.(*errors.TencentCloudSDKError); ok {
		// 腾讯云限流错误码
		errorCode := sdkErr.Code
		if errorCode == "RequestLimitExceeded" || // 请求频率超限
			errorCode == "ResourceInsufficient.RequestLimitExceeded" || // 资源不足，请求频率超限
			strings.Contains(errorCode, "Throttling") ||
			strings.Contains(errorCode, "RateLimit") {
			return true
		}
	}

	// 检查错误消息
	errMsg := err.Error()
	return strings.Contains(errMsg, "throttling") ||
		strings.Contains(errMsg, "rate limit") ||
		strings.Contains(errMsg, "request limit exceeded") ||
		strings.Contains(errMsg, "too many requests")
}

// IsNotFoundError 检查是否是资源不存在错误
func IsNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	if sdkErr, ok := err.(*errors.TencentCloudSDKError); ok {
		errorCode := sdkErr.Code
		if errorCode == "ResourceNotFound" ||
			strings.Contains(errorCode, "NotFound") ||
			strings.Contains(errorCode, "NotExist") {
			return true
		}
	}

	errMsg := err.Error()
	return strings.Contains(errMsg, "not found") ||
		strings.Contains(errMsg, "does not exist") ||
		strings.Contains(errMsg, "not exist")
}

// IsConflictError 检查是否是冲突错误（资源已存在）
func IsConflictError(err error) bool {
	if err == nil {
		return false
	}

	if sdkErr, ok := err.(*errors.TencentCloudSDKError); ok {
		errorCode := sdkErr.Code
		if errorCode == "ResourceInUse" ||
			strings.Contains(errorCode, "AlreadyExists") ||
			strings.Contains(errorCode, "Duplicate") {
			return true
		}
	}

	errMsg := err.Error()
	return strings.Contains(errMsg, "already exists") ||
		strings.Contains(errMsg, "duplicate") ||
		strings.Contains(errMsg, "conflict")
}
