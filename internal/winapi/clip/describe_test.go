package clip

import "testing"

// TestDescribesImage: the text next to an image that leaves the paste to
// the image (R96), and the real text that does not.
func TestDescribesImage(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"", true},
		{"  \t ", true},
		{"\r\n", true},
		{"https://example.com/photos/a.png", true},
		{"  http://example.com/a.jpg?x=1  ", true},
		{"file:///C:/Users/x/a.png", true},
		{"data:image/png;base64,iVBORw0KGgo=", true},
		{"[图片]", true},
		{"[图片][图片]", true},
		{"[Image] [image]", true},
		{"【图片】", true},
		{"[截图]", true},

		{"hello", false},
		{"42", false},
		{"https://example.com/a.png\r\n", false}, // a spreadsheet cell
		{"see https://example.com/a.png", false},
		{"https://example.com/a.png\nhttps://example.com/b.png", false},
		{"line one\nline two", false},
		{"[x]", false},
		{"[图片] look at this", false},
		{"[图片", false},
		{"mailto:someone@example.com", false},
		{"javascript:alert(1)", false},
		{"data:text/plain,hi", false},
		{"https://", false},
		{"C:\\Users\\x\\a.png", false},
	} {
		if got := DescribesImage(tc.text); got != tc.want {
			t.Errorf("DescribesImage(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}
