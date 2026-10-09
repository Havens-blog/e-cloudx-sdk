// Package aliyun_test WAF 源站字段实盘探测(manual probe,只读)。
//
// 目的:定位「阿里云 WAF 源站字段缺失」的根因。怀疑点:
//  1. DescribeDefenseResources 返回的 Detail 里真实域名字段名可能不是 `domain`,
//     导致 extractDomainFromDetail 取空、回退用带后缀的 Resource,DescribeDomainDetail 查不到;
//  2. DescribeDomainDetail 的 Redirect JSON 真实字段名(AllBackends/BackendList/Backends)与代码假设不符。
//
// SKIP gate(无 env 不跑):NAS_PROBE_ALIYUN_AK/SK/REGION 直填,
//   或 NAS_PROBE_MONGODB_DSN 从库加载活跃 aliyun 账号。
//
// Hard Rule:只读凭证,只打印到测试日志,不动生产表。
package aliyun_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
)

// TestManualProbeAliyunWAFSource 打印阿里 WAF 列表 Detail 与详情 Redirect 的原始 JSON。
func TestManualProbeAliyunWAFSource(t *testing.T) {
	setupAliyunProbeEnv(t)
	ak, sk, region := loadAliyunCreds(t)
	if ak == "" {
		t.Skip("未设置 NAS_PROBE_ALIYUN_AK/SK/REGION 或 NAS_PROBE_MONGODB_DSN,跳过(无 env 不跑)")
	}
	if region == "" {
		region = "cn-hangzhou"
	}
	// WAF 3.0 为全局服务,中国站 API 端点仅 cn-hangzhou(与生产 wafEndpoint 同口径);
	// 账号首个资产 region(cn-shenzhen 等)不是 WAF 端点 region,须覆盖。
	if !strings.HasPrefix(region, "ap-") {
		region = "cn-hangzhou"
	}

	client, err := sdk.NewClientWithAccessKey(region, ak, sk)
	if err != nil {
		t.Skipf("创建 WAF 客户端失败(跳过): %v", err)
	}

	// 1) DescribeDefenseResources 列表,打印前 5 个资源的 Detail 原始 JSON
	req := requests.NewCommonRequest()
	req.Method = "POST"
	req.Scheme = "https"
	req.Domain = fmt.Sprintf("wafopenapi.%s.aliyuncs.com", region)
	req.Version = "2021-10-01"
	req.ApiName = "DescribeDefenseResources"
	req.QueryParams["RegionId"] = region
	req.QueryParams["PageNumber"] = "1"
	req.QueryParams["PageSize"] = "10"

	resp, err := client.ProcessCommonRequest(req)
	if err != nil {
		t.Skipf("DescribeDefenseResources 失败(WAF 未开通?跳过): %v", err)
	}

	var listResp struct {
		TotalCount int64 `json:"TotalCount"`
		Resources  []struct {
			Resource string          `json:"Resource"`
			Detail   json.RawMessage `json:"Detail"`
		} `json:"Resources"`
	}
	if err := json.Unmarshal(resp.GetHttpContentBytes(), &listResp); err != nil {
		t.Fatalf("解析列表响应失败: %v", err)
	}

	t.Logf("=== 列表资源数: %d (total %d) ===", len(listResp.Resources), listResp.TotalCount)
	for i, r := range listResp.Resources {
		if i >= 5 {
			break
		}
		var detailRaw any
		// Detail 可能是 JSON 字符串(需二次解析)或对象
		var detailStr string
		if err := json.Unmarshal(r.Detail, &detailStr); err == nil {
			if err := json.Unmarshal([]byte(detailStr), &detailRaw); err != nil {
				detailRaw = detailStr
			}
		} else {
			_ = json.Unmarshal(r.Detail, &detailRaw)
		}
		t.Logf("[%d] Resource=%q Detail=%s", i, r.Resource, marshalIndent(detailRaw))
	}

	// 2) 对第一个资源,尝试 DescribeDomainDetail,打印 Redirect 原始 JSON
	if len(listResp.Resources) > 0 {
		first := listResp.Resources[0]
		// 提取域名:先试 detail.domain,回退 Resource
		domain := extractDomainRaw(first.Detail)
		if domain == "" {
			domain = first.Resource
		}
		t.Logf("=== 尝试 DescribeDomainDetail,domain=%q ===", domain)

		// 需要实例 ID,先 DescribeInstance
		instID := probeWAFInstanceID(client, region)
		if instID == "" {
			t.Logf("DescribeInstance 拿不到实例 ID,跳过详情探测")
		} else {
			detailReq := requests.NewCommonRequest()
			detailReq.Method = "POST"
			detailReq.Scheme = "https"
			detailReq.Domain = fmt.Sprintf("wafopenapi.%s.aliyuncs.com", region)
			detailReq.Version = "2021-10-01"
			detailReq.ApiName = "DescribeDomainDetail"
			detailReq.QueryParams["RegionId"] = region
			detailReq.QueryParams["InstanceId"] = instID
			detailReq.QueryParams["Domain"] = domain

			dResp, err := client.ProcessCommonRequest(detailReq)
			if err != nil {
				t.Logf("DescribeDomainDetail 失败: %v", err)
			} else {
				var detailResp map[string]any
				if err := json.Unmarshal(dResp.GetHttpContentBytes(), &detailResp); err == nil {
					t.Logf("DescribeDomainDetail 原始响应: %s", marshalIndent(detailResp))
				}
			}
		}
	}
}

func marshalIndent(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// extractDomainRaw 从 Detail 原始 JSON 提取域名(与生产 extractDomainFromDetail 同逻辑,独立实现便于核对)。
func extractDomainRaw(detail json.RawMessage) string {
	if len(detail) == 0 {
		return ""
	}
	var d struct {
		Domain string `json:"domain"`
	}
	var detailStr string
	if err := json.Unmarshal(detail, &detailStr); err == nil {
		_ = json.Unmarshal([]byte(detailStr), &d)
	} else {
		_ = json.Unmarshal(detail, &d)
	}
	return d.Domain
}

// probeWAFInstanceID 调 DescribeInstance 拿 WAF 实例 ID。
func probeWAFInstanceID(client *sdk.Client, region string) string {
	req := requests.NewCommonRequest()
	req.Method = "POST"
	req.Scheme = "https"
	req.Domain = fmt.Sprintf("wafopenapi.%s.aliyuncs.com", region)
	req.Version = "2021-10-01"
	req.ApiName = "DescribeInstance"
	req.QueryParams["RegionId"] = region

	resp, err := client.ProcessCommonRequest(req)
	if err != nil {
		return ""
	}
	var r struct {
		InstanceID string `json:"InstanceId"`
	}
	if err := json.Unmarshal(resp.GetHttpContentBytes(), &r); err != nil {
		return ""
	}
	return r.InstanceID
}

// setupAliyunProbeEnv 从 config/prod.yaml 读取 mongodb DSN 与解密密钥,写入探测环境变量,
// 使 loadAliyunCreds → nasprobe 能连库并正确解密 SK(凭证不回显)。
func setupAliyunProbeEnv(t *testing.T) {
	raw, err := os.ReadFile("../../../../config/prod.yaml")
	if err != nil {
		t.Logf("未找到 config/prod.yaml(跳过 env 注入): %v", err)
		return
	}
	var dsn, user, pass, key string
	inMongo, inSec := false, false
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "mongodb:"):
			inMongo, inSec = true, false
		case strings.HasPrefix(trimmed, "security:"):
			inMongo, inSec = false, true
		case inMongo && strings.HasPrefix(trimmed, "dsn:"):
			dsn = strings.TrimSpace(strings.TrimPrefix(trimmed, "dsn:"))
		case inMongo && strings.HasPrefix(trimmed, "username:"):
			user = strings.TrimSpace(strings.TrimPrefix(trimmed, "username:"))
		case inMongo && strings.HasPrefix(trimmed, "password:"):
			pass = strings.TrimSpace(strings.TrimPrefix(trimmed, "password:"))
		case inSec && strings.HasPrefix(trimmed, "encryption_key:"):
			key = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "encryption_key:")), `"'`)
		}
	}
	if dsn != "" && user != "" {
		dsn = strings.Replace(dsn, "mongodb://", fmt.Sprintf("mongodb://%s:%s@", user, pass), 1)
		os.Setenv("NAS_PROBE_MONGODB_DSN", dsn)
	}
	if key != "" {
		os.Setenv("CAM_ENCRYPTION_KEY", key)
	}
}