// SPDX-License-Identifier: Apache-2.0

import Containerization
import ContainerizationOCI
import ContainerizationOS
import Foundation

/// Helpers to translate C structs into Swift Containerization types.

/// Read a NULL-terminated C string array into a Swift [String].
func readStringArray(_ ptr: UnsafePointer<UnsafePointer<CChar>?>?) -> [String] {
    guard let ptr = ptr else { return [] }
    var result: [String] = []
    var i = 0
    while let cstr = ptr[i] {
        result.append(String(cString: cstr))
        i += 1
    }
    return result
}

/// Convert an SBMount C struct to a Containerization.Mount.
func toSwiftMount(_ m: SBMount) -> Containerization.Mount {
    let source = String(cString: m.source)
    let destination = String(cString: m.destination)
    let options = readStringArray(m.options)

    let typeStr = String(cString: m.type)
    if typeStr == "block" {
        let format = m.format != nil ? String(cString: m.format!) : "ext4"
        if options.isEmpty {
            return .block(format: format, source: source, destination: destination)
        } else {
            return .block(format: format, source: source, destination: destination, options: options)
        }
    } else {
        // share
        if options.isEmpty {
            return .share(source: source, destination: destination)
        } else {
            return .share(source: source, destination: destination, options: options)
        }
    }
}

/// C struct definitions matching silo_bridge.h.
/// These are imported via the bridging mechanism.

struct SBMount {
    var type: UnsafePointer<CChar>
    var format: UnsafePointer<CChar>?
    var source: UnsafePointer<CChar>
    var destination: UnsafePointer<CChar>
    var options: UnsafePointer<UnsafePointer<CChar>?>?
}

struct SBHostEntry {
    var ip_address: UnsafePointer<CChar>
    var hostnames: UnsafePointer<UnsafePointer<CChar>?>
}

struct SBContainerConfig {
    var cpus: Int32
    var memory_bytes: UInt64
    var arguments: UnsafePointer<UnsafePointer<CChar>?>?
    var working_directory: UnsafePointer<CChar>?
    var env_vars: UnsafePointer<UnsafePointer<CChar>?>?
    var mounts: UnsafePointer<SBMount>?
    var mount_count: UInt32
    var stdin_fd: Int32
    var stdout_fd: Int32
    var stderr_fd: Int32
    var use_terminal: Bool
    var enable_networking: Bool
    var dns_nameservers: UnsafePointer<UnsafePointer<CChar>?>?
    var host_entries: UnsafePointer<SBHostEntry>?
    var host_entry_count: UInt32
    var auto_inject_host_silo: Bool
}

struct SBExecConfig {
    var arguments: UnsafePointer<UnsafePointer<CChar>?>?
    var working_directory: UnsafePointer<CChar>?
    var stdin_fd: Int32
    var stdout_fd: Int32
    var stderr_fd: Int32
    var use_terminal: Bool
}

struct SBResult {
    var code: Int32
    var message: UnsafePointer<CChar>?
}

struct SBManagerResult {
    var code: Int32
    var message: UnsafePointer<CChar>?
    var handle: UnsafeMutableRawPointer?
}

struct SBContainerResult {
    var code: Int32
    var message: UnsafePointer<CChar>?
    var handle: UnsafeMutableRawPointer?
}

struct SBImageResult {
    var code: Int32
    var message: UnsafePointer<CChar>?
    var handle: UnsafeMutableRawPointer?
}

struct SBProcessResult {
    var code: Int32
    var message: UnsafePointer<CChar>?
    var handle: UnsafeMutableRawPointer?
}

struct SBContainerInfo {
    var vm_ip: UnsafePointer<CChar>?
    var gateway_ip: UnsafePointer<CChar>?
}

/// Apply an SBContainerConfig to a LinuxContainer.Configuration.
/// Returns (vmIP, gatewayIP) captured from the config's network interfaces.
func applyConfig(
    _ config: UnsafePointer<SBContainerConfig>,
    to swiftConfig: inout LinuxContainer.Configuration
) throws -> (vmIP: String?, gatewayIP: String?) {
    let c = config.pointee

    swiftConfig.cpus = Int(c.cpus)
    swiftConfig.memoryInBytes = c.memory_bytes

    // Process arguments
    swiftConfig.process.arguments = readStringArray(c.arguments)

    // Working directory
    if let wd = c.working_directory {
        swiftConfig.process.workingDirectory = String(cString: wd)
    }

    // Environment variables
    swiftConfig.process.environmentVariables = readStringArray(c.env_vars)

    // Mounts
    if let mountsPtr = c.mounts, c.mount_count > 0 {
        for i in 0..<Int(c.mount_count) {
            let mount = toSwiftMount(mountsPtr[i])
            swiftConfig.mounts.append(mount)
        }
    }

    // DNS
    let nameservers = readStringArray(c.dns_nameservers)
    if !nameservers.isEmpty {
        swiftConfig.dns = DNS(nameservers: nameservers)
    }

    // Host entries
    if let entriesPtr = c.host_entries, c.host_entry_count > 0 {
        var hosts = Hosts.default
        for i in 0..<Int(c.host_entry_count) {
            let entry = entriesPtr[i]
            let ip = String(cString: entry.ip_address)
            let hostnames = readStringArray(entry.hostnames)
            hosts.entries.append(Hosts.Entry(ipAddress: ip, hostnames: hostnames))
        }
        swiftConfig.hosts = hosts
    }

    // Auto-inject host.silo.internal from gateway IP
    var vmIP: String?
    var gatewayIP: String?

    if c.auto_inject_host_silo {
        if let gateway = swiftConfig.interfaces.first?.ipv4Gateway {
            var hosts = Hosts.default
            hosts.entries.append(
                Hosts.Entry(ipAddress: gateway.description, hostnames: ["host.silo.internal"])
            )
            swiftConfig.hosts = hosts
            gatewayIP = gateway.description
        }
    }

    // Capture VM IP
    vmIP = swiftConfig.interfaces.first?.ipv4Address.address.description

    // Terminal IO
    if c.use_terminal {
        let terminal = try Terminal.current
        try terminal.setraw()
        swiftConfig.process.setTerminalIO(terminal: terminal)
    } else {
        // stdin is a ReaderStream; Terminal conforms to ReaderStream so the
        // same descriptor-wrapping pattern used for stdout/stderr applies.
        // Without this, the container's stdin is unbound and processes that
        // read from it (pyright-langserver, rust-analyzer) see an immediate
        // EOF and exit. Note: `applyConfig` in this file is not on the hot
        // path used by `silo lsp` — see the inline blocks in Bridge.swift's
        // sb_manager_create_container_from_ref / _from_image — but keeping
        // this in sync avoids future drift if callers switch to applyConfig.
        if c.stdin_fd >= 0 {
            swiftConfig.process.stdin = try Terminal(descriptor: c.stdin_fd, setInitState: false)
        }
        if c.stdout_fd >= 0 {
            swiftConfig.process.stdout = try Terminal(descriptor: c.stdout_fd, setInitState: false)
        }
        if c.stderr_fd >= 0 {
            swiftConfig.process.stderr = try Terminal(descriptor: c.stderr_fd, setInitState: false)
        }
    }

    return (vmIP, gatewayIP)
}

/// Apply an SBExecConfig to an ExecProcess.Configuration.
func applyExecConfig(
    _ config: UnsafePointer<SBExecConfig>,
    to swiftConfig: inout LinuxProcessConfiguration
) throws {
    let c = config.pointee

    swiftConfig.arguments = readStringArray(c.arguments)

    if let wd = c.working_directory {
        swiftConfig.workingDirectory = String(cString: wd)
    }

    if c.use_terminal {
        let terminal = try Terminal.current
        swiftConfig.setTerminalIO(terminal: terminal)
    } else {
        if c.stdin_fd >= 0 {
            swiftConfig.stdin = try Terminal(descriptor: c.stdin_fd, setInitState: false)
        }
        if c.stdout_fd >= 0 {
            swiftConfig.stdout = try Terminal(descriptor: c.stdout_fd, setInitState: false)
        }
        if c.stderr_fd >= 0 {
            swiftConfig.stderr = try Terminal(descriptor: c.stderr_fd, setInitState: false)
        }
    }
}
