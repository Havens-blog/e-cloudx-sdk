package aws

import (
	"errors"
	"fmt"
	"testing"

	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
)

// mockHTTPResponseError satisfies both smithy.APIError and interface{ HTTPStatusCode() int }
type mockHTTPResponseError struct {
	smithy.GenericAPIError
	statusCode int
}

func (e *mockHTTPResponseError) HTTPStatusCode() int { return e.statusCode }

func TestMapGranularity(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected cetypes.Granularity
	}{
		{"daily lowercase", "daily", cetypes.GranularityDaily},
		{"daily mixed case", "Daily", cetypes.GranularityDaily},
		{"monthly lowercase", "monthly", cetypes.GranularityMonthly},
		{"monthly mixed case", "Monthly", cetypes.GranularityMonthly},
		{"default empty", "", cetypes.GranularityMonthly},
		{"default unknown", "weekly", cetypes.GranularityMonthly},
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
			name:       "UnrecognizedClientException",
			err:        &smithy.GenericAPIError{Code: "UnrecognizedClientException", Message: "bad key"},
			expectAuth: true,
		},
		{
			name:       "InvalidClientTokenId",
			err:        &smithy.GenericAPIError{Code: "InvalidClientTokenId", Message: "invalid token"},
			expectAuth: true,
		},
		{
			name:       "SignatureDoesNotMatch",
			err:        &smithy.GenericAPIError{Code: "SignatureDoesNotMatch", Message: "sig mismatch"},
			expectAuth: true,
		},
		{
			name:       "AccessDeniedException",
			err:        &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "denied"},
			expectAuth: true,
		},
		{
			name:       "ExpiredTokenException",
			err:        &smithy.GenericAPIError{Code: "ExpiredTokenException", Message: "expired"},
			expectAuth: true,
		},
		{
			name:       "HTTP 401 status",
			err:        &mockHTTPResponseError{GenericAPIError: smithy.GenericAPIError{Code: "SomeCode", Message: "unauthorized"}, statusCode: 401},
			expectAuth: true,
		},
		{
			name:       "HTTP 403 status",
			err:        &mockHTTPResponseError{GenericAPIError: smithy.GenericAPIError{Code: "SomeCode", Message: "forbidden"}, statusCode: 403},
			expectAuth: true,
		},
		{
			name:       "non-auth API error",
			err:        &smithy.GenericAPIError{Code: "LimitExceededException", Message: "rate limited"},
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
				assert.Contains(t, result.Error(), "[aws] authentication failed")
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
			err:      fmt.Errorf("[aws] authentication failed (code: AccessDeniedException): %w", errors.New("denied")),
			expected: false,
		},
		{
			name:     "UnrecognizedClientException not retryable",
			err:      &smithy.GenericAPIError{Code: "UnrecognizedClientException", Message: "bad key"},
			expected: false,
		},
		{
			name:     "AccessDeniedException not retryable",
			err:      &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "denied"},
			expected: false,
		},
		{
			name:     "LimitExceededException retryable",
			err:      &smithy.GenericAPIError{Code: "LimitExceededException", Message: "rate limited"},
			expected: true,
		},
		{
			name:     "Throttling retryable",
			err:      &smithy.GenericAPIError{Code: "Throttling", Message: "throttled"},
			expected: true,
		},
		{
			name:     "ThrottlingException retryable",
			err:      &smithy.GenericAPIError{Code: "ThrottlingException", Message: "throttled"},
			expected: true,
		},
		{
			name:     "HTTP 401 not retryable",
			err:      &mockHTTPResponseError{GenericAPIError: smithy.GenericAPIError{Code: "SomeCode"}, statusCode: 401},
			expected: false,
		},
		{
			name:     "HTTP 403 not retryable",
			err:      &mockHTTPResponseError{GenericAPIError: smithy.GenericAPIError{Code: "SomeCode"}, statusCode: 403},
			expected: false,
		},
		{
			name:     "HTTP 429 retryable",
			err:      &mockHTTPResponseError{GenericAPIError: smithy.GenericAPIError{Code: "SomeCode"}, statusCode: 429},
			expected: true,
		},
		{
			name:     "HTTP 500 retryable",
			err:      &mockHTTPResponseError{GenericAPIError: smithy.GenericAPIError{Code: "SomeCode"}, statusCode: 500},
			expected: true,
		},
		{
			name:     "HTTP 503 retryable",
			err:      &mockHTTPResponseError{GenericAPIError: smithy.GenericAPIError{Code: "SomeCode"}, statusCode: 503},
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
