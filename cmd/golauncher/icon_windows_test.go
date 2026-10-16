package main

import (
	"testing"

	"golang.org/x/sys/windows"
)

// TestIconResource: the program's icon is the icon resource with ID 1, which
// MyGo loads for its windows' title bars and taskbar buttons. Under another
// name (it was "APP") Explorer still shows the icon, but every window gets
// Windows' generic one. The test binary links the same .syso files.
func TestIconResource(t *testing.T) {
	var self windows.Handle
	if err := windows.GetModuleHandleEx(0, nil, &self); err != nil {
		t.Fatal(err)
	}
	icon, _, err := windows.NewLazySystemDLL("user32.dll").NewProc("LoadIconW").Call(uintptr(self), 1)
	if icon == 0 {
		t.Fatalf("no icon resource 1: %v (cmd/golauncher/winres/winres.json must name the icon group #1)", err)
	}
}
