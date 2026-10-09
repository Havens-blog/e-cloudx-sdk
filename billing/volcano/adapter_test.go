package volcano

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/volcengine/volcengine-go-sdk/volcengine/volcengineerr"
)

func TestAsAuthError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		expectAuth bool
	}{
		{
			name:       "SignatureDoesNotMatch error code",
			err:        volcengineerr.New("SignatureDoesNotMatch", "signature mismatch", nil),
			expectAuth: true,
		},
		{
			name:       "InvalidAccessKeyId error code",
			err:        volcengineerr.New("InvalidAccessKeyId", "bad key", nil),
			expectAuth: true,
		},
		{
			name:       "Forbidden error code",
			err:        volcengineerr.New("Forbidden", "access denied", nil),
			expectAuth: true,
		},
		{
			name:       "AuthFailure error code",
			err:        volcengineerr.New("AuthFailure", "auth failed", nil),
			expectAuth: true,
		},
		{
			name:       "InvalidAccessKey error code",
			err:        volcengineerr.New("InvalidAccessKey", "invalid key", nil),
			expectAuth: true,
		},
		{
			name:       "HTTP 401 request failure",
			err:        volcengineerr.NewRequestFailure(volcengineerr.New("SomeCode", "unauthorized", nil), 401, "req-123"),
			expectAuth: true,
		},
		{
			name:       "HTTP 403 request failure",
			err:        volcengineerr.NewRequestFailure(volcengineerr.New("SomeCode", "forbidden", nil), 403, "req-456"),
			expectAuth: true,
		},
		{
			name:       "non-auth volcengine error",
			err:        volcengineerr.New("Throttling", "rate limited", nil),
			expectAuth: false,
		},
		{
			name:       "HTTP 500 request failure is not auth",
			err:        volcengineerr.NewRequestFailure(volcengineerr.New("InternalError", "server error", nil), 500, "req-789"),
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
				assert.Contains(t, result.Error(), "[volcano] authentication failed")
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
			err:      fmt.Errorf("[volcano] authentication failed (code: AuthFailure): %w", errors.New("auth failed")),
			expected: false,
		},
		{
			name:     "SignatureDoesNotMatch not retryable",
			err:      volcengineerr.New("SignatureDoesNotMatch", "sig mismatch", nil),
			expected: false,
		},
		{
			name:     "InvalidAccessKeyId not retryable",
			err:      volcengineerr.New("InvalidAccessKeyId", "bad key", nil),
			expected: false,
		},
		{
			name:     "Forbidden not retryable",
			err:      volcengineerr.New("Forbidden", "denied", nil),
			expected: false,
		},
		{
			name:     "Throttling retryable",
			err:      volcengineerr.New("Throttling", "rate limited", nil),
			expected: true,
		},
		{
			name:     "RequestLimitExceeded retryable",
			err:      volcengineerr.New("RequestLimitExceeded", "limit exceeded", nil),
			expected: true,
		},
		{
			name:     "TooManyRequests retryable",
			err:      volcengineerr.New("TooManyRequests", "too many", nil),
			expected: true,
		},
		{
			name:     "HTTP 401 not retryable",
			err:      volcengineerr.NewRequestFailure(volcengineerr.New("Unauthorized", "unauth", nil), 401, "req-1"),
			expected: false,
		},
		{
			name:     "HTTP 403 not retryable",
			err:      volcengineerr.NewRequestFailure(volcengineerr.New("Forbidden", "denied", nil), 403, "req-2"),
			expected: false,
		},
		{
			name:     "HTTP 429 retryable",
			err:      volcengineerr.NewRequestFailure(volcengineerr.New("TooManyRequests", "throttled", nil), 429, "req-3"),
			expected: true,
		},
		{
			name:     "HTTP 500 retryable",
			err:      volcengineerr.NewRequestFailure(volcengineerr.New("InternalError", "server error", nil), 500, "req-4"),
			expected: true,
		},
		{
			name:     "HTTP 503 retryable",
			err:      volcengineerr.NewRequestFailure(volcengineerr.New("ServiceUnavailable", "unavailable", nil), 503, "req-5"),
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
			name:     "i/o timeout retryable",
			err:      errors.New("i/o timeout"),
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
