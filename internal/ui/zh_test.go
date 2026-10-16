package ui_test

import (
	"image"
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/ui"
)

// checkLayout fails if one of texts is missing, empty, outside the window
// or overlapping another of them: what a translation that is too long or a
// missing glyph would show as.
func checkLayout(t *testing.T, tt *mui.Tester, width, height float32, texts ...string) {
	t.Helper()
	var rects []mui.Rect
	for _, s := range texts {
		r, ok := tt.Find(s)
		if !ok {
			t.Fatalf("no %q in %q", s, tt.Texts())
		}
		if r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > width+0.5 || r.Y+r.H > height+0.5 {
			t.Errorf("%q at %+v is empty or outside the %vx%v window", s, r, width, height)
		}
		for i, o := range rects {
			if r.X < o.X+o.W && o.X < r.X+r.W && r.Y < o.Y+o.H && o.Y < r.Y+r.H {
				t.Errorf("%q at %+v overlaps %q at %+v", s, r, texts[i], o)
			}
		}
		rects = append(rects, r)
	}
}

func TestLauncherChinese(t *testing.T) {
	lt := newLauncher(t, "zh-CN")
	lt.tt.SetLocale("zh-CN")
	lt.step()
	lt.wantText("命令") // the section and the accessory
	checkLayout(t, lt.tt, ui.LauncherWidth, ui.LauncherHeight, "搜索", "Browse Fruits", "后台任务", "打开命令", "操作")

	lt.typeText("terminal")
	lt.key(mui.Ctrl, mui.KeyK)
	lt.wantText("搜索操作…")
	lt.wantText("Copy Path") // the test plugin's own action is not translated
	lt.key(0, mui.KeyEscape)
}

func TestSettingsChinese(t *testing.T) {
	env, s, tt := newSettings(t, "zh-CN")
	tt.SetLocale("zh-CN")
	tt.Frame()
	if env.I18n.Locale() != "zh-CN" {
		t.Fatalf("locale = %q", env.I18n.Locale())
	}
	checkLayout(t, tt, ui.SettingsWidth, ui.SettingsHeight,
		"通用", "外观", "插件", "高级", "关于",
		"启动器快捷键", "在任何地方显示或隐藏启动器。", "登录时启动", "失去焦点时隐藏", "情境感知", "语言")
	s.ShowPage(ui.PageAppearance)
	tt.SetSize(ui.SettingsWidth, 2400) // the whole page, without scrolling
	tt.Frame()
	checkLayout(t, tt, ui.SettingsWidth, 2400, "外观", "宣纸", "高对比度（深色）", "跟随 Windows 浅色和深色模式", "浅色模式时", "深色模式时", "背景不透明度", "强调色", "恢复主题默认", "文字衬底", "导出当前主题…")
	tt.SetSize(ui.SettingsWidth, ui.SettingsHeight)
	for _, page := range []string{ui.PagePlugins, ui.PageAdvanced, ui.PageAbout} {
		s.ShowPage(page)
		tt.Frame()
	}
	s.ShowPage(ui.PageAdvanced)
	tt.Frame()
	checkLayout(t, tt, ui.SettingsWidth, ui.SettingsHeight, "数据文件夹", "日志级别", "清除排序记录", "导出设置", "导入设置")
	s.ShowPage(ui.PageAbout)
	tt.Frame()
	if !tt.HasText("使用 MyGo 构建") {
		t.Fatalf("about page = %q", tt.Texts())
	}
}

// TestLanguageSelect switches the language in the settings: the choices are
// named in their own language and the change applies at once.
func TestLanguageSelect(t *testing.T) {
	// Without a locale given, the configured language applies, as in the app.
	env, _ := newEnv(t, "")
	env.Config.Update(func(c *config.Config) { c.Language = "en" })
	s := ui.NewSettings(env, &fakeHost{}, "1.0.0-test")
	tt := mui.NewTester(s.View, ui.SettingsWidth, ui.SettingsHeight)
	tt.Frame()
	if env.I18n.Locale() != "en" || !tt.HasText("Language") {
		t.Fatalf("locale %q, texts %q", env.I18n.Locale(), tt.Texts())
	}
	if err := tt.Click("English"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, want := range []string{"System default", "English", "简体中文"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q among the choices %q", want, tt.Texts())
		}
	}
	if err := tt.Click("简体中文"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := env.Config.Get().Language; got != "zh-CN" {
		t.Fatalf("language = %q", got)
	}
	if got := env.I18n.Locale(); got != "zh-CN" {
		t.Fatalf("locale = %q", got)
	}
	tt.Frame()
	for _, want := range []string{"语言", "通用", "简体中文"} {
		if !tt.HasText(want) {
			t.Fatalf("the settings are not in Chinese: no %q in %q", want, tt.Texts())
		}
	}

	// Back to English, which is called English in Chinese too.
	if err := tt.Click("简体中文"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("English"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if got := env.Config.Get().Language; got != "en" || env.I18n.Locale() != "en" || !tt.HasText("Language") {
		t.Fatalf("language %q, locale %q, texts %q", got, env.I18n.Locale(), tt.Texts())
	}
}

// TestChineseGlyphs makes sure the text engine draws Chinese: different
// characters give different images, which missing-glyph boxes would not.
func TestChineseGlyphs(t *testing.T) {
	draw := func(s string) *image.RGBA {
		return mui.Render(func(c *mui.Context) { mui.Text(c, s).FontSize(20) }, 60, 40, 1)
	}
	a, b := draw("中"), draw("国")
	if inked(a) == 0 {
		t.Fatal("中 drew nothing")
	}
	if sameImage(a, b) {
		t.Fatal("中 and 国 look the same: the font has no Chinese glyphs")
	}
}

func inked(img *image.RGBA) int {
	n := 0
	bg := img.RGBAAt(0, 0)
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			if img.RGBAAt(x, y) != bg {
				n++
			}
		}
	}
	return n
}

func sameImage(a, b *image.RGBA) bool {
	if a.Rect != b.Rect {
		return false
	}
	for y := a.Rect.Min.Y; y < a.Rect.Max.Y; y++ {
		for x := a.Rect.Min.X; x < a.Rect.Max.X; x++ {
			if a.RGBAAt(x, y) != b.RGBAAt(x, y) {
				return false
			}
		}
	}
	return true
}
