//go:build darwin

package ax

import (
	"errors"
	"fmt"
	"sync"

	"github.com/ebitengine/purego"

	"github.com/eziq1/golauncher/internal/darwin/ns"
)

// Error is an AXError other than success.
type Error int32

func (e Error) Error() string { return fmt.Sprintf("ax: AXError %d", int32(e)) }

// AXError codes.
const (
	errAPIDisabled          Error = -25211 // kAXErrorAPIDisabled: not trusted
	errNoValue              Error = -25212 // kAXErrorNoValue
	errAttributeUnsupported Error = -25205 // kAXErrorAttributeUnsupported
)

// Missing reports whether err means that the attribute has no value or is
// not supported (not a failure of the call).
func Missing(err error) bool {
	var e Error
	return errors.As(err, &e) && (e == errNoValue || e == errAttributeUnsupported)
}

type funcs struct {
	trusted            func() bool
	trustedWithOptions func(options uintptr) bool
	createSystemWide   func() uintptr
	createApplication  func(pid int32) uintptr
	copyAttributeValue func(elem, attr uintptr, value *uintptr) int32
	copyAttributeNames func(elem uintptr, names *uintptr) int32
	isSettable         func(elem, attr uintptr, settable *uint8) int32
	setAttributeValue  func(elem, attr, value uintptr) int32
	setTimeout         func(elem uintptr, seconds float32) int32
	getPid             func(elem uintptr, pid *int32) int32
	elementAtPosition  func(app uintptr, x, y float32, elem *uintptr) int32
	retain             func(ref uintptr) uintptr
	release            func(ref uintptr)
	getTypeID          func(ref uintptr) uint
	stringTypeID       func() uint
}

var load = sync.OnceValues(func() (*funcs, error) {
	if err := ns.Load(); err != nil {
		return nil, err
	}
	lib, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices",
		purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		return nil, fmt.Errorf("ax: %w", err)
	}
	f := &funcs{}
	purego.RegisterLibFunc(&f.trusted, lib, "AXIsProcessTrusted")
	purego.RegisterLibFunc(&f.trustedWithOptions, lib, "AXIsProcessTrustedWithOptions")
	purego.RegisterLibFunc(&f.createSystemWide, lib, "AXUIElementCreateSystemWide")
	purego.RegisterLibFunc(&f.createApplication, lib, "AXUIElementCreateApplication")
	purego.RegisterLibFunc(&f.copyAttributeValue, lib, "AXUIElementCopyAttributeValue")
	purego.RegisterLibFunc(&f.copyAttributeNames, lib, "AXUIElementCopyAttributeNames")
	purego.RegisterLibFunc(&f.isSettable, lib, "AXUIElementIsAttributeSettable")
	purego.RegisterLibFunc(&f.setAttributeValue, lib, "AXUIElementSetAttributeValue")
	purego.RegisterLibFunc(&f.setTimeout, lib, "AXUIElementSetMessagingTimeout")
	purego.RegisterLibFunc(&f.getPid, lib, "AXUIElementGetPid")
	purego.RegisterLibFunc(&f.elementAtPosition, lib, "AXUIElementCopyElementAtPosition")
	purego.RegisterLibFunc(&f.retain, lib, "CFRetain")
	purego.RegisterLibFunc(&f.release, lib, "CFRelease")
	purego.RegisterLibFunc(&f.getTypeID, lib, "CFGetTypeID")
	purego.RegisterLibFunc(&f.stringTypeID, lib, "CFStringGetTypeID")
	return f, nil
})

// Trusted reports whether this process has the Accessibility permission.
func Trusted() bool {
	f, err := load()
	return err == nil && f.trusted()
}

var promptOnce sync.Once

// RequestTrust asks macOS to show its prompt that offers to open the
// Accessibility settings for this program, once per process, and reports
// whether the permission is already granted (then no prompt shows).
func RequestTrust() bool {
	f, err := load()
	if err != nil {
		return false
	}
	if f.trusted() {
		return true
	}
	promptOnce.Do(func() {
		ns.Pool(func() {
			opts := ns.Send(ns.Class("NSDictionary"), "dictionaryWithObject:forKey:",
				ns.Send(ns.Class("NSNumber"), "numberWithBool:", true), ns.String("AXTrustedCheckOptionPrompt"))
			f.trustedWithOptions(uintptr(opts))
		})
	})
	return f.trusted()
}

// Element is an AXUIElement, owned: Release frees it.
type Element struct{ ref uintptr }

// Valid reports whether e refers to an element.
func (e Element) Valid() bool { return e.ref != 0 }

// Release frees e.
func (e Element) Release() {
	if f, err := load(); err == nil && e.ref != 0 {
		f.release(e.ref)
	}
}

// messagingTimeout bounds every request to another app: the default is
// six seconds, and a hung app must not hold up the launcher.
const messagingTimeout = 0.25

// SystemWide returns the system-wide element, from which the focused app
// and element are read. It fails with ErrNotTrusted without the
// permission.
func SystemWide() (Element, error) {
	f, err := load()
	if err != nil {
		return Element{}, err
	}
	if !f.trusted() {
		return Element{}, ErrNotTrusted
	}
	e := Element{f.createSystemWide()}
	f.setTimeout(e.ref, messagingTimeout)
	return e, nil
}

// Application returns the element of the app with process identifier pid.
func Application(pid int) (Element, error) {
	f, err := load()
	if err != nil {
		return Element{}, err
	}
	if !f.trusted() {
		return Element{}, ErrNotTrusted
	}
	e := Element{f.createApplication(int32(pid))}
	if e.ref == 0 {
		return e, fmt.Errorf("ax: no application %d", pid)
	}
	f.setTimeout(e.ref, messagingTimeout)
	return e, nil
}

func check(r int32) error {
	switch Error(r) {
	case 0:
		return nil
	case errAPIDisabled:
		return ErrNotTrusted
	}
	return Error(r)
}

// copyValue copies attribute attr of e (+1 retained) and runs fn with it.
func (e Element) copyValue(attr string, fn func(f *funcs, v uintptr) error) error {
	f, err := load()
	if err != nil {
		return err
	}
	var rerr error
	ns.Pool(func() {
		var v uintptr
		if rerr = check(f.copyAttributeValue(e.ref, uintptr(ns.String(attr)), &v)); rerr != nil {
			return
		}
		if v == 0 {
			rerr = errNoValue
			return
		}
		defer f.release(v)
		rerr = fn(f, v)
	})
	return rerr
}

// Element returns the element that attribute attr of e names, such as
// "AXFocusedUIElement" or "AXFocusedWindow".
func (e Element) Element(attr string) (Element, error) {
	var out Element
	err := e.copyValue(attr, func(f *funcs, v uintptr) error {
		out = Element{f.retain(v)}
		f.setTimeout(out.ref, messagingTimeout)
		return nil
	})
	return out, err
}

// String returns the string attribute attr of e, e.g. "AXRole",
// "AXSelectedText" or "AXTitle".
func (e Element) String(attr string) (string, error) {
	var s string
	err := e.copyValue(attr, func(f *funcs, v uintptr) error {
		if f.getTypeID(v) != f.stringTypeID() {
			return fmt.Errorf("ax: %s is not a string", attr)
		}
		s = ns.GoString(ns.ID(v)) // CFString is toll-free bridged to NSString
		return nil
	})
	return s, err
}

// URL returns the URL attribute attr of e as a string ("AXDocument" is a
// string with a file URL in most apps, an NSURL in some).
func (e Element) URL(attr string) (string, error) {
	var s string
	err := e.copyValue(attr, func(f *funcs, v uintptr) error {
		if f.getTypeID(v) == f.stringTypeID() {
			s = ns.GoString(ns.ID(v))
			return nil
		}
		s = ns.GoString(ns.Send(ns.ID(v), "absoluteString"))
		return nil
	})
	return s, err
}

// Names returns the attribute names of e.
func (e Element) Names() ([]string, error) {
	f, err := load()
	if err != nil {
		return nil, err
	}
	var names []string
	ns.Pool(func() {
		var arr uintptr
		if err = check(f.copyAttributeNames(e.ref, &arr)); err != nil || arr == 0 {
			return
		}
		defer f.release(arr)
		names = ns.Strings(ns.ID(arr))
	})
	return names, err
}

// Settable reports whether attribute attr of e can be set (an editable
// field's "AXValue").
func (e Element) Settable(attr string) bool {
	f, err := load()
	if err != nil {
		return false
	}
	var ok uint8
	var r int32
	ns.Pool(func() { r = f.isSettable(e.ref, uintptr(ns.String(attr)), &ok) })
	return r == 0 && ok != 0
}

// SetBool sets the boolean attribute attr of e.
func (e Element) SetBool(attr string, v bool) error {
	f, err := load()
	if err != nil {
		return err
	}
	ns.Pool(func() {
		num := ns.Send(ns.Class("NSNumber"), "numberWithBool:", v) // toll-free bridged to CFBoolean
		err = check(f.setAttributeValue(e.ref, uintptr(ns.String(attr)), uintptr(num)))
	})
	return err
}

// ElementAt returns the element at the screen point (x, y), in points from
// the top left of the main display (as CGEventGetLocation reports the
// pointer), of whatever app is there when e is the system-wide element
// (AXUIElementCopyElementAtPosition).
func (e Element) ElementAt(x, y float64) (Element, error) {
	f, err := load()
	if err != nil {
		return Element{}, err
	}
	var ref uintptr
	if err := check(f.elementAtPosition(e.ref, float32(x), float32(y), &ref)); err != nil {
		return Element{}, err
	}
	if ref == 0 {
		return Element{}, errNoValue
	}
	out := Element{ref}
	f.setTimeout(out.ref, messagingTimeout)
	return out, nil
}

// PID returns the process identifier of the app e belongs to.
func (e Element) PID() (int, error) {
	f, err := load()
	if err != nil {
		return 0, err
	}
	var pid int32
	if err := check(f.getPid(e.ref, &pid)); err != nil {
		return 0, err
	}
	return int(pid), nil
}
