//go:build linux && arm

package main

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"io"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
	"seedhammer.com/driver/drm"
	"seedhammer.com/driver/libcamera"
	"seedhammer.com/driver/mjolnir"
	"seedhammer.com/driver/wshat"
	"seedhammer.com/gui"
	"seedhammer.com/zbar"
)

// Debug hooks.
var (
	engraverHook func() io.ReadWriteCloser
	initHook     func(p *Platform) error
)

type Platform struct {
	display *drm.LCD
	events  chan gui.Event
	wakeups chan struct{}
	camera  struct {
		frames chan gui.FrameEvent
		out    chan gui.FrameEvent
		frame  gui.FrameEvent
		close  func()
		active bool
	}
}

func Init() (*Platform, error) {
	// Ignore errors from setting up filesystems; they may already have been.
	_ = mountFS()
	p := &Platform{
		events:  make(chan gui.Event, 10),
		wakeups: make(chan struct{}, 1),
	}
	c := &p.camera
	c.frames = make(chan gui.FrameEvent)
	c.out = make(chan gui.FrameEvent)
	if initHook != nil {
		if err := initHook(p); err != nil {
			log.Printf("debug: %v", err)
		}
	}
	if err := p.initSDCardNotifier(); err != nil {
		return nil, err
	}
	if err := wshat.Open(p.events); err != nil {
		return nil, err
	}
	d, err := drm.Open()
	if err != nil {
		return nil, err
	}
	p.display = d
	return p, nil
}

func (p *Platform) Wakeup() {
	select {
	case p.wakeups <- struct{}{}:
	default:
	}
}

func (p *Platform) Events() []gui.Event {
	c := &p.camera
	if c.close != nil {
		if c.frame != nil {
			c.out <- c.frame
			c.frame = nil
		}
		if !c.active {
			c.close()
			c.close = nil
		}
		c.active = false
	}
	var evts []gui.Event
	for {
		select {
		case e := <-p.events:
			evts = append(evts, e)
		case c.frame = <-c.frames:
			evts = append(evts, c.frame)
		default:
			if len(evts) > 0 {
				return evts
			}
			select {
			case e := <-p.events:
				evts = append(evts, e)
			case c.frame = <-c.frames:
				evts = append(evts, c.frame)
			case <-p.wakeups:
				return evts
			}
		}
	}
}

func (p *Platform) DisplaySize() image.Point {
	return p.display.Size()
}

func (p *Platform) Dirty(r image.Rectangle) error {
	return p.display.Dirty(r)
}

func (p *Platform) NextChunk() (draw.RGBA64Image, bool) {
	return p.display.NextChunk()
}

func (p *Platform) Engraver() (io.ReadWriteCloser, error) {
	if engraverHook != nil {
		return engraverHook(), nil
	}
	return mjolnir.Open("")
}

func (p *Platform) ScanQR(img *image.Gray) ([][]byte, error) {
	return zbar.Scan(img)
}

func (p *Platform) CameraFrame(dims image.Point) {
	c := &p.camera
	if c.close == nil {
		c.close = libcamera.Open(dims, p.camera.frames, p.camera.out)
	}
	c.active = true
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
	go func() {
		defer f.Close()
		p.events <- gui.SDCardEvent{
			Inserted: inserted,
		}
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
					switch {
					case evt.Mask&unix.IN_CREATE != 0:
						p.events <- gui.SDCardEvent{Inserted: true}
					case evt.Mask&unix.IN_DELETE != 0:
						p.events <- gui.SDCardEvent{Inserted: false}
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
