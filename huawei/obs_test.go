package huawei

import (
	"testing"
	"time"

	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-obs/obs"
)

// newOBSTestAdapter 构造仅用于纯函数单测的适配器(不发起真实请求)。
func newOBSTestAdapter() *OBSAdapter {
	return NewOBSAdapter("ak", "sk", "cn-north-4", elog.DefaultLogger)
}

// TestOBSConvertToOSSBucketNotFabricateFields 断言列表转换不伪造存储类型与标签:
// ListBuckets 响应不含这两个字段,由 ListBuckets/GetBucket 的 N+1
// (GetBucketMetadata/GetBucketTagging,与详情同源)回填,口径一致。
func TestOBSConvertToOSSBucketNotFabricateFields(t *testing.T) {
	a := newOBSTestAdapter()
	created := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)

	got := a.convertToOSSBucket(&obs.Bucket{
		Name:         "demo",
		Location:     "cn-north-4",
		CreationDate: created,
	})

	if got.StorageClass != "" {
		t.Fatalf("StorageClass = %q, want 空串(转换函数不得伪造)", got.StorageClass)
	}
	if got.Tags != nil {
		t.Fatalf("Tags = %v, want nil(转换函数不得伪造)", got.Tags)
	}
	if got.Region != "cn-north-4" || got.Location != "cn-north-4" {
		t.Fatalf("Region/Location = %q/%q, want cn-north-4", got.Region, got.Location)
	}
	if got.ExtranetEndpoint != "demo.obs.cn-north-4.myhuaweicloud.com" {
		t.Fatalf("ExtranetEndpoint = %q", got.ExtranetEndpoint)
	}
	if got.IntranetEndpoint != "demo.obs.cn-north-4.myhuaweicloud.com" {
		t.Fatalf("IntranetEndpoint = %q", got.IntranetEndpoint)
	}
	if !got.CreationTime.Equal(created) {
		t.Fatalf("CreationTime = %v, want %v", got.CreationTime, created)
	}
}

// TestOBSConvertToOSSBucketEmptyLocation 无 Location 时不组装 endpoint。
func TestOBSConvertToOSSBucketEmptyLocation(t *testing.T) {
	a := newOBSTestAdapter()
	got := a.convertToOSSBucket(&obs.Bucket{Name: "demo"})
	if got.ExtranetEndpoint != "" || got.IntranetEndpoint != "" {
		t.Fatalf("endpoint = %q/%q, want 空", got.ExtranetEndpoint, got.IntranetEndpoint)
	}
	if got.StorageClass != "" {
		t.Fatalf("StorageClass = %q, want 空串", got.StorageClass)
	}
}

// TestOBSMapTags 断言 GetBucketTagging 结果规整为统一标签 map
// (列表与详情共用):空 Key 跳过、无标签返回 nil(即不填)。
func TestOBSMapTags(t *testing.T) {
	if got := mapOBSTags(nil); got != nil {
		t.Fatalf("mapOBSTags(nil) = %v, want nil", got)
	}

	got := mapOBSTags([]obs.Tag{{Key: "env", Value: "prod"}, {Key: "", Value: "skip"}})
	if len(got) != 1 || got["env"] != "prod" {
		t.Fatalf("mapOBSTags = %v, want {env:prod}", got)
	}
}
