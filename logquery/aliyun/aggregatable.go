// 可聚合字段探测:分组聚合维度白名单。SLS 聚合硬约束 = 分组列必须开过 KV
// 分析索引(GetIndex → Index.Keys),否则 Group By 报 400 "列不存在或非索引"。
// 维度下拉此前展示 /types 全量字段字典,用户盲选未开索引字段反复撞
// TopNSkipReason。这里对每日志类型求各 logstore 索引并集,产出真正可聚合
// 的字段清单:
//   - 归一化字典字段(dimColumnExpr 键)映射列在任一 store 已建索引 → 收录;
//   - 其余已建索引的原始列(如 real_client_ip / request_uri)→ 一并收录(可透传)。
// 探测失败(权限/未建索引)返回 nil —— 调用方回退全量字典(现行为,不劣化)。
package aliyun

import (
	"context"
	"sort"
	"sync"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/gotomicro/ego/core/elog"
)

// maxProbeStores 动态枚举 project 的探测 store 上限:白名单只看字段是否
// 在某 store 建过索引,同 project 内前几个目标 store 即可代表(容器/函数
// 噪声 store 不贡献有用字段,反而拖慢冷启动)。
const maxProbeStores = 6

// AggregatableFields 该类型可聚合字段清单(跨 catalog logstore 索引并集)。
// 实现 logquery.AggregatableLister(可选能力)。索引探测走进程级缓存,
// 冷启动一次 GetIndex 后 TTL 内零 API 调用;store 间并发探测(单 store
// GetIndex 失败只跳过,不阻塞整并集)。
func (p *provider) AggregatableFields(ctx context.Context, _ *domain.CloudAccount) ([]string, error) {
	// 收集探测目标(固定 store 全量;动态枚举 project 截断前 maxProbeStores)
	var targets []struct{ region, project, logstore string }
	for _, src := range catalog {
		if src.logType != p.logType {
			continue
		}
		if src.logstore != "" {
			targets = append(targets, struct{ region, project, logstore string }{
				region: src.region, project: src.project, logstore: src.logstore,
			})
			continue
		}
		ls, _ := logstoreEnumCache.get(src.region+"/"+src.project, func() []string {
			out, err := p.clientFor(src.region).ListLogStore(src.project)
			if err != nil {
				p.logger.Debug("[logquery-aliyun] list logstores failed",
					elog.String("project", src.project), elog.FieldErr(err))
				return nil
			}
			return out
		})
		count := 0
		for _, s := range filterCatalogStores(src.kind, ls) {
			if count >= maxProbeStores {
				break
			}
			count++
			targets = append(targets, struct{ region, project, logstore string }{
				region: src.region, project: src.project, logstore: s,
			})
		}
	}
	if len(targets) == 0 {
		return nil, nil
	}

	indexed := make(map[string]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for _, tgt := range targets {
		wg.Add(1)
		go func(t struct{ region, project, logstore string }) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			keys := p.storeIndexKeys(t.region, t.project, t.logstore)
			if len(keys) == 0 {
				return
			}
			mu.Lock()
			for _, k := range keys {
				indexed[k] = true
			}
			mu.Unlock()
		}(tgt)
	}
	wg.Wait()
	if len(indexed) == 0 {
		return nil, nil // 全部探测失败(权限/未建索引):降级回退全量字典
	}
	return aggregatableFieldsFromIndexes(p.logType, indexed), nil
}

// aggregatableFieldsFromIndexes 由索引并集计算可聚合字段清单(纯函数,单测)。
// 仅看本日志类型的 catalog kind(避免跨类型 schema 列名串扰):
//   - dimColumnExpr 字段:映射列为裸标识符且本类型所有 store 都未建索引 → 不收;
//     函数表达式(如 regexp_extract)不可静态校验列,放行(查询期 TopNSkipReason 兜底);
//   - dimGroupExpr 字段:维度专用表达式,直接收录。
//
// SLS 默认对全部字段建索引,因此索引并集几乎恒为"全列";此处只回字典字段
// (带中文标签的可选维度),未收录的原始列仍可 allow-create 输入聚合。
func aggregatableFieldsFromIndexes(logType logquery.LogType, indexed map[string]bool) []string {
	var fields []string
	seen := make(map[string]bool)
	for _, src := range catalog {
		if src.logType != logType {
			continue
		}
		for field, col := range dimColumnExpr[src.kind] {
			if seen[field] {
				continue
			}
			if identifierLike(col) && !indexed[col] {
				continue
			}
			seen[field] = true
			fields = append(fields, field)
		}
		for field := range dimGroupExpr[src.kind] {
			if !seen[field] && !internalDim[field] {
				seen[field] = true
				fields = append(fields, field)
			}
		}
	}
	sort.Strings(fields)
	return fields
}
