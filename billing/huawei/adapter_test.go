package huawei

import (
	"errors"
	"fmt"
	"testing"

	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/sdkerr"
	"github.com/stretchr/testify/assert"
)

func TestAsAuthError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		expectAuth bool
	}{
		{
			name:       "HTTP 401 status",
			err:        &sdkerr.ServiceResponseError{StatusCode: 401, ErrorCode: "IAM.0001", ErrorMessage: "unauthorized"},
			expectAuth: true,
		},
		{
			name:       "HTTP 403 status",
			err:        &sdkerr.ServiceResponseError{StatusCode: 403, ErrorCode: "IAM.0101", ErrorMessage: "forbidden"},
			expectAuth: true,
		},
		{
			name:       "IAM.0001 error code",
			err:        &sdkerr.ServiceResponseError{StatusCode: 400, ErrorCode: "IAM.0001", ErrorMessage: "auth error"},
			expectAuth: true,
		},
		{
			name:       "IAM.0101 error code",
			err:        &sdkerr.ServiceResponseError{StatusCode: 400, ErrorCode: "IAM.0101", ErrorMessage: "auth error"},
			expectAuth: true,
		},
		{
			name:       "SignatureDoesNotMatch error code",
			err:        &sdkerr.ServiceResponseError{StatusCode: 400, ErrorCode: "SignatureDoesNotMatch", ErrorMessage: "sig mismatch"},
			expectAuth: true,
		},
		{
			name:       "Unauthorized error code",
			err:        &sdkerr.ServiceResponseError{StatusCode: 400, ErrorCode: "Unauthorized", ErrorMessage: "unauth"},
			expectAuth: true,
		},
		{
			name:       "Forbidden error code",
			err:        &sdkerr.ServiceResponseError{StatusCode: 400, ErrorCode: "Forbidden", ErrorMessage: "denied"},
			expectAuth: true,
		},
		{
			name:       "AuthFailure error code",
			err:        &sdkerr.ServiceResponseError{StatusCode: 400, ErrorCode: "AuthFailure", ErrorMessage: "auth failed"},
			expectAuth: true,
		},
		{
			name:       "500 server error is not auth",
			err:        &sdkerr.ServiceResponseError{StatusCode: 500, ErrorCode: "InternalError", ErrorMessage: "server error"},
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
				assert.Contains(t, result.Error(), "[huawei] authentication failed")
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
			err:      fmt.Errorf("[huawei] authentication failed (code: IAM.0001): %w", errors.New("auth failed")),
			expected: false,
		},
		{
			name:     "401 not retryable",
			err:      &sdkerr.ServiceResponseError{StatusCode: 401, ErrorCode: "IAM.0001"},
			expected: false,
		},
		{
			name:     "403 not retryable",
			err:      &sdkerr.ServiceResponseError{StatusCode: 403, ErrorCode: "Forbidden"},
			expected: false,
		},
		{
			name:     "429 rate limit retryable",
			err:      &sdkerr.ServiceResponseError{StatusCode: 429, ErrorCode: "TooManyRequests"},
			expected: true,
		},
		{
			name:     "500 server error retryable",
			err:      &sdkerr.ServiceResponseError{StatusCode: 500, ErrorCode: "InternalError"},
			expected: true,
		},
		{
			name:     "503 service unavailable retryable",
			err:      &sdkerr.ServiceResponseError{StatusCode: 503, ErrorCode: "ServiceUnavailable"},
			expected: true,
		},
		{
			name:     "Throttling error code retryable",
			err:      &sdkerr.ServiceResponseError{StatusCode: 400, ErrorCode: "Throttling"},
			expected: true,
		},
		{
			name:     "RateLimit error code retryable",
			err:      &sdkerr.ServiceResponseError{StatusCode: 400, ErrorCode: "RateLimit"},
			expected: true,
		},
		{
			name:     "TooManyRequests error code retryable",
			err:      &sdkerr.ServiceResponseError{StatusCode: 400, ErrorCode: "TooManyRequests"},
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
