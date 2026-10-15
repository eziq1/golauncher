package match

import (
	"slices"
	"testing"
)

func TestScoreKinds(t *testing.T) {
	tests := []struct {
		query, target string
		want          float64 // 0 = must not match; otherwise a lower bound
	}{
		{"code", "code", 1},
		{"vis", "Visual Studio Code", 0.9},
		{"stud", "Visual Studio Code", 0.85},
		{"vsc", "Visual Studio Code", 0.8},
		{"vscod", "Visual Studio Code", 0.8},
		{"term", "WindowsTerminal", 0.85},
		{"udio", "Visual Studio Code", 0.7},
		{"vcd", "Visual Studio Code", 0.3},
		{"vs code", "Visual Studio Code", 0.7},
		{"xyz", "Visual Studio Code", 0},
		{"", "anything", 0},
	}
	for _, tt := range tests {
		got := Score(tt.query, tt.target)
		if tt.want == 0 && got != 0 {
			t.Errorf("Score(%q, %q) = %v, want 0", tt.query, tt.target, got)
		}
		if tt.want > 0 && got < tt.want {
			t.Errorf("Score(%q, %q) = %v, want >= %v", tt.query, tt.target, got, tt.want)
		}
	}
}

func TestScoreOrdering(t *testing.T) {
	// A prefix match must beat an acronym match, which must beat a subsequence.
	prefix := Score("vis", "Visual Studio Code")
	acronym := Score("vsc", "Visual Studio Code")
	subseq := Score("vcd", "Visual Studio Code")
	if !(prefix > acronym && acronym > subseq) {
		t.Fatalf("want prefix > acronym > subsequence, got %v %v %v", prefix, acronym, subseq)
	}
}

func TestPinyin(t *testing.T) {
	keys := Keys("微信")
	if !slices.Equal(keys, []string{"微信", "wei xin", "weixin"}) {
		t.Fatalf("Keys = %q", keys)
	}
	best := func(q string) float64 {
		var s float64
		for _, k := range keys {
			s = max(s, Score(q, k))
		}
		return s
	}
	for _, q := range []string{"wx", "weixin", "wei", "微"} {
		if best(q) == 0 {
			t.Errorf("%q does not match 微信", q)
		}
	}
	if got := Keys("Notepad"); !slices.Equal(got, []string{"Notepad"}) {
		t.Errorf("Keys(Notepad) = %q", got)
	}
}
