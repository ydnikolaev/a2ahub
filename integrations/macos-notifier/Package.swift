// swift-tools-version: 6.0

import PackageDescription

let package = Package(
    name: "A2ANotifier",
    platforms: [
        .macOS(.v13),
    ],
    products: [
        .library(name: "A2ANotifierCore", targets: ["A2ANotifierCore"]),
        .executable(name: "A2ANotifier", targets: ["A2ANotifier"]),
    ],
    targets: [
        .target(name: "A2ANotifierCore"),
        .executableTarget(
            name: "A2ANotifier",
            dependencies: ["A2ANotifierCore"]
        ),
        .testTarget(
            name: "A2ANotifierCoreTests",
            dependencies: ["A2ANotifierCore"]
        ),
    ],
    swiftLanguageModes: [.v5]
)
