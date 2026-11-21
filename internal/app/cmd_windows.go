//go:build windows

package app

import (
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

var (
	modKernel32          = syscall.NewLazyDLL("kernel32.dll")
	modUser32            = syscall.NewLazyDLL("user32.dll")
	procGetModuleHandleW = modKernel32.NewProc("GetModuleHandleW")
	procLoadImageW       = modUser32.NewProc("LoadImageW")
	procFindWindowW      = modUser32.NewProc("FindWindowW")
	procSendMessageW     = modUser32.NewProc("SendMessageW")
)

func newHiddenCmd(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}

// applyWindowIcon loads the embedded RT_GROUP_ICON (ID 1) from kiralrc-gui.exe
// and applies it to the running top-level window so both Taskbar and Alt-Tab show it.
func applyWindowIcon(title string) {
	go func() {
		const (
			imageIcon   = 1
			lrDefaultSz = 0x00000040
			lrShared    = 0x00008000
			wmSetIcon   = 0x0080
			iconSmall   = 0
			iconBig     = 1
		)
		hInst, _, _ := procGetModuleHandleW.Call(0)
		if hInst == 0 {
			return
		}
		hBig, _, _ := procLoadImageW.Call(hInst, 1, imageIcon, 48, 48, lrShared)
		hSmall, _, _ := procLoadImageW.Call(hInst, 1, imageIcon, 16, 16, lrShared)
		if hBig == 0 {
			hBig, _, _ = procLoadImageW.Call(hInst, 1, imageIcon, 0, 0, lrDefaultSz|lrShared)
		}
		if hSmall == 0 {
			hSmall = hBig
		}
		if hBig == 0 {
			return
		}
		titlePtr, err := syscall.UTF16PtrFromString(title)
		if err != nil {
			return
		}
		for range 20 {
			hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(titlePtr)))
			if hwnd != 0 {
				procSendMessageW.Call(hwnd, wmSetIcon, iconBig, hBig)
				procSendMessageW.Call(hwnd, wmSetIcon, iconSmall, hSmall)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
}
