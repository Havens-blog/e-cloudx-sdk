package huawei

import (
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/elb/v3/model"
	"github.com/stretchr/testify/assert"
)

func newTestLBAdapter() *LBAdapter {
	return &LBAdapter{
		accessKeyID:     "test-ak",
		accessKeySecret: "test-sk",
		defaultRegion:   "cn-north-4",
		logger:          elog.DefaultLogger,
	}
}

func stringPtr(s string) *string {
	return &s
}

func TestConvertToLBInstance(t *testing.T) {
	adapter := newTestLBAdapter()

	tests := []struct {
		name     string
		lb       model.LoadBalancer
		region   string
		expected types.LBInstance
	}{
		{
			name: "基本字段映射",
			lb: model.LoadBalancer{
				Id:                   "lb-hw-001",
				Name:                 "web-elb",
				ProvisioningStatus:   "ACTIVE",
				VipAddress:           "192.168.1.100",
				VpcId:                "vpc-hw-001",
				VipSubnetCidrId:      "subnet-001",
				Description:          "web负载均衡",
				ProjectId:            "project-001",
				CreatedAt:            "2024-01-01T00:00:00Z",
				AvailabilityZoneList: []string{"cn-north-4a", "cn-north-4b"},
				Listeners: []model.ListenerRef{
					{Id: "listener-001"},
					{Id: "listener-002"},
				},
				Pools: []model.PoolRef{
					{Id: "pool-001"},
				},
				Tags: []model.Tag{
					{Key: stringPtr("env"), Value: stringPtr("prod")},
					{Key: stringPtr("team"), Value: stringPtr("infra")},
				},
				Eips: []model.EipInfo{
					{EipId: stringPtr("eip-001"), EipAddress: stringPtr("1.2.3.4")},
				},
			},
			region: "cn-north-4",
			expected: types.LBInstance{
				LoadBalancerID:     "lb-hw-001",
				LoadBalancerName:   "web-elb",
				LoadBalancerType:   "slb",
				Status:             "active",
				Region:             "cn-north-4",
				Zone:               "cn-north-4a",
				Address:            "192.168.1.100",
				AddressType:        "internet",
				VPCID:              "vpc-hw-001",
				VSwitchID:          "subnet-001",
				ListenerCount:      2,
				BackendServerCount: 1,
				Listeners:          nil, // client is nil, so fetchListenerDetails returns nil
				BackendServers:     nil, // client is nil, so fetchBackendServers returns nil
				CreationTime:       "2024-01-01T00:00:00Z",
				ProjectID:          "project-001",
				Description:        "web负载均衡",
				Tags:               map[string]string{"env": "prod", "team": "infra"},
				Provider:           "huawei",
			},
		},
		{
			name: "无EIP时地址类型为intranet",
			lb: model.LoadBalancer{
				Id:                   "lb-hw-002",
				Name:                 "internal-elb",
				ProvisioningStatus:   "ACTIVE",
				VipAddress:           "10.0.0.50",
				VpcId:                "vpc-hw-002",
				AvailabilityZoneList: []string{"cn-north-4a"},
				Eips:                 []model.EipInfo{},
			},
			region: "cn-north-4",
			expected: types.LBInstance{
				LoadBalancerID:   "lb-hw-002",
				LoadBalancerName: "internal-elb",
				LoadBalancerType: "slb",
				Status:           "active",
				Region:           "cn-north-4",
				Zone:             "cn-north-4a",
				Address:          "10.0.0.50",
				AddressType:      "intranet",
				VPCID:            "vpc-hw-002",
				Tags:             map[string]string{},
				Provider:         "huawei",
			},
		},
		{
			name: "空可用区列表",
			lb: model.LoadBalancer{
				Id:                   "lb-hw-003",
				Name:                 "no-az-elb",
				ProvisioningStatus:   "ACTIVE",
				AvailabilityZoneList: []string{},
			},
			region: "cn-east-3",
			expected: types.LBInstance{
				LoadBalancerID:   "lb-hw-003",
				LoadBalancerName: "no-az-elb",
				LoadBalancerType: "slb",
				Status:           "active",
				Region:           "cn-east-3",
				Zone:             "",
				AddressType:      "intranet",
				Tags:             map[string]string{},
				Provider:         "huawei",
			},
		},
		{
			name: "标签中有nil Key或Value",
			lb: model.LoadBalancer{
				Id:                 "lb-hw-004",
				ProvisioningStatus: "ACTIVE",
				Tags: []model.Tag{
					{Key: stringPtr("valid"), Value: stringPtr("tag")},
					{Key: nil, Value: stringPtr("no-key")},
					{Key: stringPtr("no-value"), Value: nil},
					{Key: nil, Value: nil},
				},
			},
			region: "cn-north-4",
			expected: types.LBInstance{
				LoadBalancerID:   "lb-hw-004",
				LoadBalancerType: "slb",
				Status:           "active",
				Region:           "cn-north-4",
				AddressType:      "intranet",
				Tags:             map[string]string{"valid": "tag"},
				Provider:         "huawei",
			},
		},
		{
			name: "空标签列表",
			lb: model.LoadBalancer{
				Id:   "lb-hw-005",
				Tags: []model.Tag{},
			},
			region: "cn-south-1",
			expected: types.LBInstance{
				LoadBalancerID:   "lb-hw-005",
				LoadBalancerType: "slb",
				Region:           "cn-south-1",
				AddressType:      "intranet",
				Tags:             map[string]string{},
				Provider:         "huawei",
			},
		},
		{
			name: "多个可用区取第一个",
			lb: model.LoadBalancer{
				Id:                   "lb-hw-006",
				AvailabilityZoneList: []string{"cn-north-4c", "cn-north-4a", "cn-north-4b"},
			},
			region: "cn-north-4",
			expected: types.LBInstance{
				LoadBalancerID:   "lb-hw-006",
				LoadBalancerType: "slb",
				Region:           "cn-north-4",
				Zone:             "cn-north-4c",
				AddressType:      "intranet",
				Tags:             map[string]string{},
				Provider:         "huawei",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Pass nil client — fetchListenerDetails and fetchBackendServers handle nil gracefully
			result := adapter.convertToLBInstance(tt.lb, tt.region, nil)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertToLBInstance_AddressTypeDetection(t *testing.T) {
	adapter := newTestLBAdapter()

	tests := []struct {
		name     string
		eips     []model.EipInfo
		expected string
	}{
		{
			name:     "无EIP为intranet",
			eips:     []model.EipInfo{},
			expected: "intranet",
		},
		{
			name:     "nil EIP为intranet",
			eips:     nil,
			expected: "intranet",
		},
		{
			name: "有EIP为internet",
			eips: []model.EipInfo{
				{EipId: stringPtr("eip-001")},
			},
			expected: "internet",
		},
		{
			name: "多个EIP为internet",
			eips: []model.EipInfo{
				{EipId: stringPtr("eip-001")},
				{EipId: stringPtr("eip-002")},
			},
			expected: "internet",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lb := model.LoadBalancer{
				Id:   "lb-addr-test",
				Eips: tt.eips,
			}
			result := adapter.convertToLBInstance(lb, "cn-north-4", nil)
			assert.Equal(t, tt.expected, result.AddressType)
		})
	}
}

func TestConvertToLBInstance_ListenerAndPoolCounts(t *testing.T) {
	adapter := newTestLBAdapter()

	tests := []struct {
		name               string
		listeners          []model.ListenerRef
		pools              []model.PoolRef
		expectedListeners  int
		expectedBackendSvr int
	}{
		{
			name:               "无监听器和池",
			listeners:          nil,
			pools:              nil,
			expectedListeners:  0,
			expectedBackendSvr: 0,
		},
		{
			name: "3个监听器2个池",
			listeners: []model.ListenerRef{
				{Id: "l1"}, {Id: "l2"}, {Id: "l3"},
			},
			pools: []model.PoolRef{
				{Id: "p1"}, {Id: "p2"},
			},
			expectedListeners:  3,
			expectedBackendSvr: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lb := model.LoadBalancer{
				Id:        "lb-count-test",
				Listeners: tt.listeners,
				Pools:     tt.pools,
			}
			result := adapter.convertToLBInstance(lb, "cn-north-4", nil)
			assert.Equal(t, tt.expectedListeners, result.ListenerCount)
			assert.Equal(t, tt.expectedBackendSvr, result.BackendServerCount)
		})
	}
}
