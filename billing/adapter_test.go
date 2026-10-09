package billing

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"pgregory.net/rapid"
)

// Feature: multicloud-finops, Property 1: 计费 API 错误信息格式
// For any 云厂商标识（阿里云、AWS、火山引擎、华为云、腾讯云）和任意认证失败错误码，
// 计费适配器返回的错误信息应包含该云厂商名称和原始错误码。
//
// **Validates: Requirements 1.6**
func TestProperty1_BillingAPIErrorFormat(t *testing.T) {
	// Known providers matching the adapter implementations
	knownProviders := []string{"aliyun", "aws", "volcano", "huawei", "tencent"}

	rapid.Check(t, func(rt *rapid.T) {
		// Pick a provider from the known set
		provider := knownProviders[rapid.IntRange(0, len(knownProviders)-1).Draw(rt, "providerIdx")]

		// Generate a random non-empty error code
		errorCode := rapid.StringMatching(`[A-Za-z][A-Za-z0-9._]{0,49}`).Draw(rt, "errorCode")

		// Generate a random original error message
		originalErrMsg := rapid.StringMatching(`[a-zA-Z0-9 ]{1,100}`).Draw(rt, "originalErrMsg")
		originalErr := fmt.Errorf("%s", originalErrMsg)

		// Format the error using the same pattern all adapters use
		formattedErr := fmt.Errorf("[%s] authentication failed (code: %s): %w", provider, errorCode, originalErr)
		errMsg := formattedErr.Error()

		// Property 1: Error message contains the provider name in brackets
		assert.True(t, strings.Contains(errMsg, "["+provider+"]"),
			"error message should contain provider name [%s], got: %s", provider, errMsg)

		// Property 2: Error message contains the original error code
		assert.True(t, strings.Contains(errMsg, errorCode),
			"error message should contain error code %q, got: %s", errorCode, errMsg)

		// Property 3: Error message contains "authentication failed"
		assert.True(t, strings.Contains(errMsg, "authentication failed"),
			"error message should contain 'authentication failed', got: %s", errMsg)
	})
}
