// 聚合能力(plan.md §8):DescribeHistogramV1 原生时间分桶,不依赖字段分析索引
// (火山自采 topic 多未建分析索引,SearchLogs SQL 会报 IndexNotExists,而
// DescribeHistogram 只需检索维度)。TopN 仍须字段分析索引,火山跳过并标注。
package volcengine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/Havens-blog/e-cloudx-sdk/logquery"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volc-sdk-golang/service/tls"
)

// histogramConcurrency DescribeHistogram 并发度(逐 topic 一请求)。火山 app
// 联邦实际枚举 2158 topic(多 region,2026-10 实测),8 并发全扇出 ≈ 48s、
// 16 并发 ≈ 38s(实测),均超联邦 30s 预算;32 并发与 Search 同级(生产
// 实测无 429;单 topic 失败仅告警跳过,不阻塞整体)。
const histogramConcurrency = 32

// Aggregate 窗口真实聚合:逐 topic DescribeHistogramV1 拉原生分桶,按 bucketSec
// 归并到对齐桶(跨源/跨云步长一致,与联邦逐桶求和口径对齐)。Total = 分桶求和。
// 字段筛选不可下推:整源跳过(否则计数失真);自定义维度不可下推:仅标注 TopN
// 缺失(趋势/总数仍有效)。
func (p *provider) Aggregate(ctx context.Context, account *domain.CloudAccount, params logquery.AggregateParams) (*logquery.AggregateResult, error) {
	if params.EndTime <= params.StartTime {
		return nil, fmt.Errorf("volcengine logquery aggregate: invalid time window")
	}
	query := "*"
	if q := strings.TrimSpace(params.Query); q != "" && q != "*" {
		query = q
	}
	isApp := p.logType == logquery.LogTypeApp
	isK8s := p.logType == logquery.LogTypeK8sEvent
	if len(params.Filters) > 0 {
		if isK8s {
			// K8s 事件:level(->type 原值)与 reason 等值可下推;
			// 其余过滤不可下推,整源跳过避免计数失真(诚实优先)
			var pushable bool
			query, pushable = k8sEventPushdownQuery(query, params.Filters)
			if !pushable {
				return nil, fmt.Errorf("volcengine: K8s 事件仅 level/reason 等值可下推聚合(跳过该源,避免计数失真)")
			}
		} else if !isApp {
			return nil, fmt.Errorf("volcengine: 字段筛选无法下推聚合(跳过该源,避免计数失真)")
		} else {
			// 应用日志:level 等值可下推(TLS key-value 检索大小写不敏感,实测证);
			// 其余过滤不可下推仍整源跳过,避免计数失真(诚实优先)。
			var pushable bool
			query, pushable = appPushdownQuery(query, params.Filters)
			if !pushable {
				return nil, fmt.Errorf("volcengine: 存在无法下推的字段筛选(仅 level 等值可下推),跳过该源避免计数失真")
			}
		}
	}
	if params.BucketSec <= 0 {
		params.BucketSec = logquery.PickBucketSec(params.StartTime, params.EndTime)
	}
	want := make(map[string]bool, len(params.Resources))
	for _, r := range params.Resources {
		if r != "" {
			want[r] = true
		}
	}
	targets := p.accessTopics(ctx, want)
	if isApp {
		// 服务等值过滤 → 资源收敛(一服务一 topic):GetLogCount 类按服务计数
		// 从 704 topic 全扇出收敛到单 topic,计数语义不变。
		if svcWant, ok := appResourceFilter(params.Filters); ok && svcWant != nil {
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
	if isK8s {
		targets = p.k8sEventTopics(ctx, want)
	}

	var (
		mu      sync.Mutex
		infos   []tls.HistogramInfoV1
		sample  []logquery.LogEntry
		sampled int
		// scanCovered/scanTotal 采样覆盖面(等距抽样的 topic 数/全部 topic 数),
		// 写入 TopNNote 供消费方判断分布代表性。
		scanCovered, scanTotal int
	)
	scanDim := isApp && (params.Dimension == "service" || params.Dimension == "trace_id" || params.Dimension == "level")
	if isK8s && k8sEventScanDims[params.Dimension] {
		// K8s 事件:火山无 SQL 分组,全部维度走窗口采样(与 app 同口径,
		// TopNNote 标注采样覆盖面)
		scanDim = true
	}
	if scanDim && !metricIsCountVolc(params.Metric) {
		// 采样行上非 count 指标不可信,与阿里扫描路径同口径诚实拒绝
		scanDim = false
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, histogramConcurrency)
	for _, tgt := range targets {
		wg.Add(1)
		go func(tgt topicRef) {
			defer wg.Done()
			if p.indexKnownDisabled(tgt) {
				return // 源清单已确认无索引:跳过
			}
			sem <- struct{}{}
			defer func() { <-sem }()
			client := p.tlsClient(tgt.region)
			// StartTime/EndTime 单位 Unix 秒(与 SearchLogs 同,SDK 测试可证);
			// Interval 不传,由 TLS 自动切分,归并侧再对齐到 bucketSec。
			resp, err := client.DescribeHistogramV1(&tls.DescribeHistogramV1Request{
				TopicID:   tgt.topicID,
				Query:     query,
				StartTime: params.StartTime / 1000,
				EndTime:   params.EndTime / 1000,
			})
			if err != nil {
				p.logger.Warn("[logquery-volcengine] aggregate histogram failed",
					elog.String("topic", tgt.topicID), elog.FieldErr(err))
				return
			}
			if resp == nil {
				return
			}
			mu.Lock()
			infos = append(infos, resp.HistogramInfos...)
			mu.Unlock()
		}(tgt)
	}
	// 扫描型维度(app level/service/trace_id:无字段分析索引,SQL 分组不可
	// 用):与分桶并发执行采样扇出(总墙钟 = max 而非 sum,挤进联邦 30s
	// 预算),分组值来自窗口内采样,口径以 TopNNote 标注。
	if scanDim {
		// 采样目标等距抽样:dev 舰队 704 topic 多数窗口内无数据,采样量早停
		// 失效(实测全扇出 40s+ 超联邦预算);按枚举序等距抽样公平覆盖服务
		// 名字母域,避免按前缀偏置 TopN。抽样面并入 TopNNote。
		scanTargets := targets
		scanTotal = len(targets)
		const maxScanTopics = 128
		if len(targets) > maxScanTopics {
			scanTargets = make([]topicRef, 0, maxScanTopics)
			step := float64(len(targets)) / float64(maxScanTopics)
			for i := 0; i < maxScanTopics; i++ {
				scanTargets = append(scanTargets, targets[int(float64(i)*step)])
			}
		}
		scanCovered = len(scanTargets)
		const (
			scanConcurrency  = 16
			scanSampleCap    = 2000
			scanPerTopicRows = 100
		)
		var scanWG sync.WaitGroup
		scanSem := make(chan struct{}, scanConcurrency)
		for _, tgt := range scanTargets {
			if ctx.Err() != nil {
				break // 联邦预算耗尽:停止新扇出,已在途的照常归并
			}
			mu.Lock()
			full := sampled >= scanSampleCap
			mu.Unlock()
			if full {
				break // 采样预算用尽,不再扇出
			}
			scanWG.Add(1)
			go func(tgt topicRef) {
				defer scanWG.Done()
				mu.Lock()
				if sampled >= scanSampleCap {
					mu.Unlock()
					return
				}
				mu.Unlock()
				if p.indexKnownDisabled(tgt) {
					return
				}
				scanSem <- struct{}{}
				defer func() { <-scanSem }()
				meta := logquery.LogMeta{
					Cloud:       domain.CloudProviderVolcengine,
					AccountID:   fmt.Sprintf("%d", p.account.ID),
					AccountName: p.account.Name,
					Region:      tgt.region,
					ResourceID:  tgt.topicName,
					Source:      tgt.region + "/" + tgt.topicID,
				}
				entries, err := p.searchLogs(context.Background(), tgt.region, tgt.topicID,
					params.StartTime, params.EndTime, query, scanPerTopicRows, meta)
				if err != nil {
					p.logger.Warn("[logquery-volcengine] scan topn sample failed",
						elog.String("topic", tgt.topicID), elog.FieldErr(err))
					return
				}
				mu.Lock()
				if sampled < scanSampleCap {
					take := len(entries)
					if take > scanSampleCap-sampled {
						take = scanSampleCap - sampled
					}
					sample = append(sample, entries[:take]...)
					sampled += take
				}
				mu.Unlock()
			}(tgt)
		}
		scanWG.Wait()
	}
	wg.Wait()

	buckets, total := flattenHistogram(infos, params.BucketSec*1000)
	result := &logquery.AggregateResult{Total: total, Buckets: buckets}
	if params.Dimension != "" {
		if scanDim {
			result.TopN = logquery.TopNFromEntries(sample, params.Dimension, params.TopNLimit)
			switch {
			case len(result.TopN) > 0:
				result.TopNNote = fmt.Sprintf("TopN 基于窗口内采样 %d 条(等距抽样 %d/%d topic),非精确全量分布",
					sampled, scanCovered, scanTotal)
			case sampled == 0:
				result.TopNSkipReason = "维度 " + params.Dimension + " 窗口内无采样条目"
			default:
				result.TopNSkipReason = "维度 " + params.Dimension + " 采样条目均无该维度值"
			}
		} else {
			// 火山源无字段分析索引,该维度取不到(SQL 路径不存在)
			result.TopNSkipReason = "火山源无字段分析索引,不支持该维度分组"
		}
	}
	return result, nil
}

// metricIsCountVolc 火山侧 count 指标判定(仅 count 可用于采样分组)。
func metricIsCountVolc(metric string) bool {
	return metric == "" || metric == "count"
}

// AggregatableFields 可聚合字段(接口实现):火山无 SQL 分组引擎,app 类型
// 的 level/service/trace_id 经"窗口采样 + 内存分组"路径聚合(口径见
// AggregateResult.TopNNote);访问日志类型无字典维度,空集(聚合入口显式
// 标注不支持,不静默)。
func (p *provider) AggregatableFields(ctx context.Context, _ *domain.CloudAccount) ([]string, error) {
	if p.logType == logquery.LogTypeApp {
		return []string{"level", "service", "trace_id"}, nil
	}
	if p.logType == logquery.LogTypeK8sEvent {
		// 扁平 schema 直取(实测字段集);采样口径,TopNNote 标注
		return []string{"cluster", "kind", "level", "namespace", "object", "reason"}, nil
	}
	return nil, nil
}

// flattenHistogram 把 DescribeHistogramV1 原始分桶归并到 bucketMs 对齐的桶。
// TLS 自动 Interval 步长不定,须跨源/与联邦 bucketSec 对齐后再逐桶求和;
// StartTime 字段单位(秒/毫秒)不做假设,ParseTimeMs 按量级自适配(秒×1000/毫秒原样)。
func flattenHistogram(infos []tls.HistogramInfoV1, bucketMs int64) ([]logquery.AggregateBucket, int64) {
	acc := make(map[int64]int64, len(infos))
	var total int64
	for _, info := range infos {
		ts := logquery.ParseTimeMs(info.StartTime)
		if ts <= 0 {
			continue
		}
		start := ts / bucketMs * bucketMs
		acc[start] += info.Count
		total += info.Count
	}
	out := make([]logquery.AggregateBucket, 0, len(acc))
	for ts, c := range acc {
		out = append(out, logquery.AggregateBucket{Timestamp: ts, Count: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp < out[j].Timestamp })
	return out, total
}
