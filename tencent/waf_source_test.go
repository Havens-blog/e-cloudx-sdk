package tencent

import (
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	teo "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/teo/v20220901"
	waf "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/waf/v20180125"
)

func TestConvertWAFDomainToInstanceUpstreamDomainList(t *testing.T) {
	// 列表接口兜底:UpstreamDomainList(域名回源)应并入 SourceIPs
	d := &waf.DomainInfo{
		Domain:             strPtr("example.com"),
		DomainId:           strPtr("waf-123"),
		Edition:            strPtr("sparta-waf"),
		State:              func() *int64 { v := int64(1); return &v }(),
		SrcList:            []*string{strPtr("1.2.3.4")},
		UpstreamDomainList: []*string{strPtr("origin.example.com")},
		Cname:              strPtr("example.com.cname"),
		CreateTime:         strPtr("2024-06-18 10:00:00"),
		Labels:             []*string{strPtr("生产"), strPtr("电商")},
	}

	a := &WAFAdapter{}
	inst := a.convertWAFDomainToInstance(d, "ap-guangzhou")

	if len(inst.SourceIPs) != 2 {
		t.Fatalf("期望 SourceIPs 有 2 项(IP + 域名回源),实际 %d: %v", len(inst.SourceIPs), inst.SourceIPs)
	}
	if inst.SourceIPs[0] != "1.2.3.4" || inst.SourceIPs[1] != "origin.example.com" {
		t.Fatalf("SourceIPs 顺序错误: %v", inst.SourceIPs)
	}
	if inst.InstanceID != "waf-123" {
		t.Fatalf("InstanceID 错误: %s", inst.InstanceID)
	}
	// 新增字段断言:创建时间来自列表接口 DomainInfo.CreateTime(厂商原值透传)
	if inst.CreationTime != "2024-06-18 10:00:00" {
		t.Fatalf("CreationTime 未填充: %s", inst.CreationTime)
	}
	// 域名标签(Labels,自由文本)以标签为键、空值为值归入 Tags
	if len(inst.Tags) != 2 || inst.Tags["生产"] != "" || inst.Tags["电商"] != "" {
		t.Fatalf("Tags 应含 2 个标签(non-nil 空值),实际 %v", inst.Tags)
	}
}

func TestConvertEOZoneToInstanceTagsAndCreationTime(t *testing.T) {
	// EO 站点:描述/创建时间/标签均来自 DescribeZones 列表字段
	zone := &teo.Zone{
		ZoneId:     strPtr("zone-1"),
		ZoneName:   strPtr("site.example.com"),
		Status:     strPtr("active"),
		Area:       strPtr("mainland"),
		Type:       strPtr("partial"),
		CreatedOn:  strPtr("2024-03-01T08:00:00Z"),
		ModifiedOn: strPtr("2024-06-01T08:00:00Z"),
		Tags: []*teo.Tag{
			{TagKey: strPtr("env"), TagValue: strPtr("prod")},
			{TagKey: strPtr("owner"), TagValue: strPtr("team-a")},
			{TagKey: strPtr("novalue"), TagValue: nil},
			nil,
		},
	}

	a := &WAFAdapter{}
	inst := a.convertEOZoneToInstance(zone)

	if inst.CreationTime != "2024-03-01T08:00:00Z" {
		t.Fatalf("CreationTime 错误: %q", inst.CreationTime)
	}
	if inst.Tags["env"] != "prod" {
		t.Fatalf("Tags env 错误: %v", inst.Tags)
	}
	if inst.Tags["owner"] != "team-a" {
		t.Fatalf("Tags owner 错误: %v", inst.Tags)
	}
	if v, ok := inst.Tags["novalue"]; !ok || v != "" {
		t.Fatalf("TagValue=nil 应保留键且值为空: got %q, ok=%v", v, ok)
	}
	if len(inst.Tags) != 3 {
		t.Fatalf("Tags 应归一 3 项(nil 项跳过),实际 %d: %v", len(inst.Tags), inst.Tags)
	}
}

func TestMergeSaaSSources(t *testing.T) {
	t.Run("IP 回源 + 域名回源合并", func(t *testing.T) {
		detail := &waf.DomainsPartInfo{
			SrcList:        []*string{strPtr("10.0.0.1"), strPtr("10.0.0.2")},
			UpstreamDomain: strPtr("backend.example.com"),
			UpstreamType:   func() *uint64 { v := uint64(1); return &v }(),
			Cname:          strPtr("waf-cname.example.com"),
			CreateTime:     strPtr("2024-01-01 00:00:00"),
		}
		inst := &types.WAFInstance{Description: "WAF防护域名"}
		mergeSaaSSources(detail, inst)

		if len(inst.SourceIPs) != 3 {
			t.Fatalf("期望 SourceIPs 3 项,实际 %d: %v", len(inst.SourceIPs), inst.SourceIPs)
		}
		if inst.SourceIPs[2] != "backend.example.com" {
			t.Fatalf("域名回源未并入: %v", inst.SourceIPs)
		}
		if inst.Cname != "waf-cname.example.com" {
			t.Fatalf("Cname 未覆盖: %s", inst.Cname)
		}
		if inst.CreationTime != "2024-01-01 00:00:00" {
			t.Fatalf("CreationTime 未覆盖: %s", inst.CreationTime)
		}
		if inst.Description != "WAF防护域名 upstream_type=domain" {
			t.Fatalf("Description 未标注域名回源: %s", inst.Description)
		}
	})

	t.Run("仅 IP 回源(UpstreamType=0)不改 Description", func(t *testing.T) {
		detail := &waf.DomainsPartInfo{
			SrcList:      []*string{strPtr("10.0.0.1")},
			UpstreamType: func() *uint64 { v := uint64(0); return &v }(),
		}
		inst := &types.WAFInstance{Description: "WAF防护域名"}
		mergeSaaSSources(detail, inst)

		if inst.Description != "WAF防护域名" {
			t.Fatalf("IP 回源不应改 Description: %s", inst.Description)
		}
		if len(inst.SourceIPs) != 1 {
			t.Fatalf("期望 SourceIPs 1 项,实际 %d", len(inst.SourceIPs))
		}
	})

	t.Run("空详情不覆盖已有源站", func(t *testing.T) {
		detail := &waf.DomainsPartInfo{}
		inst := &types.WAFInstance{SourceIPs: []string{"1.2.3.4"}}
		mergeSaaSSources(detail, inst)
		if len(inst.SourceIPs) != 1 || inst.SourceIPs[0] != "1.2.3.4" {
			t.Fatalf("空详情不应清空源站: %v", inst.SourceIPs)
		}
	})

	t.Run("Mode=1 映射 block", func(t *testing.T) {
		detail := &waf.DomainsPartInfo{Mode: func() *uint64 { v := uint64(1); return &v }()}
		inst := &types.WAFInstance{}
		mergeSaaSSources(detail, inst)
		if inst.ProtectionMode != "block" {
			t.Fatalf("Mode=1 应映射 block,实际 %q", inst.ProtectionMode)
		}
	})

	t.Run("Mode=0 映射 observe", func(t *testing.T) {
		detail := &waf.DomainsPartInfo{Mode: func() *uint64 { v := uint64(0); return &v }()}
		inst := &types.WAFInstance{}
		mergeSaaSSources(detail, inst)
		if inst.ProtectionMode != "observe" {
			t.Fatalf("Mode=0 应映射 observe,实际 %q", inst.ProtectionMode)
		}
	})
}

func TestConvertTencentRules(t *testing.T) {
	t.Run("归一门神规则字段", func(t *testing.T) {
		rules := []*waf.Rule{
			{
				Description: strPtr("SQL注入检测"),
				Type:        strPtr("sqli"),
				Level:       strPtr("high"),
				Status:      func() *uint64 { v := uint64(1); return &v }(),
			},
			{
				Description: strPtr("XSS检测"),
				Type:        strPtr("xss"),
				Level:       strPtr("medium"),
				Status:      func() *uint64 { v := uint64(0); return &v }(),
			},
			nil,
		}
		out := convertTencentRules(rules)
		if len(out) != 2 {
			t.Fatalf("期望 2 条(跳过 nil),实际 %d", len(out))
		}
		if out[0].Name != "SQL注入检测" || out[0].Level != "high" || out[0].Status != "enabled" {
			t.Fatalf("第 1 条映射错误: %+v", out[0])
		}
		if out[1].Status != "disabled" {
			t.Fatalf("Status=0 应映射 disabled,实际 %s", out[1].Status)
		}
	})
}

func TestMergeCLBSources(t *testing.T) {
	t.Run("关联 LB 写入 Description", func(t *testing.T) {
		detail := &waf.ClbDomainsInfo{
			LoadBalancerSet: []*waf.LoadBalancerPackageNew{
				{
					LoadBalancerId:   strPtr("lb-abc"),
					LoadBalancerName: strPtr("my-lb"),
					ListenerId:       strPtr("lbl-xyz"),
					ListenerName:     strPtr("https-443"),
					Protocol:         strPtr("https"),
				},
			},
		}
		inst := &types.WAFInstance{Description: "WAF防护域名"}
		mergeCLBSources(detail, &waf.DomainInfo{}, inst)

		if len(inst.SourceIPs) != 0 {
			t.Fatalf("CLB 型 SourceIPs 应留空,实际 %v", inst.SourceIPs)
		}
		if inst.Description == "WAF防护域名" {
			t.Fatalf("CLB 关联 LB 未写入 Description")
		}
		if inst.Description != "WAF防护域名(CLB) clb=my-lb(lb-abc) listener=https-443 protocol=https" {
			t.Fatalf("Description 拼装错误: %s", inst.Description)
		}
	})

	t.Run("详情无 LB 时回退列表 LB", func(t *testing.T) {
		detail := &waf.ClbDomainsInfo{}
		list := &waf.DomainInfo{
			LoadBalancerSet: []*waf.LoadBalancerPackageNew{
				{LoadBalancerId: strPtr("lb-fallback"), LoadBalancerName: strPtr("fallback-lb")},
			},
		}
		inst := &types.WAFInstance{Description: "WAF防护域名"}
		mergeCLBSources(detail, list, inst)

		if inst.Description != "WAF防护域名(CLB) clb=fallback-lb(lb-fallback)" {
			t.Fatalf("回退 LB 拼装错误: %s", inst.Description)
		}
	})

	t.Run("无任何 LB 不改 Description", func(t *testing.T) {
		detail := &waf.ClbDomainsInfo{}
		inst := &types.WAFInstance{Description: "WAF防护域名"}
		mergeCLBSources(detail, &waf.DomainInfo{}, inst)
		if inst.Description != "WAF防护域名" {
			t.Fatalf("无 LB 不应改 Description: %s", inst.Description)
		}
	})
}
