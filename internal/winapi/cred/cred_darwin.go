package cred

import (
	"fmt"
	"sync"
	"unicode/utf16"
	"unsafe"

	"github.com/ebitengine/purego"

	"github.com/eziq1/golauncher/internal/darwin/ns"
)

// On macOS a secret is a generic password of the login keychain: its
// service is the target (as shown by Keychain Access) and its account is
// "golauncher", as the user name of the Windows credential. The secret is
// stored as UTF-8. Reading it from a build with another code signature
// makes macOS ask the user for permission first.

const account = "golauncher"

// OSStatus codes of the Security framework.
const (
	errSecSuccess       = 0
	errSecItemNotFound  = -25300
	errSecDuplicateItem = -25299
)

// security holds the functions and constants of Security.framework.
type security struct {
	copyMatching func(query, result uintptr) int32
	add          func(attrs, result uintptr) int32
	update       func(query, attrs uintptr) int32
	delete       func(query uintptr) int32
	errorMessage func(status int32, reserved uintptr) uintptr

	// CFStringRef constants, keys and values of the queries.
	class, classGenericPassword, attrService, attrAccount, valueData,
	returnData, matchLimit, matchLimitOne uintptr
}

var loadSecurity = sync.OnceValues(func() (*security, error) {
	if err := ns.Load(); err != nil {
		return nil, err
	}
	lib, err := purego.Dlopen("/System/Library/Frameworks/Security.framework/Security", purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		return nil, fmt.Errorf("cred: loading Security.framework: %w", err)
	}
	s := &security{}
	purego.RegisterLibFunc(&s.copyMatching, lib, "SecItemCopyMatching")
	purego.RegisterLibFunc(&s.add, lib, "SecItemAdd")
	purego.RegisterLibFunc(&s.update, lib, "SecItemUpdate")
	purego.RegisterLibFunc(&s.delete, lib, "SecItemDelete")
	purego.RegisterLibFunc(&s.errorMessage, lib, "SecCopyErrorMessageString")
	for name, p := range map[string]*uintptr{
		"kSecClass": &s.class, "kSecClassGenericPassword": &s.classGenericPassword,
		"kSecAttrService": &s.attrService, "kSecAttrAccount": &s.attrAccount,
		"kSecValueData": &s.valueData, "kSecReturnData": &s.returnData,
		"kSecMatchLimit": &s.matchLimit, "kSecMatchLimitOne": &s.matchLimitOne,
	} {
		addr, err := purego.Dlsym(lib, name)
		if err != nil {
			return nil, fmt.Errorf("cred: %w", err)
		}
		*p = **(**uintptr)(unsafe.Pointer(&addr)) // the variable holds the CFStringRef
	}
	return s, nil
})

// query returns an autoreleased dictionary that selects the generic
// password of target, with extra key-value pairs.
func (s *security) query(target string, extra ...uintptr) ns.ID {
	d := ns.Send(ns.Class("NSMutableDictionary"), "dictionary")
	set := func(k, v uintptr) { ns.Send(d, "setObject:forKey:", ns.ID(v), ns.ID(k)) }
	set(s.class, s.classGenericPassword)
	set(s.attrService, uintptr(ns.String(target)))
	set(s.attrAccount, uintptr(ns.String(account)))
	for i := 0; i+1 < len(extra); i += 2 {
		set(extra[i], extra[i+1])
	}
	return d
}

// statusErr describes a failed Security call.
func (s *security) statusErr(what, target string, status int32) error {
	msg := fmt.Sprintf("OSStatus %d", status)
	if str := s.errorMessage(status, 0); str != 0 {
		msg = ns.GoString(ns.ID(str)) + " (" + msg + ")"
		ns.Send(ns.ID(str), "release") // a Copy function: we own it
	}
	return fmt.Errorf("cred: %s %s: %s", what, target, msg)
}

// Get returns the secret stored under target; ok is false when there is none.
func Get(target string) (secret string, ok bool, err error) {
	s, err := loadSecurity()
	if err != nil {
		return "", false, err
	}
	ns.Pool(func() {
		yes := ns.Send(ns.Class("NSNumber"), "numberWithBool:", true)
		q := s.query(target, s.returnData, uintptr(yes), s.matchLimit, s.matchLimitOne)
		var data uintptr
		switch st := s.copyMatching(uintptr(q), uintptr(unsafe.Pointer(&data))); st {
		case errSecSuccess:
			b := ns.Bytes(ns.ID(data))
			ns.Send(ns.ID(data), "release") // returned retained
			secret, ok = string(b), true
			clear(b)
		case errSecItemNotFound:
		default:
			err = s.statusErr("reading", target, st)
		}
	})
	return secret, ok, err
}

// Set stores secret under target, replacing what was there.
func Set(target, secret string) error {
	if n := len(utf16.Encode([]rune(secret))); n > MaxSecretLen {
		// The keychain has no such limit; the same one keeps settings
		// portable between Windows and macOS.
		return fmt.Errorf("cred: the secret for %s is too long (%d > %d UTF-16 code units)", target, n, MaxSecretLen)
	}
	s, err := loadSecurity()
	if err != nil {
		return err
	}
	b := []byte(secret)
	defer clear(b)
	ns.Pool(func() {
		data := uintptr(ns.Data(b))
		st := s.add(uintptr(s.query(target, s.valueData, data)), 0)
		if st == errSecDuplicateItem {
			attrs := ns.Send(ns.Class("NSDictionary"), "dictionaryWithObject:forKey:", ns.ID(data), ns.ID(s.valueData))
			st = s.update(uintptr(s.query(target)), uintptr(attrs))
		}
		if st != errSecSuccess {
			err = s.statusErr("writing", target, st)
		}
	})
	return err
}

// Delete removes the secret stored under target. Deleting a missing secret
// is not an error.
func Delete(target string) error {
	s, err := loadSecurity()
	if err != nil {
		return err
	}
	ns.Pool(func() {
		if st := s.delete(uintptr(s.query(target))); st != errSecSuccess && st != errSecItemNotFound {
			err = s.statusErr("deleting", target, st)
		}
	})
	return err
}
