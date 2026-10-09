package cloudx

import (
	"context"
	"testing"

	"github.com/Havens-blog/e-cloudx-sdk/types"
)

// rdsMetricQuerierStub RDSMetricQuerier 签名编译期断言(T2 AC-2)。
// 签名 = (ctx, rdsID, instanceName, region, engine, startDate, endDate)——带
// region(RDS 是地域性资源,与 NAS/Disk 同型,不做全局 region 推断)+ engine
// (T1 探测定案:aliyun 指标名/华为维度键都按 engine 分派,查询必经路径)。
// 参数次序漂移会让 T3 适配器 region/engine 错位接线,编译期即拦下。
type rdsMetricQuerierStub struct{}

func (rdsMetricQuerierStub) GetRDSMetrics(ctx context.Context, rdsID, instanceName, region, engine, startDate, endDate string) ([]types.RDSMetric, error) {
	return nil, nil
}

func TestRDSMetricQuerierSignature(t *testing.T) {
	var _ RDSMetricQuerier = rdsMetricQuerierStub{}
}
