//go:build linux

package identity

func DeviceID() string {
	return firstMachineID("/etc/machine-id", "/var/lib/dbus/machine-id")
}
