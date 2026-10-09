package types

import "strings"

// 标准化的 ECS 实例状态 (全小写)
const (
	StatusRunning    = "running"    // 运行中
	StatusStopped    = "stopped"    // 已停止
	StatusStarting   = "starting"   // 启动中
	StatusStopping   = "stopping"   // 停止中
	StatusPending    = "pending"    // 创建中
	StatusTerminated = "terminated" // 已销毁
	StatusRebooting  = "rebooting"  // 重启中
	StatusDeleted    = "deleted"    // 已删除
	StatusError      = "error"      // 异常
	StatusUnknown    = "unknown"    // 未知
)

// 各云平台状态映射表
var statusMapping = map[string]string{
	// 阿里云 (首字母大写)
	"Running":  StatusRunning,
	"Stopped":  StatusStopped,
	"Starting": StatusStarting,
	"Stopping": StatusStopping,
	"Pending":  StatusPending,

	// AWS (全小写)
	"running":       StatusRunning,
	"stopped":       StatusStopped,
	"pending":       StatusPending,
	"stopping":      StatusStopping,
	"shutting-down": StatusStopping,
	"terminated":    StatusTerminated,

	// 华为云 (全大写)
	"ACTIVE":      StatusRunning,
	"SHUTOFF":     StatusStopped,
	"BUILD":       StatusPending,
	"REBOOT":      StatusRebooting,
	"HARD_REBOOT": StatusRebooting,
	"REBUILD":     StatusPending,
	"MIGRATING":   StatusRunning,
	"RESIZE":      StatusPending,
	"ERROR":       StatusError,
	"DELETED":     StatusDeleted,

	// 腾讯云 (全大写)
	"RUNNING":       StatusRunning,
	"STOPPED":       StatusStopped,
	"PENDING":       StatusPending,
	"REBOOTING":     StatusRebooting,
	"STARTING":      StatusStarting,
	"STOPPING":      StatusStopping,
	"EXPIRED":       StatusStopped,
	"TERMINATING":   StatusStopping,
	"SHUTDOWN":      StatusStopped,
	"LAUNCH_FAILED": StatusError,

	// 火山引擎 (全大写)
	// RUNNING, STOPPED 等已在上面定义
}

// NormalizeStatus 标准化实例状态
// 将各云平台的状态值统一转换为小写的标准值
func NormalizeStatus(status string) string {
	// 先尝试精确匹配
	if normalized, ok := statusMapping[status]; ok {
		return normalized
	}

	// 尝试大写匹配
	if normalized, ok := statusMapping[strings.ToUpper(status)]; ok {
		return normalized
	}

	// 尝试首字母大写匹配
	if len(status) > 0 {
		titleCase := strings.ToUpper(status[:1]) + strings.ToLower(status[1:])
		if normalized, ok := statusMapping[titleCase]; ok {
			return normalized
		}
	}

	// 兜底：转小写返回
	lower := strings.ToLower(status)
	if lower == "" {
		return StatusUnknown
	}
	return lower
}

// IsRunningStatus 判断是否为运行中状态
func IsRunningStatus(status string) bool {
	return NormalizeStatus(status) == StatusRunning
}

// IsStoppedStatus 判断是否为停止状态
func IsStoppedStatus(status string) bool {
	return NormalizeStatus(status) == StatusStopped
}

// ============================================================================
// RDS 状态标准化
// ============================================================================

// RDS 标准化状态
const (
	RDSStatusRunning     = "running"     // 运行中
	RDSStatusStopped     = "stopped"     // 已停止
	RDSStatusCreating    = "creating"    // 创建中
	RDSStatusDeleting    = "deleting"    // 删除中
	RDSStatusRestarting  = "restarting"  // 重启中
	RDSStatusMaintaining = "maintaining" // 维护中
	RDSStatusUpgrading   = "upgrading"   // 升级中
	RDSStatusBackingUp   = "backing_up"  // 备份中
	RDSStatusRestoring   = "restoring"   // 恢复中
	RDSStatusSwitching   = "switching"   // 切换中
	RDSStatusError       = "error"       // 异常
	RDSStatusUnknown     = "unknown"     // 未知
)

// RDS 状态映射表
var rdsStatusMapping = map[string]string{
	// 阿里云
	"Running":                   RDSStatusRunning,
	"Creating":                  RDSStatusCreating,
	"Deleting":                  RDSStatusDeleting,
	"Rebooting":                 RDSStatusRestarting,
	"DBInstanceDeleting":        RDSStatusDeleting,
	"Restoring":                 RDSStatusRestoring,
	"Importing":                 RDSStatusRestoring,
	"EngineUpgrading":           RDSStatusUpgrading,
	"ClassChanging":             RDSStatusUpgrading,
	"NetAddressCreating":        RDSStatusMaintaining,
	"NetAddressDeleting":        RDSStatusMaintaining,
	"DBInstanceClassChanging":   RDSStatusUpgrading,
	"DBInstanceNetTypeChanging": RDSStatusMaintaining,
	"GuardSwitching":            RDSStatusSwitching,
	"LockMode":                  RDSStatusError,

	// AWS
	"available":                           RDSStatusRunning,
	"backing-up":                          RDSStatusBackingUp,
	"configuring-enhanced-monitoring":     RDSStatusMaintaining,
	"configuring-iam-database-auth":       RDSStatusMaintaining,
	"configuring-log-exports":             RDSStatusMaintaining,
	"converting-to-vpc":                   RDSStatusMaintaining,
	"creating":                            RDSStatusCreating,
	"deleting":                            RDSStatusDeleting,
	"failed":                              RDSStatusError,
	"inaccessible-encryption-credentials": RDSStatusError,
	"incompatible-network":                RDSStatusError,
	"incompatible-option-group":           RDSStatusError,
	"incompatible-parameters":             RDSStatusError,
	"incompatible-restore":                RDSStatusError,
	"maintenance":                         RDSStatusMaintaining,
	"modifying":                           RDSStatusMaintaining,
	"moving-to-vpc":                       RDSStatusMaintaining,
	"rebooting":                           RDSStatusRestarting,
	"renaming":                            RDSStatusMaintaining,
	"resetting-master-credentials":        RDSStatusMaintaining,
	"restore-error":                       RDSStatusError,
	"starting":                            RDSStatusCreating,
	"stopped":                             RDSStatusStopped,
	"stopping":                            RDSStatusDeleting,
	"storage-full":                        RDSStatusError,
	"storage-optimization":                RDSStatusMaintaining,
	"upgrading":                           RDSStatusUpgrading,

	// 华为云
	"ACTIVE":                  RDSStatusRunning,
	"BUILD":                   RDSStatusCreating,
	"FAILED":                  RDSStatusError,
	"FROZEN":                  RDSStatusStopped,
	"MODIFYING":               RDSStatusMaintaining,
	"REBOOTING":               RDSStatusRestarting,
	"RESTORING":               RDSStatusRestoring,
	"SWITCHOVER":              RDSStatusSwitching,
	"MIGRATING":               RDSStatusMaintaining,
	"BACKING UP":              RDSStatusBackingUp,
	"MODIFYING INSTANCE TYPE": RDSStatusUpgrading,
	"MODIFYING DATABASE PORT": RDSStatusMaintaining,

	// 腾讯云
	"0":  RDSStatusCreating,    // 创建中
	"1":  RDSStatusRunning,     // 运行中
	"4":  RDSStatusDeleting,    // 删除中
	"5":  RDSStatusStopped,     // 隔离中
	"6":  RDSStatusStopped,     // 已隔离
	"7":  RDSStatusMaintaining, // 任务执行中
	"8":  RDSStatusStopped,     // 已下线
	"9":  RDSStatusUpgrading,   // 实例扩容中
	"10": RDSStatusMaintaining, // 实例迁移中
	"12": RDSStatusMaintaining, // 灾备实例同步中
	"14": RDSStatusMaintaining, // 版本升级中
}

// NormalizeRDSStatus 标准化RDS实例状态
func NormalizeRDSStatus(status string) string {
	if normalized, ok := rdsStatusMapping[status]; ok {
		return normalized
	}
	if normalized, ok := rdsStatusMapping[strings.ToUpper(status)]; ok {
		return normalized
	}
	lower := strings.ToLower(status)
	if lower == "" {
		return RDSStatusUnknown
	}
	return lower
}

// ============================================================================
// Redis 状态标准化
// ============================================================================

// Redis 标准化状态
const (
	RedisStatusRunning     = "running"     // 运行中
	RedisStatusCreating    = "creating"    // 创建中
	RedisStatusDeleting    = "deleting"    // 删除中
	RedisStatusChanging    = "changing"    // 变配中
	RedisStatusMaintaining = "maintaining" // 维护中
	RedisStatusError       = "error"       // 异常
	RedisStatusUnknown     = "unknown"     // 未知
)

// Redis 状态映射表
var redisStatusMapping = map[string]string{
	// 阿里云
	"Normal":                RedisStatusRunning,
	"Creating":              RedisStatusCreating,
	"Changing":              RedisStatusChanging,
	"Inactive":              RedisStatusError,
	"Flushing":              RedisStatusMaintaining,
	"Released":              RedisStatusDeleting,
	"Transforming":          RedisStatusChanging,
	"Unavailable":           RedisStatusError,
	"Error":                 RedisStatusError,
	"Migrating":             RedisStatusMaintaining,
	"BackupRecovering":      RedisStatusMaintaining,
	"MinorVersionUpgrading": RedisStatusMaintaining,
	"NetworkModifying":      RedisStatusMaintaining,
	"SSLModifying":          RedisStatusMaintaining,
	"MajorVersionUpgrading": RedisStatusMaintaining,

	// AWS ElastiCache
	"available":               RedisStatusRunning,
	"creating":                RedisStatusCreating,
	"deleted":                 RedisStatusDeleting,
	"deleting":                RedisStatusDeleting,
	"incompatible-network":    RedisStatusError,
	"modifying":               RedisStatusChanging,
	"rebooting cluster nodes": RedisStatusMaintaining,
	"restore-failed":          RedisStatusError,
	"snapshotting":            RedisStatusMaintaining,

	// 华为云 DCS
	"RUNNING":      RedisStatusRunning,
	"CREATING":     RedisStatusCreating,
	"CREATEFAILED": RedisStatusError,
	"ERROR":        RedisStatusError,
	"RESTARTING":   RedisStatusMaintaining,
	"FROZEN":       RedisStatusError,
	"EXTENDING":    RedisStatusChanging,
	"RESTORING":    RedisStatusMaintaining,
	"FLUSHING":     RedisStatusMaintaining,

	// 腾讯云
	"0":  RedisStatusCreating,    // 待初始化
	"1":  RedisStatusMaintaining, // 流程中
	"2":  RedisStatusRunning,     // 运行中
	"-2": RedisStatusDeleting,    // 已隔离
	"-3": RedisStatusDeleting,    // 待删除
}

// NormalizeRedisStatus 标准化Redis实例状态
func NormalizeRedisStatus(status string) string {
	if normalized, ok := redisStatusMapping[status]; ok {
		return normalized
	}
	if normalized, ok := redisStatusMapping[strings.ToUpper(status)]; ok {
		return normalized
	}
	lower := strings.ToLower(status)
	if lower == "" {
		return RedisStatusUnknown
	}
	return lower
}

// ============================================================================
// MongoDB 状态标准化
// ============================================================================

// MongoDB 标准化状态
const (
	MongoDBStatusRunning     = "running"     // 运行中
	MongoDBStatusCreating    = "creating"    // 创建中
	MongoDBStatusDeleting    = "deleting"    // 删除中
	MongoDBStatusRestarting  = "restarting"  // 重启中
	MongoDBStatusMaintaining = "maintaining" // 维护中
	MongoDBStatusUpgrading   = "upgrading"   // 升级中
	MongoDBStatusError       = "error"       // 异常
	MongoDBStatusUnknown     = "unknown"     // 未知
)

// MongoDB 状态映射表
var mongoDBStatusMapping = map[string]string{
	// 阿里云
	"Running":               MongoDBStatusRunning,
	"Creating":              MongoDBStatusCreating,
	"Deleting":              MongoDBStatusDeleting,
	"DBInstanceDeleting":    MongoDBStatusDeleting,
	"Rebooting":             MongoDBStatusRestarting,
	"Restoring":             MongoDBStatusMaintaining,
	"Importing":             MongoDBStatusMaintaining,
	"NodeCreating":          MongoDBStatusCreating,
	"NodeDeleting":          MongoDBStatusDeleting,
	"ClassChanging":         MongoDBStatusUpgrading,
	"NetAddressCreating":    MongoDBStatusMaintaining,
	"NetAddressDeleting":    MongoDBStatusMaintaining,
	"MinorVersionUpgrading": MongoDBStatusUpgrading,
	"MajorVersionUpgrading": MongoDBStatusUpgrading,
	"GuardSwitching":        MongoDBStatusMaintaining,
	"SSLModifying":          MongoDBStatusMaintaining,
	"LockMode":              MongoDBStatusError,

	// AWS DocumentDB
	"available":   MongoDBStatusRunning,
	"backing-up":  MongoDBStatusMaintaining,
	"creating":    MongoDBStatusCreating,
	"deleting":    MongoDBStatusDeleting,
	"failed":      MongoDBStatusError,
	"maintenance": MongoDBStatusMaintaining,
	"modifying":   MongoDBStatusMaintaining,
	"rebooting":   MongoDBStatusRestarting,
	"renaming":    MongoDBStatusMaintaining,
	"starting":    MongoDBStatusCreating,
	"stopped":     MongoDBStatusError,
	"stopping":    MongoDBStatusDeleting,
	"upgrading":   MongoDBStatusUpgrading,

	// 华为云 DDS
	"normal":     MongoDBStatusRunning,
	"ACTIVE":     MongoDBStatusRunning,
	"BUILD":      MongoDBStatusCreating,
	"FAILED":     MongoDBStatusError,
	"FROZEN":     MongoDBStatusError,
	"MODIFYING":  MongoDBStatusMaintaining,
	"REBOOTING":  MongoDBStatusRestarting,
	"RESTORING":  MongoDBStatusMaintaining,
	"SWITCHOVER": MongoDBStatusMaintaining,
	"MIGRATING":  MongoDBStatusMaintaining,
	"BACKING UP": MongoDBStatusMaintaining,
	"GROWING":    MongoDBStatusUpgrading,
	"RESTARTING": MongoDBStatusRestarting,

	// 腾讯云
	"0":  MongoDBStatusCreating,    // 待初始化
	"1":  MongoDBStatusMaintaining, // 流程执行中
	"2":  MongoDBStatusRunning,     // 运行中
	"-2": MongoDBStatusDeleting,    // 已过期

	// 火山引擎 (Running/Creating/Deleting 与阿里云相同，已在上方定义)
	"Restarting": MongoDBStatusRestarting,
	"Upgrading":  MongoDBStatusUpgrading,
	"Error":      MongoDBStatusError,
}

// NormalizeMongoDBStatus 标准化MongoDB实例状态
func NormalizeMongoDBStatus(status string) string {
	if normalized, ok := mongoDBStatusMapping[status]; ok {
		return normalized
	}
	if normalized, ok := mongoDBStatusMapping[strings.ToUpper(status)]; ok {
		return normalized
	}
	lower := strings.ToLower(status)
	if lower == "" {
		return MongoDBStatusUnknown
	}
	return lower
}

// ============================================================================
// SecurityGroup 状态标准化
// ============================================================================

// SecurityGroup 标准化状态
const (
	SecurityGroupStatusAvailable = "available" // 可用
	SecurityGroupStatusPending   = "pending"   // 创建中
	SecurityGroupStatusDeleting  = "deleting"  // 删除中
	SecurityGroupStatusUnknown   = "unknown"   // 未知
)

// securityGroupStatusMap 安全组状态映射表 (小写输入 → 规范状态)
var securityGroupStatusMap = map[string]string{
	"available": SecurityGroupStatusAvailable,
	"active":    SecurityGroupStatusAvailable,
	"pending":   SecurityGroupStatusPending,
	"creating":  SecurityGroupStatusPending,
	"deleting":  SecurityGroupStatusDeleting,
	"":          SecurityGroupStatusUnknown,
}

// NormalizeSecurityGroupStatus 标准化安全组状态
func NormalizeSecurityGroupStatus(status string) string {
	lower := strings.ToLower(status)
	if normalized, ok := securityGroupStatusMap[lower]; ok {
		return normalized
	}
	// 兜底：未匹配返回小写形式
	return lower
}

// ============================================================================
// Image 状态标准化
// ============================================================================

// Image 标准化状态
const (
	ImageStatusAvailable   = "available"   // 可用
	ImageStatusCreating    = "creating"    // 创建中
	ImageStatusWaiting     = "waiting"     // 等待中
	ImageStatusDeprecated  = "deprecated"  // 已弃用
	ImageStatusUnavailable = "unavailable" // 不可用
	ImageStatusError       = "error"       // 异常
	ImageStatusUnknown     = "unknown"     // 未知
)

// imageStatusMap 镜像状态映射表 (小写输入 → 规范状态)
var imageStatusMap = map[string]string{
	"available":    ImageStatusAvailable,
	"active":       ImageStatusAvailable,
	"normal":       ImageStatusAvailable,
	"using":        ImageStatusAvailable,
	"creating":     ImageStatusCreating,
	"pending":      ImageStatusCreating,
	"saving":       ImageStatusCreating,
	"syncing":      ImageStatusCreating,
	"transient":    ImageStatusCreating,
	"waiting":      ImageStatusWaiting,
	"queued":       ImageStatusWaiting,
	"deprecated":   ImageStatusDeprecated,
	"deregistered": ImageStatusDeprecated,
	"unavailable":  ImageStatusUnavailable,
	"deleted":      ImageStatusUnavailable,
	"error":        ImageStatusError,
	"failed":       ImageStatusError,
	"invalid":      ImageStatusError,
	"killed":       ImageStatusError,
	"":             ImageStatusUnknown,
}

// NormalizeImageStatus 标准化镜像状态
func NormalizeImageStatus(status string) string {
	lower := strings.ToLower(status)
	if normalized, ok := imageStatusMap[lower]; ok {
		return normalized
	}
	// 兜底：未匹配返回小写形式
	return lower
}

// ============================================================================
// Disk 状态标准化
// ============================================================================

// Disk 标准化状态
const (
	DiskStatusAvailable = "available" // 可用 (未挂载)
	DiskStatusInUse     = "in_use"    // 使用中 (已挂载)
	DiskStatusCreating  = "creating"  // 创建中
	DiskStatusAttaching = "attaching" // 挂载中
	DiskStatusDetaching = "detaching" // 卸载中
	DiskStatusDeleting  = "deleting"  // 删除中
	DiskStatusReIniting = "reiniting" // 重置中
	DiskStatusError     = "error"     // 异常
	DiskStatusUnknown   = "unknown"   // 未知
)

// diskStatusMap 云盘状态映射表 (小写输入 → 规范状态)
var diskStatusMap = map[string]string{
	"available":        DiskStatusAvailable,
	"unattached":       DiskStatusAvailable,
	"in_use":           DiskStatusInUse,
	"in-use":           DiskStatusInUse,
	"attached":         DiskStatusInUse,
	"creating":         DiskStatusCreating,
	"uploading":        DiskStatusCreating,
	"downloading":      DiskStatusCreating,
	"extending":        DiskStatusCreating,
	"expanding":        DiskStatusCreating,
	"attaching":        DiskStatusAttaching,
	"detaching":        DiskStatusDetaching,
	"deleting":         DiskStatusDeleting,
	"deleted":          DiskStatusDeleting,
	"torecycle":        DiskStatusDeleting,
	"reiniting":        DiskStatusReIniting,
	"rollbacking":      DiskStatusReIniting,
	"error":            DiskStatusError,
	"error_extending":  DiskStatusError,
	"error_deleting":   DiskStatusError,
	"error_restoring":  DiskStatusError,
	"error_rollbacking": DiskStatusError,
	"all":              DiskStatusUnknown,
	"":                 DiskStatusUnknown,
}

// NormalizeDiskStatus 标准化云盘状态
func NormalizeDiskStatus(status string) string {
	lower := strings.ToLower(status)
	if normalized, ok := diskStatusMap[lower]; ok {
		return normalized
	}
	// 兜底：未匹配返回小写形式
	return lower
}

// IsDiskAvailable 判断云盘是否可用 (未挂载)
func IsDiskAvailable(status string) bool {
	return NormalizeDiskStatus(status) == DiskStatusAvailable
}

// IsDiskInUse 判断云盘是否使用中 (已挂载)
func IsDiskInUse(status string) bool {
	return NormalizeDiskStatus(status) == DiskStatusInUse
}

// ============================================================================
// Snapshot 状态标准化
// ============================================================================

// Snapshot 标准化状态
const (
	SnapshotStatusNormal       = "normal"       // 正常
	SnapshotStatusProgressing  = "progressing"  // 创建中
	SnapshotStatusAccomplished = "accomplished" // 已完成
	SnapshotStatusFailed       = "failed"       // 失败
	SnapshotStatusDeleting     = "deleting"     // 删除中
	SnapshotStatusUnknown      = "unknown"      // 未知
)

// snapshotStatusMap 快照状态映射表 (小写输入 → 规范状态)
var snapshotStatusMap = map[string]string{
	"normal":        SnapshotStatusNormal,
	"progressing":   SnapshotStatusProgressing,
	"pending":       SnapshotStatusProgressing,
	"creating":      SnapshotStatusProgressing,
	"rollbacking":   SnapshotStatusProgressing,
	"backing_up":    SnapshotStatusProgressing,
	"copying":       SnapshotStatusProgressing,
	"accomplished":  SnapshotStatusAccomplished,
	"completed":     SnapshotStatusAccomplished,
	"available":     SnapshotStatusAccomplished,
	"failed":        SnapshotStatusFailed,
	"error":         SnapshotStatusFailed,
	"error_deleting": SnapshotStatusFailed,
	"deleting":      SnapshotStatusDeleting,
	"torecycle":     SnapshotStatusDeleting,
	"":              SnapshotStatusUnknown,
}

// NormalizeSnapshotStatus 标准化快照状态
func NormalizeSnapshotStatus(status string) string {
	lower := strings.ToLower(status)
	if normalized, ok := snapshotStatusMap[lower]; ok {
		return normalized
	}
	// 兜底：未匹配返回小写形式
	return lower
}

// IsSnapshotCompleted 判断快照是否已完成
func IsSnapshotCompleted(status string) bool {
	return NormalizeSnapshotStatus(status) == SnapshotStatusAccomplished
}
