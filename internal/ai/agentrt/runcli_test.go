package agentrt

import "testing"

// TestCapped: RunCLI's output buffer keeps what fits and notes the rest.
func TestCapped(t *testing.T) {
	c := &capped{max: 5}
	for _, s := range []string{"abc", "de", "f"} {
		if n, err := c.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", s, n, err)
		}
	}
	if s, over := c.result(); s != "abcde" || !over {
		t.Fatalf("result %q, %v", s, over)
	}
	c = &capped{max: 5}
	c.Write([]byte("abc"))
	if s, over := c.result(); s != "abc" || over {
		t.Fatalf("result %q, %v", s, over)
	}
}
