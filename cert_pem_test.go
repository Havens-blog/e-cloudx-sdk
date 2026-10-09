package cloudx

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ==================== 测试材料 ====================
// 真实 DER 无需参与净化断言（块级过滤只看块类型），使用固定 base64 内容的
// 手写 PEM 块保证可读与确定性。

const (
	testSanitizeCert1 = "-----BEGIN CERTIFICATE-----\nZmFrZS1jZXJ0LTE=\n-----END CERTIFICATE-----\n"
	testSanitizeCert2 = "-----BEGIN CERTIFICATE-----\nZmFrZS1jZXJ0LTI=\n-----END CERTIFICATE-----\n"
	testSanitizeCert3 = "-----BEGIN CERTIFICATE-----\nZmFrZS1jZXJ0LTM=\n-----END CERTIFICATE-----\n"
	testSanitizeKey   = "-----BEGIN EC PRIVATE KEY-----\nZmFrZS1rZXk=\n-----END EC PRIVATE KEY-----\n"
	testSanitizeKey2  = "-----BEGIN PRIVATE KEY-----\nZmFrZS1rZXktMg==\n-----END PRIVATE KEY-----\n"
)

func TestSanitizeCertChainPEM(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "仅证书块按原序保留", in: testSanitizeCert1 + testSanitizeCert2 + testSanitizeCert3, want: testSanitizeCert1 + testSanitizeCert2 + testSanitizeCert3},
		{name: "前导私钥块被丢弃", in: testSanitizeKey + testSanitizeCert1 + testSanitizeCert2, want: testSanitizeCert1 + testSanitizeCert2},
		{name: "混排私钥块被丢弃且证书顺序不变", in: testSanitizeCert1 + testSanitizeKey2 + testSanitizeCert2, want: testSanitizeCert1 + testSanitizeCert2},
		{name: "块间非 PEM 噪声被丢弃", in: "leading-noise\n" + testSanitizeCert1 + "trailing-noise\n" + testSanitizeCert2, want: testSanitizeCert1 + testSanitizeCert2},
		{name: "块间粘连仍有分隔时逐块解析", in: testSanitizeCert1 + "garbage-between\n" + testSanitizeCert2, want: testSanitizeCert1 + testSanitizeCert2},
		{name: "仅私钥返回空串", in: testSanitizeKey + testSanitizeKey2, want: ""},
		{name: "非 PEM 输入返回空串", in: "not a pem at all", want: ""},
		{name: "空输入返回空串", in: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SanitizeCertChainPEM([]byte(tt.in)))
		})
	}
}

// TestSanitizeCertChainPEMContentLevel 内容级断言（对齐 Non-Functional
// Requirements 安全条目）：净化结果不含 "PRIVATE KEY" 字样、含且仅含
// CERTIFICATE 块。
func TestSanitizeCertChainPEMContentLevel(t *testing.T) {
	inputs := []string{
		testSanitizeKey + testSanitizeCert1 + testSanitizeCert2,
		testSanitizeCert1 + testSanitizeKey2 + testSanitizeCert3 + testSanitizeKey,
	}
	for i, in := range inputs {
		out := SanitizeCertChainPEM([]byte(in))
		require.NotEmpty(t, out, i)
		assert.NotContains(t, out, "PRIVATE KEY", i)
		// 所有 PEM 块头均为 CERTIFICATE：BEGIN 出现次数 == CERTIFICATE 块数
		assert.Equal(t, strings.Count(out, "-----BEGIN "), strings.Count(out, "-----BEGIN CERTIFICATE-----"), i)
		blocks := strings.Count(out, "-----BEGIN CERTIFICATE-----")
		assert.Equal(t, blocks, strings.Count(out, "-----END CERTIFICATE-----"), i)
	}
}

// TestSanitizeCertChainPEMNotModifyOriginalSanitizedPath 净化不修改输入 buffer
// （净化为读侧过滤，原 buffer 由调用方 Zeroize）。
func TestSanitizeCertChainPEMNotModifyOriginalSanitizedPath(t *testing.T) {
	in := []byte(testSanitizeKey + testSanitizeCert1)
	out := SanitizeCertChainPEM(in)
	assert.Equal(t, testSanitizeCert1, out)
	assert.Equal(t, testSanitizeKey+testSanitizeCert1, string(in), "净化不得篡改输入 buffer")
}

func TestZeroize(t *testing.T) {
	buf := []byte(testSanitizeKey + testSanitizeCert1)
	Zeroize(buf)
	for i, b := range buf {
		require.Zero(t, b, "第 %d 字节未归零", i)
	}
	assert.Equal(t, make([]byte, len(buf)), buf)

	// 空切片/nil 安全
	Zeroize(nil)
	Zeroize([]byte{})
}
