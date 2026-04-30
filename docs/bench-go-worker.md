# Adding the Go worker to the rampardos bench harness

This is a handoff doc for whichever agent works in `rampardos-rust-poc`. It explains how to extend `rampardos-render-worker-rs/src/bin/bench.rs` and `scripts/backend-bench.sh` to also drive the Go-based worker (`maplibre-native-go/cmd/render-worker`), so a single bench run produces a three-way Node vs Rust vs Go comparison.

## What's already done in maplibre-native-go

The Go worker is a long-lived subprocess that speaks the **same** binary frame protocol as the Rust and Node workers:

```
| 1 byte type | 4 bytes BE uint32 length | length bytes payload |

  'H' handshake (worker -> orchestrator) JSON {pid, style}
  'R' request   (orchestrator -> worker) JSON {zoom, center: [lng, lat], width, height, bearing?, pitch?}
  'K' ok        (worker -> orchestrator) raw premultiplied RGBA bytes (width * height * 4 * ratio^2)
  'E' error     (worker -> orchestrator) UTF-8 error message
```

Source: `cmd/render-worker/main.go` in this repo. CLI args mirror `rampardos-render-worker-rs`:

```
render-worker
  --style-path <abs path to style.prepared.json>   [required]
  --style-id <string>                              [default "go", reported in handshake]
  --ratio <uint>                                   [default 1]
  --mbtiles <path>     # accepted for CLI compat, ignored
  --styles-dir <path>  # accepted for CLI compat, ignored
  --fonts-dir <path>   # accepted for CLI compat, ignored
  --load-timeout <duration>   [default 30s]
  --frame-timeout <duration>  [default 30s]
```

Build it with:

```bash
cd /path/to/maplibre-native-go
eval "$(make env)"
go build -o /tmp/render-worker-go ./cmd/render-worker
```

`make env` exports `PKG_CONFIG_PATH` (pointing at `$MLN_FFI_DIR/build/pkgconfig`) and `CGO_LDFLAGS` (rpath for the dylib). The binary is a normal cgo binary and links against `libmaplibre-native-c.{dylib,so}`.

The worker has been smoke-tested locally: 3 requests across 2 different viewport sizes, correct RGBA byte counts on each, clean EOF exit on stdin close. Resize-between-renders works without the per-size renderer caching the Rust worker uses (our binding's `RenderStillImageInto` drives an internal render loop that handles size changes).

## Extending the bench

Three small changes:

### 1. `bench.rs` — add `--go-binary` flag and a third worker pool

In `Args`:

```rust
/// Path to the Go render-worker binary. When omitted, the Go pool is
/// skipped and the bench falls back to Node vs Rust only.
#[arg(long)]
go_binary: Option<PathBuf>,
```

In `run()`, where the Node and Rust pools are constructed and exercised, add a third pool conditionally on `args.go_binary.is_some()`. Reuse the existing `Worker` abstraction — the protocol is identical, so the only differences are:

- Spawn command: `Command::new(go_binary).args([...same flags as the rust worker...])`.
- Label in CSV / stdout: `"go"`.

### 2. `Worker::spawn` (or wherever the spawn arg list lives)

The Go worker accepts the same `--style-path / --mbtiles / --styles-dir / --fonts-dir / --style-id / --ratio` flags. Pass them straight through; `--mbtiles` etc. are no-ops on the Go side but kept for symmetry.

### 3. `scripts/backend-bench.sh`

In the docker run step:

- Mount the maplibre-native-go checkout: `-v "/path/to/maplibre-native-go:/maplibre-native-go"`.
- Mount the maplibre-native-ffi build: `-v "/path/to/maplibre-native-ffi:/maplibre-native-ffi"`.
- Set `MLN_FFI_DIR=/maplibre-native-ffi` and add `eval "$(make env)" && go build ...` to the build phase, producing `/target-linux/release/render-worker-go`.
- Pass `--go-binary /target-linux/release/render-worker-go` to the bench invocation.

The Linux Vulkan path uses Mesa lavapipe out-of-the-box; the Docker image already installs `mesa-vulkan-drivers + libvulkan1` for the existing `BACKEND=vulkan` runs, which is the same stack the Go worker uses. Set `MLN_FFI_DIR` to a build directory that has `libmaplibre-native-c.so` produced via the upstream `mise run build` step.

## Equivalence verdict

The bench currently checks pixel-equivalence between Node and Rust (within a small tolerance accounting for Vulkan vs OpenGL backend differences). With the Go worker in the mix, the natural extension is:

- Compare Go output to **Rust Vulkan** byte-for-byte (both ride the same maplibre-native Vulkan path, same lavapipe ICD, same `Texture2D::readImage` pattern). They should be identical or off-by-one due to staging-buffer rounding.
- Compare Go output to **Node OpenGL** with the same divergence tolerance the existing Rust-Vulkan-vs-Node-OpenGL comparison uses (~2.8% byte-divergence in current measurements).

If pixel equivalence fails on the Go path, the most likely cause is alpha handling. The Go worker emits **premultiplied** RGBA (matching mbgl). Verify this matches what the Node worker emits (the Node binding may unpremultiply in its returned bytes — worth diffing a single-pixel case before concluding the Go output is wrong).

## What this unlocks

A single `./scripts/backend-bench.sh 1000` run on a machine with all three workers built will produce three latency distributions (p50/p95/p99) and three RSS-over-time curves on the same workload. That's the strongest data the user can show when deciding which binding to deploy in production.
