package aws

// CreateUserParams AWS 创建用户组参数
type CreateUserParams struct {
	Username string
	Path     string
	Tags     map[string]string
}
