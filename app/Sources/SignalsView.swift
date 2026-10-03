import SwiftUI

struct SignalsView: View {
    @ObservedObject var model: AppModel

    var body: some View {
        VStack(alignment: .leading, spacing: CGFloat(model.tokens.controlSpacing)) {
            Text(model.text(.signalsTitle)).font(.title2.weight(.semibold))
            if let signals = model.signals {
                signal(signals.task)
                signal(signals.rate)
                signal(signals.quota)
                DisclosureGroup(model.text(.signalsSources)) {
                    VStack(alignment: .leading, spacing: 6) {
                        Text(signals.task.detail)
                        Text(signals.rate.detail)
                        Text(signals.quota.detail)
                        Text(signals.quota.source)
                        Text(model.text(.signalsObservedAt) + ": " + signals.quota.observedAt)
                        Text(model.text(.signalsCacheUpdatedAt) + ": " + signals.quota.cacheUpdatedAt)
                        Text(model.text(.signalsSourceUpdatedAt) + ": " + signals.quota.sourceUpdatedAt)
                        ForEach(Array(signals.windows.enumerated()), id: \.offset) { _, window in
                            Text(window.title + " · " + window.remaining + " · " + window.resetAt)
                        }
                    }
                    .font(.caption).foregroundStyle(.secondary).textSelection(.enabled)
                }
                Toggle(model.text(.capsuleShow), isOn: Binding(
                    get: { model.signals?.capsule.enabled ?? false },
                    set: { model.setCapsule(enabled: $0, collapsed: model.signals?.capsule.collapsed ?? false) }
                ))
                HStack {
                    Button(model.text(signals.capsule.collapsed ? .capsuleExpand : .capsuleCollapse)) {
                        model.setCapsule(enabled: signals.capsule.enabled, collapsed: !signals.capsule.collapsed)
                    }
                    Button(model.text(.capsuleResetPosition)) { CapsuleWindowController.shared.resetPosition() }
                }
                .disabled(!signals.capsule.enabled)
            } else {
                Text(model.text(.signalsNoSource)).foregroundStyle(.secondary)
            }
        }
    }

    private func signal(_ value: BackendSignalPresentation) -> some View {
        LabeledContent(value.label) { Text(value.value).monospacedDigit() }
    }
}
