//go:build !tinygo

package mjolnir

import (
	"errors"
	"io"
	"runtime"

	"github.com/tarm/serial"
)

func Open(dev string) (io.ReadWriteCloser, error) {
	// Hardware parameters.
	const (
		baudRate         = 115200
		stopBits         = 1
		parity           = false
		wordLen          = 8
		controlHandshake = 0
		flowReplace      = 0
		xonLimit         = 2048
		xoffLimit        = 512
	)

	var devices []string
	if dev != "" {
		devices = append(devices, dev)
	} else {
		switch runtime.GOOS {
		case "windows":
			devices = append(devices, "COM3")
		case "linux":
			devices = append(devices, "/dev/ttyUSB0", "/dev/ttyUSB1")
		}
	}
	if len(devices) == 0 {
		return nil, errors.New("no device specified")
	}
	var firstErr error
	for _, dev := range devices {
		c := &serial.Config{Name: dev, Baud: baudRate}
		s, err := serial.OpenPort(c)
		if err == nil {
			return s, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}
