//go:build !linux

// map-settings — COM-порт не поддерживается на этой ОС.
package main

import (
	"fmt"
	"io"
	"runtime"
)

func openSerial(path string, baud int) (io.ReadWriteCloser, error) {
	return nil, fmt.Errorf("COM-порт не поддерживается на %s", runtime.GOOS)
}

func listSerialPorts() []string { return nil }
