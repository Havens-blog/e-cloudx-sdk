package aws

import (
	"sync"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/gotomicro/ego/core/elog"
)

// s3Object S3 对象清单条目。
type s3Object struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// s3Client 按 region 构造 S3 client(静态凭证)。
func s3Client(account *domain.CloudAccount, region string) (*s3.Client, error) {
	if account.AccessKeyID == "" || account.AccessKeySecret == "" {
		return nil, fmt.Errorf("aws logquery: account %d missing credentials", account.ID)
	}
	cfg, err := awscfg.LoadDefaultConfig(context.Background(),
		awscfg.WithRegion(region),
		awscfg.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			account.AccessKeyID, account.AccessKeySecret, "")))
	if err != nil {
		return nil, fmt.Errorf("aws logquery: load config: %w", err)
	}
	return s3.NewFromConfig(cfg), nil
}

// resolveBucketRegion 探测桶真实 region(HeadBucket 跨区访问返回 301,
// 响应头 x-amz-bucket-region 携带真实区域;Go SDK v2 不像 boto3 自动跟随)。
// 同区/探测失败返回空串(调用方回退 hint region)。
func resolveBucketRegion(ctx context.Context, c *s3.Client, bucket string) string {
	_, err := c.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &bucket})
	if err == nil {
		return "" // 同区,无需纠正
	}
	return bucketRegionFromError(err)
}

// bucketRegionFromError 从错误链提取真实 region(smithyhttp.ResponseError
// 携带原始 *http.Response,x-amz-bucket-region 头由 301 响应返回)。
func bucketRegionFromError(err error) string {
	var re *smithyhttp.ResponseError
	if errors.As(err, &re) && re.Response != nil {
		return re.Response.Header.Get("x-amz-bucket-region")
	}
	return ""
}

// listObjects 列举前缀下对象(自动分页,maxKeys 保护)。
// startAfter 非空时从该 key 起列举(S3 按键字典序;键内嵌日期时=时间序,
// 用于跳过历史数据,避免 maxKeys 截断只拿到最旧对象)。
func listObjects(ctx context.Context, c *s3.Client, bucket, prefix, startAfter string, maxKeys int) ([]s3Object, error) {
	var out []s3Object
	in := &s3.ListObjectsV2Input{
		Bucket: &bucket,
		Prefix: &prefix,
	}
	if startAfter != "" {
		in.StartAfter = &startAfter
	}
	p := s3.NewListObjectsV2Paginator(c, in)
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("s3 list %s/%s: %w", bucket, prefix, err)
		}
		for _, o := range page.Contents {
			sz := int64(0)
			if o.Size != nil {
				sz = *o.Size
			}
			out = append(out, s3Object{Key: *o.Key, Size: sz, LastModified: *o.LastModified})
		}
		if len(out) >= maxKeys {
			out = out[:maxKeys]
			break
		}
	}
	// 最新优先
	sort.Slice(out, func(i, j int) bool { return out[i].LastModified.After(out[j].LastModified) })
	return out, nil
}

// s3KeyDateFloor 窗口起点(含投递回看)对应的键内嵌日期段。
// WAF 布局 <acl>/YYYY/MM/DD/HH/...;CloudFront 布局 <dist>.YYYY-MM-DD-HH.<hash>.gz。
func s3KeyDateFloor(t time.Time) string {
	return t.UTC().Format("2006/01/02/15")
}

// listCommonPrefixes 列举前缀下一级"目录"(Delimiter=/)。
func listCommonPrefixes(ctx context.Context, c *s3.Client, bucket, prefix string) ([]string, error) {
	var out []string
	p := s3.NewListObjectsV2Paginator(c, &s3.ListObjectsV2Input{
		Bucket:    &bucket,
		Prefix:    &prefix,
		Delimiter: strPtr("/"),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("s3 list prefixes %s/%s: %w", bucket, prefix, err)
		}
		for _, cp := range page.CommonPrefixes {
			out = append(out, *cp.Prefix)
		}
	}
	return out, nil
}

// getObjectBytes 下载对象;gz 自动解压;sizeCap 超限截断保护。
func getObjectBytes(ctx context.Context, c *s3.Client, bucket, key string, sizeCap int64) ([]byte, error) {
	resp, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: &bucket, Key: &key})
	if err != nil {
		return nil, fmt.Errorf("s3 get %s/%s: %w", bucket, key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var reader io.Reader = resp.Body
	if sizeCap > 0 {
		reader = io.LimitReader(resp.Body, sizeCap)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("s3 read %s/%s: %w", bucket, key, err)
	}
	if len(key) > 3 && key[len(key)-3:] == ".gz" {
		gz, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("gzip %s/%s: %w", bucket, key, err)
		}
		defer func() { _ = gz.Close() }()
		body, err = io.ReadAll(io.LimitReader(gz, sizeCap*4)) // 压缩比 ~4x 保护
		if err != nil {
			return nil, fmt.Errorf("gzip read %s/%s: %w", bucket, key, err)
		}
	}
	return body, nil
}

// walkPrefixes 逐级下钻 delimiter 目录(用于 WAFLogs/<acct>/WAFLogs/cloudfront/<acl>
// 四级布局解析,不硬编码账号 ID)。
// walkPrefixes 逐层钻取 S3 前缀(层内并发:WAF 源 4 级深钻曾是 sources
// 5.7s 的主因——4 层串行 ListObjectsV2,每往返 ~200ms 跨洋延迟 ×8 个 ACL)。
func walkPrefixes(ctx context.Context, c *s3.Client, bucket, root string, depth int) ([]string, error) {
	current := []string{root}
	for i := 0; i < depth; i++ {
		type result struct {
			prefixes []string
			err      error
		}
		results := make([]result, len(current))
		var wg sync.WaitGroup
		for j, p := range current {
			wg.Add(1)
			go func() {
				defer wg.Done()
				prefixes, err := listCommonPrefixes(ctx, c, bucket, p)
				results[j] = result{prefixes: prefixes, err: err}
			}()
		}
		wg.Wait()
		var next []string
		for _, r := range results {
			if r.err != nil {
				return nil, r.err
			}
			next = append(next, r.prefixes...)
		}
		if len(next) == 0 {
			return nil, nil
		}
		current = next
	}
	return current, nil
}

func strPtr(s string) *string { return &s }

// ---------------------------------------------------------------------
// 前缀发现缓存:S3 无 server-side 前缀索引,根前缀 list(带 Delimiter)
// 要扫满页才吐 CommonPrefixes,实测 AWSLogs/ 首层 7s、单层 RTT 230ms;
// 目录结构变化极慢,缓存 10 分钟内零 API 调用(sources 每次切 Tab 重拉,
// 无缓存时 WAF sources 5.7s 全耗在此)。
// 过期不阻塞:宽限期内先返回旧清单+后台刷新(SWR,proposal 实现注记 3),
// 刷新失败退避 30s 防故障期每请求重试。
// ---------------------------------------------------------------------

// prefixCacheTTL 前缀发现缓存新鲜时长。
const prefixCacheTTL = 30 * time.Minute

// prefixStaleGrace 新鲜期过后仍供旧清单的宽限时长(期间后台刷新)。
const prefixStaleGrace = 20 * time.Minute

// prefixRefreshBackoff 后台刷新失败后的重试退避。
const prefixRefreshBackoff = 30 * time.Second

// prefixFetchTimeout 前缀列举(含后台刷新)单次超时。
const prefixFetchTimeout = 30 * time.Second

// cacheHit 缓存命中形态(日志埋点用)。
type cacheHit int

const (
	cacheMiss  cacheHit = iota // 未命中,同步回填
	cacheFresh                 // 新鲜期内直接命中
	cacheStale                 // 宽限期内供旧清单,后台刷新中
)

func (h cacheHit) String() string {
	switch h {
	case cacheFresh:
		return "fresh"
	case cacheStale:
		return "stale"
	}
	return "miss"
}

type prefixCacheEntry struct {
	prefixes     []string
	freshUntil   time.Time
	expires      time.Time // 超过则同步重取(stale 宽限上限)
	backoffUntil time.Time // 后台刷新失败退避(退避期不重试,续供旧值)
}

// prefixCache 进程内 SWR 缓存(键 = bucket + root + depth)。
type prefixCache struct {
	mu       sync.RWMutex
	entries  map[string]prefixCacheEntry
	inflight map[string]bool // 后台刷新单飞
}

// awsPrefixCache 进程级共享实例。
var awsPrefixCache = newPrefixCache()

func newPrefixCache() *prefixCache {
	return &prefixCache{
		entries:  make(map[string]prefixCacheEntry),
		inflight: make(map[string]bool),
	}
}

// get 命中返回缓存副本;新鲜期外宽限内返回旧清单并触发后台刷新(单飞);
// 完全过期/缺失同步回填。fetch 失败不覆盖旧清单。
func (c *prefixCache) get(ctx context.Context, key string, fetch func(context.Context) ([]string, error)) ([]string, cacheHit, error) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if ok {
		now := time.Now()
		if now.Before(e.freshUntil) {
			return copyPrefixes(e.prefixes), cacheFresh, nil
		}
		if now.Before(e.expires) {
			if now.After(e.backoffUntil) {
				c.refreshAsync(key, fetch)
			}
			return copyPrefixes(e.prefixes), cacheStale, nil
		}
	}
	prefixes, err := fetch(ctx)
	if err != nil {
		return nil, cacheMiss, err
	}
	c.store(key, prefixes)
	return prefixes, cacheMiss, nil
}

// refreshAsync 后台刷新(单飞;成功换新,失败退避续供旧清单)。
func (c *prefixCache) refreshAsync(key string, fetch func(context.Context) ([]string, error)) {
	c.mu.Lock()
	if c.inflight[key] {
		c.mu.Unlock()
		return
	}
	c.inflight[key] = true
	c.mu.Unlock()
	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.inflight, key)
			c.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), prefixFetchTimeout)
		defer cancel()
		prefixes, err := fetch(ctx)
		if err != nil {
			elog.Warn("[logquery-aws] prefix cache background refresh failed",
				elog.String("key", key), elog.FieldErr(err))
			c.mu.Lock()
			if e, ok := c.entries[key]; ok {
				e.backoffUntil = time.Now().Add(prefixRefreshBackoff) // 退避:续供旧清单
				c.entries[key] = e
			}
			c.mu.Unlock()
			return
		}
		if len(prefixes) > 0 {
			c.store(key, prefixes)
		}
	}()
}

// store 写入(空结果不缓存——目录被清空时允许重探)。
func (c *prefixCache) store(key string, prefixes []string) {
	if len(prefixes) == 0 {
		return
	}
	now := time.Now()
	c.mu.Lock()
	c.entries[key] = prefixCacheEntry{
		prefixes:   copyPrefixes(prefixes),
		freshUntil: now.Add(prefixCacheTTL),
		expires:    now.Add(prefixCacheTTL + prefixStaleGrace),
	}
	c.mu.Unlock()
}

func copyPrefixes(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// forceStale 测试用:条目转入宽限期(保数据,新鲜窗归零)。
func (c *prefixCache) forceStale(key string) {
	c.mu.Lock()
	if e, ok := c.entries[key]; ok {
		e.freshUntil = time.Now().Add(-time.Second)
		c.entries[key] = e
	}
	c.mu.Unlock()
}

// forceExpire 测试用:条目转入完全过期(下次同步重取)。
func (c *prefixCache) forceExpire(key string) {
	c.mu.Lock()
	if e, ok := c.entries[key]; ok {
		e.freshUntil = time.Now().Add(-time.Second)
		e.expires = time.Now().Add(-time.Second)
		c.entries[key] = e
	}
	c.mu.Unlock()
}
