//go:build windows

package shell

import (
	"cmp"
	"slices"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// startMenuInternet is where browsers register themselves for the "Default
// apps" settings (per user and per machine).
const startMenuInternet = `SOFTWARE\Clients\StartMenuInternet`

// Browsers returns the web browsers registered under StartMenuInternet in
// HKEY_CURRENT_USER and HKEY_LOCAL_MACHINE, sorted by name, without
// duplicates. It reads a few registry keys and does not start anything.
func Browsers() []Browser {
	var out []Browser
	seen := map[string]bool{}
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		k, err := registry.OpenKey(root, startMenuInternet, registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			continue
		}
		names, _ := k.ReadSubKeyNames(-1)
		k.Close()
		for _, n := range names {
			b, ok := readBrowser(root, startMenuInternet+`\`+n)
			if !ok || seen[strings.ToLower(b.Path)] {
				continue
			}
			seen[strings.ToLower(b.Path)] = true
			out = append(out, b)
		}
	}
	slices.SortFunc(out, func(a, b Browser) int { return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out
}

func readBrowser(root registry.Key, path string) (Browser, bool) {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return Browser{}, false
	}
	name, _, _ := k.GetStringValue("")
	k.Close()
	c, err := registry.OpenKey(root, path+`\shell\open\command`, registry.QUERY_VALUE)
	if err != nil {
		return Browser{}, false
	}
	cmd, _, err := c.GetStringValue("")
	c.Close()
	if err != nil {
		return Browser{}, false
	}
	prog := commandProgram(cmd)
	if prog == "" {
		return Browser{}, false
	}
	if name == "" {
		name = path[strings.LastIndex(path, `\`)+1:]
	}
	return Browser{Name: name, Path: prog}, true
}
