//go:build !windows && !linux

package identity

func DeviceID() string { return "" }
