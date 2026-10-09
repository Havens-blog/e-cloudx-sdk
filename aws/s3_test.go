package aws

import (
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/gotomicro/ego/core/elog"
)

// newS3TestAdapter 构造仅用于纯函数单测的适配器(不发起真实请求)。
func newS3TestAdapter() *S3Adapter {
	return NewS3Adapter("ak", "sk", "us-west-1", elog.DefaultLogger)
}

// TestS3ConvertToOSSBucketNotFabricateStorageClass 审计 P1-6 回归:
// 列表转换不得再硬编码 "STANDARD",拿不到真实桶级存储类型时置空(前端显示 '-')。
func TestS3ConvertToOSSBucketNotFabricateStorageClass(t *testing.T) {
	a := newS3TestAdapter()
	created := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)

	got := a.convertToOSSBucket(&s3types.Bucket{
		Name:         aws.String("demo-bucket"),
		CreationDate: &created,
	})

	if got.StorageClass != "" {
		t.Fatalf("StorageClass = %q, want 空串(不得硬编码 STANDARD)", got.StorageClass)
	}
	if got.BucketName != "demo-bucket" {
		t.Fatalf("BucketName = %q, want demo-bucket", got.BucketName)
	}
	if got.Provider != string(domain.CloudProviderAWS) {
		t.Fatalf("Provider = %q, want %q", got.Provider, string(domain.CloudProviderAWS))
	}
	if !got.CreationTime.Equal(created) {
		t.Fatalf("CreationTime = %v, want %v", got.CreationTime, created)
	}
	// 转换函数不得伪造地域:Region/Location 由 ListBuckets/GetBucket 的
	// GetBucketLocation N+1 回填(列表与详情口径一致)。
	if got.Region != "" || got.Location != "" {
		t.Fatalf("convert Region/Location = %q/%q, want 空(由 N+1 回填)", got.Region, got.Location)
	}
}

// TestS3LocationConstraintToRegion 断言 LocationConstraint 空值(us-east-1 语义)
// 与其余地域值的映射,列表与详情共用该函数保证 Region 口径一致。
func TestS3LocationConstraintToRegion(t *testing.T) {
	a := newS3TestAdapter()
	cases := []struct {
		in   s3types.BucketLocationConstraint
		want string
	}{
		{"", "us-east-1"},          // 响应体为空(SDK 解析为零值,即 nil 语义)= us-east-1
		{"us-east-1", "us-east-1"}, // 显式返回的 us-east-1 原样透传
		{"cn-north-1", "cn-north-1"},
		{"ap-southeast-1", "ap-southeast-1"},
	}
	for _, c := range cases {
		if got := a.locationConstraintToRegion(c.in); got != c.want {
			t.Errorf("locationConstraintToRegion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestS3MapTags 断言 GetBucketTagging 结果规整为统一标签 map(列表与详情共用):
// 空 Key 跳过、nil Value 记为空串、无标签返回 nil(即不填)。
func TestS3MapTags(t *testing.T) {
	a := newS3TestAdapter()
	if got := a.mapS3Tags(nil); got != nil {
		t.Fatalf("mapS3Tags(nil) = %v, want nil", got)
	}

	got := a.mapS3Tags([]s3types.Tag{
		{Key: aws.String("env"), Value: aws.String("prod")},
		{Key: aws.String(""), Value: aws.String("skip")}, // 空 Key 跳过
		{Key: aws.String("nullable"), Value: nil},        // 空 Value 记 ""
	})

	if len(got) != 2 {
		t.Fatalf("len(tags) = %d, want 2", len(got))
	}
	if got["env"] != "prod" {
		t.Errorf("tags[env] = %q, want prod", got["env"])
	}
	if v, ok := got["nullable"]; !ok || v != "" {
		t.Errorf("tags[nullable] = %q(ok=%v), want 空串", v, ok)
	}
	if _, ok := got[""]; ok {
		t.Errorf("空 Key 不应出现在结果 map 中")
	}
}
