package types

import "testing"

// 权重指针语义：0/负数 = 无值(nil)，>0 = 有值。
// 依据：云 API 对"无权重概念"的记录返回 0，若直接透出会被展示成假的 0。
func TestNewDNSWeight(t *testing.T) {
	if got := NewDNSWeight(0); got != nil {
		t.Errorf("NewDNSWeight(0) = %v, want nil（无权重概念应为 nil，而非 0）", *got)
	}
	if got := NewDNSWeight(-1); got != nil {
		t.Errorf("NewDNSWeight(-1) = %v, want nil", *got)
	}
	got := NewDNSWeight(100)
	if got == nil || *got != 100 {
		t.Fatalf("NewDNSWeight(100) = %v, want 100", got)
	}
	// 指针独立（不共享同一地址，避免调用方互相覆盖）
	a := NewDNSWeight(10)
	b := NewDNSWeight(20)
	if a == b {
		t.Error("NewDNSWeight 返回了同一指针地址")
	}
	*a = 99
	if *b != 20 {
		t.Errorf("指针共享导致互相影响: b=%d", *b)
	}
}

// 能力矩阵：阿里云可读不可写；未核实的云一律不可写（宁缺毋滥，避免假表单）。
func TestDNSWeightCapabilities(t *testing.T) {
	if !DNSWeightSupported("aliyun") {
		t.Error("aliyun 应 DNSWeightSupported=true（DescribeDomainRecords 返回 Record.Weight）")
	}
	if DNSWeightWritable("aliyun") {
		t.Error("aliyun 不应 DNSWeightWritable=true（Add/UpdateDomainRecord 无 Weight 参数）")
	}
	// 不支持权重的云：读=不支持，写=不可写
	for _, p := range []string{"huawei", "volcano", "unknown-cloud"} {
		if DNSWeightSupported(p) {
			t.Errorf("%s 不应 DNSWeightSupported=true", p)
		}
		if DNSWeightWritable(p) {
			t.Errorf("%s 不应 DNSWeightWritable=true", p)
		}
	}
}

// 可写性隐含可读：任何可写云必须同时是支持的云。
func TestDNSWeightWritableImpliesSupported(t *testing.T) {
	for _, p := range []string{"aliyun", "tencent", "aws", "huawei", "volcano", "azure"} {
		if DNSWeightWritable(p) && !DNSWeightSupported(p) {
			t.Errorf("%s 可写但不可读，能力矩阵自相矛盾", p)
		}
	}
}
