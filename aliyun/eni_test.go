package aliyun

import (
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/gotomicro/ego/core/elog"
	"github.com/stretchr/testify/assert"
)

func newTestENIAdapter() *ENIAdapter {
	return &ENIAdapter{
		accessKeyID:     "test-ak",
		accessKeySecret: "test-sk",
		defaultRegion:   "cn-hangzhou",
		logger:          elog.DefaultLogger,
	}
}

// ==================== convertToENIInstance ====================

func TestConvertToENIInstance_BasicFields(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := ecs.NetworkInterfaceSet{
		NetworkInterfaceId:   "eni-test-001",
		NetworkInterfaceName: "web-eni",
		Description:          "web server eni",
		Status:               "InUse",
		Type:                 "Secondary",
		ZoneId:               "cn-hangzhou-a",
		VpcId:                "vpc-001",
		VSwitchId:            "vsw-001",
		PrivateIpAddress:     "10.0.0.1",
		MacAddress:           "00:16:3e:12:34:56",
		InstanceId:           "i-001",
		ResourceGroupId:      "rg-001",
		CreationTime:         "2024-01-01T00:00:00Z",
	}

	result := adapter.convertToENIInstance(eni, "cn-hangzhou")

	assert.Equal(t, "eni-test-001", result.ENIID)
	assert.Equal(t, "web-eni", result.ENIName)
	assert.Equal(t, "web server eni", result.Description)
	assert.Equal(t, types.ENIStatusInUse, result.Status)
	assert.Equal(t, "Secondary", result.Type)
	assert.Equal(t, "cn-hangzhou", result.Region)
	assert.Equal(t, "cn-hangzhou-a", result.Zone)
	assert.Equal(t, "vpc-001", result.VPCID)
	assert.Equal(t, "vsw-001", result.SubnetID)
	assert.Equal(t, "10.0.0.1", result.PrimaryPrivateIP)
	assert.Equal(t, "00:16:3e:12:34:56", result.MacAddress)
	assert.Equal(t, "i-001", result.InstanceID)
	assert.Equal(t, "rg-001", result.ResourceGroupID)
	assert.Equal(t, "2024-01-01T00:00:00Z", result.CreationTime)
	assert.Equal(t, "aliyun", result.Provider)
}

func TestConvertToENIInstance_EmptyFields(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := ecs.NetworkInterfaceSet{}
	result := adapter.convertToENIInstance(eni, "cn-shanghai")

	assert.Empty(t, result.ENIID)
	assert.Empty(t, result.ENIName)
	assert.Equal(t, "cn-shanghai", result.Region)
	assert.Equal(t, "aliyun", result.Provider)
	assert.NotNil(t, result.Tags)
	assert.Empty(t, result.Tags)
}

func TestConvertToENIInstance_WithPrivateIPs(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := ecs.NetworkInterfaceSet{
		NetworkInterfaceId: "eni-ip-test",
		PrivateIpAddress:   "10.0.0.1",
		PrivateIpSets: ecs.PrivateIpSetsInDescribeNetworkInterfaces{
			PrivateIpSet: []ecs.PrivateIpSet{
				{PrivateIpAddress: "10.0.0.1"},
				{PrivateIpAddress: "10.0.0.2"},
				{PrivateIpAddress: "10.0.0.3"},
			},
		},
	}

	result := adapter.convertToENIInstance(eni, "cn-hangzhou")

	assert.Equal(t, "10.0.0.1", result.PrimaryPrivateIP)
	assert.Len(t, result.PrivateIPAddresses, 3)
	assert.Contains(t, result.PrivateIPAddresses, "10.0.0.1")
	assert.Contains(t, result.PrivateIPAddresses, "10.0.0.2")
	assert.Contains(t, result.PrivateIPAddresses, "10.0.0.3")
}

func TestConvertToENIInstance_WithIPv6(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := ecs.NetworkInterfaceSet{
		NetworkInterfaceId: "eni-ipv6-test",
		Ipv6Sets: ecs.Ipv6SetsInDescribeNetworkInterfaces{
			Ipv6Set: []ecs.Ipv6Set{
				{Ipv6Address: "2001:db8::1"},
				{Ipv6Address: "2001:db8::2"},
			},
		},
	}

	result := adapter.convertToENIInstance(eni, "cn-hangzhou")

	assert.Len(t, result.IPv6Addresses, 2)
	assert.Contains(t, result.IPv6Addresses, "2001:db8::1")
	assert.Contains(t, result.IPv6Addresses, "2001:db8::2")
}

func TestConvertToENIInstance_WithSecurityGroups(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := ecs.NetworkInterfaceSet{
		NetworkInterfaceId: "eni-sg-test",
		SecurityGroupIds: ecs.SecurityGroupIdsInDescribeNetworkInterfaces{
			SecurityGroupId: []string{"sg-001", "sg-002", "sg-003"},
		},
	}

	result := adapter.convertToENIInstance(eni, "cn-hangzhou")

	assert.Len(t, result.SecurityGroupIDs, 3)
	assert.Contains(t, result.SecurityGroupIDs, "sg-001")
	assert.Contains(t, result.SecurityGroupIDs, "sg-002")
	assert.Contains(t, result.SecurityGroupIDs, "sg-003")
}

func TestConvertToENIInstance_WithTags(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := ecs.NetworkInterfaceSet{
		NetworkInterfaceId: "eni-tag-test",
		Tags: ecs.TagsInDescribeNetworkInterfaces{
			Tag: []ecs.Tag{
				{TagKey: "env", TagValue: "production"},
				{TagKey: "team", TagValue: "platform"},
				{TagKey: "app", TagValue: "web"},
			},
		},
	}

	result := adapter.convertToENIInstance(eni, "cn-hangzhou")

	assert.Len(t, result.Tags, 3)
	assert.Equal(t, "production", result.Tags["env"])
	assert.Equal(t, "platform", result.Tags["team"])
	assert.Equal(t, "web", result.Tags["app"])
}

func TestConvertToENIInstance_WithEIP(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := ecs.NetworkInterfaceSet{
		NetworkInterfaceId: "eni-eip-test",
		AssociatedPublicIp: ecs.AssociatedPublicIp{
			PublicIpAddress: "1.2.3.4",
		},
	}

	result := adapter.convertToENIInstance(eni, "cn-hangzhou")

	assert.Equal(t, "1.2.3.4", result.PublicIP)
	assert.Len(t, result.EIPAddresses, 1)
	assert.Equal(t, "1.2.3.4", result.EIPAddresses[0])
}

func TestConvertToENIInstance_WithoutEIP(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := ecs.NetworkInterfaceSet{
		NetworkInterfaceId: "eni-no-eip",
		AssociatedPublicIp: ecs.AssociatedPublicIp{},
	}

	result := adapter.convertToENIInstance(eni, "cn-hangzhou")

	assert.Empty(t, result.PublicIP)
	assert.Nil(t, result.EIPAddresses)
}

func TestConvertToENIInstance_StatusMapping(t *testing.T) {
	adapter := newTestENIAdapter()

	tests := []struct {
		name           string
		status         string
		expectedStatus string
	}{
		{"Available", "Available", types.ENIStatusAvailable},
		{"InUse", "InUse", types.ENIStatusInUse},
		{"Attaching", "Attaching", types.ENIStatusAttaching},
		{"Detaching", "Detaching", types.ENIStatusDetaching},
		{"Creating", "Creating", types.ENIStatusCreating},
		{"Deleting", "Deleting", types.ENIStatusDeleting},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eni := ecs.NetworkInterfaceSet{
				NetworkInterfaceId: "eni-status-test",
				Status:             tt.status,
			}
			result := adapter.convertToENIInstance(eni, "cn-hangzhou")
			assert.Equal(t, tt.expectedStatus, result.Status)
		})
	}
}

func TestConvertToENIInstance_PrimaryType(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := ecs.NetworkInterfaceSet{
		NetworkInterfaceId: "eni-primary",
		Type:               "Primary",
		InstanceId:         "i-001",
	}

	result := adapter.convertToENIInstance(eni, "cn-hangzhou")

	assert.Equal(t, "Primary", result.Type)
	assert.Equal(t, "i-001", result.InstanceID)
}

func TestConvertToENIInstance_FullPopulation(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := ecs.NetworkInterfaceSet{
		NetworkInterfaceId:   "eni-full-001",
		NetworkInterfaceName: "full-eni",
		Description:          "fully populated eni",
		Status:               "Available",
		Type:                 "Secondary",
		ZoneId:               "cn-hangzhou-b",
		VpcId:                "vpc-full",
		VSwitchId:            "vsw-full",
		PrivateIpAddress:     "10.1.0.1",
		MacAddress:           "00:16:3e:ab:cd:ef",
		InstanceId:           "",
		ResourceGroupId:      "rg-full",
		CreationTime:         "2024-06-15T10:30:00Z",
		PrivateIpSets: ecs.PrivateIpSetsInDescribeNetworkInterfaces{
			PrivateIpSet: []ecs.PrivateIpSet{
				{PrivateIpAddress: "10.1.0.1"},
				{PrivateIpAddress: "10.1.0.2"},
			},
		},
		Ipv6Sets: ecs.Ipv6SetsInDescribeNetworkInterfaces{
			Ipv6Set: []ecs.Ipv6Set{
				{Ipv6Address: "fd00::1"},
			},
		},
		SecurityGroupIds: ecs.SecurityGroupIdsInDescribeNetworkInterfaces{
			SecurityGroupId: []string{"sg-full-001"},
		},
		Tags: ecs.TagsInDescribeNetworkInterfaces{
			Tag: []ecs.Tag{
				{TagKey: "Name", TagValue: "full-eni"},
			},
		},
	}

	result := adapter.convertToENIInstance(eni, "cn-hangzhou")

	assert.Equal(t, "eni-full-001", result.ENIID)
	assert.Equal(t, "full-eni", result.ENIName)
	assert.Equal(t, "fully populated eni", result.Description)
	assert.Equal(t, types.ENIStatusAvailable, result.Status)
	assert.Equal(t, "Secondary", result.Type)
	assert.Equal(t, "cn-hangzhou", result.Region)
	assert.Equal(t, "cn-hangzhou-b", result.Zone)
	assert.Equal(t, "vpc-full", result.VPCID)
	assert.Equal(t, "vsw-full", result.SubnetID)
	assert.Equal(t, "10.1.0.1", result.PrimaryPrivateIP)
	assert.Len(t, result.PrivateIPAddresses, 2)
	assert.Equal(t, "00:16:3e:ab:cd:ef", result.MacAddress)
	assert.Len(t, result.IPv6Addresses, 1)
	assert.Empty(t, result.InstanceID)
	assert.Len(t, result.SecurityGroupIDs, 1)
	assert.Equal(t, "rg-full", result.ResourceGroupID)
	assert.Equal(t, "2024-06-15T10:30:00Z", result.CreationTime)
	assert.Equal(t, "full-eni", result.Tags["Name"])
	assert.Equal(t, "aliyun", result.Provider)
}
