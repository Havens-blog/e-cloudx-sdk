# e-cloudx-sdk

多云统一 SDK —— 从 [e-cam-service](https://github.com/Havens-blog/e-cam-service) 抽取的独立共享库，为上层服务提供统一的多云资产采集、IAM、账单、日志查询等能力。

## 安装

```bash
go get github.com/Havens-blog/e-cloudx-sdk@v0.1.0
```

要求 Go 1.25+。

## 支持的云厂商

| Provider | 包 |
|----------|-----|
| 阿里云 | `aliyun` |
| AWS | `aws` |
| 腾讯云 | `tencent` |
| 华为云 | `huawei` |
| 火山引擎 | `volcano` |
| Azure | `azure` |

## 能力域

```
domain/      云账号 / 云厂商 / IAM 等共享领域模型
providers/   各厂商 Provider 统一抽象与工厂
iam/         统一身份与访问管理
billing/     账单采集
logquery/    日志查询
asset/       资产采集
nasprobe/    NAS 探测
common/      通用类型与工具
types/       公共类型定义
```

## 用法示例

```go
import (
    "github.com/Havens-blog/e-cloudx-sdk/domain"
    "github.com/Havens-blog/e-cloudx-sdk/providers"
)

// 领域模型：云账号、云厂商、附件模型 UID 规则等
pattern := domain.AttachmentModelUIDPattern()

// Provider 工厂：按云厂商创建统一抽象的客户端
// provider, err := providers.New(...)
```

## 依赖

- [e-common-go](https://github.com/Havens-blog/e-common-go) — 通用工具库（crypto 等）
- 各云厂商官方 Go SDK

## 版本

- `v0.1.0` — 从 e-cam-service 首次抽取发布
