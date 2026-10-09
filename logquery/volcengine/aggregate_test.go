package volcengine

import (
	"testing"

	"github.com/volcengine/volc-sdk-golang/service/tls"
)

func TestFlattenHistogram(t *testing.T) {
	// TLS 返回的分桶 StartTime 或秒或毫秒都按量级自适配;归并到 1h(bucketMs=3600000)对齐桶。
	bucketMs := int64(3600 * 1000)
	infos := []tls.HistogramInfoV1{
		{Count: 10, StartTime: 1790143200}, // 秒:10:20 → 对齐 10:00
		{Count: 5, StartTime: 1790143500},  // 秒:10:25 → 仍 10:00
		{Count: 7, StartTime: 1790146800},  // 秒:11:00 → 11:00
	}
	buckets, total := flattenHistogram(infos, bucketMs)
	if total != 22 {
		t.Fatalf("total = %d, want 22", total)
	}
	// 1790143200 与 1790143500 落在同一 3600s 桶(起始 1790143200),1790146800 下一桶。
	want := map[int64]int64{
		1790143200000: 15,
		1790146800000: 7,
	}
	if len(buckets) != len(want) {
		t.Fatalf("buckets len = %d, want %d (%+v)", len(buckets), len(want), buckets)
	}
	for _, b := range buckets {
		if want[b.Timestamp] != b.Count {
			t.Errorf("bucket %d count = %d, want %d", b.Timestamp, b.Count, want[b.Timestamp])
		}
	}
}

func TestFlattenHistogram_MillisTime(t *testing.T) {
	// 13 位毫秒 StartTime:ParseTimeMs 判定为毫秒(≥1e11)原样保留
	buckets, total := flattenHistogram([]tls.HistogramInfoV1{
		{Count: 3, StartTime: 1790143200000}, // 1h 对齐
		{Count: 2, StartTime: 1790143350000}, // +150s 同桶归并
	}, 3600000)
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	if len(buckets) != 1 || buckets[0].Timestamp != 1790143200000 || buckets[0].Count != 5 {
		t.Fatalf("buckets = %+v, want single bucket @1790143200000 count 5", buckets)
	}
}

func TestFlattenHistogram_DropsNonPositive(t *testing.T) {
	buckets, total := flattenHistogram([]tls.HistogramInfoV1{{Count: 9, StartTime: 0}}, 3600000)
	if total != 0 || len(buckets) != 0 {
		t.Fatalf("total/len = %d/%d, want 0/0(StartTime=0 丢弃)", total, len(buckets))
	}
}
