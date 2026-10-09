package huawei

import (
	"encoding/json"
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	basic "github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	lts "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/lts/v2"
	ltsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/lts/v2/model"
	ltsregion "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/lts/v2/region"
	"github.com/gotomicro/ego/core/elog"
)

// defaultRegion 华为云日志所在区域(Phase 0 实测全部源在 cn-south-1)。
const defaultRegion = "cn-south-1"

func init() {
	logquery.RegisterProvider(domain.CloudProviderHuawei, logquery.LogTypeWAF, newProvider(logquery.LogTypeWAF))
	logquery.RegisterProvider(domain.CloudProviderHuawei, logquery.LogTypeSLB, newProvider(logquery.LogTypeSLB))
}

// hGroup catalog 条目:按组名枚举流,classify 按流名后缀映射。
type hGroup struct {
	group  string
	name   string
	note   string
}

// catalog 华为侧 LTS 日志组清单(Phase 0 实测;组名账号内稳定)。
var catalog = []hGroup{
	{group: "hwyun-waf-logs", name: "WAF", note: "WAF 攻击+访问日志(实时)"},
	{group: "eda-prod-elb", name: "ELB", note: "ELB 访问日志(实时)"},
}

// provider 华为云 LTS 日志 provider(单账号)。
type provider struct {
	logType logquery.LogType
	account *domain.CloudAccount
	logger  *elog.Component

	clientOnce sync.Once
	ltsClient  *lts.LtsClient
}

// newProvider 按日志类型构造。
func newProvider(logType logquery.LogType) logquery.ProviderCreator {
	return func(account *domain.CloudAccount) (logquery.LogProvider, error) {
		if account.AccessKeyID == "" || account.AccessKeySecret == "" {
			return nil, fmt.Errorf("huawei logquery: account %d missing credentials", account.ID)
		}
		return &provider{
			logType: logType,
			account: account,
			logger:  elog.DefaultLogger,
		}, nil
	}
}

// lts 惰性构造 LTS client(凭证固定,region 固定 cn-south-1)。
func (p *provider) lts() *lts.LtsClient {
	p.clientOnce.Do(func() {
		cred := basic.NewCredentialsBuilder().
			WithAk(p.account.AccessKeyID).
			WithSk(p.account.AccessKeySecret).
			Build()
		p.ltsClient = lts.NewLtsClient(
			lts.LtsClientBuilder().
				WithRegion(ltsregion.ValueOf(defaultRegion)).
				WithCredential(cred).
				Build())
	})
	return p.ltsClient
}

// Cloud 实现 LogProvider。
func (p *provider) Cloud() domain.CloudProvider { return domain.CloudProviderHuawei }

// LogType 实现 LogProvider。
func (p *provider) LogType() logquery.LogType { return p.logType }

// LogSource 与 Search 均需 group name -> id 映射,ListLogGroups 一次解析。
func (p *provider) groupIDs(ctx context.Context) (map[string]string, error) {
	resp, err := p.lts().ListLogGroups(&ltsmodel.ListLogGroupsRequest{})
	if err != nil {
		return nil, fmt.Errorf("huawei logquery: list log groups: %w", err)
	}
	out := make(map[string]string)
	if resp.LogGroups == nil {
		return out, nil
	}
	for _, g := range *resp.LogGroups {
		out[g.LogGroupName] = g.LogGroupId
	}
	return out, nil
}

// ListLogSources 枚举该账号该类型日志源(LTS 流,按 classify 过滤;清单走
// 10 分钟进程级缓存——流清单分钟级稳定,每次 sources 都 ListLogStreams ×2
// 组曾是 huawei sources ~300ms 的固定开销)。
func (p *provider) ListLogSources(ctx context.Context, account *domain.CloudAccount) ([]logquery.LogSource, error) {
	key := fmt.Sprintf("%d/%s", account.ID, p.logType)
	sources, _, err := sourceEnumCache.get(key, func() ([]logquery.LogSource, error) {
		return p.listSourcesUncached(ctx)
	})
	return sources, err
}

// listSourcesUncached 真实枚举(groupIDs 提前暴露凭证/网络问题,而非静默空清单)。
func (p *provider) listSourcesUncached(ctx context.Context) ([]logquery.LogSource, error) {
	if _, err := p.groupIDs(ctx); err != nil {
		return nil, err
	}
	var out []logquery.LogSource
	for _, src := range catalog {
		streams, err := p.lts().ListLogStreams(&ltsmodel.ListLogStreamsRequest{
			LogGroupName: &src.group,
		})
		if err != nil {
			// 单组失败隔离:记日志,不阻塞其他组
			p.logger.Warn("[logquery-huawei] list streams failed",
				elog.String("group", src.group), elog.FieldErr(err))
			continue
		}
		for _, s := range derefStreams(streams.LogStreams) {
			kind, ok := classify(src.group, s.LogStreamName)
			if !ok {
				continue
			}
			logType := logquery.LogTypeWAF
			if kind == kindELB {
				logType = logquery.LogTypeSLB
			}
			if logType != p.logType {
				continue
			}
			enabled := s.WhetherLogStorage == nil || *s.WhetherLogStorage
			out = append(out, logquery.LogSource{
				Cloud:       domain.CloudProviderHuawei,
				AccountID:   fmt.Sprintf("%d", p.account.ID),
				AccountName: p.account.Name,
				Region:      defaultRegion,
				LogType:     logType,
				ResourceID:  src.group + "/" + s.LogStreamName,
				Name:        src.name + " / " + s.LogStreamName,
				Enabled:     enabled,
				Note:        src.note,
			})
		}
	}
	return out, nil
}

// Search 查询窗口内日志:逐 (组,流) 并发 ListLogs -> 按 kind 映射 ->
// 时间倒序。单流失败隔离(记日志跳过)。
func (p *provider) Search(ctx context.Context, account *domain.CloudAccount, params logquery.SearchParams) ([]logquery.LogEntry, error) {
	if params.EndTime <= params.StartTime {
		return nil, fmt.Errorf("huawei logquery: invalid time window")
	}
	limit := params.Limit
	if limit <= 0 || limit > 3000 {
		limit = 100
	}
	ids, err := p.groupIDs(ctx)
	if err != nil {
		return nil, err
	}

	type streamTarget struct {
		group  hGroup
		kind   mapperKind
		stream ltsmodel.ListLogStreamsResponseBody1LogStreams
	}
	var targets []streamTarget
	for _, src := range catalog {
		if id := ids[src.group]; id == "" {
			continue // 组不存在(账号无此日志组),静默跳过
		}
		streams, err := p.lts().ListLogStreams(&ltsmodel.ListLogStreamsRequest{
			LogGroupName: &src.group,
		})
		if err != nil {
			p.logger.Warn("[logquery-huawei] list streams failed",
				elog.String("group", src.group), elog.FieldErr(err))
			continue
		}
		for _, s := range derefStreams(streams.LogStreams) {
			kind, ok := classify(src.group, s.LogStreamName)
			if !ok {
				continue
			}
			logType := logquery.LogTypeWAF
			if kind == kindELB {
				logType = logquery.LogTypeSLB
			}
			if logType != p.logType {
				continue
			}
			if len(params.Resources) > 0 {
				hit := false
				for _, r := range params.Resources {
					if r == s.LogStreamName || r == src.group+"/"+s.LogStreamName {
						hit = true
						break
					}
				}
				if !hit {
					continue
				}
			}
			targets = append(targets, streamTarget{group: src, kind: kind, stream: s})
		}
	}

	// ---- 流级并发查询(8 上限,单流失败隔离) ----
	results := make([][]logquery.LogEntry, len(targets))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, tgt := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			logs, err := p.fetchStreamLogs(ctx, ids[tgt.group.group], tgt.kind, tgt.stream, params, limit)
			if err != nil {
				p.logger.Warn("[logquery-huawei] fetch logs failed",
					elog.String("group", tgt.group.group),
					elog.String("stream", tgt.stream.LogStreamName), elog.FieldErr(err))
				return
			}
			results[i] = logs
		}()
	}
	wg.Wait()

	var entries []logquery.LogEntry
	for i := range targets {
		entries = append(entries, results[i]...)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].GetTimestamp() > entries[j].GetTimestamp()
	})
	// limit 为每日志流上限(ADR D4);归并后全局 1000 硬顶,不做 provider 级
	// 总截断(防热点流吃掉全部配额)
	const providerCap = 3000
	if len(entries) > providerCap {
		entries = entries[:providerCap]
	}
	return entries, nil
}

// fetchStreamLogs 单流查询:ListLogs 分页(每次 100,LineNum 滚动)。
func (p *provider) fetchStreamLogs(ctx context.Context, groupID string, kind mapperKind, stream ltsmodel.ListLogStreamsResponseBody1LogStreams, params logquery.SearchParams, limit int) ([]logquery.LogEntry, error) {
	var entries []logquery.LogEntry
	pageLimit := int32(100)
	keywords := strings.TrimSpace(params.Query)
	lineNum := ""
	for int64(len(entries)) < int64(limit) {
		body := &ltsmodel.QueryLtsLogParams{
			// LTS 查询窗口为毫秒(字符串)
			StartTime: strconv.FormatInt(params.StartTime, 10),
			EndTime:   strconv.FormatInt(params.EndTime, 10),
			Limit:     &pageLimit,
			IsDesc:    boolPtr(true), // 时间倒序
			// 高亮默认 true 会把高亮标签注入内容,关闭
			Highlight: boolPtr(false),
		}
		if keywords != "" {
			body.Keywords = &keywords
		}
		// 首查与分页均用 forwards + line_num 滚动(与 is_desc 配合取最新)
		searchType := ltsmodel.GetQueryLtsLogParamsSearchTypeEnum().FORWARDS
		body.SearchType = &searchType
		if lineNum != "" {
			body.LineNum = &lineNum
		}
		resp, err := p.lts().ListLogs(&ltsmodel.ListLogsRequest{
			LogGroupId:  groupID,
			LogStreamId: stream.LogStreamId,
			Body:        body,
		})
		if err != nil {
			return nil, fmt.Errorf("lts list_logs %s: %w", stream.LogStreamName, err)
		}
		logs := resp.Logs
		if logs == nil || len(*logs) == 0 {
			break
		}
		for _, lc := range *logs {
			content := ""
			if lc.Content != nil {
				content = *lc.Content
			}
			meta := logquery.LogMeta{
				Cloud:       domain.CloudProviderHuawei,
				AccountID:   fmt.Sprintf("%d", p.account.ID),
				AccountName: p.account.Name,
				Region:      defaultRegion,
				ResourceID:  stream.LogStreamName,
				Source:      defaultRegion + "/" + stream.LogStreamName,
			}
			if e := mapContent(kind, meta, content); e != nil {
				// 字段筛选:映射后统一字段语义过滤(与阿里/火山一致)
				if logquery.EntryMatches(e, params.Filters) {
					entries = append(entries, e)
				}
			}
		}
		last := (*logs)[len(*logs)-1].LineNum
		if last == nil || *last == lineNum { // 分页游标未前进,防死循环
			break
		}
		lineNum = *last
		if resp.IsQueryComplete != nil && *resp.IsQueryComplete {
			break
		}
	}
	return entries, nil
}

// Aggregate 窗口内真实聚合(plan.md §8):LTS is_analysis_query 管道 SQL
// 下推(活体验证 24h/98.7 万条 1.2s),每流分桶(+TopN,仅 ELB host 维度),
// Total = 分桶求和。单流失败隔离。方言差异见 aggregate.go 头注。
func (p *provider) Aggregate(ctx context.Context, account *domain.CloudAccount, params logquery.AggregateParams) (*logquery.AggregateResult, error) {
	if params.EndTime <= params.StartTime {
		return nil, fmt.Errorf("huawei logquery aggregate: invalid time window")
	}
	if params.BucketSec <= 0 {
		params.BucketSec = logquery.PickBucketSec(params.StartTime, params.EndTime)
	}
	ids, err := p.groupIDs(ctx)
	if err != nil {
		return nil, err
	}
	resources := make(map[string]bool, len(params.Resources))
	for _, r := range params.Resources {
		resources[r] = true
	}

	type streamTarget struct {
		group  hGroup
		kind   mapperKind
		stream ltsmodel.ListLogStreamsResponseBody1LogStreams
	}
	var targets []streamTarget
	for _, src := range catalog {
		if id := ids[src.group]; id == "" {
			continue
		}
		streams, err := p.lts().ListLogStreams(&ltsmodel.ListLogStreamsRequest{
			LogGroupName: &src.group,
		})
		if err != nil {
			p.logger.Warn("[logquery-huawei] aggregate list streams failed",
				elog.String("group", src.group), elog.FieldErr(err))
			continue
		}
		for _, s := range derefStreams(streams.LogStreams) {
			kind, ok := classify(src.group, s.LogStreamName)
			if !ok {
				continue
			}
			logType := logquery.LogTypeWAF
			if kind == kindELB {
				logType = logquery.LogTypeSLB
			}
			if logType != p.logType {
				continue
			}
			if len(resources) > 0 {
				hit := false
				for _, r := range params.Resources {
					if r == s.LogStreamName || r == src.group+"/"+s.LogStreamName {
						hit = true
						break
					}
				}
				if !hit {
					continue
				}
			}
			targets = append(targets, streamTarget{group: src, kind: kind, stream: s})
		}
	}

	// 字段筛选:可下推的 kind 预编译检索段,其余显式失败(整源不计,防空数)
	filterParts := make(map[mapperKind]string, len(targets))
	for _, tgt := range targets {
		if _, ok := filterParts[tgt.kind]; ok {
			continue
		}
		part, ok := filterWhere(tgt.kind, params.Filters)
		if !ok {
			return nil, fmt.Errorf("字段筛选无法下推: %s", tgt.kind)
		}
		filterParts[tgt.kind] = part
	}

	results := make([]*logquery.AggregateResult, len(targets))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, tgt := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 字段筛选不可下推的 kind:跳过(provider 级整体报错误,防空数)
			if _, ok := filterParts[tgt.kind]; !ok {
				return
			}
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = p.aggregateStream(ids[tgt.group.group], tgt.kind, tgt.stream, params, filterParts[tgt.kind])
		}()
	}
	wg.Wait()

	merged := &logquery.AggregateResult{}
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
	if len(merged.TopN) > 1 {
		counts := make(map[string]int64, len(merged.TopN))
		for _, t := range merged.TopN {
			counts[t.Name] += t.Count
		}
		merged.TopN = merged.TopN[:0]
		for name, c := range counts {
			merged.TopN = append(merged.TopN, logquery.TopNItem{Name: name, Count: c, Value: float64(c)})
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

// aggregateStream 单流两条 SQL:分桶 + TopN(无维度的流跳过 TopN)。
// analysisLogs 行为 map[string]any;t 已是毫秒(LTS __time 量纲)。
func (p *provider) aggregateStream(groupID string, kind mapperKind, stream ltsmodel.ListLogStreamsResponseBody1LogStreams, params logquery.AggregateParams, filterPart string) *logquery.AggregateResult {
	result := &logquery.AggregateResult{}
	runSQL := func(sql string, lines int32) ([]map[string]any, error) {
		body := &ltsmodel.QueryLtsLogParams{
			StartTime:       strconv.FormatInt(params.StartTime, 10),
			EndTime:         strconv.FormatInt(params.EndTime, 10),
			Query:           &sql,
			IsAnalysisQuery: boolPtr(true),
			Limit:           &lines,
		}
		resp, err := p.lts().ListLogs(&ltsmodel.ListLogsRequest{
			LogGroupId:  groupID,
			LogStreamId: stream.LogStreamId,
			Body:        body,
		})
		if err != nil {
			return nil, fmt.Errorf("lts aggregate sql %s: %w", stream.LogStreamName, err)
		}
		if resp.AnalysisLogs == nil {
			return nil, nil
		}
		rows := make([]map[string]any, 0, len(*resp.AnalysisLogs))
		for _, raw := range *resp.AnalysisLogs {
			if m, ok := raw.(map[string]any); ok {
				rows = append(rows, m)
			}
		}
		return rows, nil
	}

	bucketRows, err := runSQL(buildAggregateBucketSQL(filterPart, params.BucketSec), 200)
	if err != nil {
		p.logger.Warn("[logquery-huawei] aggregate bucket sql failed",
			elog.String("stream", stream.LogStreamName), elog.FieldErr(err))
		return result
	}
	for _, row := range bucketRows {
		ms, ok := jsonInt64(row["t"])
		c, ok2 := jsonInt64(row["c"])
		if !ok || !ok2 {
			continue
		}
		result.Buckets = append(result.Buckets, logquery.AggregateBucket{Timestamp: ms, Count: c})
		result.Total += c
	}
	// TopN 维度:自定义 dimension 优先,缺省回退 kind 默认维度(现行为兼容)
	dim := aggregateTopNExpr(kind)
	if params.Dimension != "" {
		expr, ok := dimensionExpr(kind, params.Dimension)
		if !ok {
			result.TopNSkipReason = "维度 " + params.Dimension + " 该源不支持"
			return result
		}
		dim = expr
	}
	if dim == "" {
		return result // 无维度流(WAF 攻击/访问流)跳过 TopN
	}
	if _, ok := metricSQLExpr(kind, params.Metric); !ok {
		result.TopNSkipReason = "指标 " + params.Metric + " 该源不支持"
		return result
	}
	// 指标仅 count(LTS 未验证 avg/分位):v 恒等于 n(见 buildAggregateTopNSQL)
	topnRows, err := runSQL(buildAggregateTopNSQL(filterPart, dim, 10), 10)
	if err != nil {
		p.logger.Warn("[logquery-huawei] aggregate topn sql failed",
			elog.String("stream", stream.LogStreamName), elog.FieldErr(err))
		return result
	}
	for _, row := range topnRows {
		k, ok := row["k"].(string)
		c, ok2 := jsonInt64(row["n"])
		if !ok || k == "" || !ok2 {
			continue
		}
		result.TopN = append(result.TopN, logquery.TopNItem{Name: k, Count: c, Value: float64(c)})
	}
	return result
}

// jsonInt64 analysisLogs 行值容错取数。华为 SDK 对动态字段用 json.Number
// 反序列化(非 float64/string),必须显式处理,否则整行被丢(total 恒 0)。
func jsonInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case int64:
		return t, true
	case int:
		return int64(t), true
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return int64(f), true
		}
	case string:
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return int64(f), true
		}
	}
	return 0, false
}

func boolPtr(b bool) *bool { return &b }

// derefStreams 解引用日志流指针切片(nil 安全)。
func derefStreams(s *[]ltsmodel.ListLogStreamsResponseBody1LogStreams) []ltsmodel.ListLogStreamsResponseBody1LogStreams {
	if s == nil {
		return nil
	}
	return *s
}
