import SwiftUI
import WebKit

struct ContentView: View {
    @EnvironmentObject var serverManager: ServerManager

    var body: some View {
        Group {
            if serverManager.isRunning, let url = serverManager.serverURL {
                WebView(url: url)
            } else if let error = serverManager.errorMessage {
                VStack(spacing: 16) {
                    Image(systemName: "exclamationmark.triangle")
                        .font(.system(size: 48))
                        .foregroundColor(.orange)
                    Text("Error")
                        .font(.headline)
                    Text(error)
                        .foregroundColor(.secondary)
                    Button("Open Folder...") {
                        NSApp.sendAction(#selector(AppDelegate.openFolder), to: nil, from: nil)
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                VStack(spacing: 16) {
                    Image(systemName: "folder")
                        .font(.system(size: 48))
                        .foregroundColor(.secondary)
                    Text("No Folder Selected")
                        .font(.headline)
                    Text("Choose a folder to get started")
                        .foregroundColor(.secondary)
                    Button("Open Folder...") {
                        NSApp.sendAction(#selector(AppDelegate.openFolder), to: nil, from: nil)
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .frame(minWidth: 800, minHeight: 600)
        .navigationTitle(serverManager.folderName)
    }
}

struct WebView: NSViewRepresentable {
    let url: URL

    func makeNSView(context: Context) -> WKWebView {
        let config = WKWebViewConfiguration()
        config.preferences.setValue(true, forKey: "developerExtrasEnabled")

        let webView = WKWebView(frame: .zero, configuration: config)
        webView.navigationDelegate = context.coordinator

        // Listen for reload notifications
        NotificationCenter.default.addObserver(
            forName: .reloadWebView,
            object: nil,
            queue: .main
        ) { _ in
            webView.reload()
        }

        return webView
    }

    func updateNSView(_ webView: WKWebView, context: Context) {
        // Only load if URL changed
        if webView.url != url {
            webView.load(URLRequest(url: url))
        }
    }

    func makeCoordinator() -> Coordinator {
        Coordinator()
    }

    class Coordinator: NSObject, WKNavigationDelegate {
        func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
            print("WebView navigation failed: \(error)")
        }

        func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
            // Server might not be ready yet, retry after a short delay
            if (error as NSError).code == NSURLErrorCannotConnectToHost {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                    webView.reload()
                }
            }
        }
    }
}

