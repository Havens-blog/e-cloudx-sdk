package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ============================================================================
// ENI 状态映射表结构一致性测试（表驱动化后的护栏）
// ============================================================================

// TestENIStatusMaps_TableValuesAreCanonical 校验每张厂商映射表的值都是合法的 ENI 标准状态枚举
func TestENIStatusMaps_TableValuesAreCanonical(t *testing.T) {
	valid := map[string]bool{
		ENIStatusAvailable: true,
		ENIStatusInUse:     true,
		ENIStatusAttaching: true,
		ENIStatusDetaching: true,
		ENIStatusCreating:  true,
		ENIStatusDeleting:  true,
		ENIStatusError:     true,
		ENIStatusUnknown:   true,
	}

	for provider, m := range eniStatusMaps {
		for raw, normalized := range m {
			assert.Truef(t, valid[normalized],
				"provider %q: raw %q maps to non-canonical value %q", provider, raw, normalized)
		}
	}
}

// TestENIStatusMaps_ProviderKeys 校验 provider key 集合（含 volcano/volcengine 别名）
func TestENIStatusMaps_ProviderKeys(t *testing.T) {
	expected := []string{"aliyun", "aws", "huawei", "tencent", "volcano", "volcengine"}
	assert.Len(t, eniStatusMaps, len(expected))
	for _, p := range expected {
		assert.Containsf(t, eniStatusMaps, p, "missing provider key %q", p)
	}
}

// TestENIStatusMaps_VolcengineAliasRouting 校验 volcano 与 volcengine 路由同一映射
func TestENIStatusMaps_VolcengineAliasRouting(t *testing.T) {
	assert.Equal(t, eniStatusMaps["volcano"], eniStatusMaps["volcengine"])

	// 行为层面抽样：两 key 对同一输入产出一致
	samples := []string{"Available", "InUse", "Attaching", "Detaching", "Creating", "Deleting", "CustomStatus"}
	for _, s := range samples {
		assert.Equalf(t,
			NormalizeENIStatus("volcano", s),
			NormalizeENIStatus("volcengine", s),
			"alias routing diverged for input %q", s)
	}
}

// TestENIStatusMaps_SampleInputsOutput 抽样校验映射表输入输出与原 switch 行为一致（零漂移锚点）
func TestENIStatusMaps_SampleInputsOutput(t *testing.T) {
	cases := []struct {
		provider string
		raw      string
		expected string
	}{
		// aliyun
		{"aliyun", "Available", ENIStatusAvailable},
		{"aliyun", "InUse", ENIStatusInUse},
		{"aliyun", "Attaching", ENIStatusAttaching},
		{"aliyun", "Deleting", ENIStatusDeleting},
		// aws
		{"aws", "available", ENIStatusAvailable},
		{"aws", "in-use", ENIStatusInUse},
		{"aws", "associated", ENIStatusInUse},
		// huawei
		{"huawei", "ACTIVE", ENIStatusInUse},
		{"huawei", "BUILD", ENIStatusCreating},
		{"huawei", "DOWN", ENIStatusAvailable},
		{"huawei", "ERROR", ENIStatusError},
		// tencent（含怪例）
		{"tencent", "AVAILABLE", ENIStatusAvailable},
		{"tencent", "BINDbindingd", ENIStatusAttaching},
		{"tencent", "BINDbindingd ", ENIStatusAttaching}, // 尾空格怪例，原样保留
		{"tencent", "BINDUNBINDING", ENIStatusDetaching},
		{"tencent", "BINDBOUND", ENIStatusInUse},
		{"tencent", "BINDUNBOUND", ENIStatusAvailable},
		{"tencent", "BINDDELETING", ENIStatusDeleting},
		{"tencent", "PENDING", ENIStatusCreating},
		{"tencent", "DELETING", ENIStatusDeleting},
		// volcano
		{"volcano", "Available", ENIStatusAvailable},
		{"volcano", "InUse", ENIStatusInUse},
	}

	for _, tc := range cases {
		assert.Equalf(t, tc.expected, NormalizeENIStatus(tc.provider, tc.raw),
			"provider %q raw %q", tc.provider, tc.raw)
	}
}

// TestENIStatusMaps_FallbackBehavior 校验大小写敏感与 fallback 语义（零漂移锚点）
func TestENIStatusMaps_FallbackBehavior(t *testing.T) {
	// 大小写敏感：禁止任何 ToLower 归一
	assert.Equal(t, "available", NormalizeENIStatus("aliyun", "available")) // 小写不在 aliyun 表中，原样返回
	assert.Equal(t, "Available", NormalizeENIStatus("aws", "Available"))    // 大写不在 aws 表中，原样返回
	assert.Equal(t, "AVAILABLE", NormalizeENIStatus("huawei", "AVAILABLE")) // huawei 只认大写 ACTIVE，其他原样返回
	assert.Equal(t, "Active", NormalizeENIStatus("huawei", "Active"))       // 大小写不匹配原样返回

	// 未知 provider 原样返回
	assert.Equal(t, "RUNNING", NormalizeENIStatus("gcp", "RUNNING"))
	assert.Equal(t, "active", NormalizeENIStatus("unknown_provider", "active"))

	// 已知 provider 未命中原样返回
	assert.Equal(t, "CustomStatus", NormalizeENIStatus("aliyun", "CustomStatus"))
	assert.Equal(t, "custom", NormalizeENIStatus("aws", "custom"))

	// 空字符串 → unknown
	for _, p := range []string{"aliyun", "aws", "huawei", "tencent", "volcano", "volcengine"} {
		assert.Equalf(t, ENIStatusUnknown, NormalizeENIStatus(p, ""), "provider %q empty status", p)
	}
}
