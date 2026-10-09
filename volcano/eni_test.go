package volcano

import (
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/stretchr/testify/assert"
	"github.com/volcengine/volcengine-go-sdk/service/vpc"
	"github.com/volcengine/volcengine-go-sdk/volcengine"
)

func newTestENIAdapter() *ENIAdapter {
	return &ENIAdapter{
		accessKeyID:     "test-ak",
		accessKeySecret: "test-sk",
		defaultRegion:   "cn-beijing",
		logger:          elog.DefaultLogger,
	}
}

// ==================== convertToENIInstance ====================

func TestConvertToENIInstance_BasicFields(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := &vpc.NetworkInterfaceSetForDescribeNetworkInterfacesOutput{
		NetworkInterfaceId:   volcengine.String("eni-test-001"),
		NetworkInterfaceName: volcengine.String("web-eni"),
		Description:          volcengine.String("web server eni"),
		Status:               volcengine.String("InUse"),
		Type:                 volcengine.String("Secondary"),
		ZoneId:               volcengine.String("cn-beijing-a"),
		VpcId:                volcengine.String("vpc-001"),
		SubnetId:             volcengine.String("subnet-001"),
		PrimaryIpAddress:     volcengine.String("10.0.0.1"),
		MacAddress:           volcengine.String("00:16:3e:12:34:56"),
		DeviceId:             volcengine.String("i-001"),
		CreatedAt:            volcengine.String("2024-01-01T00:00:00Z"),
	}

	result := adapter.convertToENIInstance(eni, "cn-beijing")

	assert.Equal(t, "eni-test-001", result.ENIID)
	assert.Equal(t, "web-eni", result.ENIName)
	assert.Equal(t, "web server eni", result.Description)
	assert.Equal(t, types.ENIStatusInUse, result.Status)
	assert.Equal(t, "Secondary", result.Type)
	assert.Equal(t, "cn-beijing", result.Region)
	assert.Equal(t, "cn-beijing-a", result.Zone)
	assert.Equal(t, "vpc-001", result.VPCID)
	assert.Equal(t, "subnet-001", result.SubnetID)
	assert.Equal(t, "10.0.0.1", result.PrimaryPrivateIP)
	assert.Equal(t, "00:16:3e:12:34:56", result.MacAddress)
	assert.Equal(t, "i-001", result.InstanceID)
	assert.Equal(t, "2024-01-01T00:00:00Z", result.CreationTime)
	assert.Equal(t, "volcano", result.Provider)
}

func TestConvertToENIInstance_EmptyFields(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := &vpc.NetworkInterfaceSetForDescribeNetworkInterfacesOutput{}
	result := adapter.convertToENIInstance(eni, "cn-shanghai")

	assert.Empty(t, result.ENIID)
	assert.Empty(t, result.ENIName)
	assert.Equal(t, "cn-shanghai", result.Region)
	assert.Equal(t, types.ENITypeSecondary, result.Type)
	assert.Equal(t, "volcano", result.Provider)
	assert.NotNil(t, result.Tags)
}

func TestConvertToENIInstance_PrimaryType(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := &vpc.NetworkInterfaceSetForDescribeNetworkInterfacesOutput{
		NetworkInterfaceId: volcengine.String("eni-primary"),
		Type:               volcengine.String("Primary"),
		DeviceId:           volcengine.String("i-001"),
	}

	result := adapter.convertToENIInstance(eni, "cn-beijing")

	assert.Equal(t, "Primary", result.Type)
	assert.Equal(t, "i-001", result.InstanceID)
}

func TestConvertToENIInstance_WithPrivateIPs(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := &vpc.NetworkInterfaceSetForDescribeNetworkInterfacesOutput{
		NetworkInterfaceId: volcengine.String("eni-ips"),
		PrimaryIpAddress:   volcengine.String("10.0.0.1"),
		PrivateIpSets: &vpc.PrivateIpSetsForDescribeNetworkInterfacesOutput{
			PrivateIpSet: []*vpc.PrivateIpSetForDescribeNetworkInterfacesOutput{
				{PrivateIpAddress: volcengine.String("10.0.0.1")},
				{PrivateIpAddress: volcengine.String("10.0.0.2")},
				{PrivateIpAddress: volcengine.String("10.0.0.3")},
			},
		},
	}

	result := adapter.convertToENIInstance(eni, "cn-beijing")

	assert.Equal(t, "10.0.0.1", result.PrimaryPrivateIP)
	assert.Len(t, result.PrivateIPAddresses, 3)
	assert.Contains(t, result.PrivateIPAddresses, "10.0.0.1")
	assert.Contains(t, result.PrivateIPAddresses, "10.0.0.2")
	assert.Contains(t, result.PrivateIPAddresses, "10.0.0.3")
}

func TestConvertToENIInstance_WithIPv6(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := &vpc.NetworkInterfaceSetForDescribeNetworkInterfacesOutput{
		NetworkInterfaceId: volcengine.String("eni-ipv6"),
		IPv6Sets:           []*string{volcengine.String("2001:db8::1"), volcengine.String("2001:db8::2")},
	}

	result := adapter.convertToENIInstance(eni, "cn-beijing")

	assert.Len(t, result.IPv6Addresses, 2)
	assert.Contains(t, result.IPv6Addresses, "2001:db8::1")
	assert.Contains(t, result.IPv6Addresses, "2001:db8::2")
}

func TestConvertToENIInstance_WithSecurityGroups(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := &vpc.NetworkInterfaceSetForDescribeNetworkInterfacesOutput{
		NetworkInterfaceId: volcengine.String("eni-sg"),
		SecurityGroupIds:   []*string{volcengine.String("sg-001"), volcengine.String("sg-002")},
	}

	result := adapter.convertToENIInstance(eni, "cn-beijing")

	assert.Len(t, result.SecurityGroupIDs, 2)
	assert.Contains(t, result.SecurityGroupIDs, "sg-001")
	assert.Contains(t, result.SecurityGroupIDs, "sg-002")
}

func TestConvertToENIInstance_WithEIP(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := &vpc.NetworkInterfaceSetForDescribeNetworkInterfacesOutput{
		NetworkInterfaceId: volcengine.String("eni-eip"),
		AssociatedElasticIp: &vpc.AssociatedElasticIpForDescribeNetworkInterfacesOutput{
			EipAddress: volcengine.String("1.2.3.4"),
		},
	}

	result := adapter.convertToENIInstance(eni, "cn-beijing")

	assert.Equal(t, "1.2.3.4", result.PublicIP)
	assert.Len(t, result.EIPAddresses, 1)
	assert.Equal(t, "1.2.3.4", result.EIPAddresses[0])
}

func TestConvertToENIInstance_WithoutEIP(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := &vpc.NetworkInterfaceSetForDescribeNetworkInterfacesOutput{
		NetworkInterfaceId: volcengine.String("eni-no-eip"),
	}

	result := adapter.convertToENIInstance(eni, "cn-beijing")

	assert.Empty(t, result.PublicIP)
	assert.Nil(t, result.EIPAddresses)
}

func TestConvertToENIInstance_WithTags(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := &vpc.NetworkInterfaceSetForDescribeNetworkInterfacesOutput{
		NetworkInterfaceId: volcengine.String("eni-tags"),
		Tags: []*vpc.TagForDescribeNetworkInterfacesOutput{
			{Key: volcengine.String("env"), Value: volcengine.String("production")},
			{Key: volcengine.String("team"), Value: volcengine.String("platform")},
		},
	}

	result := adapter.convertToENIInstance(eni, "cn-beijing")

	assert.Len(t, result.Tags, 2)
	assert.Equal(t, "production", result.Tags["env"])
	assert.Equal(t, "platform", result.Tags["team"])
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
			eni := &vpc.NetworkInterfaceSetForDescribeNetworkInterfacesOutput{
				NetworkInterfaceId: volcengine.String("eni-status"),
				Status:             volcengine.String(tt.status),
			}
			result := adapter.convertToENIInstance(eni, "cn-beijing")
			assert.Equal(t, tt.expectedStatus, result.Status)
		})
	}
}

func TestConvertToENIInstance_FullPopulation(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := &vpc.NetworkInterfaceSetForDescribeNetworkInterfacesOutput{
		NetworkInterfaceId:   volcengine.String("eni-full-001"),
		NetworkInterfaceName: volcengine.String("full-eni"),
		Description:          volcengine.String("fully populated"),
		Status:               volcengine.String("Available"),
		Type:                 volcengine.String("Secondary"),
		ZoneId:               volcengine.String("cn-beijing-b"),
		VpcId:                volcengine.String("vpc-full"),
		SubnetId:             volcengine.String("subnet-full"),
		PrimaryIpAddress:     volcengine.String("10.1.0.1"),
		MacAddress:           volcengine.String("00:16:3e:ff:ff:ff"),
		DeviceId:             volcengine.String(""),
		CreatedAt:            volcengine.String("2024-06-15T10:30:00Z"),
		PrivateIpSets: &vpc.PrivateIpSetsForDescribeNetworkInterfacesOutput{
			PrivateIpSet: []*vpc.PrivateIpSetForDescribeNetworkInterfacesOutput{
				{PrivateIpAddress: volcengine.String("10.1.0.1")},
				{PrivateIpAddress: volcengine.String("10.1.0.2")},
			},
		},
		IPv6Sets:         []*string{volcengine.String("fd00::1")},
		SecurityGroupIds: []*string{volcengine.String("sg-full")},
		Tags: []*vpc.TagForDescribeNetworkInterfacesOutput{
			{Key: volcengine.String("env"), Value: volcengine.String("test")},
		},
	}

	result := adapter.convertToENIInstance(eni, "cn-beijing")

	assert.Equal(t, "eni-full-001", result.ENIID)
	assert.Equal(t, "full-eni", result.ENIName)
	assert.Equal(t, "fully populated", result.Description)
	assert.Equal(t, types.ENIStatusAvailable, result.Status)
	assert.Equal(t, "Secondary", result.Type)
	assert.Equal(t, "cn-beijing", result.Region)
	assert.Equal(t, "cn-beijing-b", result.Zone)
	assert.Equal(t, "vpc-full", result.VPCID)
	assert.Equal(t, "subnet-full", result.SubnetID)
	assert.Equal(t, "10.1.0.1", result.PrimaryPrivateIP)
	assert.Len(t, result.PrivateIPAddresses, 2)
	assert.Equal(t, "00:16:3e:ff:ff:ff", result.MacAddress)
	assert.Len(t, result.IPv6Addresses, 1)
	assert.Empty(t, result.InstanceID)
	assert.Len(t, result.SecurityGroupIDs, 1)
	assert.Equal(t, "2024-06-15T10:30:00Z", result.CreationTime)
	assert.Equal(t, "test", result.Tags["env"])
	assert.Equal(t, "volcano", result.Provider)
}
