# 火山云 IAM 适配器

## 状态

🚧 **待实现** - 需要确认火山云 SDK 的具体 API 结构

## 原因

火山云 Go SDK 的 IAM 服务 API 结构与预期不同，需要：

1. 查阅火山云官方 SDK 文档
2. 确认正确的 API 调用方式
3. 了解数据结构定义

## 实现计划

### 1. SDK 调研

- 查阅火山云 IAM API 文档
- 确认 Go SDK 的使用方式
- 了解用户、策略等数据结构

### 2. 实现文件

- `adapter.go` - 核心适配器实现
- `converter.go` - 数据转换
- `types.go` - 类型定义
- `wrapper.go` - 接口包装

### 3. 功能清单

- [ ] ValidateCredentials - 验证凭证
- [ ] ListUsers - 列出用户
- [ ] GetUser - 获取用户详情
- [ ] CreateUser - 创建用户
- [ ] DeleteUser - 删除用户
- [ ] UpdateUserPermissions - 更新权限
- [ ] ListPolicies - 列出策略

## 参考资料

- [火山云官方文档](https://www.volcengine.com/docs)
- [火山云 Go SDK](https://github.com/volcengine/volcengine-go-sdk)

## 临时方案

在火山云适配器完成前，工厂会返回"尚未实现"错误。
