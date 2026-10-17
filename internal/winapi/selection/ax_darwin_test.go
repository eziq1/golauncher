package winselection

import (
	"os"
	"testing"
	"time"
)

func TestAXKind(t *testing.T) {
	for _, tt := range []struct {
		role, sub          string
		names              []string
		password, readable bool
	}{
		{"AXTextArea", "", []string{"AXValue", "AXSelectedText"}, false, true},
		{"AXTextField", "AXSecureTextField", []string{"AXValue", "AXSelectedText"}, true, false},
		{"AXSecureTextField", "", nil, true, false},
		{"AXButton", "", []string{"AXTitle"}, false, false},
		{"AXWebArea", "", []string{"AXSelectedText", "AXSelectedTextMarkerRange"}, false, true},
	} {
		p, r := axKind(tt.role, tt.sub, tt.names)
		if p != tt.password || r != tt.readable {
			t.Errorf("axKind(%q, %q) = %v, %v", tt.role, tt.sub, p, r)
		}
	}
}

// TestRealFocus reads the focused element of this Mac (the terminal running
// the test), read-only. It needs the Accessibility permission of that
// terminal; set GOLAUNCHER_AX_TESTS=1.
func TestRealFocus(t *testing.T) {
	if os.Getenv("GOLAUNCHER_AX_TESTS") != "1" {
		t.Skip("reads another app through Accessibility; set GOLAUNCHER_AX_TESTS=1")
	}
	if err := permission(); err != nil {
		t.Skip(err)
	}
	fi, ok := probeFocus(time.Second)
	t.Logf("focus %+v ok %v", fi, ok)
	if fi.elem != nil {
		parts, editable, err := fi.elem.read(t.Context(), 1000)
		t.Logf("ranges %d editable %v err %v", len(parts), editable, err)
		fi.elem.release()
	}
}
