//go:build windows

package app

import (
	"encoding/base64"
	"errors"
	"strings"
	"syscall"
	"unsafe"
)

const (
	secretPrefix           = "dpapi:"
	cryptProtectUIForbidden = 0x01
)

var (
	crypt32DLL             = syscall.NewLazyDLL("crypt32.dll")
	secKernel32DLL         = syscall.NewLazyDLL("kernel32.dll")
	procCryptProtectData   = crypt32DLL.NewProc("CryptProtectData")
	procCryptUnprotectData = crypt32DLL.NewProc("CryptUnprotectData")
	procLocalFree          = secKernel32DLL.NewProc("LocalFree")
	procSecRtlMoveMemory   = secKernel32DLL.NewProc("RtlMoveMemory")
	secretEntropy          = []byte("Kiralrc-AI-Secret-v1")
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func makeBlob(b []byte) dataBlob {
	if len(b) == 0 {
		return dataBlob{}
	}
	return dataBlob{
		cbData: uint32(len(b)),
		pbData: &b[0],
	}
}

func copyBlobBytes(b dataBlob) []byte {
	if b.cbData == 0 || b.pbData == nil {
		return nil
	}
	out := make([]byte, b.cbData)
	_, _, _ = procSecRtlMoveMemory.Call(
		uintptr(unsafe.Pointer(&out[0])),
		uintptr(unsafe.Pointer(b.pbData)),
		uintptr(b.cbData),
	)
	return out
}

// protectSecret 使用 Windows DPAPI（绑定当前系统用户凭据 + 应用专属熵）加密敏感凭据。
func protectSecret(plain string) (string, error) {
	plain = strings.TrimSpace(plain)
	if plain == "" {
		return "", nil
	}
	inBytes := []byte(plain)
	inBlob := makeBlob(inBytes)
	entBlob := makeBlob(secretEntropy)
	var outBlob dataBlob

	r1, _, err := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		0,
		uintptr(unsafe.Pointer(&entBlob)),
		0,
		0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&outBlob)),
	)
	if r1 == 0 {
		return "", err
	}
	defer func() {
		if outBlob.pbData != nil {
			_, _, _ = procLocalFree.Call(uintptr(unsafe.Pointer(outBlob.pbData)))
		}
	}()
	cipherBytes := copyBlobBytes(outBlob)
	if len(cipherBytes) == 0 {
		return "", errors.New("dpapi returned empty cipher")
	}
	return secretPrefix + base64.StdEncoding.EncodeToString(cipherBytes), nil
}

// unprotectSecret 解密由 protectSecret 加密的凭据；若遇到历史明文则返回明文并标记 needsMigrate=true。
func unprotectSecret(stored string) (plain string, needsMigrate bool, err error) {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return "", false, nil
	}
	if !strings.HasPrefix(stored, secretPrefix) {
		return stored, true, nil
	}
	cipherBytes, decErr := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, secretPrefix))
	if decErr != nil || len(cipherBytes) == 0 {
		return "", false, errors.New("invalid encrypted secret format")
	}
	inBlob := makeBlob(cipherBytes)
	entBlob := makeBlob(secretEntropy)
	var outBlob dataBlob

	r1, _, callErr := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		0,
		uintptr(unsafe.Pointer(&entBlob)),
		0,
		0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&outBlob)),
	)
	if r1 == 0 {
		return "", false, callErr
	}
	defer func() {
		if outBlob.pbData != nil {
			_, _, _ = procLocalFree.Call(uintptr(unsafe.Pointer(outBlob.pbData)))
		}
	}()
	plainBytes := copyBlobBytes(outBlob)
	return string(plainBytes), false, nil
}
