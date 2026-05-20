// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "RepoView",
    platforms: [.macOS(.v13)],
    targets: [
        .executableTarget(
            name: "RepoView",
            path: "Sources/RepoView",
            exclude: ["Info.plist"]
        )
    ]
)
