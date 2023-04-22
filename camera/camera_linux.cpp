#include <iostream>
#include <libcamera/libcamera.h>

extern "C" {

#include "_cgo_export.h"
#include "camera_linux.h"
#include <stdint.h>

using namespace libcamera;

static std::unique_ptr<CameraManager> cam_manager;
static std::unique_ptr<FrameBufferAllocator> allocator;
static std::unique_ptr<CameraConfiguration> config;
static std::shared_ptr<Camera> camera;
static std::vector<std::unique_ptr<Request>> requests;
static uintptr_t callback_handle;

static void requestComplete(Request *req) {
  if (req->status() == Request::RequestCancelled)
    return;

  auto handle = static_cast<uintptr_t>(callback_handle);
  requestCallback(handle, req->cookie());
}

buffer buffer_at(size_t idx) {
  StreamConfiguration &streamConfig = config->at(0);
  auto stream = streamConfig.stream();
  const auto &buf = allocator->buffers(stream)[idx];
  const auto &plane = buf->planes()[0];
  buffer b = {};
  b.fd = plane.fd.get();
  b.offset = plane.offset;
  b.length = plane.offset;
  // Verify that all planes are laid out in a contiguous block
  // in one file descriptor. This is true in libcamera today, but
  // may not be in future.
  for (auto &p : buf->planes()) {
    if (p.fd.get() != b.fd || p.offset != b.offset + b.length) {
      abort();
    }
    b.length += p.length;
  }
  return b;
}

size_t num_buffers() { return requests.size(); }

int queue_request(size_t buf_idx) {
  auto &req = requests.at(buf_idx);
  req.get()->reuse(Request::ReuseBuffers);
  return camera->queueRequest(req.get());
}

format frame_format() {
  StreamConfiguration &conf = config->at(0);
  format f = {
      conf.size.width,
      conf.size.height,
      conf.stride,
  };
  return f;
}

int open_camera(unsigned int width, unsigned int height, uintptr_t handle) {
  auto cm = std::make_unique<CameraManager>();
  auto ret = cm->start();
  if (ret != 0) {
    return ret;
  }
  if (cm->cameras().empty()) {
    cm->stop();
    return -EINVAL;
  }
  auto cameraId = cm->cameras()[0]->id();
  auto c = cm->get(cameraId);
  ret = c->acquire();
  if (ret != 0) {
    cm->stop();
    return ret;
  }
  // We want the center (width, height) section of the full camera sensor
  // resoluton. The only way to achieve that with libcamera is to configure
  // two streams: one (width, height) stream with the desired pixel format
  // that we'll buffer and process, and one raw output at the full sensor size
  // that won't be buffered. The only purpose of the raw output is to force
  // the sensor size.
  auto conf = c->generateConfiguration(
      {StreamRole::Viewfinder, StreamRole::Viewfinder});
  if (conf == nullptr) {
    c->release();
    cm->stop();
    return -EINVAL;
  }
  auto &streamConfig = conf->at(0);
  streamConfig.size.width = width;
  streamConfig.size.height = height;
  streamConfig.pixelFormat = formats::YUV420;
  // Minimize latency.
  streamConfig.bufferCount = 1;

  auto &rawConfig = conf->at(1);
  auto sensor_size = c->properties().get(properties::PixelArraySize).value();
  rawConfig.size = sensor_size;
  rawConfig.pixelFormat = formats::SBGGR8; // Any supported raw format would do.
  rawConfig.colorSpace = ColorSpace::Raw;
  rawConfig.bufferCount = 0;
  ret = conf->validate();
  if (conf->at(0).pixelFormat != formats::YUV420) {
    c->release();
    cm->stop();
    return -EINVAL;
  }
  ret = conf->validate();
  ret = c->configure(conf.get());
  if (ret != 0) {
    c->release();
    cm->stop();
    return ret;
  }

  auto a = std::make_unique<FrameBufferAllocator>(c);

  auto stream = streamConfig.stream();
  ret = a->allocate(stream);
  if (ret < 0) {
    c->release();
    cm->stop();
    return -EINVAL;
  }

  const auto &buffers = a->buffers(stream);
  std::vector<std::unique_ptr<Request>> reqs;
  for (unsigned int i = 0; i < buffers.size(); ++i) {
    auto req = c->createRequest(i);
    if (!req) {
      a->free(stream);
      c->release();
      cm->stop();
      return -EINVAL;
    }

    const auto &buffer = buffers[i];
    auto ret = req->addBuffer(stream, buffer.get());
    if (ret < 0) {
      a->free(stream);
      c->release();
      cm->stop();
      return -EINVAL;
    }

    reqs.push_back(std::move(req));
  }

  c->requestCompleted.connect(requestComplete);

  callback_handle = handle;
  cam_manager = std::move(cm);
  camera = std::move(c);
  allocator = std::move(a);
  config = std::move(conf);
  requests = std::move(reqs);
  return 0;
}

int start_camera(unsigned int width, unsigned int height) {
  Size sz = {width, height};
  auto max_size =
      camera->properties().get(properties::PixelArraySize).value_or(sz);
  Rectangle crop = {};
  crop.x = (max_size.width - width) / 2;
  crop.y = (max_size.height - height) / 2;
  crop.width = width;
  crop.height = height;
  auto controls = std::make_unique<ControlList>();
  controls.get()->set(libcamera::controls::ScalerCrop, crop);
  auto ret = camera->start(controls.get());
  if (ret != 0) {
    return ret;
  }
  for (auto &req : requests) {
    auto ret = camera->queueRequest(req.get());
    if (ret != 0) {
      return ret;
    }
  }
  return 0;
}

void close_camera() {
  camera->stop();
  requests.clear();
  auto &streamConfig = config->at(0);
  auto stream = streamConfig.stream();
  allocator->free(stream);
  allocator = nullptr;
  camera->release();
  camera = nullptr;
  cam_manager->stop();
  cam_manager = nullptr;
}
}