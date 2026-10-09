package domain

import (
	"fmt"
	"time"
)

// SyncTaskType 同步任务类型
type SyncTaskType string

const (
	SyncTaskTypeUserSync       SyncTaskType = "user_sync"
	SyncTaskTypePermissionSync SyncTaskType = "permission_sync"
	SyncTaskTypeGroupSync      SyncTaskType = "group_sync"
	SyncTaskTypeBatchUserSync  SyncTaskType = "batch_user_sync" // 批量用户同步（从云平台拉取所有用户）
)

// SyncTargetType 同步目标类型
type SyncTargetType string

const (
	SyncTargetTypeUser    SyncTargetType = "user"
	SyncTargetTypeGroup   SyncTargetType = "group"
	SyncTargetTypeAccount SyncTargetType = "account" // 云账号级别同步
)

// SyncTaskStatus 同步任务状态
type SyncTaskStatus string

const (
	SyncTaskStatusPending  SyncTaskStatus = "pending"
	SyncTaskStatusRunning  SyncTaskStatus = "running"
	SyncTaskStatusSuccess  SyncTaskStatus = "success"
	SyncTaskStatusFailed   SyncTaskStatus = "failed"
	SyncTaskStatusRetrying SyncTaskStatus = "retrying"
)

// SyncTask 同步任务领域模型
type SyncTask struct {
	ID             int64          `json:"id" bson:"id"`
	TaskType       SyncTaskType   `json:"task_type" bson:"task_type"`
	TargetType     SyncTargetType `json:"target_type" bson:"target_type"`
	TargetID       int64          `json:"target_id" bson:"target_id"`
	CloudAccountID int64          `json:"cloud_account_id" bson:"cloud_account_id"`
	Provider       CloudProvider  `json:"provider" bson:"provider"`
	Status         SyncTaskStatus `json:"status" bson:"status"`
	Progress       int            `json:"progress" bson:"progress"`
	RetryCount     int            `json:"retry_count" bson:"retry_count"`
	MaxRetries     int            `json:"max_retries" bson:"max_retries"`
	ErrorMessage   string         `json:"error_message" bson:"error_message"`
	StartTime      *time.Time     `json:"start_time" bson:"start_time"`
	EndTime        *time.Time     `json:"end_time" bson:"end_time"`
	CreateTime     time.Time      `json:"create_time" bson:"create_time"`
	UpdateTime     time.Time      `json:"update_time" bson:"update_time"`
	CTime          int64          `json:"ctime" bson:"ctime"`
	UTime          int64          `json:"utime" bson:"utime"`
}

// SyncTaskFilter 同步任务查询过滤器
type SyncTaskFilter struct {
	TaskType       SyncTaskType   `json:"task_type"`
	Status         SyncTaskStatus `json:"status"`
	CloudAccountID int64          `json:"cloud_account_id"`
	Provider       CloudProvider  `json:"provider"`
	Offset         int64          `json:"offset"`
	Limit          int64          `json:"limit"`
}

// CreateSyncTaskRequest 创建同步任务请求
type CreateSyncTaskRequest struct {
	TaskType       SyncTaskType   `json:"task_type" binding:"required"`
	TargetType     SyncTargetType `json:"target_type" binding:"required"`
	TargetID       int64          `json:"target_id" binding:"required"`
	CloudAccountID int64          `json:"cloud_account_id" binding:"required"`
	Provider       CloudProvider  `json:"provider" binding:"required"`
}

// 领域方法

// Validate 验证同步任务数据
func (t *SyncTask) Validate() error {
	if t.TaskType == "" {
		return fmt.Errorf("task type cannot be empty")
	}
	if t.TargetType == "" {
		return fmt.Errorf("target type cannot be empty")
	}
	if t.TargetID == 0 {
		return fmt.Errorf("target id cannot be empty")
	}
	if t.CloudAccountID == 0 {
		return fmt.Errorf("cloud account id cannot be empty")
	}
	if t.Provider == "" {
		return fmt.Errorf("provider cannot be empty")
	}
	return nil
}

// IsPending 判断任务是否为待执行状态
func (t *SyncTask) IsPending() bool {
	return t.Status == SyncTaskStatusPending
}

// IsRunning 判断任务是否正在执行
func (t *SyncTask) IsRunning() bool {
	return t.Status == SyncTaskStatusRunning
}

// IsCompleted 判断任务是否已完成
func (t *SyncTask) IsCompleted() bool {
	return t.Status == SyncTaskStatusSuccess || t.Status == SyncTaskStatusFailed
}

// CanRetry 判断任务是否可以重试
func (t *SyncTask) CanRetry() bool {
	return t.Status == SyncTaskStatusFailed && t.RetryCount < t.MaxRetries
}

// MarkAsRunning 标记任务为执行中
func (t *SyncTask) MarkAsRunning() {
	t.Status = SyncTaskStatusRunning
	now := time.Now()
	t.StartTime = &now
	t.UpdateTime = now
	t.UTime = now.Unix()
}

// MarkAsSuccess 标记任务为成功
func (t *SyncTask) MarkAsSuccess() {
	t.Status = SyncTaskStatusSuccess
	t.Progress = 100
	now := time.Now()
	t.EndTime = &now
	t.UpdateTime = now
	t.UTime = now.Unix()
}

// MarkAsFailed 标记任务为失败
func (t *SyncTask) MarkAsFailed(errorMsg string) {
	t.Status = SyncTaskStatusFailed
	t.ErrorMessage = errorMsg
	now := time.Now()
	t.EndTime = &now
	t.UpdateTime = now
	t.UTime = now.Unix()
}

// IncrementRetry 增加重试次数
func (t *SyncTask) IncrementRetry() {
	t.RetryCount++
	t.Status = SyncTaskStatusRetrying
	t.UpdateTime = time.Now()
	t.UTime = t.UpdateTime.Unix()
}

// UpdateProgress 更新进度
func (t *SyncTask) UpdateProgress(progress int) {
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	t.Progress = progress
	t.UpdateTime = time.Now()
	t.UTime = t.UpdateTime.Unix()
}
