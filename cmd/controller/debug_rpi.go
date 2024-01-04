//go:build debug && linux && arm

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const dmesg = false

var screenshotCounter int

func dbgInit() error {
	s, err := openSerial("/dev/ttyGS0")
	if err != nil {
		return err
	}
	// Redirect stderr and stdout
	unix.Dup2(int(s.Fd()), syscall.Stderr)
	unix.Dup2(int(s.Fd()), syscall.Stdout)
	go func() {
		defer s.Close()
		if err := runSerial(s); err != nil {
			log.Printf("debug: serial communication failed: %v", err)
		}
	}()
	if dmesg {
		kmsg, err := os.Open("/dev/kmsg")
		if err != nil {
			return err
		}
		go func() {
			defer kmsg.Close()
			io.Copy(os.Stderr, kmsg)
		}()
	}
	return nil
}

func runSerial(s io.Reader) error {
	r := bufio.NewReader(s)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return err
		}
		var binSize int64
		line = strings.TrimSpace(line)
		if _, err := fmt.Sscanf(line, "reload %d", &binSize); err == nil {
			binFile := "/reload-a"
			if binFile == os.Args[0] {
				binFile = "/reload-b"
			}
			if err := writeReloader(r, binFile, binSize); err != nil {
				return err
			}
			if err := syscall.Exec(binFile, []string{binFile}, nil); err != nil {
				return fmt.Errorf("%s: %w", binFile, err)
			}
			continue
		}
		switch line {
		case "screenshot":
			if display == nil {
				break
			}
			screenshotCounter++
			name := fmt.Sprintf("screenshot%d.png", screenshotCounter)
			dumpImage(name, display.Framebuffer())
		default:
			debugCommand(line)
		}
	}
}

func writeReloader(s io.Reader, binFile string, size int64) (ferr error) {
	bin, err := os.OpenFile(binFile, os.O_CREATE|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	defer func() {
		if err := bin.Close(); ferr == nil {
			ferr = err
		}
	}()
	_, err = io.CopyN(bin, s, size)
	return err
}

func dumpImage(name string, img image.Image) {
	buf := new(bytes.Buffer)
	if err := png.Encode(buf, img); err != nil {
		log.Printf("screenshot: failed to encode: %v", err)
		return
	}
	if err := dumpFile(name, buf); err != nil {
		log.Printf("screenshot: %s: %v", name, err)
		return
	}
	log.Printf("screenshot: dumped %s", name)
}

func dumpFile(path string, r io.Reader) (ferr error) {
	const mntDir = "/mnt"
	if err := os.MkdirAll(mntDir, 0o644); err != nil {
		return fmt.Errorf("mkdir %s: %w", mntDir, err)
	}
	if err := syscall.Mount("/dev/mmcblk0p1", mntDir, "vfat", 0, ""); err != nil {
		return fmt.Errorf("mount /dev/mmcblk0p1: %w", err)
	}
	defer func() {
		if err := syscall.Unmount(mntDir, 0); ferr == nil {
			ferr = err
		}
	}()
	path = filepath.Join(mntDir, path)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o644); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if err := f.Close(); ferr == nil {
			ferr = err
		}
	}()
	_, err = io.Copy(f, r)
	return err
}

func openSerial(path string) (s *os.File, err error) {
	s, err = os.OpenFile(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0666)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil && s != nil {
			s.Close()
		}
	}()
	c, err := s.SyscallConn()
	if err != nil {
		return nil, err
	}
	var errno syscall.Errno
	err = c.Control(func(fd uintptr) {
		// Base settings
		cflagToUse := uint32(unix.CREAD | unix.CLOCAL | unix.CS8)
		t := unix.Termios{
			Iflag:  unix.IGNPAR,
			Cflag:  cflagToUse,
			Ispeed: 115200,
			Ospeed: 115200,
		}
		t.Cc[unix.VMIN] = 1
		t.Cc[unix.VTIME] = 0

		if _, _, errno := unix.Syscall6(unix.SYS_IOCTL, fd, uintptr(unix.TCSETS), uintptr(unsafe.Pointer(&t)), 0, 0, 0); errno != 0 {
			panic(errno)
		}
	})
	if err != nil {
		return nil, err
	}
	if errno != 0 {
		return nil, errno
	}
	return
}
