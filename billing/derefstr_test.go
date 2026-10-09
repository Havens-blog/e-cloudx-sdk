package billing

import "testing"

func TestDerefStr(t *testing.T) {
	empty := ""
	nonEmpty := "value"

	tests := []struct {
		name string
		in   *string
		want string
	}{
		{name: "nil pointer returns empty string", in: nil, want: ""},
		{name: "pointer to empty string", in: &empty, want: ""},
		{name: "pointer to non-empty string", in: &nonEmpty, want: "value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DerefStr(tt.in); got != tt.want {
				t.Fatalf("DerefStr(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
