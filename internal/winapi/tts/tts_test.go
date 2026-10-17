package tts

import (
	"reflect"
	"testing"
)

// These tests cover the pure helpers only; nothing here may make a sound.

func TestLCIDFor(t *testing.T) {
	for _, tc := range []struct {
		lang string
		want uint32
	}{
		{"", 0},
		{"xx", 0},
		{"en", 0x409},
		{"EN-us", 0x409},
		{"en-GB", 0x809},
		{"en-NZ", 0x409}, // falls back to the primary subtag
		{"zh", 0x804},
		{"zh-CN", 0x804},
		{"zh_CN", 0x804},
		{"zh-Hans", 0x804},
		{"zh-TW", 0x404},
		{"zh-Hant", 0x404},
		{"ja", 0x411},
		{"ko", 0x412},
		{"fr", 0x40C},
		{"de", 0x407},
		{"es", 0xC0A},
		{"ru", 0x419},
		{"pt", 0x416},
		{"it", 0x410},
	} {
		if got := lcidFor(tc.lang); got != tc.want {
			t.Errorf("lcidFor(%q) = %#x, want %#x", tc.lang, got, tc.want)
		}
	}
}

func TestSAPIText(t *testing.T) {
	for _, tc := range []struct {
		text, lang string
		want       string
		xml        bool
	}{
		{"hello", "", "hello", false},
		{"a<b>&\"c'", "", "a<b>&\"c'", false}, // plain text is not escaped
		{"hello", "xx", "hello", false},
		{"hello", "en", `<voice required="Language=409">hello</voice>`, true},
		{"你好", "zh-CN", `<voice required="Language=804">你好</voice>`, true},
		{"你好", "zh-TW", `<voice required="Language=404">你好</voice>`, true},
		{"x", "fr", `<voice required="Language=40C">x</voice>`, true},
		{"x", "es", `<voice required="Language=C0A">x</voice>`, true},
		{`a<b>&"c'd`, "en", `<voice required="Language=409">a&lt;b&gt;&amp;&quot;c&apos;d</voice>`, true},
		{"a\x00b\x07c\td\ne", "en", "<voice required=\"Language=409\">abc\td\ne</voice>", true},
		{"a\x00b", "", "ab", false},
	} {
		got, xml := sapiText(tc.text, tc.lang)
		if got != tc.want || xml != tc.xml {
			t.Errorf("sapiText(%q, %q) = %q, %v; want %q, %v", tc.text, tc.lang, got, xml, tc.want, tc.xml)
		}
	}
}

const sayOutput = `Albert              en_US    # Hello! My name is Albert.
Bad News            en_US    # Hello! My name is Bad News.
Daniel              en_GB    # Hello! My name is Daniel.
Eddy (English (US)) en_US    # Hello! My name is Eddy.
Eddy (French (France)) fr_FR    # Bonjour, je m’appelle Eddy.
Kyoko               ja_JP    # こんにちは、私の名前はKyokoです。
Yuna                ko_KR    # 안녕하세요. 제 이름은 유나입니다.
Sinji               zh_HK    # 你好！我叫善怡。
Tingting            zh_CN    # 你好，我叫婷婷。
Anna                de_DE    # Hallo! Ich heiße Anna.

garbage
`

func TestParseSayVoices(t *testing.T) {
	got := parseSayVoices(sayOutput)
	want := []sayVoice{
		{"Albert", "en-us"},
		{"Bad News", "en-us"},
		{"Daniel", "en-gb"},
		{"Eddy (English (US))", "en-us"},
		{"Eddy (French (France))", "fr-fr"},
		{"Kyoko", "ja-jp"},
		{"Yuna", "ko-kr"},
		{"Sinji", "zh-hk"},
		{"Tingting", "zh-cn"},
		{"Anna", "de-de"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseSayVoices =\n%v\nwant\n%v", got, want)
	}
}

func TestChooseSayVoice(t *testing.T) {
	voices := parseSayVoices(sayOutput)
	for _, tc := range []struct{ lang, want string }{
		{"", ""},
		{"xx", ""},
		{"en", "Albert"},
		{"en-GB", "Daniel"},
		{"en-AU", "Albert"}, // no en_AU voice: any English one
		{"fr", "Eddy (French (France))"},
		{"de", "Anna"},
		{"ja", "Kyoko"},
		{"ko", "Yuna"},
		{"zh-CN", "Tingting"},
		{"zh", "Tingting"},
		{"zh-TW", "Sinji"}, // no zh_TW voice: zh_HK first
		{"zh_TW", "Sinji"},
		{"ru", ""},
	} {
		if got := chooseSayVoice(voices, tc.lang); got != tc.want {
			t.Errorf("chooseSayVoice(%q) = %q, want %q", tc.lang, got, tc.want)
		}
	}
	// zh-TW prefers a zh_TW voice, and falls back to Mandarin without zh_HK.
	tw := append([]sayVoice{{"Meijia", "zh-tw"}}, voices...)
	if got := chooseSayVoice(tw, "zh-TW"); got != "Meijia" {
		t.Errorf("chooseSayVoice(zh-TW) with Meijia = %q", got)
	}
	cn := []sayVoice{{"Tingting", "zh-cn"}}
	if got := chooseSayVoice(cn, "zh-TW"); got != "Tingting" {
		t.Errorf("chooseSayVoice(zh-TW) with only zh_CN = %q", got)
	}
}
