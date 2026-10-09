package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// networkStatusProviderKeys 网络域状态表期望的 provider key 集合（与 eni_table_test 同口径）。
func networkStatusProviderKeys() []string {
	return []string{"aliyun", "aws", "huawei", "tencent", "volcano"}
}

// TestNetworkStatusMaps_ProviderKeys 校验各网络域状态表的 provider key 集合。
func TestNetworkStatusMaps_ProviderKeys(t *testing.T) {
	expected := networkStatusProviderKeys()
	for name, m := range map[string]map[string]map[string]string{
		"eip":     eipStatusMaps,
		"lb":      lbStatusMaps,
		"vpc":     vpcStatusMaps,
		"vswitch": vswitchStatusMaps,
	} {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		assert.ElementsMatch(t, expected, keys, "%s status maps provider keys", name)
	}
}

// TestNormalizeEIPStatus_EmptyIsPassthrough 空值原样透传（字段缺失语义，不归一为 unknown）。
func TestNormalizeEIPStatus_EmptyIsPassthrough(t *testing.T) {
	assert.Equal(t, "", NormalizeEIPStatus("aliyun", ""))
	assert.Equal(t, "", NormalizeLBStatus("aws", ""))
	assert.Equal(t, "", NormalizeVPCStatus("huawei", ""))
	assert.Equal(t, "", NormalizeVSwitchStatus("volcano", ""))
}

// TestNormalizeEIPStatus_Table 各厂商代表性原始值归一断言（哨兵样例，全表由表自身声明）。
func TestNormalizeEIPStatus_Table(t *testing.T) {
	cases := []struct {
		provider string
		raw      string
		want     string
	}{
		{"aliyun", "InUse", EIPStatusInUse},
		{"aliyun", "Associating", EIPStatusAssociating},
		{"aliyun", "Unassociating", EIPStatusUnassociating},
		{"aliyun", "Releasing", EIPStatusReleasing},
		{"aliyun", "Available", EIPStatusAvailable},
		{"aws", "InUse", EIPStatusInUse},
		{"aws", "Available", EIPStatusAvailable},
		{"huawei", "FREEZED", EIPStatusFrozen},
		{"huawei", "BIND_ERROR", EIPStatusError},
		{"tencent", "BIND", EIPStatusInUse},
		{"tencent", "UNBIND", EIPStatusAvailable},
		{"tencent", "BIND_ENI", EIPStatusInUse},
		{"volcano", "Attaching", EIPStatusAssociating},
		{"volcengine", "InUse", EIPStatusInUse},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, NormalizeEIPStatus(c.provider, c.raw), "%s/%s", c.provider, c.raw)
	}
}

// TestNormalizeLBStatus_Table LB 各厂商代表性原始值归一断言。
func TestNormalizeLBStatus_Table(t *testing.T) {
	cases := []struct {
		provider string
		raw      string
		want     string
	}{
		{"aliyun", "Active", LBStatusActive},
		{"aliyun", "Inactive", LBStatusInactive},
		{"aliyun", "Provisioning", LBStatusCreating},
		{"aliyun", "Deleting", LBStatusDeleting},
		{"aws", "active", LBStatusActive},
		{"aws", "provisioning", LBStatusCreating},
		{"aws", "active_impaired", LBStatusError},
		{"aws", "failed", LBStatusError},
		{"huawei", "ACTIVE", LBStatusActive},
		{"huawei", "PENDING_CREATE", LBStatusCreating},
		{"huawei", "PENDING_UPDATE", LBStatusUpdating},
		{"huawei", "PENDING_DELETE", LBStatusDeleting},
		{"huawei", "NOT_ACTIVE", LBStatusInactive},
		{"tencent", "Creating", LBStatusCreating},
		{"tencent", "Active", LBStatusActive},
		{"volcano", "active", LBStatusActive},
		{"volcano", "inactive", LBStatusInactive},
		{"volcengine", "Active", LBStatusActive},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, NormalizeLBStatus(c.provider, c.raw), "%s/%s", c.provider, c.raw)
	}
}

// TestNormalizeVPCStatus_Table VPC 各厂商代表性原始值归一断言。
func TestNormalizeVPCStatus_Table(t *testing.T) {
	cases := []struct {
		provider string
		raw      string
		want     string
	}{
		{"aliyun", "Available", VPCStatusAvailable},
		{"aliyun", "Pending", VPCStatusPending},
		{"aws", "available", VPCStatusAvailable},
		{"aws", "pending", VPCStatusPending},
		{"huawei", "OK", VPCStatusAvailable},
		{"huawei", "ERROR", VPCStatusError},
		{"tencent", "Available", VPCStatusAvailable},
		{"volcano", "Creating", VPCStatusPending},
		{"volcengine", "Available", VPCStatusAvailable},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, NormalizeVPCStatus(c.provider, c.raw), "%s/%s", c.provider, c.raw)
	}
}

// TestNormalizeVSwitchStatus_Table VSwitch 各厂商代表性原始值归一断言。
func TestNormalizeVSwitchStatus_Table(t *testing.T) {
	cases := []struct {
		provider string
		raw      string
		want     string
	}{
		{"aliyun", "Available", VSwitchStatusAvailable},
		{"aliyun", "Pending", VSwitchStatusPending},
		{"aws", "available", VSwitchStatusAvailable},
		{"aws", "pending", VSwitchStatusPending},
		{"huawei", "ACTIVE", VSwitchStatusAvailable},
		{"huawei", "DOWN", VSwitchStatusError},
		{"tencent", "Available", VSwitchStatusAvailable},
		{"volcano", "Deleting", VSwitchStatusDeleting},
		{"volcengine", "ACTIVE", VSwitchStatusAvailable},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, NormalizeVSwitchStatus(c.provider, c.raw), "%s/%s", c.provider, c.raw)
	}
}

// TestNetworkStatus_UnknownPassthrough 未命中/未知 provider 原样透传（与 eni_table_test 语义一致）。
func TestNetworkStatus_UnknownPassthrough(t *testing.T) {
	for _, p := range networkStatusProviderKeys() {
		assert.Equal(t, "brand-new-state", NormalizeEIPStatus(p, "brand-new-state"))
		assert.Equal(t, "brand-new-state", NormalizeLBStatus(p, "brand-new-state"))
		assert.Equal(t, "brand-new-state", NormalizeVPCStatus(p, "brand-new-state"))
		assert.Equal(t, "brand-new-state", NormalizeVSwitchStatus(p, "brand-new-state"))
	}
	assert.Equal(t, "whatever", NormalizeEIPStatus("azure-unknown", "whatever"))
}

// TestNetworkStatus_VolcengineAliasRouting 校验 volcengine 路由到 volcano 同映射。
func TestNetworkStatus_VolcengineAliasRouting(t *testing.T) {
	// volcano 键在 eipStatusMaps 中存在，volcengine 通过别名路由命中同一表
	assert.Equal(t, NormalizeEIPStatus("volcano", "InUse"), NormalizeEIPStatus("volcengine", "InUse"))
	assert.Equal(t, NormalizeLBStatus("volcano", "active"), NormalizeLBStatus("volcengine", "active"))
	assert.Equal(t, NormalizeVPCStatus("volcano", "Available"), NormalizeVPCStatus("volcengine", "Available"))
	assert.Equal(t, NormalizeVSwitchStatus("volcano", "ACTIVE"), NormalizeVSwitchStatus("volcengine", "ACTIVE"))
}
