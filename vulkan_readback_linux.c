// Vulkan GPU->CPU readback for the Go binding.
//
// Adapted from mbgl::vulkan::Texture2D::readImage in
// third_party/maplibre-native/src/mbgl/vulkan/texture2d.cpp. The wrapper's
// VulkanTextureBackend allocates the offscreen image with TRANSFER_SRC
// usage so we can sample directly without an intermediate.
//
// Replaces ~10 lines once upstream lands mln_texture_read_still_image
// (sargunv/maplibre-native-ffi#9).

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <vulkan/vulkan.h>

int mln_go_vulkan_readback(void *instance_ptr, void *physical_device_ptr,
                           void *device_ptr, void *queue_ptr,
                           uint32_t queue_family_index, void *image_ptr,
                           uint32_t image_layout, uint32_t width,
                           uint32_t height, uint8_t *out_rgba,
                           size_t out_capacity, char *err_out, size_t err_len);

static void set_err(char *buf, size_t len, const char *fmt, int code) {
  if (!buf || len == 0) return;
  snprintf(buf, len, fmt, code);
}

int mln_go_vulkan_readback(void *instance_ptr, void *physical_device_ptr,
                           void *device_ptr, void *queue_ptr,
                           uint32_t queue_family_index, void *image_ptr,
                           uint32_t image_layout, uint32_t width,
                           uint32_t height, uint8_t *out_rgba,
                           size_t out_capacity, char *err_out, size_t err_len) {
  (void)instance_ptr;
  if (!physical_device_ptr || !device_ptr || !queue_ptr || !image_ptr ||
      !out_rgba) {
    set_err(err_out, err_len, "null required handle: code=%d", 0);
    return -1;
  }

  VkPhysicalDevice physical_device = (VkPhysicalDevice)physical_device_ptr;
  VkDevice device = (VkDevice)device_ptr;
  VkQueue queue = (VkQueue)queue_ptr;
  VkImage image = (VkImage)image_ptr;

  size_t needed = (size_t)width * height * 4;
  if (out_capacity < needed) {
    set_err(err_out, err_len, "out_capacity too small: code=%d", 0);
    return -2;
  }

  VkBuffer buffer = VK_NULL_HANDLE;
  VkDeviceMemory memory = VK_NULL_HANDLE;
  VkCommandPool pool = VK_NULL_HANDLE;
  VkCommandBuffer cb = VK_NULL_HANDLE;
  VkFence fence = VK_NULL_HANDLE;
  int ret = 0;

  // 1. Find host-visible host-coherent memory type.
  VkPhysicalDeviceMemoryProperties mem_props;
  vkGetPhysicalDeviceMemoryProperties(physical_device, &mem_props);
  VkMemoryPropertyFlags wanted = VK_MEMORY_PROPERTY_HOST_VISIBLE_BIT |
                                 VK_MEMORY_PROPERTY_HOST_COHERENT_BIT;
  int mem_type_index = -1;
  for (uint32_t i = 0; i < mem_props.memoryTypeCount; i++) {
    if ((mem_props.memoryTypes[i].propertyFlags & wanted) == wanted) {
      mem_type_index = (int)i;
      break;
    }
  }
  if (mem_type_index < 0) {
    set_err(err_out, err_len, "no host-visible memory type: code=%d", 0);
    return -3;
  }

  // 2. Create staging buffer.
  VkBufferCreateInfo buf_info = {
      .sType = VK_STRUCTURE_TYPE_BUFFER_CREATE_INFO,
      .size = needed,
      .usage = VK_BUFFER_USAGE_TRANSFER_DST_BIT,
      .sharingMode = VK_SHARING_MODE_EXCLUSIVE,
  };
  VkResult r = vkCreateBuffer(device, &buf_info, NULL, &buffer);
  if (r != VK_SUCCESS) {
    set_err(err_out, err_len, "vkCreateBuffer: VkResult=%d", r);
    ret = -4;
    goto cleanup;
  }

  VkMemoryRequirements mem_req;
  vkGetBufferMemoryRequirements(device, buffer, &mem_req);
  VkMemoryAllocateInfo alloc_info = {
      .sType = VK_STRUCTURE_TYPE_MEMORY_ALLOCATE_INFO,
      .allocationSize = mem_req.size,
      .memoryTypeIndex = (uint32_t)mem_type_index,
  };
  r = vkAllocateMemory(device, &alloc_info, NULL, &memory);
  if (r != VK_SUCCESS) {
    set_err(err_out, err_len, "vkAllocateMemory: VkResult=%d", r);
    ret = -5;
    goto cleanup;
  }
  r = vkBindBufferMemory(device, buffer, memory, 0);
  if (r != VK_SUCCESS) {
    set_err(err_out, err_len, "vkBindBufferMemory: VkResult=%d", r);
    ret = -6;
    goto cleanup;
  }

  // 3. Command pool + command buffer.
  VkCommandPoolCreateInfo pool_info = {
      .sType = VK_STRUCTURE_TYPE_COMMAND_POOL_CREATE_INFO,
      .flags = VK_COMMAND_POOL_CREATE_TRANSIENT_BIT,
      .queueFamilyIndex = queue_family_index,
  };
  r = vkCreateCommandPool(device, &pool_info, NULL, &pool);
  if (r != VK_SUCCESS) {
    set_err(err_out, err_len, "vkCreateCommandPool: VkResult=%d", r);
    ret = -7;
    goto cleanup;
  }
  VkCommandBufferAllocateInfo cb_alloc = {
      .sType = VK_STRUCTURE_TYPE_COMMAND_BUFFER_ALLOCATE_INFO,
      .commandPool = pool,
      .level = VK_COMMAND_BUFFER_LEVEL_PRIMARY,
      .commandBufferCount = 1,
  };
  r = vkAllocateCommandBuffers(device, &cb_alloc, &cb);
  if (r != VK_SUCCESS) {
    set_err(err_out, err_len, "vkAllocateCommandBuffers: VkResult=%d", r);
    ret = -8;
    goto cleanup;
  }

  // 4. Record commands: layout transition -> copy -> reverse layout transition.
  VkCommandBufferBeginInfo begin = {
      .sType = VK_STRUCTURE_TYPE_COMMAND_BUFFER_BEGIN_INFO,
      .flags = VK_COMMAND_BUFFER_USAGE_ONE_TIME_SUBMIT_BIT,
  };
  r = vkBeginCommandBuffer(cb, &begin);
  if (r != VK_SUCCESS) {
    set_err(err_out, err_len, "vkBeginCommandBuffer: VkResult=%d", r);
    ret = -9;
    goto cleanup;
  }

  VkImageMemoryBarrier to_transfer_src = {
      .sType = VK_STRUCTURE_TYPE_IMAGE_MEMORY_BARRIER,
      .srcAccessMask = VK_ACCESS_SHADER_READ_BIT,
      .dstAccessMask = VK_ACCESS_TRANSFER_READ_BIT,
      .oldLayout = (VkImageLayout)image_layout,
      .newLayout = VK_IMAGE_LAYOUT_TRANSFER_SRC_OPTIMAL,
      .srcQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED,
      .dstQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED,
      .image = image,
      .subresourceRange = {VK_IMAGE_ASPECT_COLOR_BIT, 0, 1, 0, 1},
  };
  vkCmdPipelineBarrier(cb, VK_PIPELINE_STAGE_FRAGMENT_SHADER_BIT,
                       VK_PIPELINE_STAGE_TRANSFER_BIT, 0, 0, NULL, 0, NULL, 1,
                       &to_transfer_src);

  VkBufferImageCopy region = {
      .bufferOffset = 0,
      .bufferRowLength = 0,    // tightly packed
      .bufferImageHeight = 0,
      .imageSubresource = {VK_IMAGE_ASPECT_COLOR_BIT, 0, 0, 1},
      .imageOffset = {0, 0, 0},
      .imageExtent = {width, height, 1},
  };
  vkCmdCopyImageToBuffer(cb, image, VK_IMAGE_LAYOUT_TRANSFER_SRC_OPTIMAL,
                         buffer, 1, &region);

  VkImageMemoryBarrier to_original = {
      .sType = VK_STRUCTURE_TYPE_IMAGE_MEMORY_BARRIER,
      .srcAccessMask = VK_ACCESS_TRANSFER_READ_BIT,
      .dstAccessMask = VK_ACCESS_SHADER_READ_BIT,
      .oldLayout = VK_IMAGE_LAYOUT_TRANSFER_SRC_OPTIMAL,
      .newLayout = (VkImageLayout)image_layout,
      .srcQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED,
      .dstQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED,
      .image = image,
      .subresourceRange = {VK_IMAGE_ASPECT_COLOR_BIT, 0, 1, 0, 1},
  };
  vkCmdPipelineBarrier(cb, VK_PIPELINE_STAGE_TRANSFER_BIT,
                       VK_PIPELINE_STAGE_FRAGMENT_SHADER_BIT, 0, 0, NULL, 0,
                       NULL, 1, &to_original);

  r = vkEndCommandBuffer(cb);
  if (r != VK_SUCCESS) {
    set_err(err_out, err_len, "vkEndCommandBuffer: VkResult=%d", r);
    ret = -10;
    goto cleanup;
  }

  // 5. Submit, fence, wait.
  VkFenceCreateInfo fence_info = {
      .sType = VK_STRUCTURE_TYPE_FENCE_CREATE_INFO,
  };
  r = vkCreateFence(device, &fence_info, NULL, &fence);
  if (r != VK_SUCCESS) {
    set_err(err_out, err_len, "vkCreateFence: VkResult=%d", r);
    ret = -11;
    goto cleanup;
  }
  VkSubmitInfo submit = {
      .sType = VK_STRUCTURE_TYPE_SUBMIT_INFO,
      .commandBufferCount = 1,
      .pCommandBuffers = &cb,
  };
  r = vkQueueSubmit(queue, 1, &submit, fence);
  if (r != VK_SUCCESS) {
    set_err(err_out, err_len, "vkQueueSubmit: VkResult=%d", r);
    ret = -12;
    goto cleanup;
  }
  r = vkWaitForFences(device, 1, &fence, VK_TRUE, UINT64_MAX);
  if (r != VK_SUCCESS) {
    set_err(err_out, err_len, "vkWaitForFences: VkResult=%d", r);
    ret = -13;
    goto cleanup;
  }

  // 6. Map, copy, unmap.
  void *mapped = NULL;
  r = vkMapMemory(device, memory, 0, needed, 0, &mapped);
  if (r != VK_SUCCESS) {
    set_err(err_out, err_len, "vkMapMemory: VkResult=%d", r);
    ret = -14;
    goto cleanup;
  }
  memcpy(out_rgba, mapped, needed);
  vkUnmapMemory(device, memory);

cleanup:
  if (fence) vkDestroyFence(device, fence, NULL);
  if (cb) vkFreeCommandBuffers(device, pool, 1, &cb);
  if (pool) vkDestroyCommandPool(device, pool, NULL);
  if (memory) vkFreeMemory(device, memory, NULL);
  if (buffer) vkDestroyBuffer(device, buffer, NULL);
  return ret;
}
