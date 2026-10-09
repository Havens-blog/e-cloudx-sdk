package aliyun

import (
	"testing"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"github.com/gotomicro/ego/core/elog"
)

// newOSSTestAdapter 构造仅用于纯函数单测的适配器(不发起真实请求)。
func newOSSTestAdapter() *OSSAdapter {
	return NewOSSAdapter("ak", "sk", "cn-hangzhou", elog.DefaultLogger)
}

// TestOSSConvertToOSSBucketStorageClassPassthrough 断言列表 StorageClass
// 直接透传 SDK 真实值(阿里云 ListBuckets 响应携带桶存储类型),不硬编码默认值。
func TestOSSConvertToOSSBucketStorageClassPassthrough(t *testing.T) {
	a := newOSSTestAdapter()
	created := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)

	got := a.convertToOSSBucket(oss.BucketProperties{
		Name:         "demo",
		Location:     "oss-cn-hangzhou",
		CreationDate: created,
		StorageClass: "IA",
	})

	if got.StorageClass != "IA" {
		t.Fatalf("StorageClass = %q, want IA(真实值透传)", got.StorageClass)
	}
	if got.Region != "cn-hangzhou" {
		t.Fatalf("Region = %q, want cn-hangzhou(剥离 oss- 前缀)", got.Region)
	}
	if got.Location != "oss-cn-hangzhou" {
		t.Fatalf("Location = %q, want oss-cn-hangzhou", got.Location)
	}
	if got.BucketName != "demo" || got.Provider != "aliyun" {
		t.Fatalf("BucketName/Provider = %q/%q, want demo/aliyun", got.BucketName, got.Provider)
	}
	if !got.CreationTime.Equal(created) {
		t.Fatalf("CreationTime = %v, want %v", got.CreationTime, created)
	}
	if got.ExtranetEndpoint != "demo.oss-cn-hangzhou.aliyuncs.com" {
		t.Fatalf("ExtranetEndpoint = %q", got.ExtranetEndpoint)
	}
}

// TestOSSConvertToOSSBucketEmptyStorageClass 响应缺失 StorageClass 时保持空
// (前端显示 '-'),不得补默认值(审计 P1-6:错误值比空白更危险)。
func TestOSSConvertToOSSBucketEmptyStorageClass(t *testing.T) {
	a := newOSSTestAdapter()
	got := a.convertToOSSBucket(oss.BucketProperties{Name: "demo", Location: "cn-beijing"})
	if got.StorageClass != "" {
		t.Fatalf("StorageClass = %q, want 空串", got.StorageClass)
	}
	if got.Region != "cn-beijing" {
		t.Fatalf("Region = %q, want cn-beijing(无 oss- 前缀原样透传)", got.Region)
	}
}

// TestOSSMapAliyunTags 断言 GetBucketTagging 结果规整为统一标签 map
// (列表与详情共用):空 Key 跳过、无标签返回 nil(即不填)。
func TestOSSMapAliyunTags(t *testing.T) {
	a := newOSSTestAdapter()
	if got := a.mapAliyunTags(nil); got != nil {
		t.Fatalf("mapAliyunTags(nil) = %v, want nil", got)
	}

	got := a.mapAliyunTags([]oss.Tag{{Key: "env", Value: "prod"}, {Key: "", Value: "skip"}})
	if len(got) != 1 || got["env"] != "prod" {
		t.Fatalf("mapAliyunTags = %v, want {env:prod}", got)
	}
}
