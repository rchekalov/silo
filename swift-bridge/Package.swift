// swift-tools-version: 6.2

import PackageDescription

let package = Package(
    name: "SiloBridge",
    platforms: [
        .macOS(.v15),
    ],
    products: [
        .library(name: "SiloBridge", type: .dynamic, targets: ["SiloBridge"]),
    ],
    dependencies: [
        .package(url: "https://github.com/apple/containerization.git", from: "0.1.0"),
    ],
    targets: [
        .target(
            name: "SiloBridge",
            dependencies: [
                .product(name: "Containerization", package: "containerization"),
                .product(name: "ContainerizationOCI", package: "containerization"),
                .product(name: "ContainerizationOS", package: "containerization"),
                .product(name: "ContainerizationExtras", package: "containerization"),
            ],
            swiftSettings: [
                .swiftLanguageMode(.v5),
            ]
        ),
    ]
)
