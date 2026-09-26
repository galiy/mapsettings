//go:build linux

// map-settings — последовательный порт (Linux, termios).
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"
	"unsafe"
)

type serialPort struct {
	f *os.File
}

func (s *serialPort) Read(p []byte) (int, error)  { return s.f.Read(p) }
func (s *serialPort) Write(p []byte) (int, error) { return s.f.Write(p) }
func (s *serialPort) Close() error                { return s.f.Close() }

func (s *serialPort) SetDeadline(t time.Time) error {
	_ = s.f.SetReadDeadline(t)
	_ = s.f.SetWriteDeadline(t)
	return nil
}

func ioctlTermios(fd uintptr, req uintptr, t *syscall.Termios) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(unsafe.Pointer(t)))
	if errno != 0 {
		return errno
	}
	return nil
}

// baudConst переводит скорость в константу termios (только стандартные).
func baudConst(baud int) (uint32, error) {
	switch baud {
	case 9600:
		return syscall.B9600, nil
	case 19200:
		return syscall.B19200, nil
	case 38400:
		return syscall.B38400, nil
	case 57600:
		return syscall.B57600, nil
	case 115200:
		return syscall.B115200, nil
	}
	return 0, fmt.Errorf("неподдерживаемая скорость %d", baud)
}

// listSerialPorts возвращает доступные последовательные порты Linux.
func listSerialPorts() []string {
	var out []string
	for _, pat := range []string{"/dev/ttyUSB*", "/dev/ttyACM*", "/dev/ttyS*"} {
		m, _ := filepath.Glob(pat)
		out = append(out, m...)
	}
	sort.Strings(out)
	return out
}

// openSerial открывает COM-порт в raw-режиме 8N1.
func openSerial(path string, baud int) (io.ReadWriteCloser, error) {
	if path == "" {
		return nil, fmt.Errorf("не задан COM-порт")
	}
	speed, err := baudConst(baud)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("открытие %s: %w", path, err)
	}
	fd := f.Fd()
	var t syscall.Termios
	if err := ioctlTermios(fd, syscall.TCGETS, &t); err != nil {
		f.Close()
		return nil, fmt.Errorf("TCGETS: %w", err)
	}
	t.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	t.Oflag &^= syscall.OPOST
	t.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	t.Cflag &^= syscall.CSIZE | syscall.PARENB
	t.Cflag |= syscall.CS8 | syscall.CREAD | syscall.CLOCAL
	const cbaudMask = 0x100F // биты скорости в c_cflag (Linux)
	t.Cflag = (t.Cflag &^ cbaudMask) | speed
	t.Cc[syscall.VMIN] = 1
	t.Cc[syscall.VTIME] = 0
	if err := ioctlTermios(fd, syscall.TCSETS, &t); err != nil {
		f.Close()
		return nil, fmt.Errorf("TCSETS: %w", err)
	}
	// Снимаем O_NONBLOCK (таймауты обеспечиваются poll через deadline).
	const fSetFL = 4
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, fSetFL, 0); errno != 0 {
		f.Close()
		return nil, fmt.Errorf("F_SETFL: %v", errno)
	}
	return &serialPort{f: f}, nil
}
