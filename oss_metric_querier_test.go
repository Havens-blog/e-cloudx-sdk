package cloudx

import (
	"context"
	"reflect"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
)

// ossMetricQuerierStub OSSMetricQuerier 签名锚(编译期断言)。
// 签名不带 region,区别于 NASMetricQuerier:OSS 是全局服务(ListBuckets 的
// region 参数可选,各厂商实现均有 defaultRegion 回退),与 CDNMetricQuerier
// 同型:bucket_name 全局唯一即检索键,唯一键 (account_id, bucket_name, date)。
type ossMetricQuerierStub struct{}

func (ossMetricQuerierStub) GetOSSMetrics(ctx context.Context, bucketName, startDate, endDate string) ([]types.OSSMetric, error) {
	return nil, nil
}

var _ OSSMetricQuerier = ossMetricQuerierStub{}

// TestOSSMetricQuerierSignature 冻结接口形状:单方法小接口,方法名 GetOSSMetrics,
// 入参 (ctx, bucketName, startDate, endDate),出参 ([]types.OSSMetric, error)。
// 签名漂移(如误加 region、改返回类型)在 var _ 断言与本测试处双重失败。
func TestOSSMetricQuerierSignature(t *testing.T) {
	mt := reflect.TypeOf((*OSSMetricQuerier)(nil)).Elem()
	if mt.NumMethod() != 1 {
		t.Fatalf("OSSMetricQuerier 方法数 = %d, want 1(保持小接口,仿 CDNMetricQuerier)", mt.NumMethod())
	}
	m, ok := mt.MethodByName("GetOSSMetrics")
	if !ok {
		t.Fatal("OSSMetricQuerier 缺少 GetOSSMetrics 方法")
	}
	ft := m.Type // 接口方法签名不含 receiver
	if got := ft.NumIn(); got != 4 {
		t.Fatalf("GetOSSMetrics 入参数 = %d, want 4(ctx+3 string)", got)
	}
	if got := ft.In(0).String(); got != "context.Context" {
		t.Fatalf("GetOSSMetrics In(0) = %s, want context.Context", got)
	}
	for i := 1; i <= 3; i++ {
		if ft.In(i).Kind() != reflect.String {
			t.Fatalf("GetOSSMetrics In(%d) = %s, want string(bucketName/startDate/endDate)", i, ft.In(i))
		}
	}
	if got := ft.NumOut(); got != 2 {
		t.Fatalf("GetOSSMetrics 出参数 = %d, want 2", got)
	}
	if want := reflect.TypeOf([]types.OSSMetric(nil)); ft.Out(0) != want {
		t.Fatalf("GetOSSMetrics Out(0) = %s, want []types.OSSMetric", ft.Out(0))
	}
	if want := reflect.TypeOf((*error)(nil)).Elem(); ft.Out(1) != want {
		t.Fatalf("GetOSSMetrics Out(1) = %s, want error", ft.Out(1))
	}
}
