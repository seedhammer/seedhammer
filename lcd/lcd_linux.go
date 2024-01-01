// package lcd implements a display on top of dumb DRM devices.
package lcd

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"os"
	"syscall"
	"unsafe"

	"seedhammer.com/image/rgb565"
)

/*
#include <stdint.h>
#include <sys/ioctl.h>
#include <drm/drm.h>

// go_ioctl is like ioctl but without the variadic parameter.
static int go_ioctl(int filedes, unsigned long request, void *arg) {
       return ioctl(filedes, request, arg);
}

// Wrapper functions for avoiding cgo pointer passing violations
// for embedded pointers.

static int ioctlDrmModeGetResources(int fd, struct drm_mode_card_res *res, __u32 nconns, __u32 *connId, __u32 ncrtcs, __u32 *crtcId) {
	res->count_connectors = nconns;
	res->connector_id_ptr = (__u64)(uintptr_t)connId;
	res->count_crtcs = ncrtcs;
	res->crtc_id_ptr = (__u64)(uintptr_t)crtcId;
	return ioctl(fd, DRM_IOCTL_MODE_GETRESOURCES, res);
}

static int ioctlDrmModeSetCrtc(int fd, struct drm_mode_crtc *crtc, __u32 nconns, __u32 *connId) {
	crtc->count_connectors = nconns;
	crtc->set_connectors_ptr = (__u64)(uintptr_t)connId;
	return ioctl(fd, DRM_IOCTL_MODE_SETCRTC, crtc);
}

static int ioctlDrmModeGetConnector(int fd, struct drm_mode_get_connector *conn, __u32 nmodes, struct drm_mode_modeinfo *mode) {
	conn->count_modes = nmodes;
	conn->modes_ptr = (__u64)(uintptr_t)mode;
	return ioctl(fd, DRM_IOCTL_MODE_GETCONNECTOR, conn);
}

static int ioctlDrmModeDirtyFb(int fd, struct drm_mode_fb_dirty_cmd *cmd, __u32 nclips, struct drm_clip_rect *clips) {
	cmd->num_clips = nclips,
	cmd->clips_ptr = (__u64)(uintptr_t)clips;
	return ioctl(fd, DRM_IOCTL_MODE_DIRTYFB, cmd);
}
*/
import "C"

type LCD struct {
	fb   draw.RGBA64Image
	dev  *os.File
	mmap []byte
	fbId C.__u32
}

func (l *LCD) Close() {
	if l.mmap != nil {
		syscall.Munmap(l.mmap)
	}
	if l.dev != nil {
		l.dev.Close()
	}
	*l = LCD{}
}

const (
	lcdWidth  = 240
	lcdHeight = 240
)

func Open() (*LCD, error) {
	drm, err := os.OpenFile("/dev/dri/card0", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	l, err := setup(drm)
	if err != nil {
		drm.Close()
		return nil, fmt.Errorf("lcd: %w", err)
	}
	return l, nil
}

func setup(dev *os.File) (*LCD, error) {
	fd := int(dev.Fd())
	caps := C.struct_drm_get_cap{
		capability: C.DRM_CAP_DUMB_BUFFER,
	}
	if err := ioctl(fd, "DRM_IOCTL_GET_CAP", C.DRM_IOCTL_GET_CAP, unsafe.Pointer(&caps)); err != nil {
		return nil, err
	}
	if caps.value != 1 {
		return nil, errors.New("DRM_CAP_DUMB_BUFFER not supported")
	}
	// Query card resources, assuming a simple setup with one
	// connector and one crtc.
	var connID, crtcID C.__u32
	var res C.struct_drm_mode_card_res
	if res, err := C.ioctlDrmModeGetResources(C.int(fd), &res, 1, &connID, 1, &crtcID); res < 0 {
		return nil, fmt.Errorf("ioctl(DRM_IOCTL_MODE_GETRESOURCES): %w", err)
	}
	// Query connector mode.
	conn := C.struct_drm_mode_get_connector{
		connector_id: connID,
	}
	var mode C.struct_drm_mode_modeinfo
	if res, err := C.ioctlDrmModeGetConnector(C.int(fd), &conn, 1, &mode); res < 0 {
		return nil, fmt.Errorf("ioctl(DRM_IOCTL_MODE_GETCONNECTOR): %w", err)
	}
	creq := C.struct_drm_mode_create_dumb{
		width:  C.__u32(mode.hdisplay),
		height: C.__u32(mode.vdisplay),
		bpp:    16,
	}
	if err := ioctl(fd, "DRM_IOCTL_MODE_CREATE_DUMB", C.DRM_IOCTL_MODE_CREATE_DUMB, unsafe.Pointer(&creq)); err != nil {
		return nil, err
	}
	fbcmd := C.struct_drm_mode_fb_cmd{
		width:  creq.width,
		height: creq.height,
		pitch:  creq.pitch,
		bpp:    creq.bpp,
		depth:  creq.bpp,
		handle: creq.handle,
	}
	if err := ioctl(fd, "DRM_IOCTL_MODE_ADDFB", C.DRM_IOCTL_MODE_ADDFB, unsafe.Pointer(&fbcmd)); err != nil {
		return nil, err
	}
	mreq := C.struct_drm_mode_map_dumb{
		handle: creq.handle,
	}
	if err := ioctl(fd, "DRM_IOCTL_MODE_MAP_DUMB", C.DRM_IOCTL_MODE_MAP_DUMB, unsafe.Pointer(&mreq)); err != nil {
		return nil, err
	}
	crtc := C.struct_drm_mode_crtc{
		crtc_id:    crtcID,
		fb_id:      fbcmd.fb_id,
		mode_valid: 1,
		mode:       mode,
	}
	if res, err := C.ioctlDrmModeSetCrtc(C.int(fd), &crtc, 1, &connID); res < 0 {
		return nil, fmt.Errorf("ioctl(DRM_IOCTL_MODE_SETCRTC): %w", err)
	}
	mmap, err := syscall.Mmap(fd, int64(mreq.offset), int(creq.size), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("framebuffer mmap failed: %w", err)
	}
	fb := &rgb565.Image{
		Pix:    unsafe.Slice((*rgb565.Color)(unsafe.Pointer(unsafe.SliceData(mmap))), len(mmap)/int(unsafe.Sizeof(rgb565.Color{}))),
		Stride: int(creq.pitch) / int(unsafe.Sizeof(rgb565.Color{})),
		Rect:   image.Rect(0, 0, int(creq.width), int(creq.height)),
	}
	l := &LCD{
		fb:   fb,
		dev:  dev,
		mmap: mmap,
		fbId: fbcmd.fb_id,
	}
	return l, nil
}

func ioctl(fd int, name string, req uintptr, arg unsafe.Pointer) error {
	if res, err := C.go_ioctl(C.int(fd), C.ulong(req), arg); res == -1 {
		return fmt.Errorf("ioctl(%s): %w", name, err)
	}
	return nil
}

func (l *LCD) Framebuffer() draw.RGBA64Image {
	return l.fb
}

func (l *LCD) Dirty(sr image.Rectangle) error {
	sr = sr.Intersect(l.fb.Bounds())
	if sr.Empty() {
		return nil
	}
	dirty := C.struct_drm_mode_fb_dirty_cmd{
		fb_id: l.fbId,
	}
	rects := []C.struct_drm_clip_rect{
		{
			x1: C.ushort(sr.Min.X),
			y1: C.ushort(sr.Min.Y),
			x2: C.ushort(sr.Max.X),
			y2: C.ushort(sr.Max.Y),
		},
	}
	if res, err := C.ioctlDrmModeDirtyFb(C.int(l.dev.Fd()), &dirty, C.__u32(len(rects)), &rects[0]); res < 0 {
		return fmt.Errorf("ioctl(DRM_IOCTL_MODE_DIRTYFB): %w", err)
	}
	return nil
}
