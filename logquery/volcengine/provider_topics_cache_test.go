package volcengine

import (
	"errors"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/Havens-blog/e-cloudx-sdk/logquery"
	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/volc-sdk-golang/service/tls"
)

// fakeTLSClient 只实现 DescribeTopics(其余方法经嵌入接口保留,调用即 panic;
// 本文件的被测路径只会碰 DescribeTopics)。
type fakeTLSClient struct {
	tls.Client
	pages map[int]*tls.DescribeTopicsResponse
	errs  map[int]error
	calls map[int]int
}

func newFakeTLSClient() *fakeTLSClient {
	return &fakeTLSClient{pages: map[int]*tls.DescribeTopicsResponse{}, errs: map[int]error{}, calls: map[int]int{}}
}

func (f *fakeTLSClient) DescribeTopics(req *tls.DescribeTopicsRequest) (*tls.DescribeTopicsResponse, error) {
	pn := req.PageNumber
	f.calls[pn]++
	if err, ok := f.errs[pn]; ok && err != nil {
		return nil, err
	}
	if resp, ok := f.pages[pn]; ok {
		return resp, nil
	}
	return &tls.DescribeTopicsResponse{}, nil
}

func tlsTopic(id string) *tls.Topic {
	return &tls.Topic{TopicID: id, TopicName: id + "-name"}
}

func tlsPage(topics ...*tls.Topic) *tls.DescribeTopicsResponse {
	return &tls.DescribeTopicsResponse{Topics: topics, Total: len(topics)}
}

// resetTopicsCache 清空进程级 topic 清单缓存,并返回恢复函数。
func resetTopicsCache(t *testing.T) func() {
	t.Helper()
	topicsCacheInst.mu.Lock()
	saved := topicsCacheInst.entries
	topicsCacheInst.entries = map[string]topicsEntry{}
	topicsCacheInst.mu.Unlock()
	return func() {
		topicsCacheInst.mu.Lock()
		topicsCacheInst.entries = saved
		topicsCacheInst.mu.Unlock()
	}
}

func newCacheTestProvider() *provider {
	return &provider{
		logType: logquery.LogTypeApp,
		account: &domain.CloudAccount{ID: 1, Name: "t", AccessKeyID: "k", AccessKeySecret: "s"},
		logger:  elog.DefaultLogger,
	}
}

func TestFetchTopicsCompleteWhenAllPagesOK(t *testing.T) {
	page1 := make([]*tls.Topic, 0, 100)
	for i := 0; i < 100; i++ {
		page1 = append(page1, tlsTopic("t"+string(rune('A'+i%26))+string(rune('a'+i/26))))
	}
	fake := newFakeTLSClient()
	fake.pages[1] = &tls.DescribeTopicsResponse{Topics: page1, Total: 150}
	fake.pages[2] = tlsPage(tlsTopic("x1"), tlsTopic("x2"), tlsTopic("x3"), tlsTopic("x4"), tlsTopic("x5"))
	p := newCacheTestProvider()
	all, complete := p.fetchTopics("cn-guangzhou", fake)
	if !complete {
		t.Fatal("全部页成功应判定为完整枚举")
	}
	if len(all) != 105 {
		t.Fatalf("应合并 100+5 条,得到 %d", len(all))
	}
}

func TestFetchTopicsIncompleteWhenPageFails(t *testing.T) {
	page1 := make([]*tls.Topic, 0, 100)
	for i := 0; i < 100; i++ {
		page1 = append(page1, tlsTopic("p"+string(rune('A'+i%26))+string(rune('a'+i/26))))
	}
	fake := newFakeTLSClient()
	fake.pages[1] = &tls.DescribeTopicsResponse{Topics: page1, Total: 150}
	fake.errs[2] = errors.New("InternalError: timeout")
	p := newCacheTestProvider()
	all, complete := p.fetchTopics("cn-guangzhou", fake)
	if complete {
		t.Fatal("并发页失败必须判定为不完整")
	}
	if len(all) != 100 {
		t.Fatalf("残缺清单应尽力返回首页 100 条,得到 %d", len(all))
	}
}

func TestFetchTopicsFirstPageFailsReturnsNilIncomplete(t *testing.T) {
	fake := newFakeTLSClient()
	fake.errs[1] = errors.New("AuthFailed")
	p := newCacheTestProvider()
	all, complete := p.fetchTopics("cn-beijing", fake)
	if complete || all != nil {
		t.Fatalf("首页失败应返回 (nil,false),得到 (%v,%v)", all, complete)
	}
}

func TestListTopicsWithCacheDoesNotCacheIncomplete(t *testing.T) {
	restore := resetTopicsCache(t)
	defer restore()
	page1 := make([]*tls.Topic, 0, 100)
	for i := 0; i < 100; i++ {
		page1 = append(page1, tlsTopic("c"+string(rune('A'+i%26))+string(rune('a'+i/26))))
	}
	fake := newFakeTLSClient()
	fake.pages[1] = &tls.DescribeTopicsResponse{Topics: page1, Total: 150}
	fake.errs[2] = errors.New("InternalError")
	p := newCacheTestProvider()
	got := p.listTopicsWithCache("cn-shanghai", fake)
	if len(got) != 100 {
		t.Fatalf("降级返回应为 100 条,得到 %d", len(got))
	}
	topicsCacheInst.mu.Lock()
	_, polluted := topicsCacheInst.entries[topicsCacheKey(1, "cn-shanghai")]
	topicsCacheInst.mu.Unlock()
	if polluted {
		t.Fatal("缺页的部分清单不得写入缓存(issue #865)")
	}
}

func TestListTopicsWithCacheCachesComplete(t *testing.T) {
	restore := resetTopicsCache(t)
	defer restore()
	fake := newFakeTLSClient()
	fake.pages[1] = tlsPage(tlsTopic("v1"), tlsTopic("v2"))
	p := newCacheTestProvider()
	got := p.listTopicsWithCache("cn-guangzhou", fake)
	if len(got) != 2 {
		t.Fatalf("完整枚举应返回 2 条,得到 %d", len(got))
	}
	topicsCacheInst.mu.Lock()
	e, ok := topicsCacheInst.entries[topicsCacheKey(1, "cn-guangzhou")]
	topicsCacheInst.mu.Unlock()
	if !ok {
		t.Fatal("完整清单应写入缓存")
	}
	if len(e.topics) != 2 {
		t.Fatalf("缓存里应为 2 条,得到 %d", len(e.topics))
	}
}

// 同一进程两个火山账号共用同一个 region 时,清单缓存必须按账号隔离
// (与腾讯侧同一问题:缓存 key 曾只有 region)。
func TestListTopicsWithCacheIsolatedPerAccount(t *testing.T) {
	restore := resetTopicsCache(t)
	defer restore()
	region := "cn-guangzhou"

	acctA := newCacheTestProvider()
	fakeA := newFakeTLSClient()
	fakeA.pages[1] = tlsPage(tlsTopic("A1"), tlsTopic("A2"))
	if got := acctA.listTopicsWithCache(region, fakeA); len(got) != 2 {
		t.Fatalf("账号 A 应拿到 2 条,得到 %d", len(got))
	}

	acctB := newCacheTestProvider()
	acctB.account = &domain.CloudAccount{ID: 2, Name: "b", AccessKeyID: "k2", AccessKeySecret: "s2"}
	fakeB := newFakeTLSClient()
	fakeB.pages[1] = tlsPage(tlsTopic("B1"))
	got := acctB.listTopicsWithCache(region, fakeB)
	if len(got) != 1 || got[0].TopicID != "B1" {
		t.Fatalf("账号 B 必须拿自己的清单(1 条 B1),不能命中账号 A 的缓存,得到 %d 条", len(got))
	}
	if fakeB.calls[1] != 1 {
		t.Fatalf("账号 B 不应命中账号 A 的缓存,应真实调用第 1 页 1 次,得到 %d", fakeB.calls[1])
	}
}
