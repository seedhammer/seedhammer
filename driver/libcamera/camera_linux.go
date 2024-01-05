// package libcamera implements an interface to the libcamera2
// camera driver.
package libcamera

/*

#cgo CFLAGS: -Werror
#cgo LDFLAGS: -lcamera -lcamera-base

#include <stdint.h>
#include "camera_linux.h"

*/
import "C"

import (
	"errors"
	"fmt"
	"image"
	"runtime/cgo"
	"syscall"

	"seedhammer.com/gui"
)

type Camera struct {
	frames    chan gui.FrameEvent
	out       <-chan gui.FrameEvent
	bufs      chan C.size_t
	destroyed chan struct{}
	closed    chan struct{}
}

type Frame struct {
	err    error
	image  image.Image
	bufIdx C.size_t
}

func (f Frame) Image() image.Image {
	return f.image
}

func (f Frame) Error() error {
	return f.err
}

func (c *Camera) Close() {
	close(c.closed)
	for {
		select {
		case <-c.frames:
		case <-c.destroyed:
			<-singleton
			return
		}
	}
}

// To simplify C++ reference management, there is only support for
// a single camera at a time.
var singleton = make(chan struct{}, 1)

//export requestCallback
func requestCallback(handle C.uintptr_t, bufIdx C.size_t) {
	c := cgo.Handle(handle).Value().(*Camera)
	select {
	case <-c.closed:
	case c.bufs <- bufIdx:
	}
}

func Open(dims image.Point, frames chan gui.FrameEvent, out <-chan gui.FrameEvent) func() {
	c := &Camera{
		frames:    frames,
		out:       out,
		destroyed: make(chan struct{}),
		closed:    make(chan struct{}),
		bufs:      make(chan C.size_t),
	}
	select {
	case singleton <- struct{}{}:
	default:
		go func() {
			frames <- Frame{err: errors.New("camera: only a single camera can be open simultaneously")}
		}()
		return func() {}
	}
	if err := c.setup(dims); err != nil {
		<-singleton
		go func() {
			frames <- Frame{err: fmt.Errorf("camera: %w", err)}
		}()
		return func() {}
	}
	return c.Close
}

func (c *Camera) setup(dims image.Point) error {
	handle := cgo.NewHandle(c)
	if res := C.open_camera(C.uint(dims.X), C.uint(dims.Y), C.uintptr_t(handle)); res != 0 {
		handle.Delete()
		return fmt.Errorf("open_camera: %d", res)
	}
	go func() {
		defer close(c.destroyed)
		defer handle.Delete()
		defer C.close_camera()

		errClosed := errors.New("closed")
		deliverFrame := func(f Frame) error {
			select {
			case <-c.closed:
				return errClosed
			case c.frames <- f:
			}
			select {
			case <-c.closed:
				return errClosed
			case f := <-c.out:
				if res := C.queue_request(f.(Frame).bufIdx); res != 0 {
					return fmt.Errorf("queue_request: %d", res)
				}
				return nil
			}
		}
		if res := C.start_camera(C.uint(dims.X), C.uint(dims.Y)); res != 0 {
			err := fmt.Errorf("camera: start_camera: %d", res)
			deliverFrame(Frame{err: err})
			return
		}
		format := C.frame_format()
		imgs := make([]*image.YCbCr, C.num_buffers())
		for i := range imgs {
			desc := C.buffer_at(C.size_t(i))
			buf, err := syscall.Mmap(int(desc.fd), int64(desc.offset), int(desc.length), syscall.PROT_READ, syscall.MAP_SHARED)
			if err != nil {
				deliverFrame(Frame{err: err})
				return
			}
			defer syscall.Munmap(buf)
			var img image.YCbCr
			w, h := int(format.width), int(format.height)
			img.Rect = image.Rect(0, 0, w, h)
			img.YStride = int(format.stride)
			img.CStride = img.YStride / 2
			img.SubsampleRatio = image.YCbCrSubsampleRatio420
			cboff := img.YStride * h
			croff := cboff + img.CStride*h/2
			img.Y = buf[:cboff]
			img.Cb = buf[cboff:croff]
			img.Cr = buf[croff:]
			imgs[i] = &img
		}
		for {
			select {
			case <-c.closed:
				return
			case bufIdx := <-c.bufs:
				f := Frame{
					image:  imgs[bufIdx],
					bufIdx: bufIdx,
				}
				if err := deliverFrame(f); err != nil {
					if !errors.Is(err, errClosed) {
						deliverFrame(Frame{err: fmt.Errorf("camera: %w", err)})
					}
					return
				}
			}
		}
	}()
	return nil
}

func (Frame) ImplementsEvent() {}
