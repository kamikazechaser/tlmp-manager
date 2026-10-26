// Package shim embeds the dstorage.dll shim built from shim/dstorage.c by `make shim`.
package shim

import (
	"crypto/md5"
	_ "embed"
	"encoding/hex"
)

var (
	//go:embed dstorage.dll
	DLL []byte

	MD5 = func() string {
		sum := md5.Sum(DLL)
		return hex.EncodeToString(sum[:])
	}()
)
