//go:build windows

package main

import "syscall"

const windowsDetachedProcess = 0x00000008

func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windowsDetachedProcess}
}
