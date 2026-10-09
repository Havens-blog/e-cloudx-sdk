package tencent

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	tcerr "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
)

func TestAsAuthError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		expectAuth bool
	}{
		{
			name:       "AuthFailure code",
			err:        &tcerr.TencentCloudSDKError{Code: "AuthFailure", Message: "auth failed"},
			expectAuth: true,
		},
		{
			name:       "AuthFailure.SecretIdNotFound",
			err:        &tcerr.TencentCloudSDKError{Code: "AuthFailure.SecretIdNotFound", Message: "secret not found"},
			expectAuth: true,
		},
		{
			name:       "AuthFailure.SignatureFailure",
			err:        &tcerr.TencentCloudSDKError{Code: "AuthFailure.SignatureFailure", Message: "sig failed"},
			expectAuth: true,
		},
		{
			name:       "UnauthorizedOperation",
			err:        &tcerr.TencentCloudSDKError{Code: "UnauthorizedOperation", Message: "unauthorized"},
			expectAuth: true,
		},
		{
			name:       "non-auth SDK error",
			err:        &tcerr.TencentCloudSDKError{Code: "RequestLimitExceeded", Message: "rate limited"},
			expectAuth: false,
		},
		{
			name:       "InternalError is not auth",
			err:        &tcerr.TencentCloudSDKError{Code: "InternalError", Message: "server error"},
			expectAuth: false,
		},
		{
			name:       "non-sdk error",
			err:        errors.New("some random error"),
			expectAuth: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := asAuthError(tt.err)
			if tt.expectAuth {
				assert.NotNil(t, result)
				assert.Contains(t, result.Error(), "[tencent] authentication failed")
			} else {
				assert.Nil(t, result)
			}
		})
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "auth error not retryable",
			err:      fmt.Errorf("[tencent] authentication failed (code: AuthFailure): %w", errors.New("auth failed")),
			expected: false,
		},
		{
			name:     "AuthFailure not retryable",
			err:      &tcerr.TencentCloudSDKError{Code: "AuthFailure", Message: "auth failed"},
			expected: false,
		},
		{
			name:     "AuthFailure.SecretIdNotFound not retryable",
			err:      &tcerr.TencentCloudSDKError{Code: "AuthFailure.SecretIdNotFound", Message: "not found"},
			expected: false,
		},
		{
			name:     "UnauthorizedOperation not retryable",
			err:      &tcerr.TencentCloudSDKError{Code: "UnauthorizedOperation", Message: "unauthorized"},
			expected: false,
		},
		{
			name:     "RequestLimitExceeded retryable",
			err:      &tcerr.TencentCloudSDKError{Code: "RequestLimitExceeded", Message: "rate limited"},
			expected: true,
		},
		{
			name:     "LimitExceeded retryable",
			err:      &tcerr.TencentCloudSDKError{Code: "LimitExceeded", Message: "limit exceeded"},
			expected: true,
		},
		{
			name:     "InternalError retryable",
			err:      &tcerr.TencentCloudSDKError{Code: "InternalError", Message: "server error"},
			expected: true,
		},
		{
			name:     "InternalError.DbError retryable",
			err:      &tcerr.TencentCloudSDKError{Code: "InternalError.DbError", Message: "db error"},
			expected: true,
		},
		{
			name:     "timeout error retryable",
			err:      errors.New("request timeout"),
			expected: true,
		},
		{
			name:     "connection refused retryable",
			err:      errors.New("connection refused"),
			expected: true,
		},
		{
			name:     "connection reset retryable",
			err:      errors.New("connection reset"),
			expected: true,
		},
		{
			name:     "i/o timeout retryable",
			err:      errors.New("i/o timeout"),
			expected: true,
		},
		{
			name:     "net/http error retryable",
			err:      errors.New("net/http: request canceled"),
			expected: true,
		},
		{
			name:     "unknown error not retryable",
			err:      errors.New("some unknown error"),
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isRetryable(tt.err))
		})
	}
}
