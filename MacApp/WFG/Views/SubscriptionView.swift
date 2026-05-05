import SwiftUI

struct SubscriptionView: View {
    @EnvironmentObject var state: AppState
    @State private var showAddSheet = false
    @State private var showLogSheet = false
    @State private var logSubID = ""
    @State private var logSubName = ""

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text("订阅管理")
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundColor(.secondary)
                Spacer()
                Button {
                    logSubID = ""
                    logSubName = ""
                    showLogSheet = true
                } label: {
                    Image(systemName: "doc.text.magnifyingglass")
                        .font(.system(size: 14))
                }
                .buttonStyle(.plain)
                .help("订阅日志")

                Button {
                    showAddSheet = true
                } label: {
                    Image(systemName: "plus.circle.fill")
                        .font(.system(size: 16))
                }
                .buttonStyle(.plain)
                .help("添加订阅")
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)

            Divider()

            if state.subscriptions.isEmpty {
                ContentUnavailableView(
                    "暂无订阅",
                    systemImage: "link.badge.plus",
                    description: Text("点击右上角 + 添加机场订阅链接")
                )
            } else {
                List(state.subscriptions) { sub in
                    SubscriptionRowView(subscription: sub) {
                        logSubID = sub.id
                        logSubName = sub.name
                        showLogSheet = true
                    }
                        .listRowInsets(EdgeInsets(top: 4, leading: 8, bottom: 4, trailing: 8))
                }
                .listStyle(.plain)
                .scrollContentBackground(.hidden)
            }
        }
        .sheet(isPresented: $showAddSheet) {
            AddSubscriptionSheet()
        }
        .sheet(isPresented: $showLogSheet) {
            SubLogSheet(subID: logSubID, title: logSubName.isEmpty ? "订阅日志" : "\(logSubName) 日志")
        }
        .task { await state.fetchSubscriptions() }
        .task {
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 30_000_000_000)
                await state.fetchSubscriptions()
            }
        }
    }
}

struct SubscriptionRowView: View {
    @EnvironmentObject var state: AppState
    let subscription: Subscription
    @State private var isRefreshing = false
    @State private var autoRefresh: Bool
    @State private var refreshInterval: Int
    @State private var refreshIntervalText: String
    let onShowLogs: () -> Void

    init(subscription: Subscription, onShowLogs: @escaping () -> Void) {
        self.subscription = subscription
        self.onShowLogs = onShowLogs
        _autoRefresh = State(initialValue: subscription.autoRefresh)
        let clampedInterval = min(max(subscription.refreshInterval, 1), 300)
        _refreshInterval = State(initialValue: clampedInterval)
        _refreshIntervalText = State(initialValue: "\(clampedInterval)")
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text(subscription.name)
                        .font(.system(size: 13, weight: .medium))
                    Text("\(subscription.nodeCount) 个节点")
                        .font(.caption)
                        .foregroundColor(.secondary)
                }
                Spacer()

                Button {
                    onShowLogs()
                } label: {
                    Image(systemName: "doc.text.magnifyingglass")
                        .font(.system(size: 13))
                }
                .buttonStyle(.plain)
                .help("查看订阅日志")

                // 刷新
                Button {
                    Task {
                        isRefreshing = true
                        await state.refreshSubscription(id: subscription.id)
                        isRefreshing = false
                    }
                } label: {
                    if isRefreshing {
                        ProgressView().scaleEffect(0.6)
                    } else {
                        Image(systemName: "arrow.clockwise")
                            .font(.system(size: 13))
                    }
                }
                .buttonStyle(.plain)
                .disabled(isRefreshing)

                // 删除
                Button {
                    Task { await state.deleteSubscription(id: subscription.id) }
                } label: {
                    Image(systemName: "trash")
                        .font(.system(size: 13))
                        .foregroundColor(.red.opacity(0.7))
                }
                .buttonStyle(.plain)
            }

            HStack(spacing: 10) {
                if let updatedAt = subscription.updatedAt, !updatedAt.isEmpty {
                    HStack(spacing: 4) {
                        Image(systemName: "clock.arrow.circlepath")
                            .font(.system(size: 9))
                        Text("最新节点更新 \(formatRelativeTime(updatedAt))")
                            .font(.system(size: 10))
                    }
                } else {
                    HStack(spacing: 4) {
                        Image(systemName: "clock.badge.questionmark")
                            .font(.system(size: 9))
                        Text("暂无节点更新时间")
                            .font(.system(size: 10))
                    }
                }

                if autoRefresh {
                    Image(systemName: "clock.arrow.circlepath")
                        .font(.system(size: 9))
                    Text("每 \(refreshInterval) 分钟")
                        .font(.system(size: 10))
                }
            }
            .foregroundColor(.secondary.opacity(0.8))

            HStack(spacing: 8) {
                Toggle("自动刷新", isOn: Binding(
                    get: { autoRefresh },
		                    set: { newValue in
		                        autoRefresh = newValue
		                        saveSettings()
		                    }
		                ))
                .toggleStyle(.checkbox)
                .font(.caption)

                HStack(spacing: 4) {
                    TextField("10", text: Binding(
                        get: { refreshIntervalText },
                        set: { newValue in
                            let digits = String(newValue.filter(\.isNumber).prefix(3))
	                            refreshIntervalText = digits
	                            guard let value = Int(digits) else { return }
	                            refreshInterval = min(max(value, 1), 300)
	                        }
	                    ))
                    .textFieldStyle(.roundedBorder)
                    .font(.caption)
                    .frame(width: 54)
	                    .disabled(!autoRefresh)
	                    .onSubmit { normalizeRefreshIntervalTextAndSave() }

	                    Text("分钟")
	                        .font(.caption)
	                        .foregroundColor(autoRefresh ? .primary : .secondary)

	                    Button {
	                        normalizeRefreshIntervalTextAndSave()
	                    } label: {
	                        Image(systemName: "checkmark.circle")
	                            .font(.system(size: 13))
	                    }
	                    .buttonStyle(.plain)
	                    .disabled(!autoRefresh)
	                    .help("确认刷新周期")
	                }
	                .disabled(!autoRefresh)

                Spacer()
            }

            // 流量进度条
            if let info = subscription.userInfo, info.total > 0 {
                let used = Double(info.upload + info.download)
                let total = Double(info.total)
                VStack(alignment: .leading, spacing: 2) {
                    ProgressView(value: used / total)
                        .tint(used / total > 0.8 ? .red : .accentColor)
                    HStack {
                        Text(formatBytes(Int64(used)) + " / " + formatBytes(info.total))
                        Spacer()
                        if info.expire > 0 {
                            Text("到期 " + Date(timeIntervalSince1970: Double(info.expire))
                                .formatted(date: .abbreviated, time: .omitted))
                        }
                    }
                    .font(.caption2)
                    .foregroundColor(.secondary)
                }
            }
        }
        .padding(.vertical, 4)
        .onChange(of: subscription.autoRefresh) { newValue in
            autoRefresh = newValue
        }
	        .onChange(of: subscription.refreshInterval) { newValue in
	            let clamped = min(max(newValue, 1), 300)
	            refreshInterval = clamped
	            refreshIntervalText = "\(clamped)"
	        }
	    }

	    func saveSettings() {
	        let id = subscription.id
	        let autoRefresh = autoRefresh
	        let refreshInterval = refreshInterval
	        Task {
	            await state.updateSubscription(
	                id: id,
	                autoRefresh: autoRefresh,
	                refreshInterval: refreshInterval
            )
        }
    }

    func normalizeRefreshIntervalTextAndSave() {
	        let normalized = min(max(Int(refreshIntervalText) ?? refreshInterval, 1), 300)
	        refreshInterval = normalized
	        refreshIntervalText = "\(normalized)"
	        saveSettings()
	    }

    func formatBytes(_ bytes: Int64) -> String {
        let gb = Double(bytes) / 1_073_741_824
        if gb >= 1 { return String(format: "%.1f GB", gb) }
        return String(format: "%.0f MB", Double(bytes) / 1_048_576)
    }

    func formatRelativeTime(_ isoString: String) -> String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let date = formatter.date(from: isoString) {
            let interval = Date().timeIntervalSince(date)
            if interval < 60 { return "刚刚" }
            if interval < 3600 { return "\(Int(interval / 60)) 分钟前" }
            if interval < 86400 { return "\(Int(interval / 3600)) 小时前" }
            return "\(Int(interval / 86400)) 天前"
        }
        // 尝试不带 fractional seconds 的格式
        let formatter2 = ISO8601DateFormatter()
        formatter2.formatOptions = [.withInternetDateTime]
        if let date = formatter2.date(from: isoString) {
            let interval = Date().timeIntervalSince(date)
            if interval < 60 { return "刚刚" }
            if interval < 3600 { return "\(Int(interval / 60)) 分钟前" }
            if interval < 86400 { return "\(Int(interval / 3600)) 小时前" }
            return "\(Int(interval / 86400)) 天前"
        }
        return isoString
    }
}

struct AddSubscriptionSheet: View {
    @EnvironmentObject var state: AppState
    @Environment(\.dismiss) var dismiss

    @State private var name = ""
    @State private var url = ""
    @State private var autoRefresh = true
    @State private var refreshInterval: Double = 60
    @State private var isAdding = false

    var canSubmit: Bool {
        !name.trimmingCharacters(in: .whitespaces).isEmpty &&
        !url.trimmingCharacters(in: .whitespaces).isEmpty
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("添加订阅")
                .font(.headline)

            Group {
                VStack(alignment: .leading, spacing: 4) {
                    Text("名称").font(.caption).foregroundColor(.secondary)
                    TextField("例：我的机场", text: $name)
                        .textFieldStyle(.roundedBorder)
                }

                VStack(alignment: .leading, spacing: 4) {
                    Text("订阅链接").font(.caption).foregroundColor(.secondary)
                    TextField("https://...", text: $url)
                        .textFieldStyle(.roundedBorder)
                }

                Toggle("自动刷新", isOn: $autoRefresh)

                if autoRefresh {
                    VStack(alignment: .leading, spacing: 4) {
                        HStack {
                            Text("刷新周期").font(.caption).foregroundColor(.secondary)
                            Spacer()
                            Text("\(Int(refreshInterval)) 分钟")
                                .font(.caption)
                                .foregroundColor(.secondary)
                        }
                        Slider(value: $refreshInterval, in: 1...300, step: 1)
                        HStack {
                            Text("1 分钟").font(.system(size: 9)).foregroundColor(.secondary.opacity(0.7))
                            Spacer()
                            Text("300 分钟").font(.system(size: 9)).foregroundColor(.secondary.opacity(0.7))
                        }
                    }
                }
            }

            HStack {
                Button("取消") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Spacer()
                Button("添加") {
                    Task {
                        isAdding = true
                        await state.addSubscription(name: name, url: url, autoRefresh: autoRefresh, refreshInterval: Int(refreshInterval))
                        isAdding = false
                        dismiss()
                    }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(!canSubmit || isAdding)
                .buttonStyle(.borderedProminent)
            }
        }
        .padding(20)
        .frame(width: 340)
    }
}

struct SubLogSheet: View {
    @EnvironmentObject var state: AppState
    @Environment(\.dismiss) var dismiss
    let subID: String
    let title: String
    @State private var logs: [SubLogEntry] = []
    @State private var isLoading = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text(title)
                    .font(.headline)
                Spacer()
                Button {
                    Task {
                        isLoading = true
                        logs = await state.fetchSubLogs(subID: subID)
                        isLoading = false
                    }
                } label: {
                    Image(systemName: "arrow.clockwise")
                        .font(.system(size: 13))
                }
                .buttonStyle(.plain)
                .disabled(isLoading)

                Button {
                    Task {
                        await state.clearSubLogs()
                        logs = []
                    }
                } label: {
                    Image(systemName: "trash")
                        .font(.system(size: 13))
                        .foregroundColor(.red.opacity(0.7))
                }
                .buttonStyle(.plain)

                Button("关闭") { dismiss() }
                    .keyboardShortcut(.cancelAction)
            }

            if logs.isEmpty {
                ContentUnavailableView(
                    "暂无日志",
                    systemImage: "doc.text",
                    description: Text("订阅刷新记录将显示在这里")
                )
                .frame(maxHeight: .infinity)
            } else {
                List(logs) { entry in
                    VStack(alignment: .leading, spacing: 4) {
                        HStack {
                            Circle()
                                .fill(entry.success ? Color.green : Color.red)
                                .frame(width: 6, height: 6)
                            Text(entry.subName)
                                .font(.system(size: 12, weight: .medium))
                            Spacer()
                            Text(formatLogTime(entry.time))
                                .font(.system(size: 10))
                                .foregroundColor(.secondary)
                        }
                        Text(entry.message)
                            .font(.system(size: 11))
                            .foregroundColor(.secondary)
                            .lineLimit(2)
                        if !entry.ips.isEmpty {
                            Text("IP: \(entry.ips.prefix(5).joined(separator: ", "))\(entry.ips.count > 5 ? "..." : "")")
                                .font(.system(size: 10))
                                .foregroundColor(.secondary.opacity(0.7))
                        }
                    }
                    .padding(.vertical, 2)
                }
                .listStyle(.plain)
                .scrollContentBackground(.hidden)
            }
        }
        .padding(16)
        .frame(width: 420, height: 400)
        .task {
            isLoading = true
            logs = await state.fetchSubLogs(subID: subID)
            isLoading = false
        }
        .task {
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 15_000_000_000)
                logs = await state.fetchSubLogs(subID: subID)
            }
        }
    }

    func formatLogTime(_ isoString: String) -> String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let date = formatter.date(from: isoString) {
            let df = DateFormatter()
            df.dateFormat = "MM-dd HH:mm"
            return df.string(from: date)
        }
        let formatter2 = ISO8601DateFormatter()
        formatter2.formatOptions = [.withInternetDateTime]
        if let date = formatter2.date(from: isoString) {
            let df = DateFormatter()
            df.dateFormat = "MM-dd HH:mm"
            return df.string(from: date)
        }
        return isoString
    }
}
