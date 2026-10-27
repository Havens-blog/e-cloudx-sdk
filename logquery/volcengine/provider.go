// Package volcengine 火山引擎日志查询 provider。
//
// 火山云日志均投递到 TLS(火山日志服务)。分两类注册:
//   - SLB(`*_lb_access`,原生 schema loadbalancer_id/listener_id)= 火山 ALB 产品日志;
//   - access(其余 *_access / *-nginx-access / *-gateway-access)= 自采实例 nginx/gateway。
//
// CDN 加速实时日志 / WAF 日志未投递(控台实时日志未开),暂不纳入。
// 与阿里 SLS / 腾讯 CLS 同构:源枚举 = DescribeTopics + 名字分型(无采样),
// Search = SearchLogs 分页拉取映射统一模型。
package volcengine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volc-sdk-golang/service/tls"
)

// endpointFor 按 region 拼 TLS endpoint。
func endpointFor(region string) string {
	return "https://tls-" + region + ".volces.com"
}

// defaultEnumRegions 源枚举兜底区域(账号 Regions 交集优先)。
var defaultEnumRegions = []string{"cn-guangzhou", "cn-beijing", "cn-shanghai"}

// topicsTTL DescribeTopics 清单缓存新鲜时长(topic 清单分钟级稳定)。
const topicsTTL = 30 * time.Minute

// topicsCache 进程级 DescribeTopics 清单缓存(每区域)。
type topicsCache struct {
	mu      sync.Mutex
	entries map[string]topicsEntry
}

type topicsEntry struct {
	topics  []*tls.Topic
	expires time.Time
}

var topicsCacheInst = &topicsCache{entries: make(map[string]topicsEntry)}

// indexCache 进程级 DescribeIndex 探测缓存(topicID → 是否可查询)。索引存在性
// 长期稳定,缓存 2h;探测走后台异步(不阻塞源列表),inflight 防重复调度。
type indexCache struct {
	mu       sync.Mutex
	entries  map[string]indexEntry
	inflight map[string]bool
	dirty    bool // 有未落盘的新增探测结果(定时刷盘用)
}

type indexEntry struct {
	enabled bool
	stale   bool // 近 staleWindow 无新数据(可能停采)
	expires time.Time
}

var indexCacheInst = &indexCache{entries: make(map[string]indexEntry), inflight: make(map[string]bool)}

// indexTTL 索引探测结果缓存时长(索引存在性稳定,长 TTL 摊薄后台探测成本)。
const indexTTL = 2 * time.Hour

// staleWindow 判定「停采」的近无数据窗口(超过该窗 TotalCount=0 即视为停采)。
const staleWindow = 7 * 24 * time.Hour

// indexProbeConcurrency 后台 DescribeIndex 探测并发度(限制 QPS)。
const indexProbeConcurrency = 24

// indexProbeSem 全局限流信号量(跨 provider 实例共享),防全量 topic 同时打 DescribeIndex。
var indexProbeSem = make(chan struct{}, indexProbeConcurrency)

// provider 火山云 TLS 日志 provider。
type provider struct {
	logType logquery.LogType
	account *domain.CloudAccount
	logger  *elog.Component
}

func init() {
	// 三类注册:SLB = 火山 ALB(`*_lb_access`),access = 自采 nginx/gateway,
	// app = 应用业务日志(`<service>-business`,与阿里同采集管道)。
	// CDN/WAF 托管产品日志未投递 TLS。
	// volcano/volcengine 双键注册:与 billing/iam/资产注册表口径一致(火山账号
	// 两个 provider 别名均存在,federation 按 acc.Provider 直查,单键会导致
	// provider=volcano 的账号在日志域查不到源)。
	logquery.RegisterProvider(domain.CloudProviderVolcengine, logquery.LogTypeSLB, newProvider(logquery.LogTypeSLB))
	logquery.RegisterProvider(domain.CloudProviderVolcengine, logquery.LogTypeAccess, newProvider(logquery.LogTypeAccess))
	logquery.RegisterProvider(domain.CloudProviderVolcengine, logquery.LogTypeApp, newProvider(logquery.LogTypeApp))
	logquery.RegisterProvider(domain.CloudProviderVolcano, logquery.LogTypeSLB, newProvider(logquery.LogTypeSLB))
	logquery.RegisterProvider(domain.CloudProviderVolcano, logquery.LogTypeAccess, newProvider(logquery.LogTypeAccess))
	logquery.RegisterProvider(domain.CloudProviderVolcano, logquery.LogTypeApp, newProvider(logquery.LogTypeApp))
}

func newProvider(logType logquery.LogType) logquery.ProviderCreator {
	return func(account *domain.CloudAccount) (logquery.LogProvider, error) {
		if account.AccessKeyID == "" || account.AccessKeySecret == "" {
			return nil, fmt.Errorf("volcengine logquery: account %d missing credentials", account.ID)
		}
		return &provider{logType: logType, account: account, logger: elog.DefaultLogger}, nil
	}
}

// Cloud 实现 LogProvider。
func (p *provider) Cloud() domain.CloudProvider { return domain.CloudProviderVolcengine }

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

// tlsClient 惰性建 TLS client(每 region;SDK 的 client 无状态仅持有 AK/SK)。
func (p *provider) tlsClient(region string) tls.Client {
	return tls.NewClient(endpointFor(region), p.account.AccessKeyID, p.account.AccessKeySecret, "", region)
}

// listTopicsCached DescribeTopics 清单(带缓存,每区域)。
func (p *provider) listTopicsCached(ctx context.Context, region string) []*tls.Topic {
	topicsCacheInst.mu.Lock()
	if e, ok := topicsCacheInst.entries[region]; ok && time.Now().Before(e.expires) {
		topicsCacheInst.mu.Unlock()
		return e.topics
	}
	topicsCacheInst.mu.Unlock()

	client := p.tlsClient(region)
	const pageSize = 100
	// DescribeTopics 每页固定延迟大(~4s),1130 topic 串行 12 页 ~50s 冷枚举。
	// 首页拿 Total 后并发拉余页,冷枚举收敛到 ~单页 + 一批并发页。
	first, err := client.DescribeTopics(&tls.DescribeTopicsRequest{
		ProjectID:  "",
		PageNumber: 1,
		PageSize:   pageSize,
	})
	if err != nil {
		p.logger.Warn("[logquery-volcengine] describe topics failed",
			elog.String("region", region), elog.FieldErr(err))
		return nil
	}
	if first == nil || len(first.Topics) == 0 {
		return nil
	}
	all := first.Topics
	pages := (first.Total + pageSize - 1) / pageSize
	if pages > 1 {
		slots := make([][]*tls.Topic, pages+1)
		slots[1] = first.Topics
		var wg sync.WaitGroup
		sem := make(chan struct{}, 12)
		for pn := 2; pn <= pages; pn++ {
			wg.Add(1)
			go func(pn int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				resp, err := client.DescribeTopics(&tls.DescribeTopicsRequest{
					ProjectID: "", PageNumber: pn, PageSize: pageSize,
				})
				if err != nil {
					p.logger.Warn("[logquery-volcengine] describe topics page failed",
						elog.String("region", region), elog.Int("page", pn), elog.FieldErr(err))
					return
				}
				if resp != nil {
					slots[pn] = resp.Topics
				}
			}(pn)
		}
		wg.Wait()
		for pn := 2; pn <= pages; pn++ {
			all = append(all, slots[pn]...)
		}
	}
	if len(all) > 0 {
		topicsCacheInst.mu.Lock()
		topicsCacheInst.entries[region] = topicsEntry{topics: all, expires: time.Now().Add(topicsTTL)}
		topicsCacheInst.mu.Unlock()
	}
	return all
}

// isAccessTopic 名字是否为访问日志形态:含 "access"(覆盖 *_access /
// *-nginx-access / *-gateway-access / *_lb_access / *-cache-access),排除
// *-tomcat-access(应用服务器访问,非边缘)与 *-business 结尾(应用业务日志——
// Java message 型,method 是类名非 HTTP 方法,照 aliyun filterCatalogStores 先例
// 排除)。用后缀匹配而非子串:*-business 结尾是业务日志,而 dev-ai-app-business_...
// _https_access 里 business 只是域名段(中缀),仍是边缘访问日志。采集管道命名
// 稳定,纯名字分型免去逐 topic 采样(采样曾是冷枚举 ~73s 主因)。
func isAccessTopic(topicName string) bool {
	name := strings.ToLower(topicName)
	return strings.Contains(name, "access") &&
		!strings.Contains(name, "tomcat-access") &&
		!strings.HasSuffix(name, "-business")
}

// isALBAccessTopic 是否为火山 ALB 产品访问日志(`*_lb_access`,含 limited 限流
// 变体)。ALB 归 SLB;其余 *_access / *-nginx-access / *-gateway-access 为自采
// 实例 nginx/gateway 访问日志,归 access 类型——两者不能混装,否则负载均衡趋势
// 会夹带源站实例流量(语义错位)。
func isALBAccessTopic(topicName string) bool {
	return strings.Contains(strings.ToLower(topicName), "_lb_access")
}

// topicRef 定位一个 TLS 访问日志 topic。
type topicRef struct {
	region    string
	topicID   string
	topicName string
}

// accessTopics 列出本 provider 类型对应的访问日志 topic(纯名字过滤,不采样)。
// 按 logType 分域:SLB 只取 ALB(`*_lb_access`),access 只取自采 nginx/gateway
// 访问日志。want 非空时按名字或 ID 收敛(源清单 ResourceID=名字,历史裸 ID 亦兼容)。
func (p *provider) accessTopics(ctx context.Context, want map[string]bool) []topicRef {
	var out []topicRef
	for _, region := range p.enumRegions() {
		for _, t := range p.listTopicsCached(ctx, region) {
			if t == nil || t.TopicID == "" || !isAccessTopic(t.TopicName) {
				continue
			}
			if p.logType == logquery.LogTypeSLB {
				if !isALBAccessTopic(t.TopicName) {
					continue
				}
			} else if isALBAccessTopic(t.TopicName) {
				continue
			}
			if len(want) > 0 && !want[t.TopicName] && !want[t.TopicID] {
				continue
			}
			out = append(out, topicRef{region: region, topicID: t.TopicID, topicName: t.TopicName})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].topicName < out[j].topicName })
	return out
}

// isIndexNotExists 判断 DescribeIndex 错误是否为「无索引」(IndexNotExists),
// 而非网络/权限/限流等瞬时错误——后者不可误判为无索引(否则会把有索引源标 false)。
func isIndexNotExists(err error) bool {
	s := err.Error()
	return strings.Contains(s, "IndexNotExists") || strings.Contains(s, "Index does not exist")
}

// indexInfo 源列表展示用:缓存命中返回真实 (enabled, stale);未缓存返回乐观
// (true, false),由后台 indexProbeAsync 补正(不阻塞源列表,54s 冷探测由此消解)。
func (p *provider) indexInfo(ref topicRef) (enabled, stale bool) {
	ensureIndexPersist()
	indexCacheInst.mu.Lock()
	defer indexCacheInst.mu.Unlock()
	if e, ok := indexCacheInst.entries[ref.topicID]; ok && time.Now().Before(e.expires) {
		return e.enabled, e.stale
	}
	return true, false
}

// recentDataCount topic 近 window 的日志总量(DescribeHistogramV1)。用于判定
// 「停采」:仍有索引但近期无新数据。仅对有索引 topic 调用(无索引已判 disabled)。
func (p *provider) recentDataCount(ref topicRef, window time.Duration) (int64, error) {
	client := p.tlsClient(ref.region)
	now := time.Now()
	resp, err := client.DescribeHistogramV1(&tls.DescribeHistogramV1Request{
		TopicID:   ref.topicID,
		Query:     "*",
		StartTime: now.Add(-window).Unix(), // Unix 秒(同 SearchLogs)
		EndTime:   now.Unix(),
	})
	if err != nil {
		return 0, err
	}
	if resp == nil {
		return 0, nil
	}
	return resp.TotalCount, nil
}

// indexProbeAsync 后台异步探测 topic 索引与停采状态(不阻塞;未缓存且未在探才
// 调度)。用 context.Background——索引/停采缓存是进程级预热,独立于单次请求生命周期。
func (p *provider) indexProbeAsync(ref topicRef) {
	ensureIndexPersist()
	indexCacheInst.mu.Lock()
	if _, ok := indexCacheInst.entries[ref.topicID]; ok {
		indexCacheInst.mu.Unlock()
		return
	}
	if indexCacheInst.inflight[ref.topicID] {
		indexCacheInst.mu.Unlock()
		return
	}
	indexCacheInst.inflight[ref.topicID] = true
	indexCacheInst.mu.Unlock()

	go func() {
		indexProbeSem <- struct{}{}
		defer func() { <-indexProbeSem }()
		client := p.tlsClient(ref.region)
		_, err := client.DescribeIndex(&tls.DescribeIndexRequest{TopicID: ref.topicID})
		enabled := true
		if err != nil && isIndexNotExists(err) {
			enabled = false
		}
		stale := false
		if enabled {
			if total, err := p.recentDataCount(ref, staleWindow); err == nil && total == 0 {
				stale = true
			}
		}
		indexCacheInst.mu.Lock()
		indexCacheInst.entries[ref.topicID] = indexEntry{enabled: enabled, stale: stale, expires: time.Now().Add(indexTTL)}
		delete(indexCacheInst.inflight, ref.topicID)
		indexCacheInst.dirty = true
		indexCacheInst.mu.Unlock()
	}()
}

// indexKnownDisabled 仅当缓存已确认该 topic 无索引时返回 true(不含冷探测,以免
// 搜索/聚合冷路径被 DescribeIndex 拖慢;未知态返回 false 照常查询,由 IndexNotExists 隔离)。
func (p *provider) indexKnownDisabled(ref topicRef) bool {
	ensureIndexPersist()
	indexCacheInst.mu.Lock()
	defer indexCacheInst.mu.Unlock()
	if e, ok := indexCacheInst.entries[ref.topicID]; ok && !e.enabled && time.Now().Before(e.expires) {
		return true
	}
	return false
}

// sourceLabel 按 topic 名给源分型标签:火山 ALB 访问日志(`*_lb_access`)标 ALB,
// 其余自采访问日志(*_https/http_access、*-nginx-access、*-gateway-access)是实例
// nginx/gateway,不能混标 CLB——它们不是火山 CLB 产品日志。
func sourceLabel(topicName string) string {
	n := strings.ToLower(topicName)
	switch {
	case strings.Contains(n, "_lb_access"):
		return "ALB"
	case strings.Contains(n, "gateway"):
		return "Gateway"
	default:
		return "Nginx"
	}
}

// ListLogSources 枚举日志源:纯名字分型 + 按名去重(同名多 topic = 同一逻辑源
// 分片/重建)+ 检索索引标 Enabled(无索引源 Enabled=false,前端可灰度置灰)。
// 索引探测走后台异步(indexProbeAsync):源列表即时返回(54s 冷探测由此消解),
// 首次加载未缓存源乐观标 enabled,后台预热完成后刷新即看到真实的 false。
func (p *provider) ListLogSources(ctx context.Context, account *domain.CloudAccount) ([]logquery.LogSource, error) {
	refs := p.accessTopics(ctx, nil)
	if p.logType == logquery.LogTypeApp {
		refs = p.appTopics(ctx, nil)
	}
	seen := make(map[string]bool, len(refs))
	out := make([]logquery.LogSource, 0, len(refs))
	for _, ref := range refs {
		name := ref.topicName
		if name == "" {
			name = ref.topicID
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		p.indexProbeAsync(ref) // 后台预热(去重 + 限流),不阻塞返回
		enabled, stale := p.indexInfo(ref)
		label := sourceLabel(name) + " / " + name
		note := "火山 TLS 访问日志"
		if p.logType == logquery.LogTypeApp {
			label = "App / " + appServiceOf(name)
			note = "火山 TLS 应用业务日志(一服务一 topic)"
		}
		if !enabled {
			note = "该 topic 未开启检索索引,查询/聚合不可用"
		} else if stale {
			note = "近 7 天无新数据,可能已停采(历史可查)"
		}
		out = append(out, logquery.LogSource{
			Cloud:       domain.CloudProviderVolcengine,
			AccountID:   fmt.Sprintf("%d", p.account.ID),
			AccountName: p.account.Name,
			Region:      ref.region,
			LogType:     p.logType,
			ResourceID:  name,
			Name:        label,
			Enabled:     enabled,
			Stale:       stale,
			Note:        note,
		})
	}
	return out, nil
}

// Search 查询窗口内日志:逐 topic SearchLogs 分页拉取映射统一模型。
func (p *provider) Search(ctx context.Context, account *domain.CloudAccount, params logquery.SearchParams) ([]logquery.LogEntry, error) {
	if params.EndTime <= params.StartTime {
		return nil, fmt.Errorf("volcengine logquery: invalid time window")
	}
	limit := params.Limit
	if limit <= 0 || limit > 3000 {
		limit = 100
	}
	want := make(map[string]bool, len(params.Resources))
	for _, r := range params.Resources {
		if r != "" {
			want[r] = true
		}
	}
	targets := p.accessTopics(ctx, want)
	if p.logType == logquery.LogTypeApp {
		// 服务等值过滤 → 资源收敛(一服务一 topic,704 → 1),与下推并用
		if svcWant, _ := appResourceFilter(params.Filters); svcWant != nil {
			if len(want) == 0 {
				want = svcWant
			} else {
				for k := range svcWant {
					want[k] = true
				}
			}
		}
		targets = p.appTopics(ctx, want)
	}
	query := "*"
	if q := strings.TrimSpace(params.Query); q != "" && q != "*" {
		query = q
	}
	if p.logType == logquery.LogTypeApp {
		// 应用日志检索式下推:level 等值(TLS key-value 大小写不敏感,单形态
		// 全变体覆盖,实测证)与 query 合并;不可下推过滤仍留明细层 EntryMatches。
		query, _ = appPushdownQuery(query, params.Filters)
	}

	results := make([][]logquery.LogEntry, len(targets))
	var wg sync.WaitGroup
	// searchConcurrency 逐 topic 并发上限:app topic 数可达 704+(dev 舰队),
	// 无界 goroutine 会打爆 TLS QPS;32 并发下 704 topic 单页扇出 ~20s 量级。
	const searchConcurrency = 32
	sem := make(chan struct{}, searchConcurrency)
	// app 检索单 topic 页预算:一服务一 topic,联邦/RCA 采样只需每源近窗样本,
	// 单 topic 100 条(通常一页)足够,超量由 provider 级 3000 帽 + 时间降序归并
	// 截断;少翻页把 704 topic 扇出压进超时预算(实测 60s → 目标 <30s)。
	perTopicLimit := limit
	if p.logType == logquery.LogTypeApp && perTopicLimit > 100 {
		perTopicLimit = 100
	}
	for i, tgt := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if p.indexKnownDisabled(tgt) {
				return // 源清单已确认无索引:跳过,不打空请求(未知态照常查,由 IndexNotExists 隔离)
			}
			meta := logquery.LogMeta{
				Cloud:       domain.CloudProviderVolcengine,
				AccountID:   fmt.Sprintf("%d", p.account.ID),
				AccountName: p.account.Name,
				Region:      tgt.region,
				ResourceID:  tgt.topicName,
				Source:      tgt.region + "/" + tgt.topicID,
			}
			entries, err := p.searchLogs(ctx, tgt.region, tgt.topicID, params.StartTime, params.EndTime, query, perTopicLimit, meta)
			if err != nil {
				p.logger.Warn("[logquery-volcengine] search topic failed",
					elog.String("region", tgt.region), elog.String("topic", tgt.topicID), elog.FieldErr(err))
				return
			}
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

// searchLogs 单 topic SearchLogs 分页拉取映射(降序,凑满 limit 即停)。
// 注意:火山 TLS SearchLogs 的 StartTime/EndTime 单位为 Unix 秒(非毫秒,
// SDK TestSearchLogsV2Normally 以 time.Now().Unix() 传参并断言条数可证),
// 入参 startMs/endMs 为毫秒,须除 1000 否则窗口错位导致查询落空。
func (p *provider) searchLogs(ctx context.Context, region, topicID string, startMs, endMs int64, query string, limit int, meta logquery.LogMeta) ([]logquery.LogEntry, error) {
	client := p.tlsClient(region)
	var out []logquery.LogEntry
	offset := int64(0)
	const pageSize = 100
	for int64(len(out)) < int64(limit) {
		resp, err := client.SearchLogs(&tls.SearchLogsRequest{
			TopicID:   topicID,
			Query:     query,
			StartTime: startMs / 1000,
			EndTime:   endMs / 1000,
			Limit:     pageSize,
			Sort:      "desc",
			Offset:    &offset,
		})
		if err != nil {
			return nil, fmt.Errorf("tls searchlogs %s: %w", topicID, err)
		}
		if resp == nil || len(resp.Logs) == 0 {
			break
		}
		for _, raw := range resp.Logs {
			if p.logType == logquery.LogTypeApp {
				out = append(out, mapApp(meta, raw))
			} else {
				out = append(out, mapAccessLog(meta, raw))
			}
		}
		if len(resp.Logs) < pageSize || resp.ListOver {
			break
		}
		offset += int64(len(resp.Logs))
	}
	return out, nil
}
