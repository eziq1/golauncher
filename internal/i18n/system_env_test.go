//go:build !windows

package i18n

import (
	"slices"
	"testing"
)

func TestEnvLanguages(t *testing.T) {
	for _, tt := range []struct {
		env  map[string]string
		want []string
	}{
		{map[string]string{"LANG": "zh_CN.UTF-8"}, []string{"zh-CN"}},
		{map[string]string{"LC_ALL": "en_GB", "LANG": "zh_CN.UTF-8"}, []string{"en-GB"}},
		{map[string]string{"LC_MESSAGES": "ja_JP.eucJP"}, []string{"ja-JP"}},
		{map[string]string{"LANG": "C.UTF-8"}, nil},
		{map[string]string{}, nil},
	} {
		if got := envLanguages(func(k string) string { return tt.env[k] }); !slices.Equal(got, tt.want) {
			t.Errorf("envLanguages(%v) = %q, want %q", tt.env, got, tt.want)
		}
	}
}
