//go:build windows

package app

import (
	"syscall"
	"unsafe"
)

var (
	modUser32Clip   = syscall.NewLazyDLL("user32.dll")
	modKernel32Clip = syscall.NewLazyDLL("kernel32.dll")

	procOpenClipboard    = modUser32Clip.NewProc("OpenClipboard")
	procCloseClipboard   = modUser32Clip.NewProc("CloseClipboard")
	procGetClipboardData = modUser32Clip.NewProc("GetClipboardData")
	procGlobalLock       = modKernel32Clip.NewProc("GlobalLock")
	procGlobalUnlock     = modKernel32Clip.NewProc("GlobalUnlock")
	procGlobalSize       = modKernel32Clip.NewProc("GlobalSize")
	procRtlMoveMemory    = modKernel32Clip.NewProc("RtlMoveMemory")
)

const cfUnicodeText = 13

// readClipboardText reads UTF-16 text from the Windows clipboard without spawning any subprocess.
func readClipboardText() string {
	r, _, _ := procOpenClipboard.Call(0)
	if r == 0 {
		return ""
	}
	defer procCloseClipboard.Call()

	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return ""
	}
	sz, _, _ := procGlobalSize.Call(h)
	if sz < 2 {
		return ""
	}
	if sz > 4*1024*1024 {
		sz = 4 * 1024 * 1024
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return ""
	}
	defer procGlobalUnlock.Call(h)

	buf := make([]uint16, sz/2)
	if len(buf) == 0 {
		return ""
	}
	procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&buf[0])), ptr, uintptr(len(buf)*2))
	return syscall.UTF16ToString(buf)
}
