// SPDX-License-Identifier: Apache-2.0

import Containerization
import ContainerizationOCI
import ContainerizationOS
import Foundation

/// Reference-counted wrappers for Containerization types.
/// These are retained/released across the FFI boundary via Unmanaged.

final class ManagerBox: @unchecked Sendable {
    var inner: ContainerManager
    init(_ manager: ContainerManager) { self.inner = manager }
}

final class ContainerBox: @unchecked Sendable {
    let inner: LinuxContainer
    /// Network info captured at creation time
    var vmIP: String?
    var gatewayIP: String?
    init(_ container: LinuxContainer) { self.inner = container }
}

final class ImageBox: @unchecked Sendable {
    let inner: Containerization.Image
    init(_ image: Containerization.Image) { self.inner = image }
}

final class ProcessBox: @unchecked Sendable {
    let inner: LinuxProcess
    init(_ process: LinuxProcess) { self.inner = process }
}
