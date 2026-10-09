package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ============================================================================
// ECS 状态标准化测试
// ============================================================================

func TestNormalizeStatus_Aliyun(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Running", StatusRunning},
		{"Stopped", StatusStopped},
		{"Starting", StatusStarting},
		{"Stopping", StatusStopping},
		{"Pending", StatusPending},
	}
	for _, tt := range tests {
		t.Run("aliyun_"+tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, NormalizeStatus(tt.input))
		})
	}
}

func TestNormalizeStatus_AWS(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"running", StatusRunning},
		{"stopped", StatusStopped},
		{"pending", StatusPending},
		{"stopping", StatusStopping},
		{"shutting-down", StatusStopping},
		{"terminated", StatusTerminated},
	}
	for _, tt := range tests {
		t.Run("aws_"+tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, NormalizeStatus(tt.input))
		})
	}
}

func TestNormalizeStatus_Huawei(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"ACTIVE", StatusRunning},
		{"SHUTOFF", StatusStopped},
		{"BUILD", StatusPending},
		{"REBOOT", StatusRebooting},
		{"HARD_REBOOT", StatusRebooting},
		{"REBUILD", StatusPending},
		{"MIGRATING", StatusRunning},
		{"RESIZE", StatusPending},
		{"ERROR", StatusError},
		{"DELETED", StatusDeleted},
	}
	for _, tt := range tests {
		t.Run("huawei_"+tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, NormalizeStatus(tt.input))
		})
	}
}

func TestNormalizeStatus_Tencent(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"RUNNING", StatusRunning},
		{"STOPPED", StatusStopped},
		{"PENDING", StatusPending},
		{"REBOOTING", StatusRebooting},
		{"STARTING", StatusStarting},
		{"STOPPING", StatusStopping},
		{"EXPIRED", StatusStopped},
		{"TERMINATING", StatusStopping},
		{"SHUTDOWN", StatusStopped},
		{"LAUNCH_FAILED", StatusError},
	}
	for _, tt := range tests {
		t.Run("tencent_"+tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, NormalizeStatus(tt.input))
		})
	}
}

func TestNormalizeStatus_EdgeCases(t *testing.T) {
	// 空字符串 → unknown
	assert.Equal(t, StatusUnknown, NormalizeStatus(""))

	// 未知状态 → 转小写返回
	assert.Equal(t, "some_custom_status", NormalizeStatus("some_custom_status"))

	// 大小写不敏感匹配
	assert.Equal(t, StatusRunning, NormalizeStatus("running"))
	assert.Equal(t, StatusRunning, NormalizeStatus("RUNNING"))
	assert.Equal(t, StatusRunning, NormalizeStatus("Running"))
}

func TestIsRunningStatus(t *testing.T) {
	assert.True(t, IsRunningStatus("Running")) // 阿里云
	assert.True(t, IsRunningStatus("running")) // AWS
	assert.True(t, IsRunningStatus("ACTIVE"))  // 华为云
	assert.True(t, IsRunningStatus("RUNNING")) // 腾讯云
	assert.False(t, IsRunningStatus("Stopped"))
	assert.False(t, IsRunningStatus(""))
}

func TestIsStoppedStatus(t *testing.T) {
	assert.True(t, IsStoppedStatus("Stopped")) // 阿里云
	assert.True(t, IsStoppedStatus("stopped")) // AWS
	assert.True(t, IsStoppedStatus("SHUTOFF")) // 华为云
	assert.True(t, IsStoppedStatus("STOPPED")) // 腾讯云
	assert.True(t, IsStoppedStatus("EXPIRED")) // 腾讯云
	assert.False(t, IsStoppedStatus("Running"))
}

// ============================================================================
// RDS 状态标准化测试
// ============================================================================

func TestNormalizeRDSStatus(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// 阿里云
		{"aliyun_running", "Running", RDSStatusRunning},
		{"aliyun_creating", "Creating", RDSStatusCreating},
		{"aliyun_deleting", "Deleting", RDSStatusDeleting},
		{"aliyun_rebooting", "Rebooting", RDSStatusRestarting},
		{"aliyun_restoring", "Restoring", RDSStatusRestoring},
		{"aliyun_upgrading", "EngineUpgrading", RDSStatusUpgrading},
		{"aliyun_lockmode", "LockMode", RDSStatusError},

		// AWS
		{"aws_available", "available", RDSStatusRunning},
		{"aws_creating", "creating", RDSStatusCreating},
		{"aws_deleting", "deleting", RDSStatusDeleting},
		{"aws_failed", "failed", RDSStatusError},
		{"aws_backing_up", "backing-up", RDSStatusBackingUp},
		{"aws_maintenance", "maintenance", RDSStatusMaintaining},
		{"aws_rebooting", "rebooting", RDSStatusRestarting},
		{"aws_stopped", "stopped", RDSStatusStopped},
		{"aws_storage_full", "storage-full", RDSStatusError},

		// 华为云
		{"huawei_active", "ACTIVE", RDSStatusRunning},
		{"huawei_build", "BUILD", RDSStatusCreating},
		{"huawei_failed", "FAILED", RDSStatusError},
		{"huawei_frozen", "FROZEN", RDSStatusStopped},
		{"huawei_rebooting", "REBOOTING", RDSStatusRestarting},

		// 腾讯云 (数字状态码)
		{"tencent_creating", "0", RDSStatusCreating},
		{"tencent_running", "1", RDSStatusRunning},
		{"tencent_deleting", "4", RDSStatusDeleting},
		{"tencent_isolated", "5", RDSStatusStopped},

		// 边界情况
		{"empty", "", RDSStatusUnknown},
		{"unknown", "weird_status", "weird_status"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, NormalizeRDSStatus(tt.input))
		})
	}
}

// ============================================================================
// Redis 状态标准化测试
// ============================================================================

func TestNormalizeRedisStatus(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// 阿里云
		{"aliyun_normal", "Normal", RedisStatusRunning},
		{"aliyun_creating", "Creating", RedisStatusCreating},
		{"aliyun_changing", "Changing", RedisStatusChanging},
		{"aliyun_inactive", "Inactive", RedisStatusError},
		{"aliyun_error", "Error", RedisStatusError},

		// AWS ElastiCache
		{"aws_available", "available", RedisStatusRunning},
		{"aws_creating", "creating", RedisStatusCreating},
		{"aws_deleting", "deleting", RedisStatusDeleting},
		{"aws_modifying", "modifying", RedisStatusChanging},

		// 华为云 DCS
		{"huawei_running", "RUNNING", RedisStatusRunning},
		{"huawei_creating", "CREATING", RedisStatusCreating},
		{"huawei_error", "ERROR", RedisStatusError},
		{"huawei_extending", "EXTENDING", RedisStatusChanging},

		// 腾讯云
		{"tencent_creating", "0", RedisStatusCreating},
		{"tencent_running", "2", RedisStatusRunning},
		{"tencent_deleting", "-2", RedisStatusDeleting},

		// 边界
		{"empty", "", RedisStatusUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, NormalizeRedisStatus(tt.input))
		})
	}
}

// ============================================================================
// MongoDB 状态标准化测试
// ============================================================================

func TestNormalizeMongoDBStatus(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// 阿里云
		{"aliyun_running", "Running", MongoDBStatusRunning},
		{"aliyun_creating", "Creating", MongoDBStatusCreating},
		{"aliyun_deleting", "Deleting", MongoDBStatusDeleting},
		{"aliyun_rebooting", "Rebooting", MongoDBStatusRestarting},
		{"aliyun_lockmode", "LockMode", MongoDBStatusError},

		// AWS DocumentDB
		{"aws_available", "available", MongoDBStatusRunning},
		{"aws_creating", "creating", MongoDBStatusCreating},
		{"aws_failed", "failed", MongoDBStatusError},
		{"aws_upgrading", "upgrading", MongoDBStatusUpgrading},

		// 华为云 DDS
		{"huawei_active", "ACTIVE", MongoDBStatusRunning},
		{"huawei_build", "BUILD", MongoDBStatusCreating},
		{"huawei_failed", "FAILED", MongoDBStatusError},
		{"huawei_growing", "GROWING", MongoDBStatusUpgrading},

		// 腾讯云
		{"tencent_running", "2", MongoDBStatusRunning},
		{"tencent_creating", "0", MongoDBStatusCreating},

		// 火山引擎
		{"volcano_restarting", "Restarting", MongoDBStatusRestarting},
		{"volcano_upgrading", "Upgrading", MongoDBStatusUpgrading},
		{"volcano_error", "Error", MongoDBStatusError},

		// 边界
		{"empty", "", MongoDBStatusUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, NormalizeMongoDBStatus(tt.input))
		})
	}
}

// ============================================================================
// SecurityGroup / Image / Disk / Snapshot 状态标准化测试
// ============================================================================

func TestNormalizeSecurityGroupStatus(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"available", SecurityGroupStatusAvailable},
		{"Available", SecurityGroupStatusAvailable},
		{"AVAILABLE", SecurityGroupStatusAvailable},
		{"active", SecurityGroupStatusAvailable},
		{"Active", SecurityGroupStatusAvailable},
		{"pending", SecurityGroupStatusPending},
		{"creating", SecurityGroupStatusPending},
		{"deleting", SecurityGroupStatusDeleting},
		{"", SecurityGroupStatusUnknown},
		{"custom", "custom"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, NormalizeSecurityGroupStatus(tt.input))
		})
	}
}

func TestNormalizeImageStatus(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// 可用
		{"available", ImageStatusAvailable},
		{"Available", ImageStatusAvailable},
		{"active", ImageStatusAvailable},
		{"normal", ImageStatusAvailable},
		{"using", ImageStatusAvailable},
		// 创建中
		{"creating", ImageStatusCreating},
		{"pending", ImageStatusCreating},
		{"saving", ImageStatusCreating},
		{"syncing", ImageStatusCreating},
		// 等待
		{"waiting", ImageStatusWaiting},
		{"queued", ImageStatusWaiting},
		// 弃用
		{"deprecated", ImageStatusDeprecated},
		{"deregistered", ImageStatusDeprecated},
		// 不可用
		{"unavailable", ImageStatusUnavailable},
		{"deleted", ImageStatusUnavailable},
		// 错误
		{"error", ImageStatusError},
		{"failed", ImageStatusError},
		{"invalid", ImageStatusError},
		{"killed", ImageStatusError},
		// 边界
		{"", ImageStatusUnknown},
		{"custom_status", "custom_status"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, NormalizeImageStatus(tt.input))
		})
	}
}

func TestNormalizeDiskStatus(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"available", DiskStatusAvailable},
		{"unattached", DiskStatusAvailable},
		{"in_use", DiskStatusInUse},
		{"in-use", DiskStatusInUse},
		{"attached", DiskStatusInUse},
		{"creating", DiskStatusCreating},
		{"uploading", DiskStatusCreating},
		{"extending", DiskStatusCreating},
		{"attaching", DiskStatusAttaching},
		{"detaching", DiskStatusDetaching},
		{"deleting", DiskStatusDeleting},
		{"deleted", DiskStatusDeleting},
		{"torecycle", DiskStatusDeleting},
		{"reiniting", DiskStatusReIniting},
		{"rollbacking", DiskStatusReIniting},
		{"error", DiskStatusError},
		{"error_extending", DiskStatusError},
		{"", DiskStatusUnknown},
		{"all", DiskStatusUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, NormalizeDiskStatus(tt.input))
		})
	}
}

func TestIsDiskAvailable(t *testing.T) {
	assert.True(t, IsDiskAvailable("available"))
	assert.True(t, IsDiskAvailable("unattached"))
	assert.False(t, IsDiskAvailable("in_use"))
	assert.False(t, IsDiskAvailable(""))
}

func TestIsDiskInUse(t *testing.T) {
	assert.True(t, IsDiskInUse("in_use"))
	assert.True(t, IsDiskInUse("in-use"))
	assert.True(t, IsDiskInUse("attached"))
	assert.False(t, IsDiskInUse("available"))
}

func TestNormalizeSnapshotStatus(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"normal", SnapshotStatusNormal},
		{"progressing", SnapshotStatusProgressing},
		{"pending", SnapshotStatusProgressing},
		{"creating", SnapshotStatusProgressing},
		{"rollbacking", SnapshotStatusProgressing},
		{"backing_up", SnapshotStatusProgressing},
		{"copying", SnapshotStatusProgressing},
		{"accomplished", SnapshotStatusAccomplished},
		{"completed", SnapshotStatusAccomplished},
		{"available", SnapshotStatusAccomplished},
		{"failed", SnapshotStatusFailed},
		{"error", SnapshotStatusFailed},
		{"error_deleting", SnapshotStatusFailed},
		{"deleting", SnapshotStatusDeleting},
		{"torecycle", SnapshotStatusDeleting},
		{"", SnapshotStatusUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, NormalizeSnapshotStatus(tt.input))
		})
	}
}

func TestIsSnapshotCompleted(t *testing.T) {
	assert.True(t, IsSnapshotCompleted("accomplished"))
	assert.True(t, IsSnapshotCompleted("completed"))
	assert.True(t, IsSnapshotCompleted("available"))
	assert.False(t, IsSnapshotCompleted("progressing"))
	assert.False(t, IsSnapshotCompleted("failed"))
	assert.False(t, IsSnapshotCompleted(""))
}

// ============================================================================
// NAS 状态标准化测试
// ============================================================================

func TestNASStatus(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		status   string
		expected string
	}{
		// 阿里云
		{"aliyun_running", "aliyun", "Running", "running"},
		{"aliyun_creating", "aliyun", "Creating", "creating"},
		{"aliyun_stopping", "aliyun", "Stopping", "stopping"},
		{"aliyun_stopped", "aliyun", "Stopped", "stopped"},
		{"aliyun_deleting", "aliyun", "Deleting", "deleting"},

		// AWS (EFS LifeCycleState, 原 aws/efs.go 本地 map 已并入)
		{"aws_available", "aws", "available", "running"},
		{"aws_creating", "aws", "creating", "creating"},
		{"aws_deleting", "aws", "deleting", "deleting"},
		{"aws_deleted", "aws", "deleted", "deleted"},
		{"aws_updating", "aws", "updating", "updating"},
		{"aws_error", "aws", "error", "error"},

		// 华为云 (含 SFS Turbo 数字状态码, 原 huawei/sfs.go 本地 map 已并入)
		{"huawei_available", "huawei", "available", "running"},
		{"huawei_creating", "huawei", "creating", "creating"},
		{"huawei_deleting", "huawei", "deleting", "deleting"},
		{"huawei_error", "huawei", "error", "error"},
		{"huawei_code_100", "huawei", "100", "creating"},
		{"huawei_code_200", "huawei", "200", "running"},
		{"huawei_code_300", "huawei", "300", "deleting"},
		{"huawei_code_303", "huawei", "303", "error"},
		{"huawei_code_400", "huawei", "400", "deleted"},

		// 腾讯云 (deleting/delete_failed 原 tencent/cfs.go 本地 map 已并入)
		{"tencent_available", "tencent", "available", "running"},
		{"tencent_creating", "tencent", "creating", "creating"},
		{"tencent_create_failed", "tencent", "create_failed", "error"},
		{"tencent_deleting", "tencent", "deleting", "deleting"},
		{"tencent_delete_failed", "tencent", "delete_failed", "error"},

		// 火山引擎
		{"volcano_running", "volcano", "Running", "running"},
		{"volcano_creating", "volcano", "Creating", "creating"},
		{"volcano_expanding", "volcano", "Expanding", "expanding"},
		{"volcano_deleting", "volcano", "Deleting", "deleting"},
		{"volcano_error", "volcano", "Error", "error"},
		{"volcano_delete_error", "volcano", "DeleteError", "error"},
		{"volcano_deleted", "volcano", "Deleted", "deleted"},
		{"volcano_stopped", "volcano", "Stopped", "stopped"},
		{"volcano_unknown", "volcano", "Unknown", "unknown"},

		// 未知厂商 → 原样返回
		{"unknown_provider", "gcp", "RUNNING", "RUNNING"},
		// 未知状态 → 原样返回
		{"unknown_status", "aliyun", "CustomStatus", "CustomStatus"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, NASStatus(tt.provider, tt.status))
		})
	}
}

// ============================================================================
// Kafka 状态标准化测试
// ============================================================================

func TestKafkaStatus(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		status   string
		expected string
	}{
		// 阿里云 (数字状态码)
		{"aliyun_creating", "aliyun", "0", "creating"},
		{"aliyun_running", "aliyun", "1", "running"},
		{"aliyun_stopped", "aliyun", "2", "stopped"},
		{"aliyun_starting", "aliyun", "3", "starting"},
		{"aliyun_stopping", "aliyun", "4", "stopping"},
		{"aliyun_serving", "aliyun", "5", "running"},
		{"aliyun_upgrading", "aliyun", "6", "upgrading"},
		{"aliyun_deleting", "aliyun", "7", "deleting"},
		{"aliyun_expired", "aliyun", "15", "expired"},

		// 华为云
		{"huawei_creating", "huawei", "CREATING", "creating"},
		{"huawei_running", "huawei", "RUNNING", "running"},
		{"huawei_faulty", "huawei", "FAULTY", "error"},
		{"huawei_restarting", "huawei", "RESTARTING", "restarting"},
		{"huawei_resizing", "huawei", "RESIZING", "resizing"},
		{"huawei_frozen", "huawei", "FROZEN", "frozen"},

		// 腾讯云
		{"tencent_creating", "tencent", "0", "creating"},
		{"tencent_running", "tencent", "1", "running"},
		{"tencent_deleting", "tencent", "2", "deleting"},
		{"tencent_isolated", "tencent", "5", "isolated"},

		// AWS
		{"aws_creating", "aws", "CREATING", "creating"},
		{"aws_active", "aws", "ACTIVE", "running"},
		{"aws_rebooting", "aws", "REBOOTING", "restarting"},
		{"aws_updating", "aws", "UPDATING", "updating"},
		{"aws_deleting", "aws", "DELETING", "deleting"},
		{"aws_failed", "aws", "FAILED", "error"},

		// 火山引擎
		{"volcano_running", "volcano", "Running", "running"},
		{"volcano_creating", "volcano", "Creating", "creating"},
		{"volcano_deleting", "volcano", "Deleting", "deleting"},
		{"volcano_error", "volcano", "Error", "error"},

		// 未知
		{"unknown_provider", "gcp", "active", "active"},
		{"unknown_status", "aliyun", "99", "99"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, KafkaStatus(tt.provider, tt.status))
		})
	}
}

// ============================================================================
// Elasticsearch 状态标准化测试
// ============================================================================

func TestElasticsearchStatus(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		status   string
		expected string
	}{
		// 阿里云
		{"aliyun_active", "aliyun", "active", "running"},
		{"aliyun_activating", "aliyun", "activating", "creating"},
		{"aliyun_inactive", "aliyun", "inactive", "stopped"},
		{"aliyun_invalid", "aliyun", "invalid", "error"},

		// 华为云 (数字状态码)
		{"huawei_creating", "huawei", "100", "creating"},
		{"huawei_running", "huawei", "200", "running"},
		{"huawei_error", "huawei", "303", "error"},
		{"huawei_deleted", "huawei", "400", "deleted"},

		// 腾讯云
		{"tencent_creating", "tencent", "0", "creating"},
		{"tencent_running", "tencent", "1", "running"},
		{"tencent_stopped", "tencent", "2", "stopped"},
		{"tencent_error", "tencent", "-1", "error"},
		{"tencent_deleting", "tencent", "-2", "deleting"},

		// AWS
		{"aws_creating", "aws", "CREATING", "creating"},
		{"aws_active", "aws", "ACTIVE", "running"},
		{"aws_modifying", "aws", "MODIFYING", "updating"},
		{"aws_upgrading", "aws", "UPGRADING", "upgrading"},
		{"aws_deleting", "aws", "DELETING", "deleting"},
		{"aws_deleted", "aws", "DELETED", "deleted"},
		{"aws_vpc_limit", "aws", "VPC_ENDPOINT_LIMIT_EXCEEDED", "error"},

		// 火山引擎
		{"volcano_running", "volcano", "Running", "running"},
		{"volcano_creating", "volcano", "Creating", "creating"},
		{"volcano_deleting", "volcano", "Deleting", "deleting"},
		{"volcano_updating", "volcano", "Updating", "updating"},
		{"volcano_error", "volcano", "Error", "error"},

		// 未知
		{"unknown_provider", "gcp", "active", "active"},
		{"unknown_status", "aliyun", "custom", "custom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, ElasticsearchStatus(tt.provider, tt.status))
		})
	}
}

// ============================================================================
// Redis/MongoDB 大写回退路径测试
// ============================================================================

func TestNormalizeRedisStatus_UppercaseFallback(t *testing.T) {
	// 测试 strings.ToUpper 回退路径
	// "running" 不在映射表中，但 "RUNNING" 在华为云映射中
	assert.Equal(t, RedisStatusRunning, NormalizeRedisStatus("running"))

	// "creating" → ToUpper → "CREATING" 在华为云映射中
	assert.Equal(t, RedisStatusCreating, NormalizeRedisStatus("creating"))

	// 完全未知的状态 → 转小写返回
	assert.Equal(t, "weirdstatus", NormalizeRedisStatus("WeirdStatus"))
}

func TestNormalizeMongoDBStatus_UppercaseFallback(t *testing.T) {
	// "active" 不在映射表中，但 "ACTIVE" 在华为云映射中
	assert.Equal(t, MongoDBStatusRunning, NormalizeMongoDBStatus("active"))

	// "build" → ToUpper → "BUILD" 在华为云映射中
	assert.Equal(t, MongoDBStatusCreating, NormalizeMongoDBStatus("build"))

	// "failed" → ToUpper → "FAILED" 在华为云映射中
	assert.Equal(t, MongoDBStatusError, NormalizeMongoDBStatus("failed"))

	// 完全未知的状态 → 转小写返回
	assert.Equal(t, "weirdstatus", NormalizeMongoDBStatus("WeirdStatus"))
}
