// SPDX-License-Identifier: Apache-2.0
//
// C-compatible interface to Apple Containerization via Swift.
// Consumed by silo-bridge-sys (Rust FFI bindings).

#ifndef SILO_BRIDGE_H
#define SILO_BRIDGE_H

#include <stdint.h>
#include <stdbool.h>

// ---------------------------------------------------------------------------
// Opaque handles — pointers to Swift-side heap objects
// ---------------------------------------------------------------------------

typedef void* SBManagerHandle;
typedef void* SBContainerHandle;
typedef void* SBImageHandle;
typedef void* SBProcessHandle;

// ---------------------------------------------------------------------------
// Result types
// ---------------------------------------------------------------------------

typedef struct {
    int32_t code;           // 0 = success, nonzero = error
    const char* message;    // null if no error; caller must free with sb_free_string()
} SBResult;

typedef struct {
    int32_t code;
    const char* message;
    SBManagerHandle handle; // valid only when code == 0
} SBManagerResult;

typedef struct {
    int32_t code;
    const char* message;
    SBContainerHandle handle;
} SBContainerResult;

typedef struct {
    int32_t code;
    const char* message;
    SBImageHandle handle;
} SBImageResult;

typedef struct {
    int32_t code;
    const char* message;
    SBProcessHandle handle;
} SBProcessResult;

// ---------------------------------------------------------------------------
// Callback types for async completion
// ---------------------------------------------------------------------------

typedef void (*SBCallback)(void* context, SBResult result);
typedef void (*SBManagerCallback)(void* context, SBManagerResult result);
typedef void (*SBContainerCallback)(void* context, SBContainerResult result);
typedef void (*SBImageCallback)(void* context, SBImageResult result);
typedef void (*SBProcessCallback)(void* context, SBProcessResult result);
typedef void (*SBExitCallback)(void* context, int32_t exit_code, const char* error);

// ---------------------------------------------------------------------------
// Mount specification
// ---------------------------------------------------------------------------

typedef struct {
    const char* type;           // "block" or "share"
    const char* format;         // "ext4" for block, NULL for share
    const char* source;
    const char* destination;
    const char* const* options; // NULL-terminated array, or NULL
} SBMount;

// ---------------------------------------------------------------------------
// Host entry for DNS/hosts configuration
// ---------------------------------------------------------------------------

typedef struct {
    const char* ip_address;
    const char* const* hostnames; // NULL-terminated array
} SBHostEntry;

// ---------------------------------------------------------------------------
// Container configuration (flat struct replaces Swift closure)
// ---------------------------------------------------------------------------

typedef struct {
    int32_t cpus;
    uint64_t memory_bytes;

    // Process config
    const char* const* arguments;       // NULL-terminated
    const char* working_directory;
    const char* const* env_vars;        // NULL-terminated, "KEY=VALUE" format

    // Mounts
    const SBMount* mounts;
    uint32_t mount_count;

    // Terminal/IO
    int32_t stdin_fd;       // -1 = not set
    int32_t stdout_fd;      // -1 = not set
    int32_t stderr_fd;      // -1 = not set
    bool use_terminal;      // true = use Terminal.current + setraw()

    // Networking
    bool enable_networking;

    // DNS
    const char* const* dns_nameservers; // NULL-terminated, or NULL

    // Host entries
    const SBHostEntry* host_entries;
    uint32_t host_entry_count;
    bool auto_inject_host_silo; // auto-inject host.silo.internal from gateway IP
} SBContainerConfig;

// ---------------------------------------------------------------------------
// Exec process configuration
// ---------------------------------------------------------------------------

typedef struct {
    const char* const* arguments;       // NULL-terminated
    const char* working_directory;
    int32_t stdin_fd;
    int32_t stdout_fd;
    int32_t stderr_fd;
    bool use_terminal;
} SBExecConfig;

// ---------------------------------------------------------------------------
// Info structs returned by queries
// ---------------------------------------------------------------------------

typedef struct {
    const char* vm_ip;      // Container VM IP (NULL if unavailable); free with sb_free_string
    const char* gateway_ip; // Gateway IP (NULL if unavailable); free with sb_free_string
} SBContainerInfo;

// ---------------------------------------------------------------------------
// Memory management
// ---------------------------------------------------------------------------

void sb_free_string(const char* str);
void sb_free_manager(SBManagerHandle handle);
void sb_free_container(SBContainerHandle handle);
void sb_free_image(SBImageHandle handle);
void sb_free_process(SBProcessHandle handle);

// ---------------------------------------------------------------------------
// Manager creation (synchronous)
// ---------------------------------------------------------------------------

SBManagerResult sb_manager_create(
    const char* kernel_path,
    const char* initfs_path,
    const char* root_path,
    bool enable_vmnet
);

// ---------------------------------------------------------------------------
// Container creation (async)
// ---------------------------------------------------------------------------

// From OCI reference string (slow, unpacks layers)
void sb_manager_create_container_from_ref(
    SBManagerHandle manager,
    const char* container_id,
    const char* reference,
    uint64_t rootfs_size_bytes,
    const SBContainerConfig* config,
    void* context,
    SBContainerCallback callback
);

// From Image handle + rootfs Mount (fast, pre-cached)
void sb_manager_create_container_from_image(
    SBManagerHandle manager,
    const char* container_id,
    SBImageHandle image,
    const SBMount* rootfs,
    const SBContainerConfig* config,
    void* context,
    SBContainerCallback callback
);

// ---------------------------------------------------------------------------
// Container lifecycle (async)
// ---------------------------------------------------------------------------

void sb_container_create(SBContainerHandle handle, void* context, SBCallback callback);
void sb_container_start(SBContainerHandle handle, void* context, SBCallback callback);
void sb_container_stop(SBContainerHandle handle, void* context, SBCallback callback);
void sb_container_wait(SBContainerHandle handle, void* context, SBExitCallback callback);
void sb_container_resize(SBContainerHandle handle, uint16_t cols, uint16_t rows,
                          void* context, SBCallback callback);

// Query container network info after creation (synchronous)
SBContainerInfo sb_container_info(SBContainerHandle handle);

// ---------------------------------------------------------------------------
// Exec (async)
// ---------------------------------------------------------------------------

void sb_container_exec(
    SBContainerHandle handle,
    const char* process_id,
    const SBExecConfig* config,
    void* context,
    SBProcessCallback callback
);

void sb_process_start(SBProcessHandle handle, void* context, SBCallback callback);
void sb_process_wait(SBProcessHandle handle, void* context, SBExitCallback callback);

// ---------------------------------------------------------------------------
// Manager operations
// ---------------------------------------------------------------------------

void sb_manager_delete(SBManagerHandle handle, const char* container_id);

// ---------------------------------------------------------------------------
// Image store
// ---------------------------------------------------------------------------

void sb_image_store_get(
    SBManagerHandle manager,
    const char* reference,
    bool pull,
    void* context,
    SBImageCallback callback
);

void sb_image_store_pull(
    SBManagerHandle manager,
    const char* reference,
    void* context,
    SBCallback callback
);

// Returns digest string; caller must free with sb_free_string()
const char* sb_image_digest(SBImageHandle image);

// ---------------------------------------------------------------------------
// Terminal helpers
// ---------------------------------------------------------------------------

int32_t sb_terminal_get_size(uint16_t* cols, uint16_t* rows);

#endif // SILO_BRIDGE_H
