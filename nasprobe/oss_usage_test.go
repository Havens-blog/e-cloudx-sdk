package nasprobe

import (
	"math"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
)

// TestOSSBucketsToUsage 单厂商 OSS bucket 列表聚合为分布统计行。
// 规格:proposal「必达厂商选择依据」——实盘 OSS bucket 数/容量按厂商占比,
// bucket 数与容量为双口径;StorageSize 为字节,聚合边界换算 GB(Hard Rule)。
func TestOSSBucketsToUsage(t *testing.T) {
	tests := []struct {
		name        string
		provider    string
		buckets     []types.OSSBucket
		wantInst    int
		wantCapGB   float64
		wantZeroCap int
	}{
		{
			name:     "空列表_零值行",
			provider: "tencent",
			buckets:  nil,
			wantInst: 0, wantCapGB: 0, wantZeroCap: 0,
		},
		{
			name:     "单bucket_字节换算GB",
			provider: "aliyun",
			buckets: []types.OSSBucket{
				{BucketName: "b1", StorageSize: 2 * 1024 * 1024 * 1024},
			},
			wantInst: 1, wantCapGB: 2, wantZeroCap: 0,
		},
		{
			name:     "多bucket_含零值_求和",
			provider: "huawei",
			buckets: []types.OSSBucket{
				{BucketName: "b1", StorageSize: 1 * 1024 * 1024 * 1024},
				{BucketName: "b2", StorageSize: 0},
				{BucketName: "b3", StorageSize: 512 * 1024 * 1024},
			},
			wantInst: 3, wantCapGB: 1.5, wantZeroCap: 1,
		},
		{
			name:     "全零值_bucket数仍计数",
			provider: "aws",
			buckets: []types.OSSBucket{
				{BucketName: "idle-1"}, {BucketName: "idle-2"},
			},
			wantInst: 2, wantCapGB: 0, wantZeroCap: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := OSSBucketsToUsage(tt.provider, tt.buckets)
			if got.Provider != tt.provider {
				t.Errorf("Provider = %q, want %q", got.Provider, tt.provider)
			}
			if got.Instances != tt.wantInst {
				t.Errorf("Instances(bucket数) = %d, want %d", got.Instances, tt.wantInst)
			}
			if math.Abs(got.CapacityGB-tt.wantCapGB) > 1e-9 {
				t.Errorf("CapacityGB = %v, want %v", got.CapacityGB, tt.wantCapGB)
			}
			if got.ZeroCap != tt.wantZeroCap {
				t.Errorf("ZeroCap = %d, want %d", got.ZeroCap, tt.wantZeroCap)
			}
		})
	}
}

// TestOSSGrowthPercent 近 30 天存储量增速计算(首末日口径)。
// 规格:proposal Urgency「近 30 天存储量增速 > X% 的高增长 bucket 数」;
// first<=0 时无法计算(闲置/新建 bucket),返回 false,禁止 NaN/Inf。
func TestOSSGrowthPercent(t *testing.T) {
	tests := []struct {
		name    string
		first   float64
		last    float64
		wantPct float64
		wantOK  bool
	}{
		{name: "正常增长", first: 100, last: 130, wantPct: 30, wantOK: true},
		{name: "下降", first: 200, last: 100, wantPct: -50, wantOK: true},
		{name: "持平", first: 100, last: 100, wantPct: 0, wantOK: true},
		{name: "首日为零_不可计算", first: 0, last: 100, wantPct: 0, wantOK: false},
		{name: "首末均零_不可计算", first: 0, last: 0, wantPct: 0, wantOK: false},
		{name: "负值_不可计算", first: -1, last: 100, wantPct: 0, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pct, ok := OSSGrowthPercent(tt.first, tt.last)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if math.IsNaN(pct) || math.IsInf(pct, 0) {
				t.Fatalf("pct = %v, 禁止 NaN/Inf", pct)
			}
			if math.Abs(pct-tt.wantPct) > 1e-9 {
				t.Errorf("pct = %v, want %v", pct, tt.wantPct)
			}
		})
	}
}

// TestIsHighGrowth 高增长判定(阈值严格大于;增速不可计算时恒为 false)。
func TestIsHighGrowth(t *testing.T) {
	if !IsHighGrowth(30.0001, true) {
		t.Errorf("30.0001%% > 30%% 应判高增长")
	}
	if IsHighGrowth(30.0, true) {
		t.Errorf("恰等于阈值(严格大于)不应判高增长")
	}
	if IsHighGrowth(-5, true) {
		t.Errorf("负增速不应判高增长")
	}
	if IsHighGrowth(100, false) {
		t.Errorf("增速不可计算(ok=false)不应判高增长")
	}
}
