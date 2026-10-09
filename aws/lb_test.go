package aws

import (
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aws/aws-sdk-go-v2/aws"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/stretchr/testify/assert"
)

func newTestLBAdapter() *LBAdapter {
	return &LBAdapter{
		accessKeyID:     "test-ak",
		accessKeySecret: "test-sk",
		defaultRegion:   "us-east-1",
		logger:          elog.DefaultLogger,
	}
}

func TestConvertToLBInstance(t *testing.T) {
	adapter := newTestLBAdapter()
	createdTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

	tests := []struct {
		name     string
		lb       elbv2types.LoadBalancer
		region   string
		expected types.LBInstance
	}{
		{
			name: "ALB类型映射",
			lb: elbv2types.LoadBalancer{
				LoadBalancerArn:  aws.String("arn:aws:elasticloadbalancing:us-east-1:123456789:loadbalancer/app/my-alb/abc123"),
				LoadBalancerName: aws.String("my-alb"),
				Type:             elbv2types.LoadBalancerTypeEnumApplication,
				State: &elbv2types.LoadBalancerState{
					Code: elbv2types.LoadBalancerStateEnumActive,
				},
				Scheme:        elbv2types.LoadBalancerSchemeEnumInternetFacing,
				VpcId:         aws.String("vpc-001"),
				DNSName:       aws.String("my-alb-123.us-east-1.elb.amazonaws.com"),
				IpAddressType: elbv2types.IpAddressTypeIpv4,
				AvailabilityZones: []elbv2types.AvailabilityZone{
					{ZoneName: aws.String("us-east-1a")},
					{ZoneName: aws.String("us-east-1b")},
				},
				CreatedTime: &createdTime,
			},
			region: "us-east-1",
			expected: types.LBInstance{
				LoadBalancerID:   "arn:aws:elasticloadbalancing:us-east-1:123456789:loadbalancer/app/my-alb/abc123",
				LoadBalancerName: "my-alb",
				LoadBalancerType: "alb",
				Status:           "active",
				Region:           "us-east-1",
				Zone:             "us-east-1a",
				Address:          "my-alb-123.us-east-1.elb.amazonaws.com",
				AddressType:      "internet",
				AddressIPVersion: "ipv4",
				VPCID:            "vpc-001",
				CreationTime:     "2024-01-15T10:30:00Z",
				Tags:             map[string]string{},
				Provider:         "aws",
			},
		},
		{
			name: "NLB类型映射",
			lb: elbv2types.LoadBalancer{
				LoadBalancerArn:  aws.String("arn:aws:elasticloadbalancing:us-west-2:123456789:loadbalancer/net/my-nlb/def456"),
				LoadBalancerName: aws.String("my-nlb"),
				Type:             elbv2types.LoadBalancerTypeEnumNetwork,
				State: &elbv2types.LoadBalancerState{
					Code: elbv2types.LoadBalancerStateEnumActive,
				},
				Scheme:        elbv2types.LoadBalancerSchemeEnumInternal,
				VpcId:         aws.String("vpc-002"),
				DNSName:       aws.String("my-nlb-456.us-west-2.elb.amazonaws.com"),
				IpAddressType: elbv2types.IpAddressTypeDualstack,
				AvailabilityZones: []elbv2types.AvailabilityZone{
					{ZoneName: aws.String("us-west-2a")},
				},
				CreatedTime: &createdTime,
			},
			region: "us-west-2",
			expected: types.LBInstance{
				LoadBalancerID:   "arn:aws:elasticloadbalancing:us-west-2:123456789:loadbalancer/net/my-nlb/def456",
				LoadBalancerName: "my-nlb",
				LoadBalancerType: "nlb",
				Status:           "active",
				Region:           "us-west-2",
				Zone:             "us-west-2a",
				Address:          "my-nlb-456.us-west-2.elb.amazonaws.com",
				AddressType:      "intranet",
				AddressIPVersion: "dualstack",
				VPCID:            "vpc-002",
				CreationTime:     "2024-01-15T10:30:00Z",
				Tags:             map[string]string{},
				Provider:         "aws",
			},
		},
		{
			name: "Gateway类型映射为slb",
			lb: elbv2types.LoadBalancer{
				LoadBalancerArn:  aws.String("arn:aws:elasticloadbalancing:us-east-1:123456789:loadbalancer/gwy/my-gwlb/ghi789"),
				LoadBalancerName: aws.String("my-gwlb"),
				Type:             elbv2types.LoadBalancerTypeEnumGateway,
				State: &elbv2types.LoadBalancerState{
					Code: elbv2types.LoadBalancerStateEnumProvisioning,
				},
				Scheme:        elbv2types.LoadBalancerSchemeEnumInternal,
				VpcId:         aws.String("vpc-003"),
				IpAddressType: elbv2types.IpAddressTypeIpv4,
			},
			region: "us-east-1",
			expected: types.LBInstance{
				LoadBalancerID:   "arn:aws:elasticloadbalancing:us-east-1:123456789:loadbalancer/gwy/my-gwlb/ghi789",
				LoadBalancerName: "my-gwlb",
				LoadBalancerType: "slb",
				Status:           "creating",
				Region:           "us-east-1",
				AddressType:      "intranet",
				AddressIPVersion: "ipv4",
				VPCID:            "vpc-003",
				Tags:             map[string]string{},
				Provider:         "aws",
			},
		},
		{
			name: "nil State和nil CreatedTime",
			lb: elbv2types.LoadBalancer{
				LoadBalancerArn:  aws.String("arn:aws:elasticloadbalancing:us-east-1:123456789:loadbalancer/app/nil-state/xyz"),
				LoadBalancerName: aws.String("nil-state-lb"),
				Type:             elbv2types.LoadBalancerTypeEnumApplication,
				Scheme:           elbv2types.LoadBalancerSchemeEnumInternetFacing,
				VpcId:            aws.String("vpc-nil"),
				IpAddressType:    elbv2types.IpAddressTypeIpv4,
			},
			region: "us-east-1",
			expected: types.LBInstance{
				LoadBalancerID:   "arn:aws:elasticloadbalancing:us-east-1:123456789:loadbalancer/app/nil-state/xyz",
				LoadBalancerName: "nil-state-lb",
				LoadBalancerType: "alb",
				Status:           "",
				Region:           "us-east-1",
				Address:          "",
				AddressType:      "internet",
				AddressIPVersion: "ipv4",
				VPCID:            "vpc-nil",
				CreationTime:     "",
				Tags:             map[string]string{},
				Provider:         "aws",
			},
		},
		{
			name: "空可用区列表",
			lb: elbv2types.LoadBalancer{
				LoadBalancerArn:   aws.String("arn:aws:elasticloadbalancing:eu-west-1:123456789:loadbalancer/app/no-az/abc"),
				LoadBalancerName:  aws.String("no-az-lb"),
				Type:              elbv2types.LoadBalancerTypeEnumApplication,
				Scheme:            elbv2types.LoadBalancerSchemeEnumInternal,
				AvailabilityZones: []elbv2types.AvailabilityZone{},
				IpAddressType:     elbv2types.IpAddressTypeIpv4,
			},
			region: "eu-west-1",
			expected: types.LBInstance{
				LoadBalancerID:   "arn:aws:elasticloadbalancing:eu-west-1:123456789:loadbalancer/app/no-az/abc",
				LoadBalancerName: "no-az-lb",
				LoadBalancerType: "alb",
				Region:           "eu-west-1",
				Zone:             "",
				AddressType:      "intranet",
				AddressIPVersion: "ipv4",
				Tags:             map[string]string{},
				Provider:         "aws",
			},
		},
		{
			name: "nil指针字段",
			lb: elbv2types.LoadBalancer{
				LoadBalancerArn:  nil,
				LoadBalancerName: nil,
				VpcId:            nil,
				DNSName:          nil,
			},
			region: "ap-northeast-1",
			expected: types.LBInstance{
				LoadBalancerID:   "",
				LoadBalancerName: "",
				LoadBalancerType: "slb",
				Region:           "ap-northeast-1",
				AddressType:      "intranet",
				AddressIPVersion: "ipv4",
				Tags:             map[string]string{},
				Provider:         "aws",
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

func TestConvertToLBInstance_StatusMapping(t *testing.T) {
	adapter := newTestLBAdapter()

	stateCodes := []struct {
		code     elbv2types.LoadBalancerStateEnum
		expected string
	}{
		// 归一后 canonical（types.NormalizeLBStatus）：provisioning→creating、active_impaired/failed→error
		{elbv2types.LoadBalancerStateEnumActive, "active"},
		{elbv2types.LoadBalancerStateEnumProvisioning, "creating"},
		{elbv2types.LoadBalancerStateEnumActiveImpaired, "error"},
		{elbv2types.LoadBalancerStateEnumFailed, "error"},
	}

	for _, sc := range stateCodes {
		t.Run(string(sc.code), func(t *testing.T) {
			lb := elbv2types.LoadBalancer{
				LoadBalancerArn: aws.String("arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/test/abc"),
				State: &elbv2types.LoadBalancerState{
					Code: sc.code,
				},
			}
			result := adapter.convertToLBInstance(lb, "us-east-1")
			assert.Equal(t, sc.expected, result.Status)
		})
	}
}

func TestConvertToLBInstance_AddressTypeMapping(t *testing.T) {
	adapter := newTestLBAdapter()

	tests := []struct {
		name     string
		scheme   elbv2types.LoadBalancerSchemeEnum
		expected string
	}{
		{"internet-facing", elbv2types.LoadBalancerSchemeEnumInternetFacing, "internet"},
		{"internal", elbv2types.LoadBalancerSchemeEnumInternal, "intranet"},
		{"empty scheme", "", "intranet"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lb := elbv2types.LoadBalancer{
				LoadBalancerArn: aws.String("arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/test/abc"),
				Scheme:          tt.scheme,
			}
			result := adapter.convertToLBInstance(lb, "us-east-1")
			assert.Equal(t, tt.expected, result.AddressType)
		})
	}
}

func TestConvertToLBInstance_IPVersionMapping(t *testing.T) {
	adapter := newTestLBAdapter()

	tests := []struct {
		name     string
		ipType   elbv2types.IpAddressType
		expected string
	}{
		{"ipv4", elbv2types.IpAddressTypeIpv4, "ipv4"},
		{"dualstack", elbv2types.IpAddressTypeDualstack, "dualstack"},
		{"empty", "", "ipv4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lb := elbv2types.LoadBalancer{
				LoadBalancerArn: aws.String("arn:aws:elasticloadbalancing:us-east-1:123:loadbalancer/app/test/abc"),
				IpAddressType:   tt.ipType,
			}
			result := adapter.convertToLBInstance(lb, "us-east-1")
			assert.Equal(t, tt.expected, result.AddressIPVersion)
		})
	}
}
