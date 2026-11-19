package huawei

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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

	// failStreams 模拟某些流的 SQL 一直失败(限流/权限),用于验证失败可见性
	failStreams map[string]bool
	// failGroups 模拟组级 ListLogStreams 一直失败(限流耗尽/权限),用于验证枚举失败可见性
	failGroups map[string]bool
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
	if f.failGroups[*req.LogGroupName] {
		return nil, fmt.Errorf("fake lts: list streams throttled (APIGW.0308) for %s", *req.LogGroupName)
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
	if f.failStreams[stream] {
		return nil, fmt.Errorf("fake lts: throttled (APIGW.0308) for %s", stream)
	}
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

// 应用日志是多流扇出:单流失败不能让 Total 静默偏小,必须把错误带回给调用方
// (cam 侧会写进 AggregateSourceOutcome.Error),同时保留已成功的分桶。
func TestAggregateAppSurfacesStreamFailure(t *testing.T) {
	fake := &fakeLTS{
		groups: map[string]string{
			"app-order":   "g-app-order",
			"app-payment": "g-app-payment",
		},
		streams: map[string][]string{
			"app-order":   {"app-order-access"},
			"app-payment": {"app-payment-business"},
		},
		counts:      map[string]int64{"app-order-access": 100, "app-payment-business": 50},
		failStreams: map[string]bool{"app-payment-business": true},
	}
	p := newAggProvider(t, logquery.LogTypeApp)
	res, err := p.aggregateWith(context.Background(), fake, aggParams())
	if err == nil {
		t.Fatal("存在失败流时必须返回错误(否则 Total 静默偏小)")
	}
	if !strings.Contains(err.Error(), "个流") {
		t.Fatalf("错误文案应说明失败流数量, got %v", err)
	}
	if res == nil {
		t.Fatal("应返回已合并的部分结果,便于前端仍能画图")
	}
	if res.Total != 100 {
		t.Fatalf("部分结果只应包含成功的流, got total=%d want 100", res.Total)
	}
}

// WAF/ELB 路径维持既有行为:单流 SQL 失败只告警、该流不计,整体不报错。
func TestAggregateWAFKeepsSingleStreamIsolation(t *testing.T) {
	fake := &fakeLTS{
		groups:      map[string]string{"hwyun-waf-logs": "g-waf"},
		streams:     map[string][]string{"hwyun-waf-logs": {"hwyun-waf-logs-attack"}},
		counts:      map[string]int64{"hwyun-waf-logs-attack": 888},
		failStreams: map[string]bool{"hwyun-waf-logs-attack": true},
	}
	p := newAggProvider(t, logquery.LogTypeWAF)
	res, err := p.aggregateWith(context.Background(), fake, aggParams())
	if err != nil {
		t.Fatalf("WAF 路径不应因单流失败而报错: %v", err)
	}
	if res == nil || res.Total != 0 {
		t.Fatalf("失败的流不计入, got %+v", res)
	}
}

// 源码级守卫:app 聚合的 ListLogs 必须走 withLTSRetry(appLTSGate)。
// 华为 LTS 网关限流 APIGW.0308 是 10 QPS/用户,而 app 是几十上百条流的 8 并发扇出,
// 直接打必然 429;失败流若只 Warn 就会让 Total 静默偏小。
func TestAppAggregateGoesThroughLTSGate(t *testing.T) {
	raw, err := os.ReadFile("provider.go")
	if err != nil {
		t.Fatalf("read provider.go: %v", err)
	}
	src := string(raw)
	if !strings.Contains(src, `p.withLTSRetry(ctx, "aggregate/"+groupID, call)`) {
		t.Fatal("app 聚合的 ListLogs 必须包在 p.withLTSRetry 里(限流重试 + 闸门)")
	}
	if strings.Contains(src, "aggregateStreamWith(api,") {
		t.Fatal("aggregateStreamWith 调用缺少 ctx:无法走闸门/取消")
	}
}

// 枚举失败的组不会产生 target:若不把该错误带出去,聚合会以 nil error 返回偏小的
// Total(该组日志既不在分桶、也不在单流错误里)。两种失败必须同时可见。
func TestAggregateAppSurfacesEnumerationFailure(t *testing.T) {
	hp := newAggProvider(t, logquery.LogTypeApp)
	fake := &fakeLTS{
		groups:     map[string]string{"app-order": "g1", "app-payment": "g2", "dev-elb": "g3"},
		streams:    map[string][]string{"app-order": {"app-order-access"}},
		counts:     map[string]int64{"app-order-access": 100},
		failGroups: map[string]bool{"app-payment": true},
	}
	fake.failGroups = map[string]bool{"app-payment": true}

	res, err := hp.aggregateWith(context.Background(), fake, aggParams())
	if err == nil {
		t.Fatal("组枚举失败必须让聚合返回错误,否则 Total 静默偏小")
	}
	if !strings.Contains(err.Error(), "个组枚举失败") {
		t.Fatalf("错误文案应区分组枚举失败, got %v", err)
	}
	if res == nil || res.Total == 0 {
		t.Fatalf("成功组的结果仍必须返回, got %+v", res)
	}
}
