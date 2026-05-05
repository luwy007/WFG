import SwiftUI

struct RulesView: View {
    @EnvironmentObject var state: AppState

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text("分流规则")
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundColor(.secondary)
                Spacer()
                Text("SSH直连 · 国内直连 · 境外代理")
                    .font(.caption2)
                    .foregroundColor(.secondary)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(Color.green.opacity(0.1), in: Capsule())
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)

            Divider()

            List(state.rules) { rule in
                RuleRowView(rule: rule)
                    .listRowInsets(EdgeInsets(top: 2, leading: 8, bottom: 2, trailing: 8))
            }
            .listStyle(.plain)
            .scrollContentBackground(.hidden)

            // 说明
            VStack(alignment: .leading, spacing: 2) {
                Text("规则按顺序匹配，命中即停止。默认：SSH 直连，国内 IP/域名直连，其余走代理。")
                    .font(.caption2)
                    .foregroundColor(.secondary)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(10)
            .background(Color(NSColor.controlBackgroundColor))
        }
        .task { await state.fetchRules() }
    }
}

struct RuleRowView: View {
    let rule: Rule

    var body: some View {
        HStack(spacing: 10) {
            // 类型标签
            Text(rule.type)
                .font(.system(size: 9, weight: .bold, design: .monospaced))
                .foregroundColor(.white)
                .padding(.horizontal, 5)
                .padding(.vertical, 2)
                .background(typeColor(rule.type), in: RoundedRectangle(cornerRadius: 3))
                .frame(width: 88, alignment: .leading)

            if rule.payload.isEmpty {
                Text("(其余所有流量)")
                    .font(.system(size: 11))
                    .foregroundColor(.secondary)
                    .italic()
            } else {
                Text(rule.payload)
                    .font(.system(size: 11, design: .monospaced))
                    .lineLimit(1)
            }

            Spacer()

            // 策略
            Text(rule.policy)
                .font(.system(size: 11, weight: .medium))
                .foregroundColor(rule.policy == "DIRECT" ? .green : .accentColor)
                .padding(.horizontal, 6)
                .padding(.vertical, 2)
                .background(
                    (rule.policy == "DIRECT" ? Color.green : Color.accentColor).opacity(0.1),
                    in: Capsule()
                )
        }
        .padding(.vertical, 3)
    }

    func typeColor(_ type: String) -> Color {
        switch type {
        case "GEOIP":           return .teal
        case "GEOSITE":         return .indigo
        case "DST-PORT":        return .pink
        case "DOMAIN-SUFFIX":   return .blue
        case "DOMAIN-KEYWORD":  return .purple
        case "DOMAIN":          return .blue.opacity(0.8)
        case "IP-CIDR":         return .orange
        case "MATCH":           return .gray
        default:                return .gray
        }
    }
}
