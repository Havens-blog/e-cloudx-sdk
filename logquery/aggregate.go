// 聚合能力(plan.md §8):窗口内真实统计下推到各云查询引擎
// (SLS 检索|SQL / LTS 分析查询),只回传分桶与分组结果——
// 百万/千万级原始行不过网关,趋势图与总数不再受采样限制。
package logquery

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
)

// AggregateBucket 时间分桶(Timestamp=桶起点 Unix 毫秒 UTC;Count=窗口内该桶精确条数)。
type AggregateBucket struct {
	Timestamp int64 `json:"timestamp"`
	Count     int64 `json:"count"`
}

// TopNItem 聚合 TopN 条目(域名/规则/状态码等按维度;由 provider 归并后取前 10)。
// Count = 该分组条目数(跨源按名求和);Value = 指标值(count 指标时 = Count,
// avg/p99/sum 等数值指标时承载数值;跨源按 count 加权均值归并,避免非可加
// 指标直接求和失真)。
type TopNItem struct {
	Name  string  `json:"name"`
	Count int64   `json:"count"`
	Value float64 `json:"value,omitempty"`
}

// AggregateMetrics 支持的聚合指标(前端下拉;能力按 kind/provider 校验,
// 不支持的源/指标显式标注)。count 全 kind 可用,其余按字段列存在性。
// nonhit_count 为 CDN 缓存分析新增指标:未命中请求计数(cache_hit 归一
// miss+error,归一口径沿用明细层 NormalizeCacheHit 单一映射源;仅 CDN 类
// kind 编译,其他源显式标注不支持)。nonhit_bytes 为同判据的未命中字节
// (then 1 → then 字节列),用于域名级字节命中率下钻。
var AggregateMetrics = []string{"count", "sum_bytes", "avg_latency", "p99_latency", "nonhit_count", "nonhit_bytes"}

// IsValidAggregateMetric 校验指标(空 = count)。
func IsValidAggregateMetric(m string) bool {
	if m == "" {
		return true
	}
	return slices.Contains(AggregateMetrics, m)
}

// AggregateTopN TopN 分组条数上限(0=默认 10;域名级字节对齐等场景传更大,
// 如 CDN 缓存分析域名字节命中率下钻需对齐请求数排序的域名排行)。
func AggregateTopN(v int) int {
	if v <= 0 {
		return 10
	}
	return v
}

// MetricIsWeighted avg/p99 等非可加指标:跨源归并需按 count 加权均值;
// count/sum_bytes/nonhit_count 可加直接求和。
func MetricIsWeighted(m string) bool {
	switch m {
	case "", "count", "sum_bytes", "nonhit_count", "nonhit_bytes":
		return false
	default:
		return true
	}
}

// AggregateParams 聚合参数。BucketSec 由服务层按窗口统一计算并随请求下发,
// 保证联邦内所有源分桶对齐(合并 = 逐桶求和)。
type AggregateParams struct {
	StartTime int64         // Unix 毫秒 UTC(含)
	EndTime   int64         // Unix 毫秒 UTC(含)
	Query     string        // 原生检索式透传(与 Search 同语义)
	BucketSec int64         // 分桶秒数(PickBucketSec 预计算)
	Resources []string      // 可选:限定资源(域名/LB ID)
	Filters   []FieldFilter // 字段筛选(能下推的源编译进 SQL,不能下推的源显式标注)
	Dimension string        // 分组维度(/types 字段 key;空=kind 默认 TopN 维度)
	Metric    string        // count / sum_bytes / avg_latency / p99_latency(空=count)
	TopNLimit int           // 分组 TopN 条数上限(0=默认 10;域名级字节对齐等需更大)
}

// AggregateResult 单源聚合结果。Total = 分桶求和(省一次 count 扫描)。
type AggregateResult struct {
	Total   int64
	Buckets []AggregateBucket
	TopN    []TopNItem
	// TopNSkipReason 维度/指标不可下推时的说明(趋势/总数不受影响,仍有效)。
	TopNSkipReason string
	// SkipFilterReason 字段筛选不可下推时设置(该源整源跳过,否则计数失真)。
	SkipFilterReason string
	// TopNNote TopN 的口径说明(采样类 TopN 必设:窗口内最近优先采样,
	// 非精确全量分布;精确 TopN 留空)。
	TopNNote string
}

// TopNFromEntries 明细采样聚合:对已取回的日志条目按维度字段内存分组计数,
// 返回降序 TopN。用于索引不支持 SQL group by 的维度(如 app 的 service 列
// doc_value=false、火山无字段分析索引)——TopN 是窗口内"最近优先"采样的
// 分布,调用方必须以 TopNNote 标注口径,不得冒充精确全量。
// 维度取值走 entryFieldValue 同一映射(service/level/message/trace_id),
// 空/缺失值不编组(不产出 "null"/"" 组)。metricIsCount=false 时本函数只产
// count(采样行上求和/分位不可信),调用方不应把非 count 指标交给采样路径。
func TopNFromEntries(entries []LogEntry, dimension string, limit int) []TopNItem {
	if dimension == "" {
		return nil
	}
	counts := make(map[string]int64)
	for _, e := range entries {
		v, ok := entryFieldValue(e, dimension)
		if !ok || v == "" || v == "null" {
			continue
		}
		counts[v]++
	}
	out := make([]TopNItem, 0, len(counts))
	for name, c := range counts {
		out = append(out, TopNItem{Name: name, Count: c, Value: float64(c)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Value > out[j].Value })
	if cap := AggregateTopN(limit); len(out) > cap {
		out = out[:cap]
	}
	return out
}

// ScanTopNNote 采样型 TopN 的口径说明(跨 provider 共用一份措辞;配
// AggregateResult.TopNNote 使用)。
func ScanTopNNote(sampled int) string {
	return fmt.Sprintf("TopN 基于窗口内最近优先采样 %d 条(跨源可能有截断),非精确全量分布", sampled)
}

// Aggregator 可选能力接口:窗口内真实聚合。未实现的 provider(S3 文件类、
// 占位 stub)由联邦层显式标注"不支持聚合",不静默缺失。
type Aggregator interface {
	Aggregate(ctx context.Context, account *domain.CloudAccount, params AggregateParams) (*AggregateResult, error)
}

// AggregatableLister 可选能力接口:返回该类型已建分析索引(可聚合)的字段
// 清单。分组聚合维度下拉按此白名单展示 —— 前端不再盲选全量字段字典里未开
// 索引的字段(盲选只会撞 TopNSkipReason)。未实现的 provider 由联邦层跳过。
type AggregatableLister interface {
	AggregatableFields(ctx context.Context, account *domain.CloudAccount) ([]string, error)
}

// aggregateBucketSteps 分桶候选步长(窗口内 ≤100 桶取最小步长)。
var aggregateBucketSteps = []int64{60, 300, 900, 3600, 21600, 86400}

// PickBucketSec 按窗口自适应分桶秒数(服务层统一计算,随参数下发)。
func PickBucketSec(startMs, endMs int64) int64 {
	spanSec := (endMs - startMs) / 1000
	if spanSec <= 0 {
		return aggregateBucketSteps[0]
	}
	for _, s := range aggregateBucketSteps {
		if spanSec/s <= 100 {
			return s
		}
	}
	return aggregateBucketSteps[len(aggregateBucketSteps)-1]
}
