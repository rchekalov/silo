// SPDX-License-Identifier: Apache-2.0
//
// C header for libSiloBridge.dylib. The dylib is built from swift-bridge/ and
// exports these symbols via Swift @_cdecl. Keep these declarations in sync
// with swift-bridge/Sources/SiloBridge/Bridge.swift and Config.swift.

#ifndef SILO_BRIDGE_H
#define SILO_BRIDGE_H

#include <stdbool.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

// Opaque handles
typedef void* SBManagerHandle;
typedef void* SBContainerHandle;
typedef void* SBImageHandle;
typedef void* SBProcessHandle;

// Mount spec passed by pointer.
typedef struct {
    const char* type;                      // "block" or "share"
    const char* format;                    // "ext4" for block, NULL for share
    const char* source;
    const char* destination;
    const char* const* options;            // NULL-terminated array, or NULL
} SBMount;

// /etc/hosts entry.
typedef struct {
    const char* ip_address;
    const char* const* hostnames;          // NULL-terminated array
} SBHostEntry;

// Unix domain socket relay (host ↔ guest). Apple Containerization runs the
// vsock pump automatically when the container's `sockets:` config is set.
// Direction is implicit "into the guest" — silo's only use case (forwarding
// $SSH_AUTH_SOCK) doesn't need outOf.
typedef struct {
    const char* host_source;               // path to host socket (e.g., $SSH_AUTH_SOCK)
    const char* guest_destination;         // path inside the guest (e.g., /run/silo/ssh-agent.sock)
} SBUnixSocket;

// Container configuration.
typedef struct {
    int32_t cpus;
    uint64_t memory_bytes;
    const char* const* arguments;          // NULL-terminated argv
    const char* working_directory;         // NULL => "/"
    const char* const* env_vars;           // NULL-terminated "KEY=VALUE" array
    const SBMount* mounts;
    uint32_t mount_count;
    int32_t stdin_fd;
    int32_t stdout_fd;
    int32_t stderr_fd;
    bool use_terminal;
    bool enable_networking;
    const char* const* dns_nameservers;
    const SBHostEntry* host_entries;
    uint32_t host_entry_count;
    bool auto_inject_host_silo;
    const SBUnixSocket* sockets;
    uint32_t socket_count;
} SBContainerConfig;

// Exec configuration.
typedef struct {
    const char* const* arguments;
    const char* working_directory;
    int32_t stdin_fd;
    int32_t stdout_fd;
    int32_t stderr_fd;
    bool use_terminal;
} SBExecConfig;

// Callback typedefs
typedef void (*SBSimpleCallback)(void* context, const char* error);
typedef void (*SBHandleCallback)(void* context, void* handle, const char* error);
typedef void (*SBExitCallback)(void* context, int32_t exit_code, const char* error);

// Memory management
void sb_free_string(const char* str);
void sb_free_manager(SBManagerHandle handle);
void sb_free_container(SBContainerHandle handle);
void sb_free_image(SBImageHandle handle);
void sb_free_process(SBProcessHandle handle);

// Manager creation (synchronous)
void sb_manager_create(
    const char* kernel_path,
    const char* initfs_path,
    const char* root_path,
    bool enable_vmnet,
    SBManagerHandle* out_handle,
    const char** out_error);

// Container creation (async)
void sb_manager_create_container_from_ref(
    SBManagerHandle manager,
    const char* container_id,
    const char* reference,
    uint64_t rootfs_size_bytes,
    const SBContainerConfig* config,
    void* context,
    SBHandleCallback callback);

void sb_manager_create_container_from_image(
    SBManagerHandle manager,
    const char* container_id,
    SBImageHandle image,
    const SBMount* rootfs,
    const SBContainerConfig* config,
    void* context,
    SBHandleCallback callback);

// Container lifecycle (async)
void sb_container_create(SBContainerHandle handle, void* context, SBSimpleCallback callback);
void sb_container_start(SBContainerHandle handle, void* context, SBSimpleCallback callback);
void sb_container_stop(SBContainerHandle handle, void* context, SBSimpleCallback callback);
void sb_container_wait(SBContainerHandle handle, void* context, SBExitCallback callback);
void sb_container_resize(SBContainerHandle handle, uint16_t cols, uint16_t rows, void* context, SBSimpleCallback callback);

// Container info (sync). Returned strings are strdup'd — caller must sb_free_string.
const char* sb_container_get_vm_ip(SBContainerHandle handle);
const char* sb_container_get_gateway_ip(SBContainerHandle handle);

// Manager delete (sync, ignores errors)
void sb_manager_delete(SBManagerHandle handle, const char* container_id);

// Exec (async)
void sb_container_exec(
    SBContainerHandle handle,
    const char* process_id,
    const SBExecConfig* config,
    void* context,
    SBHandleCallback callback);
void sb_process_start(SBProcessHandle handle, void* context, SBSimpleCallback callback);
void sb_process_wait(SBProcessHandle handle, void* context, SBExitCallback callback);

// Image store (async; digest is sync)
void sb_image_store_get(
    SBManagerHandle manager,
    const char* reference,
    bool pull,
    void* context,
    SBHandleCallback callback);
void sb_image_store_pull(
    SBManagerHandle manager,
    const char* reference,
    void* context,
    SBSimpleCallback callback);
const char* sb_image_digest(SBImageHandle image);

// Image deletion + orphan-blob GC. The "orphans" entry points fire a
// SBSizeCallback: (context, freed_or_total_bytes, error).
typedef void (*SBSizeCallback)(void* context, uint64_t size, const char* error);
void sb_image_store_delete(
    SBManagerHandle manager,
    const char* reference,
    bool performCleanup,
    void* context,
    SBSimpleCallback callback);
void sb_image_store_cleanup_orphans(
    SBManagerHandle manager,
    void* context,
    SBSizeCallback callback);
void sb_image_store_orphans_size(
    SBManagerHandle manager,
    void* context,
    SBSizeCallback callback);

// Terminal size. Returns 0 on success, -1 on error.
int32_t sb_terminal_get_size(uint16_t* cols, uint16_t* rows);

// Forward declarations of Go-exported callback trampolines.
// These are defined via //export in callbacks.go. Signatures must match
// the cgo-emitted prototypes in _cgo_export.h (plain char*, not const char*).
extern void siloSimpleCallback(void* context, char* error);
extern void siloHandleCallback(void* context, void* handle, char* error);
extern void siloExitCallback(void* context, int32_t exit_code, char* error);
extern void siloSizeCallback(void* context, uint64_t size, char* error);

// Function-pointer casts hide the const/non-const mismatch between our SB*Callback
// typedefs (const char*) and the cgo-emitted exports (char*).
#define SILO_SIMPLE_CB ((SBSimpleCallback)siloSimpleCallback)
#define SILO_HANDLE_CB ((SBHandleCallback)siloHandleCallback)
#define SILO_EXIT_CB   ((SBExitCallback)siloExitCallback)
#define SILO_SIZE_CB   ((SBSizeCallback)siloSizeCallback)

// Tiny wrappers: Go can't pass Go function pointers as C callbacks, so we call
// these thin shims that hard-code the callback trampoline.

static inline void silo_container_create(SBContainerHandle h, void* ctx) {
    sb_container_create(h, ctx, SILO_SIMPLE_CB);
}
static inline void silo_container_start(SBContainerHandle h, void* ctx) {
    sb_container_start(h, ctx, SILO_SIMPLE_CB);
}
static inline void silo_container_stop(SBContainerHandle h, void* ctx) {
    sb_container_stop(h, ctx, SILO_SIMPLE_CB);
}
static inline void silo_container_wait(SBContainerHandle h, void* ctx) {
    sb_container_wait(h, ctx, SILO_EXIT_CB);
}
static inline void silo_container_resize(SBContainerHandle h, uint16_t cols, uint16_t rows, void* ctx) {
    sb_container_resize(h, cols, rows, ctx, SILO_SIMPLE_CB);
}
static inline void silo_container_exec(SBContainerHandle h, const char* pid, const SBExecConfig* cfg, void* ctx) {
    sb_container_exec(h, pid, cfg, ctx, SILO_HANDLE_CB);
}
static inline void silo_process_start(SBProcessHandle h, void* ctx) {
    sb_process_start(h, ctx, SILO_SIMPLE_CB);
}
static inline void silo_process_wait(SBProcessHandle h, void* ctx) {
    sb_process_wait(h, ctx, SILO_EXIT_CB);
}
static inline void silo_image_store_get(SBManagerHandle m, const char* ref, bool pull, void* ctx) {
    sb_image_store_get(m, ref, pull, ctx, SILO_HANDLE_CB);
}
static inline void silo_image_store_pull(SBManagerHandle m, const char* ref, void* ctx) {
    sb_image_store_pull(m, ref, ctx, SILO_SIMPLE_CB);
}
static inline void silo_image_store_delete(SBManagerHandle m, const char* ref, bool cleanup, void* ctx) {
    sb_image_store_delete(m, ref, cleanup, ctx, SILO_SIMPLE_CB);
}
static inline void silo_image_store_cleanup_orphans(SBManagerHandle m, void* ctx) {
    sb_image_store_cleanup_orphans(m, ctx, SILO_SIZE_CB);
}
static inline void silo_image_store_orphans_size(SBManagerHandle m, void* ctx) {
    sb_image_store_orphans_size(m, ctx, SILO_SIZE_CB);
}
static inline void silo_create_container_from_ref(
    SBManagerHandle m, const char* id, const char* ref, uint64_t rootfs_size,
    const SBContainerConfig* cfg, void* ctx) {
    sb_manager_create_container_from_ref(m, id, ref, rootfs_size, cfg, ctx, SILO_HANDLE_CB);
}
static inline void silo_create_container_from_image(
    SBManagerHandle m, const char* id, SBImageHandle image, const SBMount* rootfs,
    const SBContainerConfig* cfg, void* ctx) {
    sb_manager_create_container_from_image(m, id, image, rootfs, cfg, ctx, SILO_HANDLE_CB);
}

#ifdef __cplusplus
}
#endif

#endif // SILO_BRIDGE_H
