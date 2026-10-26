package aliyun

import (
	"testing"
	"time"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	bgp "github.com/aliyun/alibaba-cloud-sdk-go/services/ddosbgp"
	coo "github.com/aliyun/alibaba-cloud-sdk-go/services/ddoscoo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertDDoSBGPInstanceToDDOS(t *testing.T) {
	t.Run("全字段映射", func(t *testing.T) {
		inst := bgp.Instance{
			InstanceId:  "ddosbgp-cn-pe33xxxxxxxx",
			Remark:      "生产原生防护包",
			Status:      "2", // 状态原值透出,不做语义转换
			ExpireTime:  1767225599000,
			GmtCreate:   1700000000000,
			AutoRenewal: true,
			Desc:        "核心业务防护",
		}

		got := convertDDoSBGPInstanceToDDOS(inst, "cn-hangzhou")

		assert.Equal(t, "ddosbgp-cn-pe33xxxxxxxx", got.InstanceID)
		assert.Equal(t, "生产原生防护包", got.InstanceName)
		assert.Equal(t, "2", got.Status)
		assert.Equal(t, "cn-hangzhou", got.Region)
		assert.Equal(t, "DDoS原生防护", got.Edition)
		assert.True(t, got.AutoRenew)
		assert.Equal(t, "核心业务防护", got.Description)
		assert.Equal(t, "aliyun", got.Provider)
		// 时间口径与 waf.go 一致:毫秒时间戳 → "2006-01-02T15:04:05Z" 布局
		assert.Equal(t, time.UnixMilli(1700000000000).Format(ddosTimeLayout), got.CreationTime)
		assert.Equal(t, time.UnixMilli(1767225599000).Format(ddosTimeLayout), got.ExpiredTime)
		// Tags 保持「非 nil 空 map」契约(ddosbgp 列表响应无标签字段)
		require.NotNil(t, got.Tags)
		assert.Empty(t, got.Tags)
		// 未无量来源的字段保持零值,不捏造
		assert.Zero(t, got.BasicBandwidth)
		assert.Zero(t, got.ElasticBandwidth)
		assert.Zero(t, got.ServiceBandwidth)
		assert.Zero(t, got.CCQPS)
		assert.Empty(t, got.ProtectedIPs)
		assert.Empty(t, got.ChargeType)
	})

	t.Run("Desc 为空时回退 Remark", func(t *testing.T) {
		inst := bgp.Instance{
			InstanceId: "ddosbgp-1",
			Remark:     "备注名",
			Desc:       "",
		}
		got := convertDDoSBGPInstanceToDDOS(inst, "cn-shanghai")
		assert.Equal(t, "备注名", got.InstanceName)
		assert.Equal(t, "备注名", got.Description)
	})

	t.Run("零值安全", func(t *testing.T) {
		got := convertDDoSBGPInstanceToDDOS(bgp.Instance{}, "cn-hangzhou")

		assert.Empty(t, got.InstanceID)
		assert.Empty(t, got.Status)
		assert.Empty(t, got.CreationTime)
		assert.Empty(t, got.ExpiredTime)
		assert.Equal(t, "DDoS原生防护", got.Edition)
		assert.Equal(t, "aliyun", got.Provider)
		require.NotNil(t, got.Tags)
		assert.Empty(t, got.Tags)
	})
}

func TestConvertDDoSCooInstanceToDDOS(t *testing.T) {
	t.Run("全字段映射", func(t *testing.T) {
		inst := coo.Instance{
			InstanceId: "ddoscoo-cn-xxxxxx",
			Remark:     "高防实例A",
			Status:     1, // 状态原值透出,不做语义转换
			Ip:         "1.2.3.4",
			Edition:    9, // 版本/套餐原值透出
			IpMode:     "fnat",
			CreateTime: 1700000000000,
			ExpireTime: 1767225599000,
		}

		got := convertDDoSCooInstanceToDDOS(inst, "cn-hangzhou")

		assert.Equal(t, "ddoscoo-cn-xxxxxx", got.InstanceID)
		assert.Equal(t, "高防实例A", got.InstanceName)
		assert.Equal(t, "1", got.Status)
		assert.Equal(t, "cn-hangzhou", got.Region)
		assert.Equal(t, "DDoS高防", got.Edition)
		require.Len(t, got.ProtectedIPs, 1)
		assert.Equal(t, "1.2.3.4", got.ProtectedIPs[0])
		// Description 仅透出原值 Edition/IpMode,不臆断语义
		assert.Equal(t, "Edition=9 IpMode=fnat", got.Description)
		assert.Equal(t, "aliyun", got.Provider)
		// 时间口径与 waf.go 一致:毫秒时间戳 → "2006-01-02T15:04:05Z" 布局
		assert.Equal(t, time.UnixMilli(1700000000000).Format(ddosTimeLayout), got.CreationTime)
		assert.Equal(t, time.UnixMilli(1767225599000).Format(ddosTimeLayout), got.ExpiredTime)
		require.NotNil(t, got.Tags)
		assert.Empty(t, got.Tags)
	})

	t.Run("Ip 为空时 ProtectedIPs 为空", func(t *testing.T) {
		got := convertDDoSCooInstanceToDDOS(coo.Instance{InstanceId: "ddoscoo-2"}, "cn-hangzhou")
		assert.Empty(t, got.ProtectedIPs)
	})

	t.Run("零值安全", func(t *testing.T) {
		got := convertDDoSCooInstanceToDDOS(coo.Instance{}, "cn-hangzhou")

		assert.Empty(t, got.InstanceID)
		assert.Equal(t, "0", got.Status) // Status=0 亦原样透出为 "0"
		assert.Empty(t, got.CreationTime)
		assert.Empty(t, got.ExpiredTime)
		assert.Empty(t, got.ProtectedIPs)
		assert.Equal(t, "Edition=0 IpMode=", got.Description)
		assert.Equal(t, "DDoS高防", got.Edition)
		assert.Equal(t, "aliyun", got.Provider)
		require.NotNil(t, got.Tags)
		assert.Empty(t, got.Tags)
	})
}

func TestPickInstanceByID(t *testing.T) {
	instances := []types.DDOSInstance{
		{InstanceID: "a", InstanceName: "实例A"},
		{InstanceID: "b", InstanceName: "实例B"},
	}

	t.Run("命中", func(t *testing.T) {
		inst, err := pickInstanceByID(instances, "b")
		require.NoError(t, err)
		require.NotNil(t, inst)
		assert.Equal(t, "实例B", inst.InstanceName)
	})

	t.Run("未命中返回错误", func(t *testing.T) {
		inst, err := pickInstanceByID(instances, "c")
		require.Error(t, err)
		assert.Nil(t, inst)
		assert.Contains(t, err.Error(), "DDoS防护实例不存在: c")
	})

	t.Run("空列表返回错误", func(t *testing.T) {
		inst, err := pickInstanceByID(nil, "a")
		require.Error(t, err)
		assert.Nil(t, inst)
	})
}

func TestFilterDDOSInstances(t *testing.T) {
	instances := []types.DDOSInstance{
		{InstanceID: "1", InstanceName: "生产-Web防护", Status: "Running"},
		{InstanceID: "2", InstanceName: "测试环境", Status: "Idle"},
		{InstanceID: "3", InstanceName: "web-网关", Status: "RUNNING"},
	}

	t.Run("filter 为 nil 原样返回", func(t *testing.T) {
		got := filterDDOSInstances(instances, nil)
		assert.Len(t, got, 3)
	})

	t.Run("实例名大小写不敏感包含匹配", func(t *testing.T) {
		got := filterDDOSInstances(instances, &types.DDOSInstanceFilter{InstanceName: "WEB"})
		require.Len(t, got, 2)
		assert.Equal(t, "1", got[0].InstanceID)
		assert.Equal(t, "3", got[1].InstanceID)
	})

	t.Run("状态大小写不敏感包含匹配", func(t *testing.T) {
		got := filterDDOSInstances(instances, &types.DDOSInstanceFilter{Status: "running"})
		require.Len(t, got, 2)
		assert.Equal(t, "1", got[0].InstanceID)
		assert.Equal(t, "3", got[1].InstanceID)
	})

	t.Run("名称与状态叠加过滤", func(t *testing.T) {
		got := filterDDOSInstances(instances, &types.DDOSInstanceFilter{
			InstanceName: "web",
			Status:       "idle", // 与名称条件叠加后无交集(Idle 的实例名不含 web)
		})
		assert.Empty(t, got)
	})

	t.Run("不支持的字段 Region/PageSize 被忽略", func(t *testing.T) {
		got := filterDDOSInstances(instances, &types.DDOSInstanceFilter{
			Region:   "cn-beijing",
			PageSize: 1, // 不参与内存过滤,不影响结果条数
		})
		assert.Len(t, got, 3)
	})

	t.Run("无匹配返回空", func(t *testing.T) {
		got := filterDDOSInstances(instances, &types.DDOSInstanceFilter{InstanceName: "不存在"})
		assert.Empty(t, got)
	})
}
