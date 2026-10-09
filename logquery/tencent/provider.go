// Package tencent 腾讯云日志查询 provider。
//
// CDN 类型:EdgeOne 访问日志真实查询(实测 2026-09-17,ap-guangzhou eo-log
// topic 近 7 天持续写入)。CLS(腾讯云日志服务)检索|分析下推:源枚举 = 动态
// 识别 EdgeOne topic(采样确认 RequestHost/EdgeResponseStatusCode 字段),
// Search = SearchLog 分页拉取映射统一 CDN 模型,Aggregate = 分桶 + TopN
// 分析 SQL(cast/group by/sum/avg/approx_percentile 实测可用)。
//
// WAF 类型:仍是占位 —— Phase 0/2026-09-17 实测 waf_access_logtopic
// (ap-shanghai)近 7 天/30 天均 0 条,数据源未流动;sources 列出已知 topic
// 并标注未启用,Search 返回明确错误引导云侧开启投递。
package tencent

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	cls "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cls/v20201016"
)

// defaultEnumRegions 源枚举兜底区域(腾讯日志常见落点;账号 Regions 交集优先)。
var defaultEnumRegions = []string{"ap-guangzhou", "ap-shanghai", "ap-beijing"}

// topicKindTTL topic 分类缓存新鲜时长(识别结果分钟级稳定)。
const topicKindTTL = 30 * time.Minute

// topicKindCache 进程级 topic 分类缓存(region/topic → "eo"|"waf"|"other")。
type topicKindCache struct {
	mu      sync.Mutex
	entries map[string]topicKindEntry
}

type topicKindEntry struct {
	kind    string
	expires time.Time
}

var topicKindCacheInst = &topicKindCache{entries: make(map[string]topicKindEntry)}

// get 命中且未过期返回 (kind, true)。
func (c *topicKindCache) get(region, topicID string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[region+"/"+topicID]
	if !ok || time.Now().After(e.expires) {
		return "", false
	}
	return e.kind, true
}

func (c *topicKindCache) set(region, topicID, kind string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[region+"/"+topicID] = topicKindEntry{kind: kind, expires: time.Now().Add(topicKindTTL)}
}

// topicKind 判定 topic 类型:采样最近一条日志按字段特征分类。
// 0 条/无特征 → "other" 且**不缓存**(投递可能晚来,下次再探);
// 有特征 → 缓存 30min(稳定)。
func (p *provider) topicKind(ctx context.Context, region, topicID string) string {
	if k, ok := topicKindCacheInst.get(region, topicID); ok {
		return k
	}
	raw := p.probeRawLog(ctx, region, topicID)
	kind := "other"
	if len(raw) > 0 {
		switch {
		case raw["RequestHost"] != "" && raw["EdgeResponseStatusCode"] != "":
			kind = "eo"
		case hasWAFFields(raw):
			kind = "waf"
		}
	}
	if len(raw) > 0 {
		topicKindCacheInst.set(region, topicID, kind)
	}
	return kind
}

// topicsCache 进程级 DescribeTopics 清单缓存(每区域;topic 清单变动频率低)。
type topicsCache struct {
	mu      sync.Mutex
	entries map[string]topicsEntry
}

type topicsEntry struct {
	topics  []string // topicID 列表
	names   map[string]string
	expires time.Time
}

var topicsCacheInst = &topicsCache{entries: make(map[string]topicsEntry)}

func (p *provider) listTopicsCached(ctx context.Context, region string) ([]string, map[string]string) {
	topicsCacheInst.mu.Lock()
	if e, ok := topicsCacheInst.entries[region]; ok && time.Now().Before(e.expires) {
		topicsCacheInst.mu.Unlock()
		return e.topics, e.names
	}
	topicsCacheInst.mu.Unlock()
	client, err := p.clsClient(region)
	if err != nil {
		return nil, nil
	}
	var ids []string
	names := make(map[string]string)
	req := cls.NewDescribeTopicsRequest()
	offset := int64(0)
	limit := int64(100)
	for {
		req.Offset = &offset
		req.Limit = &limit
		resp, err := client.DescribeTopicsWithContext(ctx, req)
		if err != nil {
			p.logger.Warn("[logquery-tencent] list topics failed",
				elog.String("region", region), elog.FieldErr(err))
			break
		}
		body := resp.Response
		if body == nil || len(body.Topics) == 0 {
			break
		}
		for _, t := range body.Topics {
			if t == nil || t.TopicId == nil {
				continue
			}
			id := *t.TopicId
			ids = append(ids, id)
			if t.TopicName != nil {
				names[id] = *t.TopicName
			}
		}
		if len(body.Topics) < 100 {
			break
		}
		offset += int64(len(body.Topics))
	}
	if len(ids) > 0 {
		topicsCacheInst.mu.Lock()
		topicsCacheInst.entries[region] = topicsEntry{topics: ids, names: names, expires: time.Now().Add(topicKindTTL)}
		topicsCacheInst.mu.Unlock()
	}
	return ids, names
}

// provider 腾讯云日志 provider。
type provider struct {
	logType   logquery.LogType
	account   *domain.CloudAccount
	logger    *elog.Component
	clientsMu sync.Mutex
	clients   map[string]*cls.Client
}

func init() {
	logquery.RegisterProvider(domain.CloudProviderTencent, logquery.LogTypeWAF, newProvider(logquery.LogTypeWAF))
	logquery.RegisterProvider(domain.CloudProviderTencent, logquery.LogTypeCDN, newProvider(logquery.LogTypeCDN))
}

func newProvider(logType logquery.LogType) logquery.ProviderCreator {
	return func(account *domain.CloudAccount) (logquery.LogProvider, error) {
		if account.AccessKeyID == "" || account.AccessKeySecret == "" {
			return nil, fmt.Errorf("tencent logquery: account %d missing credentials", account.ID)
		}
		return &provider{logType: logType, account: account, logger: elog.DefaultLogger}, nil
	}
}

// Cloud 实现 LogProvider。
func (p *provider) Cloud() domain.CloudProvider { return domain.CloudProviderTencent }

// LogType 实现 LogProvider。
func (p *provider) LogType() logquery.LogType { return p.logType }

// enumRegions 枚举区域:账号 Regions 与默认落点交集(有则用),兜底默认。
func (p *provider) enumRegions() []string {
	if len(p.account.Regions) > 0 {
		have := make(map[string]bool, len(p.account.Regions))
		for _, r := range p.account.Regions {
			have[r] = true
		}
		var out []string
		for _, r := range defaultEnumRegions {
			if have[r] {
				out = append(out, r)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return defaultEnumRegions
}

// ListLogSources 枚举日志源:CDN = EdgeOne topic, WAF = WAF topic(均动态
// 识别;WAF 已知 stub 在 0 条时仍列出 enabled=false 引导云侧投递)。
func (p *provider) ListLogSources(ctx context.Context, account *domain.CloudAccount) ([]logquery.LogSource, error) {
	switch p.logType {
	case logquery.LogTypeWAF:
		return p.wafSources(ctx)
	default:
		return p.sourcesByKind(ctx, "eo", "EdgeOne", "EdgeOne 访问日志(CLS 混装全部站点)")
	}
}

// sourcesByKind 枚举指定类型 topic 为源(识别带缓存,并发逐区域)。
func (p *provider) sourcesByKind(ctx context.Context, kind, label, note string) ([]logquery.LogSource, error) {
	regions := p.enumRegions()
	var (
		mu  sync.Mutex
		out []logquery.LogSource
		wg  sync.WaitGroup
	)
	for _, region := range regions {
		wg.Add(1)
		go func(region string) {
			defer wg.Done()
			ids, names := p.listTopicsCached(ctx, region)
			var found []logquery.LogSource
			for _, id := range ids {
				if p.topicKind(ctx, region, id) != kind {
					continue
				}
				name := names[id]
				if name == "" {
					name = id
				}
				found = append(found, logquery.LogSource{
					Cloud:       domain.CloudProviderTencent,
					AccountID:   fmt.Sprintf("%d", p.account.ID),
					AccountName: p.account.Name,
					Region:      region,
					LogType:     p.logType,
					ResourceID:  id,
					Name:        label + " / " + name,
					Enabled:     true,
					Note:        note,
				})
			}
			mu.Lock()
			out = append(out, found...)
			mu.Unlock()
		}(region)
	}
	wg.Wait()
	return out, nil
}

// wafSources WAF 日志源:动态识别的 WAF topic(enabled) + 已知 stub
// (0 条时 enabled=false 引导云侧开启投递)。
func (p *provider) wafSources(ctx context.Context) ([]logquery.LogSource, error) {
	sources, err := p.sourcesByKind(ctx, "waf", "WAF 访问日志", "腾讯云 WAF 访问日志(CLS 混装全部域名)")
	if err != nil {
		return nil, err
	}
	// 已知 stub topic:未被识别为 WAF(当前 0 条)时仍列出,引导投递
	const stubID = "a0bfd8ed-d7b1-480a-879b-3c143f7302b8"
	found := false
	for _, s := range sources {
		if s.ResourceID == stubID {
			found = true
			break
		}
	}
	if !found {
		sources = append(sources, logquery.LogSource{
			Cloud:       domain.CloudProviderTencent,
			AccountID:   fmt.Sprintf("%d", p.account.ID),
			AccountName: p.account.Name,
			Region:      "ap-shanghai",
			LogType:     p.logType,
			ResourceID:  stubID,
			Name:        "WAF 访问日志 / " + stubID,
			Enabled:     false,
			Note:        "topic 存在但近 30 天 0 条,需腾讯云 WAF 控制台开启日志服务投递(ap-shanghai)",
		})
	}
	return sources, nil
}

// kindForLogType 日志类型 → topic 分类(kind)+ 映射器。
func (p *provider) kindForLogType() (string, rawMapper) {
	if p.logType == logquery.LogTypeWAF {
		return "waf", wafEntry
	}
	return "eo", eoEntry
}

// Search 查询窗口内日志:CLS 分页拉取映射统一模型(CDN=EdgeOne,WAF=WAF 日志;
// WAF topic 0 条时自然返回空,投递开启即有数据,不硬报错)。
func (p *provider) Search(ctx context.Context, account *domain.CloudAccount, params logquery.SearchParams) ([]logquery.LogEntry, error) {
	if params.EndTime <= params.StartTime {
		return nil, fmt.Errorf("tencent logquery: invalid time window")
	}
	limit := params.Limit
	if limit <= 0 || limit > 3000 {
		limit = 100
	}
	kind, mapper := p.kindForLogType()
	targets := p.topicsByKind(ctx, kind, params.Resources)
	query := "*"
	if q := strings.TrimSpace(params.Query); q != "" && q != "*" {
		query = q
	}

	type target struct {
		region, topicID string
	}
	results := make([][]logquery.LogEntry, len(targets))
	var wg sync.WaitGroup
	for i, tgt := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			meta := logquery.LogMeta{
				Cloud:       domain.CloudProviderTencent,
				AccountID:   fmt.Sprintf("%d", p.account.ID),
				AccountName: p.account.Name,
				Region:      tgt.region,
				ResourceID:  tgt.topicID,
				Source:      tgt.region + "/" + tgt.topicID,
			}
			entries, err := p.clsSearchLogs(ctx, tgt.region, tgt.topicID, params.StartTime, params.EndTime, query, limit, meta, mapper)
			if err != nil {
				p.logger.Warn("[logquery-tencent] search topic failed",
					elog.String("region", tgt.region), elog.String("topic", tgt.topicID), elog.FieldErr(err))
				return
			}
			// 字段筛选:映射后统一字段语义过滤(采样级逐条,与其他 provider 一致)
			var filtered []logquery.LogEntry
			for _, e := range entries {
				if logquery.EntryMatches(e, params.Filters) {
					filtered = append(filtered, e)
				}
			}
			results[i] = filtered
		}()
	}
	wg.Wait()

	var entries []logquery.LogEntry
	for _, r := range results {
		entries = append(entries, r...)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].GetTimestamp() > entries[j].GetTimestamp()
	})
	const providerCap = 3000
	if len(entries) > providerCap {
		entries = entries[:providerCap]
	}
	return entries, nil
}

// topicsByKind 解析目标 topic 列表:指定 kind 的 topic;Resources 收敛(topicId)。
func (p *provider) topicsByKind(ctx context.Context, kind string, resources []string) []struct{ region, topicID string } {
	want := make(map[string]bool, len(resources))
	for _, r := range resources {
		if r != "" {
			want[r] = true
		}
	}
	var out []struct{ region, topicID string }
	for _, region := range p.enumRegions() {
		ids, _ := p.listTopicsCached(ctx, region)
		for _, id := range ids {
			if p.topicKind(ctx, region, id) != kind {
				continue
			}
			if len(want) > 0 && !want[id] {
				continue
			}
			out = append(out, struct{ region, topicID string }{region: region, topicID: id})
		}
	}
	return out
}

// Aggregate 窗口内真实聚合:CLS 分析下推分桶 + TopN。字段筛选暂不支持下推
// (返回错误,联邦层标注该源跳过,防失真)。WAF 同构(维度/指标按类型)。
func (p *provider) Aggregate(ctx context.Context, account *domain.CloudAccount, params logquery.AggregateParams) (*logquery.AggregateResult, error) {
	if params.EndTime <= params.StartTime {
		return nil, fmt.Errorf("tencent logquery aggregate: invalid time window")
	}
	if len(params.Filters) > 0 {
		return nil, fmt.Errorf("tencent logquery aggregate: 字段筛选暂不支持下推 CLS(请改用明细筛选或清空筛选聚合)")
	}
	if params.BucketSec <= 0 {
		params.BucketSec = logquery.PickBucketSec(params.StartTime, params.EndTime)
	}
	query := "*"
	if q := strings.TrimSpace(params.Query); q != "" && q != "*" {
		query = q
	}
	kind, _ := p.kindForLogType()
	targets := p.topicsByKind(ctx, kind, params.Resources)
	if len(targets) == 0 {
		return &logquery.AggregateResult{}, nil
	}

	// ---- logstore(topic)级并发聚合(单源失败隔离,与 aliyun 同型) ----
	results := make([]*logquery.AggregateResult, len(targets))
	var wg sync.WaitGroup
	for i, tgt := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = p.aggregateTopic(ctx, tgt.region, tgt.topicID, params, query, kind)
		}()
	}
	wg.Wait()

	merged := &logquery.AggregateResult{}
	weighted := logquery.MetricIsWeighted(params.Metric)
	for _, r := range results {
		if r == nil {
			continue
		}
		merged.Total += r.Total
		merged.Buckets = append(merged.Buckets, r.Buckets...)
		merged.TopN = append(merged.TopN, r.TopN...)
		if r.TopNSkipReason != "" && merged.TopNSkipReason == "" {
			merged.TopNSkipReason = r.TopNSkipReason
		}
	}
	sort.Slice(merged.Buckets, func(i, j int) bool {
		return merged.Buckets[i].Timestamp < merged.Buckets[j].Timestamp
	})
	if len(merged.TopN) > 0 {
		type acc struct{ count int64; value float64 }
		groups := make(map[string]acc, len(merged.TopN))
		for _, t := range merged.TopN {
			g := groups[t.Name]
			g.count += t.Count
			if weighted {
				g.value += t.Value * float64(t.Count)
			} else {
				g.value += t.Value
			}
			groups[t.Name] = g
		}
		merged.TopN = merged.TopN[:0]
		for name, g := range groups {
			v := g.value
			if weighted && g.count > 0 {
				v = g.value / float64(g.count)
			}
			merged.TopN = append(merged.TopN, logquery.TopNItem{Name: name, Count: g.count, Value: v})
		}
		sort.Slice(merged.TopN, func(i, j int) bool {
			return merged.TopN[i].Value > merged.TopN[j].Value
		})
	}
	const providerTopN = 10
	if len(merged.TopN) > providerTopN {
		merged.TopN = merged.TopN[:providerTopN]
	}
	return merged, nil
}

// aggregateTopic 单 topic 两条分析 SQL:分桶 + TopN。失败返回 nil(隔离)。
func (p *provider) aggregateTopic(ctx context.Context, region, topicID string, params logquery.AggregateParams, baseQuery, kind string) *logquery.AggregateResult {
	result := &logquery.AggregateResult{}
	bucketMs := params.BucketSec * 1000

	// 分桶:t = __TIMESTAMP__(ms) 整数除得桶索引;Total = 求和
	bucketSQL := fmt.Sprintf("%s | select cast(__TIMESTAMP__ as bigint)/%d as t, count(*) as c group by t order by t limit 200", baseQuery, bucketMs)
	rows, err := p.clsAnalysis(ctx, region, topicID, params.StartTime, params.EndTime, bucketSQL, 200)
	if err != nil {
		p.logger.Warn("[logquery-tencent] aggregate bucket sql failed",
			elog.String("topic", topicID), elog.FieldErr(err))
		return nil
	}
	for _, row := range rows {
		t := logquery.Int(row["t"])
		if t <= 0 {
			continue
		}
		count := logquery.Int(row["c"])
		result.Buckets = append(result.Buckets, logquery.AggregateBucket{Timestamp: t * bucketMs, Count: count})
		result.Total += count
	}

	// TopN:维度/指标编译;失败标注 TopNSkipReason(趋势/总数不受影响)
	dimExpr := eoDimensionExpr
	dimMetrics := eoMetricExpr
	defaultDim := "RequestHost"
	if kind == "waf" {
		dimExpr = wafDimensionExpr
		dimMetrics = wafMetricExpr
		defaultDim = "domain"
	}
	dim := defaultDim
	if params.Dimension != "" {
		expr, ok := dimExpr(params.Dimension)
		if !ok {
			result.TopNSkipReason = "维度 " + params.Dimension + " 该源不支持"
			return result
		}
		dim = expr
	}
	mExpr, ok := dimMetrics[params.Metric]
	if !ok {
		if params.Metric != "" {
			result.TopNSkipReason = "指标 " + params.Metric + " 该源不支持"
			return result
		}
		mExpr = dimMetrics["count"]
	}
	topnSQL := fmt.Sprintf("%s | select %s as k, count(*) as n, %s as v group by k order by v desc limit 10", baseQuery, dim, mExpr)
	rows, err = p.clsAnalysis(ctx, region, topicID, params.StartTime, params.EndTime, topnSQL, 10)
	if err != nil {
		p.logger.Warn("[logquery-tencent] aggregate topn sql failed",
			elog.String("topic", topicID), elog.String("dim", dim), elog.FieldErr(err))
		if params.Dimension != "" {
			result.TopNSkipReason = "维度 " + params.Dimension + " 查询失败:" + briefErr(err)
		}
		return result
	}
	isCount := params.Metric == "" || params.Metric == "count"
	for _, row := range rows {
		if k := row["k"]; k != "" {
			item := logquery.TopNItem{Name: k, Count: logquery.Int(row["n"])}
			if isCount {
				item.Value = float64(item.Count)
			} else if v, err2 := strconv.ParseFloat(row["v"], 64); err2 == nil {
				item.Value = v
			}
			result.TopN = append(result.TopN, item)
		}
	}
	return result
}

// briefErr 截取错误摘要(防长错误串进 TopNSkipReason 撑爆 UI)。
func briefErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if i := strings.Index(s, "errorMessage"); i >= 0 && i+80 < len(s) {
		s = s[i:]
	}
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}
