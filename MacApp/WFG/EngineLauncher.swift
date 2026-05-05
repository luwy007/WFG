import Foundation
import Darwin

/// 负责拉起 / 停止 `wfg-engine` 子进程。
///
/// 二进制查找顺序：
///   1. `Bundle.main/Contents/Resources/wfg-engine`（打包后）
///   2. 项目源码树里的 `engine/wfg-engine`（Xcode Debug 直接跑时）
enum EngineLauncher {
    // 只在主线程（AppDelegate 生命周期方法）读写，用 unsafe 绕开 Swift 6 全局状态检查
    nonisolated(unsafe) private static var process: Process?

    static let apiPort: UInt16 = 19090

    /// App 启动时调用。若端口已被占用则先 kill 旧引擎，再启动绑定当前父进程 PID 的新引擎。
    static func start() {
        if isPortOpen(apiPort) {
            NSLog("[WFG] killing stale engine on :\(apiPort)")
            let kill = Process()
            kill.launchPath = "/usr/bin/pkill"
            kill.arguments = ["-f", "wfg-engine"]
            try? kill.run()
            kill.waitUntilExit()
            // 等端口释放，最多 3 秒
            var waited = 0
            while isPortOpen(apiPort) && waited < 30 {
                Thread.sleep(forTimeInterval: 0.1)
                waited += 1
            }
        }
        guard let binURL = locateBinary() else {
            NSLog("[WFG] wfg-engine binary not found in bundle or dev path")
            return
        }

        let logURL = logFileURL()
        try? FileManager.default.createDirectory(
            at: logURL.deletingLastPathComponent(),
            withIntermediateDirectories: true
        )
        FileManager.default.createFile(atPath: logURL.path, contents: nil)
        let handle = try? FileHandle(forWritingTo: logURL)

        let p = Process()
        p.executableURL = binURL
        p.arguments = ["-port", "\(apiPort)", "-parent-pid", "\(ProcessInfo.processInfo.processIdentifier)"]
        if let h = handle {
            p.standardOutput = h
            p.standardError = h
        }
        p.terminationHandler = { proc in
            NSLog("[WFG] engine exited, status=\(proc.terminationStatus)")
        }

        do {
            try p.run()
            process = p
            NSLog("[WFG] engine launched pid=\(p.processIdentifier), log=\(logURL.path)")
        } catch {
            NSLog("[WFG] failed to launch engine: \(error)")
        }
    }

    /// App 退出时调用。给引擎一个优雅退出的机会，超时再 kill。
    static func stop() {
        guard let p = process, p.isRunning else { return }
        p.terminate() // SIGTERM，引擎会自己清理系统代理
        let deadline = Date().addingTimeInterval(3)
        while p.isRunning && Date() < deadline {
            Thread.sleep(forTimeInterval: 0.1)
        }
        if p.isRunning {
            kill(p.processIdentifier, SIGKILL)
        }
        process = nil
    }

    /// 等待引擎 HTTP 就绪，最多等 timeout 秒。比 TCP 端口检查更可靠。
    static func waitUntilReady(timeout: TimeInterval = 8.0) async {
        let url = URL(string: "http://127.0.0.1:\(apiPort)/api/status")!
        let cfg = URLSessionConfiguration.default
        cfg.timeoutIntervalForRequest = 1
        cfg.waitsForConnectivity = false
        let probe = URLSession(configuration: cfg)
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if let _ = try? await probe.data(from: url) { return }
            try? await Task.sleep(nanoseconds: 300_000_000) // 300ms
        }
    }

    // MARK: - Helpers

    private static func locateBinary() -> URL? {
        if let res = Bundle.main.resourceURL {
            let bundled = res.appendingPathComponent("wfg-engine")
            if FileManager.default.isExecutableFile(atPath: bundled.path) {
                return bundled
            }
        }
        // Dev fallback：Xcode Debug 下找项目源码树里的 engine 目录。
        // Bundle 路径形如 .../DerivedData/.../Build/Products/Debug/WFG.app
        // 向上 5 级到 Mac App 源码根再拼 ../engine/wfg-engine 不稳定，
        // 改成从 CWD 向上找 WFG/engine/wfg-engine。
        var dir = URL(fileURLWithPath: FileManager.default.currentDirectoryPath)
        for _ in 0..<6 {
            let candidate = dir.appendingPathComponent("engine/wfg-engine")
            if FileManager.default.isExecutableFile(atPath: candidate.path) {
                return candidate
            }
            dir.deleteLastPathComponent()
        }
        // 最后兜底：硬编码 dev 绝对路径（仅在本机开发机上有用）
        let devPath = "/Users/\(NSUserName())/Desktop/Downloads/cc project/WFG/engine/wfg-engine"
        if FileManager.default.isExecutableFile(atPath: devPath) {
            return URL(fileURLWithPath: devPath)
        }
        return nil
    }

    private static func logFileURL() -> URL {
        let lib = FileManager.default.urls(for: .libraryDirectory, in: .userDomainMask).first!
        return lib.appendingPathComponent("Logs/WFG/engine.log")
    }

    private static func isPortOpen(_ port: UInt16) -> Bool {
        let fd = socket(AF_INET, SOCK_STREAM, 0)
        if fd < 0 { return false }
        defer { close(fd) }

        var addr = sockaddr_in()
        addr.sin_family = sa_family_t(AF_INET)
        addr.sin_port = port.bigEndian
        addr.sin_addr.s_addr = inet_addr("127.0.0.1")

        let result = withUnsafePointer(to: &addr) { ptr -> Int32 in
            ptr.withMemoryRebound(to: sockaddr.self, capacity: 1) { sa in
                connect(fd, sa, socklen_t(MemoryLayout<sockaddr_in>.size))
            }
        }
        return result == 0
    }
}
