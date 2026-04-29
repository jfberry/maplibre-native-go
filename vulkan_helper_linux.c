// Default Vulkan context creation for the Go binding.
//
// Spins up a minimal headless Vulkan stack: an instance with no extensions,
// the first physical device, the first queue family that advertises
// VK_QUEUE_GRAPHICS_BIT, and a logical device with one queue. Suitable for
// CPU-only Mesa lavapipe deploys; a discrete GPU will also be picked up if
// present and ranks first in the enumeration.
//
// Callers wanting to share their own Vulkan stack pass handles directly to
// AttachVulkanTextureWithContext and skip this helper entirely.

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <vulkan/vulkan.h>

// Mirrors the cgo declaration in texture_vulkan_linux.go; keeping the field
// types as void* (not VkInstance / VkDevice etc) means the Go side can
// declare the struct without depending on <vulkan/vulkan.h>.
typedef struct mln_go_vulkan_context {
  void *instance;
  void *physical_device;
  void *device;
  void *queue;
  uint32_t queue_family_index;
} mln_go_vulkan_context;

int mln_go_vulkan_context_create(mln_go_vulkan_context *out, char *err_out,
                                 size_t err_len);
void mln_go_vulkan_context_destroy(mln_go_vulkan_context *ctx);

static void set_err(char *buf, size_t len, const char *fmt, int code) {
  if (!buf || len == 0) return;
  snprintf(buf, len, fmt, code);
}

int mln_go_vulkan_context_create(mln_go_vulkan_context *out, char *err_out,
                                 size_t err_len) {
  if (!out) return -1;
  memset(out, 0, sizeof(*out));

  VkInstance instance = VK_NULL_HANDLE;
  VkPhysicalDevice physical_device = VK_NULL_HANDLE;
  VkDevice device = VK_NULL_HANDLE;
  VkQueue queue = VK_NULL_HANDLE;

  VkApplicationInfo app_info = {
      .sType = VK_STRUCTURE_TYPE_APPLICATION_INFO,
      .pApplicationName = "maplibre-native-go",
      .applicationVersion = VK_MAKE_VERSION(0, 1, 0),
      .pEngineName = "maplibre-native",
      .engineVersion = VK_MAKE_VERSION(0, 1, 0),
      .apiVersion = VK_API_VERSION_1_2,
  };
  VkInstanceCreateInfo inst_info = {
      .sType = VK_STRUCTURE_TYPE_INSTANCE_CREATE_INFO,
      .pApplicationInfo = &app_info,
  };
  VkResult r = vkCreateInstance(&inst_info, NULL, &instance);
  if (r != VK_SUCCESS) {
    set_err(err_out, err_len, "vkCreateInstance: VkResult=%d", r);
    return -2;
  }

  uint32_t pd_count = 0;
  r = vkEnumeratePhysicalDevices(instance, &pd_count, NULL);
  if (r != VK_SUCCESS || pd_count == 0) {
    set_err(err_out, err_len, "vkEnumeratePhysicalDevices: VkResult=%d", r);
    vkDestroyInstance(instance, NULL);
    return -3;
  }
  VkPhysicalDevice *pds = (VkPhysicalDevice *)calloc(pd_count, sizeof(*pds));
  if (!pds) {
    set_err(err_out, err_len, "calloc physical devices: code=%d", 0);
    vkDestroyInstance(instance, NULL);
    return -4;
  }
  r = vkEnumeratePhysicalDevices(instance, &pd_count, pds);
  if (r != VK_SUCCESS) {
    free(pds);
    set_err(err_out, err_len, "vkEnumeratePhysicalDevices fill: VkResult=%d", r);
    vkDestroyInstance(instance, NULL);
    return -5;
  }
  physical_device = pds[0];
  free(pds);

  uint32_t qf_count = 0;
  vkGetPhysicalDeviceQueueFamilyProperties(physical_device, &qf_count, NULL);
  if (qf_count == 0) {
    set_err(err_out, err_len, "no Vulkan queue families: code=%d", 0);
    vkDestroyInstance(instance, NULL);
    return -6;
  }
  VkQueueFamilyProperties *qfs =
      (VkQueueFamilyProperties *)calloc(qf_count, sizeof(*qfs));
  if (!qfs) {
    set_err(err_out, err_len, "calloc queue families: code=%d", 0);
    vkDestroyInstance(instance, NULL);
    return -7;
  }
  vkGetPhysicalDeviceQueueFamilyProperties(physical_device, &qf_count, qfs);
  int graphics_qf = -1;
  for (uint32_t i = 0; i < qf_count; i++) {
    if (qfs[i].queueFlags & VK_QUEUE_GRAPHICS_BIT) {
      graphics_qf = (int)i;
      break;
    }
  }
  free(qfs);
  if (graphics_qf < 0) {
    set_err(err_out, err_len, "no graphics queue family: code=%d", 0);
    vkDestroyInstance(instance, NULL);
    return -8;
  }
  out->queue_family_index = (uint32_t)graphics_qf;

  float qprio = 1.0f;
  VkDeviceQueueCreateInfo dqi = {
      .sType = VK_STRUCTURE_TYPE_DEVICE_QUEUE_CREATE_INFO,
      .queueFamilyIndex = out->queue_family_index,
      .queueCount = 1,
      .pQueuePriorities = &qprio,
  };
  VkDeviceCreateInfo di = {
      .sType = VK_STRUCTURE_TYPE_DEVICE_CREATE_INFO,
      .queueCreateInfoCount = 1,
      .pQueueCreateInfos = &dqi,
  };
  r = vkCreateDevice(physical_device, &di, NULL, &device);
  if (r != VK_SUCCESS) {
    set_err(err_out, err_len, "vkCreateDevice: VkResult=%d", r);
    vkDestroyInstance(instance, NULL);
    return -9;
  }

  vkGetDeviceQueue(device, out->queue_family_index, 0, &queue);

  out->instance = (void *)instance;
  out->physical_device = (void *)physical_device;
  out->device = (void *)device;
  out->queue = (void *)queue;
  return 0;
}

void mln_go_vulkan_context_destroy(mln_go_vulkan_context *ctx) {
  if (!ctx) return;
  if (ctx->device) {
    vkDestroyDevice((VkDevice)ctx->device, NULL);
    ctx->device = NULL;
  }
  if (ctx->instance) {
    vkDestroyInstance((VkInstance)ctx->instance, NULL);
    ctx->instance = NULL;
  }
  ctx->physical_device = NULL;
  ctx->queue = NULL;
  ctx->queue_family_index = 0;
}
