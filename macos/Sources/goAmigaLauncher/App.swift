import AppKit
import SwiftUI

@main
struct GoAmigaApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var delegate
    @State private var model = LauncherModel()

    var body: some Scene {
        Window("goAmiga", id: "launcher") {
            LauncherView()
                .environment(model)
                .frame(minWidth: 860, minHeight: 560)
        }
        .defaultSize(width: 920, height: 700)
        .windowResizability(.contentMinSize)
        .commands {
            CommandGroup(replacing: .newItem) {
                Button("New Configuration") { model.newConfiguration() }
                    .keyboardShortcut("n")
            }
            CommandGroup(replacing: .saveItem) {
                Button("Save Configuration") { model.save() }
                    .keyboardShortcut("s")
            }
        }
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationWillFinishLaunching(_ notification: Notification) {
        signal(SIGPIPE, SIG_IGN) // a goamiga that has gone away is an error, not a crash
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        // Hiding the window while the machine runs isn't closing it.
        NSApp.activationPolicy() == .regular
    }
}
