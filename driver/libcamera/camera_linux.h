#ifndef CAMERA_LINUX_H
#define CAMERA_LINUX_H

#include <stdint.h>

typedef struct {
  int fd;
  unsigned int offset;
  unsigned int length;
} buffer;

typedef struct {
  unsigned int width;
  unsigned int height;
  unsigned int stride;
} format;

extern int open_camera(unsigned int width, unsigned int height, uintptr_t handle);
extern int start_camera(unsigned int width, unsigned int height);
extern void close_camera();
extern int queue_request(size_t buf_idx);
extern size_t num_buffers();
extern buffer buffer_at(size_t idx);
extern format frame_format();

#endif