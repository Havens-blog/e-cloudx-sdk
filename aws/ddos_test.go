package aws

import (
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aws/aws-sdk-go-v2/aws"
	shieldtypes "github.com/aws/aws-sdk-go-v2/service/shield/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/stretchr/testify/assert"
)

// TestNewDDOSAdapter 锁定构造器签名与字段装载(构造器改动会直接编译失败,此处兜底校验)。
func TestNewDDOSAdapter(t *testing.T) {
	a := NewDDOSAdapter("test-ak", "test-sk", "cn-north-1", elog.DefaultLogger)
	assert.Equal(t, "test-ak", a.accessKeyID)
	assert.Equal(t, "test-sk", a.accessKeySecret)
	assert.Equal(t, "cn-north-1", a.defaultRegion)
	assert.NotNil(t, a.logger)
}

// TestConvertSubscriptionToDDOS 基于 SDK 实际字段(shield v1.43.3)验证订阅转换:
// 该版本不存在 "DURATION"/"DURATIONLESS" 字符串枚举,
// TimeCommitmentInSeconds(int64 秒)三分支:>0=Advanced、==0=Standard、<0=原值透出。
func TestConvertSubscriptionToDDOS(t *testing.T) {
	start := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	end := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	tests := []struct {
		name string
		sub  *shieldtypes.Subscription
		want types.DDOSInstance
	}{
		{
			name: "有固定时长承诺=DURATION语义→Advanced",
			sub: &shieldtypes.Subscription{
				TimeCommitmentInSeconds: 31536000,
				AutoRenew:               shieldtypes.AutoRenewEnabled,
				StartTime:               &start,
				EndTime:                 &end,
			},
			want: types.DDOSInstance{
				InstanceID:   shieldSubscriptionInstanceID,
				InstanceName: "AWS Shield Advanced",
				Edition:      "Advanced",
				Region:       shieldGlobalRegion,
				Provider:     "aws",
				AutoRenew:    true,
				CreationTime: "2024-01-02 03:04:05",
				ExpiredTime:  "2025-01-02 03:04:05",
			},
		},
		{
			name: "无时长承诺=DURATIONLESS语义→Standard",
			sub: &shieldtypes.Subscription{
				TimeCommitmentInSeconds: 0,
				AutoRenew:               shieldtypes.AutoRenewDisabled,
				StartTime:               &start,
				EndTime:                 &end,
			},
			want: types.DDOSInstance{
				InstanceID:   shieldSubscriptionInstanceID,
				InstanceName: "AWS Shield Standard",
				Edition:      "Standard",
				Region:       shieldGlobalRegion,
				Provider:     "aws",
				AutoRenew:    false,
				CreationTime: "2024-01-02 03:04:05",
				ExpiredTime:  "2025-01-02 03:04:05",
			},
		},
		{
			name: "未知值原值透出不臆断",
			sub: &shieldtypes.Subscription{
				TimeCommitmentInSeconds: -1,
				AutoRenew:               shieldtypes.AutoRenew("UNKNOWN"),
			},
			want: types.DDOSInstance{
				InstanceID:   shieldSubscriptionInstanceID,
				InstanceName: "AWS Shield",
				Edition:      "-1",
				Region:       shieldGlobalRegion,
				Provider:     "aws",
				AutoRenew:    false,
				// StartTime/EndTime 为 nil → 时间字段空串
			},
		},
		{
			name: "nil时间安全",
			sub: &shieldtypes.Subscription{
				TimeCommitmentInSeconds: 0,
				AutoRenew:               shieldtypes.AutoRenewEnabled,
			},
			want: types.DDOSInstance{
				InstanceID:   shieldSubscriptionInstanceID,
				InstanceName: "AWS Shield Standard",
				Edition:      "Standard",
				Region:       shieldGlobalRegion,
				Provider:     "aws",
				AutoRenew:    true,
				CreationTime: "",
				ExpiredTime:  "",
			},
		},
		{
			name: "AutoRenew零值空串→false",
			sub: &shieldtypes.Subscription{
				TimeCommitmentInSeconds: 31536000,
			},
			want: types.DDOSInstance{
				InstanceID:   shieldSubscriptionInstanceID,
				InstanceName: "AWS Shield Advanced",
				Edition:      "Advanced",
				Region:       shieldGlobalRegion,
				Provider:     "aws",
				AutoRenew:    false,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, convertSubscriptionToDDOS(tt.sub))
		})
	}
}

// TestConvertSubscriptionToDDOSNil nil 订阅防御性返回零值实例。
func TestConvertSubscriptionToDDOSNil(t *testing.T) {
	assert.Equal(t, types.DDOSInstance{}, convertSubscriptionToDDOS(nil))
}

// TestDDOSMergeProtections 收集非空 ResourceArn(去重、保序)写入 ProtectedIPs;
// 字段语义:此处存“受防护资源 ARN 列表”,非 IP。
func TestDDOSMergeProtections(t *testing.T) {
	inst := &types.DDOSInstance{InstanceID: shieldSubscriptionInstanceID}
	protections := []shieldtypes.Protection{
		{Id: aws.String("p-1"), ResourceArn: aws.String("arn:aws:ec2:us-east-1:123456789012:eip-allocation/eipalloc-a")},
		{Id: aws.String("p-2"), ResourceArn: aws.String("")}, // 空串 ARN 跳过
		{Id: aws.String("p-3")}, // nil ARN 跳过
		{Id: aws.String("p-4"), ResourceArn: aws.String("arn:aws:ec2:us-east-1:123456789012:eip-allocation/eipalloc-a")}, // 重复去重
		{Id: aws.String("p-5"), ResourceArn: aws.String("arn:aws:cloudfront::123456789012:distribution/ABC123")},
	}

	mergeProtections(inst, protections)

	assert.Equal(t, []string{
		"arn:aws:ec2:us-east-1:123456789012:eip-allocation/eipalloc-a",
		"arn:aws:cloudfront::123456789012:distribution/ABC123",
	}, inst.ProtectedIPs)
	assert.Equal(t, 2, inst.ProtectedIPCount)
	assert.Equal(t, "受防护资源 2 个", inst.Description)
	assert.Equal(t, shieldSubscriptionInstanceID, inst.InstanceID) // 其余字段不被覆盖
}

// TestDDOSMergeProtectionsEmpty 空列表与 nil 实例安全。
func TestDDOSMergeProtectionsEmpty(t *testing.T) {
	inst := &types.DDOSInstance{}
	mergeProtections(inst, nil)
	assert.Equal(t, 0, inst.ProtectedIPCount)
	assert.Equal(t, "受防护资源 0 个", inst.Description)
	assert.Len(t, inst.ProtectedIPs, 0)

	// nil 实例防御性安全(不应 panic)
	mergeProtections(nil, []shieldtypes.Protection{{ResourceArn: aws.String("arn:x")}})
}

// TestFilterDDOSInstances 内存过滤:名称包含匹配(大小写不敏感)、Status 精确;
// Region/分页对 Shield 全局订阅无意义被忽略;DDOSInstanceFilter 无 Edition 维度。
func TestFilterDDOSInstances(t *testing.T) {
	instances := []types.DDOSInstance{
		{InstanceID: "shield-subscription", InstanceName: "AWS Shield Advanced", Edition: "Advanced", Status: ""},
	}

	tests := []struct {
		name   string
		filter *types.DDOSInstanceFilter
		wantN  int
	}{
		{"nil过滤全量", nil, 1},
		{"名称包含匹配大小写不敏感", &types.DDOSInstanceFilter{InstanceName: "SHIELD ADVANCED"}, 1},
		{"名称不匹配", &types.DDOSInstanceFilter{InstanceName: "Standard"}, 0},
		{"状态过滤-订阅无状态故不匹配", &types.DDOSInstanceFilter{Status: "active"}, 0},
		{"Region与分页在Shield下被忽略", &types.DDOSInstanceFilter{Region: "us-west-2", PageNumber: 2, PageSize: 99}, 1},
		{"名称+状态AND", &types.DDOSInstanceFilter{InstanceName: "shield", Status: "active"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterDDOSInstances(instances, tt.filter)
			assert.Len(t, got, tt.wantN)
		})
	}
}
