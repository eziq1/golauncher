package clip

import (
	"net/url"
	"strings"
)

// DescribesImage reports whether text found on the clipboard next to an
// image only describes that image, so that a paste into a view that accepts
// files takes the image (docs/ai.md §24 修订, R96):
//
//   - blank text (empty or only white space);
//   - the image's address: one http, https or file URL, or a data:image URL,
//     on a single line without a line break (Firefox's and some apps'
//     "Copy Image" put it next to the image);
//   - placeholders such as "[图片]" or "[Image]" and nothing else (chat
//     apps put these next to a copied picture).
//
// Anything else is real text and is pasted as text: words, numbers, several
// lines, a URL with a line break after it (a spreadsheet cell, which also
// comes with a picture of the cell), a URL among other words.
func DescribesImage(text string) bool {
	t := strings.TrimSpace(text)
	switch {
	case t == "":
		return true
	case strings.ContainsAny(text, "\r\n"):
		return false
	case imageURL(t):
		return true
	}
	return placeholders(t)
}

// imageURL reports whether s is a single URL that can be an image's address.
func imageURL(s string) bool {
	if strings.ContainsAny(s, " \t") {
		return false
	}
	if strings.HasPrefix(strings.ToLower(s), "data:image/") {
		return true
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.Host != ""
	case "file":
		return u.Path != ""
	}
	return false
}

// imagePlaceholders are the words chat apps put in brackets for a picture,
// lower case.
var imagePlaceholders = map[string]bool{
	"图片": true, "圖片": true, "图像": true, "圖像": true, "截图": true, "截圖": true,
	"表情": true, "动画表情": true, "動畫表情": true, "画像": true, "사진": true, "이미지": true,
	"image": true, "img": true, "picture": true, "photo": true, "screenshot": true, "sticker": true,
}

// placeholders reports whether s is one or more bracketed image
// placeholders ("[图片]", "[Image] [Image]") and nothing else.
func placeholders(s string) bool {
	n := 0
	for s = strings.TrimSpace(s); s != ""; s = strings.TrimSpace(s) {
		if s[0] != '[' && !strings.HasPrefix(s, "【") {
			return false
		}
		open, closer := "[", "]"
		if s[0] != '[' {
			open, closer = "【", "】"
		}
		end := strings.Index(s, closer)
		if end < 0 || !imagePlaceholders[strings.ToLower(strings.TrimSpace(s[len(open):end]))] {
			return false
		}
		s = s[end+len(closer):]
		n++
	}
	return n > 0
}
