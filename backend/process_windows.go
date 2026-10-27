//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
func terminateProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	// Snapshot descendants before terminating their parent: once the parent
	// exits Windows can no longer resolve this tree with taskkill /T.
	children := make(map[uint32][]uint32)
	snapshot, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err == nil {
		defer syscall.CloseHandle(snapshot)
		entry := syscall.ProcessEntry32{Size: uint32(unsafe.Sizeof(syscall.ProcessEntry32{}))}
		for err = syscall.Process32First(snapshot, &entry); err == nil; err = syscall.Process32Next(snapshot, &entry) {
			children[entry.ParentProcessID] = append(children[entry.ParentProcessID], entry.ProcessID)
		}
	}
	root := uint32(cmd.Process.Pid)
	pids := []uint32{root}
	seen := map[uint32]bool{root: true}
	for index := 0; index < len(pids); index++ {
		for _, pid := range children[pids[index]] {
			if !seen[pid] {
				seen[pid] = true
				pids = append(pids, pid)
			}
		}
	}
	rootError := cmd.Process.Kill()
	for _, pid := range pids[1:] {
		if handle, openError := syscall.OpenProcess(syscall.PROCESS_TERMINATE, false, pid); openError == nil {
			_ = syscall.TerminateProcess(handle, 1)
			syscall.CloseHandle(handle)
		}
	}
	if rootError == os.ErrProcessDone {
		return nil
	}
	return rootError
}
