// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "RivuneAPI",
    platforms: [
        .iOS(.v15),
        .macOS(.v12),
        .tvOS(.v15),
        .visionOS(.v1),
    ],
    products: [
        .library(name: "RivuneAPI", targets: ["RivuneAPI"]),
        .library(name: "RivuneAppCore", targets: ["RivuneAppCore"]),
    ],
    dependencies: [
        .package(name: "MPVKitBinary", path: "MPVKitBinary"),
    ],
    targets: [
        .target(name: "RivuneAPI"),
        .target(
            name: "RivuneAppCore",
            dependencies: [
                "RivuneAPI",
                .product(name: "MPVKit", package: "MPVKitBinary"),
            ],
            resources: [.process("Resources")]
        ),
        .testTarget(name: "RivuneAPITests", dependencies: ["RivuneAPI"]),
        .testTarget(name: "RivuneAppCoreTests", dependencies: ["RivuneAppCore", "RivuneAPI"]),
    ]
)
