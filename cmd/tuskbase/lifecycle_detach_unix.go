//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package main

import "syscall"

func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
