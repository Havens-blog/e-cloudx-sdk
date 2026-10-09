# 腾讯云 CAM 适配器

## 概述

腾讯云 CAM (Cloud Access Management) 适配器实现了 `CloudIAMAdapter` 接口，用于管理腾讯云 CAM 用户、用户组和权限策略。

## 实现状态

### ⏳ 待实现

当前所有方法都是占位符实现，需要集成腾讯云 Go SDK 后完成具体实现。

#### 用户管理

- ⏳ `ListUsers`: 获取 CAM 用户列表
- ⏳ `GetUser`: 获取用户详情
- ⏳ `CreateUser`: 创建 CAM 用户
- ⏳ `DeleteUser`: 删除 CAM 用户
- ⏳ `UpdateUserPermissions`: 更新用户权限

#### 用户组管理

- ⏳ `ListGroups`: 获取用户组列表
- ⏳ `GetGroup`: 获取用户组详情
- ⏳ `CreateGroup`: 创建用户组
- ⏳ `UpdateGroupPolicies`: 更新用户组权限策略
- ⏳ `DeleteGroup`: 删除用户组
- ⏳ `ListGroupUsers`: 获取用户组成员列表
- ⏳ `AddUserToGroup`: 添加用户到用户组
- ⏳ `RemoveUserFromGroup`: 从用户组移除用户

#### 策略管理

- ⏳ `ListPolicies`: 获取权限策略列表
- ⏳ `GetPolicy`: 获取策略详情

#### 凭证验证

- ⏳ `ValidateCredentials`: 验证凭证

## 技术规格

### 限流配置

- QPS 限制: 15 请求/秒
- 使用 `golang.org/x/time/rate` 实现令牌桶限流

### 重试策略

- 最大重试次数: 3 次
- 使用指数退避策略
- 需要实现腾讯云限流错误检测

## 依赖

### 需要添加的依赖

```bash
# 腾讯云 Go SDK
go get github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cam/v20190116
go get github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common
```

### 腾讯云 CAM API 文档

- [腾讯云 CAM API 参考](https://cloud.tencent.com/document/product/598/13896)
- [腾讯云 Go SDK 文档](https://github.com/TencentCloud/tencentcloud-sdk-go)

## 实现步骤

### 1. 添加腾讯云 SDK 依赖

```bash
go get github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cam/v20190116
```

### 2. 创建腾讯云客户端工具

在 `internal/shared/cloudx/common/tencent/` 目录下创建：

- `client.go`: 客户端创建和配置
- `error.go`: 错误处理工具
- `rate_limiter.go`: 限流器（如果需要自定义）

### 3. 实现用户管理方法

参考阿里云和 AWS 的实现：

- 使用腾讯云 CAM SDK 调用相应的 API
- 实现数据转换（腾讯云类型 -> 领域模型）
- 添加错误处理和重试逻辑
- 添加详细的日志记录

### 4. 实现用户组管理方法

- 实现用户组 CRUD 操作
- 实现智能策略更新（对比并增量更新）
- 实现安全删除（自动清理成员和策略）

### 5. 实现策略管理方法

- 获取策略列表
- 获取策略详情（包含策略文档）

### 6. 实现凭证验证

- 调用腾讯云 API 验证 SecretId/SecretKey 是否有效
- 验证权限是否足够

### 7. 更新转换器

在 `converter.go` 中实现：

- `ConvertTencentUserToCloudUser`: 转换用户数据
- `ConvertTencentGroupToPermissionGroup`: 转换用户组数据
- `ConvertPolicyType`: 转换策略类型

### 8. 测试

- 编写单元测试
- 编写集成测试
- 测试限流和重试机制

## 腾讯云 CAM 特性

### 用户管理

- 支持子用户和协作者
- 支持用户标签
- 支持用户备注

### 用户组管理

- 用户组可以包含多个用户
- 用户组可以关联多个策略
- 支持用户组备注

### 策略管理

- 预设策略（腾讯云预定义）
- 自定义策略
- 支持基于角色的访问控制（RBAC）

### API 限制

- 每个主账号最多 1000 个子用户
- 每个用户最多加入 10 个用户组
- 每个用户组最多关联 100 个策略

## 注意事项

1. **权限要求**: 确保使用的 SecretId/SecretKey 具有足够的 CAM 权限
2. **区域配置**: 腾讯云 CAM 是全局服务，不需要指定区域
3. **限流处理**: 注意腾讯云的 API 限流策略，实现合理的重试机制
4. **错误处理**: 腾讯云的错误码和错误信息需要正确解析和处理

## 参考实现

可以参考以下已实现的适配器：

- `internal/shared/cloudx/iam/aliyun/`: 阿里云 RAM 适配器
- `internal/shared/cloudx/iam/aws/`: AWS IAM 适配器

这些实现提供了完整的用户、用户组和策略管理功能，可以作为腾讯云实现的参考。
