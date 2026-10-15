package cred

import (
	"errors"
	"fmt"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	advapi32       = windows.NewLazySystemDLL("advapi32.dll")
	procCredWriteW = advapi32.NewProc("CredWriteW")
	procCredReadW  = advapi32.NewProc("CredReadW")
	procCredDelete = advapi32.NewProc("CredDeleteW")
	procCredFree   = advapi32.NewProc("CredFree")
)

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
)

// credential is CREDENTIALW.
type credential struct {
	flags              uint32
	typ                uint32
	targetName         *uint16
	comment            *uint16
	lastWritten        windows.Filetime
	credentialBlobSize uint32
	credentialBlob     *byte
	persist            uint32
	attributeCount     uint32
	attributes         uintptr
	targetAlias        *uint16
	userName           *uint16
}

// native converts an address returned by Windows to a pointer without
// tripping vet's unsafeptr check; the memory is not managed by Go.
func native(addr uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&addr)) }

// Get returns the secret stored under target; ok is false when there is none.
func Get(target string) (secret string, ok bool, err error) {
	t, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return "", false, err
	}
	var p uintptr
	if r, _, e := procCredReadW.Call(uintptr(unsafe.Pointer(t)), credTypeGeneric, 0, uintptr(unsafe.Pointer(&p))); r == 0 {
		if errors.Is(e, windows.ERROR_NOT_FOUND) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("cred: reading %s: %w", target, e)
	}
	defer procCredFree.Call(p)
	c := (*credential)(native(p))
	if c.credentialBlobSize == 0 || c.credentialBlob == nil {
		return "", true, nil
	}
	blob := unsafe.Slice(c.credentialBlob, c.credentialBlobSize)
	u := make([]uint16, len(blob)/2)
	for i := range u {
		u[i] = uint16(blob[2*i]) | uint16(blob[2*i+1])<<8
	}
	return string(utf16.Decode(u)), true, nil
}

// Set stores secret under target, replacing what was there.
func Set(target, secret string) error {
	t, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	u := utf16.Encode([]rune(secret))
	if len(u) > MaxSecretLen {
		return fmt.Errorf("cred: the secret for %s is too long (%d > %d UTF-16 code units)", target, len(u), MaxSecretLen)
	}
	blob := make([]byte, 2*len(u))
	for i, v := range u {
		blob[2*i], blob[2*i+1] = byte(v), byte(v>>8)
	}
	user, _ := windows.UTF16PtrFromString("golauncher")
	c := credential{
		typ:                credTypeGeneric,
		targetName:         t,
		credentialBlobSize: uint32(len(blob)),
		persist:            credPersistLocalMachine,
		userName:           user,
	}
	if len(blob) > 0 {
		c.credentialBlob = &blob[0]
	}
	if r, _, e := procCredWriteW.Call(uintptr(unsafe.Pointer(&c)), 0); r == 0 {
		return fmt.Errorf("cred: writing %s: %w", target, e)
	}
	clear(blob)
	return nil
}

// Delete removes the secret stored under target. Deleting a missing secret
// is not an error.
func Delete(target string) error {
	t, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	if r, _, e := procCredDelete.Call(uintptr(unsafe.Pointer(t)), credTypeGeneric, 0); r == 0 {
		if errors.Is(e, windows.ERROR_NOT_FOUND) {
			return nil
		}
		return fmt.Errorf("cred: deleting %s: %w", target, e)
	}
	return nil
}
