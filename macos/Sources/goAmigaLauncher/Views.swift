import AppKit
import SwiftUI
import UniformTypeIdentifiers

// The configuration screen: the saved configurations in the sidebar, the
// settings in tabs, the status line with Start below.
struct LauncherView: View {
    @Environment(LauncherModel.self) private var model
    @State private var confirmDelete = false

    var body: some View {
        @Bindable var model = model
        NavigationSplitView {
            List(model.saved, id: \.self, selection: $model.selection) { name in
                Text(name)
            }
            .contextMenu(forSelectionType: String.self) { names in
                if let n = names.first {
                    Button("Start") {
                        model.load(n)
                        model.start()
                    }
                    Button("Delete…", role: .destructive) {
                        model.selection = n
                        confirmDelete = true
                    }
                }
            } primaryAction: { names in
                if let n = names.first { // double-click: load and start
                    model.load(n)
                    model.start()
                }
            }
            .onChange(of: model.selection) { _, n in
                if let n { model.load(n) }
            }
            .safeAreaInset(edge: .bottom) {
                VStack(alignment: .leading, spacing: 8) {
                    TextField("Name", text: $model.name)
                        .onSubmit { model.save() }
                    HStack {
                        Button("Save") { model.save() }
                        Button("New") { model.newConfiguration() }
                        Spacer()
                        Button("Delete…") { confirmDelete = true }
                            .disabled(model.selection == nil)
                    }
                    Text("Double-click a configuration to start it.")
                        .font(.caption).foregroundStyle(.secondary)
                }
                .padding(10)
            }
            .navigationSplitViewColumnWidth(min: 190, ideal: 220)
            .navigationTitle("Configurations")
        } detail: {
            VStack(spacing: 0) {
                TabView(selection: $model.tab) {
                    MachineTab().tabItem { Text("Machine") }.tag(LauncherModel.Tab.machine)
                    MemoryTab().tabItem { Text("Memory") }.tag(LauncherModel.Tab.memory)
                    FloppyTab().tabItem { Text("Floppies") }.tag(LauncherModel.Tab.floppies)
                    HardDiskTab().tabItem { Text("Hard disks") }.tag(LauncherModel.Tab.hardDisks)
                    WHDLoadTab().tabItem { Text("WHDLoad") }.tag(LauncherModel.Tab.whdload)
                    PortsTab().tabItem { Text("Ports") }.tag(LauncherModel.Tab.ports)
                    DisplayTab().tabItem { Text("Display") }.tag(LauncherModel.Tab.display)
                }
                .padding([.horizontal, .top])
                Divider().padding(.top, 10)
                HStack(alignment: .center) {
                    Text(model.status)
                        .foregroundStyle(model.statusIsError ? Color.red : Color.primary)
                        .lineLimit(3)
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Button("Quit") { NSApp.terminate(nil) }
                        .controlSize(.large)
                    Button("Start") { model.start() }
                        .keyboardShortcut(.defaultAction)
                        .controlSize(.large)
                        .disabled(model.running)
                }
                .padding()
            }
            .navigationTitle(model.name.isEmpty ? "goAmiga" : "goAmiga — \(model.name)")
        }
        .task { await saveTabShots(model) }
        .confirmationDialog("Delete the configuration \(model.selection ?? "")?", isPresented: $confirmDelete) {
            Button("Delete", role: .destructive) {
                if let n = model.selection { model.delete(n) }
            }
        }
    }
}

// MARK: - Controls

// Choice is a pop-up menu of values with their labels. A value from a
// settings file that isn't on the menu is shown as it is.
struct Choice<T: Hashable>: View {
    let title: String
    @Binding var value: T
    let options: [(String, T)]
    var other: (T) -> String = { "\($0)" }

    var body: some View {
        Picker(title, selection: $value) {
            ForEach(options, id: \.1) { label, v in
                Text(label).tag(v)
            }
            if !options.contains(where: { $0.1 == value }) {
                Text(other(value)).tag(value)
            }
        }
    }
}

// FileRow shows a file or folder with Choose… and Clear, and takes one
// dropped from the Finder.
struct FileRow: View {
    @Environment(LauncherModel.self) private var model
    let title: String
    let target: String
    let path: String
    var folder = false
    var types: [String] = []
    var empty = "None"
    let clear: () -> Void

    var body: some View {
        LabeledContent(title) {
            HStack {
                Text(path.isEmpty ? empty : path)
                    .foregroundStyle(path.isEmpty ? .secondary : .primary)
                    .lineLimit(1).truncationMode(.middle)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .help(path)
                Button("Choose…") {
                    if let url = openPanel(title: title, folder: folder, types: types, start: path) {
                        model.choose(target, url)
                    }
                }
                Button("Clear", action: clear).disabled(path.isEmpty)
            }
        }
        .dropDestination(for: URL.self) { urls, _ in
            guard let url = urls.first else { return false }
            model.choose(target, url)
            return true
        }
    }
}

let romTypes = ["rom", "bin"]
let diskTypes = ["adf", "ipf", "zip", "adz", "gz"]
let hdfTypes = ["hdf", "hdz"]
let whdloadTypes = ["lha", "lzh", "slave"]

// openPanel asks for a file (of these extensions) or a folder.
@MainActor
func openPanel(title: String, folder: Bool, types: [String] = [], start: String = "") -> URL? {
    let p = NSOpenPanel()
    p.title = title
    p.message = title
    p.canChooseFiles = !folder
    p.canChooseDirectories = folder
    p.allowsMultipleSelection = false
    if !types.isEmpty {
        p.allowedContentTypes = types.compactMap { UTType(filenameExtension: $0) }
        p.allowsOtherFileTypes = true
    }
    if !start.isEmpty {
        let url = URL(fileURLWithPath: start)
        p.directoryURL = folder ? url : url.deletingLastPathComponent()
    }
    return p.runModal() == .OK ? p.url : nil
}

struct Note: View {
    let text: String
    init(_ text: String) { self.text = text }
    var body: some View {
        Text(text).font(.callout).foregroundStyle(.secondary)
            .multilineTextAlignment(.leading)
            .frame(maxWidth: .infinity, alignment: .leading)
            .fixedSize(horizontal: false, vertical: true)
    }
}

struct Warning: View {
    let text: String
    init(_ text: String) { self.text = text }
    var body: some View {
        Label(text, systemImage: "exclamationmark.triangle.fill")
            .foregroundStyle(.red).font(.callout)
            .fixedSize(horizontal: false, vertical: true)
    }
}
