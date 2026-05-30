//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly || windows)

package main

import "syscall"

func detachedSysProcAttr() *syscall.SysProcAttr {
	return nil
}
