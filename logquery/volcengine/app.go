// app.go 火山 TLS 应用业务日志(kindApp,LogTypeApp)。
//
// 实测(2026-10,probe-applog -provider volcano):采集管道与阿里同构,
// TLS topic `<service>-business`(一服务一 topic,dev 舰队 2158 个),
// schema:document_type=<service>-business / level=INFO/ERROR(大写) /
// msg=正文(message=原始行兜底) / exception=堆栈 / trace_id / __time__(ms) /
// pod 元数据;log_type=java-business。
// 与阿里 SLS 的关键差异:TLS key-value 检索大小写不敏感(实测 level:info
// 命中大写 INFO 数据),level 下推单形态即可,无需大小写变体展开。
package volcengine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// isAppTopic 名字是否为应用业务日志 topic:以 -business 结尾(采集管道命名,
// 一服务一 topic)。访问日志分型(isAccessTopic)已显式排除该后缀,两者不相交;
// business 出现在中段(如 dev-ai-app-business_..._https_access)仍属访问日志。
func isAppTopic(topicName string) bool {
	return strings.HasSuffix(strings.ToLower(topicName), "-business")
}

// appServiceOf topic 名/document_type → 服务名(去 -business 后缀)。
func appServiceOf(name string) string {
	return strings.TrimSuffix(strings.TrimSpace(name), "-business")
}

// appTopics 列出应用业务日志 topic(纯名字过滤,与访问日志共用 topics 清单缓存)。
// want 非空时按名字收敛(源清单 ResourceID = topic 名 = document_type,亦即服务
// 语义名 <service>-business,federation 资源过滤传服务名或源名均可命中)。
func (p *provider) appTopics(ctx context.Context, want map[string]bool) []topicRef {
	var out []topicRef
	for _, region := range p.enumRegions() {
		for _, t := range p.listTopicsCached(ctx, region) {
			if t == nil || t.TopicID == "" || !isAppTopic(t.TopicName) {
				continue
			}
			if len(want) > 0 && !want[t.TopicName] && !want[appServiceOf(t.TopicName)] {
				continue
			}
			out = append(out, topicRef{region: region, topicID: t.TopicID, topicName: t.TopicName})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].topicName < out[j].topicName })
	return out
}

// mapApp 火山应用业务日志 -> AppLogEntry(字段与阿里同构,service 兜底链:
// document_type 去 -business → 源 topic 名去 -business;缺失如实留空)。
func mapApp(m logquery.LogMeta, raw map[string]any) *logquery.AppLogEntry {
	service := appServiceOf(logquery.Str(raw["document_type"]))
	if service == "" {
		service = appServiceOf(m.ResourceID)
	}
	msg := logquery.Str(raw["msg"])
	if msg == "" {
		msg = logquery.Str(raw["message"])
	}
	return &logquery.AppLogEntry{
		Meta:      m,
		Timestamp: parseTimeField(raw["__time__"], raw["@timestamp"]),
		Service:   service,
		Level:     logquery.NormalizeLevel(logquery.Str(raw["level"])),
		Message:   msg,
		Exception: logquery.Str(raw["exception"]),
		TraceID:   logquery.Str(raw["trace_id"]),
		Raw:       raw,
	}
}

// appPushdownQuery 应用日志检索式下推:level 等值(TLS key-value 大小写不敏感,
// 单形态 `level:ERROR` 全变体覆盖,实测证)与 service 等值(`document_type:
// <service>-business`,实测证,合并 AND 组合查询亦证)可下推;其余过滤
// (neq/contains 类)留在明细层 EntryMatches。返回 pushable=false 表示存在
// 不可下推过滤,聚合层应整源跳过(否则计数失真),检索层可继续用原 query。
func appPushdownQuery(query string, filters []logquery.FieldFilter) (string, bool) {
	pushable := true
	var terms []string
	for _, f := range filters {
		switch {
		case strings.EqualFold(f.Field, "level") && (f.Op == "eq" || f.Op == ""):
			// 大小写不敏感:统一大写单形态
			terms = append(terms, fmt.Sprintf("level:%s", strings.ToUpper(strings.TrimSpace(f.Value))))
		case strings.EqualFold(f.Field, "service") && (f.Op == "eq" || f.Op == ""):
			terms = append(terms, fmt.Sprintf("document_type:%s", appTopicNameOf(strings.TrimSpace(f.Value))))
		case strings.EqualFold(f.Field, "level") && f.Op == "neq",
			strings.EqualFold(f.Field, "service") && f.Op == "neq":
			// not 语法未实测,不冒险,留在明细层
			pushable = false
		default:
			pushable = false
		}
	}
	if len(terms) == 0 {
		return query, pushable
	}
	q := strings.TrimSpace(query)
	if q == "" || q == "*" {
		return strings.Join(terms, " and "), pushable
	}
	return q + " and " + strings.Join(terms, " and "), pushable
}

// appTopicNameOf 服务名 → topic/document_type 名(裸服务名补 -business 后缀;
// 已带后缀的源名原样)。
func appTopicNameOf(service string) string {
	if strings.HasSuffix(strings.ToLower(service), "-business") {
		return service
	}
	return service + "-business"
}

// appResourceFilter 从过滤中提取 service 等值 → 资源(topic 名)收敛集。
// 火山一服务一 topic(topic 名 = document_type = <service>-business),服务过滤
// 可直接收敛扫描面(704 topic → 1),检索与聚合共用;无可收敛服务时返回 nil
// (不收敛,全量扫描)。返回第二个值表示是否存在不可收敛的 service 过滤
// (neq/contains —— 聚合层据此整源跳过,检索层照常全量扫描 + 明细层过滤)。
func appResourceFilter(filters []logquery.FieldFilter) (map[string]bool, bool) {
	var out map[string]bool
	allPushable := true
	for _, f := range filters {
		if !strings.EqualFold(f.Field, "service") {
			continue
		}
		if f.Op != "eq" && f.Op != "" {
			allPushable = false
			continue
		}
		if out == nil {
			out = make(map[string]bool)
		}
		out[appTopicNameOf(strings.TrimSpace(f.Value))] = true
	}
	return out, allPushable
}
