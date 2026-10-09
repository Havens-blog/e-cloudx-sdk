package aliyun

import (
	"testing"
	"time"
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
