//go:build windows

package clip

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image/png"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/eziq1/golauncher/pkg/plugin"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")

	procOpenClipboard                 = user32.NewProc("OpenClipboard")
	procCloseClipboard                = user32.NewProc("CloseClipboard")
	procEmptyClipboard                = user32.NewProc("EmptyClipboard")
	procGetClipboardData              = user32.NewProc("GetClipboardData")
	procSetClipboardData              = user32.NewProc("SetClipboardData")
	procIsClipboardFormatAvailable    = user32.NewProc("IsClipboardFormatAvailable")
	procRegisterClipboardFormatW      = user32.NewProc("RegisterClipboardFormatW")
	procGetClipboardOwner             = user32.NewProc("GetClipboardOwner")
	procGetClipboardSequenceNumber    = user32.NewProc("GetClipboardSequenceNumber")
	procAddClipboardFormatListener    = user32.NewProc("AddClipboardFormatListener")
	procRemoveClipboardFormatListener = user32.NewProc("RemoveClipboardFormatListener")
	procRegisterClassExW              = user32.NewProc("RegisterClassExW")
	procCreateWindowExW               = user32.NewProc("CreateWindowExW")
	procDestroyWindow                 = user32.NewProc("DestroyWindow")
	procDefWindowProcW                = user32.NewProc("DefWindowProcW")
	procGetMessageW                   = user32.NewProc("GetMessageW")
	procDispatchMessageW              = user32.NewProc("DispatchMessageW")
	procPostMessageW                  = user32.NewProc("PostMessageW")
	procPostQuitMessage               = user32.NewProc("PostQuitMessage")
	procIsWindow                      = user32.NewProc("IsWindow")
	procIsIconic                      = user32.NewProc("IsIconic")
	procShowWindow                    = user32.NewProc("ShowWindow")
	procSetForegroundWindow           = user32.NewProc("SetForegroundWindow")
	procBringWindowToTop              = user32.NewProc("BringWindowToTop")
	procGetAncestor                   = user32.NewProc("GetAncestor")
	procAttachThreadInput             = user32.NewProc("AttachThreadInput")
	procSendInput                     = user32.NewProc("SendInput")
	procGetAsyncKeyState              = user32.NewProc("GetAsyncKeyState")
	procGlobalAlloc                   = kernel32.NewProc("GlobalAlloc")
	procGlobalFree                    = kernel32.NewProc("GlobalFree")
	procGlobalLock                    = kernel32.NewProc("GlobalLock")
	procGlobalUnlock                  = kernel32.NewProc("GlobalUnlock")
	procGlobalSize                    = kernel32.NewProc("GlobalSize")
	procGetModuleHandleW              = kernel32.NewProc("GetModuleHandleW")
	procDragQueryFileW                = shell32.NewProc("DragQueryFileW")
)

const (
	cfDIB         = 8
	cfUnicodeText = 13
	cfHDROP       = 15
	cfDIBV5       = 17

	gmemMoveable = 0x0002
	maxDataSize  = 256 << 20

	wmDestroy         = 0x0002
	wmClose           = 0x0010
	wmClipboardUpdate = 0x031D
	hwndMessage       = ^uintptr(2) // (HWND)-3

	swRestore    = 9
	gaRootOwner  = 3
	inputKeybd   = 1
	keyUp        = 0x0002
	keyExtended  = 0x0001
	vkShift      = 0x10
	vkControl    = 0x11
	vkMenu       = 0x12
	vkLWin       = 0x5B
	vkRWin       = 0x5C
	vkV          = 0x56
	errClassUsed = 1410 // ERROR_CLASS_ALREADY_EXISTS
)

// registered holds the ids of registered clipboard formats.
type registered struct {
	png, exclude, viewerIgnore, history, cloud uintptr
}

var formats = sync.OnceValue(func() registered {
	return registered{
		png:          registerFormat("PNG"),
		exclude:      registerFormat("ExcludeClipboardContentFromMonitorProcessing"),
		viewerIgnore: registerFormat("Clipboard Viewer Ignore"),
		history:      registerFormat("CanIncludeInClipboardHistory"),
		cloud:        registerFormat("CanUploadToCloudClipboard"),
	}
})

func registerFormat(name string) uintptr {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0
	}
	r, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(p)))
	return r
}

// withClipboard runs fn with the clipboard open. The clipboard is opened
// and closed on the same, locked OS thread, retrying while another program
// holds it. While a watcher runs, its window becomes the owner of what we
// write, which lets Watch recognize our own writes.
func withClipboard(fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	opened := false
	for i := range 10 {
		if r, _, _ := procOpenClipboard.Call(ownerWindow()); r != 0 {
			opened = true
			break
		}
		if i < 9 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !opened {
		return ErrBusy
	}
	defer procCloseClipboard.Call()
	return fn()
}

func available(format uintptr) bool {
	if format == 0 {
		return false
	}
	r, _, _ := procIsClipboardFormatAvailable.Call(format)
	return r != 0
}

// getData copies the data of format from the open clipboard.
func getData(format uintptr) ([]byte, bool) {
	h, _, _ := procGetClipboardData.Call(format)
	if h == 0 {
		return nil, false
	}
	n, _, _ := procGlobalSize.Call(h)
	if n == 0 || n > maxDataSize {
		return nil, false
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return nil, false
	}
	defer procGlobalUnlock.Call(h)
	return bytes.Clone(unsafe.Slice((*byte)(native(p)), n)), true
}

// setData puts data on the open, emptied clipboard.
func setData(format uintptr, data []byte) error {
	h, _, err := procGlobalAlloc.Call(gmemMoveable, uintptr(max(len(data), 1)))
	if h == 0 {
		return fmt.Errorf("clip: GlobalAlloc: %w", err)
	}
	p, _, err := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("clip: GlobalLock: %w", err)
	}
	copy(unsafe.Slice((*byte)(native(p)), len(data)), data)
	procGlobalUnlock.Call(h)
	if r, _, err := procSetClipboardData.Call(format, h); r == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("clip: SetClipboardData: %w", err)
	}
	return nil
}

// Read returns the current clipboard content: Unicode text and a file list
// (CF_HDROP) and, only when there is neither, an image as PNG (the
// registered "PNG" format, else a converted CF_DIBV5/CF_DIB). Content that
// asks not to be recorded in clipboard history only has SourceApp and
// Exclude set. It is ReadWith(ReadOptions{Images: true}).
func Read() (plugin.ClipboardContent, error) {
	return ReadWith(ReadOptions{Images: true})
}

// ReadWith is Read with options. The clipboard is held open only while raw
// bytes are copied; image formats are requested only when they will be
// used, since that can force the source program to render them (delayed
// rendering, e.g. Excel), and conversion to PNG happens after the clipboard
// is closed.
func ReadWith(opts ReadOptions) (plugin.ClipboardContent, error) {
	var (
		c    plugin.ClipboardContent
		pid  uint32
		text []byte
		img  rawImage
	)
	err := withClipboard(func() error {
		pid = ownerPID()
		if c.Exclude = excluded(); c.Exclude {
			return nil
		}
		if available(cfUnicodeText) {
			text, _ = getData(cfUnicodeText)
		}
		if available(cfHDROP) {
			c.Files = hdropFiles()
		}
		if opts.Images && len(c.Files) == 0 && utf16Blank(text) {
			img = readRawImage()
		}
		return nil
	})
	if err != nil {
		return plugin.ClipboardContent{}, err
	}
	if pid != 0 {
		c.SourceApp = processName(pid)
	}
	c.Text = utf16String(text)
	c.Image = img.toPNG()
	return c, nil
}

// excluded reports whether the open clipboard's content asks not to be
// recorded in clipboard history.
func excluded() bool {
	f := formats()
	if available(f.exclude) || available(f.viewerIgnore) {
		return true
	}
	if available(f.history) {
		if b, ok := getData(f.history); ok && len(b) >= 4 && binary.LittleEndian.Uint32(b) == 0 {
			return true
		}
	}
	return false
}

// readRawImage copies the first usable image format from the open
// clipboard without decoding it: the registered "PNG" format, else
// CF_DIBV5, else CF_DIB. Only headers are checked here.
func readRawImage() rawImage {
	if f := formats().png; available(f) {
		if b, ok := getData(f); ok && bytes.HasPrefix(b, pngSignature) {
			return rawImage{data: b, isPNG: true}
		}
	}
	for _, f := range []uintptr{cfDIBV5, cfDIB} {
		if !available(f) {
			continue
		}
		if b, ok := getData(f); ok && dibSupported(b) {
			return rawImage{data: b}
		}
	}
	return rawImage{}
}

func hdropFiles() []string {
	h, _, _ := procGetClipboardData.Call(cfHDROP)
	if h == 0 {
		return nil
	}
	n, _, _ := procDragQueryFileW.Call(h, 0xFFFFFFFF, 0, 0)
	var paths []string
	for i := range n {
		size, _, _ := procDragQueryFileW.Call(h, i, 0, 0)
		buf := make([]uint16, size+1)
		procDragQueryFileW.Call(h, i, uintptr(unsafe.Pointer(&buf[0])), size+1)
		if p := windows.UTF16ToString(buf); p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// ownerPID returns the process id of the clipboard owner, or 0.
func ownerPID() uint32 {
	hwnd, _, _ := procGetClipboardOwner.Call()
	if hwnd == 0 {
		return 0
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(windows.HWND(hwnd), &pid); err != nil {
		return 0
	}
	return pid
}

// processName returns the executable base name of process pid, or "".
func processName(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return filepath.Base(windows.UTF16ToString(buf[:n]))
}

func sequenceNumber() uint32 {
	r, _, _ := procGetClipboardSequenceNumber.Call()
	return uint32(r)
}

// lastWriteSeq is the clipboard sequence number right after our last write.
// Writes open the clipboard without an owner window, so this is how Watch
// recognizes them.
var lastWriteSeq atomic.Uint32

// clipData is one format of a clipboard write.
type clipData struct {
	format uintptr
	data   []byte
}

func write(items ...clipData) error {
	err := withClipboard(func() error {
		if r, _, err := procEmptyClipboard.Call(); r == 0 {
			return fmt.Errorf("clip: EmptyClipboard: %w", err)
		}
		for _, it := range items {
			if err := setData(it.format, it.data); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		lastWriteSeq.Store(sequenceNumber())
	}
	return err
}

// WriteText replaces the clipboard content with text.
func WriteText(s string) error {
	return write(clipData{cfUnicodeText, utf16Bytes(s)})
}

// WriteSecret is WriteText for secrets such as passwords. The content also
// carries the formats that ask clipboard managers (ExcludeClipboardContent-
// FromMonitorProcessing), Windows clipboard history (Win+V) and cloud
// clipboard sync not to record it; the last two hold a DWORD 0.
func WriteSecret(s string) error {
	f := formats()
	items := []clipData{{cfUnicodeText, utf16Bytes(s)}}
	for _, id := range []uintptr{f.exclude, f.history, f.cloud} {
		if id != 0 {
			items = append(items, clipData{id, []byte{0, 0, 0, 0}})
		}
	}
	return write(items...)
}

// WriteImage replaces the clipboard content with a PNG image. It is offered
// both as the registered "PNG" format and as CF_DIB for older programs.
func WriteImage(data []byte) error {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("clip: decode PNG: %w", err)
	}
	return write(clipData{formats().png, data}, clipData{cfDIB, imageToDIB(img)})
}

// WriteFiles replaces the clipboard content with a list of files (CF_HDROP).
func WriteFiles(paths []string) error {
	if len(paths) == 0 {
		return errors.New("clip: no files")
	}
	return write(clipData{cfHDROP, dropFiles(paths)})
}

// watchOwnWrites disables the filter that hides our own writes from Watch.
// Tests set it.
var watchOwnWrites atomic.Bool

// ownWrite reports whether the current clipboard content was written by
// this process.
func ownWrite() bool {
	if watchOwnWrites.Load() {
		return false
	}
	if seq := lastWriteSeq.Load(); seq != 0 && seq == sequenceNumber() {
		return true
	}
	return ownerPID() == windows.GetCurrentProcessId()
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
	Private uint32
}

var (
	watchMu  sync.Mutex
	watchers = map[uintptr]*debouncer{}
)

// ownerWindow returns the window of a running watcher, or 0. Its thread
// pumps messages, so it can receive WM_DESTROYCLIPBOARD as the owner.
func ownerWindow() uintptr {
	watchMu.Lock()
	defer watchMu.Unlock()
	for h := range watchers {
		if isWindow(h) {
			return h
		}
	}
	return 0
}

func wndProc(hwnd, m, wparam, lparam uintptr) uintptr {
	switch m {
	case wmClipboardUpdate:
		watchMu.Lock()
		d := watchers[hwnd]
		watchMu.Unlock()
		if d != nil && !ownWrite() {
			d.trigger()
		}
		return 0
	case wmClose:
		procRemoveClipboardFormatListener.Call(hwnd)
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, m, wparam, lparam)
	return r
}

type windowClass struct {
	name     *uint16
	instance uintptr
}

// watcherClass registers the window class once; callbacks are a limited
// resource, so there is a single window procedure for all watchers.
var watcherClass = sync.OnceValues(func() (windowClass, error) {
	inst, _, _ := procGetModuleHandleW.Call(0)
	name, _ := windows.UTF16PtrFromString("golauncher.clip.watcher")
	wc := wndClassEx{
		WndProc:   windows.NewCallback(wndProc),
		Instance:  inst,
		ClassName: name,
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		if errno, ok := errors.AsType[syscall.Errno](err); !ok || errno != errClassUsed {
			return windowClass{}, fmt.Errorf("clip: RegisterClassEx: %w", err)
		}
	}
	return windowClass{name: name, instance: inst}, nil
})

// Watch calls fn (on its own goroutine) after the clipboard changes. Bursts
// of updates are coalesced, and changes made by this process are ignored.
// stop removes the listener and waits for the watcher to exit; it is safe to
// call more than once.
func Watch(fn func()) (stop func(), err error) {
	cls, err := watcherClass()
	if err != nil {
		return nil, err
	}
	deb := &debouncer{delay: debounceDelay, fn: fn}
	ready := make(chan error, 1)
	done := make(chan struct{})
	var hwnd uintptr
	go func() {
		defer close(done)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		h, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls.name)), 0, 0,
			0, 0, 0, 0, hwndMessage, 0, cls.instance, 0)
		if h == 0 {
			ready <- fmt.Errorf("clip: CreateWindowEx: %w", err)
			return
		}
		watchMu.Lock()
		watchers[h] = deb
		watchMu.Unlock()
		defer func() {
			watchMu.Lock()
			delete(watchers, h)
			watchMu.Unlock()
		}()
		if r, _, err := procAddClipboardFormatListener.Call(h); r == 0 {
			procDestroyWindow.Call(h)
			ready <- fmt.Errorf("clip: AddClipboardFormatListener: %w", err)
			return
		}
		hwnd = h
		ready <- nil
		var m msg
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				break
			}
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
		if isWindow(h) { // GetMessage failed
			procRemoveClipboardFormatListener.Call(h)
			procDestroyWindow.Call(h)
		}
	}()
	if err := <-ready; err != nil {
		<-done
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			deb.stop()
			procPostMessageW.Call(hwnd, wmClose, 0, 0)
			<-done
		})
	}, nil
}

func isWindow(hwnd uintptr) bool {
	r, _, _ := procIsWindow.Call(hwnd)
	return r != 0
}

// ForegroundWindow returns the current foreground window, or 0.
func ForegroundWindow() uintptr {
	return uintptr(windows.GetForegroundWindow())
}

func rootOwner(hwnd uintptr) uintptr {
	r, _, _ := procGetAncestor.Call(hwnd, gaRootOwner)
	return r
}

func isForeground(hwnd uintptr) bool {
	fg := ForegroundWindow()
	return fg != 0 && (fg == hwnd || rootOwner(fg) == rootOwner(hwnd))
}

func waitForeground(hwnd uintptr, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if isForeground(hwnd) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func windowThread(hwnd uintptr) uint32 {
	tid, _ := windows.GetWindowThreadProcessId(windows.HWND(hwnd), nil)
	return tid
}

// activate brings hwnd to the foreground and waits until it is there.
func activate(hwnd uintptr) error {
	if r, _, _ := procIsIconic.Call(hwnd); r != 0 {
		procShowWindow.Call(hwnd, swRestore)
	}
	if isForeground(hwnd) {
		return nil
	}
	if r, _, _ := procSetForegroundWindow.Call(hwnd); r != 0 && waitForeground(hwnd, 50*time.Millisecond) {
		return nil
	}
	// Windows refuses to let background threads steal the focus; joining the
	// input queues of the foreground and target threads lifts that.
	func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		cur := windows.GetCurrentThreadId()
		var attached []uint32
		for _, w := range []uintptr{ForegroundWindow(), hwnd} {
			if w == 0 {
				continue
			}
			if tid := windowThread(w); tid != 0 && tid != cur {
				if r, _, _ := procAttachThreadInput.Call(uintptr(cur), uintptr(tid), 1); r != 0 {
					attached = append(attached, tid)
				}
			}
		}
		procBringWindowToTop.Call(hwnd)
		procSetForegroundWindow.Call(hwnd)
		for _, tid := range attached {
			procAttachThreadInput.Call(uintptr(cur), uintptr(tid), 0)
		}
	}()
	if !waitForeground(hwnd, 300*time.Millisecond) {
		return ErrNotForeground
	}
	return nil
}

// mouseInput mirrors Win32 MOUSEINPUT, the largest member of the INPUT
// union; it is only used for its size.
type mouseInput struct {
	Dx, Dy    int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

// keybdInput mirrors Win32 KEYBDINPUT.
type keybdInput struct {
	Vk        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

// input mirrors Win32 INPUT holding a KEYBDINPUT. Go's alignment rules
// place the union where C does (offset 8 on 64-bit, 4 on 32-bit), and the
// padding makes the union as large as MOUSEINPUT: 40 bytes on amd64/arm64,
// 28 on 386. SendInput rejects any other size.
type input struct {
	Type uint32
	Ki   keybdInput
	_    [unsafe.Sizeof(mouseInput{}) - unsafe.Sizeof(keybdInput{})]byte
}

func key(vk uint16, up bool) input {
	in := input{Type: inputKeybd, Ki: keybdInput{Vk: vk}}
	if up {
		in.Ki.Flags |= keyUp
	}
	if vk == vkLWin || vk == vkRWin {
		in.Ki.Flags |= keyExtended
	}
	return in
}

// sendInput injects in and returns an error unless every event was
// inserted. Events are rejected as a whole when blocked by UIPI (the target
// runs elevated) or when another thread blocks input.
func sendInput(in []input) error {
	n, _, err := procSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(input{}))
	if int(n) == len(in) {
		return nil
	}
	if errno, ok := errors.AsType[syscall.Errno](err); ok && errno != 0 {
		return fmt.Errorf("clip: SendInput inserted %d of %d events: %w", n, len(in), err)
	}
	return fmt.Errorf("clip: SendInput inserted %d of %d events (input blocked, e.g. the target runs elevated)", n, len(in))
}

func keyHeld(vk uint16) bool {
	r, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	return r&0x8000 != 0
}

// PasteTo brings hwnd to the foreground and sends Ctrl+V to it. Modifier
// keys still held from a hotkey (Alt, Shift, Win) are released first; Ctrl
// is pressed before Alt is released so that no menu bar activates.
func PasteTo(hwnd uintptr) error {
	if hwnd == 0 || !isWindow(hwnd) {
		return errors.New("clip: invalid target window")
	}
	if err := activate(hwnd); err != nil {
		return err
	}
	in := []input{key(vkControl, false)}
	for _, vk := range []uint16{vkMenu, vkShift, vkLWin, vkRWin} {
		if keyHeld(vk) {
			in = append(in, key(vk, true))
		}
	}
	in = append(in, key(vkV, false), key(vkV, true), key(vkControl, true))
	if err := sendInput(in); err != nil {
		// Do not leave Ctrl or V stuck down if only part was inserted.
		_ = sendInput([]input{key(vkV, true), key(vkControl, true)})
		return err
	}
	return nil
}

// native converts a Win32 memory address (from GlobalLock) to a pointer
// without tripping vet's uintptr check; the memory is not Go-managed.
func native(addr uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&addr)) }
