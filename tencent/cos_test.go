package tencent

import (
	"testing"
	"time"

	"github.com/gotomicro/ego/core/elog"
	cos "github.com/tencentyun/cos-go-sdk-v5"
)

// newCOSTestAdapter 构造仅用于纯函数单测的适配器(不发起真实请求)。
func newCOSTestAdapter() *COSAdapter {
	return NewCOSAdapter("ak", "sk", "ap-guangzhou", elog.DefaultLogger)
}

// TestCOSConvertToOSSBucketNotFabricateStorageClass 审计 P1-6 回归:
// 列表转换不得再硬编码 "STANDARD"(COS 无桶级默认存储类型概念),置空由前端显示 '-'。
func TestCOSConvertToOSSBucketNotFabricateStorageClass(t *testing.T) {
	a := newCOSTestAdapter()

	got := a.convertToOSSBucket(&cos.Bucket{
		Name:         "demo-1250000000",
		Region:       "ap-guangzhou",
		CreationDate: "2024-01-02T03:04:05Z",
	})

	if got.StorageClass != "" {
		t.Fatalf("StorageClass = %q, want 空串(不得硬编码 STANDARD)", got.StorageClass)
	}
	if got.Region != "ap-guangzhou" || got.Location != "ap-guangzhou" {
		t.Fatalf("Region/Location = %q/%q, want ap-guangzhou", got.Region, got.Location)
	}
	if got.BucketName != "demo-1250000000" {
		t.Fatalf("BucketName = %q, want demo-1250000000", got.BucketName)
	}
	want := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	if !got.CreationTime.Equal(want) {
		t.Fatalf("CreationTime = %v, want %v", got.CreationTime, want)
	}
	if got.ExtranetEndpoint != "demo-1250000000.cos.ap-guangzhou.myqcloud.com" {
		t.Fatalf("ExtranetEndpoint = %q", got.ExtranetEndpoint)
	}
}

// TestCOSMapTags 断言 GetTagging 结果规整为统一标签 map(列表与详情共用):
// 空 Key 跳过、无标签返回 nil(即不填)。
func TestCOSMapTags(t *testing.T) {
	a := newCOSTestAdapter()
	if got := a.mapCOSTags(nil); got != nil {
		t.Fatalf("mapCOSTags(nil) = %v, want nil", got)
	}

	got := a.mapCOSTags([]cos.BucketTaggingTag{{Key: "env", Value: "prod"}, {Key: "", Value: "skip"}})
	if len(got) != 1 || got["env"] != "prod" {
		t.Fatalf("mapCOSTags = %v, want {env:prod}", got)
	}
}
