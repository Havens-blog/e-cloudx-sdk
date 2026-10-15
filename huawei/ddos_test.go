package huawei

import (
	"testing"
	"time"

	"github.com/gotomicro/ego/core/elog"
	aadmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/aad/v1/model"
	"github.com/stretchr/testify/assert"
)

// 本地指针小工具:构造 SDK 模型的指针字段
func strp(s string) *string { return &s }
func i32p(v int32) *int32   { return &v }
func i64p(v int64) *int64   { return &v }

func newTestDDOSAdapter() *DDOSAdapter {
	return &DDOSAdapter{
		accessKeyID:     "test-ak",
		accessKeySecret: "test-sk",
		defaultRegion:   "cn-north-4",
		logger:          elog.DefaultLogger,
	}
}

// ==================== convertAADInstanceToDDOS ====================

// TestConvertAADInstanceToDDOS_AllFields 全字段映射(含多 IP 聚合与企业项目)
func TestConvertAADInstanceToDDOS_AllFields(t *testing.T) {
	adapter := newTestDDOSAdapter()

	info := &aadmodel.InstanceInfo{
		InstanceId:   strp("i-1"),
		InstanceName: strp("ddos-1"),
		Ips: &[]aadmodel.InstanceIpInfo{
			{Ip: strp("1.1.1.1"), IpId: strp("ip-1"), BasicBandwidth: i32p(100), ElasticBandwidth: i32p(200), IpStatus: i32p(1)},
			{Ip: strp("2.2.2.2"), IpId: strp("ip-2"), BasicBandwidth: i32p(50), ElasticBandwidth: i32p(60), IpStatus: i32p(0)},
		},
		ExpireTime:          i64p(1893456000000), // 2030-01-01T00:00:00Z
		ServiceBandwidth:    i32p(30),
		InstanceStatus:      i32p(1),
		EnterpriseProjectId: strp("ep-1"),
	}

	result := adapter.convertAADInstanceToDDOS(info, "cn-north-4")

	assert.Equal(t, "i-1", result.InstanceID)
	assert.Equal(t, "ddos-1", result.InstanceName)
	assert.Equal(t, "1", result.Status) // 状态数字原值透出
	assert.Equal(t, "cn-north-4", result.Region)
	assert.Equal(t, "Anti-DDoS", result.Edition)
	assert.Equal(t, "huawei", result.Provider)

	assert.Equal(t, int64(30), result.ServiceBandwidth)
	assert.Empty(t, result.BandwidthUnit) // SDK 未标注单位,留空不猜

	// IP 级 Basic/ElasticBandwidth 是"每IP"口径,不并入实例级
	assert.Equal(t, int64(0), result.BasicBandwidth)
	assert.Equal(t, int64(0), result.ElasticBandwidth)

	assert.Equal(t, []string{"1.1.1.1", "2.2.2.2"}, result.ProtectedIPs)
	assert.Equal(t, 2, result.ProtectedIPCount)
	assert.Equal(t, "2 个防护IP", result.Description)

	// 过期时间:毫秒时间戳,格式沿用 waf.go 的 "2006-01-02T15:04:05Z" 口径
	expected := time.UnixMilli(*info.ExpireTime).Format("2006-01-02T15:04:05Z")
	assert.Equal(t, expected, result.ExpiredTime)

	assert.Equal(t, "ep-1", result.ResourceGroupID) // 企业项目 → 资源组
	assert.NotNil(t, result.Tags)
}

// TestConvertAADInstanceToDDOS_NilIps 防护IP列表为空
func TestConvertAADInstanceToDDOS_NilIps(t *testing.T) {
	adapter := newTestDDOSAdapter()

	info := &aadmodel.InstanceInfo{
		InstanceId:   strp("i-2"),
		InstanceName: strp("ddos-2"),
		Ips:          nil,
	}

	result := adapter.convertAADInstanceToDDOS(info, "cn-north-4")

	assert.Equal(t, "i-2", result.InstanceID)
	assert.Empty(t, result.ProtectedIPs)
	assert.Equal(t, 0, result.ProtectedIPCount)
	assert.Equal(t, "0 个防护IP", result.Description)
}

// TestConvertAADInstanceToDDOS_NilExpireTime 过期时间为空
func TestConvertAADInstanceToDDOS_NilExpireTime(t *testing.T) {
	adapter := newTestDDOSAdapter()

	info := &aadmodel.InstanceInfo{
		InstanceId: strp("i-3"),
		ExpireTime: nil,
	}

	result := adapter.convertAADInstanceToDDOS(info, "cn-north-4")

	assert.Equal(t, "i-3", result.InstanceID)
	assert.Empty(t, result.ExpiredTime)
}

// TestConvertAADInstanceToDDOS_StatusRawValue 状态数字原值透出(SDK 未标注枚举语义,不臆断)
func TestConvertAADInstanceToDDOS_StatusRawValue(t *testing.T) {
	adapter := newTestDDOSAdapter()

	result := adapter.convertAADInstanceToDDOS(&aadmodel.InstanceInfo{InstanceStatus: i32p(2)}, "cn-north-4")
	assert.Equal(t, "2", result.Status)

	resultZero := adapter.convertAADInstanceToDDOS(&aadmodel.InstanceInfo{InstanceStatus: i32p(0)}, "cn-north-4")
	assert.Equal(t, "0", resultZero.Status)

	resultNil := adapter.convertAADInstanceToDDOS(&aadmodel.InstanceInfo{InstanceStatus: nil}, "cn-north-4")
	assert.Empty(t, resultNil.Status)
}

// TestConvertAADInstanceToDDOS_EnterpriseProjectToResourceGroup 企业项目 → 资源组口径
func TestConvertAADInstanceToDDOS_EnterpriseProjectToResourceGroup(t *testing.T) {
	adapter := newTestDDOSAdapter()

	info := &aadmodel.InstanceInfo{
		InstanceId:          strp("i-4"),
		EnterpriseProjectId: strp("ep-prod"),
	}

	result := adapter.convertAADInstanceToDDOS(info, "cn-north-4")
	assert.Equal(t, "ep-prod", result.ResourceGroupID)
}

// TestConvertAADInstanceToDDOS_MultiIPAggregation 多 IP 聚合:只取 IP 地址,IP 级带宽不并入实例级
func TestConvertAADInstanceToDDOS_MultiIPAggregation(t *testing.T) {
	adapter := newTestDDOSAdapter()

	info := &aadmodel.InstanceInfo{
		InstanceId: strp("i-5"),
		Ips: &[]aadmodel.InstanceIpInfo{
			{Ip: strp("10.0.0.1"), BasicBandwidth: i32p(100), ElasticBandwidth: i32p(200)},
			{Ip: nil, BasicBandwidth: i32p(0), ElasticBandwidth: i32p(0)}, // 无IP地址的记录跳过
			{Ip: strp("10.0.0.2"), BasicBandwidth: i32p(80), ElasticBandwidth: i32p(160)},
		},
	}

	result := adapter.convertAADInstanceToDDOS(info, "ap-southeast-1")

	assert.Equal(t, []string{"10.0.0.1", "10.0.0.2"}, result.ProtectedIPs)
	assert.Equal(t, 2, result.ProtectedIPCount)
	// 每 IP 的 Basic/ElasticBandwidth 语义与实例级不同,不并入
	assert.Equal(t, int64(0), result.BasicBandwidth)
	assert.Equal(t, int64(0), result.ElasticBandwidth)
}

// TestConvertAADInstanceToDDOS_NilInfo 空指针防御
func TestConvertAADInstanceToDDOS_NilInfo(t *testing.T) {
	adapter := newTestDDOSAdapter()

	result := adapter.convertAADInstanceToDDOS(nil, "cn-north-4")

	assert.Equal(t, "cn-north-4", result.Region)
	assert.Empty(t, result.InstanceID)
	assert.Empty(t, result.Status)
	assert.Empty(t, result.ExpiredTime)
}
