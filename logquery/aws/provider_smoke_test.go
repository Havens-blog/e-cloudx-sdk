package aws

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
//	LOGQUERY_SMOKE_AWS_AK / LOGQUERY_SMOKE_AWS_SK 设置为真实 AWS 凭证后运行。
func TestLiveSearchSmoke(t *testing.T) {
	ak, sk := os.Getenv("LOGQUERY_SMOKE_AWS_AK"), os.Getenv("LOGQUERY_SMOKE_AWS_SK")
	if ak == "" || sk == "" {
		t.Skip("LOGQUERY_SMOKE_AWS_AK/SK not set; live smoke skipped")
	}
	acc := &domain.CloudAccount{
		ID: 3, Name: "smoke", Provider: domain.CloudProviderAWS,
		AccessKeyID: ak, AccessKeySecret: sk,
	}
	for _, logType := range []logquery.LogType{logquery.LogTypeWAF, logquery.LogTypeCDN} {
		t.Run(string(logType), func(t *testing.T) {
			p, err := newProvider(logType)(acc)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			sources, err := p.ListLogSources(ctx, acc)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s sources: %d", logType, len(sources))
			for i, s := range sources {
				if i >= 5 {
					break
				}
				t.Logf("  source: %+v", s)
			}
			now := time.Now().UnixMilli()
			entries, err := p.Search(ctx, acc, logquery.SearchParams{
				StartTime: now - 6 * 3600_000, EndTime: now, Limit: 10,
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
			if len(entries) > 0 && entries[0].GetTimestamp() <= 0 {
				t.Error("timestamp not normalized")
			}
		})
	}
}
