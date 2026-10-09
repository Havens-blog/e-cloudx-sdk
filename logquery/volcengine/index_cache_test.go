package volcengine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 验证磁盘持久化往返:写 → 清空内存 → 读回(未过期项恢复,过期项丢弃)。
func TestIndexCachePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index-cache.json")

	now := time.Now()
	// 直接篡改全局缓存(测试包内无其他用例共享该 map)
	indexCacheInst.mu.Lock()
	indexCacheInst.entries = map[string]indexEntry{
		"topic-a": {enabled: true, expires: now.Add(time.Hour)},   // 未过期:应恢复 enabled
		"topic-b": {enabled: false, expires: now.Add(time.Hour)},  // 未过期:应恢复 disabled
		"topic-c": {enabled: false, expires: now.Add(-time.Hour)}, // 已过期:应丢弃
	}
	indexCacheInst.mu.Unlock()

	if err := saveIndexCacheForTest(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	// 读回前清空内存模拟进程重启
	indexCacheInst.mu.Lock()
	indexCacheInst.entries = map[string]indexEntry{}
	indexCacheInst.mu.Unlock()
	loadIndexCache(path)

	indexCacheInst.mu.Lock()
	defer indexCacheInst.mu.Unlock()
	if !indexCacheInst.entries["topic-a"].enabled {
		t.Errorf("topic-a 应恢复 enabled=true")
	}
	if indexCacheInst.entries["topic-b"].enabled {
		t.Errorf("topic-b 应恢复 enabled=false")
	}
	if _, ok := indexCacheInst.entries["topic-c"]; ok {
		t.Errorf("topic-c 已过期应被丢弃")
	}
}

// saveIndexCacheForTest 重导出 saveIndexCache(路径入参已隔离,这里封装便于直接断言 error)。
func saveIndexCacheForTest(path string) error {
	saveIndexCache(path)
	return nil // saveIndexCache 写失败静默(增强型缓存),此处只验证读回正确性
}

// 冒烟:默认路径由 env 覆盖(开发态可用 LOGQUERY_INDEX_CACHE_FILE 定向)。
func TestIndexCachePath(t *testing.T) {
	t.Setenv("LOGQUERY_INDEX_CACHE_FILE", "C:/tmp/custom-index.json")
	if got := indexCachePath(); got != "C:/tmp/custom-index.json" {
		t.Errorf("indexCachePath() = %q, want env 覆盖值", got)
	}
	// 未设 env 时回退临时目录
	t.Setenv("LOGQUERY_INDEX_CACHE_FILE", "")
	if got := indexCachePath(); !filepath.IsAbs(got) {
		t.Errorf("默认 indexCachePath 应为绝对路径, got %q", got)
	}
	_ = os.Remove("C:/tmp/custom-index.json")
}
