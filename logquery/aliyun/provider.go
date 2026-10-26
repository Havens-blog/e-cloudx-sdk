// Package aliyun 阿里云日志查询 provider(Phase 1)。
//
// Phase 0 实测(source-status.md):阿里侧全部日志已汇聚 SLS,无需碰
// Kafka/日志文件包;每账号 8 类源按固定 catalog 查询:
//   - SLB:cn-shenzhen/jlc-lb-log(ALB 实例流 + 聚合流)、eu-central-1/jlc-prod-overseas-log(海外)
//   - WAF:eu-central-1 wafnew-project(WAF3.0 海外)、cn-hangzhou wafng-project
//     (WAF3.0 国内主库)、eu-central-1 Akamai 自采(jlc-prod-akamai-waf-log)
//   - CDN:cn-shenzhen dcdn-edge-rtlog-*(DCDN 边缘实时)、jlc-prod-cdn-log-monitor
//     (CDN 实时投递,与 DCDN 同构)、jlc-prod-cdn-log(离线转存,独立 PascalCase
//     schema,域名在 RequestURL)、eu-central-1 Akamai 自采(jlc-prod-akamai-cdn-log)
package aliyun

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
	sls "github.com/aliyun/aliyun-log-go-sdk"
	"github.com/gotomicro/ego/core/elog"
)

func init() {
	logquery.RegisterProvider(domain.CloudProviderAliyun, logquery.LogTypeSLB, newProvider(logquery.LogTypeSLB))
	logquery.RegisterProvider(domain.CloudProviderAliyun, logquery.LogTypeWAF, newProvider(logquery.LogTypeWAF))
	logquery.RegisterProvider(domain.CloudProviderAliyun, logquery.LogTypeCDN, newProvider(logquery.LogTypeCDN))
	logquery.RegisterProvider(domain.CloudProviderAliyun, logquery.LogTypeApp, newProvider(logquery.LogTypeApp))
}

// slsSource SLS 源 catalog 条目:project(含 logstore 为动态枚举)。
type slsSource struct {
	region   string
	project  string
	logType  logquery.LogType
	kind     mapperKind
	logstore string               // 空=ListLogStores 动态枚举(过滤 -metrics 等内部流)
	name     string               // 展示名(logstore 动态枚举时的前缀描述)
	fixed    []logquery.LogSource // 固定源(不枚举,直接给)
	note     string
	// mixedDomains 单 logstore 混装全部域名/host(需要按域名扇出查询):
	// CDN(DCDN 边缘实时/实时投递/Akamai 自采)与 WAF(WAF3.0 两渠道/
	// Akamai 自采)均混装;域名转存类 logstore 即单域名资源,整流查询即可。
	mixedDomains bool
}

// catalog 阿里侧 SLS 日志源清单(Phase 0 实测;project 名账号内稳定)。
var catalog = []slsSource{
	// ---- SLB(ALB 访问日志,topic=alb_layer7_access_log) ----
	{region: "cn-shenzhen", project: "jlc-lb-log", logType: logquery.LogTypeSLB, kind: kindALB,
		logstore: "", name: "ALB", note: "国内 ALB/CLB 访问日志(实例流+聚合)"},
	{region: "eu-central-1", project: "jlc-prod-overseas-log", logType: logquery.LogTypeSLB, kind: kindALB,
		logstore: "", name: "海外 ALB", note: "海外 ALB 访问日志"},
	// ---- WAF(单 store 混装全部 host,与 CDN 同为域名粒度选择) ----
	{region: "eu-central-1", project: "wafnew-project-1210557380197478-eu-central-1", logType: logquery.LogTypeWAF, kind: kindWAF3,
		logstore: "wafnew-logstore", name: "WAF3.0(海外)", note: "eu-central-1 WAF 日志库", mixedDomains: true},
	// 国内 WAF 主库(2026-09-07 实测:cn-hangzhou wafng-project,30 天 100+ 域名
	// 在流动;cn-shenzhen 云安全中心渠道 wafng-logstore 是空壳,30 天 0 行)
	{region: "cn-hangzhou", project: "wafng-project-1210557380197478-cn-hangzhou", logType: logquery.LogTypeWAF, kind: kindWAF3,
		logstore: "wafng-logstore", name: "WAF3.0(国内)", note: "国内 WAF 日志主库(cn-hangzhou)", mixedDomains: true},
	// Akamai 自采(Phase 0 实测两 store 均在 eu-central-1,误写 cn-shenzhen 会静默查空)
	{region: "eu-central-1", project: "jlc-prod-akamai-cdnwaf-log", logType: logquery.LogTypeWAF, kind: kindAkamaiWAF,
		logstore: "jlc-prod-akamai-waf-log", name: "Akamai WAF(自采)", note: "Akamai WAF 日志自采入库(CEF 展开)", mixedDomains: true},
	// ---- CDN ----
	{region: "eu-central-1", project: "jlc-prod-akamai-cdnwaf-log", logType: logquery.LogTypeCDN, kind: kindAkamaiCDN,
		logstore: "jlc-prod-akamai-cdn-log", name: "Akamai CDN(自采)", note: "Akamai CDN 日志自采入库", mixedDomains: true},
	{region: "cn-shenzhen", project: "dcdn-edge-rtlog-cn-42d9825f", logType: logquery.LogTypeCDN, kind: kindDCDN,
		logstore: "dcdn-edge-rtlog", name: "DCDN 边缘实时", note: "DCDN 实时日志", mixedDomains: true},
	// CDN(非 DCDN)实时投递(监控):schema 与 DCDN 边缘实时完全同构,复用 mapper 与域名扇出
	{region: "cn-shenzhen", project: "jlc-prod-cdn-log-monitor", logType: logquery.LogTypeCDN, kind: kindDCDN,
		logstore: "jlc-prod-cdn-log-monitor", name: "CDN 实时(监控投递)", note: "CDN 实时日志投递(监控),schema 同 DCDN", mixedDomains: true},
	// CDN 离线转存:logstore 即转存任务(单站点,PascalCase schema),动态枚举
	// (当前 jlcfa-his=www.jlcfa.com + api_forface3d_com;metrics/internal 流被过滤)
	{region: "cn-shenzhen", project: "jlc-prod-cdn-log", logType: logquery.LogTypeCDN, kind: kindCDNOffline,
		logstore: "", name: "CDN 离线转存", note: "CDN 离线日志转存(按转存任务)"},
	// ---- 应用业务日志(2026-10 实测:jlc-prod-log 每 <service>-business
	// store 单服务,java logback schema;RCA 服务间消费的数据源) ----
	{region: "cn-shenzhen", project: "jlc-prod-log", logType: logquery.LogTypeApp, kind: kindApp,
		logstore: "", name: "应用业务日志", note: "生产应用业务日志(服务级 store,service/level/message 契约字段)"},
}

// maxFanoutConcurrency logstore/域名扇出并发上限。混装源按域名/lb 实例扇出
// 时逐源各发一条 SLS 检索,8 并发在数百个空 store 场景(WAF 37 域名、ALB
// 140+ 实例)壁钟 ~20s;提高到 16 使波浪减半(单 project GetLogs QPS 仍宽松)。
const maxFanoutConcurrency = 16

// provider 阿里云 SLS 日志 provider(单账号)。
type provider struct {
	logType   logquery.LogType
	account   *domain.CloudAccount
	logger    *elog.Component
	clientsMu sync.Mutex
	clients   map[string]*sls.Client // region -> SLS client
}

// newProvider 按日志类型构造(SLS endpoint 随 region 切换,client 惰性按 region 建)。
func newProvider(logType logquery.LogType) logquery.ProviderCreator {
	return func(account *domain.CloudAccount) (logquery.LogProvider, error) {
		if account.AccessKeyID == "" || account.AccessKeySecret == "" {
			return nil, fmt.Errorf("aliyun logquery: account %d missing credentials", account.ID)
		}
		return &provider{
			logType: logType,
			account: account,
			logger:  elog.DefaultLogger,
		}, nil
	}
}

// clients 按 region 缓存 SLS client(endpoint = <region>.log.aliyuncs.com)。
func (p *provider) clientFor(region string) *sls.Client {
	p.clientsMu.Lock()
	defer p.clientsMu.Unlock()
	if p.clients == nil {
		p.clients = make(map[string]*sls.Client)
	}
	if c, ok := p.clients[region]; ok {
		return c
	}
	c := &sls.Client{
		Endpoint:        fmt.Sprintf("%s.log.aliyuncs.com", region),
		AccessKeyID:     p.account.AccessKeyID,
		AccessKeySecret: p.account.AccessKeySecret,
		RequestTimeOut:  20 * time.Second,
		RetryTimeOut:    40 * time.Second,
	}
	p.clients[region] = c
	return c
}

// Cloud 实现 LogProvider。
func (p *provider) Cloud() domain.CloudProvider { return domain.CloudProviderAliyun }

// LogType 实现 LogProvider。
func (p *provider) LogType() logquery.LogType { return p.logType }

// ListLogSources 枚举该账号该类型日志源:SLS ListLogStores + catalog 元数据。
// 混装源的域名枚举(SQL 分组 ~1.5s/条)并发执行且带 10 分钟缓存
// (串行曾是 WAF sources 5s 的主因);动态枚举 logstore 的 catalog 条目
// 同样并发。
func (p *provider) ListLogSources(ctx context.Context, account *domain.CloudAccount) ([]logquery.LogSource, error) {
	type enumTask struct {
		idx      int // 写入槽位
		src      slsSource
		logstore string // 动态枚举时为空
	}
	var (
		out   []logquery.LogSource
		fixed []logquery.LogSource // 非枚举条目(整源 + 占位)
		tasks []enumTask
		slots = make([][]logquery.LogSource, len(catalog))
	)
	for _, src := range catalog {
		if src.logType != p.logType {
			continue
		}
		if src.logstore != "" {
			s := p.toSource(src, src.logstore, true)
			if src.mixedDomains {
				s.Note = src.note + " · 全部域名(整源查询)"
			}
			fixed = append(fixed, s)
			if src.mixedDomains {
				// 混装源:并发跑域名枚举,槽位存子源
				tasks = append(tasks, enumTask{src: src})
			}
			continue
		}
		// 动态枚举 logstore:并发跑
		tasks = append(tasks, enumTask{src: src})
	}
	// 并发执行枚举任务(单任务失败隔离,记日志不阻塞;逐任务阶段耗时与
	// 缓存命中可见,可测回归)
	var wg sync.WaitGroup
	for i := range tasks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			t := &tasks[i]
			var (
				out []logquery.LogSource
				hit bool
			)
			if t.src.logstore != "" {
				// 固定 logstore 的混装源:域名枚举(带缓存)
				out, hit = p.domainSourcesCached(ctx, t.src)
			} else {
				// 动态枚举 logstore(logstore 清单分钟级稳定,同走缓存;
				// 曾每次 sources 都 ListLogStore ×2 project,SLB sources 1.9s 主因)
				stores, h := logstoreEnumCache.get(t.src.region+"/"+t.src.project, func() []string {
					ls, err := p.clientFor(t.src.region).ListLogStore(t.src.project)
					if err != nil {
						p.logger.Warn("[logquery-aliyun] list logstores failed",
							elog.String("project", t.src.project), elog.FieldErr(err))
						return nil
					}
					return ls
				})
				hit = h
				for _, store := range filterCatalogStores(t.src.kind, stores) {
					out = append(out, p.toSource(t.src, store, true))
				}
			}
			p.logger.Info("[logquery-aliyun] sources enum done",
				elog.String("kind", string(t.src.kind)),
				elog.String("project", t.src.project),
				elog.String("logstore", t.src.logstore),
				elog.Int("count", len(out)),
				elog.String("cache_hit", strconv.FormatBool(hit)),
				elog.Int64("duration_ms", time.Since(start).Milliseconds()))
			slots[i] = out
		}()
	}
	wg.Wait()
	out = append(out, fixed...)
	for _, s := range slots {
		out = append(out, s...)
	}
	return out, nil
}

// toSource catalog 条目 -> LogSource。
func (p *provider) toSource(src slsSource, logstore string, enabled bool) logquery.LogSource {
	return logquery.LogSource{
		Cloud:       domain.CloudProviderAliyun,
		AccountID:   fmt.Sprintf("%d", p.account.ID),
		AccountName: p.account.Name,
		Region:      src.region,
		LogType:     src.logType,
		ResourceID:  logstore,
		Name:        src.name + " / " + logstore,
		Enabled:     enabled,
		Note:        src.note,
	}
}

// domainSourcesCached 混装源活跃域名子源(带 10 分钟进程内缓存:
// 域名分布分钟级稳定,sources 每次切 Tab 重拉,缓存内零 API 调用)。
// 第二返回值为缓存命中标记(阶段耗时埋点)。
func (p *provider) domainSourcesCached(ctx context.Context, src slsSource) ([]logquery.LogSource, bool) {
	key := src.region + "/" + src.project + "/" + src.logstore + "/" + string(src.kind)
	domains, hit := domainEnumCache.get(key, func() []string {
		return p.enumDomains(ctx, src)
	})
	out := make([]logquery.LogSource, 0, len(domains))
	for _, d := range domains {
		s := p.toSource(src, d, true)
		s.Note = "活跃域名(近30天;低频域名近期可能无日志)"
		out = append(out, s)
	}
	return out, hit
}

// enumDomains 域名枚举:冷启动先小样本探查(百 ms~秒级)立即返回,30 天 SQL
// 全量分布后台刷新补全(SWR)。探针窗口/条数不能太苛刻 —— 曾用 24h×100 条,
// 冷启动只见最近日志里少数活跃域名(如 WAF 国内库 82 个域名,冷启动只返回
// 八九个),30 天全量要等后台刷新 + 二次拉取才出现,观感"只识别了几个",
// 且服务重启缓存清零会反复复现。改 7d×1000 后冷启动即覆盖绝大多数活跃
// 域名(SLS 千行日志读取仍百 ms~秒级);探查为空/失败时同步走 SQL 兜底。
func (p *provider) enumDomains(ctx context.Context, src slsSource) []string {
	// Akamai 自采(CEF 展开)store 体量大:raw 探查(7d×1000 反向分页)实测 ~15s
	// 且热点集中只出 7 个域名(DCDN/WAF 同探针百 ms 级,故才用探针);而该 store
	// 的 SQL group-by(30d,count 100)实测 ~400ms 且完整。此 kind 直接用 SQL
	// 枚举(快且全),不再走 raw 探查。
	if src.kind == kindAkamaiCDN || src.kind == kindAkamaiWAF {
		if domains := p.activeDomains(ctx, src); len(domains) > 0 {
			return domains
		}
	}
	now := time.Now().UnixMilli()
	probe, err := p.probeDomains(ctx, src, src.logstore, logquery.SearchParams{
		StartTime: now - 7*24*3600_000,
		EndTime:   now,
	}, 1000)
	if err != nil {
		p.logger.Warn("[logquery-aliyun] domain probe failed",
			elog.String("project", src.project), elog.String("logstore", src.logstore),
			elog.FieldErr(err))
	}
	if err == nil {
		if domains := domainNamesFromSample(src.kind, probe); len(domains) > 0 {
			p.refreshActiveDomainsAsync(src)
			return domains
		}
		// 近 24h 探查为空:回退 30 天 SQL 全量分布(同步,旧行为)
	}
	if domains := p.activeDomains(ctx, src); domains != nil {
		return domains
	}
	return domainNamesFromSample(src.kind, probe)
}

// activeDomainRefreshMu/Busy 30 天 SQL 域名分布后台刷新单飞(键与
// domainEnumCache 一致;provider 每请求新建,单飞标记须进程级)。
var (
	activeDomainRefreshMu   sync.Mutex
	activeDomainRefreshBusy = make(map[string]bool)
)

// refreshActiveDomainsAsync 后台补全 30 天活跃域名分布(成功且非空才覆盖
// 缓存;失败静默保留探查清单,activeDomains 内部已记 Warn)。
func (p *provider) refreshActiveDomainsAsync(src slsSource) {
	key := src.region + "/" + src.project + "/" + src.logstore + "/" + string(src.kind)
	activeDomainRefreshMu.Lock()
	if activeDomainRefreshBusy[key] {
		activeDomainRefreshMu.Unlock()
		return
	}
	activeDomainRefreshBusy[key] = true
	activeDomainRefreshMu.Unlock()
	go func() {
		defer func() {
			activeDomainRefreshMu.Lock()
			delete(activeDomainRefreshBusy, key)
			activeDomainRefreshMu.Unlock()
		}()
		// 脱离请求 ctx:请求返回后刷新仍要跑完
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		start := time.Now()
		domains := p.activeDomains(ctx, src)
		if len(domains) > 0 {
			domainEnumCache.set(key, domains)
		}
		p.logger.Info("[logquery-aliyun] active domains background refresh done",
			elog.String("project", src.project),
			elog.String("logstore", src.logstore),
			elog.Int("count", len(domains)),
			elog.Int64("duration_ms", time.Since(start).Milliseconds()))
	}()
}

// activeDomains SQL 分组枚举活跃域名(近 30 天,热度序,上限 100):
// 一条 select <维度>, count(1) group by 拿全量分布。窗口取 30 天对齐
// 域名目录口径(如 www.lcsc.com 近 7 天无流量但 30 天 8364 万条,枚举
// 窗口短了会漏列;选中近窗无流量的域名查询为空是真实数据,非 bug)。
func (p *provider) activeDomains(ctx context.Context, src slsSource) []string {
	expr := activeDomainExpr(src.kind)
	if expr == "" {
		return nil
	}
	client := p.clientFor(src.region)
	now := time.Now().UnixMilli()
	searchPart := buildAggregateSearchPart(src.kind, "", nil, src.logstore, src.project)
	sql := buildAggregateTopNSQL(searchPart, expr, "count(1)", 100)
	resp, err := client.GetLogsV2(src.project, src.logstore, &sls.GetLogRequest{
		From:  (now - 30*86400_000) / 1000,
		To:    now / 1000,
		Query: sql,
		Lines: 100,
	})
	if err != nil {
		p.logger.Warn("[logquery-aliyun] active domains sql failed",
			elog.String("project", src.project), elog.String("logstore", src.logstore), elog.FieldErr(err))
		return nil
	}
	out := make([]string, 0, len(resp.Logs))
	for _, row := range resp.Logs {
		if k := row["k"]; k != "" {
			out = append(out, k)
		}
	}
	return out
}

// Search 查询窗口内日志并映射统一模型:逐 catalog 查询,单源失败跳过并记日志,
// 结果按时间倒序(联邦编排层再做跨源归并)。
func (p *provider) Search(ctx context.Context, account *domain.CloudAccount, params logquery.SearchParams) ([]logquery.LogEntry, error) {
	if params.EndTime <= params.StartTime {
		return nil, fmt.Errorf("aliyun logquery: invalid time window")
	}
	limit := params.Limit
	if limit <= 0 || limit > 3000 {
		limit = 100 // SLS GetLogs 单次 Lines<=100,分页到 limit
	}
	resourceFilter := make(map[string]bool, len(params.Resources))
	for _, r := range params.Resources {
		resourceFilter[r] = true
	}
	regionFilter := make(map[string]bool, len(params.Regions))
	for _, r := range params.Regions {
		regionFilter[r] = true
	}

	// ---- 收集 fetch 目标(catalog x logstore) ----
	type fetchTarget struct {
		src      slsSource
		logstore string
	}
	var targets []fetchTarget
	for _, src := range catalog {
		if src.logType != p.logType {
			continue
		}
		if len(regionFilter) > 0 && !regionFilter[src.region] {
			continue
		}
		stores := []string{src.logstore}
		if src.logstore == "" {
			// logstore 清单走缓存(Search 每请求都来一遍,裸调用曾多耗 ~1s)
			ls, _ := logstoreEnumCache.get(src.region+"/"+src.project, func() []string {
				out, err := p.clientFor(src.region).ListLogStore(src.project)
				if err != nil {
					p.logger.Warn("[logquery-aliyun] list logstores failed",
						elog.String("project", src.project), elog.FieldErr(err))
					return nil
				}
				return out
			})
			stores = filterCatalogStores(src.kind, ls)
		}
		for _, ls := range stores {
			// mixedDomains 源的资源语义是"域名"(扇出内部过滤),logstore 名
			// 仅作整体开关;此处不按 logstore 过滤(否则选域名时混装源被整流跳过)
			// kindApp 资源语义是"服务名"(store=<service>-business),两者都放行
			if !src.mixedDomains && len(resourceFilter) > 0 &&
				!resourceFilter[ls] && !resourceFilter[src.project] &&
				!(src.kind == kindApp && resourceFilter[appServiceOf(ls)]) {
				continue
			}
			targets = append(targets, fetchTarget{src: src, logstore: ls})
		}
	}

	// ---- logstore 级并发拉取(8 并发上限,单源失败隔离) ----
	// CDN 类(DCDN/Akamai)单 logstore 混装全部域名:先小样本探查活跃域名,
	// 再按域名扇出并发查询(每域名各自凑配额,热点域名不挤占长尾);
	// 其余类(ALB/WAF)单流本身就是单源,直接整流查询。
	results := make([][]map[string]string, len(targets))
	sem := make(chan struct{}, maxFanoutConcurrency)
	var wg sync.WaitGroup
	for i, tgt := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var logs []map[string]string
			if tgt.src.mixedDomains {
				logs = p.fetchCDNByDomains(ctx, tgt.src, tgt.logstore, params, limit)
			} else {
				var err error
				logs, err = p.fetchLogs(ctx, tgt.src, tgt.logstore, params, limit)
				if err != nil {
					p.logger.Warn("[logquery-aliyun] fetch logs failed",
						elog.String("project", tgt.src.project), elog.String("logstore", tgt.logstore),
						elog.FieldErr(err))
					return
				}
			}
			results[i] = logs
		}()
	}
	wg.Wait()

	// ---- 映射(顺序,无锁) ----
	var entries []logquery.LogEntry
	for i, tgt := range targets {
		if len(results[i]) == 0 {
			continue
		}
		meta := logquery.LogMeta{
			Cloud:       domain.CloudProviderAliyun,
			AccountID:   fmt.Sprintf("%d", p.account.ID),
			AccountName: p.account.Name,
			Region:      tgt.src.region,
			ResourceID:  tgt.logstore,
			Source:      tgt.src.project + "/" + tgt.logstore,
		}
		for _, raw := range results[i] {
			if e := mapEntry(tgt.src.kind, meta, raw); e != nil {
				// 字段筛选:映射后统一字段语义过滤(采样量级逐条,跨源一致)
				if logquery.EntryMatches(e, params.Filters) {
					entries = append(entries, e)
				}
			}
		}
	}
	// 时间倒序;总硬顶与联邦级一致(ADR D4:limit 为**每日志源**上限,
	// 多源归并后按时间排序,全局 1000 防响应过大;不做 provider 级总截断,
	// 否则热点源会吃掉全部配额,其他源(域名)被挤出局)
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].GetTimestamp() > entries[j].GetTimestamp()
	})
	const providerCap = 3000
	if len(entries) > providerCap {
		entries = entries[:providerCap]
	}
	return entries, nil
}

// Aggregate 窗口内真实聚合(plan.md §8):每 logstore 两条检索|SQL——
// 时间分桶(求和即精确总数)+ TopN(按 kind 维度),百万/千万级行下推
// SLS 分析引擎,只回传分桶与分组结果。单 logstore 失败跳过并记日志。
func (p *provider) Aggregate(ctx context.Context, account *domain.CloudAccount, params logquery.AggregateParams) (*logquery.AggregateResult, error) {
	if params.EndTime <= params.StartTime {
		return nil, fmt.Errorf("aliyun logquery aggregate: invalid time window")
	}
	if params.BucketSec <= 0 {
		params.BucketSec = logquery.PickBucketSec(params.StartTime, params.EndTime)
	}
	resourceFilter := make(map[string]bool, len(params.Resources))
	for _, r := range params.Resources {
		resourceFilter[r] = true
	}

	// ---- 收集聚合目标(catalog x logstore;与 Search 的资源过滤语义一致) ----
	type aggrTarget struct {
		src      slsSource
		logstore string
	}
	var targets []aggrTarget
	for _, src := range catalog {
		if src.logType != p.logType {
			continue
		}
		stores := []string{src.logstore}
		if src.logstore == "" {
			ls, _ := logstoreEnumCache.get(src.region+"/"+src.project, func() []string {
				out, err := p.clientFor(src.region).ListLogStore(src.project)
				if err != nil {
					p.logger.Warn("[logquery-aliyun] aggregate list logstores failed",
						elog.String("project", src.project), elog.FieldErr(err))
					return nil
				}
				return out
			})
			stores = filterCatalogStores(src.kind, ls)
		}
		for _, ls := range stores {
			// 与 Search 相同:混装源的资源语义是域名(拼进检索段),整 store 条目放行;
			// kindApp 资源语义是"服务名"(store=<service>-business),两者都放行
			if !src.mixedDomains && len(resourceFilter) > 0 &&
				!resourceFilter[ls] && !resourceFilter[src.project] &&
				!(src.kind == kindApp && resourceFilter[appServiceOf(ls)]) {
				continue
			}
			targets = append(targets, aggrTarget{src: src, logstore: ls})
		}
	}

	// ---- 预编译 kind → 筛选检索段:字段不可下推的 kind 整源跳过 ----
	// catalog 内同 类型(logType) 多 kind(CDN: DCDN/Akamai/转存),缺哪个
	// kind 记入 skipKinds,整源不参与聚合(否则未过滤的计数失真)。
	filterParts := make(map[mapperKind]string, len(targets))
	var skipFilter []string
	for _, tgt := range targets {
		if _, ok := filterParts[tgt.src.kind]; ok {
			continue
		}
		part, ok := filterSearchPart(tgt.src.kind, params.Filters)
		if !ok {
			skipFilter = append(skipFilter, string(tgt.src.kind))
			continue
		}
		filterParts[tgt.src.kind] = part
	}

	// ---- logstore 级并发聚合(单源失败隔离) ----
	results := make([]*logquery.AggregateResult, len(targets))
	sem := make(chan struct{}, maxFanoutConcurrency)
	var wg sync.WaitGroup
	for i, tgt := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 字段筛选不可下推的 kind:跳过(provider 级整体报错误,防空数)
			if _, ok := filterParts[tgt.src.kind]; !ok {
				return
			}
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = p.aggregateStore(ctx, tgt.src, tgt.logstore, params, filterParts[tgt.src.kind])
		}()
	}
	wg.Wait()

	merged := &logquery.AggregateResult{}
	// 指标可加性:count/sum_bytes 按值求和;avg/p99 非可加,跨源按
	// count 加权均值归并(单源分组只承载自身,避免直接求和失真)。
	weighted := metricIsWeighted(params.Metric)
	if len(skipFilter) > 0 {
		merged.SkipFilterReason = "字段筛选无法下推: " + strings.Join(skipFilter, ",")
	}
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
	// 跨 logstore 的 TopN 归并(同名聚合,取前 10)
	if len(merged.TopN) > 0 {
		type acc struct {
			count int64
			value float64
		}
		groups := make(map[string]acc, len(merged.TopN))
		for _, t := range merged.TopN {
			g := groups[t.Name]
			g.count += t.Count
			if weighted {
				g.value += t.Value * float64(t.Count) // 加权和(归并后除回)
			} else {
				g.value += t.Value // 可加指标(count/sum_bytes)直接求和
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
	if cap := logquery.AggregateTopN(params.TopNLimit); len(merged.TopN) > cap {
		merged.TopN = merged.TopN[:cap]
	}
	if merged.SkipFilterReason != "" {
		return nil, fmt.Errorf("%s", merged.SkipFilterReason)
	}
	return merged, nil
}

// aggregateStore 单 logstore 两条 SQL:分桶 + TopN(自定义维度/指标;无维度
// 的源跳过 TopN)。失败返回 nil(隔离,不阻塞其他 logstore)。
func (p *provider) aggregateStore(ctx context.Context, src slsSource, logstore string, params logquery.AggregateParams, filterPart string) *logquery.AggregateResult {
	client := p.clientFor(src.region)
	// 整源选择(Resources 含本 logstore/project)时不加域名过滤
	searchPart := buildAggregateSearchPart(src.kind, params.Query, params.Resources, logstore, src.project)
	if filterPart != "" {
		searchPart = appendSearchPart(searchPart, filterPart)
	}
	from := params.StartTime / 1000
	to := params.EndTime / 1000

	result := &logquery.AggregateResult{}
	// 分桶:t 为桶起点 Unix 秒,c 为精确条数;Total = 求和
	bucketSQL := buildAggregateBucketSQL(searchPart, params.BucketSec)
	resp, err := client.GetLogsV2(src.project, logstore, &sls.GetLogRequest{
		From: from, To: to, Query: bucketSQL, Lines: 200,
	})
	if err != nil {
		p.logger.Warn("[logquery-aliyun] aggregate bucket sql failed",
			elog.String("project", src.project), elog.String("logstore", logstore), elog.FieldErr(err))
		return nil
	}
	for _, row := range resp.Logs {
		sec := logquery.Int(row["t"])
		if sec <= 0 && row["t"] != "0" {
			continue
		}
		result.Buckets = append(result.Buckets, logquery.AggregateBucket{
			Timestamp: sec * 1000,
			Count:     logquery.Int(row["c"]),
		})
		result.Total += logquery.Int(row["c"])
	}

	// TopN 维度:自定义 dimension 优先,缺省回退 kind 默认维度(现行为兼容)
	dim := aggregateTopNExpr(src.kind)
	if params.Dimension != "" {
		expr, ok := dimensionExpr(src.kind, params.Dimension)
		if !ok {
			result.TopNSkipReason = "维度 " + params.Dimension + " 该源不支持"
			return result
		}
		dim = expr
	}
	if dim == "" {
		return result // 无维度源(Akamai WAF3 访问流等)跳过 TopN
	}
	// 透传自定义维度:先校验列已建分析索引(未索引的列 Group By 报 400),
	// 给出可用字段清单而非盲试报错。索引探测失败时降级放行。
	if col, ok := passthroughColumn(src.kind, params.Dimension); ok {
		if keys := p.storeIndexKeys(src.region, src.project, logstore); len(keys) > 0 {
			if hint := indexHint(col, keys); hint != "" {
				result.TopNSkipReason = hint
				return result
			}
		}
	}
	mExpr, ok := metricSQLExpr(src.kind, params.Metric)
	if !ok {
		result.TopNSkipReason = "指标 " + params.Metric + " 该源不支持"
		return result
	}
	topn := logquery.AggregateTopN(params.TopNLimit)
	topnSQL := buildAggregateTopNSQL(searchPart, dim, mExpr, topn)
	resp, err = client.GetLogsV2(src.project, logstore, &sls.GetLogRequest{
		From: from, To: to, Query: topnSQL, Lines: int64(topn),
	})
	if err != nil {
		// 常见于自定义列名不存在或非索引列:显式标注(趋势/总数不受影响),
		// 不静默报错也不给 0 条误导
		p.logger.Warn("[logquery-aliyun] aggregate topn sql failed",
			elog.String("project", src.project), elog.String("logstore", logstore), elog.FieldErr(err))
		if params.Dimension != "" && result.TopNSkipReason == "" {
			result.TopNSkipReason = "维度 " + params.Dimension + " 查询失败(列不存在或非索引):" + briefErr(err)
		}
		return result
	}
	for _, row := range resp.Logs {
		if k := row["k"]; k != "" {
			item := logquery.TopNItem{Name: k, Count: logquery.Int(row["n"])}
			if metricIsCount(params.Metric) {
				item.Value = float64(item.Count)
			} else if v, err2 := strconv.ParseFloat(row["v"], 64); err2 == nil {
				item.Value = v
			}
			result.TopN = append(result.TopN, item)
		}
	}
	return result
}

// fetchLogs 单 logstore 分页拉取(SLS 单次 Lines<=100)。
func (p *provider) fetchLogs(ctx context.Context, src slsSource, logstore string, params logquery.SearchParams, limit int) ([]map[string]string, error) {
	client := p.clientFor(src.region)
	// SLS 查询窗口为秒
	from := params.StartTime / 1000
	to := params.EndTime / 1000
	query := buildQuery(src.kind, params.Query)
	var all []map[string]string
	offset := int64(0)
	const pageSize = 100
	for int64(len(all)) < int64(limit) {
		resp, err := client.GetLogsV2(src.project, logstore, &sls.GetLogRequest{
			From:    from,
			To:      to,
			Query:   query,
			Lines:   pageSize,
			Offset:  offset,
			Reverse: true, // 时间倒序
		})
		if err != nil {
			return nil, fmt.Errorf("sls get_logs %s/%s: %w", src.project, logstore, err)
		}
		if len(resp.Logs) == 0 {
			break
		}
		all = append(all, resp.Logs...)
		if len(resp.Logs) < pageSize || !resp.IsComplete() {
			break
		}
		offset += int64(len(resp.Logs))
	}
	return all, nil
}

// topicFilter kind 专属 __topic__ 过滤(服务端过滤:聚合 project 里混有
// nacos/app 等非 LB 日志流,topic 是 ALB/WAF 访问日志的稳定判据;无 topic
// 的源(DCDN rtlog/Akamai/转存)不加过滤)。
func topicFilter(kind mapperKind) string {
	switch kind {
	case kindALB:
		return "__topic__:alb_layer7_access_log"
	case kindWAF3:
		return "__topic__:waf_access_log"
	}
	return ""
}

// buildQuery 原始查询式 + kind 专属 __topic__ 过滤。
func buildQuery(kind mapperKind, userQuery string) string {
	topic := topicFilter(kind)
	q := strings.TrimSpace(userQuery)
	if q == "" || q == "*" {
		if topic == "" {
			return "*"
		}
		return topic
	}
	if topic == "" {
		return q
	}
	return q + " and " + topic
}

// ---------------------------------------------------------------------
// CDN 按域名扇出(单 logstore 混装全部域名,热点域名会吃掉整窗配额——
// 100 条样本实测单一域名占 51%。按域名拆分并发查询,每域名各自凑配额,
// 长尾域名不被挤出结果)
// ---------------------------------------------------------------------

// domainField 按 kind 返回域名原始字段名(单测覆盖)。
func domainField(kind mapperKind) string {
	switch kind {
	case kindDCDN:
		return "domain"
	case kindAkamaiCDN:
		return "reqHost"
	case kindWAF3:
		return "host"
	case kindAkamaiWAF:
		return "dhost"
	default:
		return ""
	}
}

// splitByDomain 把探查样本按域名分组(保序:域名首现顺序即热度序)。
func splitByDomain(kind mapperKind, logs []map[string]string) map[string][]map[string]string {
	field := domainField(kind)
	if field == "" {
		return nil
	}
	grouped := make(map[string][]map[string]string)
	for _, l := range logs {
		d := l[field]
		if d == "" {
			continue
		}
		grouped[d] = append(grouped[d], l)
	}
	return grouped
}

// domainNamesFromSample 从探查样本提取域名清单(热度序:样本数降序)。
// ListLogSources 域名枚举与 Search 域名扇出共用此排序。
func domainNamesFromSample(kind mapperKind, probe []map[string]string) []string {
	grouped := splitByDomain(kind, probe)
	domains := make([]string, 0, len(grouped))
	for d := range grouped {
		domains = append(domains, d)
	}
	sort.Slice(domains, func(i, j int) bool {
		return len(grouped[domains[i]]) > len(grouped[domains[j]])
	})
	return domains
}

// probeDomains logstore 探查:小样本拉取活跃域名集合(probeLimit 条,
// 兼作首批数据;探查失败返回 nil——扇出退化为整 store 查询)。
func (p *provider) probeDomains(ctx context.Context, src slsSource, logstore string, params logquery.SearchParams, probeLimit int) ([]map[string]string, error) {
	return p.fetchLogs(ctx, src, logstore, params, probeLimit)
}

// fetchPerDomain 单域名查询:topic 过滤 + 用户检索式 + domain 过滤,凑满 limit 即停。
func (p *provider) fetchPerDomain(ctx context.Context, src slsSource, logstore, domain string, params logquery.SearchParams, limit int) ([]map[string]string, error) {
	client := p.clientFor(src.region)
	from := params.StartTime / 1000
	to := params.EndTime / 1000
	// topic 过滤必须随扇出保留(WAF3 渠道 store 混有噪声流),否则按 host
	// 查询会把噪声流也算进来
	var parts []string
	if t := topicFilter(src.kind); t != "" {
		parts = append(parts, t)
	}
	if u := strings.TrimSpace(params.Query); u != "" && u != "*" {
		parts = append(parts, "("+u+")")
	}
	parts = append(parts, domainField(src.kind)+": "+domain)
	q := strings.Join(parts, " and ")
	var all []map[string]string
	offset := int64(0)
	const pageSize = 100
	for int64(len(all)) < int64(limit) {
		resp, err := client.GetLogsV2(src.project, logstore, &sls.GetLogRequest{
			From:    from,
			To:      to,
			Query:   q,
			Lines:   pageSize,
			Offset:  offset,
			Reverse: true,
		})
		if err != nil {
			return nil, fmt.Errorf("sls get_logs %s/%s domain=%s: %w", src.project, logstore, domain, err)
		}
		if len(resp.Logs) == 0 {
			break
		}
		all = append(all, resp.Logs...)
		if len(resp.Logs) < pageSize || !resp.IsComplete() {
			break
		}
		offset += int64(len(resp.Logs))
	}
	return all, nil
}

// fetchCDNByDomains CDN 类按域名扇出:
//  1. 探查(probeLimit 条,兼作首批数据)-> 域名集合(热度序);
//  2. 用户指定 Resources 时按其过滤域名(同时作为唯一域名清单);
//  3. 逐域名并发(8 上限)查询,每域名各自凑满 limit;
//  4. 探查失败降级为整 store 查询(总截断到 limit,退回旧行为)。
func (p *provider) fetchCDNByDomains(ctx context.Context, src slsSource, logstore string, params logquery.SearchParams, limit int) []map[string]string {
	const probeLimit = 100
	probe, err := p.probeDomains(ctx, src, logstore, params, probeLimit)
	if err != nil {
		p.logger.Warn("[logquery-aliyun] cdn probe failed, fallback to whole-store query",
			elog.String("project", src.project), elog.String("logstore", logstore),
			elog.FieldErr(err))
		logs, ferr := p.fetchLogs(ctx, src, logstore, params, limit)
		if ferr != nil {
			p.logger.Warn("[logquery-aliyun] fetch logs failed",
				elog.String("project", src.project), elog.String("logstore", logstore),
				elog.FieldErr(ferr))
			return nil
		}
		return logs
	}

	// 域名清单 = 探查结果分组(保热度序);探查样本直接计入对应域名配额
	grouped := splitByDomain(src.kind, probe)
	if len(grouped) == 0 {
		return probe
	}
	domains := domainNamesFromSample(src.kind, probe)

	// 用户指定资源过滤:域名清单收敛为其交集。
	// Resources 可能是域名(混装源的展示/选择粒度)或 logstore 名(选中整个
	// 混装源)——logstore 名等于本源自身时视为"不过滤域名,查全源"。
	if len(params.Resources) > 0 {
		wholeSource := false
		allowed := make(map[string]bool, len(params.Resources))
		for _, r := range params.Resources {
			if r == logstore || r == src.project {
				wholeSource = true
			}
			allowed[r] = true
		}
		if wholeSource {
			allowed = nil // 整源:不限域名
		}
		if allowed != nil {
			filtered := domains[:0]
			for _, d := range domains {
				if allowed[d] {
					filtered = append(filtered, d)
				}
			}
			if len(filtered) == 0 {
				// 指定资源不在探查样本中(低频域名):按指定清单全量查
				for _, r := range params.Resources {
					filtered = append(filtered, r)
				}
			}
			domains = filtered
		}
	}

	// 逐域名并发,每域名配额 = limit - 探查已得(探查样本均分抵扣)
	type domainResult struct {
		logs []map[string]string
	}
	perDomainQuota := limit / len(domains)
	if perDomainQuota < 1 {
		perDomainQuota = 1
	}
	results := make([]domainResult, len(domains))
	sem2 := make(chan struct{}, maxFanoutConcurrency)
	var wg2 sync.WaitGroup
	for i, d := range domains {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			sem2 <- struct{}{}
			defer func() { <-sem2 }()
			quota := perDomainQuota
			if got := len(grouped[d]); got < quota {
				quota -= got // 探查已贡献部分配额
			} else {
				results[i] = domainResult{logs: grouped[d]}
				return // 探查样本已凑满该域名配额
			}
			logs, err := p.fetchPerDomain(ctx, src, logstore, d, params, quota)
			if err != nil {
				p.logger.Warn("[logquery-aliyun] per-domain fetch failed",
					elog.String("domain", d), elog.FieldErr(err))
				// 单域名失败隔离:退回探查样本保底
			}
			// 合并探查样本 + 域名查询(同一窗口都从最新开始,重叠是常态,
			// 按 request_id/组合键去重,否则趋势图条数虚高)
			merged := append(append([]map[string]string{}, grouped[d]...), logs...)
			results[i] = domainResult{logs: dedupLogs(merged)}
		}()
	}
	wg2.Wait()

	var out []map[string]string
	for i := range domains {
		out = append(out, results[i].logs...)
	}
	return out
}

// dedupLogs 探查样本与按域名查询重叠去重。键 = 全字段指纹(键名排序拼接):
// 探查与单域名查询同一窗口且都从最新开始,重叠行字段集完全相同;不同请求
// 必有 request_id/时间/UA 等差异,不会误合。此前用 CDN 字段名(domain/
// unixtime)做组合键,WAF 行字段名不同全落空键,整源被去重到只剩 1 条。
func dedupLogs(logs []map[string]string) []map[string]string {
	if len(logs) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(logs))
	out := make([]map[string]string, 0, len(logs))
	for _, l := range logs {
		k := logKey(l)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, l)
	}
	return out
}

// logKey 全字段指纹(键名排序拼接,与字段名无关,全 kind 通用)。
func logKey(l map[string]string) string {
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(l[k])
		b.WriteByte('|')
	}
	return b.String()
}

// filterCatalogStores 动态枚举 logstore 的 kind 感知过滤:
//   - 基础:SLS 内部流(-metrics/-metrics-result/internal-*)
//   - ALB:混装项目(jlc-lb-log)混有 app 业务日志(-business/-tomcat-access)
//     与 K8s 控制面流(apiserver/ccm/controlplane/kcm/scheduler);这些 store
//     没有 alb_layer7_access_log 主题,但逐条全扫描(空 store ~350ms/条)仍会
//     拖慢联邦查询,且污染 sources 清单。
func filterCatalogStores(kind mapperKind, stores []string) []string {
	out := make([]string, 0, len(stores))
	for _, ls := range stores {
		if isInternalStore(ls) {
			continue
		}
		if kind == kindALB && nonALBStore(ls) {
			continue
		}
		// kindApp 只收 <service>-business 业务流(nacos/内部流/ALB 流全部排除;
		// 命名约定即"一 store 一服务",服务名 = store 去 -business 后缀)
		if kind == kindApp && !strings.HasSuffix(ls, "-business") {
			continue
		}
		out = append(out, ls)
	}
	return out
}

// appServiceOf 应用业务 store 的服务名(<service>-business -> <service>)。
// Search/Aggregate 的资源过滤同时接受服务名与 store 名(RCA 消费方只知道
// 服务名,不应感知 store 命名约定)。
func appServiceOf(logstore string) string {
	return strings.TrimSuffix(logstore, "-business")
}

func isInternalStore(ls string) bool {
	return strings.HasSuffix(ls, "-metrics") || strings.HasSuffix(ls, "-metrics-result") ||
		strings.HasPrefix(ls, "internal-")
}

// nonALBStore ALB(负载均衡)类型下,混装项目里非 LB 访问日志的 app/控制面流。
func nonALBStore(ls string) bool {
	if strings.Contains(ls, "-business") || strings.Contains(ls, "-tomcat-access") {
		return true
	}
	for _, p := range []string{"apiserver-", "ccm-", "controlplane-", "kcm-", "scheduler-"} {
		if strings.HasPrefix(ls, p) {
			return true
		}
	}
	return false
}
