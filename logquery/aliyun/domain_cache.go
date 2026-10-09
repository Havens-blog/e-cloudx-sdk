// 域名枚举缓存:域名分布分钟级稳定(实际小时级不变),sources 页每次切
// Tab/云都会重拉,缓存 30 分钟内零 API 调用(SLS SQL 分组 ~1.5s/条,混装源
// 串行曾是 WAF sources 5s 的主因)。过期采用 SWR:有旧值先返回旧值、后台
// 单飞刷新,前台永不阻塞(源清单变动频率远低于请求频率)。
package aliyun

import (
	"sync"
	"time"
)

// domainCacheTTL 域名枚举缓存新鲜时长(变动频率低:30 分钟)。
const domainCacheTTL = 30 * time.Minute

// domainCache 进程内 TTL 缓存(键 = project/logstore/kind 摘要)。
type domainCache struct {
	mu         sync.RWMutex
	entries    map[string]domainCacheEntry
	refreshing map[string]bool // 后台刷新单飞(防并发重复拉取)
}

type domainCacheEntry struct {
	domains []string
	expires time.Time
}

func newDomainCache() *domainCache {
	return &domainCache{entries: make(map[string]domainCacheEntry), refreshing: make(map[string]bool)}
}

// get 取缓存副本。SWR 语义:
//   - 新鲜命中:返回缓存 + hit=true(零 API 调用);
//   - 已过期但有旧值:立即返回旧值 + hit=false,后台单飞刷新(前台不阻塞);
//   - 无旧值:同步 fetch 回填 + hit=false。
//
// 调用方在 hit=false 且返回非空时应直接使用返回的旧值(阶段耗时埋点沿用)。
func (c *domainCache) get(key string, fetch func() []string) ([]string, bool) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if ok && time.Now().Before(e.expires) {
		out := make([]string, len(e.domains))
		copy(out, e.domains)
		return out, true
	}
	if ok {
		// 过期有旧值:SWR —— 前台先拿旧值,后台单飞刷新
		c.refreshAsync(key, fetch)
		out := make([]string, len(e.domains))
		copy(out, e.domains)
		return out, false
	}
	domains := fetch()
	if len(domains) > 0 {
		c.set(key, domains)
	}
	return domains, false
}

// refreshAsync 后台单飞刷新(goroutine 生命周期脱离请求 ctx;失败保留旧值)。
func (c *domainCache) refreshAsync(key string, fetch func() []string) {
	c.mu.Lock()
	if c.refreshing[key] {
		c.mu.Unlock()
		return
	}
	c.refreshing[key] = true
	c.mu.Unlock()
	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.refreshing, key)
			c.mu.Unlock()
		}()
		if domains := fetch(); len(domains) > 0 {
			c.set(key, domains)
		}
	}()
}

// set 写入(TTL 重置;空结果不缓存——源数据未就绪时不长期锁死)。
func (c *domainCache) set(key string, domains []string) {
	if len(domains) == 0 {
		return
	}
	cp := make([]string, len(domains))
	copy(cp, domains)
	c.mu.Lock()
	c.entries[key] = domainCacheEntry{domains: cp, expires: time.Now().Add(domainCacheTTL)}
	c.mu.Unlock()
}

// expire 强制失效(测试用)。
func (c *domainCache) expire(key string) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

// domainEnumCache 进程级共享实例(provider 每账号一个,缓存跨实例共享)。
var domainEnumCache = newDomainCache()

// logstoreEnumCache logstore 清单缓存(与域名枚举同型:清单分钟级稳定,
// Search/Aggregate/ListLogSources 每请求都重复 ListLogStore 曾是 SLB
// sources 与查询延迟的固定开销)。
var logstoreEnumCache = newDomainCache()
