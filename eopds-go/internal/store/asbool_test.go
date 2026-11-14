package store

import "testing"

func TestAsBool(t *testing.T) {
	cases := []struct {
		in   any
		want bool
	}{
		{true, true}, {false, false},
		{int64(1), true}, {int64(0), false},
		{int(2), true}, {"1", true}, {"t", true}, {"true", true},
		{"0", false}, {[]byte("t"), true}, {[]byte("f"), false}, {nil, false},
	}
	for _, c := range cases {
		if got := asBool(c.in); got != c.want {
			t.Errorf("asBool(%v)=%v want %v", c.in, got, c.want)
		}
	}
}
