//go:build windows

package main

import "os"

func consoleWriter() *os.File {
	f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err != nil {
		return nil
	}
	return f
}
