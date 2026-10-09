package aws

import (
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/stretchr/testify/assert"
)

func newTestENIAdapter() *ENIAdapter {
	return &ENIAdapter{
		accessKeyID:     "test-ak",
		accessKeySecret: "test-sk",
		defaultRegion:   "us-east-1",
		logger:          elog.DefaultLogger,
	}
}

// ==================== convertToENIInstance ====================

func TestConvertToENIInstance_BasicFields(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := ec2types.NetworkInterface{
		NetworkInterfaceId: aws.String("eni-test-001"),
		Description:        aws.String("web server eni"),
		Status:             ec2types.NetworkInterfaceStatusInUse,
		VpcId:              aws.String("vpc-001"),
		SubnetId:           aws.String("subnet-001"),
		PrivateIpAddress:   aws.String("10.0.0.1"),
		MacAddress:         aws.String("0a:1b:2c:3d:4e:5f"),
		AvailabilityZone:   aws.String("us-east-1a"),
		Attachment: &ec2types.NetworkInterfaceAttachment{
			InstanceId:  aws.String("i-001"),
			DeviceIndex: aws.Int32(0),
		},
	}

	result := adapter.convertToENIInstance(eni, "us-east-1")

	assert.Equal(t, "eni-test-001", result.ENIID)
	assert.Equal(t, "web server eni", result.Description)
	assert.Equal(t, types.ENIStatusInUse, result.Status)
	assert.Equal(t, types.ENITypePrimary, result.Type) // DeviceIndex=0 → Primary
	assert.Equal(t, "us-east-1", result.Region)
	assert.Equal(t, "us-east-1a", result.Zone)
	assert.Equal(t, "vpc-001", result.VPCID)
	assert.Equal(t, "subnet-001", result.SubnetID)
	assert.Equal(t, "10.0.0.1", result.PrimaryPrivateIP)
	assert.Equal(t, "0a:1b:2c:3d:4e:5f", result.MacAddress)
	assert.Equal(t, "i-001", result.InstanceID)
	assert.Equal(t, 0, result.DeviceIndex)
	assert.Equal(t, "aws", result.Provider)
}

func TestConvertToENIInstance_EmptyFields(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := ec2types.NetworkInterface{}
	result := adapter.convertToENIInstance(eni, "us-west-2")

	assert.Empty(t, result.ENIID)
	assert.Equal(t, "us-west-2", result.Region)
	assert.Equal(t, types.ENITypeSecondary, result.Type) // No attachment → Secondary
	assert.Equal(t, "aws", result.Provider)
	assert.NotNil(t, result.Tags)
}

func TestConvertToENIInstance_SecondaryENI(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := ec2types.NetworkInterface{
		NetworkInterfaceId: aws.String("eni-secondary"),
		Status:             ec2types.NetworkInterfaceStatusInUse,
		Attachment: &ec2types.NetworkInterfaceAttachment{
			InstanceId:  aws.String("i-001"),
			DeviceIndex: aws.Int32(1), // DeviceIndex > 0 → Secondary
		},
	}

	result := adapter.convertToENIInstance(eni, "us-east-1")

	assert.Equal(t, types.ENITypeSecondary, result.Type)
	assert.Equal(t, 1, result.DeviceIndex)
	assert.Equal(t, "i-001", result.InstanceID)
}

func TestConvertToENIInstance_WithPrivateIPs(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := ec2types.NetworkInterface{
		NetworkInterfaceId: aws.String("eni-ips"),
		PrivateIpAddress:   aws.String("10.0.0.1"),
		PrivateIpAddresses: []ec2types.NetworkInterfacePrivateIpAddress{
			{PrivateIpAddress: aws.String("10.0.0.1")},
			{PrivateIpAddress: aws.String("10.0.0.2")},
			{PrivateIpAddress: aws.String("10.0.0.3")},
		},
	}

	result := adapter.convertToENIInstance(eni, "us-east-1")

	assert.Equal(t, "10.0.0.1", result.PrimaryPrivateIP)
	assert.Len(t, result.PrivateIPAddresses, 3)
	assert.Contains(t, result.PrivateIPAddresses, "10.0.0.1")
	assert.Contains(t, result.PrivateIPAddresses, "10.0.0.2")
	assert.Contains(t, result.PrivateIPAddresses, "10.0.0.3")
}

func TestConvertToENIInstance_WithIPv6(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := ec2types.NetworkInterface{
		NetworkInterfaceId: aws.String("eni-ipv6"),
		Ipv6Addresses: []ec2types.NetworkInterfaceIpv6Address{
			{Ipv6Address: aws.String("2001:db8::1")},
			{Ipv6Address: aws.String("2001:db8::2")},
		},
	}

	result := adapter.convertToENIInstance(eni, "us-east-1")

	assert.Len(t, result.IPv6Addresses, 2)
	assert.Contains(t, result.IPv6Addresses, "2001:db8::1")
	assert.Contains(t, result.IPv6Addresses, "2001:db8::2")
}

func TestConvertToENIInstance_WithSecurityGroups(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := ec2types.NetworkInterface{
		NetworkInterfaceId: aws.String("eni-sg"),
		Groups: []ec2types.GroupIdentifier{
			{GroupId: aws.String("sg-001"), GroupName: aws.String("web-sg")},
			{GroupId: aws.String("sg-002"), GroupName: aws.String("db-sg")},
		},
	}

	result := adapter.convertToENIInstance(eni, "us-east-1")

	assert.Len(t, result.SecurityGroupIDs, 2)
	assert.Contains(t, result.SecurityGroupIDs, "sg-001")
	assert.Contains(t, result.SecurityGroupIDs, "sg-002")
}

func TestConvertToENIInstance_WithEIP(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := ec2types.NetworkInterface{
		NetworkInterfaceId: aws.String("eni-eip"),
		Association: &ec2types.NetworkInterfaceAssociation{
			PublicIp: aws.String("54.1.2.3"),
		},
	}

	result := adapter.convertToENIInstance(eni, "us-east-1")

	assert.Equal(t, "54.1.2.3", result.PublicIP)
	assert.Len(t, result.EIPAddresses, 1)
	assert.Equal(t, "54.1.2.3", result.EIPAddresses[0])
}

func TestConvertToENIInstance_WithoutEIP(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := ec2types.NetworkInterface{
		NetworkInterfaceId: aws.String("eni-no-eip"),
	}

	result := adapter.convertToENIInstance(eni, "us-east-1")

	assert.Empty(t, result.PublicIP)
	assert.Nil(t, result.EIPAddresses)
}

func TestConvertToENIInstance_WithTags(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := ec2types.NetworkInterface{
		NetworkInterfaceId: aws.String("eni-tags"),
		TagSet: []ec2types.Tag{
			{Key: aws.String("Name"), Value: aws.String("my-eni")},
			{Key: aws.String("env"), Value: aws.String("production")},
			{Key: aws.String("team"), Value: aws.String("platform")},
		},
	}

	result := adapter.convertToENIInstance(eni, "us-east-1")

	assert.Equal(t, "my-eni", result.ENIName) // Name tag → ENIName
	assert.Len(t, result.Tags, 3)
	assert.Equal(t, "my-eni", result.Tags["Name"])
	assert.Equal(t, "production", result.Tags["env"])
	assert.Equal(t, "platform", result.Tags["team"])
}

func TestConvertToENIInstance_StatusMapping(t *testing.T) {
	adapter := newTestENIAdapter()

	tests := []struct {
		name           string
		status         ec2types.NetworkInterfaceStatus
		expectedStatus string
	}{
		{"available", ec2types.NetworkInterfaceStatusAvailable, types.ENIStatusAvailable},
		{"in-use", ec2types.NetworkInterfaceStatusInUse, types.ENIStatusInUse},
		{"attaching", ec2types.NetworkInterfaceStatusAttaching, types.ENIStatusAttaching},
		{"detaching", ec2types.NetworkInterfaceStatusDetaching, types.ENIStatusDetaching},
		{"associated", ec2types.NetworkInterfaceStatusAssociated, types.ENIStatusInUse},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eni := ec2types.NetworkInterface{
				NetworkInterfaceId: aws.String("eni-status"),
				Status:             tt.status,
			}
			result := adapter.convertToENIInstance(eni, "us-east-1")
			assert.Equal(t, tt.expectedStatus, result.Status)
		})
	}
}

func TestConvertToENIInstance_NoAttachment(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := ec2types.NetworkInterface{
		NetworkInterfaceId: aws.String("eni-detached"),
		Status:             ec2types.NetworkInterfaceStatusAvailable,
	}

	result := adapter.convertToENIInstance(eni, "us-east-1")

	assert.Empty(t, result.InstanceID)
	assert.Equal(t, 0, result.DeviceIndex)
	assert.Equal(t, types.ENITypeSecondary, result.Type)
}

func TestConvertToENIInstance_FullPopulation(t *testing.T) {
	adapter := newTestENIAdapter()

	eni := ec2types.NetworkInterface{
		NetworkInterfaceId: aws.String("eni-full-001"),
		Description:        aws.String("fully populated eni"),
		Status:             ec2types.NetworkInterfaceStatusInUse,
		VpcId:              aws.String("vpc-full"),
		SubnetId:           aws.String("subnet-full"),
		PrivateIpAddress:   aws.String("10.1.0.1"),
		MacAddress:         aws.String("0a:ff:ff:ff:ff:ff"),
		AvailabilityZone:   aws.String("us-east-1b"),
		Attachment: &ec2types.NetworkInterfaceAttachment{
			InstanceId:  aws.String("i-full"),
			DeviceIndex: aws.Int32(2),
		},
		PrivateIpAddresses: []ec2types.NetworkInterfacePrivateIpAddress{
			{PrivateIpAddress: aws.String("10.1.0.1")},
			{PrivateIpAddress: aws.String("10.1.0.2")},
		},
		Ipv6Addresses: []ec2types.NetworkInterfaceIpv6Address{
			{Ipv6Address: aws.String("fd00::1")},
		},
		Groups: []ec2types.GroupIdentifier{
			{GroupId: aws.String("sg-full")},
		},
		Association: &ec2types.NetworkInterfaceAssociation{
			PublicIp: aws.String("54.100.200.1"),
		},
		TagSet: []ec2types.Tag{
			{Key: aws.String("Name"), Value: aws.String("full-eni")},
			{Key: aws.String("env"), Value: aws.String("test")},
		},
	}

	result := adapter.convertToENIInstance(eni, "us-east-1")

	assert.Equal(t, "eni-full-001", result.ENIID)
	assert.Equal(t, "full-eni", result.ENIName)
	assert.Equal(t, "fully populated eni", result.Description)
	assert.Equal(t, types.ENIStatusInUse, result.Status)
	assert.Equal(t, types.ENITypeSecondary, result.Type) // DeviceIndex=2 → Secondary
	assert.Equal(t, "us-east-1", result.Region)
	assert.Equal(t, "us-east-1b", result.Zone)
	assert.Equal(t, "vpc-full", result.VPCID)
	assert.Equal(t, "subnet-full", result.SubnetID)
	assert.Equal(t, "10.1.0.1", result.PrimaryPrivateIP)
	assert.Len(t, result.PrivateIPAddresses, 2)
	assert.Equal(t, "0a:ff:ff:ff:ff:ff", result.MacAddress)
	assert.Len(t, result.IPv6Addresses, 1)
	assert.Equal(t, "i-full", result.InstanceID)
	assert.Equal(t, 2, result.DeviceIndex)
	assert.Len(t, result.SecurityGroupIDs, 1)
	assert.Equal(t, "54.100.200.1", result.PublicIP)
	assert.Len(t, result.EIPAddresses, 1)
	assert.Equal(t, "test", result.Tags["env"])
	assert.Equal(t, "aws", result.Provider)
}
