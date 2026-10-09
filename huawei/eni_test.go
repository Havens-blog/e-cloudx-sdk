package huawei

import (
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/vpc/v2/model"
	"github.com/stretchr/testify/assert"
)

func newTestENIAdapter() *ENIAdapter {
	return &ENIAdapter{
		accessKeyID:     "test-ak",
		accessKeySecret: "test-sk",
		defaultRegion:   "cn-north-4",
		logger:          elog.DefaultLogger,
	}
}

// ==================== convertToENIInstance ====================

func TestConvertToENIInstance_BasicFields(t *testing.T) {
	adapter := newTestENIAdapter()

	ipAddr := "10.0.0.1"
	subnetID := "subnet-001"
	port := model.Port{
		Id:         "port-test-001",
		Name:       "web-port",
		Status:     model.GetPortStatusEnum().ACTIVE,
		MacAddress: "fa:16:3e:12:34:56",
		DeviceId:   "server-001",
		ZoneId:     "cn-north-4a",
		TenantId:   "tenant-001",
		FixedIps: []model.FixedIp{
			{IpAddress: &ipAddr, SubnetId: &subnetID},
		},
		SecurityGroups: []string{"sg-001", "sg-002"},
	}

	result := adapter.convertToENIInstance(port, "cn-north-4")

	assert.Equal(t, "port-test-001", result.ENIID)
	assert.Equal(t, "web-port", result.ENIName)
	assert.Equal(t, types.ENIStatusInUse, result.Status) // ACTIVE → in_use
	assert.Equal(t, types.ENITypeSecondary, result.Type) // 无 binding:vif_details 时默认辅助网卡
	assert.Equal(t, "cn-north-4", result.Region)
	assert.Equal(t, "cn-north-4a", result.Zone)     // 可用区取自 port.zone_id
	assert.Equal(t, "tenant-001", result.ProjectID) // 项目ID取自 tenant_id
	assert.Equal(t, "subnet-001", result.SubnetID)
	assert.Equal(t, "10.0.0.1", result.PrimaryPrivateIP)
	assert.Equal(t, "fa:16:3e:12:34:56", result.MacAddress)
	assert.Equal(t, "server-001", result.InstanceID)
	assert.Len(t, result.SecurityGroupIDs, 2)
	assert.Contains(t, result.SecurityGroupIDs, "sg-001")
	assert.Contains(t, result.SecurityGroupIDs, "sg-002")
	assert.Equal(t, "huawei", result.Provider)
}

func TestConvertToENIInstance_EmptyFields(t *testing.T) {
	adapter := newTestENIAdapter()

	port := model.Port{
		Id:     "port-empty",
		Status: model.GetPortStatusEnum().DOWN,
	}

	result := adapter.convertToENIInstance(port, "cn-east-3")

	assert.Equal(t, "port-empty", result.ENIID)
	assert.Empty(t, result.ENIName)
	assert.Equal(t, types.ENIStatusAvailable, result.Status) // DOWN → available
	assert.Equal(t, "cn-east-3", result.Region)
	assert.Empty(t, result.Zone)
	assert.Empty(t, result.ProjectID)
	assert.Empty(t, result.PrimaryPrivateIP)
	assert.Empty(t, result.SubnetID)
	assert.Empty(t, result.InstanceID)
	assert.NotNil(t, result.Tags)
	assert.Equal(t, "huawei", result.Provider)
}

func TestConvertToENIInstance_WithMultipleIPs(t *testing.T) {
	adapter := newTestENIAdapter()

	ip1 := "10.0.0.1"
	ip2 := "10.0.0.2"
	subnet1 := "subnet-001"
	subnet2 := "subnet-001"
	port := model.Port{
		Id: "port-multi-ip",
		FixedIps: []model.FixedIp{
			{IpAddress: &ip1, SubnetId: &subnet1},
			{IpAddress: &ip2, SubnetId: &subnet2},
		},
	}

	result := adapter.convertToENIInstance(port, "cn-north-4")

	assert.Equal(t, "10.0.0.1", result.PrimaryPrivateIP) // 第一个IP作为主IP
	assert.Len(t, result.PrivateIPAddresses, 2)
	assert.Contains(t, result.PrivateIPAddresses, "10.0.0.1")
	assert.Contains(t, result.PrivateIPAddresses, "10.0.0.2")
}

func TestConvertToENIInstance_StatusMapping(t *testing.T) {
	adapter := newTestENIAdapter()

	tests := []struct {
		name           string
		status         model.PortStatus
		expectedStatus string
	}{
		{"ACTIVE", model.GetPortStatusEnum().ACTIVE, types.ENIStatusInUse},
		{"BUILD", model.GetPortStatusEnum().BUILD, types.ENIStatusCreating},
		{"DOWN", model.GetPortStatusEnum().DOWN, types.ENIStatusAvailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port := model.Port{
				Id:     "port-status",
				Status: tt.status,
			}
			result := adapter.convertToENIInstance(port, "cn-north-4")
			assert.Equal(t, tt.expectedStatus, result.Status)
		})
	}
}

func TestConvertToENIInstance_WithSecurityGroups(t *testing.T) {
	adapter := newTestENIAdapter()

	port := model.Port{
		Id:             "port-sg",
		SecurityGroups: []string{"sg-001", "sg-002", "sg-003"},
	}

	result := adapter.convertToENIInstance(port, "cn-north-4")

	assert.Len(t, result.SecurityGroupIDs, 3)
	assert.Contains(t, result.SecurityGroupIDs, "sg-001")
	assert.Contains(t, result.SecurityGroupIDs, "sg-002")
	assert.Contains(t, result.SecurityGroupIDs, "sg-003")
}

func TestConvertToENIInstance_EmptySecurityGroups(t *testing.T) {
	adapter := newTestENIAdapter()

	port := model.Port{
		Id: "port-no-sg",
	}

	result := adapter.convertToENIInstance(port, "cn-north-4")

	assert.Nil(t, result.SecurityGroupIDs)
}

func TestConvertToENIInstance_NilIPAddress(t *testing.T) {
	adapter := newTestENIAdapter()

	subnetID := "subnet-001"
	port := model.Port{
		Id: "port-nil-ip",
		FixedIps: []model.FixedIp{
			{IpAddress: nil, SubnetId: &subnetID},
		},
	}

	result := adapter.convertToENIInstance(port, "cn-north-4")

	assert.Empty(t, result.PrimaryPrivateIP)
	assert.Empty(t, result.PrivateIPAddresses)
	assert.Equal(t, "subnet-001", result.SubnetID)
}

func TestConvertToENIInstance_TagsAlwaysInitialized(t *testing.T) {
	adapter := newTestENIAdapter()

	port := model.Port{Id: "port-tags"}
	result := adapter.convertToENIInstance(port, "cn-north-4")

	assert.NotNil(t, result.Tags)
	assert.Empty(t, result.Tags)
}

func TestConvertToENIInstance_FullPopulation(t *testing.T) {
	adapter := newTestENIAdapter()

	ip1 := "10.1.0.1"
	ip2 := "10.1.0.2"
	subnet := "subnet-full"
	primaryTrue := true
	port := model.Port{
		Id:         "port-full-001",
		Name:       "full-port",
		Status:     model.GetPortStatusEnum().ACTIVE,
		MacAddress: "fa:16:3e:ff:ff:ff",
		DeviceId:   "server-full",
		ZoneId:     "cn-north-4b",
		TenantId:   "tenant-full",
		FixedIps: []model.FixedIp{
			{IpAddress: &ip1, SubnetId: &subnet},
			{IpAddress: &ip2, SubnetId: &subnet},
		},
		SecurityGroups:    []string{"sg-full-001", "sg-full-002"},
		BindingvifDetails: &model.BindingVifDetails{PrimaryInterface: &primaryTrue},
	}

	result := adapter.convertToENIInstance(port, "cn-north-4")

	assert.Equal(t, "port-full-001", result.ENIID)
	assert.Equal(t, "full-port", result.ENIName)
	assert.Equal(t, types.ENIStatusInUse, result.Status)
	assert.Equal(t, types.ENITypePrimary, result.Type) // primary_interface=true → 主网卡
	assert.Equal(t, "cn-north-4", result.Region)
	assert.Equal(t, "cn-north-4b", result.Zone)
	assert.Equal(t, "tenant-full", result.ProjectID)
	assert.Equal(t, "subnet-full", result.SubnetID)
	assert.Equal(t, "10.1.0.1", result.PrimaryPrivateIP)
	assert.Len(t, result.PrivateIPAddresses, 2)
	assert.Equal(t, "fa:16:3e:ff:ff:ff", result.MacAddress)
	assert.Equal(t, "server-full", result.InstanceID)
	assert.Len(t, result.SecurityGroupIDs, 2)
	assert.NotNil(t, result.Tags)
	assert.Equal(t, "huawei", result.Provider)
}

func TestConvertToENIInstance_NetworkIDSubnetFallback(t *testing.T) {
	adapter := newTestENIAdapter()

	// fixed_ips 为空时, 子网ID回退使用 port.network_id(华为云中子网即 network)
	port := model.Port{
		Id:        "port-network-fallback",
		NetworkId: "network-001",
	}

	result := adapter.convertToENIInstance(port, "cn-north-4")

	assert.Equal(t, "network-001", result.SubnetID)
}

func TestConvertToENIInstance_PrimaryInterfaceFalse(t *testing.T) {
	adapter := newTestENIAdapter()

	primaryFalse := false
	port := model.Port{
		Id:                "port-secondary",
		BindingvifDetails: &model.BindingVifDetails{PrimaryInterface: &primaryFalse},
	}

	result := adapter.convertToENIInstance(port, "cn-north-4")

	// primary_interface=false 明确为辅助网卡
	assert.Equal(t, types.ENITypeSecondary, result.Type)
}
