// swift-tools-version:5.10
// goAmiga's native configuration screen for macOS. scripts/macos-app.sh
// builds it as goAmiga.app's executable, with the goamiga binary beside
// it; it asks goamiga --launcher-api for everything about settings and
// starts the machine as a goamiga run.
import PackageDescription

let package = Package(
    name: "goAmigaLauncher",
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(name: "goAmigaLauncher", path: "Sources/goAmigaLauncher")
    ]
)
