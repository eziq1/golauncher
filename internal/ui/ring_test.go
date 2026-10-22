package ui_test

import (
	"image/png"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/ring"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// ringHost records what the ring view asks of its window.
type ringHost struct{ calls []string }

func (h *ringHost) Choose(i int) { h.calls = append(h.calls, "choose "+string(rune('0'+i))) }
func (h *ringHost) Close()       { h.calls = append(h.calls, "close") }

func (h *ringHost) take() []string {
	c := h.calls
	h.calls = nil
	return c
}

// newRingView is the ring with four fixed slots and one context slot.
func newRingView(t *testing.T, locale string) (*ui.Ring, *mui.Tester, *ringHost) {
	t.Helper()
	env, _ := newEnv(t, locale)
	host := &ringHost{}
	r := ui.NewRing(env, host)
	slots := []ring.Slot{
		{Key: "ring:launcher", Title: "Open golauncher", Icon: plugin.Icon{Name: "command"}, Fixed: true},
		{Key: "ring:askai", Title: "Ask AI", Icon: plugin.Icon{Name: "sparkles"}, Fixed: true},
		{Key: "ring:screenshot", Title: "Screenshot", Icon: plugin.Icon{Name: "scissors"}, Fixed: true},
		{Key: "builtin.system/system:lock", Title: "Lock", Icon: plugin.Icon{Name: "lock"}, Fixed: true},
		{Key: "builtin.files/terminal", Title: "Open Terminal Here", Icon: plugin.Icon{Name: "terminal"}},
	}
	r.Show(ring.Arrange(slots))
	tt := mui.NewTester(r.View, ui.RingWidth, ui.RingHeight)
	tt.SetLocale(locale)
	tt.Frame()
	tt.Advance(time.Second) // past the opening
	tt.Frame()
	return r, tt, host
}

// count is how many texts (and labels) of the frame are s.
func count(tt *mui.Tester, s string) int {
	return len(slices.DeleteFunc(tt.Texts(), func(x string) bool { return x != s }))
}

// slotCenter is the centre of slot i in the ring's window.
func slotCenter(i int) (float32, float32) {
	dx, dy := ring.SlotOffset(i, ui.RingRadius)
	return float32(ui.RingWidth/2 + dx), float32(ui.RingHeight/2 + dy)
}

// TestRingView: the slots show by their names (labels), the name of the
// hovered one shows outside the ring with its number key, a click runs it,
// the number keys choose a slot, Escape, the × and a click outside close.
func TestRingView(t *testing.T) {
	r, tt, host := newRingView(t, "en")
	for _, s := range []string{"Open golauncher", "Ask AI", "Screenshot", "Lock", "Open Terminal Here", "Close"} {
		if count(tt, s) != 1 {
			t.Fatalf("%q shows %d times in %q", s, count(tt, s), tt.Texts())
		}
	}
	// Hovered: the name shows in a pill, with the slot's number.
	x, y := slotCenter(2) // right: Ask AI
	tt.Move(x, y)
	tt.Frame()
	if count(tt, "Ask AI") != 2 || count(tt, "3") != 1 {
		t.Fatalf("hovered: %q", tt.Texts())
	}
	if lbl, ok := tt.Find("3"); !ok || lbl.X < x {
		t.Errorf("the pill of the right slot is at %+v, not right of it", lbl)
	}
	x, y = slotCenter(6) // left: Lock
	tt.Move(x, y)
	tt.Frame()
	if count(tt, "Ask AI") != 1 || count(tt, "Lock") != 2 {
		t.Fatalf("hovered left: %q", tt.Texts())
	}
	if lbl, ok := tt.Find("7"); !ok || lbl.X+lbl.W > x {
		t.Errorf("the pill of the left slot is at %+v, not left of it", lbl)
	}
	tt.Move(5, 5) // the empty corner: no pill
	tt.Frame()
	if count(tt, "Lock") != 1 {
		t.Fatalf("not hovered: %q", tt.Texts())
	}

	// A drag highlights through the shell; the pill follows.
	if !r.SetHighlight(1) || r.SetHighlight(1) {
		t.Error("SetHighlight did not report the change once")
	}
	tt.Frame()
	if count(tt, "Open Terminal Here") != 2 {
		t.Fatalf("highlighted: %q", tt.Texts())
	}
	if r.SetHighlight(5) || count(tt, "Open Terminal Here") != 2 { // an empty slot: no highlight
		t.Log("highlight of an empty slot")
	}
	r.SetHighlight(-1)
	tt.Frame()

	// A click on a slot.
	if err := tt.Click("Screenshot"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := host.take(); !slices.Equal(got, []string{"choose 4"}) {
		t.Errorf("click: %q", got)
	}
	// The number keys and Escape.
	tt.Key(0, mui.Key2)
	tt.Frame()
	tt.Key(0, mui.Key6) // an empty slot: nothing
	tt.Frame()
	tt.Key(0, mui.KeyEscape)
	tt.Frame()
	if got := host.take(); !slices.Equal(got, []string{"choose 1", "close"}) {
		t.Errorf("keys: %q", got)
	}
	// The keys the shell catches globally.
	if !r.Key(mui.Key1) || r.Key(mui.Key8) || r.Key(mui.KeyA) {
		t.Error("Key reported wrong")
	}
	if got := host.take(); !slices.Equal(got, []string{"choose 0"}) {
		t.Errorf("global keys: %q", got)
	}
	// The ×, and a click outside the ring.
	if err := tt.Click("Close"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.ClickAt(5, 5)
	tt.Frame()
	tt.ClickAt(ui.RingWidth/2+ui.RingRadius*0.7, ui.RingHeight/2) // on the disc between slots: nothing
	tt.Frame()
	if got := host.take(); !slices.Equal(got, []string{"close", "close"}) {
		t.Errorf("close: %q", got)
	}

	if dir := os.Getenv("GOLAUNCHER_RING_PNG"); dir != "" { // a look for the author, never committed
		tt.Move(slotCenter(2))
		tt.Frame()
		f, err := os.Create(dir)
		if err == nil {
			png.Encode(f, tt.Image())
			f.Close()
		}
	}
}

// TestRingViewChinese: the close button's name and a long slot name.
func TestRingViewChinese(t *testing.T) {
	r, tt, _ := newRingView(t, "zh-CN")
	if !tt.HasText("关闭") {
		t.Fatalf("texts %q", tt.Texts())
	}
	l := r.Layout()
	long := *l[0]
	long.Title = strings.Repeat("很长的名称", 10)
	l[0] = &long
	r.SetLayout(l)
	r.SetHighlight(0)
	tt.Frame()
	if lbl, ok := tt.Find(long.Title); !ok || lbl.Y < 0 {
		t.Errorf("the long name's pill: %+v %v", lbl, ok)
	}
}

// TestRingOwn: the ring's own actions by name, in both languages.
func TestRingOwn(t *testing.T) {
	for locale, want := range map[string][]string{"en": {"Open golauncher", "Ask AI", "Screenshot"}, "zh-CN": {"打开 golauncher", "问 AI", "截图"}} {
		env, _ := newEnv(t, locale)
		own := ui.RingOwn(env.I18n.Localizer("core"))
		for i, key := range []string{"ring:launcher", "ring:askai", "ring:screenshot"} {
			if title, icon, ok := own(key); !ok || title != want[i] || icon == "" {
				t.Errorf("%s %s: %q %q %v", locale, key, title, icon, ok)
			}
		}
		if _, _, ok := own("builtin.system/system:lock"); ok {
			t.Error("an entry taken for the ring's own")
		}
	}
}
