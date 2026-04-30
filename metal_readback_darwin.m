// Metal GPU->CPU readback for the Go binding.
//
// Takes a borrowed id<MTLTexture> from a maplibre-native texture session
// frame, blits it into a host-visible MTLBuffer, and copies the bytes to
// the caller's slice. The wrapper's offscreen texture lives in private
// storage so [MTLTexture getBytes:] would not work directly; the blit is
// the canonical pattern.
//
// Replaces ~10 lines once upstream lands mln_texture_read_still_image
// (sargunv/maplibre-native-ffi#9).

#import <Metal/Metal.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

int mln_go_metal_readback(void *device_ptr, void *texture_ptr,
                          uint8_t *out_rgba, size_t out_capacity,
                          uint32_t width, uint32_t height, char *err_out,
                          size_t err_len);

static void set_err(char *buf, size_t len, const char *msg) {
  if (!buf || len == 0) return;
  strncpy(buf, msg, len - 1);
  buf[len - 1] = 0;
}

int mln_go_metal_readback(void *device_ptr, void *texture_ptr,
                          uint8_t *out_rgba, size_t out_capacity,
                          uint32_t width, uint32_t height, char *err_out,
                          size_t err_len) {
  if (!device_ptr || !texture_ptr || !out_rgba) {
    set_err(err_out, err_len, "device, texture, and out_rgba must be non-null");
    return -1;
  }
  @autoreleasepool {
    id<MTLDevice> device = (__bridge id<MTLDevice>)device_ptr;
    id<MTLTexture> texture = (__bridge id<MTLTexture>)texture_ptr;

    size_t stride = (size_t)width * 4;
    size_t needed = stride * (size_t)height;
    if (out_capacity < needed) {
      char buf[128];
      snprintf(buf, sizeof(buf), "out_capacity %zu < required %zu",
               out_capacity, needed);
      set_err(err_out, err_len, buf);
      return -2;
    }

    id<MTLBuffer> buffer =
        [device newBufferWithLength:needed
                            options:MTLResourceStorageModeShared];
    if (!buffer) {
      set_err(err_out, err_len, "newBufferWithLength returned nil");
      return -3;
    }

    id<MTLCommandQueue> queue = [device newCommandQueue];
    if (!queue) {
      set_err(err_out, err_len, "newCommandQueue returned nil");
      return -4;
    }

    id<MTLCommandBuffer> cb = [queue commandBuffer];
    if (!cb) {
      set_err(err_out, err_len, "commandBuffer returned nil");
      return -5;
    }

    id<MTLBlitCommandEncoder> blit = [cb blitCommandEncoder];
    if (!blit) {
      set_err(err_out, err_len, "blitCommandEncoder returned nil");
      return -6;
    }

    [blit copyFromTexture:texture
                sourceSlice:0
                sourceLevel:0
               sourceOrigin:MTLOriginMake(0, 0, 0)
                 sourceSize:MTLSizeMake(width, height, 1)
                   toBuffer:buffer
          destinationOffset:0
     destinationBytesPerRow:stride
   destinationBytesPerImage:needed];
    [blit endEncoding];
    [cb commit];
    [cb waitUntilCompleted];

    if (cb.status != MTLCommandBufferStatusCompleted) {
      char buf[128];
      snprintf(buf, sizeof(buf),
               "command buffer status=%ld error=%ld",
               (long)cb.status,
               cb.error ? (long)cb.error.code : 0L);
      set_err(err_out, err_len, buf);
      return -7;
    }

    memcpy(out_rgba, [buffer contents], needed);
    return 0;
  }
}
