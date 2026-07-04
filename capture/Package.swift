// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "cast-capture",
    platforms: [.macOS(.v13)],
    targets: [
        .executableTarget(
            name: "cast-capture",
            path: "Sources/cast-capture"
        )
    ]
)
