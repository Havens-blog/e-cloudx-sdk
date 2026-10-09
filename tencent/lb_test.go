package tencent

import (
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/stretchr/testify/assert"
	clb "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/clb/v20180317"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
)

func newTestLBAdapter() *LBAdapter {
	return &LBAdapter{
		accessKeyID:     "test-ak",
		accessKeySecret: "test-sk",
		defaultRegion:   "ap-guangzhou",
		logger:          elog.DefaultLogger,
	}
}

func uint64Ptr(v uint64) *uint64 {
	return &v
}

func TestConvertToLBInstance(t *testing.T) {
	adapter := newTestLBAdapter()

	tests := []struct {
		name     string
		lb       *clb.LoadBalancer
		region   string
		expected types.LBInstance
	}{
		{
			name: "基本字段映射",
			lb: &clb.LoadBalancer{
				LoadBalancerId:   common.StringPtr("lb-tc-001"),
				LoadBalancerName: common.StringPtr("web-clb"),
				LoadBalancerType: common.StringPtr("OPEN"),
				Status:           uint64Ptr(1),
				LoadBalancerVips: common.StringPtrs([]string{"1.2.3.4"}),
				VpcId:            common.StringPtr("vpc-tc-001"),
				SubnetId:         common.StringPtr("subnet-001"),
				MasterZone: &clb.ZoneInfo{
					Zone: common.StringPtr("ap-guangzhou-3"),
				},
				Zones:            common.StringPtrs([]string{"ap-guangzhou-3", "ap-guangzhou-4"}),
				CreateTime:       common.StringPtr("2024-01-01 00:00:00"),
				ExpireTime:       common.StringPtr("2025-01-01 00:00:00"),
				ChargeType:       common.StringPtr("PREPAID"),
				ProjectId:        uint64Ptr(12345),
				AddressIPVersion: common.StringPtr("ipv4"),
				Tags: []*clb.TagInfo{
					{TagKey: common.StringPtr("env"), TagValue: common.StringPtr("prod")},
					{TagKey: common.StringPtr("team"), TagValue: common.StringPtr("infra")},
				},
			},
			region: "ap-guangzhou",
			expected: types.LBInstance{
				LoadBalancerID:   "lb-tc-001",
				LoadBalancerName: "web-clb",
				LoadBalancerType: "slb",
				Status:           "active",
				Region:           "ap-guangzhou",
				Zone:             "ap-guangzhou-3",
				SlaveZone:        "ap-guangzhou-4",
				Address:          "1.2.3.4",
				AddressType:      "internet",
				AddressIPVersion: "ipv4",
				VPCID:            "vpc-tc-001",
				VSwitchID:        "subnet-001",
				ChargeType:       "PREPAID",
				CreationTime:     "2024-01-01 00:00:00",
				ExpiredTime:      "2025-01-01 00:00:00",
				ProjectID:        "12345",
				Tags:             map[string]string{"env": "prod", "team": "infra"},
				Provider:         "tencent",
			},
		},
		{
			name: "Status=0为Creating",
			lb: &clb.LoadBalancer{
				LoadBalancerId: common.StringPtr("lb-creating"),
				Status:         uint64Ptr(0),
			},
			region: "ap-shanghai",
			expected: types.LBInstance{
				LoadBalancerID:   "lb-creating",
				LoadBalancerType: "slb",
				Status:           "creating",
				Region:           "ap-shanghai",
				AddressType:      "intranet",
				AddressIPVersion: "ipv4",
				Tags:             map[string]string{},
				Provider:         "tencent",
			},
		},
		{
			name: "Status=1为Active",
			lb: &clb.LoadBalancer{
				LoadBalancerId: common.StringPtr("lb-active"),
				Status:         uint64Ptr(1),
			},
			region: "ap-beijing",
			expected: types.LBInstance{
				LoadBalancerID:   "lb-active",
				LoadBalancerType: "slb",
				Status:           "active",
				Region:           "ap-beijing",
				AddressType:      "intranet",
				AddressIPVersion: "ipv4",
				Tags:             map[string]string{},
				Provider:         "tencent",
			},
		},
		{
			name: "未知Status值",
			lb: &clb.LoadBalancer{
				LoadBalancerId: common.StringPtr("lb-unknown"),
				Status:         uint64Ptr(99),
			},
			region: "ap-chengdu",
			expected: types.LBInstance{
				LoadBalancerID:   "lb-unknown",
				LoadBalancerType: "slb",
				Status:           "99",
				Region:           "ap-chengdu",
				AddressType:      "intranet",
				AddressIPVersion: "ipv4",
				Tags:             map[string]string{},
				Provider:         "tencent",
			},
		},
		{
			name: "INTERNAL类型为intranet",
			lb: &clb.LoadBalancer{
				LoadBalancerId:   common.StringPtr("lb-internal"),
				LoadBalancerType: common.StringPtr("INTERNAL"),
			},
			region: "ap-guangzhou",
			expected: types.LBInstance{
				LoadBalancerID:   "lb-internal",
				LoadBalancerType: "slb",
				AddressType:      "intranet",
				AddressIPVersion: "ipv4",
				Region:           "ap-guangzhou",
				Tags:             map[string]string{},
				Provider:         "tencent",
			},
		},
		{
			name: "OPEN类型为internet",
			lb: &clb.LoadBalancer{
				LoadBalancerId:   common.StringPtr("lb-open"),
				LoadBalancerType: common.StringPtr("OPEN"),
			},
			region: "ap-guangzhou",
			expected: types.LBInstance{
				LoadBalancerID:   "lb-open",
				LoadBalancerType: "slb",
				AddressType:      "internet",
				AddressIPVersion: "ipv4",
				Region:           "ap-guangzhou",
				Tags:             map[string]string{},
				Provider:         "tencent",
			},
		},
		{
			name:   "所有字段为nil",
			lb:     &clb.LoadBalancer{},
			region: "ap-nanjing",
			expected: types.LBInstance{
				LoadBalancerType: "slb",
				AddressType:      "intranet",
				AddressIPVersion: "ipv4",
				Region:           "ap-nanjing",
				Tags:             map[string]string{},
				Provider:         "tencent",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := adapter.convertToLBInstance(tt.lb, tt.region)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertToLBInstance_VIPExtraction(t *testing.T) {
	adapter := newTestLBAdapter()

	tests := []struct {
		name     string
		vips     []*string
		expected string
	}{
		{
			name:     "单个VIP",
			vips:     common.StringPtrs([]string{"10.0.0.1"}),
			expected: "10.0.0.1",
		},
		{
			name:     "多个VIP取第一个",
			vips:     common.StringPtrs([]string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}),
			expected: "10.0.0.1",
		},
		{
			name:     "空VIP列表",
			vips:     []*string{},
			expected: "",
		},
		{
			name:     "nil VIP列表",
			vips:     nil,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lb := &clb.LoadBalancer{
				LoadBalancerVips: tt.vips,
			}
			result := adapter.convertToLBInstance(lb, "ap-guangzhou")
			assert.Equal(t, tt.expected, result.Address)
		})
	}
}

func TestConvertToLBInstance_TagExtraction(t *testing.T) {
	adapter := newTestLBAdapter()

	tests := []struct {
		name     string
		tags     []*clb.TagInfo
		expected map[string]string
	}{
		{
			name:     "nil标签",
			tags:     nil,
			expected: map[string]string{},
		},
		{
			name:     "空标签列表",
			tags:     []*clb.TagInfo{},
			expected: map[string]string{},
		},
		{
			name: "正常标签",
			tags: []*clb.TagInfo{
				{TagKey: common.StringPtr("k1"), TagValue: common.StringPtr("v1")},
				{TagKey: common.StringPtr("k2"), TagValue: common.StringPtr("v2")},
			},
			expected: map[string]string{"k1": "v1", "k2": "v2"},
		},
		{
			name: "标签中有nil Key或Value",
			tags: []*clb.TagInfo{
				{TagKey: common.StringPtr("valid"), TagValue: common.StringPtr("tag")},
				{TagKey: nil, TagValue: common.StringPtr("no-key")},
				{TagKey: common.StringPtr("no-value"), TagValue: nil},
			},
			expected: map[string]string{"valid": "tag"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lb := &clb.LoadBalancer{
				Tags: tt.tags,
			}
			result := adapter.convertToLBInstance(lb, "ap-guangzhou")
			assert.Equal(t, tt.expected, result.Tags)
		})
	}
}

func TestConvertToLBInstance_ZoneExtraction(t *testing.T) {
	adapter := newTestLBAdapter()

	tests := []struct {
		name          string
		masterZone    *clb.ZoneInfo
		zones         []*string
		expectedZone  string
		expectedSlave string
	}{
		{
			name: "有MasterZone和Zones",
			masterZone: &clb.ZoneInfo{
				Zone: common.StringPtr("ap-guangzhou-3"),
			},
			zones:         common.StringPtrs([]string{"ap-guangzhou-3", "ap-guangzhou-4"}),
			expectedZone:  "ap-guangzhou-3",
			expectedSlave: "ap-guangzhou-4",
		},
		{
			name:          "nil MasterZone",
			masterZone:    nil,
			zones:         common.StringPtrs([]string{"ap-guangzhou-3", "ap-guangzhou-4"}),
			expectedZone:  "",
			expectedSlave: "ap-guangzhou-4",
		},
		{
			name: "MasterZone的Zone为nil",
			masterZone: &clb.ZoneInfo{
				Zone: nil,
			},
			expectedZone:  "",
			expectedSlave: "",
		},
		{
			name:          "Zones只有一个元素",
			masterZone:    nil,
			zones:         common.StringPtrs([]string{"ap-guangzhou-3"}),
			expectedZone:  "",
			expectedSlave: "",
		},
		{
			name:          "nil Zones",
			masterZone:    nil,
			zones:         nil,
			expectedZone:  "",
			expectedSlave: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lb := &clb.LoadBalancer{
				MasterZone: tt.masterZone,
				Zones:      tt.zones,
			}
			result := adapter.convertToLBInstance(lb, "ap-guangzhou")
			assert.Equal(t, tt.expectedZone, result.Zone)
			assert.Equal(t, tt.expectedSlave, result.SlaveZone)
		})
	}
}
