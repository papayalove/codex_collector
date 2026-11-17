//go:build windows

package identity

import (
	"os/exec"
	"strings"
)

func DeviceID() string {
	out, err := exec.Command("reg", "query", `HKLM\SOFTWARE\Microsoft\Cryptography`, "/v", "MachineGuid").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "MachineGuid") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				return fields[len(fields)-1]
			}
		}
	}
	return ""
}
