//go:build darwin

package ns

import (
	"bytes"
	"slices"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	Pool(func() {
		for _, s := range []string{"", "hello", "你好, 世界", "a\x00b"} {
			if got := GoString(String(s)); got != s && s != "a\x00b" {
				t.Errorf("GoString(String(%q)) = %q", s, got)
			}
		}
		b := []byte{0, 1, 2, 255}
		if got := Bytes(Data(b)); !bytes.Equal(got, b) {
			t.Errorf("Bytes(Data) = %v", got)
		}
		if got := Strings(StringArray("a", "β")); !slices.Equal(got, []string{"a", "β"}) {
			t.Errorf("Strings = %q", got)
		}
		if Class("NoSuchClassAnywhere") != 0 {
			t.Error("missing class found")
		}
		if Send(0, "description") != 0 || SendBool(0, "isEqual:", ID(0)) {
			t.Error("message to nil")
		}
	})
}
