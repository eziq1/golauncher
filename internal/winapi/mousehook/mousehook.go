// Package mousehook watches the mouse system-wide for the Action Ring
// (docs/action-ring.md): a low-level mouse hook (WH_MOUSE_LL) on Windows,
// an event tap (CGEventTap) on macOS. It reports the middle button, and the
// pointer and the other buttons only while the Handler asks for them, to a
// ring.Machine, which decides on the hook's thread whether each event is
// kept from the apps; it replays the middle button the machine held back,
// marked so that the hook lets it pass. Nothing is recorded.
//
// Tests never install the hook: TestRealHook, behind GOLAUNCHER_HOOK_TESTS=1,
// is the only one that does.
package mousehook

import "github.com/eziq1/golauncher/internal/ring"

// Handler decides about the hook's events (ring.Machine). Its methods run
// on the hook's thread and must return fast: Windows removes a low-level
// hook that keeps it waiting, and macOS turns off a slow event tap.
type Handler interface {
	Handle(ring.Event) (swallow bool)
	// WantsMoves and WantsButtons tell whether the pointer's moves and the
	// other buttons matter now; the hook skips them otherwise.
	WantsMoves() bool
	WantsButtons() bool
}

// App is the app the pointer is over (Windows) or the frontmost one
// (macOS), as AppAt finds it.
type App struct {
	// Name is the executable's name ("mstsc.exe"), Path its path; on macOS
	// also the bundle identifier and the app's name.
	Name, Path, Bundle, Title string
	// Blocked: the middle button cannot be given back to it (Windows: it
	// runs as administrator and golauncher does not, and UIPI blocks
	// injected input), so the ring must leave it alone.
	Blocked bool
	// FullScreen: a full-screen exclusive app (a game) has the screen.
	FullScreen bool
}

// marker is the extra information of the middle button events this
// package injects ("GLRI"), so that the hook tells them from the user's.
const marker = 0x474C5249
