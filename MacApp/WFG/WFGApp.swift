import SwiftUI

@main
struct WFGApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var delegate

    var body: some Scene {
        // 菜单栏应用无主窗口
        Settings {
            EmptyView()
        }
    }
}

class AppDelegate: NSObject, NSApplicationDelegate {
    private var statusBarController: StatusBarController?

    func applicationDidFinishLaunching(_ notification: Notification) {
        // 先拉引擎；子进程启动很快，状态栏建好后引擎多半已在监听。
        // 就算还没就绪也没关系：AppState 的 fetchStatus 会在连不上时静默失败，
        // 用户点"启动"时 120s 的 longSession 会给足余量。
        EngineLauncher.start()

        NSApp.setActivationPolicy(.accessory)
        statusBarController = StatusBarController()
    }

    func applicationWillTerminate(_ notification: Notification) {
        EngineLauncher.stop()
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        return false
    }
}
