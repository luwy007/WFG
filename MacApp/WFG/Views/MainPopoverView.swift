import SwiftUI

struct MainPopoverView: View {
    @EnvironmentObject var state: AppState
    @State private var selectedTab: Tab = .dashboard

    enum Tab: String, CaseIterable {
        case dashboard = "概览"
        case nodes = "节点"
        case subscriptions = "订阅"
        case rules = "规则"
    }

    var body: some View {
        VStack(spacing: 0) {
            // 顶栏
            HStack {
                Image(systemName: "shield.lefthalf.filled")
                    .foregroundColor(state.isRunning ? .green : .secondary)
                Text("WFG")
                    .font(.headline)
                Spacer()
                ProxyModeButtons()
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 12)
            .background(Color(NSColor.controlBackgroundColor))

            Divider()

            // Tab 选择
            HStack(spacing: 0) {
                ForEach(Tab.allCases, id: \.self) { tab in
                    TabButton(title: tab.rawValue, isSelected: selectedTab == tab) {
                        selectedTab = tab
                    }
                }
            }
            .padding(.horizontal, 8)
            .padding(.top, 6)

            Divider().padding(.top, 6)

            // 内容区
            Group {
                switch selectedTab {
                case .dashboard:    DashboardView()
                case .nodes:        NodeListView()
                case .subscriptions: SubscriptionView()
                case .rules:        RulesView()
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)

            // 错误提示
            if let err = state.errorMessage {
                ErrorBanner(message: err) {
                    state.errorMessage = nil
                }
            }
        }
        .frame(width: 360, height: 520)
        .task {
            await EngineLauncher.waitUntilReady()
            await state.fetchStatus()
        }
    }
}

struct ErrorBanner: View {
    let message: String
    let onDismiss: () -> Void
    @State private var showingDetails = false

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundColor(.orange)
                .padding(.top, 1)

            VStack(alignment: .leading, spacing: 6) {
                Text(message)
                    .font(.caption)
                    .lineLimit(3)
                    .truncationMode(.tail)
                    .textSelection(.enabled)

                Button {
                    showingDetails = true
                } label: {
                    Label("查看完整错误", systemImage: "doc.text.magnifyingglass")
                        .font(.caption2)
                }
                .buttonStyle(.plain)
                .foregroundColor(.accentColor)
            }

            Spacer(minLength: 4)

            Button {
                onDismiss()
            } label: {
                Image(systemName: "xmark")
                    .font(.system(size: 12, weight: .semibold))
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
        }
        .padding(8)
        .background(Color.orange.opacity(0.1))
        .sheet(isPresented: $showingDetails) {
            ErrorDetailsView(message: message, onDismiss: onDismiss)
        }
    }
}

struct ErrorDetailsView: View {
    let message: String
    let onDismiss: () -> Void
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Label("完整错误信息", systemImage: "exclamationmark.triangle.fill")
                    .font(.headline)
                    .foregroundColor(.orange)
                Spacer()
                Button {
                    copyMessage()
                } label: {
                    Label("复制", systemImage: "doc.on.doc")
                }
                Button {
                    onDismiss()
                    dismiss()
                } label: {
                    Label("关闭", systemImage: "xmark")
                }
            }

            ScrollView {
                Text(message)
                    .font(.system(size: 12, design: .monospaced))
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(10)
            }
            .frame(minHeight: 220)
            .background(Color(NSColor.textBackgroundColor), in: RoundedRectangle(cornerRadius: 6))
            .overlay(
                RoundedRectangle(cornerRadius: 6)
                    .stroke(Color.secondary.opacity(0.2))
            )
        }
        .padding(16)
        .frame(width: 560)
        .frame(minHeight: 320)
    }

    private func copyMessage() {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(message, forType: .string)
    }
}

struct TabButton: View {
    let title: String
    let isSelected: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Text(title)
                .font(.system(size: 12, weight: isSelected ? .semibold : .regular))
                .foregroundColor(isSelected ? .accentColor : .secondary)
                .padding(.horizontal, 10)
                .padding(.vertical, 4)
                .background(
                    isSelected ? Color.accentColor.opacity(0.12) : Color.clear,
                    in: Capsule()
                )
        }
        .buttonStyle(.plain)
    }
}

struct ProxyModeButtons: View {
    @EnvironmentObject var state: AppState

    var body: some View {
        HStack(spacing: 6) {
            if state.isLoading {
                ProgressView().scaleEffect(0.7).frame(width: 120)
            } else {
                ModeButton(
                    label: "系统代理",
                    icon: "network",
                    isActive: state.isRunning && !state.tunEnabled
                ) {
                    Task { await state.startInSystemProxyMode() }
                }
                ModeButton(
                    label: "TUN 模式",
                    icon: "arrow.triangle.branch",
                    isActive: state.isRunning && state.tunEnabled
                ) {
                    Task { await state.startInTunMode() }
                }
            }
        }
        .disabled(state.isLoading)
    }
}

struct ModeButton: View {
    let label: String
    let icon: String
    let isActive: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 4) {
                Image(systemName: isActive ? "stop.fill" : icon)
                    .font(.system(size: 10))
                Text(isActive ? "停止" : label)
                    .font(.system(size: 11, weight: .medium))
            }
            .foregroundColor(isActive ? .red : .white)
            .padding(.horizontal, 10)
            .padding(.vertical, 5)
            .background(isActive ? Color.red.opacity(0.12) : Color.accentColor, in: Capsule())
        }
        .buttonStyle(.plain)
    }
}
