import AppKit

// With GOAMIGA_UISHOT naming a folder, the configuration screen saves each
// tab there as tabN.png and quits (for checking the layout, as the
// Ebitengine launcher does). CGWindowListCreateImage is deprecated, but
// unlike ScreenCaptureKit needs no permission for the app's own window.
@MainActor
func saveTabShots(_ model: LauncherModel) async {
    guard let dir = ProcessInfo.processInfo.environment["GOAMIGA_UISHOT"] else { return }
    for (i, tab) in LauncherModel.Tab.allCases.enumerated() {
        model.tab = tab
        try? await Task.sleep(for: .milliseconds(700))
        guard let window = NSApp.windows.first(where: { $0.isVisible && $0.canBecomeMain }),
              let image = CGWindowListCreateImage(.null, .optionIncludingWindow, CGWindowID(window.windowNumber), [.boundsIgnoreFraming])
        else { continue }
        try? NSBitmapImageRep(cgImage: image).representation(using: .png, properties: [:])?
            .write(to: URL(fileURLWithPath: dir).appendingPathComponent("tab\(i).png"))
    }
    NSApp.terminate(nil)
}

