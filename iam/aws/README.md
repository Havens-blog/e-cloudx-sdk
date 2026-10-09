.html)asiam-quotce_de/referenerGuilatest/Us/IAM/amazon.comcs.aws.dotps://M 配额](ht

- [IAtices.html)best-pracGuide//latest/User/IAMom.amazon.cawss.//doc 践](https:- [IAM 最佳实)
  2/-sdk-go-v.io/awsws.githubs://a Go v2](http for SDK [AWS
  -ence/)Refer/latest/APIon.com/IAMocs.aws.amazttps://d 文档](hAWS IAM API

- [考资料

## 参中户不在任何组策略

3. 确保用
4. 确保已分离所有 cessKey 有 Ac
5. 确保已删除所案\*\*:错误

**解决方 ict` `DeleteConfl 状**: 除用户失败
\*\*症 3: 删 问题 量操作

###3. 使用批 2. 增加重试次数
限制
降低 QPS 决方案\*\*:

1. ` 错误

**解 ptionceottlingEx 状**: 大量 `Thr 错误频繁
\*\*症### 问题 2: 限流查账号是否被禁用

检 ser`权限
3.`iam:GetU. 确认凭证有确
2 是否正 KeySecret AccessccessKeyID 和\*\*:

1. 检查 A 错误

\*_解决方案 entials` credinvalid aws_: `*症状*凭证验证失败
\*# 问题 1: # 故障排查

##

#.

````
/aws/..am/cloudx/iernal/shared ./intrationeg-tags=int test
goetyour-secrSS_KEY=CRET_ACCErt AWS_SExpor-key
eEY_ID=youS_KWS_ACCESexport A证
 AWS 凭 需要配置
#bash试
```# 集成测```

##..
iam/aws/.x/ared/cloudernal/sht ./intsh
go tes```ba试
## 单元测
## 测试

#日志
 定期审查IAM 操作
-ail
- 记录所有 Tr启用 Cloud
- 审计日志

### 3.
```]
}  }
     "*"
rce":"Resou     ,

      ]ies"icPolistam:L"i      cy",
  PolihUserDetac      "iam:
  y",achUserPolic "iam:Att    ,
   olicies"ttachedUserPistAm:L       "ia",
 ssKeyleteAcceam:De     "i
   ys",sKeestAccis"iam:L",
        rteUse  "iam:Dele      ateUser",
re    "iam:C
    ,Users""iam:List        ",
am:GetUser "i      ion": [
      "Act",
 llow "Act":  "Effe

    {: ["Statement"
  0-17",: "2012-1rsion"
{
  "Ve

```json2. 必需权限
### 原则

- 最小权限AccessKey
- 定期轮换 户或角色
- 使用 IAM 用 凭证管理# 1.# 安全建议

## 调用次数

#- 减少 APIPI
量 A
- 尽量使用批. 批量操作制

### 3率限I 速过 AP发请求
- 避免超 限流器控制并并发控制
-
### 2.  自动处理分页标记
多 100 条记录
-or
- 每页最K 的 Paginat用 AWS SD分页处理
- 使
### 1.
## 性能优化

````

tomcyTypeCus Poliolicy" →olicy/MyP012:p456789am::123"arn:aws:i 略
/ 客户托管策

/stemeSyicyTyp" → Polesscc/ReadOnlyA::aws:policyamn:aws:i
"ar 托管策略/ AWS

```go
/
yTypeliccy ARN → PoPoli
```

### ,

} }.Tags),
s(iamUsernvertTag co Tags:,
astUsedPasswordLiamUser.rdLastSet: sswo Pa: {
MetadataateDate,
User.Cream iateTime: CreerId,
.Us: iamUserserIDloudU
CderAWS,Provi Cloudider: ProvMUser,
IATypeerudUslo Ce: ypUserT,
UserNamemUser.: iamerna UseudUser{
Clo
`go

``CloudUserser → # IAM U

##

## 数据转换

重试限流错误

````30s
// 只大退避:  最, ...
//s, 2s, 4s 1退避时间:重试 3 次
//  最多//

```go
试策略

### 重不重试理: 返回错误，名已存在
   - 处: 用户
   - 原因s**dyExistea**EntityAlr重试

4. 误，不返回错 - 处理: : 凭证权限不足
   原因nied**
   -**AccessDe试

3. 处理: 返回错误，不重  -
  用户或策略不存在原因:-
Entity**
2. **NoSuch，指数退避
处理: 自动重试
   - PI 速率限制因: 超过 A - 原*
  tion*gExcephrottlin
1. **T
### 常见错误
处理

## 错误 个托管策略
**: 最多 10用户策略 个
- **每量**: 默认 1500 **策略数000 个
-户数量**: 默认 5- **用
### 配额限制
用
作减少 API 调建议**: 使用批量操账号类型不同
- ****: 根据**AWS 限制: 10 QPS
- *默认限流**制
- *

### 速率限 限制
## APIKey 数量
cessser` 查询 Ac`GetU
- 可通过  AccessKey删除用户前自动删除所有essKey
- cc A 创建用户时不自动创建 管理

-ccessKey
### A",
}
````

.comdoe@example"john. Email":  
 "","John DoeayName":
"Displring{ing]sttr
Tags: map[s

```go储为标签：il` 存 ame`和`EmayNla 适配器会将 `Disp 持为用户添加标签，AWS 支# 用户标签

权限

##可自定义理

- 由客户创建和管
  -PolicyName`licy/012:po3456789:iam::12式: `arn:aws- ARN 格 om)
  (Cust **客户托管策略** ess`

2.owerUserAccess`, `PyAcc 示例: `ReadOnl 建和维护

- 由 AWS 创
  -yName`olicy/Polics:iam::aws:p 格式: `arn:aw)
- ARN(System 托管策略** 1. **AWS 型

性

### 策略类# AWS IAM 特

````

#s)iepolicjohn.doe", ", account, ons(ctxrPermissieUser.Updatdapte := a,
}

err   }peSystem,
 cyTydomain.PoliType: Policy        rAWS,
oudProvideCl  domain.der:      Provi   ",
nlyAccessdOName: "Rea      Policy",
  dOnlyAccessy/Rea:aws:policaws:iam: "arn:yID:  lic    Po

    {ionPolicy{main.Permiss := []do
policies

```go权限## 更新用户``

#unt, req)
`ctx, accoser(ateUr.Cre := adapte
user, err",
}
mple.coxamjohn.doe@e"         Email:
  "John Doe",playName: Dis    n.doe",
"joh rname:      Usest{
 erRequeUs.Create= &typesq :re

```go
### 创建用户

}
````

serID).CloudUme, userr.Userna", use\nID: %s) ("User: %sntf( fmt.Prisers {
e u:= rangser \_, ucount)
for (ctx, acListUsers := adapter. errgo
users,列出用户

```### t)

```

coun actials(ctx,denlidateCre adapter.Va
}

err :=",tenant-001 " TenantID: ",
PLEKEYAMPxRfiCYEXENG/bMDtnFEMI/K7lrXUwJacret: "ccessKeySe A",
MPLEIOSFODNN7EXAIA "AK KeyID: Access 1,
D:  
 IloudAccount{main.Count := &do

````go
acc### 验证凭证
``


`roviderAWS)main.CloudPdoapter(eAdctory.Creatr := faadapter, er)
ry(loggerctoAMAdapterFaCloudI= iam.New
factory :/ 通过工厂创建in"
)

/shared/domace/internal/-cam-servi-blog/eom/Havens"github.c"
    cloudx/iamd/nal/shareervice/inter-sam-cavens-blog/eom/H"github.ct (

impor配器

```go

### 创建适例## 使用示nied`

essDe`Acc** - **权限错误✅ ity`
- NoSuchEnt在** - `源不存*资
- ✅ *Exception`Requests`TooMany, xception`lingE* - `Thrott- ✅ **限流错误* 4. 错误处理
 秒

###隔 30 次，最大间- 最多重试 3✅ **指数退避** - on 并重试
ceptiingEx测 Throttl - 检 **自动重试**
- ✅PS流器** - 10 Q**令牌桶限
- ✅ 限流和重试

### 3. 分页获取所有托管策略es** - ci **ListPoli客户托管策略
- ✅ 托管策略和 AWS
  - 支持略策略
  - 附加新策需要的离不 - 自动分户策略
 ns** - 智能同步用rmissioteUserPe*Upda✅ * 权限管理
-
### 2.ey 和策略）
essKAcc- 删除用户（自动清理 ser**  ✅ **DeleteU 用户（支持标签）
- 创建 IAM* -r*CreateUse 数量
- ✅ **eyessK 获取用户详情和 Acc*GetUser** -AM 用户
- ✅ * - 分页获取所有 Irs**tUse✅ **Lis- 理

### 1. 用户管## 功能特性

````

    # 本文档E.md   └── READM # 接口包装器

rapper.go  
├── w # 类型定义 go types.数据转换工具
├── o # r.gtenver
├── co 核心适配器实现 #o r.gpte/
├── ada

```
aws
## 文件结构一访问。
(IAM) 服务的统nagement Mad Access y anWS Identit` 接口，提供对 AteroudIAMAdap器实现了 `Cl IAM 适配

AWS# 概述AM 适配器

#WS I# A
```
