package aliyun

import (
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/stretchr/testify/assert"
)

func TestParseTime(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantZero bool
	}{
		{"RFC3339", "2024-01-15T10:30:00Z", false},
		{"RFC3339_with_offset", "2024-01-15T10:30:00+08:00", false},
		{"ISO8601_no_tz", "2024-01-15T10:30:00Z", false},
		{"datetime_space", "2024-01-15 10:30:00", false},
		{"empty_string", "", true},
		{"invalid_format", "not-a-date", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseTime(tt.input)
			if tt.wantZero {
				assert.True(t, result.IsZero())
			} else {
				assert.False(t, result.IsZero())
				assert.Equal(t, 2024, result.Year())
				assert.Equal(t, time.January, result.Month())
				assert.Equal(t, 15, result.Day())
			}
		})
	}
}

func TestConvertPolicyType(t *testing.T) {
	tests := []struct {
		input    string
		expected domain.PolicyType
	}{
		{"System", domain.PolicyTypeSystem},
		{"Custom", domain.PolicyTypeCustom},
		{"Unknown", domain.PolicyTypeCustom},
		{"", domain.PolicyTypeCustom},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, convertPolicyType(tt.input))
		})
	}
}
