package tts

import (
	"testing"
	"unsafe"
)

// TestVtable checks the ISpVoice vtable indexes against a real SpVoice with
// silent setter/getter pairs and WaitUntilDone on an idle voice (S_OK). A
// wrong index would call a different method and fail these checks. It never
// speaks: Speak itself is not called.
func TestVtable(t *testing.T) {
	const (
		vtSetRate   = 28
		vtSetVolume = 30
	)
	err := withCOM(func() error {
		voice, err := newVoice()
		if err != nil {
			t.Skipf("no SAPI: %v", err)
		}
		defer release(voice)
		if r := comCall(voice, vtSetRate, 3); failed(r) {
			t.Errorf("SetRate = %#x", r)
		}
		rate := int32(-99)
		if r := comCall(voice, vtGetRate, uintptr(unsafe.Pointer(&rate))); failed(r) || rate != 3 {
			t.Errorf("GetRate = %#x, rate %d; want S_OK, 3", r, rate)
		}
		if r := comCall(voice, vtSetVolume, 57); failed(r) {
			t.Errorf("SetVolume = %#x", r)
		}
		var vol uint16
		if r := comCall(voice, vtGetVolume, uintptr(unsafe.Pointer(&vol))); failed(r) || vol != 57 {
			t.Errorf("GetVolume = %#x, volume %d; want S_OK, 57", r, vol)
		}
		if r := comCall(voice, vtWaitUntilDone, 0); r != sOK {
			t.Errorf("WaitUntilDone(0) on an idle voice = %#x, want S_OK", r)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
