package tts

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The COM helpers follow internal/winapi/install.

var (
	ole32                = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
)

var (
	clsidSpVoice = mustGUID("{96749377-3391-11D2-9EE3-00C04F797396}")
	iidISpVoice  = mustGUID("{6C44DF74-72B9-4992-A1EC-EF996E0422D4}")
)

func mustGUID(s string) windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		panic(err)
	}
	return g
}

// Vtable indexes of ISpVoice (sapi.h): IUnknown 0-2, ISpNotifySource 3-9
// (SetNotifySink … GetNotifyEventHandle), ISpEventSource 10-12 (SetInterest,
// GetEvents, GetInfo), then ISpVoice's own methods from SetOutput at 13.
const (
	vtRelease       = 2
	vtSpeak         = 20
	vtGetRate       = 29
	vtGetVolume     = 31
	vtWaitUntilDone = 32
)

// Speak flags (SPEAKFLAGS).
const (
	spfAsync             = 0x1
	spfPurgeBeforeSpeak  = 0x2
	spfIsXML             = 0x8
	spfIsNotXML          = 0x10
	sOK                  = 0
	regdbEClassNotReg    = 0x80040154
	pollMillis           = 50
	coinitMultithreaded  = 0x0
	coinitDisableOLE1DDE = 0x4
	clsctxInprocServer   = 0x1
)

// comCall calls method i of the COM interface obj.
//
//go:uintptrescapes
func comCall(obj uintptr, i int, args ...uintptr) uintptr {
	vtbl := *(*uintptr)(native(obj))
	fn := *(*uintptr)(unsafe.Add(native(vtbl), i*int(unsafe.Sizeof(uintptr(0)))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

// native converts an address returned by Windows to a pointer without
// tripping vet's unsafeptr check; the memory is not managed by Go.
func native(addr uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&addr)) }

func release(obj uintptr) {
	if obj != 0 {
		comCall(obj, vtRelease)
	}
}

func failed(hr uintptr) bool { return int32(uint32(hr)) < 0 }

func hresultError(what string, hr uintptr) error {
	return fmt.Errorf("tts: %s failed (HRESULT %#08x)", what, uint32(hr))
}

// withCOM runs fn on a locked OS thread with COM initialized. SAPI's async
// speech runs on its own thread, so the MTA needs no message pump.
func withCOM(fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := procCoInitializeEx.Call(0, coinitMultithreaded|coinitDisableOLE1DDE)
	if !failed(hr) {
		defer procCoUninitialize.Call()
	}
	return fn()
}

// newVoice creates an SpVoice; COM must be initialized. The caller releases it.
func newVoice() (uintptr, error) {
	var voice uintptr
	r, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidSpVoice)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidISpVoice)), uintptr(unsafe.Pointer(&voice)))
	if uint32(r) == regdbEClassNotReg {
		return 0, ErrUnsupported
	}
	if failed(r) || voice == 0 {
		return 0, hresultError("CoCreateInstance(SpVoice)", r)
	}
	return voice, nil
}

type sapi struct{}

// New returns the system's speaker: SAPI on Windows, the `say` command on macOS; elsewhere one returning ErrUnsupported.
func New() Speaker { return sapi{} }

func (sapi) Speak(ctx context.Context, text, lang string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	s, isXML := sapiText(text, lang)
	p, err := windows.UTF16PtrFromString(s) // cannot fail: sapiText drops NULs
	if err != nil {
		return fmt.Errorf("tts: %w", err)
	}
	flags := uintptr(spfAsync | spfIsNotXML)
	if isXML {
		flags = spfAsync | spfIsXML
	}
	return withCOM(func() error {
		voice, err := newVoice()
		if err != nil {
			return err
		}
		defer release(voice)
		if r := comCall(voice, vtSpeak, uintptr(unsafe.Pointer(p)), flags, 0); failed(r) {
			return hresultError("ISpVoice.Speak", r)
		}
		// p must stay alive until the speech ends: SAPI may read it asynchronously.
		defer runtime.KeepAlive(p)
		for {
			r := comCall(voice, vtWaitUntilDone, pollMillis)
			if failed(r) {
				return hresultError("ISpVoice.WaitUntilDone", r)
			}
			if r == sOK {
				return nil
			}
			if ctx.Err() != nil {
				// Purging with no new text stops the current speech at once.
				comCall(voice, vtSpeak, 0, spfPurgeBeforeSpeak, 0)
				return ctx.Err()
			}
		}
	})
}
