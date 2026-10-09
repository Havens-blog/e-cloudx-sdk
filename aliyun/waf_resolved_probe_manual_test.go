// Package aliyun_test WAF 源站解析修复实盘验证(manual probe,只读)。
//
// 目的:核验「按站点缓存实例 ID」修复后,跨站点域名能正确解析源站。
// 走公开适配器 ListInstancesWithFilter(ap-southeast-1 国际站,54 域名),
// 打印源站解析结果;修复前应按站点错位实例报 Waf.Instance.ValidFaild。
//
// Hard Rule:只读凭证,只打印实例颗要到测试日志,不动生产表。
package aliyun_test

import (
	"context"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/aliyun"
	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
)

// TestLiveAliyunWAFResolved 打印 ap-southeast-1(国际站)与 us-east-1(中国站)解析结果。
func TestLiveAliyunWAFResolved(t *testing.T) {
	setupAliyunProbeEnv(t)
	ak, sk, _ := loadAliyunCreds(t)
	if ak == "" {
		t.Skip("无 aliyun 凭证,跳过")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	adapter := aliyun.NewWAFAdapter(ak, sk, "cn-hangzhou", elog.DefaultLogger)
	for _, site := range []struct{ region, label string }{
		{"ap-southeast-1", "国际站"},
		{"us-east-1", "中国站(cn-hangzhou 映射)"},
	} {
		// 只取首页(PageSize=3 仍会分页到 TotalCount,但先小测两页看源站是否非空)
		instances, err := adapter.ListInstancesWithFilter(ctx, site.region,
			&types.WAFInstanceFilter{PageSize: 3})
		if err != nil {
			t.Logf("[%s] ListInstancesWithFilter err=%v", site.label, err)
			continue
		}
		filled := 0
		for _, ins := range instances {
			if len(ins.SourceIPs) > 0 {
				filled++
				if filled <= 3 {
					t.Logf("[%s] %s → sources=%v", site.label, ins.InstanceName, ins.SourceIPs)
				}
			}
		}
		t.Logf("[%s] 总 %d 个,源站非空 %d 个", site.label, len(instances), filled)
	}
}