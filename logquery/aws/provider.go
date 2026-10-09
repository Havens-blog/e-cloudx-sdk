package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gotomicro/ego/core/elog"
)

func init() {
	logquery.RegisterProvider(domain.CloudProviderAWS, logquery.LogTypeWAF, newProvider(logquery.LogTypeWAF))
	logquery.RegisterProvider(domain.CloudProviderAWS, logquery.LogTypeCDN, newProvider(logquery.LogTypeCDN))
}

// 投递特征:Firehose 分钟级、CloudFront ~1 小时;对象 LastModified 早于窗口
// 起点也可能含窗口内日志,向前多看一个投递延迟。
const (
	wafLookback        = 2 * time.Hour
	cloudFrontLookback = 3 * time.Hour
	maxObjectsPerScan  = 60 // 单源单轮最多下载对象数(防大窗口拉爆)
	maxObjectBytes     = 32 << 20
	// maxFanoutConcurrency 源级(S3 对象扫描/ACL 前缀)并发上限。
	maxFanoutConcurrency = 16
	// perTargetConcurrency 单源内对象下载并发:GetObject 是 S3 往返,逐对象串行
	// 是 AWS WAF 搜索(下载阶段 ~=结果条数×单对象 RTT)数秒的主因。收尾按时间
	// 倒序截取 limit 保持"每源最新 limit 条"语义,并发只影响速度不影响序。
	perTargetConcurrency = 4
)

// aSource S3 源 catalog 条目。
type aSource struct {
	bucket   string
	prefix   string // 空=桶根(域名目录布局)
	region   string
	kind     string // "waf-json" | "cloudfront-tsv"
	logType  logquery.LogType
	name     string
	note     string
	lookback time.Duration
}

// catalog AWS 侧日志源清单(Phase 0 实测;ALB 访问日志大多未开启,不纳入)。
var catalog = []aSource{
	{
		// WAFv2 CLOUDFRONT scope 主桶(Firehose aws-waf-logs-CL-s3-* 落地)
		bucket: "waf-loghub-clloggingbucket5f34e4eb-hjalewqix1db",
		prefix: "AWSLogs/", region: "us-east-1", kind: "waf-json",
		logType: logquery.LogTypeWAF, name: "AWS WAF",
		note:    "Firehose 投递 JSON(分钟级)", lookback: wafLookback,
	},
	{
		// 第二 Firehose 桶(部分 WebACL 落此)
		bucket: "loghub-clloggingbucket5f34e4eb-asng9spoysri",
		prefix: "AWSLogs/", region: "us-east-1", kind: "waf-json",
		logType: logquery.LogTypeWAF, name: "AWS WAF",
		note:    "Firehose 投递 JSON(分钟级)", lookback: wafLookback,
	},
	{
		// CloudFront 标准日志(按域名前缀分目录)
		bucket: "jlc-prod-cdn-log", prefix: "", region: "eu-west-1", kind: "cloudfront-tsv",
		logType: logquery.LogTypeCDN, name: "CloudFront",
		note:    "标准日志 TSV(~1h 延迟)", lookback: cloudFrontLookback,
	},
}

// provider AWS S3 日志 provider(单账号)。
type provider struct {
	logType logquery.LogType
	account *domain.CloudAccount
	logger  *elog.Component
}

func newProvider(logType logquery.LogType) logquery.ProviderCreator {
	return func(account *domain.CloudAccount) (logquery.LogProvider, error) {
		if account.AccessKeyID == "" || account.AccessKeySecret == "" {
			return nil, fmt.Errorf("aws logquery: account %d missing credentials", account.ID)
		}
		return &provider{
			logType: logType,
			account: account,
			logger:  elog.DefaultLogger,
		}, nil
	}
}

// clientFor 按 bucket 取 S3 client(region 以 catalog 为 hint,首次使用时
// HeadBucket 探测真实区域并纠正——Go SDK v2 不会像 boto3 自动跟随 301)。
// 缓存是进程级的:provider 每请求新建(per-request client 缓存形同虚设,
// 每次 sources 都重复 client 构造 + HeadBucket 探测,实测拖慢 sources 数秒)。
func (p *provider) clientFor(bucket, hintRegion string) (*s3.Client, error) {
	awsClientCache.mu.Lock()
	defer awsClientCache.mu.Unlock()
	if c, ok := awsClientCache.clients[bucket]; ok {
		return c, nil
	}
	c, err := s3Client(p.account, hintRegion)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if real := resolveBucketRegion(ctx, c, bucket); real != "" && real != hintRegion {
		if fixed, ferr := s3Client(p.account, real); ferr == nil {
			p.logger.Info("[logquery-aws] bucket region resolved",
				elog.String("bucket", bucket), elog.String("region", real))
			c = fixed
		}
	}
	if awsClientCache.clients == nil {
		awsClientCache.clients = make(map[string]*s3.Client)
	}
	awsClientCache.clients[bucket] = c
	return c, nil
}

// awsClientCache 进程级 bucket -> S3 client 缓存(凭证按账号解密后唯一)。
var awsClientCache = struct {
	mu      sync.Mutex
	clients map[string]*s3.Client
}{clients: make(map[string]*s3.Client)}

// Cloud 实现 LogProvider。
func (p *provider) Cloud() domain.CloudProvider { return domain.CloudProviderAWS }

// LogType 实现 LogProvider。
func (p *provider) LogType() logquery.LogType { return p.logType }

// sourcesOf 该类型全部 catalog 源。
func (p *provider) sourcesOf() []aSource {
	var out []aSource
	for _, s := range catalog {
		if s.logType == p.logType {
			out = append(out, s)
		}
	}
	return out
}

// ListLogSources 枚举日志源:WAF=逐 ACL 前缀下钻;CDN=桶根一级目录(域名)。
// 前缀发现带 10 分钟缓存(SWR 宽限期内供旧清单+后台刷新),各 catalog 源
// **并发**枚举(此前串行:2 WAF 桶 4 级钻取 + CDN 根 list 叠加曾是冷启动
// sources ~15s 的主因),并输出逐源阶段耗时(可测回归)。
func (p *provider) ListLogSources(ctx context.Context, account *domain.CloudAccount) ([]logquery.LogSource, error) {
	srcs := p.sourcesOf()
	slots := make([][]logquery.LogSource, len(srcs))
	var wg sync.WaitGroup
	for i := range srcs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			src := srcs[i]
			out, hit := p.listOneSource(ctx, src)
			p.logger.Info("[logquery-aws] list sources stage done",
				elog.String("bucket", src.bucket),
				elog.String("kind", src.kind),
				elog.Int("count", len(out)),
				elog.String("cache", hit.String()),
				elog.Int64("duration_ms", time.Since(start).Milliseconds()))
			slots[i] = out
		}()
	}
	wg.Wait()
	var out []logquery.LogSource
	for _, s := range slots {
		out = append(out, s...)
	}
	return out, nil
}

// listOneSource 单 catalog 源枚举(前缀发现走 SWR 缓存;失败隔离返回 nil)。
func (p *provider) listOneSource(ctx context.Context, src aSource) (out []logquery.LogSource, hit cacheHit) {
	client, err := p.clientFor(src.bucket, src.region)
	if err != nil {
		p.logger.Warn("[logquery-aws] s3 client failed", elog.FieldErr(err))
		return nil, cacheMiss
	}
	switch src.kind {
	case "waf-json":
		// AWSLogs/<acct>/WAFLogs/cloudfront/<acl>/ -> 深钻 4 级取 ACL
		key := src.bucket + "/" + src.prefix + "/waf4"
		aclPrefixes, h, err := awsPrefixCache.get(ctx, key, func(cctx context.Context) ([]string, error) {
			return walkPrefixes(cctx, client, src.bucket, src.prefix, 4)
		})
		hit = h
		if err != nil {
			p.logger.Warn("[logquery-aws] walk waf prefixes failed",
				elog.String("bucket", src.bucket), elog.FieldErr(err))
			return nil, hit
		}
		for _, pref := range aclPrefixes {
			acl := pathBase(strings.TrimSuffix(pref, "/"))
			out = append(out, p.toSource(src, acl, acl, true))
		}
	case "cloudfront-tsv":
		key := src.bucket + "/cloudfront1"
		domainPrefixes, h, err := awsPrefixCache.get(ctx, key, func(cctx context.Context) ([]string, error) {
			return listCommonPrefixes(cctx, client, src.bucket, "")
		})
		hit = h
		if err != nil {
			p.logger.Warn("[logquery-aws] list domains failed",
				elog.String("bucket", src.bucket), elog.FieldErr(err))
			return nil, hit
		}
		for _, pref := range domainPrefixes {
			d := strings.TrimSuffix(pref, "/")
			out = append(out, p.toSource(src, d, d, true))
		}
	}
	return out, hit
}

// toSource catalog+资源 -> LogSource。
func (p *provider) toSource(src aSource, resourceID, name string, enabled bool) logquery.LogSource {
	return logquery.LogSource{
		Cloud:       domain.CloudProviderAWS,
		AccountID:   fmt.Sprintf("%d", p.account.ID),
		AccountName: p.account.Name,
		Region:      src.region,
		LogType:     src.logType,
		ResourceID:  resourceID,
		Name:        src.name + " / " + name,
		Enabled:     enabled,
		Note:        src.note,
	}
}

// Search 窗口内日志:逐源列对象(最新优先)-> 下载解压 -> 按格式解析 ->
// 时间过滤 -> 归并倒序。单源失败隔离。
func (p *provider) Search(ctx context.Context, account *domain.CloudAccount, params logquery.SearchParams) ([]logquery.LogEntry, error) {
	if params.EndTime <= params.StartTime {
		return nil, fmt.Errorf("aws logquery: invalid time window")
	}
	limit := params.Limit
	if limit <= 0 || limit > 3000 {
		limit = 100
	}
	resourceFilter := map[string]bool{}
	for _, r := range params.Resources {
		resourceFilter[r] = true
	}
	regionFilter := map[string]bool{}
	for _, r := range params.Regions {
		regionFilter[r] = true
	}

	type scanTarget struct {
		src       aSource
		prefix    string // 对象列举前缀(源级或资源级)
		resource  string // 资源标识(ACL/域名;空=整源)
	}
	var targets []scanTarget
	for _, src := range p.sourcesOf() {
		if len(regionFilter) > 0 && !regionFilter[src.region] {
			continue
		}
		switch src.kind {
		case "waf-json":
			client, err := p.clientFor(src.bucket, src.region)
			if err != nil {
				continue
			}
			// 前缀发现与 ListLogSources 共用同一 SWR 缓存(此前 Search 每请求
			// 裸钻 4 级 ×2 桶,热查询也要 12s+ ——退化主因)
			key := src.bucket + "/" + src.prefix + "/waf4"
			prefixes, _, err := awsPrefixCache.get(ctx, key, func(cctx context.Context) ([]string, error) {
				return walkPrefixes(cctx, client, src.bucket, src.prefix, 4)
			})
			if err != nil {
				p.logger.Warn("[logquery-aws] walk waf prefixes failed",
					elog.String("bucket", src.bucket), elog.FieldErr(err))
				continue
			}
			for _, pref := range prefixes {
				acl := pathBase(strings.TrimSuffix(pref, "/"))
				if len(resourceFilter) > 0 && !resourceFilter[acl] {
					continue
				}
				targets = append(targets, scanTarget{src: src, prefix: pref, resource: acl})
			}
		case "cloudfront-tsv":
			client, err := p.clientFor(src.bucket, src.region)
			if err != nil {
				continue
			}
			key := src.bucket + "/cloudfront1"
			prefixes, _, err := awsPrefixCache.get(ctx, key, func(cctx context.Context) ([]string, error) {
				return listCommonPrefixes(cctx, client, src.bucket, "")
			})
			if err != nil {
				p.logger.Warn("[logquery-aws] list domains failed",
					elog.String("bucket", src.bucket), elog.FieldErr(err))
				continue
			}
			for _, pref := range prefixes {
				d := strings.TrimSuffix(pref, "/")
				if len(resourceFilter) > 0 && !resourceFilter[d] {
					continue
				}
				targets = append(targets, scanTarget{src: src, prefix: pref, resource: d})
			}
		}
	}

	// ---- 源级并发(8 上限,单源失败隔离) ----
	results := make([][]logquery.LogEntry, len(targets))
	sem := make(chan struct{}, maxFanoutConcurrency)
	var wg sync.WaitGroup
	for i, tgt := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			logs, err := p.scanTarget(ctx, tgt.src, tgt.prefix, tgt.resource, params, limit)
			if err != nil {
				p.logger.Warn("[logquery-aws] scan failed",
					elog.String("bucket", tgt.src.bucket), elog.String("prefix", tgt.prefix),
					elog.FieldErr(err))
				return
			}
			results[i] = logs
		}()
	}
	wg.Wait()

	var entries []logquery.LogEntry
	for i := range targets {
		for _, e := range results[i] {
			// 字段筛选:映射后统一字段语义过滤(与阿里/华为一致)
			if logquery.EntryMatches(e, params.Filters) {
				entries = append(entries, e)
			}
		}
	}
	// limit 为每日志源(ACL/域名)上限(ADR D4);归并后全局 1000 硬顶,
	// 不做 provider 级总截断(防热点域名吃掉全部配额,长尾域名被挤出局)
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].GetTimestamp() > entries[j].GetTimestamp()
	})
	const providerCap = 3000
	if len(entries) > providerCap {
		entries = entries[:providerCap]
	}
	return entries, nil
}

// scanTarget 单源扫描:列对象(最新优先)-> 并发下载解析 -> 时间倒序截取 limit。
// GetObject 是 S3 往返,逐对象串行是 AWS 搜索数秒的主因;候选对象(≤
// maxObjectsPerScan)按 perTargetConcurrency 并发下载解析,收尾统一按时间
// 倒序截取 limit(并发不改变"每源最新 limit 条"语义,只压缩墙钟)。
func (p *provider) scanTarget(ctx context.Context, src aSource, prefix, resource string, params logquery.SearchParams, limit int) ([]logquery.LogEntry, error) {
	client, err := p.clientFor(src.bucket, src.region)
	if err != nil {
		return nil, err
	}
	// 键内嵌日期的布局(WAF 嵌套目录)按窗口起点跳过历史数据;
	// CloudFront 扁平布局(dist 前缀)无法按日期定位,靠 maxKeys+最新优先容忍
	startAfter := ""
	if src.kind == "waf-json" {
		// <acl>/YYYY/MM/DD/HH:窗口起点(含回看)所在小时,前一小时兜底跨文件边界
		floor := time.UnixMilli(params.StartTime).Add(-src.lookback).Add(-time.Hour)
		startAfter = prefix + s3KeyDateFloor(floor)
	}
	objects, err := listObjects(ctx, client, src.bucket, prefix, startAfter, 500)
	if err != nil {
		return nil, err
	}
	notBefore := time.UnixMilli(params.StartTime).Add(-src.lookback)
	notAfter := time.UnixMilli(params.EndTime).Add(time.Hour) // 投递延迟容忍

	// 组装下载候选:最新优先 + 时间剪枝 + 上限 maxObjectsPerScan。
	// LastModified 过旧即止(对象按最后修改≈投递序排列,其后更旧)。
	var candidates []s3Object
	for _, o := range objects {
		if len(candidates) >= maxObjectsPerScan {
			break
		}
		if o.LastModified.Before(notBefore) {
			break
		}
		if o.LastModified.After(notAfter) {
			continue // 未来对象(时钟偏差),跳过
		}
		candidates = append(candidates, o)
	}

	// 并发下载解析:worker 池按候选序(最新优先)取对象,shared collected 计数
	// 用于"攒够 limit 即停"——但并发下载中最多 perTargetConcurrency 个对象在
	// 途会略超取;收尾按时间倒序截取 limit 兜底。相比整批下载全部候选(每源
	// 最多 60 对象,超取拉爆 S3),此版只在最新对象未凑满 limit 时才继续下载。
	var collected atomic.Int64
	jobs := make(chan s3Object)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var entries []logquery.LogEntry
	for w := 0; w < perTargetConcurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for o := range jobs {
				if collected.Load() >= int64(limit) {
					continue // 已达 limit,跳过后续对象(仍要消费掉 channel)
				}
				body, err := getObjectBytes(ctx, client, src.bucket, o.Key, maxObjectBytes)
				if err != nil {
					p.logger.Warn("[logquery-aws] get object failed",
						elog.String("key", o.Key), elog.FieldErr(err))
					continue
				}
				meta := logquery.LogMeta{
					Cloud:       domain.CloudProviderAWS,
					AccountID:   fmt.Sprintf("%d", p.account.ID),
					AccountName: p.account.Name,
					Region:      src.region,
					ResourceID:  resource,
					Source:      src.bucket + "/" + o.Key,
				}
				var parsed []logquery.LogEntry
				switch src.kind {
				case "waf-json":
					parsed = parseWAFJSONLines(meta, body, params, int64(limit))
				case "cloudfront-tsv":
					parsed = parseCloudFrontBody(meta, body, params, int64(limit))
				}
				collected.Add(int64(len(parsed)))
				mu.Lock()
				entries = append(entries, parsed...)
				mu.Unlock()
			}
		}()
	}
	for _, o := range candidates {
		jobs <- o
	}
	close(jobs)
	wg.Wait()

	// 并发下载可能略超取,按时间倒序截取 limit 保持"每源最新 limit 条"语义。
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].GetTimestamp() > entries[j].GetTimestamp()
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

// parseWAFJSONLines JSON Lines -> 过滤窗口 -> 统一模型。
func parseWAFJSONLines(meta logquery.LogMeta, body []byte, params logquery.SearchParams, remain int64) []logquery.LogEntry {
	var out []logquery.LogEntry
	for _, line := range strings.Split(string(body), "\n") {
		if int64(len(out)) >= remain {
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue // 非法行跳过
		}
		ts := logquery.ParseTimeMs(raw["timestamp"])
		if ts < params.StartTime || ts > params.EndTime {
			continue
		}
		out = append(out, mapWAFJSON(meta, raw))
	}
	return out
}

// parseCloudFrontBody TSV 正文(#Fields 头驱动)-> 过滤窗口 -> 统一模型。
func parseCloudFrontBody(meta logquery.LogMeta, body []byte, params logquery.SearchParams, remain int64) []logquery.LogEntry {
	var fields []string
	var out []logquery.LogEntry
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#Version") {
			continue
		}
		if strings.HasPrefix(line, "#Fields:") {
			fields = strings.Fields(strings.TrimPrefix(line, "#Fields:"))
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		if int64(len(out)) >= remain {
			break
		}
		if len(fields) == 0 {
			continue // 无字段头的正文无法解析
		}
		e := mapCloudFrontLine(meta, fields, line)
		if e.Timestamp < params.StartTime || e.Timestamp > params.EndTime {
			continue
		}
		out = append(out, e)
	}
	return out
}

// pathBase 取路径末段("AWSLogs/123/WAFLogs/cloudfront/<acl>/" -> <acl>)。
func pathBase(s string) string {
	s = strings.TrimSuffix(s, "/")
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		return s[i+1:]
	}
	return s
}
