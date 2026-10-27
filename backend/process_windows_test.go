//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestTimeoutTerminatesDescendant(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	command := helper(t, "spawn")
	command.Args = append(command.Args, pidFile)
	_, err := runCommand(command, nil, 1000, CodecContext{})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout, got %v", err)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if process, findErr := os.FindProcess(pid); findErr == nil {
		defer process.Kill()
	}
	handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return
	} // A reaped descendant no longer has a process handle.
	defer syscall.CloseHandle(handle)
	status, err := syscall.WaitForSingleObject(handle, 2000)
	if err != nil || status != syscall.WAIT_OBJECT_0 {
		t.Fatalf("descendant survived timeout: status=%d, error=%v", status, err)
	}
}
