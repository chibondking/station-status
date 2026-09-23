//go:build windows

package main

import "syscall"

const (
	enableQuickEditMode = 0x0040
	enableExtendedFlags = 0x0080
)

var procSetConsoleMode = syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleMode")

// disableQuickEdit turns off QuickEdit mode for the console the agent is
// running in. With QuickEdit on (the Windows default), a single click inside
// the console window starts a text selection, and while that selection is
// active Windows blocks every write to the console. Go's log package holds a
// mutex while it writes, so the first goroutine to log -- say, main's report
// loop logging one failed report -- blocks forever, and it takes every other
// goroutine that tries to log down with it. From the outside that looks
// exactly like the station going offline on the status page until someone
// presses a key in the window or restarts the agent.
//
// Best-effort: when stdin isn't a console (a service, redirected input),
// there's no QuickEdit to turn off, and nothing is changed.
func disableQuickEdit() {
	h, err := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE)
	if err != nil {
		return
	}
	var mode uint32
	if err := syscall.GetConsoleMode(h, &mode); err != nil {
		return
	}
	procSetConsoleMode.Call(uintptr(h), uintptr(mode&^enableQuickEditMode|enableExtendedFlags))
}
