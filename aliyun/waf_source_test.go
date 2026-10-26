package aliyun

import (
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
)

func TestConvertResourceToInstanceCreationTime(t *testing.T) {
	t.Run("GmtCreate 毫秒时间戳映射为 RFC3339", func(t *testing.T) {
		// 2024-01-02 03:04:05 UTC = 1704164645000 毫秒
		res := defenseResource{
			Resource:  "example.com",
			GmtCreate: 1704164645000,
			Detail:    []byte("{}"),
		}
		a := &WAFAdapter{}
		inst := a.convertResourceToInstance(res, "cn-hangzhou")

		want := time.UnixMilli(1704164645000).Format("2006-01-02T15:04:05Z")
		if inst.CreationTime != want {
			t.Fatalf("CreationTime 错误: got %q want %q", inst.CreationTime, want)
		}
	})

	t.Run("GmtCreate=0 时留空", func(t *testing.T) {
		res := defenseResource{
			Resource:  "example.com",
			GmtCreate: 0,
			Detail:    []byte("{}"),
		}
		a := &WAFAdapter{}
		inst := a.convertResourceToInstance(res, "cn-hangzhou")

		if inst.CreationTime != "" {
			t.Fatalf("GmtCreate=0 时 CreationTime 应留空,实际 %q", inst.CreationTime)
		}
	})
}

func TestConvertResourceToInstanceTagsUnavailable(t *testing.T) {
	// 阿里云 WAF 3.0 列表(DescribeDefenseResources)/详情(DescribeDomainDetail)均不含
	// 域名级标签字段,转换层保持「非 nil 空 map」契约(理由见 waf.go convertResourceToInstance
	// 处注释:标签只挂在 WAF 实例层,与按防护对象同步的粒度不一致,不做 N+1)。
	res := defenseResource{
		Resource: "example.com",
		Detail:   []byte(`{"product":"waf","domain":"example.com"}`),
	}
	a := &WAFAdapter{}
	inst := a.convertResourceToInstance(res, "cn-hangzhou")

	if inst.Tags == nil {
		t.Fatalf("Tags 应保持非 nil 空 map,实际 nil")
	}
	if len(inst.Tags) != 0 {
		t.Fatalf("Tags 应保持空 map,实际 %v", inst.Tags)
	}
}

func TestMergeDomainDetailEnrich(t *testing.T) {
	t.Run("AllBackends 优先并回填 Cname/资源组", func(t *testing.T) {
		inst := &types.WAFInstance{InstanceID: "example.com"}
		mergeDomainDetailEnrich(inst, &describeDomainDetailResponse{
			Cname: "abc.waf.example.com",
			Redirect: &redirectDetail{
				AllBackends: []string{"1.2.3.4:443"},
				BackendList: []string{"5.6.7.8:443"},
			},
			ResourceManagerResourceGroupId: "rg-abc123",
		})
		if len(inst.SourceIPs) != 1 || inst.SourceIPs[0] != "1.2.3.4:443" {
			t.Fatalf("SourceIPs 应取 AllBackends: %v", inst.SourceIPs)
		}
		if inst.Cname != "abc.waf.example.com" {
			t.Fatalf("Cname 未回填: %q", inst.Cname)
		}
		if inst.ResourceGroupID != "rg-abc123" {
			t.Fatalf("ResourceGroupID 未回填: %q", inst.ResourceGroupID)
		}
	})

	t.Run("Backends 逐条回退", func(t *testing.T) {
		inst := &types.WAFInstance{}
		mergeDomainDetailEnrich(inst, &describeDomainDetailResponse{
			Redirect: &redirectDetail{Backends: []backendItem{{Backend: "9.9.9.9:80"}, {Backend: ""}}},
		})
		if len(inst.SourceIPs) != 1 || inst.SourceIPs[0] != "9.9.9.9:80" {
			t.Fatalf("Backends 回退失败: %v", inst.SourceIPs)
		}
	})

	t.Run("nil/空详情保持原值", func(t *testing.T) {
		inst := &types.WAFInstance{Cname: "keep", ResourceGroupID: "rg-old"}
		mergeDomainDetailEnrich(inst, nil)
		if inst.Cname != "keep" || inst.ResourceGroupID != "rg-old" {
			t.Fatalf("nil 详情不应改变原值: %+v", inst)
		}
		mergeDomainDetailEnrich(inst, &describeDomainDetailResponse{})
		if inst.Cname != "keep" || inst.ResourceGroupID != "rg-old" {
			t.Fatalf("空详情不应改变原值: %+v", inst)
		}
	})
}

func TestWafSite(t *testing.T) {
	cases := []struct {
		name   string
		region string
		want   string
	}{
		{"国际站", "ap-southeast-1", "ap-southeast-1"},
		{"空地域归国际站", "", "ap-southeast-1"},
		{"中国站 cn-hangzhou", "cn-hangzhou", "cn-hangzhou"},
		{"中国站 cn-shenzhen", "cn-shenzhen", "cn-hangzhou"},
		{"海外资产地域归中国站", "us-east-1", "cn-hangzhou"},
		{"海外资产地域归中国站 eu", "eu-west-1", "cn-hangzhou"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wafSite(c.region); got != c.want {
				t.Fatalf("wafSite(%q) = %q, want %q", c.region, got, c.want)
			}
		})
	}
}
