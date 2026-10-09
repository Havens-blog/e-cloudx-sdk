package aliyun

import (
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/slb"
	"github.com/gotomicro/ego/core/elog"
	"github.com/stretchr/testify/assert"
)

func newTestLBAdapter() *LBAdapter {
	return &LBAdapter{
		accessKeyID:     "test-ak",
		accessKeySecret: "test-sk",
		defaultRegion:   "cn-hangzhou",
		logger:          elog.DefaultLogger,
	}
}

// ==================== convertToLBInstance ====================

func TestConvertToLBInstance(t *testing.T) {
	adapter := newTestLBAdapter()

	tests := []struct {
		name     string
		lb       slb.LoadBalancer
		region   string
		expected types.LBInstance
	}{
		{
			name: "基本字段映射",
			lb: slb.LoadBalancer{
				LoadBalancerId:     "lb-test-001",
				LoadBalancerName:   "web-slb",
				LoadBalancerStatus: "active",
				MasterZoneId:       "cn-hangzhou-a",
				SlaveZoneId:        "cn-hangzhou-b",
				Address:            "10.0.0.1",
				AddressType:        "internet",
				AddressIPVersion:   "ipv4",
				VpcId:              "vpc-001",
				VSwitchId:          "vsw-001",
				NetworkType:        "vpc",
				LoadBalancerSpec:   "slb.s2.medium",
				Bandwidth:          100,
				InternetChargeType: "PayByTraffic",
				PayType:            "PostPaid",
				CreateTime:         "2024-01-01T00:00:00Z",
				ResourceGroupId:    "rg-001",
			},
			region: "cn-hangzhou",
			expected: types.LBInstance{
				LoadBalancerID:     "lb-test-001",
				LoadBalancerName:   "web-slb",
				LoadBalancerType:   "clb",
				Status:             "active",
				Region:             "cn-hangzhou",
				Zone:               "cn-hangzhou-a",
				SlaveZone:          "cn-hangzhou-b",
				Address:            "10.0.0.1",
				AddressType:        "internet",
				AddressIPVersion:   "ipv4",
				VPCID:              "vpc-001",
				VSwitchID:          "vsw-001",
				NetworkType:        "vpc",
				LoadBalancerSpec:   "slb.s2.medium",
				Bandwidth:          100,
				InternetChargeType: "PayByTraffic",
				ChargeType:         "PostPaid",
				CreationTime:       "2024-01-01T00:00:00Z",
				ResourceGroupID:    "rg-001",
				Tags:               map[string]string{},
				Provider:           "aliyun",
			},
		},
		{
			name:   "空字段",
			lb:     slb.LoadBalancer{},
			region: "cn-shanghai",
			expected: types.LBInstance{
				LoadBalancerType: "clb",
				Region:           "cn-shanghai",
				Tags:             map[string]string{},
				Provider:         "aliyun",
			},
		},
		{
			name: "有LoadBalancerSpec时类型为clb",
			lb: slb.LoadBalancer{
				LoadBalancerSpec: "slb.s3.large",
			},
			region: "cn-beijing",
			expected: types.LBInstance{
				LoadBalancerType: "clb",
				LoadBalancerSpec: "slb.s3.large",
				Region:           "cn-beijing",
				Tags:             map[string]string{},
				Provider:         "aliyun",
			},
		},
		{
			name: "无LoadBalancerSpec时类型仍为clb",
			lb: slb.LoadBalancer{
				LoadBalancerId: "lb-no-spec",
			},
			region: "cn-shenzhen",
			expected: types.LBInstance{
				LoadBalancerID:   "lb-no-spec",
				LoadBalancerType: "clb",
				Region:           "cn-shenzhen",
				Tags:             map[string]string{},
				Provider:         "aliyun",
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

// ==================== convertDetailToLBInstance ====================

func TestConvertDetailToLBInstance(t *testing.T) {
	adapter := newTestLBAdapter()

	tests := []struct {
		name     string
		resp     *slb.DescribeLoadBalancerAttributeResponse
		region   string
		expected types.LBInstance
	}{
		{
			name: "基本字段映射含监听器和后端服务器",
			resp: &slb.DescribeLoadBalancerAttributeResponse{
				LoadBalancerId:     "lb-detail-001",
				LoadBalancerName:   "detail-slb",
				LoadBalancerStatus: "active",
				MasterZoneId:       "cn-hangzhou-a",
				SlaveZoneId:        "cn-hangzhou-b",
				Address:            "10.0.0.2",
				AddressType:        "intranet",
				AddressIPVersion:   "ipv4",
				VpcId:              "vpc-002",
				VSwitchId:          "vsw-002",
				NetworkType:        "vpc",
				LoadBalancerSpec:   "slb.s1.small",
				Bandwidth:          50,
				InternetChargeType: "PayByBandwidth",
				PayType:            "PrePaid",
				CreateTime:         "2024-06-01T12:00:00Z",
				ResourceGroupId:    "rg-002",
				ListenerPortsAndProtocol: slb.ListenerPortsAndProtocol{
					ListenerPortAndProtocol: []slb.ListenerPortAndProtocol{
						{ListenerPort: 80, ListenerProtocol: "HTTP", ListenerForward: "on"},
						{ListenerPort: 443, ListenerProtocol: "HTTPS", ListenerForward: "off"},
					},
				},
				BackendServers: slb.BackendServersInDescribeLoadBalancerAttribute{
					BackendServer: []slb.BackendServerInDescribeLoadBalancerAttribute{
						{ServerId: "i-001", Weight: 100, Type: "ecs", Description: "web-1"},
						{ServerId: "i-002", Weight: 50, Type: "ecs", Description: "web-2"},
					},
				},
			},
			region: "cn-hangzhou",
			expected: types.LBInstance{
				LoadBalancerID:     "lb-detail-001",
				LoadBalancerName:   "detail-slb",
				LoadBalancerType:   "clb",
				Status:             "active",
				Region:             "cn-hangzhou",
				Zone:               "cn-hangzhou-a",
				SlaveZone:          "cn-hangzhou-b",
				Address:            "10.0.0.2",
				AddressType:        "intranet",
				AddressIPVersion:   "ipv4",
				VPCID:              "vpc-002",
				VSwitchID:          "vsw-002",
				NetworkType:        "vpc",
				LoadBalancerSpec:   "slb.s1.small",
				Bandwidth:          50,
				InternetChargeType: "PayByBandwidth",
				ChargeType:         "PrePaid",
				CreationTime:       "2024-06-01T12:00:00Z",
				ResourceGroupID:    "rg-002",
				ListenerCount:      2,
				BackendServerCount: 2,
				Listeners: []types.LBListener{
					{ListenerPort: 80, ListenerProtocol: "HTTP", Description: "on"},
					{ListenerPort: 443, ListenerProtocol: "HTTPS", Description: "off"},
				},
				BackendServers: []types.LBBackendServer{
					{ServerID: "i-001", Weight: 100, Type: "ecs", Description: "web-1"},
					{ServerID: "i-002", Weight: 50, Type: "ecs", Description: "web-2"},
				},
				Tags:        map[string]string{},
				Description: "detail-slb",
				Provider:    "aliyun",
			},
		},
		{
			name: "无监听器和后端服务器",
			resp: &slb.DescribeLoadBalancerAttributeResponse{
				LoadBalancerId:   "lb-empty",
				LoadBalancerName: "empty-slb",
			},
			region: "cn-shanghai",
			expected: types.LBInstance{
				LoadBalancerID:   "lb-empty",
				LoadBalancerName: "empty-slb",
				LoadBalancerType: "clb",
				Region:           "cn-shanghai",
				Tags:             map[string]string{},
				Description:      "empty-slb",
				Provider:         "aliyun",
			},
		},
		{
			name:   "完全空的响应",
			resp:   &slb.DescribeLoadBalancerAttributeResponse{},
			region: "cn-beijing",
			expected: types.LBInstance{
				LoadBalancerType: "clb",
				Region:           "cn-beijing",
				Tags:             map[string]string{},
				Provider:         "aliyun",
			},
		},
		{
			name: "单个监听器和单个后端服务器",
			resp: &slb.DescribeLoadBalancerAttributeResponse{
				LoadBalancerId: "lb-single",
				ListenerPortsAndProtocol: slb.ListenerPortsAndProtocol{
					ListenerPortAndProtocol: []slb.ListenerPortAndProtocol{
						{ListenerPort: 8080, ListenerProtocol: "TCP"},
					},
				},
				BackendServers: slb.BackendServersInDescribeLoadBalancerAttribute{
					BackendServer: []slb.BackendServerInDescribeLoadBalancerAttribute{
						{ServerId: "i-single", Weight: 100, Type: "eni"},
					},
				},
			},
			region: "cn-hangzhou",
			expected: types.LBInstance{
				LoadBalancerID:     "lb-single",
				LoadBalancerType:   "clb",
				Region:             "cn-hangzhou",
				ListenerCount:      1,
				BackendServerCount: 1,
				Listeners: []types.LBListener{
					{ListenerPort: 8080, ListenerProtocol: "TCP"},
				},
				BackendServers: []types.LBBackendServer{
					{ServerID: "i-single", Weight: 100, Type: "eni"},
				},
				Tags:     map[string]string{},
				Provider: "aliyun",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := adapter.convertDetailToLBInstance(tt.resp, tt.region)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ==================== convertALBToLBInstance ====================

func TestConvertALBToLBInstance(t *testing.T) {
	adapter := newTestLBAdapter()

	tests := []struct {
		name     string
		lb       albInstance
		region   string
		expected types.LBInstance
	}{
		{
			name: "基本ALB字段映射",
			lb: albInstance{
				LoadBalancerID:      "alb-001",
				LoadBalancerName:    "web-alb",
				LoadBalancerStatus:  "Active",
				AddressType:         "Internet",
				VpcID:               "vpc-alb-001",
				CreateTime:          "2024-03-01T00:00:00Z",
				LoadBalancerEdition: "Standard",
				DNSName:             "alb-001.cn-hangzhou.alb.aliyuncs.com",
				ResourceGroupID:     "rg-alb-001",
				AddressIPVersion:    "IPv4",
			},
			region: "cn-hangzhou",
			expected: types.LBInstance{
				LoadBalancerID:      "alb-001",
				LoadBalancerName:    "web-alb",
				LoadBalancerType:    "alb",
				Status:              "active",
				Region:              "cn-hangzhou",
				Address:             "alb-001.cn-hangzhou.alb.aliyuncs.com",
				AddressType:         "Internet",
				AddressIPVersion:    "IPv4",
				VPCID:               "vpc-alb-001",
				LoadBalancerEdition: "Standard",
				CreationTime:        "2024-03-01T00:00:00Z",
				ResourceGroupID:     "rg-alb-001",
				Tags:                map[string]string{},
				Provider:            "aliyun",
			},
		},
		{
			name:   "空ALB实例",
			lb:     albInstance{},
			region: "cn-shanghai",
			expected: types.LBInstance{
				LoadBalancerType: "alb",
				Region:           "cn-shanghai",
				Tags:             map[string]string{},
				Provider:         "aliyun",
			},
		},
		{
			name: "WAF版本ALB",
			lb: albInstance{
				LoadBalancerID:      "alb-waf",
				LoadBalancerEdition: "WAF",
				AddressType:         "Intranet",
			},
			region: "cn-beijing",
			expected: types.LBInstance{
				LoadBalancerID:      "alb-waf",
				LoadBalancerType:    "alb",
				Region:              "cn-beijing",
				AddressType:         "Intranet",
				LoadBalancerEdition: "WAF",
				Tags:                map[string]string{},
				Provider:            "aliyun",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := adapter.convertALBToLBInstance(tt.lb, tt.region)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ==================== convertNLBToLBInstance ====================

func TestConvertNLBToLBInstance(t *testing.T) {
	adapter := newTestLBAdapter()

	tests := []struct {
		name     string
		lb       nlbInstance
		region   string
		expected types.LBInstance
	}{
		{
			name: "基本NLB字段映射",
			lb: nlbInstance{
				LoadBalancerID:     "nlb-001",
				LoadBalancerName:   "tcp-nlb",
				LoadBalancerStatus: "Active",
				AddressType:        "Internet",
				VpcID:              "vpc-nlb-001",
				CreateTime:         "2024-05-01T00:00:00Z",
				DNSName:            "nlb-001.cn-hangzhou.nlb.aliyuncs.com",
				ResourceGroupID:    "rg-nlb-001",
				AddressIPVersion:   "DualStack",
				BandwidthPackageID: "bwp-001",
			},
			region: "cn-hangzhou",
			expected: types.LBInstance{
				LoadBalancerID:     "nlb-001",
				LoadBalancerName:   "tcp-nlb",
				LoadBalancerType:   "nlb",
				Status:             "active",
				Region:             "cn-hangzhou",
				Address:            "nlb-001.cn-hangzhou.nlb.aliyuncs.com",
				AddressType:        "Internet",
				AddressIPVersion:   "DualStack",
				VPCID:              "vpc-nlb-001",
				BandwidthPackageID: "bwp-001",
				CreationTime:       "2024-05-01T00:00:00Z",
				ResourceGroupID:    "rg-nlb-001",
				Tags:               map[string]string{},
				Provider:           "aliyun",
			},
		},
		{
			name:   "空NLB实例",
			lb:     nlbInstance{},
			region: "cn-shanghai",
			expected: types.LBInstance{
				LoadBalancerType: "nlb",
				Region:           "cn-shanghai",
				Tags:             map[string]string{},
				Provider:         "aliyun",
			},
		},
		{
			name: "内网NLB",
			lb: nlbInstance{
				LoadBalancerID:   "nlb-internal",
				LoadBalancerName: "internal-nlb",
				AddressType:      "Intranet",
				VpcID:            "vpc-internal",
			},
			region: "cn-shenzhen",
			expected: types.LBInstance{
				LoadBalancerID:   "nlb-internal",
				LoadBalancerName: "internal-nlb",
				LoadBalancerType: "nlb",
				Region:           "cn-shenzhen",
				AddressType:      "Intranet",
				VPCID:            "vpc-internal",
				Tags:             map[string]string{},
				Provider:         "aliyun",
			},
		},
		{
			name: "带带宽包的NLB",
			lb: nlbInstance{
				LoadBalancerID:     "nlb-bwp",
				BandwidthPackageID: "bwp-large",
				AddressIPVersion:   "IPv4",
			},
			region: "cn-hangzhou",
			expected: types.LBInstance{
				LoadBalancerID:     "nlb-bwp",
				LoadBalancerType:   "nlb",
				Region:             "cn-hangzhou",
				AddressIPVersion:   "IPv4",
				BandwidthPackageID: "bwp-large",
				Tags:               map[string]string{},
				Provider:           "aliyun",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := adapter.convertNLBToLBInstance(tt.lb, tt.region)
			assert.Equal(t, tt.expected, result)
		})
	}
}
