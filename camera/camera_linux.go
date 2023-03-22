// package camera implements an interface to the V4L2 camera
// interface on Raspberry Pi.
package camera

/*
#include <dlfcn.h>
#include <fcntl.h>
#include <stdlib.h>
#include <unistd.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/mman.h>
#include <sys/time.h>
#include <linux/videodev2.h>

// function wrappers that omit variadic parameters.

static int go_open(const char *path, int oflag) {
	return open(path, oflag);
}

static int go_ioctl(int filedes, unsigned long request, void *arg) {
	return ioctl(filedes, request, arg);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"image"
	"unsafe"
)

type Camera struct {
	dev       *v4l2Dev
	frames    chan Frame
	out       <-chan Frame
	destroyed chan struct{}
	closed    chan struct{}
}

type Frame struct {
	Err   error
	Image image.Image
	deq   C.struct_v4l2_buffer
}

func (c *Camera) Close() {
	c.closed <- struct{}{}
	for {
		select {
		case <-c.frames:
		case <-c.destroyed:
			return
		}
	}
}

func Open(dims image.Point, frames chan Frame, out <-chan Frame) (func(), error) {
	vid0, err := openDevice("/dev/video0")
	if err != nil {
		return nil, fmt.Errorf("camera: %w", err)
	}
	c := &Camera{
		frames:    frames,
		out:       out,
		dev:       vid0,
		destroyed: make(chan struct{}),
		closed:    make(chan struct{}),
	}
	if err := c.setup(dims); err != nil {
		return nil, fmt.Errorf("camera: %w", err)
	}
	return c.Close, nil
}

func (c *Camera) setup(dims image.Point) error {
	caps, err := c.dev.QueryCap()
	if err != nil {
		return err
	}

	const need = C.V4L2_CAP_VIDEO_CAPTURE | C.V4L2_CAP_STREAMING
	got := caps.capabilities
	if got&C.V4L2_CAP_DEVICE_CAPS != 0 {
		got = caps.device_caps
	}
	if got&need != need {
		return fmt.Errorf("missing camera capabilities (got %#x)", got)
	}

	wantFmt := C.struct_v4l2_pix_format{
		field:       C.V4L2_FIELD_NONE,
		colorspace:  C.V4L2_COLORSPACE_SMPTE170M,
		width:       C.__u32(dims.X),
		height:      C.__u32(dims.Y),
		pixelformat: C.V4L2_PIX_FMT_YUV420,
	}
	if err := c.dev.SFmtPix(C.V4L2_BUF_TYPE_VIDEO_CAPTURE, wantFmt); err != nil {
		return err
	}

	gotFmt, err := c.dev.GFmtPix(C.V4L2_BUF_TYPE_VIDEO_CAPTURE)
	if err != nil {
		return err
	}

	if gotFmt.field != wantFmt.field || gotFmt.pixelformat != wantFmt.pixelformat {
		return fmt.Errorf("format mismatch: got %+v, want %+v", gotFmt, wantFmt)
	}

	// Allocate streaming buffers.
	const nframes = 1
	req := C.struct_v4l2_requestbuffers{
		count:  nframes,
		_type:  C.V4L2_BUF_TYPE_VIDEO_CAPTURE,
		memory: C.V4L2_MEMORY_MMAP,
	}
	if err := c.dev.ReqBufs(req); err != nil {
		return err
	}

	if req.count <= 0 {
		return fmt.Errorf("camera: REQBUFS returned %d buffers", req.count)
	}
	var buffers [][]byte
	for i := 0; i < int(req.count); i++ {
		buf, err := c.dev.QueryBuf(C.V4L2_BUF_TYPE_VIDEO_CAPTURE, C.__u32(i))
		if err != nil {
			return err
		}
		off := *(*C.__u32)(unsafe.Pointer(&buf.m[0]))
		addr, err := C.mmap(nil, C.size_t(buf.length), C.PROT_READ, C.MAP_SHARED, c.dev.fd, C.off_t(off))
		if addr == nil {
			return fmt.Errorf("mmap of v4l2_buffer: %v", err)
		}
		s := unsafe.Slice((*byte)(addr), buf.length)
		buffers = append(buffers, s)
		if err := c.dev.QBuf(buf); err != nil {
			return err
		}
	}

	if err := c.dev.StreamOn(C.V4L2_BUF_TYPE_VIDEO_CAPTURE); err != nil {
		return err
	}
	go func() {
		defer close(c.destroyed)
		defer C.close(c.dev.fd)
		defer c.dev.StreamOff(C.V4L2_BUF_TYPE_VIDEO_CAPTURE)
		for _, buf := range buffers {
			defer C.munmap(unsafe.Pointer(&buf[0]), C.size_t(len(buf)))
		}
		var img image.YCbCr
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
				return c.dev.QBuf(f.deq)
			}
		}
		for {
			deq, err := c.dev.DQBuf(C.V4L2_BUF_TYPE_VIDEO_CAPTURE, C.V4L2_MEMORY_MMAP)
			if err != nil {
				deliverFrame(Frame{Err: err})
				return
			}
			if deq.flags&C.V4L2_BUF_FLAG_ERROR != 0 {
				if err := c.dev.QBuf(deq); err != nil {
					deliverFrame(Frame{Err: err})
					return
				}
				continue
			}
			buf := buffers[deq.index]
			w, h := int(gotFmt.width), int(gotFmt.height)
			img.Rect = image.Rect(0, 0, w, h)
			img.YStride = int(gotFmt.bytesperline)
			img.CStride = img.YStride / 2
			img.SubsampleRatio = image.YCbCrSubsampleRatio420
			cboff := img.YStride * h
			croff := cboff + img.CStride*h/2
			img.Y = buf[:cboff]
			img.Cb = buf[cboff:croff]
			img.Cr = buf[croff:]
			if err := deliverFrame(Frame{Image: &img, deq: deq}); err != nil {
				if !errors.Is(err, errClosed) {
					deliverFrame(Frame{Err: fmt.Errorf("camera: %w", err)})
				}
				return
			}
		}
	}()
	return nil
}

type v4l2Dev struct {
	fd C.int
}

func dlsym(dlh unsafe.Pointer, name string) (*[0]byte, error) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	sym := C.dlsym(dlh, cname)
	if sym == nil {
		return nil, fmt.Errorf("dlsym(%q): %s", name, C.GoString(C.dlerror()))
	}
	return (*[0]byte)(sym), nil
}

func openDevice(name string) (*v4l2Dev, error) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	// v4l2-compat.so hooks into the C library open(2).
	fd, err := C.go_open(cname, C.O_RDWR|C.O_CLOEXEC)
	if fd == -1 {
		return nil, fmt.Errorf("open %s: %v", name, err)
	}
	return &v4l2Dev{
		fd: fd,
	}, nil
}

func (d *v4l2Dev) ioctl(name string, req uintptr, arg unsafe.Pointer) error {
	if res, err := C.go_ioctl(d.fd, C.ulong(req), arg); res == -1 {
		return fmt.Errorf("v4l2: %s: %v", name, err)
	}
	return nil
}

func (d *v4l2Dev) QueryCap() (C.struct_v4l2_capability, error) {
	var caps C.struct_v4l2_capability
	err := d.ioctl("C.VIDIOC_QUERYCAP", C.VIDIOC_QUERYCAP, unsafe.Pointer(&caps))
	return caps, err
}

func (d *v4l2Dev) GFmtPix(_type C.__u32) (C.struct_v4l2_pix_format, error) {
	format := C.struct_v4l2_format{
		_type: _type,
	}
	err := d.ioctl("C.VIDIOC_G_FMT", C.VIDIOC_G_FMT, unsafe.Pointer(&format))
	pix := (*C.struct_v4l2_pix_format)(unsafe.Pointer(&format.fmt))
	return *pix, err
}

func (d *v4l2Dev) SFmtPix(_type C.__u32, f C.struct_v4l2_pix_format) error {
	format := C.struct_v4l2_format{
		_type: _type,
	}
	pix := (*C.struct_v4l2_pix_format)(unsafe.Pointer(&format.fmt))
	*pix = f
	return d.ioctl("C.VIDIOC_S_FMT", C.VIDIOC_S_FMT, unsafe.Pointer(&format))
}

func (d *v4l2Dev) SCtrl(id C.__u32, val C.__s32) error {
	ctrl := C.struct_v4l2_control{
		id:    id,
		value: val,
	}
	return d.ioctl("C.VIDIOC_S_CTRL", C.VIDIOC_S_CTRL, unsafe.Pointer(&ctrl))
}

func (d *v4l2Dev) ReqBufs(req C.struct_v4l2_requestbuffers) error {
	return d.ioctl("C.VIDIOC_REQBUFS", C.VIDIOC_REQBUFS, unsafe.Pointer(&req))
}

func (d *v4l2Dev) QueryBuf(_type, idx C.__u32) (C.struct_v4l2_buffer, error) {
	buf := C.struct_v4l2_buffer{
		_type: _type,
		index: idx,
	}
	err := d.ioctl("C.VIDIOC_QUERYBUF", C.VIDIOC_QUERYBUF, unsafe.Pointer(&buf))
	return buf, err
}

func (d *v4l2Dev) QBuf(buf C.struct_v4l2_buffer) error {
	return d.ioctl("C.VIDIOC_QBUF", C.VIDIOC_QBUF, unsafe.Pointer(&buf))
}

func (d *v4l2Dev) StreamOn(_type uint32) error {
	return d.ioctl("C.VIDIOC_STREAMON", C.VIDIOC_STREAMON, unsafe.Pointer(&_type))
}

func (d *v4l2Dev) StreamOff(_type uint32) error {
	return d.ioctl("C.VIDIOC_STREAMOFF", C.VIDIOC_STREAMOFF, unsafe.Pointer(&_type))
}

func (d *v4l2Dev) DQBuf(_type, mem C.__u32) (C.struct_v4l2_buffer, error) {
	deq := C.struct_v4l2_buffer{
		_type:  _type,
		memory: mem,
	}
	err := d.ioctl("C.VIDIOC_DQBUF", C.VIDIOC_DQBUF, unsafe.Pointer(&deq))
	return deq, err
}
