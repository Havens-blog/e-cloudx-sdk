package aliyun

import (
	"testing"

	vpc "github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"github.com/gotomicro/ego/core/elog"
)

const (
	schemeTestAK     = "test-access-key-id"
	schemeTestSK     = "test-access-key-secret"
	schemeTestRegion = "cn-hangzhou"
	schemeHTTPS      = "HTTPS"
)

// TestAliyunSDKDefaultSchemeIsHTTP 记录修复前提：SDK 简写构造器默认走 HTTP。
func TestAliyunSDKDefaultSchemeIsHTTP(t *testing.T) {
	client, err := vpc.NewClientWithAccessKey(schemeTestRegion, schemeTestAK, schemeTestSK)
	if err != nil {
		t.Fatalf("构造 VPC 客户端失败: %v", err)
	}
	if got := client.GetConfig().Scheme; got != "HTTP" {
		t.Fatalf("SDK 默认协议应为 HTTP（修复前提），实际 %q", got)
	}
}

// TestAdaptersUseHTTPSByDefault 逐个适配器断言客户端默认协议已改成 HTTPS，
// 避免出现「有的适配器显式设置 request.Scheme、有的靠 SDK 默认 HTTP」的分裂。
func TestAdaptersUseHTTPSByDefault(t *testing.T) {
	logger := elog.DefaultLogger

	cases := []struct {
		name   string
		create func() (*vpc.Client, error)
	}{
		{"VPC", func() (*vpc.Client, error) {
			return NewVPCAdapter(schemeTestAK, schemeTestSK, schemeTestRegion, logger).createClient("")
		}},
		{"VSwitch", func() (*vpc.Client, error) {
			return NewVSwitchAdapter(schemeTestAK, schemeTestSK, schemeTestRegion, logger).createClient("")
		}},
		{"EIP", func() (*vpc.Client, error) {
			return NewEIPAdapter(schemeTestAK, schemeTestSK, schemeTestRegion, logger).createClient("")
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client, err := c.create()
			if err != nil {
				t.Fatalf("构造客户端失败: %v", err)
			}
			if got := client.GetConfig().Scheme; got != schemeHTTPS {
				t.Fatalf("%s 客户端默认协议应为 HTTPS，实际 %q", c.name, got)
			}
		})
	}
}

// TestOtherAdaptersUseHTTPSByDefault 覆盖非 vpc 包客户端（ecs/slb/cdn/alidns...）。
func TestOtherAdaptersUseHTTPSByDefault(t *testing.T) {
	logger := elog.DefaultLogger

	eniClient, err := NewENIAdapter(schemeTestAK, schemeTestSK, schemeTestRegion, logger).createClient("")
	if err != nil {
		t.Fatalf("构造 ENI 客户端失败: %v", err)
	}
	if got := eniClient.GetConfig().Scheme; got != schemeHTTPS {
		t.Fatalf("ENI 客户端默认协议应为 HTTPS，实际 %q", got)
	}

	lbClient, err := NewLBAdapter(schemeTestAK, schemeTestSK, schemeTestRegion, logger).createClient("")
	if err != nil {
		t.Fatalf("构造 SLB 客户端失败: %v", err)
	}
	if got := lbClient.GetConfig().Scheme; got != schemeHTTPS {
		t.Fatalf("SLB 客户端默认协议应为 HTTPS，实际 %q", got)
	}

	dnsClient, err := NewDNSAdapter(schemeTestAK, schemeTestSK, schemeTestRegion, logger).createClient()
	if err != nil {
		t.Fatalf("构造 Alidns 客户端失败: %v", err)
	}
	if got := dnsClient.GetConfig().Scheme; got != schemeHTTPS {
		t.Fatalf("Alidns 客户端默认协议应为 HTTPS，实际 %q", got)
	}

	tagClient, err := NewTagAdapter(schemeTestAK, schemeTestSK, schemeTestRegion, logger).createClient("")
	if err != nil {
		t.Fatalf("构造 ECS(Tag) 客户端失败: %v", err)
	}
	if got := tagClient.GetConfig().Scheme; got != schemeHTTPS {
		t.Fatalf("ECS(Tag) 客户端默认协议应为 HTTPS，实际 %q", got)
	}

	cdnClient, err := NewCDNAdapter(schemeTestAK, schemeTestSK, schemeTestRegion, logger).createClient()
	if err != nil {
		t.Fatalf("构造 CDN 客户端失败: %v", err)
	}
	if got := cdnClient.GetConfig().Scheme; got != schemeHTTPS {
		t.Fatalf("CDN 客户端默认协议应为 HTTPS，实际 %q", got)
	}
}
