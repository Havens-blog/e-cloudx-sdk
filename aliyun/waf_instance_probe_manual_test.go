// Package aliyun_test WAF 实例/地域分裂实盘探测(manual probe,只读)。
//
// 目的:定位阿里 WAF 源站缺失根因 Waf.Instance.ValidFaild。怀疑点:
//  1. 阿里 WAF 3.0 中国站(cn-hangzhou)与国际站(ap-southeast-1)是独立实例,
//     DescribeInstance 的 RegionId 会返回不同 InstanceId;通用适配器只缓存单个
//     instanceID,跨 region 同步时实例/域名错位 → DescribeDomainDetail 校验失败。
//  2. ap-southeast-1 列表混入 CLB 型防护对象("alb-*-alb"),非域名,详情查不到。
//
// Hard Rule:只读凭证,只打印到测试日志,不动生产表。
package aliyun_test

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
)

// keysOf 返回 map 的排序后键名(便于定位真实字段名)。
func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestProbeAliyunWAFInstanceSplit 打印两个站点的 DescribeInstance 原始响应 + 域名详情校验。
func TestProbeAliyunWAFInstanceSplit(t *testing.T) {
	setupAliyunProbeEnv(t)
	ak, sk, _ := loadAliyunCreds(t)
	if ak == "" {
		t.Skip("无 aliyun 凭证,跳过")
	}

	for _, site := range []string{"cn-hangzhou", "ap-southeast-1"} {
		client, err := sdk.NewClientWithAccessKey(site, ak, sk)
		if err != nil {
			t.Logf("[%s] 建客户端失败: %v", site, err)
			continue
		}
		req := requests.NewCommonRequest()
		req.Method = "POST"
		req.Scheme = "https"
		req.Domain = fmt.Sprintf("wafopenapi.%s.aliyuncs.com", site)
		req.Version = "2021-10-01"
		req.ApiName = "DescribeInstance"
		req.QueryParams["RegionId"] = site

		resp, err := client.ProcessCommonRequest(req)
		if err != nil {
			t.Logf("[%s] DescribeInstance err=%v", site, err)
			continue
		}
		var raw map[string]any
		_ = json.Unmarshal(resp.GetHttpContentBytes(), &raw)
		// 只打印顶层/含 Instance/Id 的键,定位实例 ID 真实字段名
		for k, v := range raw {
			if k == "InstanceId" || k == "InstanceInfo" || k == "instanceId" || k == "Code" || k == "Message" {
				t.Logf("[%s] key %q = %v", site, k, v)
			}
		}
		t.Logf("[%s] top-level keys: %v", site, keysOf(raw))
	}
}