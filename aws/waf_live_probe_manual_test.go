// Package aws AWS WAF 源站/防护域名解析实盘探测(manual probe,只读)。
//
// 自包含:DSN 从 config/prod.yaml 读取(不回显凭证),经 nasprobe 只读加载活跃 aws
// 账号并解密 SK,直接调用 wafv2 ListWebACLs 逐 scope 打印结果与错误,
// 再用适配器 ListInstances 打印解析后的防护域名/源站,核对「AWS WAF 看不到源站」根因。
//
// Hard Rule:只读,只写实例概要与掩码凭证到测试日志,不动生产表。
package aws

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/nasprobe"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cloudfronttypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	"github.com/aws/aws-sdk-go-v2/service/wafv2"
	"github.com/aws/aws-sdk-go-v2/service/wafv2/types"
	"github.com/gotomicro/ego/core/elog"
)

// TestLiveAWSWAFResolved 直接用真实账号解析 AWS WAF 源站与防护域名。
func TestLiveAWSWAFResolved(t *testing.T) {
	ak, sk, regions := probeAWSAccount(t)
	if ak == "" || sk == "" {
		t.Skip("库中无 aws 账号,跳过")
	}
	t.Logf("探测账号 AK=%s SK_len=%d regions=%v", nasprobe.MaskAK(ak), len(sk), regions)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// 1) 直连 wafv2,逐 scope 打印 ListWebACLs 数量与错误(定位是 auth 还是 scope 问题)。
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(ak, sk, "")),
	)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	client := wafv2.NewFromConfig(cfg)

	regionalOut, rErr := client.ListWebACLs(ctx, &wafv2.ListWebACLsInput{Scope: types.ScopeRegional})
	t.Logf("Regional(us-east-1): count=%d err=%v", wafCount(regionalOut), rErr)

	cfOut, cfErr := client.ListWebACLs(ctx, &wafv2.ListWebACLsInput{Scope: types.ScopeCloudfront})
	t.Logf("CloudFront(us-east-1): count=%d err=%v", wafCount(cfOut), cfErr)

	// 1b) 逐 WebACL 直接打印 ListResourcesForWebACL 结果/错误,定位 hosts=0 根因。
	if cfOut != nil {
		for i, acl := range cfOut.WebACLs {
			if i >= 4 {
				break
			}
			detail, gErr := client.GetWebACL(ctx, &wafv2.GetWebACLInput{
				Name: acl.Name, Id: acl.Id, Scope: types.ScopeCloudfront,
			})
			if gErr != nil || detail.WebACL == nil || detail.WebACL.ARN == nil {
				t.Logf("[%d] %s GetWebACL err=%v", i, *acl.Name, gErr)
				continue
			}
			res, lErr := client.ListResourcesForWebACL(ctx, &wafv2.ListResourcesForWebACLInput{
				WebACLArn: detail.WebACL.ARN,
			})
			arns := []string(nil)
			if res != nil {
				arns = res.ResourceArns
			}
			t.Logf("[%d] %s ARN=%s ListResourcesForWebACL arns=%v err=%v",
				i, *acl.Name, *detail.WebACL.ARN, arns, lErr)
		}
	}

	// 1c) CloudFront 侧回查:ListDistributions 打印 WebACLId,核对是否真正关联。
	cfc, cfErrC := newCloudFrontClientForProbe(ctx, ak, sk)
	if cfErrC == nil {
		var all []cloudfrontSrv
		var marker *string
		for {
			out, lErr := cfc.ListDistributions(ctx, &cloudfront.ListDistributionsInput{Marker: marker})
			if lErr != nil {
				t.Logf("ListDistributions err=%v", lErr)
				break
			}
			if out.DistributionList != nil {
				for _, d := range out.DistributionList.Items {
					all = append(all, cloudfrontSrv{
						id: str(d.Id), domain: str(d.DomainName), wacl: str(d.WebACLId),
						origins: originDomains(d.Origins), aliases: aliasItems(d.Aliases),
					})
				}
				if out.DistributionList.NextMarker != nil {
					marker = out.DistributionList.NextMarker
					continue
				}
			}
			break
		}
		t.Logf("=== CloudFront 分配总数: %d ===", len(all))
		for i, d := range all {
			if i >= 6 {
				break
			}
			t.Logf("[cf %d] wacl=%q domain=%q aliases=%v origins=%v", i, d.wacl, d.domain, d.aliases, d.origins)
		}
	} else {
		t.Logf("创建 CloudFront 客户端失败: %v", cfErrC)
	}

	// 2) 用适配器 ListInstances(us-east-1)打印解析后的源站/防护域名。
	adapter := NewWAFAdapter(ak, sk, "us-east-1", elog.DefaultLogger)
	instances, err := adapter.ListInstances(ctx, "us-east-1")
	if err != nil {
		t.Fatalf("ListInstances 失败: %v", err)
	}
	t.Logf("=== 适配器解析实例总数: %d ===", len(instances))
	for _, ins := range instances {
		t.Logf("[%s] region=%s hosts=%d sources=%d rules=%d",
			ins.InstanceName, ins.Region, len(ins.ProtectedHosts), len(ins.SourceIPs), ins.RuleCount)
		t.Logf("     protected_hosts = %v", ins.ProtectedHosts)
		t.Logf("     source_ips      = %v", ins.SourceIPs)
	}
}

func wafCount(out *wafv2.ListWebACLsOutput) int {
	if out == nil {
		return 0
	}
	return len(out.WebACLs)
}

// cloudfrontSrv CloudFront 分配概要(探测日志用)。
type cloudfrontSrv struct {
	id, domain, wacl string
	aliases, origins []string
}

func str(s *string) string { return aws.ToString(s) }

func originDomains(o *cloudfronttypes.Origins) []string {
	if o == nil {
		return nil
	}
	var out []string
	for _, it := range o.Items {
		if it.DomainName != nil {
			d := *it.DomainName
			if it.OriginPath != nil && *it.OriginPath != "" {
				d += *it.OriginPath
			}
			out = append(out, d)
		}
	}
	return out
}

func aliasItems(a *cloudfronttypes.Aliases) []string {
	if a == nil {
		return nil
	}
	return a.Items
}

func newCloudFrontClientForProbe(ctx context.Context, ak, sk string) (*cloudfront.Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(ak, sk, "")),
	)
	if err != nil {
		return nil, err
	}
	return cloudfront.NewFromConfig(cfg), nil
}

// probeAWSAccount 从库只读加载第一个活跃 aws 账号(不回显 AK/SK 明文)。
func probeAWSAccount(t *testing.T) (ak, sk string, regions []string) {
	dsn := probeDSNFromConfig(t)
	if dsn == "" {
		return "", "", nil
	}
	os.Setenv("NAS_PROBE_MONGODB_DSN", dsn)
	// 解密密钥从 config/prod.yaml 读取(security.encryption_key),补齐 CAM_ENCRYPTION_KEY
	// 环境变量,使 nasprobe.probeDecrypt 能用正确密钥解密库中密文 AK/SK。
	if key := probeEncryptionKeyFromConfig(); key != "" {
		os.Setenv("CAM_ENCRYPTION_KEY", key)
	}
	accounts, err := nasprobe.LoadProbeAccounts()
	if err != nil {
		t.Fatalf("加载账号失败: %v", err)
	}
	for _, a := range accounts {
		if a.Provider == "aws" {
			return a.AK, a.SK, a.Regions
		}
	}
	return "", "", nil
}

// probeEncryptionKeyFromConfig 从 config/prod.yaml 读取 security.encryption_key(不回显)。
func probeEncryptionKeyFromConfig() string {
	raw, err := os.ReadFile("../../../../config/prod.yaml")
	if err != nil {
		return ""
	}
	inSec := false
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "security:"):
			inSec = true
		case inSec && strings.HasPrefix(trimmed, "encryption_key:"):
			v := strings.TrimSpace(strings.TrimPrefix(trimmed, "encryption_key:"))
			return strings.Trim(v, `"'`)
		case inSec && !strings.HasPrefix(line, " ") && trimmed != "":
			inSec = false
		}
	}
	return ""
}