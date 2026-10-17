//go:build darwin

package shell

import (
	"fmt"
	"strings"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"

	"github.com/eziq1/golauncher/internal/darwin/ns"
)

// The Foundation and AppKit calls of the macOS implementation: property
// lists, Finder's display names, icons and the apps that handle URLs.

// nsRect is NSRect (CGRect) on 64-bit macOS.
type nsRect struct{ X, Y, W, H float64 }

// Typed objc_msgSend variants for the messages whose arguments the
// variadic objc.Send cannot pass (structs, many mixed arguments).
var (
	msgInitBitmap func(obj ns.ID, sel objc.SEL, planes uintptr, width, height, bitsPerSample, samplesPerPixel int,
		alpha, planar bool, colorSpace ns.ID, bytesPerRow, bitsPerPixel int) ns.ID
	msgDrawImage func(obj ns.ID, sel objc.SEL, dst, src nsRect, op uint, fraction float64)
)

var loadMsg = sync.OnceValue(func() error {
	if err := ns.Load(); err != nil {
		return err
	}
	lib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		return err
	}
	addr, err := purego.Dlsym(lib, "objc_msgSend")
	if err != nil {
		return err
	}
	purego.RegisterFunc(&msgInitBitmap, addr)
	purego.RegisterFunc(&msgDrawImage, addr)
	return nil
})

func isKind(obj ns.ID, class string) bool {
	return obj != 0 && ns.SendBool(obj, "isKindOfClass:", ns.Class(class))
}

// dictStrings returns the string values of the NSDictionary d (numbers and
// booleans as their description: "1" for true).
func dictStrings(d ns.ID) map[string]string {
	if !isKind(d, "NSDictionary") {
		return nil
	}
	m := map[string]string{}
	keys := ns.Send(d, "allKeys")
	for i := range ns.SendInt(keys, "count") {
		k := ns.Send(keys, "objectAtIndex:", i)
		if !isKind(k, "NSString") {
			continue
		}
		v := ns.Send(d, "objectForKey:", k)
		switch {
		case isKind(v, "NSString"):
			m[ns.GoString(k)] = ns.GoString(v)
		case isKind(v, "NSNumber"):
			m[ns.GoString(k)] = ns.GoString(ns.Send(v, "description"))
		}
	}
	return m
}

// readDict reads the property list (XML, binary or the old-style text of
// .strings files) at path as an NSDictionary; 0 when it is none.
func readDict(path string) ns.ID {
	return ns.Send(ns.Class("NSDictionary"), "dictionaryWithContentsOfFile:", ns.String(path))
}

// plistStrings returns the string values of the property list at path
// (nil when it cannot be read).
func plistStrings(path string) map[string]string {
	var m map[string]string
	ns.Pool(func() { m = dictStrings(readDict(path)) })
	return m
}

// plistTable returns the dictionaries of strings of the property list at
// path, by key: the localizations of a .loctable.
func plistTable(path string) map[string]map[string]string {
	out := map[string]map[string]string{}
	ns.Pool(func() {
		d := readDict(path)
		if d == 0 {
			return
		}
		keys := ns.Send(d, "allKeys")
		for i := range ns.SendInt(keys, "count") {
			k := ns.Send(keys, "objectAtIndex:", i)
			if m := dictStrings(ns.Send(d, "objectForKey:", k)); len(m) > 0 && isKind(k, "NSString") {
				out[ns.GoString(k)] = m
			}
		}
	})
	return out
}

// displayName returns the name Finder shows for path in the user's language.
func displayName(path string) string {
	var s string
	ns.Pool(func() {
		fm := ns.Send(ns.Class("NSFileManager"), "defaultManager")
		s = ns.GoString(ns.Send(fm, "displayNameAtPath:", ns.String(path)))
	})
	return s
}

func workspace() ns.ID { return ns.Send(ns.Class("NSWorkspace"), "sharedWorkspace") }

// iconPNG draws the icon Finder shows for path (NSWorkspace iconForFile:)
// into an sRGB bitmap of size×size pixels and encodes it as PNG.
func iconPNG(path string, size int) ([]byte, error) {
	if err := loadMsg(); err != nil {
		return nil, err
	}
	var png []byte
	ns.Pool(func() {
		img := ns.Send(workspace(), "iconForFile:", ns.String(path))
		if img == 0 {
			return
		}
		rep := msgInitBitmap(ns.Send(ns.Class("NSBitmapImageRep"), "alloc"),
			ns.Sel("initWithBitmapDataPlanes:pixelsWide:pixelsHigh:bitsPerSample:samplesPerPixel:hasAlpha:isPlanar:colorSpaceName:bytesPerRow:bitsPerPixel:"),
			0, size, size, 8, 4, true, false, ns.String("NSCalibratedRGBColorSpace"), 0, 0)
		if rep == 0 {
			return
		}
		ns.Send(rep, "autorelease")
		rep = ns.Send(rep, "bitmapImageRepByRetaggingWithColorSpace:", ns.Send(ns.Class("NSColorSpace"), "sRGBColorSpace"))
		ctx := ns.Send(ns.Class("NSGraphicsContext"), "graphicsContextWithBitmapImageRep:", rep)
		if ctx == 0 {
			return
		}
		gc := ns.Class("NSGraphicsContext")
		ns.Send(gc, "saveGraphicsState")
		ns.Send(gc, "setCurrentContext:", ctx)
		ns.Send(ctx, "setImageInterpolation:", 3) // NSImageInterpolationHigh
		const sourceOver = 2                      // NSCompositingOperationSourceOver
		dst := nsRect{W: float64(size), H: float64(size)}
		msgDrawImage(img, ns.Sel("drawInRect:fromRect:operation:fraction:"), dst, nsRect{}, sourceOver, 1)
		ns.Send(gc, "restoreGraphicsState")
		const typePNG = 4 // NSBitmapImageFileTypePNG
		png = ns.Bytes(ns.Send(rep, "representationUsingType:properties:", typePNG, ns.Send(ns.Class("NSDictionary"), "dictionary")))
	})
	if len(png) == 0 {
		return nil, fmt.Errorf("shell: no icon for %q", path)
	}
	return png, nil
}

// nsURL returns an autoreleased NSURL of the URL string u, or 0.
func nsURL(u string) ns.ID { return ns.Send(ns.Class("NSURL"), "URLWithString:", ns.String(u)) }

// urlPath returns the file path of the file NSURL u.
func urlPath(u ns.ID) string { return ns.GoString(ns.Send(u, "path")) }

// appForURL returns the bundle of the app that opens links like u, or "".
func appForURL(u string) string {
	var p string
	ns.Pool(func() {
		if url := nsURL(u); url != 0 {
			if app := ns.Send(workspace(), "URLForApplicationToOpenURL:", url); app != 0 {
				p = urlPath(app)
			}
		}
	})
	return p
}

// ProtocolRegistered reports whether an app handles links of the URL scheme.
func ProtocolRegistered(scheme string) bool {
	return scheme != "" && appForURL(scheme+"://x") != ""
}

// Browsers returns the apps that open web links (NSWorkspace
// URLsForApplicationsToOpenURL:, macOS 12 or later), the default first.
func Browsers() []Browser {
	var paths []string
	ns.Pool(func() {
		ws := workspace()
		url := nsURL("https://example.com")
		if url == 0 || !ns.SendBool(ws, "respondsToSelector:", ns.Sel("URLsForApplicationsToOpenURL:")) {
			return
		}
		apps := ns.Send(ws, "URLsForApplicationsToOpenURL:", url)
		for i := range ns.SendInt(apps, "count") {
			paths = append(paths, urlPath(ns.Send(apps, "objectAtIndex:", i)))
		}
	})
	def := appForURL("https://example.com")
	return browsersOf(paths, def, displayName)
}

// browsersOf returns the Browsers of the app bundles paths, the default def
// first, each once, named by name.
func browsersOf(paths []string, def string, name func(string) string) []Browser {
	var out []Browser
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		n := name(p)
		if n == "" {
			return
		}
		out = append(out, Browser{Name: strings.TrimSuffix(n, ".app"), Path: p})
	}
	add(def)
	for _, p := range paths {
		add(p)
	}
	return out
}
