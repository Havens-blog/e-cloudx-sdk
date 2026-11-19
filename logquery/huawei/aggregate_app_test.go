package huawei

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/Havens-blog/e-cloudx-sdk/logquery"
	ltsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/lts/v2/model"
)

// fakeLTS 用内存流清单替代真实 LTS,让聚合的「选流 + 分桶求和」可离线单测。
// 行值刻意用 json.Number:LTS analysisLogs 就是这种类型(jsonInt64 的既有坑)。
type fakeLTS struct {
	groups  map[string]string // 组名 -> 组ID
	streams map[string][]string
	counts  map[string]int64            // 流名 -> 窗口内条数(拆两个桶)
	topn    map[string]map[string]int64 // 流名 -> 维度值 -> 条数

	mu      sync.Mutex
	queried []string // 真正跑了 SQL 的流名(断言选流口径)
}

func (f *fakeLTS) ListLogGroups(*ltsmodel.ListLogGroupsRequest) (*ltsmodel.ListLogGroupsResponse, error) {
	names := make([]string, 0, len(f.groups))
	for n := range f.groups {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]ltsmodel.LogGroup, 0, len(names))
	for _, n := range names {
		out = append(out, ltsmodel.LogGroup{LogGroupName: n, LogGroupId: f.groups[n]})
	}
	return &ltsmodel.ListLogGroupsResponse{LogGroups: &out}, nil
}

func (f *fakeLTS) ListLogStreams(req *ltsmodel.ListLogStreamsRequest) (*ltsmodel.ListLogStreamsResponse, error) {
	if req.LogGroupName == nil {
		return nil, fmt.Errorf("fake lts: missing log group name")
	}
	out := make([]ltsmodel.ListLogStreamsResponseBody1LogStreams, 0, len(f.streams[*req.LogGroupName]))
	for _, name := range f.streams[*req.LogGroupName] {
		out = append(out, ltsmodel.ListLogStreamsResponseBody1LogStreams{
			LogStreamId:   name, // 流名唯一即可,ListLogs 按它取条数
			LogStreamName: name,
		})
	}
	return &ltsmodel.ListLogStreamsResponse{LogStreams: &out}, nil
}

func (f *fakeLTS) ListLogs(req *ltsmodel.ListLogsRequest) (*ltsmodel.ListLogsResponse, error) {
	if req.Body == nil || req.Body.Query == nil {
		return nil, fmt.Errorf("fake lts: missing query")
	}
	sql := *req.Body.Query
	stream := req.LogStreamId
	rows := make([]interface{}, 0, 2)
	switch {
	case strings.Contains(sql, "group by t"):
		f.mu.Lock()
		f.queried = append(f.queried, stream)
		f.mu.Unlock()
		total := f.counts[stream]
		if total > 0 {
			rows = append(rows,
				map[string]any{"t": json.Number("0"), "c": json.Number(fmt.Sprintf("%d", total/2))},
				map[string]any{"t": json.Number("60000"), "c": json.Number(fmt.Sprintf("%d", total-total/2))},
			)
		}
	case strings.Contains(sql, "group by k"):
		for k, n := range f.topn[stream] {
			rows = append(rows, map[string]any{
				"k": k, "n": json.Number(fmt.Sprintf("%d", n)), "v": json.Number(fmt.Sprintf("%d", n)),
			})
		}
	}
	return &ltsmodel.ListLogsResponse{AnalysisLogs: &rows}, nil
}

func (f *fakeLTS) queriedStreams() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]string(nil), f.queried...)
	sort.Strings(out)
	return out
}

func newAggProvider(t *testing.T, logType logquery.LogType) *provider {
	t.Helper()
	creator := newProvider(logType)
	p, err := creator(&domain.CloudAccount{
		ID:              7,
		Name:            "unit",
		Provider:        domain.CloudProviderHuawei,
		AccessKeyID:     "ak",
		AccessKeySecret: "sk",
	})
	if err != nil {
		t.Fatalf("newProvider(%s): %v", logType, err)
	}
	return p.(*provider)
}

func aggParams() logquery.AggregateParams {
	return logquery.AggregateParams{StartTime: 1_700_000_000_000, EndTime: 1_700_003_600_000, BucketSec: 60}
}

// 应用日志的候选流与 ListLogSources/Search 同源(appGroups),不能混入 WAF/ELB 组。
func TestAggregateAppTargetsAppStreams(t *testing.T) {
	fake := &fakeLTS{
		groups: map[string]string{
			"app-order":      "g-app-order",
			"app-payment":    "g-app-payment",
			"dev-elb":        "g-dev-elb",
			"hwyun-waf-logs": "g-waf",
		},
		streams: map[string][]string{
			"app-order":      {"app-order-access"},
			"app-payment":    {"app-payment-business"},
			"dev-elb":        {"dayu-access-gateway"},
			"hwyun-waf-logs": {"hwyun-waf-logs-access"},
		},
		counts: map[string]int64{
			"app-order-access":      100,
			"app-payment-business":  50,
			"dayu-access-gateway":   999,
			"hwyun-waf-logs-access": 888,
		},
	}
	p := newAggProvider(t, logquery.LogTypeApp)
	res, err := p.aggregateWith(context.Background(), fake, aggParams())
	if err != nil {
		t.Fatalf("aggregateWith: %v", err)
	}
	if res.Total != 150 {
		t.Fatalf("Total = %d, want 150(只累计 app 流)", res.Total)
	}
	if got := fake.queriedStreams(); len(got) != 2 || got[0] != "app-order-access" || got[1] != "app-payment-business" {
		t.Fatalf("queried streams = %v, want 两个 app 流", got)
	}
	if len(res.Buckets) != 4 {
		t.Fatalf("buckets = %d, want 4(两流各两桶)", len(res.Buckets))
	}
	for i := 1; i < len(res.Buckets); i++ {
		if res.Buckets[i-1].Timestamp > res.Buckets[i].Timestamp {
			t.Fatalf("buckets 未按时间升序: %v", res.Buckets)
		}
	}
}

// WAF/SLB 的选流口径不因 app 分支而改变。
func TestAggregateWAFStillTargetsWafGroupsOnly(t *testing.T) {
	fake := &fakeLTS{
		groups: map[string]string{
			"app-order":      "g-app-order",
			"dev-elb":        "g-dev-elb",
			"hwyun-waf-logs": "g-waf",
		},
		streams: map[string][]string{
			"app-order":      {"app-order-access"},
			"dev-elb":        {"dayu-access-gateway"},
			"hwyun-waf-logs": {"hwyun-waf-logs-access"},
		},
		counts: map[string]int64{
			"app-order-access":      100,
			"dayu-access-gateway":   999,
			"hwyun-waf-logs-access": 888,
		},
	}
	p := newAggProvider(t, logquery.LogTypeWAF)
	res, err := p.aggregateWith(context.Background(), fake, aggParams())
	if err != nil {
		t.Fatalf("aggregateWith: %v", err)
	}
	if res.Total != 888 {
		t.Fatalf("Total = %d, want 888(只累计 WAF 流)", res.Total)
	}
	if got := fake.queriedStreams(); len(got) != 1 || got[0] != "hwyun-waf-logs-access" {
		t.Fatalf("queried streams = %v, want 仅 WAF 流", got)
	}
}

// app 没有可下推的结构化列:给字段筛选必须显式失败,而不是返回「未筛选的全窗数」。
func TestAggregateAppRejectsFieldFilters(t *testing.T) {
	fake := &fakeLTS{
		groups:  map[string]string{"app-order": "g-app-order"},
		streams: map[string][]string{"app-order": {"app-order-access"}},
		counts:  map[string]int64{"app-order-access": 100},
	}
	params := aggParams()
	params.Filters = []logquery.FieldFilter{{Field: "status", Op: "eq", Value: "500"}}
	p := newAggProvider(t, logquery.LogTypeApp)
	if _, err := p.aggregateWith(context.Background(), fake, params); err == nil {
		t.Fatal("字段筛选应报错(app 不可下推),实际返回 nil error")
	} else if !strings.Contains(err.Error(), "字段筛选") {
		t.Fatalf("错误信息应说明字段筛选不可下推, got %v", err)
	}
}

// resources 过滤同时接受 "组/流" 与裸流名;未命中则无目标(Total 0 且不报错)。
func TestAggregateAppResourceFilter(t *testing.T) {
	fake := &fakeLTS{
		groups:  map[string]string{"app-order": "g-app-order", "app-payment": "g-app-payment"},
		streams: map[string][]string{"app-order": {"app-order-access"}, "app-payment": {"app-payment-business"}},
		counts:  map[string]int64{"app-order-access": 100, "app-payment-business": 50},
	}
	p := newAggProvider(t, logquery.LogTypeApp)

	params := aggParams()
	params.Resources = []string{"app-order/app-order-access"}
	res, err := p.aggregateWith(context.Background(), fake, params)
	if err != nil {
		t.Fatalf("aggregateWith: %v", err)
	}
	if res.Total != 100 {
		t.Fatalf("Total = %d, want 100(仅点名流)", res.Total)
	}

	params.Resources = []string{"zzz-no-such-stream"}
	res, err = p.aggregateWith(context.Background(), fake, params)
	if err != nil {
		t.Fatalf("未命中也应正常返回: %v", err)
	}
	if res.Total != 0 || len(res.Buckets) != 0 {
		t.Fatalf("未命中应为空, got total=%d buckets=%d", res.Total, len(res.Buckets))
	}
}

// app 无 SQL 维度:分桶照常返回,TopN 显式跳过并给出原因(不退化成静默空)。
func TestAggregateAppDimensionSkipsTopN(t *testing.T) {
	fake := &fakeLTS{
		groups:  map[string]string{"app-order": "g-app-order"},
		streams: map[string][]string{"app-order": {"app-order-access"}},
		counts:  map[string]int64{"app-order-access": 100},
	}
	params := aggParams()
	params.Dimension = "host"
	p := newAggProvider(t, logquery.LogTypeApp)
	res, err := p.aggregateWith(context.Background(), fake, params)
	if err != nil {
		t.Fatalf("aggregateWith: %v", err)
	}
	if res.Total != 100 || len(res.Buckets) == 0 {
		t.Fatalf("分桶应照常返回, got total=%d buckets=%d", res.Total, len(res.Buckets))
	}
	if len(res.TopN) != 0 {
		t.Fatalf("app 不应产出 TopN, got %v", res.TopN)
	}
	if !strings.Contains(res.TopNSkipReason, "host") {
		t.Fatalf("TopNSkipReason 应说明维度不支持, got %q", res.TopNSkipReason)
	}
}

func TestWantStream(t *testing.T) {
	cases := []struct {
		name      string
		resources []string
		group     string
		stream    string
		want      bool
	}{
		{"空表示全选", nil, "app-order", "app-order-access", true},
		{"组/流", []string{"app-order/app-order-access"}, "app-order", "app-order-access", true},
		{"裸流名", []string{"app-order-access"}, "app-order", "app-order-access", true},
		{"未命中", []string{"other/x"}, "app-order", "app-order-access", false},
		{"前缀相同但不同流", []string{"app-order/app-order"}, "app-order", "app-order-access", false},
	}
	for _, c := range cases {
		if got := wantStream(c.resources, c.group, c.stream); got != c.want {
			t.Fatalf("%s: wantStream = %v, want %v", c.name, got, c.want)
		}
	}
}
