package proc

import "testing"

func TestNeedsElevation(t *testing.T) {
	cases := []struct {
		out  string
		want bool
	}{
		{"Windows IP Configuration\r\n\r\nThe requested operation requires elevation.\r\n", true},
		{"Windows IP 配置\n\n请求的操作需要提升。\n", true},
		{"The requested operation requires elevation (Run as administrator).", true},
		{"请求的操作需要提升(作为管理员运行)。", true},
		{"[SC] OpenService FAILED 5:\n\nAccess is denied.\n", true},
		{"  [SC] OpenSCManager FAILED 5:\n\n拒绝访问。", true},
		{"[SC] OpenService FAILED 1060:\n\nThe specified service does not exist as an installed service.", false},
		{"Access is denied.", false},
		{"拒绝访问。", false},
		{"error: permission denied", false},
		{"", false},
	}
	for _, c := range cases {
		if got := NeedsElevation(c.out); got != c.want {
			t.Errorf("NeedsElevation(%q) = %v", c.out, got)
		}
	}
}
