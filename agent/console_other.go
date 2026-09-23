//go:build !windows

package main

// disableQuickEdit is a no-op outside Windows -- see console_windows.go.
func disableQuickEdit() {}
