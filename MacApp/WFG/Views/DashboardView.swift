import SwiftUI

struct DashboardView: View {
    @EnvironmentObject var state: AppState

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                // 状态卡片
                StatusCard()

                // Geo 数据库缺失警告
                if !state.geoReady {
                    GeoWarningCard()
                }

                // 连接速度测试
                PingCard()

                // 流量信息（来自订阅 UserInfo）
                if let sub = state.subscriptions.first(where: { $0.userInfo != nil }),
                   let info = sub.userInfo {
                    TrafficCard(info: info, subName: sub.name)
                }

                // 快捷操作
                QuickActionsCard()
            }
            .padding(16)
        }
        .task {
            await EngineLauncher.waitUntilReady()
            await state.fetchSubscriptions()
            await state.fetchNodes()
            await state.checkGeoStatus()
        }
    }
}

struct StatusCard: View {
    @EnvironmentObject var state: AppState

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label("运行状态", systemImage: "antenna.radiowaves.left.and.right")
                .font(.caption)
                .foregroundColor(.secondary)

            HStack(spacing: 12) {
                StatusDot(active: state.isRunning)

                VStack(alignment: .leading, spacing: 2) {
                    Text(state.isRunning ? "代理运行中" : "已停止")
                        .font(.system(size: 14, weight: .semibold))
                    if state.isRunning, let node = state.selectedNode, !node.isEmpty {
                        Text(node)
                            .font(.caption)
                            .foregroundColor(.accentColor)
                            .lineLimit(1)
                            .truncationMode(.middle)
                    } else {
                        Text(state.isRunning ? "本地端口 7890 · 国内直连 / 境外代理" : "点击「启动」开始使用")
                            .font(.caption)
                            .foregroundColor(.secondary)
                    }
                }
                Spacer()

                VStack(spacing: 4) {
                    Text(modeLabel(state.proxyMode))
                        .font(.caption)
                        .foregroundColor(.secondary)
                        .padding(.horizontal, 8)
                        .padding(.vertical, 3)
                        .background(Color.secondary.opacity(0.1), in: Capsule())
                    Text("\(state.nodes.count) 节点")
                        .font(.caption)
                        .foregroundColor(.secondary)
                }
            }
        }
        .padding(12)
        .background(Color(NSColor.controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
    }

    func modeLabel(_ mode: String) -> String {
        switch mode {
        case "global": return "全局代理"
        case "direct": return "全部直连"
        default:       return "规则分流"
        }
    }
}

struct PingCard: View {
    @EnvironmentObject var state: AppState

    private let sites: [(key: String, label: String, icon: String)] = [
        ("google", "Google", "g.circle"),
        ("x", "X (Twitter)", "xmark.circle"),
        ("youtube", "YouTube", "play.circle"),
        ("baidu", "百度", "magnifyingglass.circle"),
        ("xiaohongshu", "小红书", "heart.circle"),
        ("tencent", "腾讯", "t.circle"),
    ]

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Label("连接速度测试", systemImage: "speedometer")
                    .font(.caption)
                    .foregroundColor(.secondary)
                Spacer()
                Button {
                    Task { await state.pingWebsites() }
                } label: {
                    if state.isPinging {
                        ProgressView().scaleEffect(0.6).frame(width: 36)
                    } else {
                        Text("检测")
                            .font(.system(size: 11, weight: .medium))
                            .foregroundColor(.accentColor)
                    }
                }
                .buttonStyle(.plain)
                .disabled(state.isPinging || !state.isRunning)
            }

            ForEach(sites, id: \.key) { site in
                HStack {
                    Image(systemName: site.icon)
                        .font(.system(size: 12))
                        .foregroundColor(.secondary)
                        .frame(width: 16)
                    Text(site.label)
                        .font(.system(size: 12))
                    Spacer()
                    PingResultBadge(ms: state.pingResults[site.key])
                }
            }

            if !state.isRunning {
                Text("启动代理后可检测")
                    .font(.system(size: 10))
                    .foregroundColor(.secondary)
            }
        }
        .padding(12)
        .background(Color(NSColor.controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
    }
}

struct PingResultBadge: View {
    let ms: Int?

    var body: some View {
        Group {
            if let ms = ms {
                if ms == 0 {
                    Text("超时")
                        .foregroundColor(.red)
                } else {
                    Text("\(ms) ms")
                        .foregroundColor(ms < 500 ? .green : ms < 1000 ? .yellow : .orange)
                }
            } else {
                Text("—")
                    .foregroundColor(.secondary)
            }
        }
        .font(.system(size: 11, weight: .medium).monospacedDigit())
    }
}

struct StatusDot: View {
    let active: Bool
    @State private var pulse = false

    var body: some View {
        ZStack {
            if active {
                Circle()
                    .fill(Color.green.opacity(0.3))
                    .frame(width: 20, height: 20)
                    .scaleEffect(pulse ? 1.4 : 1.0)
                    .animation(.easeInOut(duration: 1.2).repeatForever(autoreverses: true), value: pulse)
                    .onAppear { pulse = true }
            }
            Circle()
                .fill(active ? Color.green : Color.gray.opacity(0.4))
                .frame(width: 10, height: 10)
        }
    }
}

struct TrafficCard: View {
    let info: UserInfo
    let subName: String

    var usedBytes: Int64 { info.upload + info.download }
    var progress: Double { info.total > 0 ? Double(usedBytes) / Double(info.total) : 0 }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label(subName, systemImage: "chart.bar.fill")
                .font(.caption)
                .foregroundColor(.secondary)

            HStack {
                Text("已用 \(formatBytes(usedBytes))")
                    .font(.system(size: 13, weight: .medium))
                Spacer()
                Text("共 \(formatBytes(info.total))")
                    .font(.caption)
                    .foregroundColor(.secondary)
            }

            ProgressView(value: progress)
                .tint(progress > 0.8 ? .red : .accentColor)

            if info.expire > 0 {
                Text("到期：\(Date(timeIntervalSince1970: Double(info.expire)).formatted(date: .abbreviated, time: .omitted))")
                    .font(.caption)
                    .foregroundColor(.secondary)
            }
        }
        .padding(12)
        .background(Color(NSColor.controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
    }

    func formatBytes(_ bytes: Int64) -> String {
        let gb = Double(bytes) / 1_073_741_824
        if gb >= 1 { return String(format: "%.1f GB", gb) }
        let mb = Double(bytes) / 1_048_576
        return String(format: "%.0f MB", mb)
    }
}

struct QuickActionsCard: View {
    @EnvironmentObject var state: AppState

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label("快捷操作", systemImage: "bolt.fill")
                .font(.caption)
                .foregroundColor(.secondary)

            HStack(spacing: 8) {
                QuickActionButton(icon: "arrow.clockwise", title: "刷新所有订阅") {
                    Task {
                        for sub in state.subscriptions {
                            await state.refreshSubscription(id: sub.id)
                        }
                    }
                }
                QuickActionButton(icon: "speedometer", title: "批量测速") {
                    Task { await state.fetchNodes() }
                }
                QuickActionButton(icon: "arrow.up.arrow.down", title: modeCycleLabel(state.proxyMode)) {
                    Task { await state.setProxyMode(nextMode(state.proxyMode)) }
                }
            }
        }
        .padding(12)
        .background(Color(NSColor.controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
    }

    func nextMode(_ current: String) -> String {
        switch current {
        case "rule":   return "global"
        case "global": return "direct"
        default:       return "rule"
        }
    }

    func modeCycleLabel(_ current: String) -> String {
        switch current {
        case "rule":   return "规则分流"
        case "global": return "全局代理"
        case "direct": return "全部直连"
        default:       return "规则分流"
        }
    }
}

struct QuickActionButton: View {
    let icon: String
    let title: String
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(spacing: 4) {
                Image(systemName: icon)
                    .font(.system(size: 16))
                Text(title)
                    .font(.system(size: 10))
                    .multilineTextAlignment(.center)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 8)
            .background(Color.accentColor.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
        }
        .buttonStyle(.plain)
    }
}

struct GeoWarningCard: View {
    @EnvironmentObject var state: AppState

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundColor(.orange)
                Text("路由规则数据缺失")
                    .font(.caption)
                    .fontWeight(.medium)
                    .foregroundColor(.orange)
                Spacer()
                Button {
                    Task { await state.downloadGeoData() }
                } label: {
                    if state.isDownloadingGeo {
                        HStack(spacing: 4) {
                            ProgressView().scaleEffect(0.6)
                            Text("下载中…").font(.system(size: 11))
                        }
                    } else {
                        Text("立即下载")
                            .font(.system(size: 11, weight: .medium))
                            .foregroundColor(.white)
                            .padding(.horizontal, 10)
                            .padding(.vertical, 4)
                            .background(Color.orange, in: Capsule())
                    }
                }
                .buttonStyle(.plain)
                .disabled(state.isDownloadingGeo)
            }
            Text("geoip / geosite 数据库未下载，GEOIP/GEOSITE 规则无法匹配，所有流量将直连。需先启动系统代理再下载。")
                .font(.system(size: 10))
                .foregroundColor(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(12)
        .background(Color.orange.opacity(0.08), in: RoundedRectangle(cornerRadius: 10))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color.orange.opacity(0.3), lineWidth: 1))
    }
}
