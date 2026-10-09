package cloudx

import "errors"

var (
	// ErrUnsupportedProvider 不支持的云厂商
	ErrUnsupportedProvider = errors.New("unsupported cloud provider")

	// ErrInvalidCredentials 无效的凭证
	ErrInvalidCredentials = errors.New("invalid credentials")

	// ErrConnectionTimeout 连接超时
	ErrConnectionTimeout = errors.New("connection timeout")

	// ErrPermissionDenied 权限不足
	ErrPermissionDenied = errors.New("permission denied")

	// ErrRegionNotSupported 地域不支持
	ErrRegionNotSupported = errors.New("region not supported")

	// ErrAccountDisabled 云账号已禁用
	ErrAccountDisabled = errors.New("cloud account is disabled")

	// ErrAccountExpired 云账号已过期
	ErrAccountExpired = errors.New("cloud account is expired")

	// ErrInvalidConfig 无效的账号配置
	ErrInvalidConfig = errors.New("invalid account configuration")

	// ErrResourceNotFound 资源不存在
	ErrResourceNotFound = errors.New("resource not found")

	// ErrRateLimitExceeded 超过速率限制
	ErrRateLimitExceeded = errors.New("rate limit exceeded")

	// ErrNotImplemented 功能未实现
	ErrNotImplemented = errors.New("not implemented")
)
