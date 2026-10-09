package tencent

import (
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/stretchr/testify/assert"
	vpc "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/vpc/v20170312"
)

func newTestENIAdapter() *ENIAdapter {
	return &ENIAdapter{
		accessKeyID:     "test-ak",
		accessKeySecret: "test-sk",
		defaultRegion:   "ap-guangzhou",
		logger:          elog.DefaultLogger,
	}
}

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

// ==================== convertToENIInstance ====================

func TestConvertToENIInstance_BasicFields(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := &vpc.NetworkInterface{
		NetworkInterfaceId:          strPtr("eni-test-001"),
		NetworkInterfaceName:        strPtr("web-eni"),
		NetworkInterfaceDescription: strPtr("web server eni"),
		State:                       strPtr("AVAILABLE"),
		Primary:                     boolPtr(false),
		VpcId:                       strPtr("vpc-001"),
		SubnetId:                    strPtr("subnet-001"),
		Zone:                        strPtr("ap-guangzhou-3"),
		MacAddress:                  strPtr("20:90:6f:12:34:56"),
		CreatedTime:                 strPtr("2024-01-01T00:00:00Z"),
	}

	result := adapter.convertToENIInstance(eni, "ap-guangzhou")

	assert.Equal(t, "eni-test-001", result.ENIID)
	assert.Equal(t, "web-eni", result.ENIName)
	assert.Equal(t, "web server eni", result.Description)
	assert.Equal(t, types.ENIStatusAvailable, result.Status)
	assert.Equal(t, types.ENITypeSecondary, result.Type)
	assert.Equal(t, "ap-guangzhou", result.Region)
	assert.Equal(t, "ap-guangzhou-3", result.Zone)
	assert.Equal(t, "vpc-001", result.VPCID)
	assert.Equal(t, "subnet-001", result.SubnetID)
	assert.Equal(t, "20:90:6f:12:34:56", result.MacAddress)
	assert.Equal(t, "2024-01-01T00:00:00Z", result.CreationTime)
	assert.Equal(t, "tencent", result.Provider)
}

func TestConvertToENIInstance_EmptyFields(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := &vpc.NetworkInterface{}
	result := adapter.convertToENIInstance(eni, "ap-shanghai")

	assert.Empty(t, result.ENIID)
	assert.Empty(t, result.ENIName)
	assert.Equal(t, "ap-shanghai", result.Region)
	assert.Equal(t, types.ENITypeSecondary, result.Type)
	assert.Equal(t, "tencent", result.Provider)
	assert.NotNil(t, result.Tags)
}

func TestConvertToENIInstance_PrimaryENI(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := &vpc.NetworkInterface{
		NetworkInterfaceId: strPtr("eni-primary"),
		Primary:            boolPtr(true),
		Attachment: &vpc.NetworkInterfaceAttachment{
			InstanceId:  strPtr("ins-001"),
			DeviceIndex: uint64Ptr(0),
		},
	}

	result := adapter.convertToENIInstance(eni, "ap-guangzhou")

	assert.Equal(t, types.ENITypePrimary, result.Type)
	assert.Equal(t, "ins-001", result.InstanceID)
	assert.Equal(t, 0, result.DeviceIndex)
}

func TestConvertToENIInstance_SecondaryENI(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := &vpc.NetworkInterface{
		NetworkInterfaceId: strPtr("eni-secondary"),
		Primary:            boolPtr(false),
		Attachment: &vpc.NetworkInterfaceAttachment{
			InstanceId:  strPtr("ins-001"),
			DeviceIndex: uint64Ptr(1),
		},
	}

	result := adapter.convertToENIInstance(eni, "ap-guangzhou")

	assert.Equal(t, types.ENITypeSecondary, result.Type)
	assert.Equal(t, "ins-001", result.InstanceID)
	assert.Equal(t, 1, result.DeviceIndex)
}

func TestConvertToENIInstance_WithPrivateIPs(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := &vpc.NetworkInterface{
		NetworkInterfaceId: strPtr("eni-ips"),
		PrivateIpAddressSet: []*vpc.PrivateIpAddressSpecification{
			{PrivateIpAddress: strPtr("10.0.0.1"), Primary: boolPtr(true)},
			{PrivateIpAddress: strPtr("10.0.0.2"), Primary: boolPtr(false)},
			{PrivateIpAddress: strPtr("10.0.0.3"), Primary: boolPtr(false)},
		},
	}

	result := adapter.convertToENIInstance(eni, "ap-guangzhou")

	assert.Equal(t, "10.0.0.1", result.PrimaryPrivateIP)
	assert.Len(t, result.PrivateIPAddresses, 3)
	assert.Contains(t, result.PrivateIPAddresses, "10.0.0.1")
	assert.Contains(t, result.PrivateIPAddresses, "10.0.0.2")
	assert.Contains(t, result.PrivateIPAddresses, "10.0.0.3")
}

func TestConvertToENIInstance_WithIPv6(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := &vpc.NetworkInterface{
		NetworkInterfaceId: strPtr("eni-ipv6"),
		Ipv6AddressSet: []*vpc.Ipv6Address{
			{Address: strPtr("2001:db8::1")},
			{Address: strPtr("2001:db8::2")},
		},
	}

	result := adapter.convertToENIInstance(eni, "ap-guangzhou")

	assert.Len(t, result.IPv6Addresses, 2)
	assert.Contains(t, result.IPv6Addresses, "2001:db8::1")
	assert.Contains(t, result.IPv6Addresses, "2001:db8::2")
}

func TestConvertToENIInstance_WithSecurityGroups(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := &vpc.NetworkInterface{
		NetworkInterfaceId: strPtr("eni-sg"),
		GroupSet:           []*string{strPtr("sg-001"), strPtr("sg-002")},
	}

	result := adapter.convertToENIInstance(eni, "ap-guangzhou")

	assert.Len(t, result.SecurityGroupIDs, 2)
	assert.Contains(t, result.SecurityGroupIDs, "sg-001")
	assert.Contains(t, result.SecurityGroupIDs, "sg-002")
}

func TestConvertToENIInstance_WithEIP(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := &vpc.NetworkInterface{
		NetworkInterfaceId: strPtr("eni-eip"),
		PrivateIpAddressSet: []*vpc.PrivateIpAddressSpecification{
			{
				PrivateIpAddress: strPtr("10.0.0.1"),
				Primary:          boolPtr(true),
				PublicIpAddress:  strPtr("1.2.3.4"),
			},
		},
	}

	result := adapter.convertToENIInstance(eni, "ap-guangzhou")

	assert.Equal(t, "1.2.3.4", result.PublicIP)
	assert.Len(t, result.EIPAddresses, 1)
	assert.Equal(t, "1.2.3.4", result.EIPAddresses[0])
}

func TestConvertToENIInstance_WithTags(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := &vpc.NetworkInterface{
		NetworkInterfaceId: strPtr("eni-tags"),
		TagSet: []*vpc.Tag{
			{Key: strPtr("env"), Value: strPtr("production")},
			{Key: strPtr("team"), Value: strPtr("platform")},
		},
	}

	result := adapter.convertToENIInstance(eni, "ap-guangzhou")

	assert.Len(t, result.Tags, 2)
	assert.Equal(t, "production", result.Tags["env"])
	assert.Equal(t, "platform", result.Tags["team"])
}

func TestConvertToENIInstance_StatusMapping(t *testing.T) {
	adapter := newTestENIAdapter()

	tests := []struct {
		name           string
		state          string
		expectedStatus string
	}{
		{"AVAILABLE", "AVAILABLE", types.ENIStatusAvailable},
		{"PENDING", "PENDING", types.ENIStatusCreating},
		{"DELETING", "DELETING", types.ENIStatusDeleting},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eni := &vpc.NetworkInterface{
				NetworkInterfaceId: strPtr("eni-status"),
				State:              strPtr(tt.state),
			}
			result := adapter.convertToENIInstance(eni, "ap-guangzhou")
			assert.Equal(t, tt.expectedStatus, result.Status)
		})
	}
}

func TestConvertToENIInstance_NoAttachment(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := &vpc.NetworkInterface{
		NetworkInterfaceId: strPtr("eni-detached"),
		State:              strPtr("AVAILABLE"),
	}

	result := adapter.convertToENIInstance(eni, "ap-guangzhou")

	assert.Empty(t, result.InstanceID)
	assert.Equal(t, 0, result.DeviceIndex)
}

func TestConvertToENIInstance_FullPopulation(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := &vpc.NetworkInterface{
		NetworkInterfaceId:          strPtr("eni-full-001"),
		NetworkInterfaceName:        strPtr("full-eni"),
		NetworkInterfaceDescription: strPtr("fully populated"),
		State:                       strPtr("AVAILABLE"),
		Primary:                     boolPtr(false),
		VpcId:                       strPtr("vpc-full"),
		SubnetId:                    strPtr("subnet-full"),
		Zone:                        strPtr("ap-guangzhou-4"),
		MacAddress:                  strPtr("20:90:6f:ff:ff:ff"),
		CreatedTime:                 strPtr("2024-06-15T10:30:00Z"),
		Attachment: &vpc.NetworkInterfaceAttachment{
			InstanceId:  strPtr("ins-full"),
			DeviceIndex: uint64Ptr(2),
		},
		PrivateIpAddressSet: []*vpc.PrivateIpAddressSpecification{
			{PrivateIpAddress: strPtr("10.1.0.1"), Primary: boolPtr(true), PublicIpAddress: strPtr("1.2.3.4")},
			{PrivateIpAddress: strPtr("10.1.0.2"), Primary: boolPtr(false)},
		},
		Ipv6AddressSet: []*vpc.Ipv6Address{
			{Address: strPtr("fd00::1")},
		},
		GroupSet: []*string{strPtr("sg-full")},
		TagSet: []*vpc.Tag{
			{Key: strPtr("env"), Value: strPtr("test")},
		},
	}

	result := adapter.convertToENIInstance(eni, "ap-guangzhou")

	assert.Equal(t, "eni-full-001", result.ENIID)
	assert.Equal(t, "full-eni", result.ENIName)
	assert.Equal(t, "fully populated", result.Description)
	assert.Equal(t, types.ENIStatusAvailable, result.Status)
	assert.Equal(t, types.ENITypeSecondary, result.Type)
	assert.Equal(t, "ap-guangzhou", result.Region)
	assert.Equal(t, "ap-guangzhou-4", result.Zone)
	assert.Equal(t, "vpc-full", result.VPCID)
	assert.Equal(t, "subnet-full", result.SubnetID)
	assert.Equal(t, "10.1.0.1", result.PrimaryPrivateIP)
	assert.Len(t, result.PrivateIPAddresses, 2)
	assert.Equal(t, "20:90:6f:ff:ff:ff", result.MacAddress)
	assert.Len(t, result.IPv6Addresses, 1)
	assert.Equal(t, "ins-full", result.InstanceID)
	assert.Equal(t, 2, result.DeviceIndex)
	assert.Len(t, result.SecurityGroupIDs, 1)
	assert.Equal(t, "1.2.3.4", result.PublicIP)
	assert.Len(t, result.EIPAddresses, 1)
	assert.Equal(t, "2024-06-15T10:30:00Z", result.CreationTime)
	assert.Equal(t, "test", result.Tags["env"])
	assert.Equal(t, "tencent", result.Provider)
}
