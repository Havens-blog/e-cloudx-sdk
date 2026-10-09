package aws

import "testing"

func TestAWSARNService(t *testing.T) {
	cases := []struct {
		name string
		arn  string
		want string
	}{
		{"cloudfront", "arn:aws:cloudfront::123456789012:distribution/E123ABC", "cloudfront"},
		{"alb", "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/my-alb/abc123", "elasticloadbalancing"},
		{"malformed", "not-an-arn", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := awsARNService(c.arn); got != c.want {
				t.Fatalf("awsARNService(%q) = %q, want %q", c.arn, got, c.want)
			}
		})
	}
}

func TestClassifyALBTargetID(t *testing.T) {
	cases := []struct {
		name      string
		id        string
		wantKind  string
		wantValue string
	}{
		{"ipv4", "10.0.0.5", "ip", "10.0.0.5"},
		{"instance", "i-0abc123def456", "instance", "i-0abc123def456"},
		{"lambda arn", "arn:aws:lambda:us-east-1:123:function:fn", "lambda", "arn:aws:lambda:us-east-1:123:function:fn"},
		{"empty", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, val := classifyALBTargetID(c.id)
			if kind != c.wantKind || val != c.wantValue {
				t.Fatalf("classifyALBTargetID(%q) = (%q, %q), want (%q, %q)", c.id, kind, val, c.wantKind, c.wantValue)
			}
		})
	}
}

func TestDedupStrings(t *testing.T) {
	t.Run("去重剔空保持顺序", func(t *testing.T) {
		in := []string{"a.com", "  ", "b.com", "a.com", "c.com"}
		out := dedupStrings(in)
		want := []string{"a.com", "b.com", "c.com"}
		if len(out) != len(want) {
			t.Fatalf("dedupStrings = %v, want %v", out, want)
		}
		for i := range want {
			if out[i] != want[i] {
				t.Fatalf("dedupStrings = %v, want %v", out, want)
			}
		}
	})

	t.Run("空输入", func(t *testing.T) {
		if out := dedupStrings(nil); len(out) != 0 {
			t.Fatalf("dedupStrings(nil) = %v, want empty", out)
		}
	})
}