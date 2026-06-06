// Package tools provides pluggable filesystem operations and shell command execution interfaces
package tools

import (
	"context"
	"io"
	"os"
	"os/exec"
	"runtime"
)

// FileSystem defines the pluggable file system operations used by the tools.
type FileSystem interface {
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, content []byte, perm os.FileMode) error
	Stat(path string) (os.FileInfo, error)
	MkdirAll(path string, perm os.FileMode) error
	ReadDir(path string) ([]os.DirEntry, error)
}

// OSFileSystem is the default local implementation of FileSystem.
type OSFileSystem struct{}

func (OSFileSystem) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (OSFileSystem) WriteFile(path string, content []byte, perm os.FileMode) error {
	return os.WriteFile(path, content, perm)
}

func (OSFileSystem) Stat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

func (OSFileSystem) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (OSFileSystem) ReadDir(path string) ([]os.DirEntry, error) {
	return os.ReadDir(path)
}

// ShellExecutor defines the interface for running shell commands.
type ShellExecutor interface {
	RunCommand(ctx context.Context, cmd string, stdout io.Writer, stderr io.Writer) error
}

// OSShellExecutor is the default local implementation of ShellExecutor.
type OSShellExecutor struct{}

func (OSShellExecutor) RunCommand(ctx context.Context, cmdStr string, stdout io.Writer, stderr io.Writer) error {
	var shellName string
	var shellArgs []string

	if runtime.GOOS == "windows" {
		shellName = "cmd.exe"
		shellArgs = []string{"/c", cmdStr}
	} else {
		shellName = "/bin/sh"
		shellArgs = []string{"-c", cmdStr}
	}

	cmd := exec.Command(shellName, shellArgs...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	setProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		killProcessGroup(cmd)
		<-done
		return ctx.Err()
	case err := <-done:
		return err
	}
}
