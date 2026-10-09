package aws

import (
	"errors"
	"strings"

	"github.com/aws/smithy-go"
)

// IsThrottlingError 检查是否是 AWS 限流错误
func IsThrottlingError(err error) bool {
	if err == nil {
		return false
	}

	// 检查 AWS SDK 的 API 错误
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		return code == "Throttling" ||
			code == "ThrottlingException" ||
			code == "TooManyRequestsException" ||
			code == "RequestLimitExceeded"
	}

	// 后备方案：字符串匹配
	errMsg := err.Error()
	return strings.Contains(errMsg, "Throttling") ||
		strings.Contains(errMsg, "TooManyRequests") ||
		strings.Contains(errMsg, "RequestLimitExceeded")
}

// IsNotFoundError 检查是否是资源不存在错误
func IsNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		return code == "NoSuchEntity" ||
			code == "ResourceNotFoundException" ||
			code == "NotFound"
	}

	errMsg := err.Error()
	return strings.Contains(errMsg, "NoSuchEntity") ||
		strings.Contains(errMsg, "NotFound")
}

// IsPermissionError 检查是否是权限错误
func IsPermissionError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		return code == "AccessDenied" ||
			code == "UnauthorizedOperation" ||
			code == "Forbidden"
	}

	errMsg := err.Error()
	return strings.Contains(errMsg, "AccessDenied") ||
		strings.Contains(errMsg, "Forbidden")
}
