// Metal compositor that samples a maplibre-native-go texture-session frame
// into the SDL window's CAMetalLayer.
//
// Mirrors the zig-map render/metal pattern:
//   - Create an MTLDevice; configure the layer's device + pixelFormat +
//     drawableSize.
//   - Compile a tiny vertex+fragment shader that emits a screen-space quad
//     and samples our texture into it.
//   - Each draw: nextDrawable -> render pass -> drawPrimitives -> present.
//
// The shader source is embedded so the example is self-contained.

#import <Metal/Metal.h>
#import <QuartzCore/CAMetalLayer.h>
#include <stdlib.h>
#include <string.h>

static NSString *const kShaderSource = @
    "#include <metal_stdlib>\n"
    "using namespace metal;\n"
    "struct VertexOut { float4 position [[position]]; float2 uv; };\n"
    "vertex VertexOut vertex_main(uint vid [[vertex_id]]) {\n"
    "  float2 positions[6] = {{-1,-1},{1,-1},{-1,1},{1,-1},{1,1},{-1,1}};\n"
    "  float2 uvs[6]       = {{0,1},{1,1},{0,0},{1,1},{1,0},{0,0}};\n"
    "  VertexOut o;\n"
    "  o.position = float4(positions[vid], 0, 1);\n"
    "  o.uv = uvs[vid];\n"
    "  return o;\n"
    "}\n"
    "fragment float4 fragment_main(VertexOut in [[stage_in]],\n"
    "                              texture2d<float> tex [[texture(0)]]) {\n"
    "  constexpr sampler s(address::clamp_to_edge, filter::linear);\n"
    "  return tex.sample(s, in.uv);\n"
    "}\n";

typedef struct mln_compositor mln_compositor;
mln_compositor *compositor_create(void *layer_ptr);
void           *compositor_device(mln_compositor *c);
void            compositor_resize(mln_compositor *c, double width, double height);
int             compositor_draw(mln_compositor *c, void *texture_ptr);
const char     *compositor_last_error(mln_compositor *c);
void            compositor_destroy(mln_compositor *c);

struct mln_compositor {
  void *layer;        // CAMetalLayer*  (retained)
  void *device;       // id<MTLDevice>  (retained)
  void *queue;        // id<MTLCommandQueue> (retained)
  void *pipeline;     // id<MTLRenderPipelineState> (retained)
  char  last_error[256];
};

static void set_error(mln_compositor *c, NSString *msg) {
  if (!c) return;
  const char *s = [msg UTF8String];
  if (!s) s = "(no message)";
  strncpy(c->last_error, s, sizeof(c->last_error) - 1);
  c->last_error[sizeof(c->last_error) - 1] = 0;
}

static id<MTLRenderPipelineState>
build_pipeline(mln_compositor *c, id<MTLDevice> device) {
  NSError *error = nil;
  id<MTLLibrary> library =
      [device newLibraryWithSource:kShaderSource options:nil error:&error];
  if (!library) {
    set_error(c, [NSString stringWithFormat:@"newLibraryWithSource: %@",
                                            error.localizedDescription]);
    return nil;
  }
  id<MTLFunction> vfn = [library newFunctionWithName:@"vertex_main"];
  id<MTLFunction> ffn = [library newFunctionWithName:@"fragment_main"];
  if (!vfn || !ffn) {
    set_error(c, @"newFunctionWithName failed");
    return nil;
  }
  MTLRenderPipelineDescriptor *desc = [[MTLRenderPipelineDescriptor alloc] init];
  desc.vertexFunction = vfn;
  desc.fragmentFunction = ffn;
  desc.colorAttachments[0].pixelFormat = MTLPixelFormatBGRA8Unorm;
  id<MTLRenderPipelineState> pso =
      [device newRenderPipelineStateWithDescriptor:desc error:&error];
  if (!pso) {
    set_error(c, [NSString stringWithFormat:@"newRenderPipelineState: %@",
                                            error.localizedDescription]);
    return nil;
  }
  return pso;
}

mln_compositor *compositor_create(void *layer_ptr) {
  if (!layer_ptr) return NULL;
  @autoreleasepool {
    mln_compositor *c = (mln_compositor *)calloc(1, sizeof(*c));
    if (!c) return NULL;

    CAMetalLayer *layer = (__bridge CAMetalLayer *)layer_ptr;
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
      set_error(c, @"MTLCreateSystemDefaultDevice returned nil");
      compositor_destroy(c);
      return NULL;
    }
    layer.device = device;
    layer.pixelFormat = MTLPixelFormatBGRA8Unorm;

    id<MTLCommandQueue> queue = [device newCommandQueue];
    if (!queue) {
      set_error(c, @"newCommandQueue failed");
      compositor_destroy(c);
      return NULL;
    }
    id<MTLRenderPipelineState> pso = build_pipeline(c, device);
    if (!pso) {
      compositor_destroy(c);
      return NULL;
    }

    c->layer    = (__bridge_retained void *)layer;
    c->device   = (__bridge_retained void *)device;
    c->queue    = (__bridge_retained void *)queue;
    c->pipeline = (__bridge_retained void *)pso;
    return c;
  }
}

void *compositor_device(mln_compositor *c) {
  if (!c) return NULL;
  return c->device;
}

void compositor_resize(mln_compositor *c, double width, double height) {
  if (!c || !c->layer) return;
  @autoreleasepool {
    CAMetalLayer *layer = (__bridge CAMetalLayer *)c->layer;
    layer.drawableSize = CGSizeMake(width, height);
  }
}

int compositor_draw(mln_compositor *c, void *texture_ptr) {
  if (!c || !c->layer || !c->queue || !c->pipeline || !texture_ptr) {
    return -1;
  }
  @autoreleasepool {
    CAMetalLayer *layer = (__bridge CAMetalLayer *)c->layer;
    id<MTLCommandQueue> queue = (__bridge id<MTLCommandQueue>)c->queue;
    id<MTLRenderPipelineState> pso =
        (__bridge id<MTLRenderPipelineState>)c->pipeline;
    id<MTLTexture> srcTex = (__bridge id<MTLTexture>)texture_ptr;

    id<CAMetalDrawable> drawable = [layer nextDrawable];
    if (!drawable) {
      set_error(c, @"nextDrawable returned nil");
      return -2;
    }
    MTLRenderPassDescriptor *pass = [MTLRenderPassDescriptor renderPassDescriptor];
    pass.colorAttachments[0].texture     = drawable.texture;
    pass.colorAttachments[0].loadAction  = MTLLoadActionClear;
    pass.colorAttachments[0].storeAction = MTLStoreActionStore;
    pass.colorAttachments[0].clearColor  = MTLClearColorMake(0.08, 0.09, 0.11, 1.0);

    id<MTLCommandBuffer> cb = [queue commandBuffer];
    if (!cb) {
      set_error(c, @"commandBuffer returned nil");
      return -3;
    }
    id<MTLRenderCommandEncoder> enc = [cb renderCommandEncoderWithDescriptor:pass];
    [enc setRenderPipelineState:pso];
    [enc setFragmentTexture:srcTex atIndex:0];
    [enc drawPrimitives:MTLPrimitiveTypeTriangle vertexStart:0 vertexCount:6];
    [enc endEncoding];
    [cb presentDrawable:drawable];
    [cb commit];
    return 0;
  }
}

const char *compositor_last_error(mln_compositor *c) {
  if (!c) return "compositor is null";
  return c->last_error;
}

void compositor_destroy(mln_compositor *c) {
  if (!c) return;
  @autoreleasepool {
    if (c->pipeline) {
      CFRelease(c->pipeline);
      c->pipeline = NULL;
    }
    if (c->queue) {
      CFRelease(c->queue);
      c->queue = NULL;
    }
    if (c->device) {
      CFRelease(c->device);
      c->device = NULL;
    }
    if (c->layer) {
      CFRelease(c->layer);
      c->layer = NULL;
    }
  }
  free(c);
}
