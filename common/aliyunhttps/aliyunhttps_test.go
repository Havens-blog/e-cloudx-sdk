package aliyunhttps

import (
	"testing"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
)

// TestForceOverridesSDKHTTPDefault 固化「为什么要这个包」：
// 通用 SDK 的默认协议是 HTTP，不修正的话 AccessKeyId 与签名会明文上网。
func TestForceOverridesSDKHTTPDefault(t *testing.T) {
	config := sdk.NewConfig()
	if config.Scheme != "HTTP" {
		t.Fatalf("SDK 默认协议变了：期望 HTTP（本包的修复前提），实际 %q", config.Scheme)
	}

	Force(config)

	if config.Scheme != SchemeHTTPS {
		t.Fatalf("Force 后协议应为 %q，实际 %q", SchemeHTTPS, config.Scheme)
	}
}

// TestForceIgnoresNilConfig 客户端未初始化时不能 panic。
func TestForceIgnoresNilConfig(t *testing.T) {
	Force(nil)
}

// TestForceIsIdempotent 重复调用保持 HTTPS（客户端复用路径会重复构造）。
func TestForceIsIdempotent(t *testing.T) {
	config := sdk.NewConfig()
	Force(config)
	Force(config)

	if config.Scheme != SchemeHTTPS {
		t.Fatalf("重复 Force 后协议应为 %q，实际 %q", SchemeHTTPS, config.Scheme)
	}
}
