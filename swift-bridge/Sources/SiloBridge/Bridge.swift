// SPDX-License-Identifier: Apache-2.0

import Containerization
import ContainerizationOCI
import ContainerizationOS
import Foundation

// MARK: - Memory Management

@_cdecl("sb_free_string")
public func sb_free_string(_ str: UnsafePointer<CChar>?) {
    guard let str = str else { return }
    free(UnsafeMutablePointer(mutating: str))
}

@_cdecl("sb_free_manager")
public func sb_free_manager(_ handle: UnsafeMutableRawPointer?) {
    guard let handle = handle else { return }
    Unmanaged<ManagerBox>.fromOpaque(handle).release()
}

@_cdecl("sb_free_container")
public func sb_free_container(_ handle: UnsafeMutableRawPointer?) {
    guard let handle = handle else { return }
    Unmanaged<ContainerBox>.fromOpaque(handle).release()
}

@_cdecl("sb_free_image")
public func sb_free_image(_ handle: UnsafeMutableRawPointer?) {
    guard let handle = handle else { return }
    Unmanaged<ImageBox>.fromOpaque(handle).release()
}

@_cdecl("sb_free_process")
public func sb_free_process(_ handle: UnsafeMutableRawPointer?) {
    guard let handle = handle else { return }
    Unmanaged<ProcessBox>.fromOpaque(handle).release()
}

// MARK: - Helpers

private func makeError(_ message: String) -> UnsafePointer<CChar> {
    return UnsafePointer(strdup(message)!)
}

// MARK: - Manager Creation (Synchronous)
// Returns: handle via out-param, error string (null on success). Caller frees both.

@_cdecl("sb_manager_create")
public func sb_manager_create(
    _ kernelPath: UnsafePointer<CChar>,
    _ initfsPath: UnsafePointer<CChar>,
    _ rootPath: UnsafePointer<CChar>,
    _ enableVmnet: Bool,
    _ outHandle: UnsafeMutablePointer<UnsafeMutableRawPointer?>,
    _ outError: UnsafeMutablePointer<UnsafePointer<CChar>?>
) {
    do {
        let kernel = Kernel(
            path: URL(fileURLWithPath: String(cString: kernelPath)),
            platform: .linuxArm,
            commandline: Kernel.CommandLine(debug: false, panic: 0)
        )

        let initfs = Containerization.Mount.block(
            format: "ext4",
            source: String(cString: initfsPath),
            destination: "/",
            options: ["ro"]
        )

        let root = URL(fileURLWithPath: String(cString: rootPath))

        let manager: ContainerManager
        if #available(macOS 26.0, *), enableVmnet {
            let network = try? VmnetNetwork()
            manager = try ContainerManager(kernel: kernel, initfs: initfs, root: root, network: network)
        } else {
            manager = try ContainerManager(kernel: kernel, initfs: initfs, root: root)
        }

        let box_ = ManagerBox(manager)
        outHandle.pointee = Unmanaged.passRetained(box_).toOpaque()
        outError.pointee = nil
    } catch {
        outHandle.pointee = nil
        outError.pointee = makeError(String(describing: error))
    }
}

// MARK: - Container Creation (Async)
// Uses simple C types for @_cdecl compatibility.
// Config is passed as UnsafeRawPointer pointing to the C struct.

@_cdecl("sb_manager_create_container_from_ref")
public func sb_manager_create_container_from_ref(
    _ managerHandle: UnsafeMutableRawPointer,
    _ containerID: UnsafePointer<CChar>,
    _ reference: UnsafePointer<CChar>,
    _ rootfsSizeBytes: UInt64,
    _ configPtr: UnsafeRawPointer,
    _ context: UnsafeMutableRawPointer?,
    _ callback: @convention(c) @Sendable (UnsafeMutableRawPointer?, UnsafeMutableRawPointer?, UnsafePointer<CChar>?) -> Void
) {
    let manager = Unmanaged<ManagerBox>.fromOpaque(managerHandle).takeUnretainedValue()
    let id = String(cString: containerID)
    let ref = String(cString: reference)
    let config = configPtr.assumingMemoryBound(to: SBContainerConfig.self)
    // Copy config values we need before async boundary
    let cpus = Int(config.pointee.cpus)
    let memBytes = config.pointee.memory_bytes
    let args = readStringArray(config.pointee.arguments)
    let workDir = config.pointee.working_directory.map { String(cString: $0) } ?? "/"
    let envVars = readStringArray(config.pointee.env_vars)
    let useTerm = config.pointee.use_terminal
    let stdinFd = config.pointee.stdin_fd
    let stdoutFd = config.pointee.stdout_fd
    let stderrFd = config.pointee.stderr_fd
    let autoInject = config.pointee.auto_inject_host_silo
    let dnsServers = readStringArray(config.pointee.dns_nameservers)
    // Copy mounts
    var mountSpecs: [(type: String, format: String?, source: String, dest: String, opts: [String])] = []
    if let mountsPtr = config.pointee.mounts {
        for i in 0..<Int(config.pointee.mount_count) {
            let m = mountsPtr[i]
            let t = String(cString: m.type)
            let f = m.format.map { String(cString: $0) }
            let s = String(cString: m.source)
            let d = String(cString: m.destination)
            let o = readStringArray(m.options)
            mountSpecs.append((t, f, s, d, o))
        }
    }
    // Copy host entries
    var hostEntries: [(ip: String, names: [String])] = []
    if let entriesPtr = config.pointee.host_entries {
        for i in 0..<Int(config.pointee.host_entry_count) {
            let e = entriesPtr[i]
            let ip = String(cString: e.ip_address)
            let names = readStringArray(e.hostnames)
            hostEntries.append((ip, names))
        }
    }
    // Copy socket relays (host ↔ guest unix sockets, .into direction).
    var socketSpecs: [(source: String, dest: String)] = []
    if let socketsPtr = config.pointee.sockets {
        for i in 0..<Int(config.pointee.socket_count) {
            let s = socketsPtr[i]
            socketSpecs.append((String(cString: s.host_source), String(cString: s.guest_destination)))
        }
    }

    Task {
        do {
            var capturedVmIP: String?
            var capturedGatewayIP: String?
            let container = try await manager.inner.create(
                id, reference: ref, rootfsSizeInBytes: rootfsSizeBytes
            ) { swiftConfig in
                swiftConfig.cpus = cpus
                swiftConfig.memoryInBytes = memBytes
                swiftConfig.process.arguments = args
                swiftConfig.process.workingDirectory = workDir
                swiftConfig.process.environmentVariables = envVars

                for m in mountSpecs {
                    let mount: Containerization.Mount
                    if m.type == "block" {
                        let fmt = m.format ?? "ext4"
                        if m.opts.isEmpty {
                            mount = .block(format: fmt, source: m.source, destination: m.dest)
                        } else {
                            mount = .block(format: fmt, source: m.source, destination: m.dest, options: m.opts)
                        }
                    } else {
                        if m.opts.isEmpty {
                            mount = .share(source: m.source, destination: m.dest)
                        } else {
                            mount = .share(source: m.source, destination: m.dest, options: m.opts)
                        }
                    }
                    swiftConfig.mounts.append(mount)
                }

                for s in socketSpecs {
                    swiftConfig.sockets.append(
                        UnixSocketConfiguration(
                            source: URL(fileURLWithPath: s.source),
                            destination: URL(fileURLWithPath: s.dest),
                            direction: .into
                        )
                    )
                }

                if !dnsServers.isEmpty {
                    swiftConfig.dns = DNS(nameservers: dnsServers)
                }

                if !hostEntries.isEmpty {
                    var hosts = Hosts.default
                    for e in hostEntries {
                        hosts.entries.append(Hosts.Entry(ipAddress: e.ip, hostnames: e.names))
                    }
                    swiftConfig.hosts = hosts
                }

                if autoInject {
                    if let gateway = swiftConfig.interfaces.first?.ipv4Gateway {
                        var hosts = Hosts.default
                        hosts.entries.append(
                            Hosts.Entry(ipAddress: gateway.description, hostnames: ["host.silo.internal"])
                        )
                        swiftConfig.hosts = hosts
                        capturedGatewayIP = gateway.description
                    }
                }

                capturedVmIP = swiftConfig.interfaces.first?.ipv4Address.address.description

                if useTerm {
                    let terminal = try Terminal.current
                    try terminal.setraw()
                    swiftConfig.process.setTerminalIO(terminal: terminal)
                } else {
                    // Wire host-provided stdin FD (a pipe for `silo lsp`) so
                    // the container process actually receives the stream it
                    // expects. Without this branch, stdin is unbound and
                    // language servers see an immediate EOF and exit.
                    if stdinFd >= 0 {
                        swiftConfig.process.stdin = try Terminal(descriptor: stdinFd, setInitState: false)
                    }
                    if stdoutFd >= 0 {
                        swiftConfig.process.stdout = try Terminal(descriptor: stdoutFd, setInitState: false)
                    }
                    if stderrFd >= 0 {
                        swiftConfig.process.stderr = try Terminal(descriptor: stderrFd, setInitState: false)
                    }
                }
            }

            let box_ = ContainerBox(container)
            box_.vmIP = capturedVmIP
            box_.gatewayIP = capturedGatewayIP
            let handle = Unmanaged.passRetained(box_).toOpaque()
            callback(context, handle, nil)
        } catch {
            callback(context, nil, makeError(String(describing: error)))
        }
    }
}

@_cdecl("sb_manager_create_container_from_image")
public func sb_manager_create_container_from_image(
    _ managerHandle: UnsafeMutableRawPointer,
    _ containerID: UnsafePointer<CChar>,
    _ imageHandle: UnsafeMutableRawPointer,
    _ rootfsPtr: UnsafeRawPointer,
    _ configPtr: UnsafeRawPointer,
    _ context: UnsafeMutableRawPointer?,
    _ callback: @convention(c) @Sendable (UnsafeMutableRawPointer?, UnsafeMutableRawPointer?, UnsafePointer<CChar>?) -> Void
) {
    let manager = Unmanaged<ManagerBox>.fromOpaque(managerHandle).takeUnretainedValue()
    let imageBox = Unmanaged<ImageBox>.fromOpaque(imageHandle).takeUnretainedValue()
    let id = String(cString: containerID)
    let rootfsMountC = rootfsPtr.assumingMemoryBound(to: SBMount.self).pointee
    let rootfsMount = toSwiftMount(rootfsMountC)
    let config = configPtr.assumingMemoryBound(to: SBContainerConfig.self)

    // Copy config data before async boundary (same as above)
    let cpus = Int(config.pointee.cpus)
    let memBytes = config.pointee.memory_bytes
    let args = readStringArray(config.pointee.arguments)
    let workDir = config.pointee.working_directory.map { String(cString: $0) } ?? "/"
    let envVars = readStringArray(config.pointee.env_vars)
    let useTerm = config.pointee.use_terminal
    let stdinFd = config.pointee.stdin_fd
    let stdoutFd = config.pointee.stdout_fd
    let stderrFd = config.pointee.stderr_fd
    let autoInject = config.pointee.auto_inject_host_silo
    let dnsServers = readStringArray(config.pointee.dns_nameservers)
    var mountSpecs: [(type: String, format: String?, source: String, dest: String, opts: [String])] = []
    if let mountsPtr = config.pointee.mounts {
        for i in 0..<Int(config.pointee.mount_count) {
            let m = mountsPtr[i]
            mountSpecs.append((String(cString: m.type), m.format.map { String(cString: $0) }, String(cString: m.source), String(cString: m.destination), readStringArray(m.options)))
        }
    }
    var hostEntries: [(ip: String, names: [String])] = []
    if let entriesPtr = config.pointee.host_entries {
        for i in 0..<Int(config.pointee.host_entry_count) {
            let e = entriesPtr[i]
            hostEntries.append((String(cString: e.ip_address), readStringArray(e.hostnames)))
        }
    }
    var socketSpecs: [(source: String, dest: String)] = []
    if let socketsPtr = config.pointee.sockets {
        for i in 0..<Int(config.pointee.socket_count) {
            let s = socketsPtr[i]
            socketSpecs.append((String(cString: s.host_source), String(cString: s.guest_destination)))
        }
    }

    Task {
        do {
            var capturedVmIP: String?
            var capturedGatewayIP: String?
            let container = try await manager.inner.create(
                id, image: imageBox.inner, rootfs: rootfsMount
            ) { swiftConfig in
                swiftConfig.cpus = cpus
                swiftConfig.memoryInBytes = memBytes
                swiftConfig.process.arguments = args
                swiftConfig.process.workingDirectory = workDir
                swiftConfig.process.environmentVariables = envVars

                for m in mountSpecs {
                    let mount: Containerization.Mount
                    if m.type == "block" {
                        mount = m.opts.isEmpty ? .block(format: m.format ?? "ext4", source: m.source, destination: m.dest) : .block(format: m.format ?? "ext4", source: m.source, destination: m.dest, options: m.opts)
                    } else {
                        mount = m.opts.isEmpty ? .share(source: m.source, destination: m.dest) : .share(source: m.source, destination: m.dest, options: m.opts)
                    }
                    swiftConfig.mounts.append(mount)
                }

                for s in socketSpecs {
                    swiftConfig.sockets.append(
                        UnixSocketConfiguration(
                            source: URL(fileURLWithPath: s.source),
                            destination: URL(fileURLWithPath: s.dest),
                            direction: .into
                        )
                    )
                }

                if !dnsServers.isEmpty { swiftConfig.dns = DNS(nameservers: dnsServers) }
                if !hostEntries.isEmpty {
                    var hosts = Hosts.default
                    for e in hostEntries { hosts.entries.append(Hosts.Entry(ipAddress: e.ip, hostnames: e.names)) }
                    swiftConfig.hosts = hosts
                }
                if autoInject {
                    if let gateway = swiftConfig.interfaces.first?.ipv4Gateway {
                        var hosts = Hosts.default
                        hosts.entries.append(Hosts.Entry(ipAddress: gateway.description, hostnames: ["host.silo.internal"]))
                        swiftConfig.hosts = hosts
                        capturedGatewayIP = gateway.description
                    }
                }
                capturedVmIP = swiftConfig.interfaces.first?.ipv4Address.address.description

                if useTerm {
                    let terminal = try Terminal.current
                    try terminal.setraw()
                    swiftConfig.process.setTerminalIO(terminal: terminal)
                } else {
                    if stdinFd >= 0 { swiftConfig.process.stdin = try Terminal(descriptor: stdinFd, setInitState: false) }
                    if stdoutFd >= 0 { swiftConfig.process.stdout = try Terminal(descriptor: stdoutFd, setInitState: false) }
                    if stderrFd >= 0 { swiftConfig.process.stderr = try Terminal(descriptor: stderrFd, setInitState: false) }
                }
            }

            let box_ = ContainerBox(container)
            box_.vmIP = capturedVmIP
            box_.gatewayIP = capturedGatewayIP
            let handle = Unmanaged.passRetained(box_).toOpaque()
            callback(context, handle, nil)
        } catch {
            callback(context, nil, makeError(String(describing: error)))
        }
    }
}

// MARK: - Container Lifecycle (Async)
// Callbacks: (context, error_string_or_null)

@_cdecl("sb_container_create")
public func sb_container_create(
    _ handle: UnsafeMutableRawPointer,
    _ context: UnsafeMutableRawPointer?,
    _ callback: @convention(c) @Sendable (UnsafeMutableRawPointer?, UnsafePointer<CChar>?) -> Void
) {
    let box_ = Unmanaged<ContainerBox>.fromOpaque(handle).takeUnretainedValue()
    Task {
        do {
            try await box_.inner.create()
            callback(context, nil)
        } catch {
            callback(context, makeError(String(describing: error)))
        }
    }
}

@_cdecl("sb_container_start")
public func sb_container_start(
    _ handle: UnsafeMutableRawPointer,
    _ context: UnsafeMutableRawPointer?,
    _ callback: @convention(c) @Sendable (UnsafeMutableRawPointer?, UnsafePointer<CChar>?) -> Void
) {
    let box_ = Unmanaged<ContainerBox>.fromOpaque(handle).takeUnretainedValue()
    Task {
        do {
            try await box_.inner.start()
            callback(context, nil)
        } catch {
            callback(context, makeError(String(describing: error)))
        }
    }
}

@_cdecl("sb_container_stop")
public func sb_container_stop(
    _ handle: UnsafeMutableRawPointer,
    _ context: UnsafeMutableRawPointer?,
    _ callback: @convention(c) @Sendable (UnsafeMutableRawPointer?, UnsafePointer<CChar>?) -> Void
) {
    let box_ = Unmanaged<ContainerBox>.fromOpaque(handle).takeUnretainedValue()
    Task {
        do {
            try await box_.inner.stop()
            callback(context, nil)
        } catch {
            callback(context, makeError(String(describing: error)))
        }
    }
}

// Wait callback: (context, exit_code, error_or_null)
@_cdecl("sb_container_wait")
public func sb_container_wait(
    _ handle: UnsafeMutableRawPointer,
    _ context: UnsafeMutableRawPointer?,
    _ callback: @convention(c) @Sendable (UnsafeMutableRawPointer?, Int32, UnsafePointer<CChar>?) -> Void
) {
    let box_ = Unmanaged<ContainerBox>.fromOpaque(handle).takeUnretainedValue()
    Task {
        do {
            let exitStatus = try await box_.inner.wait()
            callback(context, exitStatus.exitCode, nil)
        } catch {
            callback(context, -1, makeError(String(describing: error)))
        }
    }
}

@_cdecl("sb_container_resize")
public func sb_container_resize(
    _ handle: UnsafeMutableRawPointer,
    _ cols: UInt16,
    _ rows: UInt16,
    _ context: UnsafeMutableRawPointer?,
    _ callback: @convention(c) @Sendable (UnsafeMutableRawPointer?, UnsafePointer<CChar>?) -> Void
) {
    let box_ = Unmanaged<ContainerBox>.fromOpaque(handle).takeUnretainedValue()
    Task {
        do {
            try await box_.inner.resize(to: Terminal.Size(width: cols, height: rows))
            callback(context, nil)
        } catch {
            callback(context, makeError(String(describing: error)))
        }
    }
}

// Returns VM/gateway IP as C strings (caller frees with sb_free_string)
@_cdecl("sb_container_get_vm_ip")
public func sb_container_get_vm_ip(_ handle: UnsafeMutableRawPointer) -> UnsafePointer<CChar>? {
    let box_ = Unmanaged<ContainerBox>.fromOpaque(handle).takeUnretainedValue()
    return box_.vmIP.map { UnsafePointer(strdup($0)!) }
}

@_cdecl("sb_container_get_gateway_ip")
public func sb_container_get_gateway_ip(_ handle: UnsafeMutableRawPointer) -> UnsafePointer<CChar>? {
    let box_ = Unmanaged<ContainerBox>.fromOpaque(handle).takeUnretainedValue()
    return box_.gatewayIP.map { UnsafePointer(strdup($0)!) }
}

// MARK: - Exec (Async)
// Exec callback: (context, process_handle_or_null, error_or_null)

@_cdecl("sb_container_exec")
public func sb_container_exec(
    _ handle: UnsafeMutableRawPointer,
    _ processID: UnsafePointer<CChar>,
    _ configPtr: UnsafeRawPointer,
    _ context: UnsafeMutableRawPointer?,
    _ callback: @convention(c) @Sendable (UnsafeMutableRawPointer?, UnsafeMutableRawPointer?, UnsafePointer<CChar>?) -> Void
) {
    let box_ = Unmanaged<ContainerBox>.fromOpaque(handle).takeUnretainedValue()
    let pid = String(cString: processID)
    let config = configPtr.assumingMemoryBound(to: SBExecConfig.self)
    let args = readStringArray(config.pointee.arguments)
    let workDir = config.pointee.working_directory.map { String(cString: $0) } ?? "/"
    let useTerm = config.pointee.use_terminal
    let stdinFd = config.pointee.stdin_fd
    let stdoutFd = config.pointee.stdout_fd
    let stderrFd = config.pointee.stderr_fd

    Task {
        do {
            var execConfig = LinuxProcessConfiguration()
            execConfig.arguments = args
            execConfig.workingDirectory = workDir
            if useTerm {
                let terminal = try Terminal.current
                execConfig.setTerminalIO(terminal: terminal)
            } else {
                if stdinFd >= 0 { execConfig.stdin = try Terminal(descriptor: stdinFd, setInitState: false) }
                if stdoutFd >= 0 { execConfig.stdout = try Terminal(descriptor: stdoutFd, setInitState: false) }
                if stderrFd >= 0 { execConfig.stderr = try Terminal(descriptor: stderrFd, setInitState: false) }
            }

            let process = try await box_.inner.exec(pid, configuration: execConfig)
            let processBox = ProcessBox(process)
            let processHandle = Unmanaged.passRetained(processBox).toOpaque()
            callback(context, processHandle, nil)
        } catch {
            callback(context, nil, makeError(String(describing: error)))
        }
    }
}

@_cdecl("sb_process_start")
public func sb_process_start(
    _ handle: UnsafeMutableRawPointer,
    _ context: UnsafeMutableRawPointer?,
    _ callback: @convention(c) @Sendable (UnsafeMutableRawPointer?, UnsafePointer<CChar>?) -> Void
) {
    let box_ = Unmanaged<ProcessBox>.fromOpaque(handle).takeUnretainedValue()
    Task {
        do {
            try await box_.inner.start()
            callback(context, nil)
        } catch {
            callback(context, makeError(String(describing: error)))
        }
    }
}

@_cdecl("sb_process_wait")
public func sb_process_wait(
    _ handle: UnsafeMutableRawPointer,
    _ context: UnsafeMutableRawPointer?,
    _ callback: @convention(c) @Sendable (UnsafeMutableRawPointer?, Int32, UnsafePointer<CChar>?) -> Void
) {
    let box_ = Unmanaged<ProcessBox>.fromOpaque(handle).takeUnretainedValue()
    Task {
        do {
            let exitStatus = try await box_.inner.wait()
            callback(context, exitStatus.exitCode, nil)
        } catch {
            callback(context, -1, makeError(String(describing: error)))
        }
    }
}

// MARK: - Manager Operations

@_cdecl("sb_manager_delete")
public func sb_manager_delete(
    _ handle: UnsafeMutableRawPointer,
    _ containerID: UnsafePointer<CChar>
) {
    let box_ = Unmanaged<ManagerBox>.fromOpaque(handle).takeUnretainedValue()
    let id = String(cString: containerID)
    try? box_.inner.delete(id)
}

// MARK: - Image Store
// Get callback: (context, image_handle_or_null, error_or_null)

@_cdecl("sb_image_store_get")
public func sb_image_store_get(
    _ managerHandle: UnsafeMutableRawPointer,
    _ reference: UnsafePointer<CChar>,
    _ pull: Bool,
    _ context: UnsafeMutableRawPointer?,
    _ callback: @convention(c) @Sendable (UnsafeMutableRawPointer?, UnsafeMutableRawPointer?, UnsafePointer<CChar>?) -> Void
) {
    let manager = Unmanaged<ManagerBox>.fromOpaque(managerHandle).takeUnretainedValue()
    let ref = String(cString: reference)

    Task {
        do {
            let image = try await manager.inner.imageStore.get(reference: ref, pull: pull)
            let box_ = ImageBox(image)
            let handle = Unmanaged.passRetained(box_).toOpaque()
            callback(context, handle, nil)
        } catch {
            callback(context, nil, makeError(String(describing: error)))
        }
    }
}

@_cdecl("sb_image_store_pull")
public func sb_image_store_pull(
    _ managerHandle: UnsafeMutableRawPointer,
    _ reference: UnsafePointer<CChar>,
    _ context: UnsafeMutableRawPointer?,
    _ callback: @convention(c) @Sendable (UnsafeMutableRawPointer?, UnsafePointer<CChar>?) -> Void
) {
    let manager = Unmanaged<ManagerBox>.fromOpaque(managerHandle).takeUnretainedValue()
    let ref = String(cString: reference)

    Task {
        do {
            _ = try await manager.inner.imageStore.pull(reference: ref)
            callback(context, nil)
        } catch {
            callback(context, makeError(String(describing: error)))
        }
    }
}

@_cdecl("sb_image_digest")
public func sb_image_digest(
    _ handle: UnsafeMutableRawPointer
) -> UnsafePointer<CChar>? {
    let box_ = Unmanaged<ImageBox>.fromOpaque(handle).takeUnretainedValue()
    return UnsafePointer(strdup(box_.inner.digest))
}

// Delete image: sb_image_store_delete(manager, reference, performCleanup, ctx, cb)
// cb(context, error_or_null)
@_cdecl("sb_image_store_delete")
public func sb_image_store_delete(
    _ managerHandle: UnsafeMutableRawPointer,
    _ reference: UnsafePointer<CChar>,
    _ performCleanup: Bool,
    _ context: UnsafeMutableRawPointer?,
    _ callback: @convention(c) @Sendable (UnsafeMutableRawPointer?, UnsafePointer<CChar>?) -> Void
) {
    let manager = Unmanaged<ManagerBox>.fromOpaque(managerHandle).takeUnretainedValue()
    let ref = String(cString: reference)

    Task {
        do {
            try await manager.inner.imageStore.delete(reference: ref, performCleanup: performCleanup)
            callback(context, nil)
        } catch {
            callback(context, makeError(String(describing: error)))
        }
    }
}

// Clean up orphaned layer blobs. cb(context, freedBytes, error_or_null)
@_cdecl("sb_image_store_cleanup_orphans")
public func sb_image_store_cleanup_orphans(
    _ managerHandle: UnsafeMutableRawPointer,
    _ context: UnsafeMutableRawPointer?,
    _ callback: @convention(c) @Sendable (UnsafeMutableRawPointer?, UInt64, UnsafePointer<CChar>?) -> Void
) {
    let manager = Unmanaged<ManagerBox>.fromOpaque(managerHandle).takeUnretainedValue()
    Task {
        do {
            let (_, freed) = try await manager.inner.imageStore.cleanUpOrphanedBlobs()
            callback(context, freed, nil)
        } catch {
            callback(context, 0, makeError(String(describing: error)))
        }
    }
}

// Reports the size (bytes) of blobs not referenced by any image. cb(context, bytes, error_or_null)
@_cdecl("sb_image_store_orphans_size")
public func sb_image_store_orphans_size(
    _ managerHandle: UnsafeMutableRawPointer,
    _ context: UnsafeMutableRawPointer?,
    _ callback: @convention(c) @Sendable (UnsafeMutableRawPointer?, UInt64, UnsafePointer<CChar>?) -> Void
) {
    let manager = Unmanaged<ManagerBox>.fromOpaque(managerHandle).takeUnretainedValue()
    Task {
        do {
            let size = try await manager.inner.imageStore.calculateOrphanedBlobsSize()
            callback(context, size, nil)
        } catch {
            callback(context, 0, makeError(String(describing: error)))
        }
    }
}

// MARK: - Terminal Helpers

@_cdecl("sb_terminal_get_size")
public func sb_terminal_get_size(
    _ cols: UnsafeMutablePointer<UInt16>,
    _ rows: UnsafeMutablePointer<UInt16>
) -> Int32 {
    do {
        let terminal = try Terminal.current
        let size = try terminal.size
        cols.pointee = UInt16(size.width)
        rows.pointee = UInt16(size.height)
        return 0
    } catch {
        return -1
    }
}
