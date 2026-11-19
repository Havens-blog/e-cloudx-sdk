// app.go 华为云 LTS 应用业务日志(LogTypeApp)。
//
// 与 WAF/ELB 不同,应用日志在 LTS 侧没有固定组名:日志组/流由账号自行创建
// (ECS/CCE 采集器或 SDK 直写),字段形态也没有统一 schema。因此:
//   - 源发现:ListLogGroups 全量组名中排除 WAF/ELB catalog 组,其余组下的
//     全部流都算 app 源(不按流名后缀过滤;catalog 两组只属于 WAF/ELB);
//   - 内容映射:JSON 对象按键别名尽力映射;非 JSON 纯文本行识别常见级别
//     写法与 trace key=value,取不到如实留空(不编造)。
package huawei

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/Havens-blog/e-cloudx-sdk/logquery"
	"github.com/gotomicro/ego/core/elog"
	ltsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/lts/v2/model"
)

// appNote app 源引导文案(前端"资源"页展示;说明字段只能尽力映射)。
const appNote = "LTS 中不属于 WAF/ELB 的日志流，按应用业务日志查询（组/流由账号自行创建，字段按 JSON 或纯文本尽力映射）"

// LTS 网关限流实测:APIGW.0308 "policy user over ratelimit,limit:10,time:1 second"。
// app 源要「每组一次 ListLogStreams + 每流一次 ListLogs」,17 个流并发打过去会大量
// 429;被跳过的组会让源清单静默少一半(实测 18 → 9 个源,且不报错)。故所有 LTS
// 调用统一过闸门(≈8 QPS,低于 10 QPS 上限)并做限流重试。
const (
	ltsCallInterval = 120 * time.Millisecond
	ltsMaxAttempts  = 4
	ltsRetryBase    = 350 * time.Millisecond
)

// appLTSGate 串行化 LTS 调用:包级共享(限流按账号/用户维度,全进程共用一个闸门
// 更保守也足够)。预约式等待——并发调用各自占一个未来的槽位,不会同时放行。
var appLTSGate = &ltsGate{}

type ltsGate struct {
	mu   sync.Mutex
	last time.Time
}

// wait 占一个 LTS 调用槽位(ctx 取消时返回 ctx.Err())。首次调用不等待。
func (g *ltsGate) wait(ctx context.Context) error {
	g.mu.Lock()
	now := time.Now()
	next := g.last.Add(ltsCallInterval)
	if !next.After(now) {
		g.last = now
		g.mu.Unlock()
		return nil
	}
	g.last = next
	g.mu.Unlock()
	select {
	case <-time.After(next.Sub(now)):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// isLTSThrottled 判定是否 LTS 网关限流。只认明确特征(不裸匹配 "429"——request
// id 是十六进制,含 429 会误判)。
func isLTSThrottled(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "APIGW.0308") ||
		strings.Contains(msg, "\"status_code\":429") ||
		strings.Contains(msg, "status code: 429") ||
		strings.Contains(msg, "Throttling") ||
		strings.Contains(msg, "throttling threshold")
}

// withLTSRetry 过闸门 + 限流退避重试(非限流错误立即返回)。
func (p *provider) withLTSRetry(ctx context.Context, call string, fn func() error) error {
	var err error
	for attempt := 1; attempt <= ltsMaxAttempts; attempt++ {
		if waitErr := appLTSGate.wait(ctx); waitErr != nil {
			return waitErr
		}
		err = fn()
		if err == nil || !isLTSThrottled(err) || attempt == ltsMaxAttempts {
			return err
		}
		p.logger.Warn("[logquery-huawei] lts throttled, retrying",
			elog.String("call", call), elog.Int("attempt", attempt), elog.FieldErr(err))
		select {
		case <-time.After(ltsRetryBase * time.Duration(attempt)):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

// isWAFELBGroup 是否为 WAF/ELB 形态的日志组(按 classifyGroup 的命名规则
// 判定,不再依赖精确组名白名单)。这类组只属于 WAF/负载均衡页签,不得出现在
// app 源里 —— 早先按 catalog 精确匹配,dev-elb / eda-uat-elb 因此被当成应用
// 日志源(用通用映射解析 ELB 文本行,字段全空)。
func isWAFELBGroup(name string) bool {
	_, ok := classifyGroup(name)
	return ok
}

// platformGroups LTS 自身运维日志组(实测账号里是 lts-system/…-statistics)。
// 这些不是租户的应用日志,列进 app 源只会制造噪音。
var platformGroups = map[string]bool{
	"lts-system": true,
}

// isPlatformGroup 是否为云平台自身的运维日志组。
func isPlatformGroup(name string) bool {
	return platformGroups[name]
}

// appGroups LTS 全量日志组名 -> 应用业务日志组(排除 WAF/ELB catalog 组与平台自身运维组)。
// 纯函数(表驱动单测):空名/重复名丢弃,结果按字典序(源清单稳定)。
func appGroups(allGroups []string) []string {
	seen := make(map[string]bool, len(allGroups))
	out := make([]string, 0, len(allGroups))
	for _, g := range allGroups {
		if g == "" || seen[g] || isWAFELBGroup(g) || isPlatformGroup(g) {
			continue
		}
		seen[g] = true
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// appStream app 候选流(组名 + LTS 流对象)。
type appStream struct {
	group  string
	stream ltsmodel.ListLogStreamsResponseBody1LogStreams
}

// listAppStreams 枚举 app 候选流:组清单来自 ListLogGroups 动态发现,
// 单组 ListLogStreams 失败隔离(记 Warn、跳过该组,不整体失败)。
func (p *provider) listAppStreams(ctx context.Context, ids map[string]string) []appStream {
	return p.listAppStreamsWith(ctx, p.lts(), ids)
}

// listAppStreamsWith 同 listAppStreams,client 由调用方注入(聚合与单测复用)。
// 组枚举失败只记 Warn 并跳过:Search/ListLogSources 是尽力而为的清单,
// 单组不可用不应让整个清单失败(与 WAF/ELB 的枚举一致)。
func (p *provider) listAppStreamsWith(ctx context.Context, api ltsAPI, ids map[string]string) []appStream {
	out, _ := p.listAppStreamsChecked(ctx, api, ids)
	return out
}

// listAppStreamsChecked 同 listAppStreamsWith,但同时返回每个失败组的错误。
// 聚合路径必须用这个变体:枚举失败的组不会产生 target,该组日志既进不了分桶、
// 也进不了单流错误列表,Total 会静默偏小(必须把错误交给调用方显式上报)。
func (p *provider) listAppStreamsChecked(ctx context.Context, api ltsAPI, ids map[string]string) ([]appStream, []error) {
	names := make([]string, 0, len(ids))
	for name := range ids {
		names = append(names, name)
	}
	var (
		out  []appStream
		errs []error
	)
	for _, g := range appGroups(names) {
		group := g
		var streams *ltsmodel.ListLogStreamsResponse
		err := p.withLTSRetry(ctx, "list_streams/"+group, func() error {
			resp, callErr := api.ListLogStreams(&ltsmodel.ListLogStreamsRequest{
				LogGroupName: &group,
			})
			if callErr != nil {
				return callErr
			}
			streams = resp
			return nil
		})
		if err != nil {
			p.logger.Warn("[logquery-huawei] list streams failed",
				elog.String("group", group), elog.FieldErr(err))
			errs = append(errs, fmt.Errorf("list streams %s: %w", group, err))
			continue
		}
		for _, s := range derefStreams(streams.LogStreams) {
			out = append(out, appStream{group: group, stream: s})
		}
	}
	return out, errs
}

// listAppSources 应用日志源枚举:Catalog 之外的组 × 全部流,每个 (组,流)
// 一个源(ResourceID = group + "/" + stream)。
func (p *provider) listAppSources(ctx context.Context) ([]logquery.LogSource, error) {
	// groupIDs 提前暴露凭证/网络问题,而非静默空清单(与 WAF/ELB 一致)。
	ids, err := p.groupIDs(ctx)
	if err != nil {
		return nil, err
	}
	streams := p.listAppStreams(ctx, ids)
	out := make([]logquery.LogSource, 0, len(streams))
	for _, tgt := range streams {
		enabled := tgt.stream.WhetherLogStorage == nil || *tgt.stream.WhetherLogStorage
		out = append(out, logquery.LogSource{
			Cloud:       domain.CloudProviderHuawei,
			AccountID:   fmt.Sprintf("%d", p.account.ID),
			AccountName: p.account.Name,
			Region:      defaultRegion,
			LogType:     logquery.LogTypeApp,
			ResourceID:  tgt.group + "/" + tgt.stream.LogStreamName,
			Name:        "应用日志 / " + tgt.group + "/" + tgt.stream.LogStreamName,
			Enabled:     enabled,
			Note:        appNote,
		})
	}
	return out, nil
}

// appResourceMatch params.Resources 为空表示不限;否则按流名或
// group+"/"+stream 命中(与 WAF/ELB 的资源过滤语义一致)。
func appResourceMatch(group, stream string, resources []string) bool {
	if len(resources) == 0 {
		return true
	}
	for _, r := range resources {
		if r == stream || r == group+"/"+stream {
			return true
		}
	}
	return false
}

// searchApp 应用日志检索:逐 (组,流) 并发 ListLogs(上限 8,单流失败隔离)
// -> mapAppContent -> 时间倒序 -> provider 级 3000 硬顶。
// limit 为每流上限(沿用 Search 的归一化结果)。
func (p *provider) searchApp(ctx context.Context, params logquery.SearchParams, limit int) ([]logquery.LogEntry, error) {
	ids, err := p.groupIDs(ctx)
	if err != nil {
		return nil, err
	}
	var targets []appStream
	for _, tgt := range p.listAppStreams(ctx, ids) {
		if !appResourceMatch(tgt.group, tgt.stream.LogStreamName, params.Resources) {
			continue
		}
		targets = append(targets, tgt)
	}

	results := make([][]logquery.LogEntry, len(targets))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i := range targets {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tgt := targets[i]
			sem <- struct{}{}
			defer func() { <-sem }()
			logs, err := func() ([]logquery.LogEntry, error) {
				var got []logquery.LogEntry
				callErr := p.withLTSRetry(ctx, "list_logs/"+tgt.group+"/"+tgt.stream.LogStreamName, func() error {
					entries, fetchErr := p.fetchStreamLogsMapped(ctx, ids[tgt.group], tgt.stream, params, limit, mapAppContent)
					if fetchErr != nil {
						return fetchErr
					}
					got = entries
					return nil
				})
				return got, callErr
			}()
			if err != nil {
				p.logger.Warn("[logquery-huawei] fetch logs failed",
					elog.String("group", tgt.group),
					elog.String("stream", tgt.stream.LogStreamName), elog.FieldErr(err))
				return
			}
			results[i] = stampAppResource(logs, tgt.group, tgt.stream.LogStreamName)
		}(i)
	}
	wg.Wait()

	var entries []logquery.LogEntry
	for i := range targets {
		entries = append(entries, results[i]...)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].GetTimestamp() > entries[j].GetTimestamp()
	})
	// limit 为每日志流上限;归并后全局 3000 硬顶(与 WAF/ELB 同用 logquery.ProviderResultCap)
	if len(entries) > logquery.ProviderResultCap {
		entries = entries[:logquery.ProviderResultCap]
	}
	return entries, nil
}

// stampAppResource 把 app 条目的 meta.resource_id 补成 "<组>/<流>"。
//
// 与 WAF/ELB 共用 fetchStreamLogsMapped 时 meta.resource_id 只有流名,而 app 的源
// ResourceID 是 "group/stream"(同组流名可能重名),不补则结果无法按源归属,前端
// 「资源」列也看不出日志来自哪个组。只动 app 路径,不影响 WAF/ELB。
func stampAppResource(entries []logquery.LogEntry, group, stream string) []logquery.LogEntry {
	resourceID := group + "/" + stream
	for _, e := range entries {
		if app, ok := e.(*logquery.AppLogEntry); ok && app != nil {
			app.Meta.ResourceID = resourceID
		}
	}
	return entries
}

// mapAppContent 应用日志 content -> AppLogEntry(纯函数,表驱动单测覆盖)。
//
// JSON 对象:按键别名(大小写不敏感,逐个别名尝试)取值;整份 JSON 进 Raw。
// 非 JSON 纯文本:整行进 Message,识别行首常见级别与 trace key=value。
// 空内容(含纯空白)返回 nil,由调用方跳过。
func mapAppContent(meta logquery.LogMeta, content string) logquery.LogEntry {
	content = strings.TrimPrefix(content, "\ufeff")
	if strings.TrimSpace(content) == "" {
		return nil
	}
	if raw, ok := parseAppJSONObject(content); ok {
		return mapAppJSON(meta, raw)
	}
	return mapAppText(meta, content)
}

// parseAppJSONObject 尝试把 content 解析为 JSON 对象(容忍前导空白/BOM);
// 非对象或非法 JSON 返回 ok=false,走纯文本回退。
func parseAppJSONObject(content string) (map[string]any, bool) {
	s := strings.TrimSpace(strings.TrimPrefix(content, "\ufeff"))
	if !strings.HasPrefix(s, "{") {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber() // 保留数字原文(时间戳精度/Str 提取都更稳)
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil || raw == nil {
		return nil, false
	}
	return raw, true
}

// appLowerKeys 键名小写化索引(别名查找大小写不敏感)。同键不同大小写
// 并存时保留先出现者(极少数异常数据,不做额外裁决)。
func appLowerKeys(raw map[string]any) map[string]any {
	lower := make(map[string]any, len(raw))
	for k, v := range raw {
		lk := strings.ToLower(k)
		if _, ok := lower[lk]; !ok {
			lower[lk] = v
		}
	}
	return lower
}

// firstStr 按别名顺序取第一个非空字符串(缺失/非字符串返回 "")。
func firstStr(lower map[string]any, aliases ...string) string {
	for _, a := range aliases {
		if v, ok := lower[a]; ok {
			if s := logquery.Str(v); s != "" {
				return s
			}
		}
	}
	return ""
}

// firstString 按别名顺序取第一个非空 JSON 字符串(数值型不做字符串化)。
// 级别专用:数值型 severity(如 pino 的 30)不映射为级别,留空由上层展示原始
// Raw,避免产出契约(level 归一小写 error/warn/info/debug/"")之外的取值。
func firstString(lower map[string]any, aliases ...string) string {
	for _, a := range aliases {
		if v, ok := lower[a]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// firstTimestampMs 按别名顺序取第一个可解析时间(毫秒);全失败返回 0,
// 由上层保留 LTS 记录时间。
func firstTimestampMs(lower map[string]any, aliases ...string) int64 {
	for _, a := range aliases {
		if v, ok := lower[a]; ok {
			if ts := logquery.ParseTimeMs(v); ts != 0 {
				return ts
			}
		}
	}
	return 0
}

// mapAppJSON JSON 对象 -> AppLogEntry:字段别名宽容,取不到留空;
// service 缺失时回退流名(调用方约定 meta.ResourceID = 流名)。
func mapAppJSON(meta logquery.LogMeta, raw map[string]any) *logquery.AppLogEntry {
	lower := appLowerKeys(raw)
	service := firstStr(lower,
		"service", "service_name", "app", "application", "app_name", "logger", "module")
	if service == "" {
		service = strings.TrimSpace(meta.ResourceID) // 回退流名,不编造
	}
	return &logquery.AppLogEntry{
		Meta:      meta,
		Timestamp: firstTimestampMs(lower, "__time__", "timestamp", "time", "@timestamp"),
		Service:   service,
		Level: logquery.NormalizeLevel(firstString(lower,
			"level", "severity", "lvl", "log_level", "loglevel")),
		Message: firstStr(lower, "msg", "message", "content", "log", "body"),
		Exception: firstStr(lower,
			"exception", "stack", "stacktrace", "throwable", "error", "err"),
		TraceID: firstStr(lower,
			"trace_id", "traceid", "tid", "x_trace_id", "trace"),
		Raw: raw,
	}
}

// canonicLevel 行首级别词(仅契约内常见四值 + trace/fatal;WARNING 之类别名
// 不做语义折叠,避免产出契约外取值)。
const appLevelToken = `ERROR|WARN|INFO|DEBUG|TRACE|FATAL`

// appTextLevelPatterns 纯文本级别识别(按优先级;大小写不敏感):
//  1. [ERROR] …
//  2. ERROR … / ERROR: …
//  3. 2026-08-27 12:00:00.123 WARN …(ISO 时间在前)
//  4. level=INFO / level: INFO / log_level=INFO(键值式,任意位置)
var appTextLevelPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^\s*\[(` + appLevelToken + `)\]`),
	regexp.MustCompile(`(?i)^\s*(` + appLevelToken + `)\b`),
	regexp.MustCompile(`(?i)^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?\s+\[?(` + appLevelToken + `)\b\]?`),
	regexp.MustCompile(`(?i)\b(?:log_)?level\s*[=:]\s*(` + appLevelToken + `)\b`),
}

// appTextTracePattern trace key=value 就近提取(traceId=/trace_id=/tid= 等);
// 值允许引号包裹,取字母数字/点/下划线/连字符串(32 位 hex 与含 - 的串都覆盖)。
var appTextTracePattern = regexp.MustCompile(`(?i)\b(?:trace_?id|tid|x[-_]trace[-_]id)\s*[=:]\s*["']?([0-9A-Za-z][0-9A-Za-z._-]{5,63})["']?`)

// detectAppTextLevel 行首级别识别;识别不到返回 ""(不编造)。
func detectAppTextLevel(line string) string {
	for _, re := range appTextLevelPatterns {
		if m := re.FindStringSubmatch(line); m != nil {
			return logquery.NormalizeLevel(m[1])
		}
	}
	return ""
}

// detectAppTextTrace 行内 trace key=value 提取;提取不到返回 ""。
func detectAppTextTrace(content string) string {
	if m := appTextTracePattern.FindStringSubmatch(content); m != nil {
		return m[1]
	}
	return ""
}

// mapAppText 纯文本行 -> AppLogEntry:Message = 整行;级别/trace 尽力识别,
// 识别不到如实留空;service 与 JSON 一致回退流名(meta.ResourceID)。
func mapAppText(meta logquery.LogMeta, content string) *logquery.AppLogEntry {
	line := content
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i] // 级别/Trace 只认首行;多行堆栈保留在 Message
	}
	return &logquery.AppLogEntry{
		Meta:    meta,
		Service: strings.TrimSpace(meta.ResourceID), // 回退流名,不编造
		Level:   detectAppTextLevel(line),
		Message: strings.TrimRight(content, "\r\n"),
		TraceID: detectAppTextTrace(content),
		Raw:     map[string]any{"content": content},
	}
}
