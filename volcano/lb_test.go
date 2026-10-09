package volcano

import (
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/stretchr/testify/assert"
	"github.com/volcengine/volcengine-go-sdk/service/alb"
	"github.com/volcengine/volcengine-go-sdk/service/clb"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
)

func newTestLBAdapter() *LBAdapter {
	return &LBAdapter{
		accessKeyID:     "test-ak",
		accessKeySecret: "test-sk",
		defaultRegion:   "cn-beijing",
		logger:          elog.DefaultLogger,
	}
}

// ==================== convertToLBInstance (CLB) ====================

func TestConvertToLBInstance(t *testing.T) {
	adapter := newTestLBAdapter()

	tests := []struct {
		name     string
		lb       *clb.LoadBalancerForDescribeLoadBalancersOutput
		region   string
		expected types.LBInstance
	}{
		{
			name: "基本CLB字段映射",
			lb: &clb.LoadBalancerForDescribeLoadBalancersOutput{
				LoadBalancerId:   volcengine.String("clb-001"),
				LoadBalancerName: volcengine.String("web-clb"),
				Status:           volcengine.String("Active"),
				EniAddress:       volcengine.String("192.168.1.10"),
				Type:             volcengine.String("public"),
				VpcId:            volcengine.String("vpc-001"),
				SubnetId:         volcengine.String("subnet-001"),
				CreateTime:       volcengine.String("2024-01-01T00:00:00Z"),
				Description:      volcengine.String("web负载均衡"),
				Tags: []*clb.TagForDescribeLoadBalancersOutput{
					{Key: volcengine.String("env"), Value: volcengine.String("prod")},
					{Key: volcengine.String("team"), Value: volcengine.String("infra")},
				},
			},
			region: "cn-beijing",
			expected: types.LBInstance{
				LoadBalancerID:   "clb-001",
				LoadBalancerName: "web-clb",
				LoadBalancerType: "clb",
				Status:           "active",
				Region:           "cn-beijing",
				Address:          "192.168.1.10",
				AddressType:      "internet",
				VPCID:            "vpc-001",
				VSwitchID:        "subnet-001",
				CreationTime:     "2024-01-01T00:00:00Z",
				Description:      "web负载均衡",
				Tags:             map[string]string{"env": "prod", "team": "infra"},
				Provider:         "volcano",
			},
		},
		{
			name: "CLB类型为clb",
			lb: &clb.LoadBalancerForDescribeLoadBalancersOutput{
				LoadBalancerId: volcengine.String("clb-type-test"),
			},
			region: "cn-beijing",
			expected: types.LBInstance{
				LoadBalancerID:   "clb-type-test",
				LoadBalancerType: "clb",
				Region:           "cn-beijing",
				AddressType:      "intranet",
				Tags:             map[string]string{},
				Provider:         "volcano",
			},
		},
		{
			name: "非public类型为intranet",
			lb: &clb.LoadBalancerForDescribeLoadBalancersOutput{
				LoadBalancerId: volcengine.String("clb-internal"),
				Type:           volcengine.String("private"),
			},
			region: "cn-shanghai",
			expected: types.LBInstance{
				LoadBalancerID:   "clb-internal",
				LoadBalancerType: "clb",
				Region:           "cn-shanghai",
				AddressType:      "intranet",
				Tags:             map[string]string{},
				Provider:         "volcano",
			},
		},
		{
			name: "nil Type为intranet",
			lb: &clb.LoadBalancerForDescribeLoadBalancersOutput{
				LoadBalancerId: volcengine.String("clb-nil-type"),
				Type:           nil,
			},
			region: "cn-beijing",
			expected: types.LBInstance{
				LoadBalancerID:   "clb-nil-type",
				LoadBalancerType: "clb",
				Region:           "cn-beijing",
				AddressType:      "intranet",
				Tags:             map[string]string{},
				Provider:         "volcano",
			},
		},
		{
			name:   "所有字段为nil",
			lb:     &clb.LoadBalancerForDescribeLoadBalancersOutput{},
			region: "cn-guangzhou",
			expected: types.LBInstance{
				LoadBalancerType: "clb",
				Region:           "cn-guangzhou",
				AddressType:      "intranet",
				Tags:             map[string]string{},
				Provider:         "volcano",
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
		output   *clb.DescribeLoadBalancerAttributesOutput
		region   string
		expected types.LBInstance
	}{
		{
			name: "基本详情字段映射含监听器",
			output: &clb.DescribeLoadBalancerAttributesOutput{
				LoadBalancerId:   volcengine.String("clb-detail-001"),
				LoadBalancerName: volcengine.String("detail-clb"),
				Status:           volcengine.String("Active"),
				EniAddress:       volcengine.String("10.0.0.5"),
				Type:             volcengine.String("public"),
				VpcId:            volcengine.String("vpc-detail"),
				SubnetId:         volcengine.String("subnet-detail"),
				CreateTime:       volcengine.String("2024-03-01T00:00:00Z"),
				Description:      volcengine.String("详情测试"),
				Listeners: []*clb.ListenerForDescribeLoadBalancerAttributesOutput{
					{ListenerId: volcengine.String("listener-001"), ListenerName: volcengine.String("http-80")},
					{ListenerId: volcengine.String("listener-002"), ListenerName: volcengine.String("https-443")},
				},
				Tags: []*clb.TagForDescribeLoadBalancerAttributesOutput{
					{Key: volcengine.String("app"), Value: volcengine.String("web")},
				},
			},
			region: "cn-beijing",
			expected: types.LBInstance{
				LoadBalancerID:   "clb-detail-001",
				LoadBalancerName: "detail-clb",
				LoadBalancerType: "clb",
				Status:           "active",
				Region:           "cn-beijing",
				Address:          "10.0.0.5",
				AddressType:      "internet",
				VPCID:            "vpc-detail",
				VSwitchID:        "subnet-detail",
				ListenerCount:    2,
				Listeners: []types.LBListener{
					{ListenerID: "listener-001", Description: "http-80"},
					{ListenerID: "listener-002", Description: "https-443"},
				},
				CreationTime: "2024-03-01T00:00:00Z",
				Description:  "详情测试",
				Tags:         map[string]string{"app": "web"},
				Provider:     "volcano",
			},
		},
		{
			name: "无监听器",
			output: &clb.DescribeLoadBalancerAttributesOutput{
				LoadBalancerId: volcengine.String("clb-no-listener"),
				Listeners:      nil,
			},
			region: "cn-beijing",
			expected: types.LBInstance{
				LoadBalancerID:   "clb-no-listener",
				LoadBalancerType: "clb",
				Region:           "cn-beijing",
				AddressType:      "intranet",
				ListenerCount:    0,
				Tags:             map[string]string{},
				Provider:         "volcano",
			},
		},
		{
			name:   "所有字段为nil",
			output: &clb.DescribeLoadBalancerAttributesOutput{},
			region: "cn-shanghai",
			expected: types.LBInstance{
				LoadBalancerType: "clb",
				Region:           "cn-shanghai",
				AddressType:      "intranet",
				Tags:             map[string]string{},
				Provider:         "volcano",
			},
		},
		{
			name: "监听器中有nil字段",
			output: &clb.DescribeLoadBalancerAttributesOutput{
				LoadBalancerId: volcengine.String("clb-nil-listener-fields"),
				Listeners: []*clb.ListenerForDescribeLoadBalancerAttributesOutput{
					{ListenerId: nil, ListenerName: nil},
					{ListenerId: volcengine.String("l-valid"), ListenerName: volcengine.String("valid")},
				},
			},
			region: "cn-beijing",
			expected: types.LBInstance{
				LoadBalancerID:   "clb-nil-listener-fields",
				LoadBalancerType: "clb",
				Region:           "cn-beijing",
				AddressType:      "intranet",
				ListenerCount:    2,
				Listeners: []types.LBListener{
					{},
					{ListenerID: "l-valid", Description: "valid"},
				},
				Tags:     map[string]string{},
				Provider: "volcano",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := adapter.convertDetailToLBInstance(tt.output, tt.region)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ==================== convertALBToLBInstance ====================

func TestConvertALBToLBInstance(t *testing.T) {
	adapter := newTestLBAdapter()

	tests := []struct {
		name     string
		lb       *alb.LoadBalancerForDescribeLoadBalancersOutput
		region   string
		expected types.LBInstance
	}{
		{
			name: "基本ALB字段映射",
			lb: &alb.LoadBalancerForDescribeLoadBalancersOutput{
				LoadBalancerId:      volcengine.String("alb-001"),
				LoadBalancerName:    volcengine.String("web-alb"),
				Status:              volcengine.String("Active"),
				EniAddress:          volcengine.String("10.0.0.20"),
				Type:                volcengine.String("public"),
				AddressIpVersion:    volcengine.String("IPv4"),
				VpcId:               volcengine.String("vpc-alb-001"),
				SubnetId:            volcengine.String("subnet-alb-001"),
				CreateTime:          volcengine.String("2024-02-01T00:00:00Z"),
				Description:         volcengine.String("ALB测试"),
				LoadBalancerEdition: volcengine.String("Standard"),
				Tags: []*alb.TagForDescribeLoadBalancersOutput{
					{Key: volcengine.String("env"), Value: volcengine.String("staging")},
				},
			},
			region: "cn-beijing",
			expected: types.LBInstance{
				LoadBalancerID:      "alb-001",
				LoadBalancerName:    "web-alb",
				LoadBalancerType:    "alb",
				LoadBalancerEdition: "Standard",
				Status:              "active",
				Region:              "cn-beijing",
				Address:             "10.0.0.20",
				AddressType:         "internet",
				AddressIPVersion:    "IPv4",
				VPCID:               "vpc-alb-001",
				VSwitchID:           "subnet-alb-001",
				CreationTime:        "2024-02-01T00:00:00Z",
				Description:         "ALB测试",
				Tags:                map[string]string{"env": "staging"},
				Provider:            "volcano",
			},
		},
		{
			name: "ALB类型为alb",
			lb: &alb.LoadBalancerForDescribeLoadBalancersOutput{
				LoadBalancerId: volcengine.String("alb-type-test"),
			},
			region: "cn-beijing",
			expected: types.LBInstance{
				LoadBalancerID:   "alb-type-test",
				LoadBalancerType: "alb",
				Region:           "cn-beijing",
				AddressType:      "intranet",
				Tags:             map[string]string{},
				Provider:         "volcano",
			},
		},
		{
			name: "内网ALB",
			lb: &alb.LoadBalancerForDescribeLoadBalancersOutput{
				LoadBalancerId: volcengine.String("alb-internal"),
				Type:           volcengine.String("private"),
				EniAddress:     volcengine.String("172.16.0.10"),
			},
			region: "cn-shanghai",
			expected: types.LBInstance{
				LoadBalancerID:   "alb-internal",
				LoadBalancerType: "alb",
				Region:           "cn-shanghai",
				Address:          "172.16.0.10",
				AddressType:      "intranet",
				Tags:             map[string]string{},
				Provider:         "volcano",
			},
		},
		{
			name:   "所有字段为nil",
			lb:     &alb.LoadBalancerForDescribeLoadBalancersOutput{},
			region: "cn-guangzhou",
			expected: types.LBInstance{
				LoadBalancerType: "alb",
				Region:           "cn-guangzhou",
				AddressType:      "intranet",
				Tags:             map[string]string{},
				Provider:         "volcano",
			},
		},
		{
			name: "带LoadBalancerEdition",
			lb: &alb.LoadBalancerForDescribeLoadBalancersOutput{
				LoadBalancerId:      volcengine.String("alb-edition"),
				LoadBalancerEdition: volcengine.String("WAF"),
			},
			region: "cn-beijing",
			expected: types.LBInstance{
				LoadBalancerID:      "alb-edition",
				LoadBalancerType:    "alb",
				LoadBalancerEdition: "WAF",
				Region:              "cn-beijing",
				AddressType:         "intranet",
				Tags:                map[string]string{},
				Provider:            "volcano",
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

// ==================== Tag extraction tests ====================

func TestConvertToLBInstance_TagExtraction(t *testing.T) {
	adapter := newTestLBAdapter()

	tests := []struct {
		name     string
		tags     []*clb.TagForDescribeLoadBalancersOutput
		expected map[string]string
	}{
		{
			name:     "nil标签",
			tags:     nil,
			expected: map[string]string{},
		},
		{
			name:     "空标签列表",
			tags:     []*clb.TagForDescribeLoadBalancersOutput{},
			expected: map[string]string{},
		},
		{
			name: "正常标签",
			tags: []*clb.TagForDescribeLoadBalancersOutput{
				{Key: volcengine.String("k1"), Value: volcengine.String("v1")},
				{Key: volcengine.String("k2"), Value: volcengine.String("v2")},
			},
			expected: map[string]string{"k1": "v1", "k2": "v2"},
		},
		{
			name: "标签中有nil Key或Value",
			tags: []*clb.TagForDescribeLoadBalancersOutput{
				{Key: volcengine.String("valid"), Value: volcengine.String("tag")},
				{Key: nil, Value: volcengine.String("no-key")},
				{Key: volcengine.String("no-value"), Value: nil},
			},
			expected: map[string]string{"valid": "tag"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lb := &clb.LoadBalancerForDescribeLoadBalancersOutput{
				Tags: tt.tags,
			}
			result := adapter.convertToLBInstance(lb, "cn-beijing")
			assert.Equal(t, tt.expected, result.Tags)
		})
	}
}

func TestConvertALBToLBInstance_TagExtraction(t *testing.T) {
	adapter := newTestLBAdapter()

	tests := []struct {
		name     string
		tags     []*alb.TagForDescribeLoadBalancersOutput
		expected map[string]string
	}{
		{
			name:     "nil标签",
			tags:     nil,
			expected: map[string]string{},
		},
		{
			name: "正常标签",
			tags: []*alb.TagForDescribeLoadBalancersOutput{
				{Key: volcengine.String("k1"), Value: volcengine.String("v1")},
			},
			expected: map[string]string{"k1": "v1"},
		},
		{
			name: "标签中有nil Key或Value",
			tags: []*alb.TagForDescribeLoadBalancersOutput{
				{Key: volcengine.String("valid"), Value: volcengine.String("tag")},
				{Key: nil, Value: volcengine.String("no-key")},
				{Key: volcengine.String("no-value"), Value: nil},
			},
			expected: map[string]string{"valid": "tag"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lb := &alb.LoadBalancerForDescribeLoadBalancersOutput{
				Tags: tt.tags,
			}
			result := adapter.convertALBToLBInstance(lb, "cn-beijing")
			assert.Equal(t, tt.expected, result.Tags)
		})
	}
}

// ==================== Address type mapping tests ====================

func TestConvertToLBInstance_AddressTypeMapping(t *testing.T) {
	adapter := newTestLBAdapter()

	tests := []struct {
		name     string
		lbType   *string
		expected string
	}{
		{"public为internet", volcengine.String("public"), "internet"},
		{"private为intranet", volcengine.String("private"), "intranet"},
		{"nil为intranet", nil, "intranet"},
		{"空字符串为intranet", volcengine.String(""), "intranet"},
		{"其他值为intranet", volcengine.String("other"), "intranet"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lb := &clb.LoadBalancerForDescribeLoadBalancersOutput{
				Type: tt.lbType,
			}
			result := adapter.convertToLBInstance(lb, "cn-beijing")
			assert.Equal(t, tt.expected, result.AddressType)
		})
	}
}

func TestConvertALBToLBInstance_AddressTypeMapping(t *testing.T) {
	adapter := newTestLBAdapter()

	tests := []struct {
		name     string
		lbType   *string
		expected string
	}{
		{"public为internet", volcengine.String("public"), "internet"},
		{"private为intranet", volcengine.String("private"), "intranet"},
		{"nil为intranet", nil, "intranet"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lb := &alb.LoadBalancerForDescribeLoadBalancersOutput{
				Type: tt.lbType,
			}
			result := adapter.convertALBToLBInstance(lb, "cn-beijing")
			assert.Equal(t, tt.expected, result.AddressType)
		})
	}
}
