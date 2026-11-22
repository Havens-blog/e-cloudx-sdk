package aliyun

import (
	"errors"
	"testing"

	sls "github.com/aliyun/aliyun-log-go-sdk"
)

// fakeProjectLister 模拟 SLS project 分页：total 个 project，
// 服务端单页实际返回 serverPageSize 个（可能小于请求的 projectPageSize）。
type fakeProjectLister struct {
	total          int
	serverPageSize int
	calls          []int
	err            error
	reportedTotals []int
}

func (f *fakeProjectLister) ListProjectV2(offset, size int) ([]sls.LogProject, int, int, error) {
	if f.err != nil {
		return nil, 0, 0, f.err
	}
	f.calls = append(f.calls, offset)
	f.reportedTotals = append(f.reportedTotals, f.total)
	if offset >= f.total {
		return nil, 0, f.total, nil
	}
	end := offset + f.serverPageSize
	if end > f.total {
		end = f.total
	}
	out := make([]sls.LogProject, 0, end-offset)
	for i := offset; i < end; i++ {
		out = append(out, sls.LogProject{Name: "proj"})
	}
	return out, end - offset, f.total, nil
}

// 超过服务端默认单页时必须翻页取满 total，不能只拿第一页。
func TestListAllProjectNamesPaginatesToTotal(t *testing.T) {
	f := &fakeProjectLister{total: 1240, serverPageSize: projectPageSize}
	got, err := listAllProjectNames(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1240 {
		t.Fatalf("期望 1240 个 project（分页合并），实际 %d", len(got))
	}
	want := []int{0, 500, 1000}
	if len(f.calls) != len(want) {
		t.Fatalf("分页调用次数 期望 %d，实际 %d (%v)", len(want), len(f.calls), f.calls)
	}
	for i := range want {
		if f.calls[i] != want[i] {
			t.Fatalf("第 %d 次分页 offset 期望 %d，实际 %d", i, want[i], f.calls[i])
		}
	}
}

// 服务端单页被压到比请求的 pageSize 更小时，total 驱动的循环仍能取满。
func TestListAllProjectNamesSurvivesServerPageCap(t *testing.T) {
	f := &fakeProjectLister{total: 240, serverPageSize: 100}
	got, err := listAllProjectNames(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 240 {
		t.Fatalf("期望 240（服务端单页 100 < 请求 500 时仍取满），实际 %d", len(got))
	}
	if len(f.calls) != 3 {
		t.Fatalf("期望 3 次分页（100+100+40），实际 %d (%v)", len(f.calls), f.calls)
	}
}

// 恰好整页：total 驱动判定，不需要多请求一次空页。
func TestListAllProjectNamesExactMultiple(t *testing.T) {
	f := &fakeProjectLister{total: 1000, serverPageSize: projectPageSize}
	got, err := listAllProjectNames(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1000 || len(f.calls) != 2 {
		t.Fatalf("期望 1000 个 / 2 次调用，实际 %d 个 / %d 次 (%v)", len(got), len(f.calls), f.calls)
	}
}

// 单页以内一次调用即可。
func TestListAllProjectNamesSinglePage(t *testing.T) {
	f := &fakeProjectLister{total: 24, serverPageSize: projectPageSize}
	got, err := listAllProjectNames(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 24 || len(f.calls) != 1 {
		t.Fatalf("期望 24 个 / 1 次调用，实际 %d 个 / %d 次", len(got), len(f.calls))
	}
}

// 分页中途失败必须上抛，不能返回半截清单。
func TestListAllProjectNamesPropagatesError(t *testing.T) {
	f := &fakeProjectLister{total: 1240, serverPageSize: projectPageSize, err: errors.New("throttled")}
	if _, err := listAllProjectNames(f); err == nil {
		t.Fatal("分页失败必须返回错误")
	}
}

// 触达页数上限且未取满 total 时报错，而不是静默返回已取部分。
func TestListAllProjectNamesPageLimit(t *testing.T) {
	f := &fakeProjectLister{total: (projectPageLimit + 2) * projectPageSize, serverPageSize: projectPageSize}
	if _, err := listAllProjectNames(f); err == nil {
		t.Fatal("超过页数上限且未取满 total 必须报错")
	}
}
