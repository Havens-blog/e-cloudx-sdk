package aliyun

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	sdkerrors "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	"github.com/stretchr/testify/assert"
)

func TestMapGranularity(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"daily lowercase", "daily", "DAILY"},
		{"daily mixed case", "Daily", "DAILY"},
		{"monthly lowercase", "monthly", "MONTHLY"},
		{"monthly mixed case", "Monthly", "MONTHLY"},
		{"default empty", "", "MONTHLY"},
		{"default unknown", "weekly", "MONTHLY"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, mapGranularity(tt.input))
		})
	}
}

func TestAsAuthError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		expectAuth bool
	}{
		{
			name:       "401 unauthorized",
			err:        sdkerrors.NewServerError(http.StatusUnauthorized, `{"Code":"InvalidAccessKeyId.NotFound"}`, ""),
			expectAuth: true,
		},
		{
			name:       "403 forbidden",
			err:        sdkerrors.NewServerError(http.StatusForbidden, `{"Code":"Forbidden.RAM"}`, ""),
			expectAuth: true,
		},
		{
			name:       "500 server error is not auth",
			err:        sdkerrors.NewServerError(http.StatusInternalServerError, `{"Code":"InternalError"}`, ""),
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
				assert.Contains(t, result.Error(), "[aliyun] authentication failed")
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
			err:      fmt.Errorf("[aliyun] authentication failed (code: Forbidden.RAM): %w", errors.New("forbidden")),
			expected: false,
		},
		{
			name:     "401 not retryable",
			err:      sdkerrors.NewServerError(http.StatusUnauthorized, `{"Code":"InvalidAccessKeyId.NotFound"}`, ""),
			expected: false,
		},
		{
			name:     "403 not retryable",
			err:      sdkerrors.NewServerError(http.StatusForbidden, `{"Code":"Forbidden.RAM"}`, ""),
			expected: false,
		},
		{
			name:     "429 rate limit retryable",
			err:      sdkerrors.NewServerError(http.StatusTooManyRequests, `{"Code":"Throttling"}`, ""),
			expected: true,
		},
		{
			name:     "500 server error retryable",
			err:      sdkerrors.NewServerError(http.StatusInternalServerError, `{"Code":"InternalError"}`, ""),
			expected: true,
		},
		{
			name:     "502 bad gateway retryable",
			err:      sdkerrors.NewServerError(http.StatusBadGateway, `{"Code":"BadGateway"}`, ""),
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
