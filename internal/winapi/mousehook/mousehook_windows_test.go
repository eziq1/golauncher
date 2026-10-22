//go:build windows

package mousehook

import (
	"os"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"github.com/eziq1/golauncher/internal/ring"
)

// fakeHandler records the events it gets and swallows the middle button.
type fakeHandler struct {
	moves, buttons bool
	got            []ring.Event
}

func (h *fakeHandler) Handle(ev ring.Event) bool {
	h.got = append(h.got, ev)
	return ev.Kind == ring.MiddleDown || ev.Kind == ring.MiddleUp
}
func (h *fakeHandler) WantsMoves() bool   { return h.moves }
func (h *fakeHandler) WantsButtons() bool { return h.buttons }

// call runs the hook's decision on a made-up MSLLHOOKSTRUCT, without any
// hook installed.
func call(h Handler, msg uintptr, info msllhookstruct) bool {
	ok := handle(h, msg, uintptr(unsafe.Pointer(&info)))
	runtime.KeepAlive(&info)
	return ok
}

// TestHandle: the hook reports the middle button always, the moves and
// the other buttons only when asked, with the position; golauncher's own
// replays are marked Own, other programs' injected events Injected; other
// messages pass untouched.
func TestHandle(t *testing.T) {
	h := &fakeHandler{}
	if !call(h, wmMButtonDown, msllhookstruct{X: 10, Y: -20}) || !call(h, wmMButtonUp, msllhookstruct{X: 11, Y: -20}) {
		t.Error("the middle button was not swallowed")
	}
	if call(h, wmMouseMove, msllhookstruct{X: 1}) || call(h, wmLButtonDown, msllhookstruct{}) || call(h, 0x020A /* wheel */, msllhookstruct{}) {
		t.Error("an event swallowed")
	}
	if len(h.got) != 2 || h.got[0] != (ring.Event{Kind: ring.MiddleDown, Pt: ring.Point{X: 10, Y: -20}}) || h.got[1].Kind != ring.MiddleUp {
		t.Fatalf("events %+v", h.got)
	}
	h = &fakeHandler{moves: true, buttons: true}
	call(h, wmMouseMove, msllhookstruct{X: 5, Y: 6})
	call(h, wmRButtonDown, msllhookstruct{})
	call(h, wmXButtonDown, msllhookstruct{})
	call(h, wmMButtonDown, msllhookstruct{ExtraInfo: marker, Flags: llmhfInjected})
	call(h, wmMButtonDown, msllhookstruct{Flags: llmhfInjected})
	call(h, wmMButtonDown, msllhookstruct{Flags: llmhfLowerILInjected, ExtraInfo: 7})
	want := []ring.Event{
		{Kind: ring.Move, Pt: ring.Point{X: 5, Y: 6}},
		{Kind: ring.OtherDown}, {Kind: ring.OtherDown},
		{Kind: ring.MiddleDown, Own: true},
		{Kind: ring.MiddleDown, Injected: true},
		{Kind: ring.MiddleDown, Injected: true},
	}
	if len(h.got) != len(want) {
		t.Fatalf("events %+v", h.got)
	}
	for i := range want {
		if h.got[i] != want[i] {
			t.Errorf("event %d: %+v, want %+v", i, h.got[i], want[i])
		}
	}
}

// TestInputLayout: SendInput takes INPUT of exactly its C size.
func TestInputLayout(t *testing.T) {
	if runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64" {
		if s := unsafe.Sizeof(input{}); s != 40 {
			t.Errorf("sizeof(INPUT) = %d, want 40", s)
		}
		if o := unsafe.Offsetof(input{}.Mi); o != 8 {
			t.Errorf("offsetof(INPUT.mi) = %d, want 8", o)
		}
		if o := unsafe.Offsetof(msllhookstruct{}.ExtraInfo); o != 24 {
			t.Errorf("offsetof(MSLLHOOKSTRUCT.dwExtraInfo) = %d, want 24", o)
		}
	}
}

// TestProcessInfo: this process is known by name, never blocked; the
// threshold is a few pixels.
func TestProcessInfo(t *testing.T) {
	if p := processInfo(uint32(os.Getpid())); p.blocked || p.name == "" {
		t.Errorf("this process: %+v", p)
	}
	if d := DragThreshold(); d < 8 || d > 100 {
		t.Errorf("threshold %v", d)
	}
	if !Supported() || !Trusted() {
		t.Error("not supported on Windows")
	}
}

// TestRealHook installs the real hook for a moment: only with
// GOLAUNCHER_HOOK_TESTS=1, by hand (it sees the user's mouse). Nothing is
// swallowed: the handler only counts.
func TestRealHook(t *testing.T) {
	if os.Getenv("GOLAUNCHER_HOOK_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_HOOK_TESTS=1 to install a real mouse hook")
	}
	h := &countHandler{}
	stop, err := Start(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Start(h); err == nil {
		t.Error("a second hook started")
	}
	time.Sleep(2 * time.Second) // move the mouse meanwhile
	stop()
	stop()
	t.Logf("moves seen: %d", h.n)
}

type countHandler struct{ n int }

func (h *countHandler) Handle(ring.Event) bool { h.n++; return false }
func (h *countHandler) WantsMoves() bool       { return true }
func (h *countHandler) WantsButtons() bool     { return false }
