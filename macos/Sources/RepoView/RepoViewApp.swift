import SwiftUI

@main
struct RepoViewApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environmentObject(appDelegate.serverManager)
        }
        .commands {
            CommandGroup(replacing: .newItem) {
                Button("Open Folder...") {
                    appDelegate.openFolder()
                }
                .keyboardShortcut("o", modifiers: .command)
            }

            CommandGroup(after: .toolbar) {
                Button("Reload") {
                    NotificationCenter.default.post(name: .reloadWebView, object: nil)
                }
                .keyboardShortcut("r", modifiers: .command)

                Divider()

                Button("Open in Browser") {
                    appDelegate.openInBrowser()
                }
                .keyboardShortcut("b", modifiers: [.command, .shift])
            }
        }
    }
}

class AppDelegate: NSObject, NSApplicationDelegate {
    let serverManager = ServerManager()

    func applicationDidFinishLaunching(_ notification: Notification) {
        // Check for saved folder or prompt for one
        if let savedPath = UserDefaults.standard.string(forKey: "lastFolderPath"),
           FileManager.default.fileExists(atPath: savedPath) {
            serverManager.start(folder: savedPath)
        } else {
            // Delay slightly to let window appear first
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) {
                self.openFolder()
            }
        }
    }

    func applicationWillTerminate(_ notification: Notification) {
        serverManager.stop()
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        return true
    }

    @objc func openFolder() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.message = "Choose a folder to view"
        panel.prompt = "Open"

        if panel.runModal() == .OK, let url = panel.url {
            let path = url.path
            UserDefaults.standard.set(path, forKey: "lastFolderPath")
            serverManager.start(folder: path)
        }
    }

    func openInBrowser() {
        guard let url = serverManager.serverURL else { return }
        NSWorkspace.shared.open(url)
    }
}

extension Notification.Name {
    static let reloadWebView = Notification.Name("reloadWebView")
}
