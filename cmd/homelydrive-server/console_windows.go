//go:build windows

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var hasConsole bool

// attachConsole connects to the console of the parent process (cmd.exe,
// PowerShell) so command line output works although the program is built as
// a GUI application (no console window pops up for the server window).
func attachConsole() {
	k32 := windows.NewLazySystemDLL("kernel32.dll")
	attach := k32.NewProc("AttachConsole")
	r, _, _ := attach.Call(uintptr(^uint32(0))) // ATTACH_PARENT_PROCESS
	if r == 0 {
		return
	}
	hasConsole = true
	for _, h := range []struct {
		std  uint32
		file **os.File
		name string
	}{
		{windows.STD_OUTPUT_HANDLE, &os.Stdout, "CONOUT$"},
		{windows.STD_ERROR_HANDLE, &os.Stderr, "CONOUT$"},
		{windows.STD_INPUT_HANDLE, &os.Stdin, "CONIN$"},
	} {
		access := uint32(windows.GENERIC_READ | windows.GENERIC_WRITE)
		name, _ := windows.UTF16PtrFromString(h.name)
		fh, err := windows.CreateFile(name, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			continue
		}
		windows.SetStdHandle(h.std, fh)
		*h.file = os.NewFile(uintptr(fh), h.name)
	}
}

func showError(msg string) {
	if hasConsole {
		fmt.Fprintln(os.Stderr, "Fehler:", msg)
		return
	}
	text, _ := syscall.UTF16PtrFromString(msg)
	title, _ := syscall.UTF16PtrFromString("Homelydrive Server")
	user32 := windows.NewLazySystemDLL("user32.dll")
	user32.NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}
