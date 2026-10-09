// LTS 流清单缓存:流清单分钟级稳定,每次 sources 都 ListLogStreams ×2 组
// (实测 ~300ms/请求)曾是 huawei sources 热查询的固定开销;缓存 10 分钟内
// 零 API 调用(与 aliyun 域名/logstore、aws 前缀发现同型的进程级缓存)。
package huawei

import (
	"sync"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/logquery"
)

// sourceCacheTTL 流清单缓存新鲜时长(变动频率低:30 分钟)。
const sourceCacheTTL = 30 * time.Minute

type sourceCacheEntry struct {
	sources []logquery.LogSource
	expires time.Time
}

// sourceCache 进程内 TTL 缓存(键 = 账号/日志类型)。
type sourceCache struct {
	mu         sync.RWMutex
	entries    map[string]sourceCacheEntry
	refreshing map[string]bool // 后台刷新单飞
}

var sourceEnumCache = &sourceCache{entries: make(map[string]sourceCacheEntry), refreshing: make(map[string]bool)}

// get 取流清单缓存副本。SWR 语义:
//   - 新鲜命中:返回缓存 + hit=true;
//   - 已过期但有旧值:立即返回旧值 + hit=false,后台单飞刷新(前台不阻塞);
//   - 无旧值:同步 fetch(成功且非空才缓存;失败/空不缓存,不长期锁死)。
func (c *sourceCache) get(key string, fetch func() ([]logquery.LogSource, error)) ([]logquery.LogSource, bool, error) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if ok && time.Now().Before(e.expires) {
		return copySources(e.sources), true, nil
	}
	if ok {
		// 过期有旧值:SWR,前台先拿旧值,后台刷新
		c.refreshAsync(key, fetch)
		return copySources(e.sources), false, nil
	}
	sources, err := fetch()
	if err != nil || len(sources) == 0 {
		return sources, false, err
	}
	c.mu.Lock()
	c.entries[key] = sourceCacheEntry{sources: copySources(sources), expires: time.Now().Add(sourceCacheTTL)}
	c.mu.Unlock()
	return sources, false, nil
}

// refreshAsync 后台单飞刷新(脱离请求 ctx;失败保留旧值)。
func (c *sourceCache) refreshAsync(key string, fetch func() ([]logquery.LogSource, error)) {
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
		sources, err := fetch()
		if err != nil || len(sources) == 0 {
			return
		}
		c.mu.Lock()
		c.entries[key] = sourceCacheEntry{sources: copySources(sources), expires: time.Now().Add(sourceCacheTTL)}
		c.mu.Unlock()
	}()
}

// copySources 浅拷贝切片(LogSource 全值字段,共享底层数组安全)。
func copySources(in []logquery.LogSource) []logquery.LogSource {
	out := make([]logquery.LogSource, len(in))
	copy(out, in)
	return out
}
