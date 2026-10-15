package install

import "testing"

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b  string
		order int
		ok    bool
	}{
		{"0.1.0", "0.1.0", 0, true},
		{"v0.1.0", "0.1.0", 0, true},
		{"0.1.0-2-gabc1234-dirty", "0.1.0-2-gabc1234-dirty", 0, true},
		{"0.2.0", "0.1.0", 1, true},
		{"0.1.0", "0.2.0", -1, true},
		{"1.0.0", "0.99.99", 1, true},
		{"0.10.0", "0.9.0", 1, true},
		{"0.1", "0.1.0", 0, false}, // same position, different strings
		{"0.1", "0.1.1", -1, true},
		{"0.1.0-2-gabc1234", "0.1.0", 1, true},
		{"0.1.0-2-gabc1234", "0.1.0-3-gdef5678", -1, true},
		{"0.1.0-12-gabc1234", "0.1.0-3-gdef5678", 1, true},
		{"0.1.0-2-gabc1234", "0.1.1", -1, true},
		{"0.1.0-2-gabc1234", "0.1.0-2-gdef5678", 0, false},
		{"0.2.0-rc.1", "0.2.0", -1, true},
		{"0.2.0-rc.1", "0.2.0-rc.2", -1, true},
		{"0.2.0-rc.2", "0.2.0-rc.10", -1, true},
		{"0.2.0-alpha", "0.2.0-beta", -1, true},
		{"0.2.0-1", "0.2.0-alpha", -1, true},
		{"0.2.0-alpha", "0.2.0-alpha.1", -1, true},
		{"0.2.0-rc1-3-gabc1234", "0.2.0-rc1", 1, true},
		{"0.2.0-rc1-3-gabc1234", "0.2.0", -1, true},
		{"1.0.0+build.1", "1.0.0+build.2", 0, false},
		{"1.0.0+build.1", "0.9.0", 1, true},
		{"0.0.0-abc1234", "0.0.0-def5678", 0, false},
		{"0.0.0-abc1234", "0.1.0", 0, false},
		{"0.1.0-dirty", "0.1.0", 0, false},
		{"0.1.0-2-gabc1234-dirty", "0.1.0", 0, false},
		{"dev", "0.1.0", 0, false},
		{"", "0.1.0", 0, false},
		{"1.x.0", "1.0.0", 0, false},
		{"1.2.3.4", "1.2.3", 0, false},
		{"1.2.3-", "1.2.3", 0, false},
	} {
		order, ok := CompareVersions(tc.a, tc.b)
		if order != tc.order || ok != tc.ok {
			t.Errorf("CompareVersions(%q, %q) = %d, %v; want %d, %v", tc.a, tc.b, order, ok, tc.order, tc.ok)
		}
		if tc.ok {
			if back, _ := CompareVersions(tc.b, tc.a); back != -tc.order {
				t.Errorf("CompareVersions(%q, %q) = %d, not antisymmetric", tc.b, tc.a, back)
			}
		}
	}
}
