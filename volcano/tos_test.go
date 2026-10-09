package volcano

import (
	"testing"
	"time"

	"github.com/gotomicro/ego/core/elog"
	"github.com/volcengine/ve-tos-golang-sdk/v2/tos"
)

// newTOSTestAdapter 构造仅用于纯函数单测的适配器(不发起真实请求)。
func newTOSTestAdapter() *TOSAdapter {
	return NewTOSAdapter("ak", "sk", "cn-beijing", elog.DefaultLogger)
}

// TestTOSConvertToOSSBucketNotFabricateFields 断言列表转换不伪造存储类型与标签:
// ListedBucket 不含这两个字段,由 ListBuckets/GetBucket 的 N+1
// (HeadBucket 取 StorageClass、GetBucketTagging 取标签,与详情同源)回填,口径一致。
func TestTOSConvertToOSSBucketNotFabricateFields(t *testing.T) {
	a := newTOSTestAdapter()

	got := a.convertToOSSBucket(tos.ListedBucket{
		Name:         "demo",
		Location:     "cn-beijing",
		CreationDate: "2024-01-02T03:04:05Z",
	})

	if got.StorageClass != "" {
		t.Fatalf("StorageClass = %q, want 空串(转换函数不得伪造)", got.StorageClass)
	}
	if got.Tags != nil {
		t.Fatalf("Tags = %v, want nil(转换函数不得伪造)", got.Tags)
	}
	if got.Region != "cn-beijing" || got.Location != "cn-beijing" {
		t.Fatalf("Region/Location = %q/%q, want cn-beijing", got.Region, got.Location)
	}
	want := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	if !got.CreationTime.Equal(want) {
		t.Fatalf("CreationTime = %v, want %v", got.CreationTime, want)
	}
	if got.ExtranetEndpoint != "demo.tos-cn-beijing.volces.com" {
		t.Fatalf("ExtranetEndpoint = %q", got.ExtranetEndpoint)
	}
	if got.IntranetEndpoint != "demo.tos-cn-beijing.ivolces.com" {
		t.Fatalf("IntranetEndpoint = %q", got.IntranetEndpoint)
	}
}

// TestTOSConvertDefaultRegionFallback Location 缺失时 Region 回落 defaultRegion,
// Location 保持 SDK 原样(空),不伪造。
func TestTOSConvertDefaultRegionFallback(t *testing.T) {
	a := newTOSTestAdapter()
	got := a.convertToOSSBucket(tos.ListedBucket{Name: "demo"})
	if got.Region != "cn-beijing" {
		t.Fatalf("Region = %q, want 回落 defaultRegion cn-beijing", got.Region)
	}
	if got.Location != "" {
		t.Fatalf("Location = %q, want 空(保持 SDK 原样)", got.Location)
	}
	if got.StorageClass != "" {
		t.Fatalf("StorageClass = %q, want 空串", got.StorageClass)
	}
}

// TestTOSTOSCreationDateFormats 兼容 RFC3339 带时区/UTC 两种创建时间格式。
func TestTOSTOSCreationDateFormats(t *testing.T) {
	a := newTOSTestAdapter()
	cases := []struct {
		raw  string
		want time.Time
	}{
		{"2024-01-02T03:04:05Z", time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)},
		{"2024-01-02T11:04:05+08:00", time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)},
	}
	for _, c := range cases {
		got := a.convertToOSSBucket(tos.ListedBucket{Name: "demo", CreationDate: c.raw})
		if !got.CreationTime.Equal(c.want) {
			t.Fatalf("CreationDate %q => %v, want %v", c.raw, got.CreationTime, c.want)
		}
	}
}

// TestTOSMapTags 断言 GetBucketTagging 结果规整为统一标签 map(列表与详情共用):
// 空 Key 跳过、无标签返回 nil(即不填)。
func TestTOSMapTags(t *testing.T) {
	if got := mapTOSTags(nil); got != nil {
		t.Fatalf("mapTOSTags(nil) = %v, want nil", got)
	}

	got := mapTOSTags([]tos.Tag{{Key: "env", Value: "prod"}, {Key: "", Value: "skip"}})
	if len(got) != 1 || got["env"] != "prod" {
		t.Fatalf("mapTOSTags = %v, want {env:prod}", got)
	}
}
