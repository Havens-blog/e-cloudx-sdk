package domain

import (
	"errors"
	"time"
)

var (
	// ErrTenantIDRequired 租户ID必填错误
	ErrTenantIDRequired = errors.New("tenant id is required")
)

// TenantResource 租户资源基础结构
// 所有需要租户隔离的资源都可以嵌入这个结构
type TenantResource struct {
	TenantID int64 `json:"tenant_id" bson:"tenant_id"`
}

// Validate 验证租户ID
func (t *TenantResource) Validate() error {
	if t.TenantID == 0 {
		return ErrTenantIDRequired
	}
	return nil
}

// BelongsToTenant 检查资源是否属于指定租户
func (t *TenantResource) BelongsToTenant(tenantID int64) bool {
	return t.TenantID == tenantID
}

// TimeStamps 时间戳基础结构
type TimeStamps struct {
	CreateTime time.Time `json:"create_time" bson:"create_time"`
	UpdateTime time.Time `json:"update_time" bson:"update_time"`
	CTime      int64     `json:"ctime" bson:"ctime"`
	UTime      int64     `json:"utime" bson:"utime"`
}

// UpdateTimestamp 更新时间戳
func (t *TimeStamps) UpdateTimestamp() {
	now := time.Now()
	t.UpdateTime = now
	t.UTime = now.Unix()
}

// InitTimestamp 初始化时间戳
func (t *TimeStamps) InitTimestamp() {
	now := time.Now()
	t.CreateTime = now
	t.UpdateTime = now
	t.CTime = now.Unix()
	t.UTime = now.Unix()
}

// BaseModel 基础模型，包含租户和时间戳
type BaseModel struct {
	TenantResource
	TimeStamps
}
