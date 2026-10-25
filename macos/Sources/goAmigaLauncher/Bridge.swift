import Foundation

// Settings mirrors config.Settings (its JSON tags).
struct Settings: Codable, Equatable {
    var rom = ""
    var drives: [[String]] = [[], [], [], []]
    var numDrives = 0
    var hardfiles: [String] = []
    var dirs: [String] = []
    var ide: [String] = []
    var scsi: [String] = []
    var tape = ""
    var whdload = ""
    var gayle = "auto"
    var hdfCreate = 0
    var writeProtect = false

    var cpu = "68000"
    var cpuSpeed = 0
    var fpu = "auto"
    var chipset = ""
    var video = "pal"
    var chipRAM = 0
    var slowRAM = 0
    var fastRAM = 2048

    var motherboard = "none"
    var mbRAM = 16384
    var z3RAM = 0
    var cpuSlotRAM = 0
    var rtg = 0

    var serial = ""
    var bsdSocket = false
    var clock = true
    var clockY2K = false
    var a2065 = false
    var a2065Fwd = "2323:23"
    var joyKeys = false
    var joyAnalog = false

    var exactBus = true
    var clipboard = true

    var mute = false
    var overscan = false
    var scale = 2
    var statusBar = true
    var warp = false
    var screenshotDir = "."
    var stateDir = "."

    var hasMotherboard: Bool { motherboard != "none" && motherboard != "" }
}

struct ROMDescription: Codable, Equatable {
    var description: String?
    var error: String?
    var mismatch: String?
    var model: String?
    var version = 0
}

struct WHDLoadInfo: Codable, Equatable {
    var kicks: [String] = []
    var installed = false
    var home = ""
}

struct Request: Encodable {
    var op: String
    var name: String?
    var settings: Settings?
    var preset: String?
    var target: String?
    var path: String?
}

struct Response: Decodable {
    var ok = false
    var error: String?
    var status: String?
    var settings: Settings?
    var name: String?
    var saved: [String]?
    var presets: [String]?
    var rom: ROMDescription?
    var whdload: WHDLoadInfo?
    var config: String?
    var limits: [String: Int]?
}

// Bridge talks to goamiga --launcher-api: one JSON line out, one back.
final class Bridge {
    let goamiga: URL
    private var process: Process?
    private var input: FileHandle?
    private var output: FileHandle?
    private var buffer = Data()

    init() {
        if let env = ProcessInfo.processInfo.environment["GOAMIGA_BIN"] {
            goamiga = URL(fileURLWithPath: env)
        } else {
            goamiga = Bundle.main.executableURL!.deletingLastPathComponent().appendingPathComponent("goamiga")
        }
    }

    private func start() throws {
        let p = Process()
        p.executableURL = goamiga
        p.arguments = ["--launcher-api"]
        p.currentDirectoryURL = Bridge.workingDirectory
        let inPipe = Pipe(), outPipe = Pipe()
        p.standardInput = inPipe
        p.standardOutput = outPipe
        p.standardError = FileHandle.nullDevice
        try p.run()
        (process, input, output, buffer) = (p, inPipe.fileHandleForWriting, outPipe.fileHandleForReading, Data())
    }

    // The folder goamiga runs in: where the app was started, unless that is
    // the root (as from the Finder), then the home folder.
    static var workingDirectory: URL {
        let cwd = FileManager.default.currentDirectoryPath
        return cwd == "/" ? FileManager.default.homeDirectoryForCurrentUser : URL(fileURLWithPath: cwd)
    }

    func call(_ req: Request) -> Response {
        do {
            if process?.isRunning != true {
                try start()
            }
            var line = try JSONEncoder().encode(req)
            line.append(0x0A)
            try input!.write(contentsOf: line)
            while true {
                if let nl = buffer.firstIndex(of: 0x0A) {
                    let reply = buffer[buffer.startIndex..<nl]
                    buffer = Data(buffer[(nl + 1)...])
                    return try JSONDecoder().decode(Response.self, from: reply)
                }
                let more = output!.availableData // what has come, waiting for some
                guard !more.isEmpty else {
                    process = nil
                    return Response(error: "goamiga stopped answering")
                }
                buffer.append(more)
            }
        } catch {
            process = nil
            return Response(error: "goamiga (\(goamiga.path)): \(error.localizedDescription)")
        }
    }
}
