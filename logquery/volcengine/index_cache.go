// 索引探测结果(indexCache)的磁盘持久化:进程重启(CD 热重载)会清空内存缓存,
// 落盘后重启即可恢复 Enabled 准确性,免去 ~1min 后台预热。键 = topicID(全局唯一,
// 非租户维度),文件 ~数十 KB,写入走原子 rename(临时文件 + rename)。
//
// 持久化是缓存增强:读失败(无文件/损坏)静默回落冷探测,写失败只丢增强不丢正确性。
package volcengine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// persistEntry 磁盘格式(与内存 indexEntry 同构,保留 expires 以延续 TTL 语义)。
type persistEntry struct {
	Enabled bool  `json:"enabled"`
	Stale   bool  `json:"stale"`
	Expires int64 `json:"expires"` // unix 毫秒
}

// indexPersistOnce 惰性初始化只跑一次:加载磁盘缓存 + 启动定时刷盘。
var indexPersistOnce sync.Once

// indexFlushEvery 刷盘间隔(仅脏时写;探测结果散落 ~50s 内,10s 间隔能及时落盘)。
const indexFlushEvery = 10 * time.Second

// indexCachePath 持久化文件路径(env 覆盖;默认系统临时目录——缓存丢失仅回落冷探测)。
func indexCachePath() string {
	if v := os.Getenv("LOGQUERY_INDEX_CACHE_FILE"); v != "" {
		return v
	}
	return filepath.Join(os.TempDir(), "e-cam-logquery-index-cache.json")
}

// loadIndexCache 从磁盘恢复索引缓存(仅未过期项,延续 TTL)。
func loadIndexCache(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var m map[string]persistEntry
	if json.Unmarshal(data, &m) != nil {
		return
	}
	now := time.Now()
	indexCacheInst.mu.Lock()
	defer indexCacheInst.mu.Unlock()
	for id, e := range m {
		exp := time.UnixMilli(e.Expires)
		if exp.After(now) {
			indexCacheInst.entries[id] = indexEntry{enabled: e.Enabled, stale: e.Stale, expires: exp}
		}
	}
}

// saveIndexCache 原子落盘(临时文件 + rename,避免半写)。
func saveIndexCache(path string) {
	indexCacheInst.mu.Lock()
	m := make(map[string]persistEntry, len(indexCacheInst.entries))
	for id, e := range indexCacheInst.entries {
		m[id] = persistEntry{Enabled: e.enabled, Stale: e.stale, Expires: e.expires.UnixMilli()}
	}
	indexCacheInst.mu.Unlock()

	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// ensureIndexPersist 惰性:加载磁盘缓存 + 启动定时刷盘(仅脏时写)。
func ensureIndexPersist() {
	indexPersistOnce.Do(func() {
		path := indexCachePath()
		loadIndexCache(path)
		go func() {
			t := time.NewTicker(indexFlushEvery)
			defer t.Stop()
			for range t.C {
				indexCacheInst.mu.Lock()
				dirty := indexCacheInst.dirty
				indexCacheInst.dirty = false
				indexCacheInst.mu.Unlock()
				if dirty {
					saveIndexCache(path)
				}
			}
		}()
	})
}
