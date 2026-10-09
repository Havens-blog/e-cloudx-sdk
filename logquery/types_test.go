package logquery

import (
	"encoding/json"
	"testing"
)

func TestParseTimeMs(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want int64
	}{
		// 阿里 ALB ISO8601 带时区(fixture: aliyun-alb-access)
		{"iso8601 offset", "2026-08-20T20:55:57+08:00", 1787230557000},
		// 华为 WAF ISO Z(fixture: huawei-waf-attack attack-time)
		{"iso8601 z", "2026-08-27T09:56:02.000Z", 1787824562000},
		// 阿里 WAF start_time unix 秒(fixture: aliyun-waf3-access)
		{"unix sec", "1787824584", 1787824584000},
		// DCDN unixtime unix 秒数值
		{"unix sec int", int64(1787824545), 1787824545000},
		// AWS WAF timestamp 毫秒(fixture: aws-waf-cloudfront)
		{"unix ms", int64(1756005799639), 1756005799639},
		// Akamai _unixtimestamp_ 纳秒(fixture: aliyun-akamai-waf)
		{"unix ns", "1787824606814450895", 1787824606814},
		// 华为 ELB 首列 秒.毫秒(fixture: huawei-elb-access)
		{"sec.frac", "1787824562.04", 1787824562040},
		// Akamai CDN reqTimeSec 时刻戳.毫秒
		{"sec.frac2", float64(1787225893.800), 1787225893800},
		// 缺失/非法
		{"empty", "", 0},
		{"nil", nil, 0},
		{"garbage", "not-a-time", 0},
		{"float string", "0.027", 27},
		// 火山 TLS SearchLogs 用 UseNumber() 反序列化,数字以 json.Number 出现
		{"json.Number ms", json.Number("1790229254000"), 1790229254000},
		{"json.Number sec", json.Number("1787824584"), 1787824584000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseTimeMs(c.in)
			if got != c.want {
				t.Errorf("ParseTimeMs(%v) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

func TestNormalizeAction(t *testing.T) {
	cases := map[string]string{
		"block": "block", "BLOCK": "block", "Intercept": "block",
		"alert": "alert", "ALLOW": "allow", "pass": "pass",
		"Default_Action": "allow", "": "", "captcha": "captcha",
	}
	for in, want := range cases {
		if got := NormalizeAction(in); got != want {
			t.Errorf("NormalizeAction(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeCacheHit(t *testing.T) {
	cases := map[string]string{
		"TCP_HIT": "hit", "HIT": "hit", "0": "hit",
		"Miss": "miss", "TCP_MISS": "miss",
		"Error": "error", "-": "-", "": "-",
		// DCDN hit_info 整串("-,WS|CHARGE|NOTLAST")由 aliyun mapper 先取分段再归一,
		// NormalizeCacheHit 只消费已提取的命中段
	}
	for in, want := range cases {
		if got := NormalizeCacheHit(in); got != want {
			t.Errorf("NormalizeCacheHit(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeSeverity(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"2", "low"}, {"5", "high"}, {float64(7), "high"},
		{"low", "low"}, {"HIGH", "high"}, {"unknown", "-"}, {nil, "-"},
	}
	for _, c := range cases {
		if got := NormalizeSeverity(c.in); got != c.want {
			t.Errorf("NormalizeSeverity(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestToInt64(t *testing.T) {
	cases := []struct {
		in   any
		want int64
	}{
		{"403", 403}, {int(403), 403}, {float64(403), 403},
		{json.Number("403"), 403}, // UseNumber() 数字(火山 TLS)
		{"0.027", 0}, // 截断取整,毫秒换算由调用方乘
		{"3.097, 3.072", 3},
		{"", 0}, {nil, 0}, {"abc", 0},
	}
	for _, c := range cases {
		if got := Int(c.in); got != c.want {
			t.Errorf("Int(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}
