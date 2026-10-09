package domain

import (
	"time"
)

// AuditOperationType 审计操作类型
type AuditOperationType string

const (
	AuditOpCreateUser       AuditOperationType = "create_user"
	AuditOpUpdateUser       AuditOperationType = "update_user"
	AuditOpDeleteUser       AuditOperationType = "delete_user"
	AuditOpCreateGroup      AuditOperationType = "create_group"
	AuditOpUpdateGroup      AuditOperationType = "update_group"
	AuditOpDeleteGroup      AuditOperationType = "delete_group"
	AuditOpAssignPermission AuditOperationType = "assign_permission"
	AuditOpRevokePermission AuditOperationType = "revoke_permission"
	AuditOpSyncUser         AuditOperationType = "sync_user"
	AuditOpSyncPermission   AuditOperationType = "sync_permission"
)

// AuditResult 审计结果
type AuditResult string

const (
	AuditResultSuccess AuditResult = "success"
	AuditResultFailed  AuditResult = "failed"
)

// AuditLog 审计日志领域模型
type AuditLog struct {
	ID            int64              `json:"id" bson:"id"`
	OperationType AuditOperationType `json:"operation_type" bson:"operation_type"`
	OperatorID    string             `json:"operator_id" bson:"operator_id"`
	OperatorName  string             `json:"operator_name" bson:"operator_name"`
	TargetType    string             `json:"target_type" bson:"target_type"`
	TargetID      int64              `json:"target_id" bson:"target_id"`
	TargetName    string             `json:"target_name" bson:"target_name"`
	CloudPlatform CloudProvider      `json:"cloud_platform" bson:"cloud_platform"`
	BeforeValue   string             `json:"before_value" bson:"before_value"`
	AfterValue    string             `json:"after_value" bson:"after_value"`
	Result        AuditResult        `json:"result" bson:"result"`
	ErrorMessage  string             `json:"error_message" bson:"error_message"`
	IPAddress     string             `json:"ip_address" bson:"ip_address"`
	UserAgent     string             `json:"user_agent" bson:"user_agent"`
	TenantID      int64              `json:"tenant_id" bson:"tenant_id"`
	CreateTime    time.Time          `json:"create_time" bson:"create_time"`
	CTime         int64              `json:"ctime" bson:"ctime"`
}

// AuditLogFilter 审计日志查询过滤器
type AuditLogFilter struct {
	OperationType AuditOperationType `json:"operation_type"`
	OperatorID    string             `json:"operator_id"`
	TargetType    string             `json:"target_type"`
	CloudPlatform CloudProvider      `json:"cloud_platform"`
	TenantID      int64              `json:"tenant_id"`
	StartTime     *time.Time         `json:"start_time"`
	EndTime       *time.Time         `json:"end_time"`
	Offset        int64              `json:"offset"`
	Limit         int64              `json:"limit"`
}

// ExportFormat 导出格式
type ExportFormat string

const (
	ExportFormatCSV  ExportFormat = "csv"
	ExportFormatJSON ExportFormat = "json"
)

// AuditReportRequest 审计报告请求
type AuditReportRequest struct {
	StartTime *time.Time `json:"start_time" binding:"required"`
	EndTime   *time.Time `json:"end_time" binding:"required"`
	TenantID  int64      `json:"tenant_id"`
}

// AuditReport 审计报告
type AuditReport struct {
	StartTime     time.Time                    `json:"start_time"`
	EndTime       time.Time                    `json:"end_time"`
	TotalOps      int64                        `json:"total_ops"`
	SuccessOps    int64                        `json:"success_ops"`
	FailedOps     int64                        `json:"failed_ops"`
	OpsByType     map[AuditOperationType]int64 `json:"ops_by_type"`
	OpsByPlatform map[CloudProvider]int64      `json:"ops_by_platform"`
	TopOperators  []OperatorStats              `json:"top_operators"`
	GeneratedAt   time.Time                    `json:"generated_at"`
}

// OperatorStats 操作人统计
type OperatorStats struct {
	OperatorID   string `json:"operator_id"`
	OperatorName string `json:"operator_name"`
	OpCount      int64  `json:"op_count"`
}
