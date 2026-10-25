import AppKit
import Observation

// The configuration screen's state. Everything about settings files,
// presets and Kickstart ROMs is asked of goamiga (Bridge); this keeps what
// is on screen and starts the machine.
@MainActor @Observable
final class LauncherModel {
    var settings = Settings() {
        didSet { if settings != oldValue { settingsChanged(from: oldValue) } }
    }
    var name = ""
    var saved: [String] = []
    var selection: String?
    var status = ""
    var statusIsError = false
    var tab = Tab.machine

    var presets: [String] = []
    var limits: [String: Int] = [:]
    var rom = ROMDescription()
    var whdload = WHDLoadInfo()

    var running = false
    private var machine: Process?
    private var machineOutput: [String] = []
    private var hidden: [NSWindow] = [] // the windows hidden while it runs

    let bridge = Bridge()

    enum Tab: String, CaseIterable, Identifiable {
        case machine = "Machine", memory = "Memory", floppies = "Floppies", hardDisks = "Hard disks"
        case whdload = "WHDLoad", ports = "Ports", display = "Display"
        var id: String { rawValue }
    }

    init() {
        let info = bridge.call(Request(op: "info"))
        (presets, limits) = (info.presets ?? [], info.limits ?? [:])
        if let e = info.error {
            say(e, error: true)
        }
        let r = bridge.call(Request(op: "startup"))
        if r.ok {
            apply(r)
        }
        refreshROM()
    }

    func say(_ message: String, error: Bool = false) {
        (status, statusIsError) = (message, error)
    }

    // apply takes a response's settings, name, saved list and status.
    private func apply(_ r: Response) {
        if let s = r.settings {
            settings = s
        }
        if let n = r.name {
            name = n
        }
        if let s = r.saved {
            saved = s
        }
        selection = saved.contains(name) ? name : nil
        if let e = r.error {
            say(e, error: true)
        } else if let s = r.status {
            say(s)
        }
    }

    private func settingsChanged(from old: Settings) {
        if settings.rom != old.rom || settings.cpu != old.cpu || settings.motherboard != old.motherboard {
            refreshROM()
        }
    }

    func refreshROM() {
        rom = bridge.call(Request(op: "rom", settings: settings)).rom ?? ROMDescription()
        whdload = bridge.call(Request(op: "whdload", settings: settings)).whdload ?? WHDLoadInfo()
    }

    // MARK: Saved configurations

    func load(_ n: String) {
        guard n != name || !saved.contains(n) else { return }
        apply(bridge.call(Request(op: "load", name: n)))
    }

    func save() {
        let r = bridge.call(Request(op: "save", name: name, settings: settings))
        apply(r)
    }

    func newConfiguration() {
        let r = bridge.call(Request(op: "defaults"))
        if let s = r.settings {
            settings = s
        }
        name = ""
        selection = nil
        say("New configuration: the default A500 settings")
    }

    func delete(_ n: String) {
        let r = bridge.call(Request(op: "delete", name: n))
        if r.ok, name == n {
            name = ""
        }
        apply(r)
    }

    func applyPreset(_ p: String) {
        apply(bridge.call(Request(op: "preset", settings: settings, preset: p)))
    }

    // choose puts a file or folder where target says (applyChoice in Go).
    func choose(_ target: String, _ url: URL) {
        apply(bridge.call(Request(op: "choose", settings: settings, target: target, path: url.path)))
    }

    // MARK: Running the machine

    func start() {
        guard !running else { return }
        let r = bridge.call(Request(op: "start", name: name, settings: settings))
        guard r.ok, let conf = r.config else {
            say(r.error ?? "Can't start", error: true)
            if rom.mismatch != nil || settings.rom.isEmpty {
                tab = .machine
            }
            return
        }
        let back = URL(fileURLWithPath: conf).deletingLastPathComponent().appendingPathComponent("return.conf")
        try? FileManager.default.removeItem(at: back)

        let p = Process()
        p.executableURL = bridge.goamiga
        p.arguments = ["--config", conf] + (name.isEmpty ? [] : ["--name", name])
        p.currentDirectoryURL = Bridge.workingDirectory
        var env = ProcessInfo.processInfo.environment
        env["GOAMIGA_RETURN_CONFIG"] = back.path
        p.environment = env
        let pipe = Pipe()
        p.standardOutput = pipe
        p.standardError = pipe
        machineOutput = []
        pipe.fileHandleForReading.readabilityHandler = { [weak self] h in
            let data = h.availableData
            guard !data.isEmpty, let text = String(data: data, encoding: .utf8) else { return }
            FileHandle.standardOutput.write(data) // still on the terminal, if any
            Task { @MainActor in self?.keepOutput(text) }
        }
        p.terminationHandler = { [weak self] p in
            let code = p.terminationStatus
            Task { @MainActor in self?.machineEnded(code, returnFile: back) }
        }
        do {
            try p.run()
        } catch {
            say("Can't run goamiga: \(error.localizedDescription)", error: true)
            return
        }
        (machine, running) = (p, true)
        // The machine's window is the app now: the configuration screen
        // hides, and its Dock icon with it.
        hidden = NSApp.windows.filter(\.isVisible)
        for w in hidden { w.orderOut(nil) }
        NSApp.setActivationPolicy(.accessory)
    }

    private func keepOutput(_ text: String) {
        machineOutput.append(contentsOf: text.split(separator: "\n").map(String.init))
        if machineOutput.count > 50 {
            machineOutput.removeFirst(machineOutput.count - 50)
        }
    }

    private func machineEnded(_ code: Int32, returnFile: URL) {
        (machine, running) = (nil, false)
        switch code {
        case 0:
            NSApp.terminate(nil) // closing the machine's window quits goAmiga
            return
        case 3: // Config (Cmd+,): the settings come back, as they are now
            let r = bridge.call(Request(op: "loadFile", path: returnFile.path))
            if let s = r.settings {
                settings = s
            }
            say("The machine has been switched off. Change the settings and Start it again.")
        default:
            let err = machineOutput.last(where: { $0.hasPrefix("Error:") || $0.contains("error") }) ?? machineOutput.last
            say("The machine stopped (exit \(code))" + (err.map { ": " + $0 } ?? ""), error: true)
        }
        NSApp.setActivationPolicy(.regular)
        for w in hidden { w.makeKeyAndOrderFront(nil) }
        hidden = []
        NSApp.activate(ignoringOtherApps: true)
    }

    func stopMachine() {
        machine?.terminate()
    }
}
