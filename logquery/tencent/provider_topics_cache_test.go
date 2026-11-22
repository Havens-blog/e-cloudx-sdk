package tencent

import (
	"context"
	"errors"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/Havens-blog/e-cloudx-sdk/logquery"
	"github.com/gotomicro/ego/core/elog"
	cls "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cls/v20201016"
)

// fakeTopicsDescriber 按调用次序回放响应/错误:第 n 次调用先查 errs[n],
// 非 nil 即返回错误,否则返回 pages[n](若存在;不存在返回空响应=自然终止)。
type fakeTopicsDescriber struct {
	pages []*cls.DescribeTopicsResponse
	errs  []error
	calls int
}

func (f *fakeTopicsDescriber) DescribeTopicsWithContext(
	_ context.Context, _ *cls.DescribeTopicsRequest,
) (*cls.DescribeTopicsResponse, error) {
	n := f.calls
	f.calls++
	if n < len(f.errs) && f.errs[n] != nil {
		return nil, f.errs[n]
	}
	if n < len(f.pages) {
		return f.pages[n], nil
	}
	return &cls.DescribeTopicsResponse{}, nil
}

func topicPage(ids ...string) *cls.DescribeTopicsResponse {
	topics := make([]*cls.TopicInfo, 0, len(ids))
	for _, id := range ids {
		id := id
		name := id + "-name"
		topics = append(topics, &cls.TopicInfo{TopicId: &id, TopicName: &name})
	}
	total := int64(len(ids))
	return &cls.DescribeTopicsResponse{
		Response: &cls.DescribeTopicsResponseParams{Topics: topics, TotalCount: &total},
	}
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

func TestCollectTopicsCompleteOnShortPage(t *testing.T) {
	fake := &fakeTopicsDescriber{pages: []*cls.DescribeTopicsResponse{topicPage("a", "b", "c")}}
	ids, names, complete := collectTopics(context.Background(), elog.DefaultLogger, "ap-guangzhou", fake)
	if !complete {
		t.Fatal("短页应判定为完整枚举")
	}
	if len(ids) != 3 || names["a"] != "a-name" {
		t.Fatalf("ids=%v names=%v", ids, names)
	}
}

func TestCollectTopicsIncompleteOnError(t *testing.T) {
	full := make([]string, 100)
	for i := range full {
		full[i] = "topic-" + string(rune('A'+i%26)) + "-" + string(rune('a'+i/26))
	}
	fake := &fakeTopicsDescriber{
		pages: []*cls.DescribeTopicsResponse{topicPage(full...)},
		errs:  []error{nil, errors.New("RequestLimitExceeded: limit exceeded")},
	}
	ids, _, complete := collectTopics(context.Background(), elog.DefaultLogger, "ap-guangzhou", fake)
	if complete {
		t.Fatal("第 2 页失败必须判定为不完整")
	}
	if len(ids) != 100 {
		t.Fatalf("残缺清单应尽力返回第 1 页 100 条,得到 %d", len(ids))
	}
	if fake.calls != 2 {
		t.Fatalf("应恰好调用 2 次,得到 %d", fake.calls)
	}
}

func TestListTopicsWithCacheDoesNotCacheIncomplete(t *testing.T) {
	restore := resetTopicsCache(t)
	defer restore()
	full := make([]string, 100)
	for i := range full {
		full[i] = "t-" + string(rune('A'+i%26)) + string(rune('a'+i/26))
	}
	fake := &fakeTopicsDescriber{
		pages: []*cls.DescribeTopicsResponse{topicPage(full...)},
		errs:  []error{nil, errors.New("RequestLimitExceeded")},
	}
	p := newCacheTestProvider()
	ids, _ := p.listTopicsWithCache(context.Background(), "ap-guangzhou", fake)
	if len(ids) != 100 {
		t.Fatalf("降级返回应为 100 条,得到 %d", len(ids))
	}
	topicsCacheInst.mu.Lock()
	_, polluted := topicsCacheInst.entries[topicsCacheKey(1, "ap-guangzhou")]
	topicsCacheInst.mu.Unlock()
	if polluted {
		t.Fatal("分页中途失败的部分清单不得写入缓存(issue #865)")
	}
}

func TestListTopicsWithCacheCachesComplete(t *testing.T) {
	restore := resetTopicsCache(t)
	defer restore()
	fake := &fakeTopicsDescriber{pages: []*cls.DescribeTopicsResponse{
		topicPage("x1", "x2", "y1"),
	}}
	p := newCacheTestProvider()
	ids, _ := p.listTopicsWithCache(context.Background(), "ap-shanghai", fake)
	if len(ids) != 3 {
		t.Fatalf("完整枚举应返回 3 条,得到 %d", len(ids))
	}
	topicsCacheInst.mu.Lock()
	e, ok := topicsCacheInst.entries[topicsCacheKey(1, "ap-shanghai")]
	topicsCacheInst.mu.Unlock()
	if !ok {
		t.Fatal("完整清单应写入缓存")
	}
	if len(e.topics) != 3 {
		t.Fatalf("缓存里应为 3 条,得到 %d", len(e.topics))
	}
}

// 同一进程两个腾讯账号共用同一个 region 时,清单缓存必须按账号隔离
// (OpenCodeReview 对 5a531ca 的意见:缓存 key 曾只有 region)。
func TestListTopicsWithCacheIsolatedPerAccount(t *testing.T) {
	restore := resetTopicsCache(t)
	defer restore()
	ctx := context.Background()
	region := "ap-guangzhou"

	acctA := newCacheTestProvider()
	fakeA := &fakeTopicsDescriber{pages: []*cls.DescribeTopicsResponse{topicPage("A1", "A2")}}
	if got, _ := acctA.listTopicsWithCache(ctx, region, fakeA); len(got) != 2 {
		t.Fatalf("账号 A 应拿到 2 条,得到 %d", len(got))
	}

	acctB := newCacheTestProvider()
	acctB.account = &domain.CloudAccount{ID: 2, Name: "b", AccessKeyID: "k2", AccessKeySecret: "s2"}
	fakeB := &fakeTopicsDescriber{pages: []*cls.DescribeTopicsResponse{topicPage("B1")}}
	got, _ := acctB.listTopicsWithCache(ctx, region, fakeB)
	if len(got) != 1 || got[0] != "B1" {
		t.Fatalf("账号 B 必须拿自己的清单(1 条 B1),不能命中账号 A 的缓存,得到 %v", got)
	}
	if fakeB.calls != 1 {
		t.Fatalf("账号 B 不应命中账号 A 的缓存,应真实调用 1 次,得到 %d", fakeB.calls)
	}
}
