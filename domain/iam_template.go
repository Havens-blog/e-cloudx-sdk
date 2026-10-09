package domain

import (
	"fmt"
	"time"
)

// TemplateCategory 模板分类
type TemplateCategory string

const (
	TemplateCategoryReadOnly  TemplateCategory = "read_only"
	TemplateCategoryAdmin     TemplateCategory = "admin"
	TemplateCategoryDeveloper TemplateCategory = "developer"
	TemplateCategoryCustom    TemplateCategory = "custom"
)

// PolicyTemplate 策略模板领域模型
type PolicyTemplate struct {
	ID             int64              `json:"id" bson:"id"`
	Name           string             `json:"name" bson:"name"`
	Description    string             `json:"description" bson:"description"`
	Category       TemplateCategory   `json:"category" bson:"category"`
	Policies       []PermissionPolicy `json:"policies" bson:"policies"`
	CloudPlatforms []CloudProvider    `json:"cloud_platforms" bson:"cloud_platforms"`
	IsBuiltIn      bool               `json:"is_built_in" bson:"is_built_in"`
	TenantID       int64              `json:"tenant_id" bson:"tenant_id"`
	CreateTime     time.Time          `json:"create_time" bson:"create_time"`
	UpdateTime     time.Time          `json:"update_time" bson:"update_time"`
	CTime          int64              `json:"ctime" bson:"ctime"`
	UTime          int64              `json:"utime" bson:"utime"`
}

// TemplateFilter 模板查询过滤器
type TemplateFilter struct {
	Category  TemplateCategory `json:"category"`
	IsBuiltIn *bool            `json:"is_built_in"`
	TenantID  int64            `json:"tenant_id"`
	Keyword   string           `json:"keyword"`
	Offset    int64            `json:"offset"`
	Limit     int64            `json:"limit"`
}

// CreateTemplateRequest 创建模板请求
type CreateTemplateRequest struct {
	Name           string             `json:"name" binding:"required,min=1,max=100"`
	Description    string             `json:"description" binding:"max=500"`
	Category       TemplateCategory   `json:"category" binding:"required"`
	Policies       []PermissionPolicy `json:"policies"`
	CloudPlatforms []CloudProvider    `json:"cloud_platforms" binding:"required,min=1"`
	TenantID       int64              `json:"tenant_id"`
}

// UpdateTemplateRequest 更新模板请求
type UpdateTemplateRequest struct {
	Name           *string            `json:"name,omitempty"`
	Description    *string            `json:"description,omitempty"`
	Policies       []PermissionPolicy `json:"policies,omitempty"`
	CloudPlatforms []CloudProvider    `json:"cloud_platforms,omitempty"`
}

// 领域方法

// Validate 验证模板数据
func (t *PolicyTemplate) Validate() error {
	if t.Name == "" {
		return fmt.Errorf("template name cannot be empty")
	}
	if t.Category == "" {
		return fmt.Errorf("category cannot be empty")
	}
	if len(t.CloudPlatforms) == 0 {
		return fmt.Errorf("cloud platforms cannot be empty")
	}
	if !t.IsBuiltIn && t.TenantID == 0 {
		return fmt.Errorf("tenant id cannot be empty for custom template")
	}
	return nil
}

// IsEditable 判断模板是否可编辑
func (t *PolicyTemplate) IsEditable() bool {
	return !t.IsBuiltIn
}
