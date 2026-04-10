package vulnerable

import (
	"crypto/tls"
	"os/exec"
)

func InsecureTLSConfig() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true}
}

func BuildShellCommand(command string) *exec.Cmd {
	return exec.Command("sh", "-c", command)
}
