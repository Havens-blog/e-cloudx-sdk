package aliyun

import (
	"errors"
	"testing"
)

// fakeStoreLister 模拟 SLS 分页：total 个 store，每次最多返回 pageSize 个。
type fakeStoreLister struct {
	total    int
	pageSize int
	calls    []int
	err      error
}

func (f *fakeStoreLister) ListLogStoreV2(project string, offset, size int, telemetryType string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.calls = append(f.calls, offset)
	if offset >= f.total {
		return nil, nil
	}
	end := offset + f.pageSize
	if end > f.total {
		end = f.total
	}
	out := make([]string, 0, end-offset)
	for i := offset; i < end; i++ {
		out = append(out, "store")
	}
	return out, nil
}

// 超过服务端单页上限时必须继续翻页，不能只拿第一页。
func TestListAllLogStoresPaginatesBeyondFirstPage(t *testing.T) {
	f := &fakeStoreLister{total: 2593, pageSize: logStorePageSize}
	got, err := listAllLogStores(f, "jlc-prod-log")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2593 {
		t.Fatalf("期望 2593 个 store（分页合并），实际 %d", len(got))
	}
	want := []int{0, 500, 1000, 1500, 2000, 2500}
	if len(f.calls) != len(want) {
		t.Fatalf("分页调用次数 期望 %d，实际 %d (%v)", len(want), len(f.calls), f.calls)
	}
	for i := range want {
		if f.calls[i] != want[i] {
			t.Fatalf("第 %d 次分页 offset 期望 %d，实际 %d", i, want[i], f.calls[i])
		}
	}
}

// 恰好整页时不能提前结束：500 条之后还要再请求一次空页。
func TestListAllLogStoresExactMultiple(t *testing.T) {
	f := &fakeStoreLister{total: 1000, pageSize: logStorePageSize}
	got, err := listAllLogStores(f, "p")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1000 {
		t.Fatalf("期望 1000，实际 %d", len(got))
	}
	if len(f.calls) != 3 {
		t.Fatalf("恰好整页时应多请求一次取值结束，期望 3 次调用，实际 %d (%v)", len(f.calls), f.calls)
	}
}

// 单页以内一次调用即可，不产生多余请求。
func TestListAllLogStoresSinglePage(t *testing.T) {
	f := &fakeStoreLister{total: 102, pageSize: logStorePageSize}
	got, err := listAllLogStores(f, "p")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 102 || len(f.calls) != 1 {
		t.Fatalf("期望 102 个 / 1 次调用，实际 %d 个 / %d 次", len(got), len(f.calls))
	}
}

// 分页中途失败必须上抛，不能返回半截清单（否则调用方会把不完整清单当完整）。
func TestListAllLogStoresPropagatesError(t *testing.T) {
	f := &fakeStoreLister{total: 2593, pageSize: logStorePageSize, err: errors.New("throttled")}
	if _, err := listAllLogStores(f, "p"); err == nil {
		t.Fatal("分页失败必须返回错误")
	}
}

// 触达页数上限时报错而不是静默返回已取部分。
func TestListAllLogStoresPageLimit(t *testing.T) {
	f := &fakeStoreLister{total: (logStorePageLimit + 2) * logStorePageSize, pageSize: logStorePageSize}
	if _, err := listAllLogStores(f, "p"); err == nil {
		t.Fatal("超过页数上限必须报错")
	}
}
