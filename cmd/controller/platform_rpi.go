//go:build linux && arm

package main

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
	"seedhammer.com/driver/drm"
	"seedhammer.com/driver/libcamera"
	"seedhammer.com/driver/wshat"
	"seedhammer.com/gui"
	"seedhammer.com/mjolnir"
	"seedhammer.com/zbar"
)

// Debug hooks.
var (
	engraverHook func() io.ReadWriteCloser
	initHook     func(p *Platform) error
)

type Platform struct {
	sdcard  chan bool
	display *drm.LCD
	inputCh chan<- gui.Event
}

func Init() (*Platform, error) {
	// Ignore errors from setting up filesystems; they may already have been.
	_ = mountFS()
	p := &Platform{
		sdcard: make(chan bool, 1),
	}
	if initHook != nil {
		if err := initHook(p); err != nil {
			log.Printf("debug: %v", err)
		}
	}
	if err := p.initSDCardNotifier(); err != nil {
		return nil, err
	}
	return p, nil
}

// SDCard returns a channel that is notified whenever
// an microSD card is inserted or removed.
func (p *Platform) SDCard() <-chan bool {
	return p.sdcard
}

func (p *Platform) Engraver() (io.ReadWriteCloser, error) {
	if engraverHook != nil {
		return engraverHook(), nil
	}
	return mjolnir.Open("")
}

func (p *Platform) Input(ch chan<- gui.Event) error {
	p.inputCh = ch
	return wshat.Open(ch)
}

func (p *Platform) ScanQR(img *image.Gray) ([][]byte, error) {
	return zbar.Scan(img)
}

func (p *Platform) Display() (gui.LCD, error) {
	d, err := drm.Open()
	if err != nil {
		return nil, err
	}
	p.display = d
	return d, nil
}

func (p *Platform) Camera(dims image.Point, frames chan gui.Frame, out <-chan gui.Frame) func() {
	return libcamera.Open(dims, frames, out)
}

func (p *Platform) initSDCardNotifier() error {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return fmt.Errorf("inotify_init1: %w", err)
	}
	f := os.NewFile(uintptr(fd), "inotify")
	var flags uint32 = unix.IN_CREATE | unix.IN_DELETE
	const dev = "/dev"
	if _, err = unix.InotifyAddWatch(fd, dev, flags); err != nil {
		f.Close()
		return fmt.Errorf("inotify_add_watch: %w", err)
	}
	const sdcName = "mmcblk0"
	inserted := true
	if _, err := os.Stat(filepath.Join(dev, sdcName)); os.IsNotExist(err) {
		inserted = false
	}
	p.sdcard <- inserted
	go func() {
		defer f.Close()
		// Make room for 100 events plus paths and their NUL terminator.
		var buf [(unix.SizeofInotifyEvent + unix.PathMax + 1) * 100]byte
		for {
			n, err := f.Read(buf[:])
			if err != nil {
				panic(err)
			}
			evts := buf[:n]
			for len(evts) > 0 {
				evt := (*unix.InotifyEvent)(unsafe.Pointer(&evts[0]))
				evts = evts[unix.SizeofInotifyEvent:]
				var name string
				if evt.Len > 0 {
					// Extract name, without NUL terminator.
					nameb := evts[:evt.Len-1]
					evts = evts[evt.Len:]
					// Kernel pads name with NULs. Trim them.
					nameb = bytes.TrimRight(nameb, "\000")
					name = string(nameb)
				}
				if name == sdcName {
					// Empty sdcard channel.
					select {
					case <-p.sdcard:
					default:
					}
					switch {
					case evt.Mask&unix.IN_CREATE != 0:
						p.sdcard <- true
					case evt.Mask&unix.IN_DELETE != 0:
						p.sdcard <- false
					}
				}
			}
		}
	}()
	return nil
}

func mountFS() error {
	devices := []struct {
		path string
		fs   string
	}{
		{"/dev", "devtmpfs"},
		{"/sys", "sysfs"},
		{"/proc", "proc"},
	}
	for _, dev := range devices {
		if err := os.MkdirAll(dev.path, 0o644); err != nil {
			return fmt.Errorf("platform: %w", err)
		}
		if err := syscall.Mount(dev.fs, dev.path, dev.fs, 0, ""); err != nil {
			return fmt.Errorf("platform: mount %s: %w", dev.path, err)
		}
	}
	return nil
}
