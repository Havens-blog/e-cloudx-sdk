package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ============================================================================
// 表驱动状态映射一致性测试
// 验证 4 个包级 map 表 (securityGroupStatusMap/imageStatusMap/diskStatusMap/snapshotStatusMap)
// 与对应 NormalizeXStatus 函数行为一致：表中每个键经归一化后返回表中登记的规范值，
// 大小写变体（strings.ToLower 路径）与原键同结果，未匹配时兜底返回小写形式。
// ============================================================================

func TestSecurityGroupStatusMapConsistency(t *testing.T) {
	for input, want := range securityGroupStatusMap {
		assert.Equal(t, want, NormalizeSecurityGroupStatus(input), "input %q", input)
		assert.Equal(t, want, NormalizeSecurityGroupStatus(strings.ToUpper(input)), "upper(%q)", input)
	}
	// 兜底：未匹配返回小写形式
	assert.Equal(t, "custom", NormalizeSecurityGroupStatus("CuStOm"))
}

func TestImageStatusMapConsistency(t *testing.T) {
	for input, want := range imageStatusMap {
		assert.Equal(t, want, NormalizeImageStatus(input), "input %q", input)
		assert.Equal(t, want, NormalizeImageStatus(strings.ToUpper(input)), "upper(%q)", input)
	}
	assert.Equal(t, "custom_status", NormalizeImageStatus("CUSTOM_STATUS"))
}

func TestDiskStatusMapConsistency(t *testing.T) {
	for input, want := range diskStatusMap {
		assert.Equal(t, want, NormalizeDiskStatus(input), "input %q", input)
		assert.Equal(t, want, NormalizeDiskStatus(strings.ToUpper(input)), "upper(%q)", input)
	}
	// 多值 case 完整性抽查：in_use/in-use/attached 同映射
	assert.Equal(t, DiskStatusInUse, diskStatusMap["in_use"])
	assert.Equal(t, DiskStatusInUse, diskStatusMap["in-use"])
	assert.Equal(t, DiskStatusInUse, diskStatusMap["attached"])
	assert.Equal(t, "custom", NormalizeDiskStatus("CuStOm"))
}

func TestSnapshotStatusMapConsistency(t *testing.T) {
	for input, want := range snapshotStatusMap {
		assert.Equal(t, want, NormalizeSnapshotStatus(input), "input %q", input)
		assert.Equal(t, want, NormalizeSnapshotStatus(strings.ToUpper(input)), "upper(%q)", input)
	}
	assert.Equal(t, "custom", NormalizeSnapshotStatus("CuStOm"))
}

// TestStatusTablesNoDrift 抽样验证表值与规范状态常量一致，防止抄表漂移。
func TestStatusTablesNoDrift(t *testing.T) {
	// SecurityGroup
	assert.Equal(t, SecurityGroupStatusAvailable, securityGroupStatusMap["available"])
	assert.Equal(t, SecurityGroupStatusAvailable, securityGroupStatusMap["active"])
	assert.Equal(t, SecurityGroupStatusPending, securityGroupStatusMap["pending"])
	assert.Equal(t, SecurityGroupStatusPending, securityGroupStatusMap["creating"])
	assert.Equal(t, SecurityGroupStatusDeleting, securityGroupStatusMap["deleting"])
	assert.Equal(t, SecurityGroupStatusUnknown, securityGroupStatusMap[""])

	// Image
	assert.Equal(t, ImageStatusAvailable, imageStatusMap["available"])
	assert.Equal(t, ImageStatusCreating, imageStatusMap["transient"])
	assert.Equal(t, ImageStatusWaiting, imageStatusMap["queued"])
	assert.Equal(t, ImageStatusDeprecated, imageStatusMap["deregistered"])
	assert.Equal(t, ImageStatusUnavailable, imageStatusMap["deleted"])
	assert.Equal(t, ImageStatusError, imageStatusMap["killed"])
	assert.Equal(t, ImageStatusUnknown, imageStatusMap[""])

	// Disk
	assert.Equal(t, DiskStatusAvailable, diskStatusMap["unattached"])
	assert.Equal(t, DiskStatusCreating, diskStatusMap["expanding"])
	assert.Equal(t, DiskStatusAttaching, diskStatusMap["attaching"])
	assert.Equal(t, DiskStatusDetaching, diskStatusMap["detaching"])
	assert.Equal(t, DiskStatusDeleting, diskStatusMap["torecycle"])
	assert.Equal(t, DiskStatusReIniting, diskStatusMap["rollbacking"])
	assert.Equal(t, DiskStatusError, diskStatusMap["error_rollbacking"])
	assert.Equal(t, DiskStatusUnknown, diskStatusMap["all"])
	assert.Equal(t, DiskStatusUnknown, diskStatusMap[""])

	// Snapshot
	assert.Equal(t, SnapshotStatusNormal, snapshotStatusMap["normal"])
	assert.Equal(t, SnapshotStatusProgressing, snapshotStatusMap["backing_up"])
	assert.Equal(t, SnapshotStatusProgressing, snapshotStatusMap["copying"])
	assert.Equal(t, SnapshotStatusAccomplished, snapshotStatusMap["available"])
	assert.Equal(t, SnapshotStatusFailed, snapshotStatusMap["error_deleting"])
	assert.Equal(t, SnapshotStatusDeleting, snapshotStatusMap["torecycle"])
	assert.Equal(t, SnapshotStatusUnknown, snapshotStatusMap[""])
}
