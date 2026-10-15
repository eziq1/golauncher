package system

import "testing"

func TestHomeEdition(t *testing.T) {
	for ed, want := range map[string]bool{"Core": true, "CoreSingleLanguage": true, "CoreN": true,
		"Professional": false, "Enterprise": false, "Education": false, "": false} {
		if HomeEdition(ed) != want {
			t.Errorf("HomeEdition(%q) = %v", ed, !want)
		}
	}
}
