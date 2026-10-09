package huawei

import (
	"context"
	"os"
	"strconv"
	"time"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	ltsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/lts/v2/model"
)

func strPtr(s string) *string { return &s }

// TestLiveAggregateProbe 探测 LTS 管道 SQL(is_analysis_query,内测能力)
// 对本账号是否可用:LOGQUERY_SMOKE_HW=1 时运行。输出 SQL 原始响应供接入决策。
func TestLiveAggregateProbe(t *testing.T) {
	if os.Getenv("LOGQUERY_SMOKE_HW") != "1" {
		t.Skip("set LOGQUERY_SMOKE_HW=1 to run live probe")
	}
	ak := os.Getenv("HUAWEI_ACCESS_KEY")
	sk := os.Getenv("HUAWEI_SECRET_KEY")
	if ak == "" || sk == "" {
		t.Fatal("HUAWEI_ACCESS_KEY/HUAWEI_SECRET_KEY required")
	}
	acc := &domain.CloudAccount{ID: 1, Name: "probe", Provider: domain.CloudProviderHuawei,
		AccessKeyID: ak, AccessKeySecret: sk}
	p, err := newProvider(logquery.LogTypeWAF)(acc)
	if err != nil {
		t.Fatal(err)
	}
	hp := p.(*provider)
	ids, err := hp.groupIDs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("groups: %+v", ids)
	// 取 WAF 组第一个流
	streams, err := hp.lts().ListLogStreams(&ltsmodel.ListLogStreamsRequest{LogGroupName: strPtr("hwyun-waf-logs")})
	if err != nil {
		t.Fatal(err)
	}
	list := derefStreams(streams.LogStreams)
	if len(list) == 0 {
		t.Fatal("no streams")
	}
	s := list[0]
	t.Logf("probe stream: %s id=%s", s.LogStreamName, s.LogStreamId)

	probes := map[string]string{
		"count":       "* | select count(1) as c",
		"count_limit": "* | select count(1) as c",
	}
	for name, sql := range probes {
		body := &ltsmodel.QueryLtsLogParams{
			StartTime:       "1788400000000",
			EndTime:         "1788486000000",
			Query:           strPtr(sql),
			IsAnalysisQuery: boolPtr(true),
		}
		if name == "count_limit" {
			l := int32(10)
			body.Limit = &l
		}
		resp, err := hp.lts().ListLogs(&ltsmodel.ListLogsRequest{
			LogGroupId: ids["hwyun-waf-logs"], LogStreamId: s.LogStreamId, Body: body,
		})
		if err != nil {
			t.Logf("probe[%s] REJECTED: %v", name, err)
			continue
		}
		if resp.AnalysisLogs != nil && len(*resp.AnalysisLogs) > 0 {
			for _, row := range *resp.AnalysisLogs {
				t.Logf("probe[%s] row: %+v", name, row)
			}
		} else {
			t.Logf("probe[%s] empty analysisLogs (logs=%p)", name, resp.Logs)
		}
	}
}

// TestLiveAggregateMethod 活体调用 Aggregate 方法定位 total=0(调试用,勿常开)。
func TestLiveAggregateMethod(t *testing.T) {
	if os.Getenv("LOGQUERY_SMOKE_HW") != "1" {
		t.Skip("set LOGQUERY_SMOKE_HW=1 to run live probe")
	}
	ak := os.Getenv("HUAWEI_ACCESS_KEY")
	sk := os.Getenv("HUAWEI_SECRET_KEY")
	if ak == "" || sk == "" {
		t.Fatal("creds required")
	}
	acc := &domain.CloudAccount{ID: 1, Name: "probe", Provider: domain.CloudProviderHuawei,
		AccessKeyID: ak, AccessKeySecret: sk}
	p, err := newProvider(logquery.LogTypeWAF)(acc)
	if err != nil {
		t.Fatal(err)
	}
	hp := p.(*provider)
	now := time.Now().UnixMilli()
	res, err := hp.Aggregate(context.Background(), acc, logquery.AggregateParams{
		StartTime: now - 24*3600_000, EndTime: now, BucketSec: 900,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("total=%d buckets=%d topn=%d", res.Total, len(res.Buckets), len(res.TopN))
	for i, b := range res.Buckets {
		if i < 3 {
			t.Logf("bucket %d: ts=%d c=%d", i, b.Timestamp, b.Count)
		}
	}
}

// TestLiveAggregateDebug 复现 aggregateStream 完整调用链定位丢数据点。
func TestLiveAggregateDebug(t *testing.T) {
	if os.Getenv("LOGQUERY_SMOKE_HW") != "1" {
		t.Skip("set LOGQUERY_SMOKE_HW=1 to run live probe")
	}
	ak := os.Getenv("HUAWEI_ACCESS_KEY")
	sk := os.Getenv("HUAWEI_SECRET_KEY")
	if ak == "" || sk == "" {
		t.Fatal("creds required")
	}
	acc := &domain.CloudAccount{ID: 1, Name: "probe", Provider: domain.CloudProviderHuawei,
		AccessKeyID: ak, AccessKeySecret: sk}
	p, err := newProvider(logquery.LogTypeWAF)(acc)
	if err != nil {
		t.Fatal(err)
	}
	hp := p.(*provider)
	ctx := context.Background()
	ids, err := hp.groupIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	streams, err := hp.lts().ListLogStreams(&ltsmodel.ListLogStreamsRequest{LogGroupName: strPtr("hwyun-waf-logs")})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range derefStreams(streams.LogStreams) {
		kind, ok := classify("hwyun-waf-logs", s.LogStreamName)
		t.Logf("stream=%s kind=%s ok=%v id=%q", s.LogStreamName, kind, ok, s.LogStreamId)
		if !ok {
			continue
		}
		now := time.Now().UnixMilli()
		sql := buildAggregateBucketSQL("", 900)
		t.Logf("sql=%q window=[%d,%d]", sql, now-24*3600_000, now)
		body := &ltsmodel.QueryLtsLogParams{
			StartTime:       strconv.FormatInt(now-24*3600_000, 10),
			EndTime:         strconv.FormatInt(now, 10),
			Query:           strPtr(sql),
			IsAnalysisQuery: boolPtr(true),
			Limit:           strPtr32(200),
		}
		resp, err := hp.lts().ListLogs(&ltsmodel.ListLogsRequest{
			LogGroupId: ids["hwyun-waf-logs"], LogStreamId: s.LogStreamId, Body: body,
		})
		if err != nil {
			t.Logf("REJECTED: %v", err)
			continue
		}
		t.Logf("analysisLogs=%v logs=%v", resp.AnalysisLogs != nil, resp.Logs != nil)
		if resp.AnalysisLogs != nil {
			for i, row := range *resp.AnalysisLogs {
				if i < 3 {
					t.Logf("row: %T %+v", row, row)
				}
			}
		}
		if resp.Logs != nil {
			t.Logf("normal-mode logs: %d rows", len(*resp.Logs))
		}
	}
}

func strPtr32(v int32) *int32 { return &v }

// TestLiveAggregateStreamDirect 直调 aggregateStream 定位(调试用)。
func TestLiveAggregateStreamDirect(t *testing.T) {
	if os.Getenv("LOGQUERY_SMOKE_HW") != "1" {
		t.Skip("set LOGQUERY_SMOKE_HW=1 to run live probe")
	}
	ak := os.Getenv("HUAWEI_ACCESS_KEY")
	sk := os.Getenv("HUAWEI_SECRET_KEY")
	if ak == "" || sk == "" {
		t.Fatal("creds required")
	}
	acc := &domain.CloudAccount{ID: 1, Name: "probe", Provider: domain.CloudProviderHuawei,
		AccessKeyID: ak, AccessKeySecret: sk}
	p, err := newProvider(logquery.LogTypeWAF)(acc)
	if err != nil {
		t.Fatal(err)
	}
	hp := p.(*provider)
	ctx := context.Background()
	ids, err := hp.groupIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	streams, err := hp.lts().ListLogStreams(&ltsmodel.ListLogStreamsRequest{LogGroupName: strPtr("hwyun-waf-logs")})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range derefStreams(streams.LogStreams) {
		kind, ok := classify("hwyun-waf-logs", s.LogStreamName)
		if !ok || kind != kindWAFAttack {
			continue
		}
		now := time.Now().UnixMilli()
		res := hp.aggregateStream(ids["hwyun-waf-logs"], kind, s, logquery.AggregateParams{
			StartTime: now - 24*3600_000, EndTime: now, BucketSec: 900,
		}, "")
		t.Logf("direct: total=%d buckets=%d topn=%d", res.Total, len(res.Buckets), len(res.TopN))
	}
}
