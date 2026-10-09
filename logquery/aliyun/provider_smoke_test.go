package aliyun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
)

// TestLiveSearchSmoke 真实环境冒烟(env 门控,默认跳过):
//
//	LOGQUERY_SMOKE_AK / LOGQUERY_SMOKE_SK 设置为真实阿里云凭证后运行。
//
// 验证 client 构造 -> catalog 查询 -> SLS GetLogs -> mapper 全链路
// (三种日志类型全跑,单类型单 provider 实例)。
func TestLiveSearchSmoke(t *testing.T) {
	ak, sk := os.Getenv("LOGQUERY_SMOKE_AK"), os.Getenv("LOGQUERY_SMOKE_SK")
	if ak == "" || sk == "" {
		t.Skip("LOGQUERY_SMOKE_AK/SK not set; live smoke skipped")
	}
	acc := &domain.CloudAccount{
		ID: 1, Name: "smoke", Provider: domain.CloudProviderAliyun,
		AccessKeyID: ak, AccessKeySecret: sk,
	}
	now := time.Now().UnixMilli()
	for _, logType := range []logquery.LogType{logquery.LogTypeSLB, logquery.LogTypeWAF, logquery.LogTypeCDN} {
		t.Run(string(logType), func(t *testing.T) {
			p, err := newProvider(logType)(acc)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := p.Search(context.Background(), acc, logquery.SearchParams{
				StartTime: now - 3600_000, EndTime: now, Limit: 10,
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s search returned %d entries", logType, len(entries))
			for i, e := range entries {
				if i >= 3 {
					break
				}
				t.Logf("  [%d] ts=%d meta=%+v", i, e.GetTimestamp(), e.GetMeta())
			}
			if len(entries) > 0 {
				if entries[0].GetTimestamp() <= 0 {
					t.Error("timestamp not normalized")
				}
			}
		})
	}
}
