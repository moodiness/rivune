// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "RivuneAPI",
    platforms: [
        .iOS(.v15),
        .macOS(.v12),
        .tvOS(.v15),
    ],
    products: [
        .library(name: "RivuneAPI", targets: ["RivuneAPI"]),
    ],
    targets: [
        .target(name: "RivuneAPI"),
    ]
)
