import AppKit
import SwiftUI

@MainActor
final class CapsuleWindowController {
    static let shared = CapsuleWindowController()
    private var panel: NSPanel?
    private var screenObserver: NSObjectProtocol?

    func render(model: AppModel) {
        guard let signals = model.signals, signals.capsule.enabled else {
            panel?.orderOut(nil)
            return
        }
        if panel == nil {
            let window = NSPanel(contentRect: .zero,
                styleMask: [.borderless, .nonactivatingPanel], backing: .buffered, defer: false)
            window.level = .normal
            window.isFloatingPanel = false
            window.hidesOnDeactivate = false
            window.isReleasedWhenClosed = false
            window.isMovableByWindowBackground = true
            window.backgroundColor = .windowBackgroundColor
            window.hasShadow = true
            panel = window
            window.contentView = NSHostingView(rootView: CapsuleView(model: model))
            screenObserver = NotificationCenter.default.addObserver(
                forName: NSApplication.didChangeScreenParametersNotification, object: nil, queue: .main
            ) { [weak self] _ in
                Task { @MainActor in self?.clampToScreen() }
            }
            resetPosition()
        }
        guard let panel else { return }
        panel.title = model.text(.capsuleTitle)
        panel.setContentSize(NSSize(
            width: CGFloat(signals.capsule.collapsed ? model.tokens.capsuleCollapsedWidth : model.tokens.capsuleWidth),
            height: CGFloat(model.tokens.capsuleHeight)))
        clampToScreen()
        if !panel.isVisible { panel.orderFrontRegardless() }
    }

    func hide() { panel?.orderOut(nil) }

    func close() {
        if let screenObserver { NotificationCenter.default.removeObserver(screenObserver) }
        screenObserver = nil
        panel?.close()
        panel = nil
    }

    func resetPosition() {
        guard let panel, let frame = NSScreen.main?.visibleFrame else { return }
        panel.setFrameOrigin(NSPoint(x: frame.maxX - panel.frame.width - 16,
                                    y: frame.minY + 16))
        clampToScreen()
    }

    private func clampToScreen() {
        guard let panel else { return }
        let screen = NSScreen.screens.first { $0.visibleFrame.intersects(panel.frame) } ?? NSScreen.main
        guard let frame = screen?.visibleFrame else { return }
        let origin = NSPoint(
            x: min(max(panel.frame.minX, frame.minX), max(frame.minX, frame.maxX - panel.frame.width)),
            y: min(max(panel.frame.minY, frame.minY), max(frame.minY, frame.maxY - panel.frame.height)))
        panel.setFrameOrigin(origin)
    }
}

private struct CapsuleView: View {
    @ObservedObject var model: AppModel
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        HStack(spacing: CGFloat(model.tokens.controlSpacing)) {
            VStack(alignment: .leading, spacing: 3) {
                Text(model.signals?.quota.value ?? model.text(.signalsUnavailable))
                    .font(.body.weight(.semibold)).monospacedDigit()
                if model.signals?.capsule.collapsed != true {
                    Text(model.signals?.quota.detail ?? model.text(.signalsNoSource))
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            .lineLimit(1)
            Spacer(minLength: 0)
            Button(model.text(.capsuleOpenPanel)) {
                openWindow(id: "main")
                NSApplication.shared.activate(ignoringOtherApps: true)
            }
        }
        .padding(CGFloat(model.tokens.controlSpacing))
        .accessibilityElement(children: .contain)
    }
}
