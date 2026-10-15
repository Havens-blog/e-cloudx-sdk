package tencent

import (
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
	"github.com/gotomicro/ego/core/elog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	antiddos "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/antiddos/v20200309"
)

// 本文件复用包内既有测试 helper:strPtr(eni_test.go)、uint64Ptr(lb_test.go)。
// 测试只覆盖纯转换/分页/过滤逻辑,不做真实 API 调用。

// ==================== convertBGPInstanceToDDOS(高防包 BGP) ====================

func TestConvertBGPInstanceToDDOSFullFields(t *testing.T) {
	bgp := &antiddos.BGPInstance{
		InstanceDetail: &antiddos.InstanceRelation{
			InstanceId: strPtr("bgp-00000001"),
			EipList:    []*string{strPtr("1.2.3.4")},
		},
		SpecificationLimit: &antiddos.BGPInstanceSpecification{
			ProtectBandwidth:     uint64Ptr(300), // SDK注释:保底防护峰值,单位Gbps
			ServiceBandWidth:     uint64Ptr(100), // SDK注释:业务带宽
			ProtectIPNumberLimit: uint64Ptr(10),
			AutoRenewFlag:        uint64Ptr(1), // SDK注释:1=开启自动续费
		},
		Usage: &antiddos.BGPInstanceUsages{
			ProtectIPNumberUsage: uint64Ptr(2),
			Last7DayAttackCount:  uint64Ptr(15),
		},
		Region:      &antiddos.RegionInfo{Region: strPtr("ap-shanghai")},
		Status:      strPtr("attacking"),
		Name:        strPtr("企业高防包"),
		CreatedTime: strPtr("2024-01-01 10:00:00"),
		ExpiredTime: strPtr("2025-01-01 10:00:00"),
		EipProductInfos: []*antiddos.EipProductInfo{
			{Ip: strPtr("1.1.1.1"), BizType: strPtr("public")},
			nil,
			{Ip: nil},
			{Ip: strPtr("2.2.2.2")},
		},
		TagInfoList: []*antiddos.TagInfo{
			{TagKey: strPtr("env"), TagValue: strPtr("prod")},
			{TagKey: strPtr("owner"), TagValue: nil},
			nil,
			{TagKey: nil, TagValue: strPtr("no-key")},
			{TagKey: strPtr(""), TagValue: strPtr("empty-key")},
		},
	}

	inst := convertBGPInstanceToDDOS(bgp)

	assert.Equal(t, "bgp-00000001", inst.InstanceID)
	assert.Equal(t, "企业高防包", inst.InstanceName)
	assert.Equal(t, "attacking", inst.Status, "状态原值透传")
	assert.Equal(t, "ap-shanghai", inst.Region)
	assert.Equal(t, "高防包", inst.Edition)
	assert.Equal(t, int64(300), inst.BasicBandwidth, "保底防护带宽单位Gbps")
	assert.Equal(t, "Gbps", inst.BandwidthUnit)
	assert.Equal(t, int64(100), inst.ServiceBandwidth)
	assert.True(t, inst.AutoRenew, "AutoRenewFlag=1 → 自动续费开启")
	assert.Equal(t, "2024-01-01 10:00:00", inst.CreationTime)
	assert.Equal(t, "2025-01-01 10:00:00", inst.ExpiredTime)
	assert.Equal(t, []string{"1.1.1.1", "2.2.2.2"}, inst.ProtectedIPs, "ProtectedIPs 聚合 EipProductInfos 的 Ip,nil 条目跳过")
	assert.Equal(t, 2, inst.ProtectedIPCount)
	assert.Equal(t, "prod", inst.Tags["env"])
	require.Contains(t, inst.Tags, "owner")
	assert.Equal(t, "", inst.Tags["owner"], "TagValue 为空保留键")
	assert.NotContains(t, inst.Tags, "no-key", "nil TagKey 跳过")
	assert.NotContains(t, inst.Tags, "empty-key", "空 TagKey 跳过")
	assert.Equal(t, "tencent", inst.Provider)
}

func TestConvertBGPInstanceToDDOSNilFieldsSafe(t *testing.T) {
	// 全 nil 字段实例:不应 panic,取值应有零值兜底
	inst := convertBGPInstanceToDDOS(&antiddos.BGPInstance{})

	assert.Empty(t, inst.InstanceID)
	assert.Empty(t, inst.InstanceName)
	assert.Empty(t, inst.Status)
	assert.Equal(t, "ap-guangzhou", inst.Region, "Region 为 nil 时默认 ap-guangzhou")
	assert.Equal(t, "高防包", inst.Edition)
	assert.Equal(t, int64(0), inst.BasicBandwidth)
	assert.Equal(t, "Gbps", inst.BandwidthUnit)
	assert.Equal(t, int64(0), inst.ServiceBandwidth)
	assert.False(t, inst.AutoRenew)
	assert.Empty(t, inst.ProtectedIPs)
	assert.Equal(t, 0, inst.ProtectedIPCount)
	assert.Equal(t, "", inst.CreationTime)
	assert.Equal(t, "", inst.ExpiredTime)
	assert.NotNil(t, inst.Tags)
	assert.Empty(t, inst.Tags)
	assert.Equal(t, "tencent", inst.Provider)

	// Region 非 nil 但 Region.Region 为 nil
	inst2 := convertBGPInstanceToDDOS(&antiddos.BGPInstance{Region: &antiddos.RegionInfo{}})
	assert.Equal(t, "ap-guangzhou", inst2.Region)

	// SpecificationLimit 非 nil 但内部字段为 nil
	inst3 := convertBGPInstanceToDDOS(&antiddos.BGPInstance{SpecificationLimit: &antiddos.BGPInstanceSpecification{}})
	assert.Equal(t, int64(0), inst3.BasicBandwidth)
	assert.False(t, inst3.AutoRenew)

	// InstanceDetail 非 nil 但 InstanceId 为 nil
	inst4 := convertBGPInstanceToDDOS(&antiddos.BGPInstance{InstanceDetail: &antiddos.InstanceRelation{}})
	assert.Empty(t, inst4.InstanceID)
}

func TestConvertBGPInstanceToDDOSNilInstance(t *testing.T) {
	inst := convertBGPInstanceToDDOS(nil)
	assert.Equal(t, "tencent", inst.Provider)
	assert.Empty(t, inst.InstanceID)
}

func TestConvertBGPInstanceToDDOSAutoRenewOff(t *testing.T) {
	// AutoRenewFlag=0 → 未开自动续费
	bgp := &antiddos.BGPInstance{SpecificationLimit: &antiddos.BGPInstanceSpecification{AutoRenewFlag: uint64Ptr(0)}}
	assert.False(t, convertBGPInstanceToDDOS(bgp).AutoRenew)
}

func TestConvertBGPInstanceToDDOSStatusPassthrough(t *testing.T) {
	// SDK 注释状态枚举:idle 正常/attacking 攻击中/blocking 封堵中/
	// creating 创建中/deblocking 解封中/isolate 回收隔离中 → 全部原值透传
	for _, s := range []string{"idle", "attacking", "blocking", "creating", "deblocking", "isolate"} {
		inst := convertBGPInstanceToDDOS(&antiddos.BGPInstance{Status: strPtr(s)})
		assert.Equal(t, s, inst.Status, "状态 %s 应原值透传", s)
	}
}

// ==================== convertBGPIPInstanceToDDOS(高防IP BGPIP) ====================

func TestConvertBGPIPInstanceToDDOSFullFields(t *testing.T) {
	bgpip := &antiddos.BGPIPInstance{
		InstanceDetail: &antiddos.InstanceRelation{
			InstanceId: strPtr("bgpip-00000001"),
			EipList:    []*string{strPtr("3.3.3.3"), strPtr("4.4.4.4")},
		},
		SpecificationLimit: &antiddos.BGPIPInstanceSpecification{
			ProtectBandwidth: uint64Ptr(200),    // SDK注释:保底防护峰值,单位Mbps
			ElasticBandwidth: uint64Ptr(1000),   // SDK注释:弹性防护峰值,单位Mbps
			NormalBandwidth:  uint64Ptr(150),    // SDK注释:正常业务带宽,单位Mbps
			ProtectCCQPS:     uint64Ptr(500000), // SDK注释:CC防护峰值,单位qps
			AutoRenewFlag:    uint64Ptr(1),
			Line:             uint64Ptr(1), // BGP线路
		},
		Usage: &antiddos.BGPIPInstanceUsages{
			Last7DayAttackCount: uint64Ptr(3),
		},
		Region:           &antiddos.RegionInfo{Region: strPtr("ap-beijing")},
		Status:           strPtr("blocking"),
		Name:             strPtr("游戏高防IP"),
		CreatedTime:      strPtr("2024-02-01 08:00:00"),
		ExpiredTime:      strPtr("2025-02-01 08:00:00"),
		EipAddressStatus: strPtr("BIND"), // 高防弹性公网IP状态,原值透出到 Description
		TagInfoList: []*antiddos.TagInfo{
			{TagKey: strPtr("team"), TagValue: strPtr("infra")},
		},
	}

	inst := convertBGPIPInstanceToDDOS(bgpip)

	assert.Equal(t, "bgpip-00000001", inst.InstanceID)
	assert.Equal(t, "游戏高防IP", inst.InstanceName)
	assert.Equal(t, "blocking", inst.Status)
	assert.Equal(t, "ap-beijing", inst.Region)
	assert.Equal(t, "高防IP", inst.Edition)
	assert.Equal(t, int64(200), inst.BasicBandwidth, "保底防护带宽单位Mbps")
	assert.Equal(t, int64(1000), inst.ElasticBandwidth, "弹性防护带宽单位Mbps")
	assert.Equal(t, "Mbps", inst.BandwidthUnit)
	assert.Equal(t, int64(500000), inst.CCQPS)
	assert.True(t, inst.AutoRenew)
	assert.Equal(t, []string{"3.3.3.3", "4.4.4.4"}, inst.ProtectedIPs, "ProtectedIPs 来自 InstanceDetail.EipList")
	assert.Equal(t, 2, inst.ProtectedIPCount)
	assert.Equal(t, "高防IP eip_address_status=BIND", inst.Description, "EipAddressStatus 原值透出")
	assert.Equal(t, "infra", inst.Tags["team"])
	assert.Equal(t, "2024-02-01 08:00:00", inst.CreationTime)
	assert.Equal(t, "2025-02-01 08:00:00", inst.ExpiredTime)
	assert.Equal(t, "tencent", inst.Provider)
}

func TestConvertBGPIPInstanceToDDOSNilFieldsSafe(t *testing.T) {
	inst := convertBGPIPInstanceToDDOS(&antiddos.BGPIPInstance{})

	assert.Empty(t, inst.InstanceID)
	assert.Empty(t, inst.InstanceName)
	assert.Empty(t, inst.Status)
	assert.Equal(t, "ap-guangzhou", inst.Region)
	assert.Equal(t, "高防IP", inst.Edition)
	assert.Equal(t, int64(0), inst.BasicBandwidth)
	assert.Equal(t, int64(0), inst.ElasticBandwidth)
	assert.Equal(t, int64(0), inst.ServiceBandwidth)
	assert.Equal(t, int64(0), inst.CCQPS)
	assert.Equal(t, "Mbps", inst.BandwidthUnit)
	assert.False(t, inst.AutoRenew)
	assert.Empty(t, inst.ProtectedIPs)
	assert.Equal(t, 0, inst.ProtectedIPCount)
	assert.Equal(t, "高防IP", inst.Description, "EipAddressStatus 为 nil 时不追加状态后缀")
	assert.NotNil(t, inst.Tags)
	assert.Empty(t, inst.Tags)
	assert.Equal(t, "tencent", inst.Provider)

	// SpecificationLimit 非 nil 但 NormalBandwidth 单独为 nil
	inst2 := convertBGPIPInstanceToDDOS(&antiddos.BGPIPInstance{
		SpecificationLimit: &antiddos.BGPIPInstanceSpecification{NormalBandwidth: nil},
	})
	assert.Equal(t, int64(0), inst2.ServiceBandwidth)
}

func TestConvertBGPIPInstanceToDDOSNormalBandwidthMbps(t *testing.T) {
	// 单位实证:SDK v1.3.131 注释为“正常业务带宽,单位Mbps”(并非 kbps),
	// 因此 ServiceBandwidth 按原值直读,不做 kbps→Mbps 换算。
	bgpip := &antiddos.BGPIPInstance{
		SpecificationLimit: &antiddos.BGPIPInstanceSpecification{
			NormalBandwidth: uint64Ptr(150),
		},
	}
	inst := convertBGPIPInstanceToDDOS(bgpip)
	assert.Equal(t, int64(150), inst.ServiceBandwidth, "NormalBandwidth 原值即 Mbps,无需换算")
	assert.Equal(t, "Mbps", inst.BandwidthUnit)
}

func TestConvertBGPIPInstanceToDDOSNilInstance(t *testing.T) {
	inst := convertBGPIPInstanceToDDOS(nil)
	assert.Equal(t, "tencent", inst.Provider)
	assert.Empty(t, inst.InstanceID)
}

func TestConvertBGPIPInstanceToDDOSStatusPassthrough(t *testing.T) {
	for _, s := range []string{"idle", "attacking", "blocking", "creating", "deblocking", "isolate"} {
		inst := convertBGPIPInstanceToDDOS(&antiddos.BGPIPInstance{Status: strPtr(s)})
		assert.Equal(t, s, inst.Status, "状态 %s 应原值透传", s)
	}
}

// ==================== shouldContinuePaging(分页判定) ====================

func TestDDOSShouldContinuePaging(t *testing.T) {
	total := func(v uint64) *uint64 { return &v }

	tests := []struct {
		name        string
		accumulated int
		pageLen     int
		limit       int
		total       *uint64
		want        bool
	}{
		{"整页且未到总数,继续", 100, 100, 100, total(250), true},
		{"整页且总数未知,继续", 100, 100, 100, nil, true},
		{"上一页为空,停止", 0, 0, 100, total(250), false},
		{"短页即最后一页,停止", 230, 30, 100, total(300), false},
		{"累计达到总数,停止", 250, 100, 100, total(250), false},
		{"累计超过总数(异常数据),停止", 300, 100, 100, total(250), false},
		{"短页但总数未知,停止", 30, 30, 100, nil, false},
		{"首轮短页,停止", 30, 30, 100, total(250), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, shouldContinuePaging(tt.accumulated, tt.pageLen, tt.limit, tt.total))
		})
	}
}

// ==================== filterDDOSInstancesByName(名称过滤) ====================

func TestDDOSFilterInstancesByName(t *testing.T) {
	insts := []types.DDOSInstance{
		{InstanceID: "i1", InstanceName: "GameDDoS-PRO"},
		{InstanceID: "i2", InstanceName: "game test"},
		{InstanceID: "i3", InstanceName: "官网高防包"},
		{InstanceID: "i4", InstanceName: ""},
	}

	// 大小写不敏感包含
	assert.Len(t, filterDDOSInstancesByName(insts, "game"), 2)
	assert.Len(t, filterDDOSInstancesByName(insts, "GAMEDDOS"), 1)
	assert.Len(t, filterDDOSInstancesByName(insts, "高防"), 1)
	assert.Len(t, filterDDOSInstancesByName(insts, "nomatch"), 0)
	assert.Len(t, filterDDOSInstancesByName(nil, ""), 0)
	assert.Len(t, filterDDOSInstancesByName(insts, ""), 4, "空过滤名不过滤")
}

// ==================== NewDDOSAdapter ====================

func TestNewDDOSAdapter(t *testing.T) {
	a := NewDDOSAdapter("ak", "sk", "ap-shanghai", elog.DefaultLogger)
	require.NotNil(t, a)
	assert.Equal(t, "ak", a.accessKeyID)
	assert.Equal(t, "sk", a.accessKeySecret)
	assert.Equal(t, "ap-shanghai", a.defaultRegion)
	assert.NotNil(t, a.logger)
}
