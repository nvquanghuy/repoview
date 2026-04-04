import Foundation
import Combine

class ServerManager: ObservableObject {
    @Published var isRunning = false
    @Published var serverURL: URL?
    @Published var folderName: String = "RepoView"
    @Published var errorMessage: String?

    private var process: Process?
    private var outputPipe: Pipe?

    func start(folder: String) {
        // Stop any existing server
        stop()

        // Find an available port
        let port = findAvailablePort()

        // Get the path to the bundled repoview binary
        guard let binaryPath = Bundle.main.path(forResource: "repoview", ofType: nil) else {
            errorMessage = "repoview binary not found in app bundle"
            return
        }

        // Make sure it's executable
        try? FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: binaryPath)

        let process = Process()
        process.executableURL = URL(fileURLWithPath: binaryPath)
        process.arguments = ["--port", String(port), folder]
        process.currentDirectoryURL = URL(fileURLWithPath: folder)

        // Capture output to detect when server is ready
        let pipe = Pipe()
        process.standardOutput = pipe
        process.standardError = pipe
        self.outputPipe = pipe

        // Handle process termination
        process.terminationHandler = { [weak self] process in
            DispatchQueue.main.async {
                self?.isRunning = false
                if process.terminationStatus != 0 {
                    self?.errorMessage = "Server exited with code \(process.terminationStatus)"
                }
            }
        }

        do {
            try process.run()
            self.process = process
            self.isRunning = true
            self.serverURL = URL(string: "http://localhost:\(port)")
            self.folderName = URL(fileURLWithPath: folder).lastPathComponent
            self.errorMessage = nil

            // Read output in background to prevent pipe buffer from filling
            DispatchQueue.global(qos: .background).async {
                let handle = pipe.fileHandleForReading
                while self.process?.isRunning == true {
                    let data = handle.availableData
                    if data.isEmpty { break }
                    // Optionally log: print(String(data: data, encoding: .utf8) ?? "")
                }
            }
        } catch {
            errorMessage = "Failed to start server: \(error.localizedDescription)"
        }
    }

    func stop() {
        process?.terminate()
        process?.waitUntilExit()
        process = nil
        isRunning = false
        serverURL = nil
    }

    private func findAvailablePort() -> Int {
        // Try to find an available port starting from 7777
        for port in 7777..<8777 {
            if isPortAvailable(port) {
                return port
            }
        }
        // Fallback to random high port
        return Int.random(in: 49152..<65535)
    }

    private func isPortAvailable(_ port: Int) -> Bool {
        let socketFD = socket(AF_INET, SOCK_STREAM, IPPROTO_TCP)
        guard socketFD >= 0 else { return false }
        defer { close(socketFD) }

        var addr = sockaddr_in()
        addr.sin_family = sa_family_t(AF_INET)
        addr.sin_port = in_port_t(port).bigEndian
        addr.sin_addr.s_addr = INADDR_LOOPBACK.bigEndian

        let result = withUnsafePointer(to: &addr) { ptr in
            ptr.withMemoryRebound(to: sockaddr.self, capacity: 1) { sockaddrPtr in
                bind(socketFD, sockaddrPtr, socklen_t(MemoryLayout<sockaddr_in>.size))
            }
        }

        return result == 0
    }
}
