package cloudx

import (
	"context"
	"reflect"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
)

// nasMetricQuerierStub NASMetricQuerier 签名锚(编译期断言)。
// 签名带 region,区别于 CDNMetricQuerier 的全局签名:CDN 是全局服务故签名无
// region,NAS 是地域性资源,必须按实例所在 region 调用对应厂商监控 API,
// 对多 region 账号按「实例 → region」逐实例查询,不做全局 region 推断。
type nasMetricQuerierStub struct{}

func (nasMetricQuerierStub) GetNASMetrics(ctx context.Context, fsID, fsName, region, startDate, endDate string) ([]types.NASMetric, error) {
	return nil, nil
}

var _ NASMetricQuerier = nasMetricQuerierStub{}

// TestNASMetricQuerierSignature 冻结接口形状:单方法小接口,方法名 GetNASMetrics,
// 入参 (ctx, fsID, fsName, region, startDate, endDate),出参 ([]types.NASMetric, error)。
// 签名漂移(如丢 region、改返回类型)在 var _ 断言与本测试处双重失败。
func TestNASMetricQuerierSignature(t *testing.T) {
	mt := reflect.TypeOf((*NASMetricQuerier)(nil)).Elem()
	if mt.NumMethod() != 1 {
		t.Fatalf("NASMetricQuerier 方法数 = %d, want 1(保持小接口,仿 CDNMetricQuerier)", mt.NumMethod())
	}
	m, ok := mt.MethodByName("GetNASMetrics")
	if !ok {
		t.Fatal("NASMetricQuerier 缺少 GetNASMetrics 方法")
	}
	ft := m.Type // 接口方法签名不含 receiver
	if got := ft.NumIn(); got != 6 {
		t.Fatalf("GetNASMetrics 入参数 = %d, want 6(ctx+5 string)", got)
	}
	if got := ft.In(0).String(); got != "context.Context" {
		t.Fatalf("GetNASMetrics In(0) = %s, want context.Context", got)
	}
	for i := 1; i <= 5; i++ {
		if ft.In(i).Kind() != reflect.String {
			t.Fatalf("GetNASMetrics In(%d) = %s, want string(fsID/fsName/region/startDate/endDate)", i, ft.In(i))
		}
	}
	if got := ft.NumOut(); got != 2 {
		t.Fatalf("GetNASMetrics 出参数 = %d, want 2", got)
	}
	if want := reflect.TypeOf([]types.NASMetric(nil)); ft.Out(0) != want {
		t.Fatalf("GetNASMetrics Out(0) = %s, want []types.NASMetric", ft.Out(0))
	}
	if want := reflect.TypeOf((*error)(nil)).Elem(); ft.Out(1) != want {
		t.Fatalf("GetNASMetrics Out(1) = %s, want error", ft.Out(1))
	}
}
