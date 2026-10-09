package types

// CreateUserRequest 创建用户请求（所有云厂商IAM适配器通用）
type CreateUserRequest struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

// CreateGroupRequest 创建用户组请求（所有云厂商IAM适配器通用）
type CreateGroupRequest struct {
	GroupName   string `json:"group_name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}
