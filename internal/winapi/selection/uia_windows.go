package winselection

import (
	"context"
	"errors"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/eziq1/golauncher/pkg/plugin"
)

func init() {
	windowFacts = windowFactsOf
	processFacts = processInfo
	focusProbe = uiaFocusProbe
	uia.init = uiaThreadInit
}

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	oleaut32 = windows.NewLazySystemDLL("oleaut32.dll")

	procIsWindow         = user32.NewProc("IsWindow")
	procGetAncestor      = user32.NewProc("GetAncestor")
	procGetClassNameW    = user32.NewProc("GetClassNameW")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procSysFreeString    = oleaut32.NewProc("SysFreeString")
	procSysStringLen     = oleaut32.NewProc("SysStringLen")
)

// GUIDs from UIAutomationClient.h.
var (
	clsidCUIAutomation8         = mustGUID("{E22AD333-B25F-460C-83D0-0581107395C9}")
	clsidCUIAutomation          = mustGUID("{FF48DBA4-60EF-4201-AA87-54103EEF594E}")
	iidIUIAutomation            = mustGUID("{30CBE57D-D9D0-452A-AB13-7AC5AC4825EE}")
	iidIUIAutomation2           = mustGUID("{34723AFF-0C9D-49D0-9896-7AB52DF8CD8A}")
	iidIUIAutomationTextPattern = mustGUID("{32EBA289-3583-42C9-9C59-3B6D9A1E9B6A}")
	iidIUIAutomationTextPat2    = mustGUID("{506A921A-FCC9-409F-B23B-37EB74106872}")
	iidIUIAutomationValuePat    = mustGUID("{A94CD8B1-0844-4CD6-9D2D-640537AB39E9}")
)

func mustGUID(s string) windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		panic(err)
	}
	return g
}

// Vtable indexes, counted in the C vtables of UIAutomationClient.h
// (Windows SDK 10.0.22621).
const (
	vtQueryInterface = 0
	vtAddRef         = 1
	vtRelease        = 2

	vtAutomationGetFocusedElement = 8  // IUIAutomation::GetFocusedElement
	vtAutomation2PutConnTimeout   = 61 // IUIAutomation2::put_ConnectionTimeout
	vtAutomation2PutTransTimeout  = 63 // IUIAutomation2::put_TransactionTimeout

	vtElementGetCurrentPatternAs = 14 // IUIAutomationElement::GetCurrentPatternAs
	vtElementCurrentProcessID    = 20 // IUIAutomationElement::get_CurrentProcessId
	vtElementCurrentControlType  = 21 // IUIAutomationElement::get_CurrentControlType
	vtElementCurrentIsPassword   = 35 // IUIAutomationElement::get_CurrentIsPassword

	vtTextPatternGetSelection = 5  // IUIAutomationTextPattern::GetSelection
	vtRangeArrayLength        = 3  // IUIAutomationTextRangeArray::get_Length
	vtRangeArrayGetElement    = 4  // IUIAutomationTextRangeArray::GetElement
	vtTextRangeGetText        = 12 // IUIAutomationTextRange::GetText
	vtValueCurrentIsReadOnly  = 5  // IUIAutomationValuePattern::get_CurrentIsReadOnly
)

// UI Automation identifiers.
const (
	uiaValuePatternID        = 10002
	uiaTextPatternID         = 10014
	uiaTextPattern2ID        = 10024
	uiaEditControlTypeID     = 50004
	uiaDocumentControlTypeID = 50030
)

// uiaTimeout bounds how long UI Automation waits for an unresponsive
// target (IUIAutomation2 connection and transaction timeouts).
const uiaTimeout = 200 // ms

// maxRanges bounds the selected ranges read (multi-selection).
const maxRanges = 64

// comCall calls method i of the COM interface obj.
//
//go:uintptrescapes
func comCall(obj uintptr, i int, args ...uintptr) uintptr {
	vtbl := *(*uintptr)(native(obj))
	fn := *(*uintptr)(unsafe.Add(native(vtbl), i*int(unsafe.Sizeof(uintptr(0)))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

// native converts an address returned by Windows to a pointer; the memory
// is not managed by Go.
func native(addr uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&addr)) }

func release(obj uintptr) {
	if obj != 0 {
		comCall(obj, vtRelease)
	}
}

func failed(hr uintptr) bool { return int32(uint32(hr)) < 0 }

func hresultError(hr uintptr) error { return syscall.Errno(uint32(hr)) }

// uiaState is the UI Automation client of the UI Automation thread. It is
// only touched on that thread.
var uiaState struct {
	automation uintptr    // IUIAutomation
	live       []*uiaElem // elements kept for phase 2, released at teardown
	initErr    error      // CoCreateInstance failed
	created    bool
}

// uiaThreadInit prepares the UI Automation thread: COM in the
// multithreaded apartment. The teardown releases the client and any
// element still kept, then uninitializes COM.
func uiaThreadInit() func() {
	const coinitMultithreaded = 0x0
	hr, _, _ := procCoInitializeEx.Call(0, coinitMultithreaded)
	return func() {
		releaseLive()
		release(uiaState.automation)
		uiaState.automation, uiaState.created, uiaState.initErr = 0, false, nil
		if !failed(hr) {
			procCoUninitialize.Call()
		}
	}
}

// automation returns the thread's IUIAutomation, created on first use.
func automation() (uintptr, error) {
	if uiaState.created {
		return uiaState.automation, uiaState.initErr
	}
	uiaState.created = true
	const clsctxInprocServer = 0x1
	var obj uintptr
	hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidCUIAutomation8)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIUIAutomation)), uintptr(unsafe.Pointer(&obj)))
	if failed(hr) || obj == 0 {
		hr, _, _ = procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidCUIAutomation)), 0, clsctxInprocServer,
			uintptr(unsafe.Pointer(&iidIUIAutomation)), uintptr(unsafe.Pointer(&obj)))
	}
	if failed(hr) || obj == 0 {
		uiaState.initErr = hresultError(hr)
		return 0, uiaState.initErr
	}
	var a2 uintptr
	if !failed(comCall(obj, vtQueryInterface, uintptr(unsafe.Pointer(&iidIUIAutomation2)), uintptr(unsafe.Pointer(&a2)))) && a2 != 0 {
		comCall(a2, vtAutomation2PutConnTimeout, uiaTimeout)
		comCall(a2, vtAutomation2PutTransTimeout, uiaTimeout)
		release(a2)
	}
	uiaState.automation = obj
	return obj, nil
}

// releaseLive releases the elements still kept from earlier captures: a
// new capture makes them stale, and the thread is ending at teardown.
func releaseLive() {
	for _, e := range uiaState.live {
		e.free()
	}
	uiaState.live = nil
}

var errNoElement = errors.New("winselection: no focused element")

// uiaFocusProbe is phase 1 on the UI Automation thread: the focused
// element, its process, whether it is a password field and its
// TextPattern.
func uiaFocusProbe() (focusInfo, error) {
	releaseLive()
	a, err := automation()
	if err != nil {
		return focusInfo{}, err
	}
	var el uintptr
	if hr := comCall(a, vtAutomationGetFocusedElement, uintptr(unsafe.Pointer(&el))); failed(hr) || el == 0 {
		if failed(hr) {
			return focusInfo{}, hresultError(hr)
		}
		return focusInfo{}, errNoElement
	}
	keep := false
	defer func() {
		if !keep {
			release(el)
		}
	}()
	var fi focusInfo
	var pid int32
	comCall(el, vtElementCurrentProcessID, uintptr(unsafe.Pointer(&pid)))
	fi.PID = int(pid)
	if fi.PID == ownPID {
		return fi, nil // never query our own UI from this thread
	}
	var pw int32
	comCall(el, vtElementCurrentIsPassword, uintptr(unsafe.Pointer(&pw)))
	if fi.Password = pw != 0; fi.Password {
		return fi, nil
	}
	tp := patternAs(el, uiaTextPattern2ID, &iidIUIAutomationTextPat2)
	if tp == 0 {
		tp = patternAs(el, uiaTextPatternID, &iidIUIAutomationTextPattern)
	}
	if tp == 0 {
		return fi, nil
	}
	fi.TextPattern = true
	e := &uiaElem{el: el, pattern: tp}
	uiaState.live = append(uiaState.live, e)
	fi.elem = e
	keep = true
	return fi, nil
}

// patternAs returns the pattern interface iid of element el, or 0 when the
// element does not support pattern id.
func patternAs(el uintptr, id int, iid *windows.GUID) uintptr {
	var p uintptr
	if failed(comCall(el, vtElementGetCurrentPatternAs, uintptr(id), uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&p)))) {
		return 0
	}
	return p
}

// uiaElem is a focused element with its TextPattern (IUIAutomationTextPattern
// or IUIAutomationTextPattern2, which extends it), kept for phase 2.
type uiaElem struct {
	el, pattern uintptr
}

func (e *uiaElem) free() {
	release(e.pattern)
	release(e.el)
	e.pattern, e.el = 0, 0
}

func (e *uiaElem) release() {
	e.free()
	for i, x := range uiaState.live {
		if x == e {
			uiaState.live = append(uiaState.live[:i], uiaState.live[i+1:]...)
			break
		}
	}
}

func (e *uiaElem) read(ctx context.Context, units int) ([]string, bool, error) {
	if e.pattern == 0 {
		return nil, false, errReleased
	}
	ranges, err := e.selection(units)
	if err == nil && len(ranges) == 0 {
		// Chromium answers the first query before its accessibility tree
		// exists; ask once more.
		if dl, ok := ctx.Deadline(); !ok || time.Until(dl) > 2*chromiumRetry {
			select {
			case <-time.After(chromiumRetry):
				ranges, err = e.selection(units)
			case <-ctx.Done():
			}
		}
	}
	if err != nil {
		return nil, false, err
	}
	if len(ranges) == 0 {
		return nil, false, nil
	}
	return ranges, e.editable(), nil
}

// selection reads the texts of the selected ranges, each at most units
// UTF-16 units, until the total exceeds units.
func (e *uiaElem) selection(units int) ([]string, error) {
	var arr uintptr
	if hr := comCall(e.pattern, vtTextPatternGetSelection, uintptr(unsafe.Pointer(&arr))); failed(hr) {
		return nil, hresultError(hr)
	}
	if arr == 0 {
		return nil, nil
	}
	defer release(arr)
	var n int32
	if hr := comCall(arr, vtRangeArrayLength, uintptr(unsafe.Pointer(&n))); failed(hr) {
		return nil, hresultError(hr)
	}
	var out []string
	total := 0
	for i := range min(int(n), maxRanges) {
		if total > units {
			break
		}
		var r uintptr
		if failed(comCall(arr, vtRangeArrayGetElement, uintptr(i), uintptr(unsafe.Pointer(&r)))) || r == 0 {
			continue
		}
		var bstr uintptr
		hr := comCall(r, vtTextRangeGetText, uintptr(int32(units)), uintptr(unsafe.Pointer(&bstr)))
		release(r)
		if failed(hr) {
			return nil, hresultError(hr)
		}
		s := bstrText(bstr)
		total += len(s)
		out = append(out, s)
	}
	return out, nil
}

// bstrText converts and frees a BSTR.
func bstrText(b uintptr) string {
	if b == 0 {
		return ""
	}
	defer procSysFreeString.Call(b)
	n, _, _ := procSysStringLen.Call(b)
	if n == 0 {
		return ""
	}
	return utf16Text(unsafe.Slice((*uint16)(native(b)), n))
}

// editable reports whether the element is an editable text control: an
// Edit or Document whose ValuePattern is not read-only, or an Edit without
// a ValuePattern.
func (e *uiaElem) editable() bool {
	var ct int32
	if failed(comCall(e.el, vtElementCurrentControlType, uintptr(unsafe.Pointer(&ct)))) {
		return false
	}
	if ct != uiaEditControlTypeID && ct != uiaDocumentControlTypeID {
		return false
	}
	vp := patternAs(e.el, uiaValuePatternID, &iidIUIAutomationValuePat)
	if vp == 0 {
		return ct == uiaEditControlTypeID
	}
	defer release(vp)
	var ro int32
	if failed(comCall(vp, vtValueCurrentIsReadOnly, uintptr(unsafe.Pointer(&ro)))) {
		return ct == uiaEditControlTypeID
	}
	return ro == 0
}

// windowFactsOf describes window hwnd with local calls only: its process,
// the class of its top-level window and whether the process runs above our
// integrity level.
func windowFactsOf(hwnd uintptr) windowInfo {
	if r, _, _ := procIsWindow.Call(hwnd); r == 0 {
		return windowInfo{}
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(windows.HWND(hwnd), &pid); err != nil || pid == 0 {
		return windowInfo{}
	}
	const gaRoot = 2
	root, _, _ := procGetAncestor.Call(hwnd, gaRoot)
	if root == 0 {
		root = hwnd
	}
	return windowInfo{Valid: true, PID: int(pid), Class: className(root), Elevated: processElevated(pid)}
}

func className(hwnd uintptr) string {
	buf := make([]uint16, 256)
	n, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf[:n])
}

const processQueryLimitedInformation = 0x1000

// processInfo describes process pid: its executable's path and name.
func processInfo(pid int) plugin.AppInfo {
	a := plugin.AppInfo{PID: pid}
	h, err := windows.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return a
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil {
		a.Path = windows.UTF16ToString(buf[:n])
		a.Name = filepath.Base(a.Path)
	}
	return a
}

// processElevated reports whether process pid runs at a higher integrity
// level than this process, or cannot be inspected.
func processElevated(pid uint32) bool {
	own, err := integrityLevel(windows.GetCurrentProcessToken())
	if err != nil {
		own = 0x2000 // SECURITY_MANDATORY_MEDIUM_RID
	}
	target, err := processIntegrity(pid)
	return elevatedThan(target, own, err)
}

func processIntegrity(pid uint32) (uint32, error) {
	h, err := windows.OpenProcess(processQueryLimitedInformation, false, pid)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(h)
	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		return 0, err
	}
	defer tok.Close()
	return integrityLevel(tok)
}

// integrityLevel returns the mandatory label RID of a token (0x2000
// medium, 0x3000 high, …).
func integrityLevel(tok windows.Token) (uint32, error) {
	buf := make([]byte, 64)
	for {
		var n uint32
		err := windows.GetTokenInformation(tok, windows.TokenIntegrityLevel, &buf[0], uint32(len(buf)), &n)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) || int(n) <= len(buf) {
			return 0, err
		}
		buf = make([]byte, n)
	}
	label := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&buf[0]))
	sid := label.Label.Sid
	if sid == nil || sid.SubAuthorityCount() == 0 {
		return 0, errors.New("winselection: no integrity label")
	}
	return sid.SubAuthority(uint32(sid.SubAuthorityCount() - 1)), nil
}
