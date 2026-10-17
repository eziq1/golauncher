//go:build darwin

package ns

import (
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// ID is an Objective-C object.
type ID = objc.ID

var load = sync.OnceValue(func() error {
	for _, f := range []string{
		"/System/Library/Frameworks/Foundation.framework/Foundation",
		"/System/Library/Frameworks/AppKit.framework/AppKit",
	} {
		if _, err := purego.Dlopen(f, purego.RTLD_GLOBAL|purego.RTLD_NOW); err != nil {
			return err
		}
	}
	return nil
})

// Load loads Foundation and AppKit once; the other helpers call it.
func Load() error { return load() }

var (
	selMu sync.Mutex
	sels  = map[string]objc.SEL{}
)

// Sel returns the selector name, cached (RegisterName takes a global lock).
func Sel(name string) objc.SEL {
	selMu.Lock()
	defer selMu.Unlock()
	s, ok := sels[name]
	if !ok {
		s = objc.RegisterName(name)
		sels[name] = s
	}
	return s
}

// Class returns the class name as an object to send class messages to, or 0
// when it does not exist.
func Class(name string) ID {
	if Load() != nil {
		return 0
	}
	return ID(objc.GetClass(name))
}

// Send sends the message sel to obj; a message to 0 returns 0, as in
// Objective-C.
func Send(obj ID, sel string, args ...any) ID {
	if obj == 0 {
		return 0
	}
	return obj.Send(Sel(sel), args...)
}

// SendBool sends sel to obj and returns its BOOL result.
func SendBool(obj ID, sel string, args ...any) bool {
	if obj == 0 {
		return false
	}
	return objc.Send[bool](obj, Sel(sel), args...)
}

// SendInt sends sel to obj and returns its NSInteger (or NSUInteger) result.
func SendInt(obj ID, sel string, args ...any) int {
	if obj == 0 {
		return 0
	}
	return objc.Send[int](obj, Sel(sel), args...)
}

// Pool runs fn in an autorelease pool on a locked OS thread.
func Pool(fn func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if Load() != nil {
		fn()
		return
	}
	pool := Send(Send(Class("NSAutoreleasePool"), "alloc"), "init")
	defer Send(pool, "drain")
	fn()
}

// String returns s as an autoreleased NSString.
func String(s string) ID {
	b := []byte(s)
	if len(b) == 0 {
		return Send(Class("NSString"), "string")
	}
	const utf8Encoding = 4 // NSUTF8StringEncoding
	str := Send(Send(Class("NSString"), "alloc"), "initWithBytes:length:encoding:",
		unsafe.Pointer(&b[0]), len(b), utf8Encoding)
	runtime.KeepAlive(b)
	return Send(str, "autorelease")
}

// GoString returns the NSString str as a Go string ("" for 0).
func GoString(str ID) string {
	if str == 0 {
		return ""
	}
	p := objc.Send[*byte](str, Sel("UTF8String"))
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
		n++
	}
	return string(unsafe.Slice(p, n))
}

// Bytes copies the content of the NSData d (nil for 0).
func Bytes(d ID) []byte {
	if d == 0 {
		return nil
	}
	n := SendInt(d, "length")
	if n == 0 {
		return []byte{}
	}
	p := objc.Send[unsafe.Pointer](d, Sel("bytes"))
	return append([]byte(nil), unsafe.Slice((*byte)(p), n)...)
}

// Data returns a copy of b as an autoreleased NSData.
func Data(b []byte) ID {
	if len(b) == 0 {
		return Send(Class("NSData"), "data")
	}
	d := Send(Class("NSData"), "dataWithBytes:length:", unsafe.Pointer(&b[0]), len(b))
	runtime.KeepAlive(b)
	return d
}

// Strings returns the NSStrings of the NSArray a.
func Strings(a ID) []string {
	n := SendInt(a, "count")
	s := make([]string, 0, n)
	for i := range n {
		s = append(s, GoString(Send(a, "objectAtIndex:", i)))
	}
	return s
}

// Array returns an autoreleased NSArray of the objects.
func Array(objs ...ID) ID {
	a := Send(Class("NSMutableArray"), "arrayWithCapacity:", len(objs))
	for _, o := range objs {
		Send(a, "addObject:", o)
	}
	return a
}

// StringArray returns an autoreleased NSArray of NSStrings.
func StringArray(s ...string) ID {
	objs := make([]ID, len(s))
	for i, v := range s {
		objs[i] = String(v)
	}
	return Array(objs...)
}
