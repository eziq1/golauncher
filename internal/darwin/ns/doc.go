// Package ns is a minimal helper over the Objective-C runtime for the
// macOS implementations of internal/winapi/*: it loads Foundation and
// AppKit with purego and converts strings, data and arrays between Go and
// Foundation. There is no cgo; everything builds with CGO_ENABLED=0 and
// cross-compiles (docs/macos-port.md §5.2). It is empty on other platforms.
//
// Objects returned by the helpers are autoreleased: call them inside Pool,
// which also pins the goroutine to its OS thread for the pool's lifetime.
package ns
