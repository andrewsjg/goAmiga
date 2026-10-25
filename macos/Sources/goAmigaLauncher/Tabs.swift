import SwiftUI

// The settings tabs, as in the Ebitengine launcher (cmd/goamiga/launcher.go).

struct MachineTab: View {
    @Environment(LauncherModel.self) private var model

    var body: some View {
        @Bindable var model = model
        Form {
            Section {
                LabeledContent("Model") {
                    HStack {
                        ForEach(model.presets, id: \.self) { p in
                            Button(p) { model.applyPreset(p) }
                        }
                    }
                }
                FileRow(title: "Kickstart ROM", target: "rom", path: model.settings.rom, types: romTypes) {
                    model.settings.rom = ""
                }
                if let e = model.rom.error {
                    Warning(e)
                } else if let m = model.rom.mismatch {
                    HStack {
                        Warning(m)
                        if let preset = model.rom.model {
                            Button("Use \(preset) settings") { model.applyPreset(preset) }
                        }
                    }
                } else if let d = model.rom.description {
                    Note(d)
                }
            }
            Section {
                Choice(title: "CPU", value: $model.settings.cpu, options: [
                    ("68000 (A500, A600)", "68000"), ("68020 (A1200)", "68020"),
                    ("68030 (A3000)", "68030"), ("68040 (A4000)", "68040"),
                ])
                Choice(title: "CPU speed", value: $model.settings.cpuSpeed, options: [
                    ("Model's own", 0), ("100% (7 MHz)", 100), ("150%", 150), ("200%", 200),
                    ("250% (A1200)", 250), ("300%", 300), ("400%", 400), ("800%", 800),
                    ("1000% (A4000)", 1000), ("1600%", 1600),
                ], other: { "\($0)%" })
                Toggle("Exact bus timing (68000)", isOn: $model.settings.exactBus)
                    .help("A 68000 at 7 MHz has its chip accesses timed one by one against the Copper and DMA; off is faster but rougher")
                Choice(title: "FPU", value: $model.settings.fpu, options: [
                    ("Auto", "auto"), ("None", "none"), ("68881", "68881"), ("68882", "68882"),
                ])
                if model.settings.fpu != "none" && model.settings.fpu != "auto" && model.settings.cpu == "68000" {
                    Warning("An FPU needs a 68020 or 68030")
                } else if model.settings.cpu == "68040" {
                    Note(model.settings.fpu == "none" ? "68LC040: no FPU" : "The 68040's own FPU")
                }
            }
            Section {
                Choice(title: "Chipset", value: $model.settings.chipset, options: [
                    ("Automatic", ""), ("OCS (A500)", "ocs"), ("ECS Agnus (A500 1 MB)", "ecs-agnus"),
                    ("ECS (A500+, A600)", "ecs"), ("AGA (A1200)", "aga"),
                ])
                Choice(title: "Video", value: $model.settings.video, options: [
                    ("PAL (50 Hz)", "pal"), ("NTSC (60 Hz)", "ntsc"),
                ])
                Choice(title: "Gayle (IDE)", value: $model.settings.gayle, options: [
                    ("Automatic (with AGA)", "auto"), ("Fitted (A600, A1200)", "on"), ("Not fitted", "off"),
                ])
                Choice(title: "Motherboard", value: $model.settings.motherboard, options: [
                    ("None", "none"), ("A3000 (Ramsey, 32-bit RAM)", "a3000"), ("A4000 (with its IDE)", "a4000"),
                ])
                if model.settings.hasMotherboard && (model.settings.cpu == "68000" || model.settings.cpu == "68020") {
                    Warning("The motherboard needs a 68030 or 68040")
                }
            }
        }
        .formStyle(.grouped)
    }
}

struct MemoryTab: View {
    @Environment(LauncherModel.self) private var model

    var body: some View {
        @Bindable var model = model
        let s = model.settings
        Form {
            Section {
                Choice(title: "Chip RAM", value: $model.settings.chipRAM, options: [
                    ("Automatic", 0), ("512 KB", 512), ("1 MB", 1024), ("2 MB (AGA)", 2048),
                ], other: { "\($0) KB" })
                Choice(title: "Slow RAM", value: $model.settings.slowRAM, options: [
                    ("None", 0), ("512 KB", 512), ("1 MB", 1024), ("1.5 MB", 1536),
                ], other: { "\($0) KB" })
                Choice(title: "Fast RAM (Zorro II)", value: $model.settings.fastRAM, options: [
                    ("None", 0), ("64 KB", 64), ("128 KB", 128), ("256 KB", 256), ("512 KB", 512),
                    ("1 MB", 1024), ("2 MB", 2048), ("4 MB", 4096), ("8 MB", 8192),
                ], other: { "\($0) KB" })
            }
            if s.hasMotherboard {
                Section("Motherboard") {
                    Choice(title: "Motherboard RAM", value: $model.settings.mbRAM, options: [
                        ("None", 0), ("1 MB", 1024), ("2 MB", 2048), ("4 MB", 4096), ("8 MB", 8192), ("16 MB", 16384),
                    ], other: { "\($0) KB" })
                    Choice(title: "CPU-slot RAM", value: $model.settings.cpuSlotRAM, options: [
                        ("None", 0), ("16 MB", 16), ("32 MB", 32), ("64 MB", 64), ("128 MB", 128),
                    ], other: { "\($0) MB" })
                    Choice(title: "Zorro III RAM", value: $model.settings.z3RAM, options: [
                        ("None", 0), ("16 MB", 16), ("32 MB", 32), ("64 MB", 64), ("128 MB", 128), ("256 MB", 256),
                    ], other: { "\($0) MB" })
                }
            }
            Section {
                if s.hasMotherboard {
                    Choice(title: "RTG card", value: $model.settings.rtg, options: [
                        ("None", 0), ("16 MB (Zorro III)", 16), ("32 MB (Zorro III)", 32), ("64 MB (Zorro III)", 64),
                    ], other: { "\($0) MB" })
                } else {
                    Choice(title: "RTG card", value: $model.settings.rtg, options: [
                        ("None", 0), ("1 MB (Zorro II)", 1), ("2 MB (Zorro II)", 2), ("4 MB (Zorro II)", 4),
                    ], other: { "\($0) MB" })
                    if s.rtg > 0 && s.fastRAM + s.rtg * 1024 > 8192 {
                        Warning("Too much Zorro II memory with the fast RAM (8 MB in all)")
                    }
                }
            } footer: {
                Note("Chip RAM is shared with the custom chips; slow RAM is the A500 trapdoor expansion at $C00000; fast RAM is a Zorro II board. Some old games refuse to run with fast RAM under Kickstart 1.3 — turn it off for them. The RTG card is the uaegfx graphics card for Picasso96: install Picasso96 with its uaegfx driver.")
            }
        }
        .formStyle(.grouped)
        .onChange(of: model.settings.motherboard) { _, _ in
            // The RTG card is on Zorro III with a motherboard, Zorro II without.
            if model.settings.hasMotherboard && model.settings.rtg > 0 && model.settings.rtg < 16 {
                model.settings.rtg = 16
            } else if !model.settings.hasMotherboard && model.settings.rtg > 4 {
                model.settings.rtg = 4
            }
        }
    }
}

struct FloppyTab: View {
    @Environment(LauncherModel.self) private var model

    var body: some View {
        @Bindable var model = model
        Form {
            Section {
                ForEach(0..<4, id: \.self) { n in
                    let disks = model.settings.drives[n]
                    FileRow(title: "DF\(n):", target: "df\(n)", path: disks.first ?? "", types: diskTypes) {
                        model.settings.drives[n] = []
                    }
                    if disks.count > 1 {
                        Note("and \(disks.count - 1) more disk(s) for Cmd+F11")
                    }
                }
            }
            Section {
                Choice(title: "Drives", value: $model.settings.numDrives, options: [
                    ("As many as have disks", 0), ("1 (DF0:)", 1), ("2", 2), ("3", 3), ("4", 4),
                ])
                Toggle("Write-protect all disks (the image files are never changed)", isOn: $model.settings.writeProtect)
            } footer: {
                Note("Drop a disk image on a drive to put it in. A zip of several disks inserts the boot disk; the rest go to Cmd+F11.")
            }
        }
        .formStyle(.grouped)
    }
}

struct HardDiskTab: View {
    @Environment(LauncherModel.self) private var model

    // A drive in the list: a hardfile, a host folder or a SCSI disk.
    struct Drive: Identifiable {
        let id: String
        let kind: String
        let path: String
        let remove: (inout Settings) -> Void
    }

    var drives: [Drive] {
        let s = model.settings
        var d: [Drive] = []
        for (i, f) in s.hardfiles.enumerated() {
            d.append(Drive(id: "hdf\(i)", kind: "Unit \(i)", path: f) { $0.hardfiles.remove(at: i) })
        }
        for (i, v) in s.dirs.enumerated() {
            var vol = "", p = v
            if let eq = v.firstIndex(of: "="), v.distance(from: v.startIndex, to: eq) <= 30,
               !v[..<eq].contains(where: { "/\\:".contains($0) }), eq != v.startIndex {
                vol = String(v[..<eq])
                p = String(v[v.index(after: eq)...])
            }
            if vol.isEmpty { vol = URL(fileURLWithPath: p).lastPathComponent }
            d.append(Drive(id: "dir\(i)", kind: "Folder \(vol)", path: p) { $0.dirs.remove(at: i) })
        }
        for (i, v) in s.scsi.enumerated() {
            let parts = v.split(separator: ":", maxSplits: 1).map(String.init)
            let kind = parts.count == 2 ? "SCSI ID \(parts[0])" : "SCSI"
            d.append(Drive(id: "scsi\(i)", kind: kind, path: parts.last ?? v) { $0.scsi.remove(at: i) })
        }
        return d
    }

    var body: some View {
        let s = model.settings
        let partitions = s.hardfiles.count + s.dirs.count
        Form {
            Section {
                FileRow(title: "IDE master", target: "ide0", path: s.ide.first ?? "", types: hdfTypes) {
                    model.settings.ide = []
                }
                if !s.ide.isEmpty {
                    FileRow(title: "IDE slave", target: "ide1", path: s.ide.count > 1 ? s.ide[1] : "", types: hdfTypes) {
                        model.settings.ide = Array(model.settings.ide.prefix(1))
                    }
                }
                FileRow(title: "Tape (SCSI 4)", target: "tape", path: s.tape.count > 2 ? String(s.tape.dropFirst(2)) : "",
                        folder: true) {
                    model.settings.tape = ""
                }
            } footer: {
                Note("IDE drives need a Rigid Disk Block; they go on Gayle, or the A4000's own interface.")
            }
            Section("Hardfiles, host folders and SCSI disks") {
                if drives.isEmpty {
                    Note("None. Add them below, or drop hardfiles and folders here.")
                }
                ForEach(drives) { d in
                    LabeledContent(d.kind) {
                        HStack {
                            Text(d.path).lineLimit(1).truncationMode(.middle)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .help(d.path)
                            Button("Remove") { d.remove(&model.settings) }
                        }
                    }
                }
                HStack {
                    Button("Add Hardfile…") { add("hdf", folder: false) }
                        .disabled(s.hardfiles.count >= (model.limits["hardfiles"] ?? 28) || partitions >= 64)
                    Button("Add Folder…") { add("dir", folder: true) }
                        .disabled(partitions >= 64)
                    Button("Add SCSI Disk…") { add("scsi", folder: false) }
                        .disabled(s.scsi.count >= 7)
                }
            }
            .dropDestination(for: URL.self) { urls, _ in
                for url in urls {
                    let isDir = (try? url.resourceValues(forKeys: [.isDirectoryKey]).isDirectory) ?? false
                    model.choose(isDir ? "dir" : "hdf", url)
                }
                return !urls.isEmpty
            }
            Section {
            } footer: {
                Note("Plain hardfiles and RDB images both work. A new, empty hardfile can be made with --hdf-create from the command line and formatted from Workbench. A folder appears as a volume named after it, and boots if it has S/Startup-Sequence. SCSI disks go on an A2091 for Amiga Unix (the first at ID 6, where it looks for its root); AmigaOS sees them too.")
            }
        }
        .formStyle(.grouped)
    }

    private func add(_ target: String, folder: Bool) {
        let title = ["hdf": "Choose a hardfile", "dir": "Choose a folder to use as an Amiga drive",
                     "scsi": "Choose a hardfile for the SCSI disk"][target]!
        if let url = openPanel(title: title, folder: folder, types: folder ? [] : hdfTypes) {
            model.choose(target, url)
        }
    }
}

struct WHDLoadTab: View {
    @Environment(LauncherModel.self) private var model

    var body: some View {
        let s = model.settings
        let w = model.whdload
        Form {
            Section {
                LabeledContent("Game") {
                    HStack {
                        Text(s.whdload.isEmpty ? "None: the machine boots as usual" : s.whdload)
                            .foregroundStyle(s.whdload.isEmpty ? .secondary : .primary)
                            .lineLimit(1).truncationMode(.middle)
                            .frame(maxWidth: .infinity, alignment: .leading)
                        Button("Archive…") {
                            if let url = openPanel(title: "Choose a WHDLoad game (.lha or .slave)", folder: false, types: whdloadTypes, start: s.whdload) {
                                model.choose("whdload", url)
                            }
                        }
                        Button("Folder…") {
                            if let url = openPanel(title: "Choose a WHDLoad game's folder", folder: true, start: s.whdload) {
                                model.choose("whdloaddir", url)
                            }
                        }
                        Button("Clear") { model.settings.whdload = "" }.disabled(s.whdload.isEmpty)
                    }
                }
                .dropDestination(for: URL.self) { urls, _ in
                    guard let url = urls.first else { return false }
                    model.choose("whdload", url)
                    return true
                }
            }
            Section {
                if !s.rom.isEmpty, model.rom.error == nil, let d = model.rom.description {
                    if model.rom.version < 37 {
                        Warning("WHDLoad needs Kickstart 2.0 or later: this ROM is \(d).")
                    } else {
                        LabeledContent("Kickstart", value: "\(d), which WHDLoad runs on")
                    }
                }
                LabeledContent("Kickstart images") {
                    Text(w.kicks.isEmpty ? "None beside the ROM: games that emulate one won't start" : w.kicks.joined(separator: ", "))
                        .foregroundStyle(w.kicks.isEmpty ? .secondary : .primary)
                }
                LabeledContent("WHDLoad") {
                    Text(w.installed ? "In \(w.home)" : "Downloaded from whdload.de the first time a game starts")
                }
            } footer: {
                Note("A WHDLoad game boots straight in, with no Workbench needed: its .lha archive (unpacked once into \(w.home)/games, where its saves and high scores stay), its folder or its .slave. Dropping an .lha on the running Amiga starts it too. F10 quits the game. Most games want fast RAM and many AGA: the A1200 + fast preset suits them, with a Kickstart 3.x ROM.")
            }
        }
        .formStyle(.grouped)
    }
}

struct PortsTab: View {
    @Environment(LauncherModel.self) private var model

    // The serial setting is MODE:ADDRESS.
    private var serialMode: Binding<String> {
        Binding {
            model.settings.serial.split(separator: ":", maxSplits: 1).first.map(String.init) ?? ""
        } set: { mode in
            let addr = serialAddress.wrappedValue
            model.settings.serial = mode.isEmpty ? "" : mode + ":" + (addr.isEmpty ? "2323" : addr)
        }
    }

    private var serialAddress: Binding<String> {
        Binding {
            let parts = model.settings.serial.split(separator: ":", maxSplits: 1)
            return parts.count == 2 ? String(parts[1]) : ""
        } set: { addr in
            let mode = serialMode.wrappedValue
            if !mode.isEmpty { model.settings.serial = mode + ":" + addr }
        }
    }

    var body: some View {
        @Bindable var model = model
        Form {
            Section("Serial port") {
                Choice(title: "Connection", value: serialMode, options: [
                    ("Not connected", ""), ("Telnet (a terminal)", "telnet"), ("Raw TCP", "tcp"),
                ])
                if !serialMode.wrappedValue.isEmpty {
                    TextField("Port", text: serialAddress)
                    let addr = serialAddress.wrappedValue
                    let host = addr.contains(":") ? addr.replacingOccurrences(of: ":", with: " ") : "localhost " + addr
                    Note("Connect with: \(serialMode.wrappedValue == "tcp" ? "nc" : "telnet") \(host). On the Amiga, NewShell AUX: starts a shell there.")
                }
            }
            Section("Network") {
                Toggle(isOn: $model.settings.bsdSocket) {
                    Text("bsdsocket.library")
                    Text("Amiga TCP/IP software uses this computer's network.")
                }
                Toggle(isOn: $model.settings.a2065) {
                    Text("A2065 Ethernet (Amiga Unix)")
                    Text("10.0.2.15, through this computer; telnet to localhost 2323 reaches it.")
                }
            }
            Section("Joystick") {
                Toggle(isOn: $model.settings.joyKeys) {
                    Text("Arrow keys and Right Ctrl/Alt (Ctrl+F12 toggles)")
                    Text("A gamepad, if connected, is always the joystick.")
                }
                Toggle(isOn: $model.settings.joyAnalog) {
                    Text("Gamepad as an analogue joystick")
                    Text("Proportional, for the few games that read one.")
                }
            }
            Section("Clipboard") {
                Toggle(isOn: $model.settings.clipboard) {
                    Text("Share with this computer")
                    Text("Copy on either side, paste on the other. Needs Kickstart 2.0 or later and clipboard.device in DEVS:, as every Workbench has.")
                }
            }
        }
        .formStyle(.grouped)
    }
}

struct DisplayTab: View {
    @Environment(LauncherModel.self) private var model

    var body: some View {
        @Bindable var model = model
        let here = "The folder goAmiga starts in"
        Form {
            Section {
                Choice(title: "Window size", value: $model.settings.scale, options: [
                    ("1×", 1), ("2×", 2), ("3×", 3), ("4×", 4),
                ], other: { "\($0)×" })
                Toggle("Show the overscan area (720×284)", isOn: $model.settings.overscan)
                Toggle("Status bar with the activity lights", isOn: $model.settings.statusBar)
                Toggle("Start in warp mode (Cmd+W toggles)", isOn: $model.settings.warp)
                Toggle("No sound", isOn: $model.settings.mute)
            }
            Section {
                FileRow(title: "Screenshots", target: "shots", path: model.settings.screenshotDir == "." ? "" : model.settings.screenshotDir,
                        folder: true, empty: here) {
                    model.settings.screenshotDir = "."
                }
                FileRow(title: "Save states", target: "states", path: model.settings.stateDir == "." ? "" : model.settings.stateDir,
                        folder: true, empty: here) {
                    model.settings.stateDir = "."
                }
            }
        }
        .formStyle(.grouped)
    }
}
