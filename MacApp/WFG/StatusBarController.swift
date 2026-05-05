import AppKit
import SwiftUI

@MainActor
class StatusBarController {
    private var statusItem: NSStatusItem
    private var popover: NSPopover
    private var appState: AppState

    init() {
        appState = AppState()
        popover = NSPopover()
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)

        setupPopover()
        setupStatusItem()

        // 启动时等引擎就绪再检查状态
        Task {
            await EngineLauncher.waitUntilReady()
            await appState.fetchStatus()
        }
    }

    private func setupPopover() {
        popover.contentSize = NSSize(width: 360, height: 520)
        popover.behavior = .transient
        popover.animates = true
        popover.contentViewController = NSHostingController(
            rootView: MainPopoverView().environmentObject(appState)
        )
    }

    private func setupStatusItem() {
        guard let button = statusItem.button else { return }
        updateStatusIcon(running: false)
        button.action = #selector(togglePopover)
        button.target = self

        // 右键菜单
        let menu = NSMenu()
        menu.addItem(NSMenuItem(title: "打开面板", action: #selector(togglePopover), keyEquivalent: ""))
        menu.addItem(NSMenuItem.separator())
        menu.addItem(NSMenuItem(title: "退出 WFG", action: #selector(quit), keyEquivalent: "q"))
        statusItem.menu = nil // 左键点击显示 popover，不挂菜单
        _ = menu // 备用
    }

    func updateStatusIcon(running: Bool) {
        let name = running ? "shield.fill" : "shield.slash"
        statusItem.button?.image = NSImage(systemSymbolName: name, accessibilityDescription: "WFG")
        statusItem.button?.image?.isTemplate = true
    }

    @objc private func togglePopover() {
        guard let button = statusItem.button else { return }
        if popover.isShown {
            popover.performClose(nil)
        } else {
            popover.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
            popover.contentViewController?.view.window?.makeKey()
        }
    }

    @objc private func quit() {
        Task { @MainActor in
            await appState.stopProxy()
            NSApp.terminate(nil)
        }
    }
}
