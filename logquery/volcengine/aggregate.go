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

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volc-sdk-golang/service/tls"
)

// histogramConcurrency DescribeHistogram 并发度(逐 topic 一请求,1130 topic
// 会打爆,收敛到与 Search 相近的扇出并发)。
const histogramConcurrency = 8

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
	if len(params.Filters) > 0 {
		if !isApp {
			return nil, fmt.Errorf("volcengine: 字段筛选无法下推聚合(跳过该源,避免计数失真)")
		}
		// 应用日志:level 等值可下推(TLS key-value 检索大小写不敏感,实测证);
		// 其余过滤不可下推仍整源跳过,避免计数失真(诚实优先)。
		var pushable bool
		query, pushable = appPushdownQuery(query, params.Filters)
		if !pushable {
			return nil, fmt.Errorf("volcengine: 存在无法下推的字段筛选(仅 level 等值可下推),跳过该源避免计数失真")
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

	var (
		mu    sync.Mutex
		infos []tls.HistogramInfoV1
	)
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
	wg.Wait()

	buckets, total := flattenHistogram(infos, params.BucketSec*1000)
	result := &logquery.AggregateResult{Total: total, Buckets: buckets}
	if params.Dimension != "" {
		// 火山自采 topic 无字段分析索引,任意维度分组都取不到
		result.TopNSkipReason = "火山源无字段分析索引,不支持该维度分组"
	}
	return result, nil
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
