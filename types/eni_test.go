package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ============================================================================
// ENI 状态标准化测试
// ============================================================================

func TestNormalizeENIStatus_Aliyun(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		expected string
	}{
		{"Available", "Available", ENIStatusAvailable},
		{"InUse", "InUse", ENIStatusInUse},
		{"Attaching", "Attaching", ENIStatusAttaching},
		{"Detaching", "Detaching", ENIStatusDetaching},
		{"Creating", "Creating", ENIStatusCreating},
		{"Deleting", "Deleting", ENIStatusDeleting},
		{"未知状态", "CustomStatus", "CustomStatus"},
	}
	for _, tt := range tests {
		t.Run("aliyun_"+tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, NormalizeENIStatus("aliyun", tt.status))
		})
	}
}

func TestNormalizeENIStatus_AWS(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		expected string
	}{
		{"available", "available", ENIStatusAvailable},
		{"in-use", "in-use", ENIStatusInUse},
		{"attaching", "attaching", ENIStatusAttaching},
		{"detaching", "detaching", ENIStatusDetaching},
		{"associated", "associated", ENIStatusInUse},
		{"未知状态", "custom", "custom"},
	}
	for _, tt := range tests {
		t.Run("aws_"+tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, NormalizeENIStatus("aws", tt.status))
		})
	}
}

func TestNormalizeENIStatus_Huawei(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		expected string
	}{
		{"ACTIVE", "ACTIVE", ENIStatusInUse},
		{"BUILD", "BUILD", ENIStatusCreating},
		{"DOWN", "DOWN", ENIStatusAvailable},
		{"ERROR", "ERROR", ENIStatusError},
		{"未知状态", "CUSTOM", "CUSTOM"},
	}
	for _, tt := range tests {
		t.Run("huawei_"+tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, NormalizeENIStatus("huawei", tt.status))
		})
	}
}

func TestNormalizeENIStatus_Tencent(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		expected string
	}{
		{"AVAILABLE", "AVAILABLE", ENIStatusAvailable},
		{"PENDING", "PENDING", ENIStatusCreating},
		{"DELETING", "DELETING", ENIStatusDeleting},
		{"未知状态", "CUSTOM", "CUSTOM"},
	}
	for _, tt := range tests {
		t.Run("tencent_"+tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, NormalizeENIStatus("tencent", tt.status))
		})
	}
}

func TestNormalizeENIStatus_Volcano(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		expected string
	}{
		{"Available", "Available", ENIStatusAvailable},
		{"InUse", "InUse", ENIStatusInUse},
		{"Attaching", "Attaching", ENIStatusAttaching},
		{"Detaching", "Detaching", ENIStatusDetaching},
		{"Creating", "Creating", ENIStatusCreating},
		{"Deleting", "Deleting", ENIStatusDeleting},
		{"未知状态", "CustomStatus", "CustomStatus"},
	}
	for _, tt := range tests {
		t.Run("volcano_"+tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, NormalizeENIStatus("volcano", tt.status))
		})
	}
}

func TestNormalizeENIStatus_Volcengine(t *testing.T) {
	// volcengine 应该和 volcano 使用相同的映射
	assert.Equal(t, ENIStatusAvailable, NormalizeENIStatus("volcengine", "Available"))
	assert.Equal(t, ENIStatusInUse, NormalizeENIStatus("volcengine", "InUse"))
}

func TestNormalizeENIStatus_EdgeCases(t *testing.T) {
	// 空字符串 → unknown
	assert.Equal(t, ENIStatusUnknown, NormalizeENIStatus("aliyun", ""))
	assert.Equal(t, ENIStatusUnknown, NormalizeENIStatus("aws", ""))
	assert.Equal(t, ENIStatusUnknown, NormalizeENIStatus("huawei", ""))
	assert.Equal(t, ENIStatusUnknown, NormalizeENIStatus("tencent", ""))
	assert.Equal(t, ENIStatusUnknown, NormalizeENIStatus("volcano", ""))

	// 未知厂商 → 原样返回
	assert.Equal(t, "RUNNING", NormalizeENIStatus("gcp", "RUNNING"))
	assert.Equal(t, "active", NormalizeENIStatus("unknown_provider", "active"))
}

// ============================================================================
// ENI 常量测试
// ============================================================================

func TestENITypeConstants(t *testing.T) {
	assert.Equal(t, "Primary", ENITypePrimary)
	assert.Equal(t, "Secondary", ENITypeSecondary)
}

func TestENIStatusConstants(t *testing.T) {
	assert.Equal(t, "available", ENIStatusAvailable)
	assert.Equal(t, "in_use", ENIStatusInUse)
	assert.Equal(t, "attaching", ENIStatusAttaching)
	assert.Equal(t, "detaching", ENIStatusDetaching)
	assert.Equal(t, "creating", ENIStatusCreating)
	assert.Equal(t, "deleting", ENIStatusDeleting)
	assert.Equal(t, "error", ENIStatusError)
	assert.Equal(t, "unknown", ENIStatusUnknown)
}

// ============================================================================
// ENI 类型结构测试
// ============================================================================

func TestENIInstance_DefaultValues(t *testing.T) {
	eni := ENIInstance{}
	assert.Empty(t, eni.ENIID)
	assert.Empty(t, eni.ENIName)
	assert.Empty(t, eni.Status)
	assert.Empty(t, eni.Type)
	assert.Empty(t, eni.Region)
	assert.Empty(t, eni.VPCID)
	assert.Empty(t, eni.SubnetID)
	assert.Empty(t, eni.PrimaryPrivateIP)
	assert.Nil(t, eni.PrivateIPAddresses)
	assert.Empty(t, eni.MacAddress)
	assert.Nil(t, eni.IPv6Addresses)
	assert.Empty(t, eni.InstanceID)
	assert.Nil(t, eni.SecurityGroupIDs)
	assert.Nil(t, eni.Tags)
	assert.Empty(t, eni.Provider)
	assert.Equal(t, 0, eni.DeviceIndex)
	assert.Equal(t, int64(0), eni.CloudAccountID)
}

func TestENIInstance_FullPopulation(t *testing.T) {
	eni := ENIInstance{
		ENIID:              "eni-001",
		ENIName:            "test-eni",
		Description:        "test description",
		Status:             ENIStatusAvailable,
		Type:               ENITypeSecondary,
		Region:             "cn-hangzhou",
		Zone:               "cn-hangzhou-a",
		VPCID:              "vpc-001",
		SubnetID:           "vsw-001",
		PrimaryPrivateIP:   "10.0.0.1",
		PrivateIPAddresses: []string{"10.0.0.1", "10.0.0.2"},
		MacAddress:         "00:16:3e:12:34:56",
		IPv6Addresses:      []string{"::1"},
		InstanceID:         "i-001",
		InstanceName:       "web-server",
		DeviceIndex:        1,
		SecurityGroupIDs:   []string{"sg-001", "sg-002"},
		PublicIP:           "1.2.3.4",
		EIPAddresses:       []string{"1.2.3.4"},
		ResourceGroupID:    "rg-001",
		ProjectID:          "proj-001",
		CreationTime:       "2024-01-01T00:00:00Z",
		CloudAccountID:     100,
		CloudAccountName:   "test-account",
		Tags:               map[string]string{"env": "test"},
		Provider:           "aliyun",
	}

	assert.Equal(t, "eni-001", eni.ENIID)
	assert.Equal(t, "test-eni", eni.ENIName)
	assert.Equal(t, "test description", eni.Description)
	assert.Equal(t, ENIStatusAvailable, eni.Status)
	assert.Equal(t, ENITypeSecondary, eni.Type)
	assert.Equal(t, "cn-hangzhou", eni.Region)
	assert.Equal(t, "cn-hangzhou-a", eni.Zone)
	assert.Equal(t, "vpc-001", eni.VPCID)
	assert.Equal(t, "vsw-001", eni.SubnetID)
	assert.Equal(t, "10.0.0.1", eni.PrimaryPrivateIP)
	assert.Len(t, eni.PrivateIPAddresses, 2)
	assert.Equal(t, "00:16:3e:12:34:56", eni.MacAddress)
	assert.Len(t, eni.IPv6Addresses, 1)
	assert.Equal(t, "i-001", eni.InstanceID)
	assert.Equal(t, "web-server", eni.InstanceName)
	assert.Equal(t, 1, eni.DeviceIndex)
	assert.Len(t, eni.SecurityGroupIDs, 2)
	assert.Equal(t, "1.2.3.4", eni.PublicIP)
	assert.Len(t, eni.EIPAddresses, 1)
	assert.Equal(t, "rg-001", eni.ResourceGroupID)
	assert.Equal(t, "proj-001", eni.ProjectID)
	assert.Equal(t, "2024-01-01T00:00:00Z", eni.CreationTime)
	assert.Equal(t, int64(100), eni.CloudAccountID)
	assert.Equal(t, "test-account", eni.CloudAccountName)
	assert.Equal(t, "test", eni.Tags["env"])
	assert.Equal(t, "aliyun", eni.Provider)
}

func TestENIInstanceFilter_DefaultValues(t *testing.T) {
	filter := ENIInstanceFilter{}
	assert.Nil(t, filter.ENIIDs)
	assert.Empty(t, filter.ENIName)
	assert.Nil(t, filter.Status)
	assert.Empty(t, filter.Type)
	assert.Empty(t, filter.VPCID)
	assert.Empty(t, filter.SubnetID)
	assert.Empty(t, filter.InstanceID)
	assert.Empty(t, filter.PrimaryPrivateIP)
	assert.Empty(t, filter.SecurityGroupID)
	assert.Nil(t, filter.Tags)
	assert.Equal(t, 0, filter.PageNumber)
	assert.Equal(t, 0, filter.PageSize)
}

func TestENIInstanceFilter_FullPopulation(t *testing.T) {
	filter := ENIInstanceFilter{
		ENIIDs:           []string{"eni-001", "eni-002"},
		ENIName:          "test-eni",
		Status:           []string{ENIStatusAvailable, ENIStatusInUse},
		Type:             ENITypeSecondary,
		VPCID:            "vpc-001",
		SubnetID:         "vsw-001",
		InstanceID:       "i-001",
		PrimaryPrivateIP: "10.0.0.1",
		SecurityGroupID:  "sg-001",
		Tags:             map[string]string{"env": "prod"},
		PageNumber:       1,
		PageSize:         50,
	}

	assert.Len(t, filter.ENIIDs, 2)
	assert.Equal(t, "test-eni", filter.ENIName)
	assert.Len(t, filter.Status, 2)
	assert.Equal(t, ENITypeSecondary, filter.Type)
	assert.Equal(t, "vpc-001", filter.VPCID)
	assert.Equal(t, "vsw-001", filter.SubnetID)
	assert.Equal(t, "i-001", filter.InstanceID)
	assert.Equal(t, "10.0.0.1", filter.PrimaryPrivateIP)
	assert.Equal(t, "sg-001", filter.SecurityGroupID)
	assert.Equal(t, "prod", filter.Tags["env"])
	assert.Equal(t, 1, filter.PageNumber)
	assert.Equal(t, 50, filter.PageSize)
}
