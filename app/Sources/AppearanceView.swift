import AppKit
import SwiftUI
import UniformTypeIdentifiers

struct AppearanceView: View {
    @ObservedObject var model: AppModel
    @State private var draft = AppearanceDraft()
    @State private var isShowingImageImporter = false
    @State private var importedImage: BackendAppearanceImageResult?
    @State private var localErrorKey: String?
    @State private var pendingOperations = 0
    @State private var operationVersion: UInt64 = 0

    private var isBusy: Bool { pendingOperations > 0 }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: CGFloat(model.tokens.sectionSpacing)) {
                VStack(alignment: .leading, spacing: CGFloat(model.tokens.compactSpacing)) {
                    Text(model.text(.appearanceTitle))
                        .font(.largeTitle.weight(.semibold))
                    Text(model.text(.appearanceSubtitle))
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }

                if let snapshot = model.appearance {
                    connectionStatus(snapshot)
                    Divider()
                    settings(snapshot)
                    Divider()
                    actions(snapshot)
                    savedSettings(snapshot)
                } else {
                    ProgressView(model.text(.appearanceBusy))
                }
            }
            .frame(maxWidth: CGFloat(model.tokens.contentMaxWidth), alignment: .leading)
            .padding(CGFloat(model.tokens.pagePadding))
        }
        .background(Color(nsColor: .windowBackgroundColor))
        .navigationTitle(model.text(.navAppearance))
        .onAppear {
            if let snapshot = model.appearance { draft.receive(snapshot) }
        }
        .onChange(of: model.appearance) { snapshot in
            if let snapshot { draft.receive(snapshot) }
        }
        .fileImporter(
            isPresented: $isShowingImageImporter,
            allowedContentTypes: [.png, .jpeg, .webP],
            allowsMultipleSelection: false,
            onCompletion: selectImage
        )
    }

    private func connectionStatus(_ snapshot: BackendAppearanceSnapshot) -> some View {
        VStack(alignment: .leading, spacing: CGFloat(model.tokens.controlSpacing)) {
            Label(
                model.canSendAppearanceCommands
                    ? snapshot.targetId.map { model.text(.appearanceTarget, ["target": $0]) }
                        ?? model.text(.appearanceTargetUnconfirmed)
                    : model.text(.appearanceTargetUnconfirmed),
                systemImage: "macwindow"
            )
            .font(.headline)
            .accessibilityIdentifier("appearance.target")

            Text(model.canSendAppearanceCommands
                 ? model.appearanceText(snapshot.statusTextKey)
                 : model.text(.appBackendNotRunning))
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)

            if snapshot.preview != nil, model.canSendAppearanceCommands {
                Label(model.text(.appearancePreviewOnly), systemImage: "eye")
                    .font(.callout)
            }
            if draft.hasLocalEdits {
                Label(model.text(.appearanceLocalDraft), systemImage: "pencil")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            if let key = snapshot.errorTextKey ?? localErrorKey {
                Label(model.appearanceText(key), systemImage: "exclamationmark.triangle")
                    .foregroundStyle(model.tokens.warningColorValue)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("appearance.error")
            }
        }
    }

    private func settings(_ snapshot: BackendAppearanceSnapshot) -> some View {
        VStack(alignment: .leading, spacing: CGFloat(model.tokens.sectionSpacing)) {
            optionPicker(.appearanceTheme, selection: $draft.theme, options: snapshot.options.themes)
            optionPicker(.appearanceLayout, selection: $draft.readingLayout, options: snapshot.options.readingLayouts)
            optionPicker(.appearanceBackground, selection: $draft.backgroundMode, options: snapshot.options.backgroundModes)

            if draft.backgroundMode == "solid" {
                LabeledContent(model.text(.appearanceSolidColor)) {
                    TextField(model.text(.appearanceSolidColor), text: $draft.solidColor)
                        .font(.system(.body, design: .monospaced))
                        .textFieldStyle(.roundedBorder)
                        .frame(maxWidth: 220)
                        .accessibilityIdentifier("appearance.solidColor")
                }
            }
            if draft.backgroundMode == "local-image" {
                VStack(alignment: .leading, spacing: CGFloat(model.tokens.compactSpacing)) {
                    LabeledContent(model.text(.appearanceImage)) {
                        Button(model.text(.appearanceChooseImage), systemImage: "photo") {
                            isShowingImageImporter = true
                        }
                        .disabled(!snapshot.actions.importImage || !model.canSendAppearanceCommands)
                        .accessibilityIdentifier("appearance.chooseImage")
                    }
                    Text(imageDescription)
                        .font(.callout)
                    Text(model.text(.appearanceImageLimits))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            if draft.backgroundMode != "off" {
                LabeledContent(model.text(.appearanceOverlayOpacity)) {
                    HStack(spacing: CGFloat(model.tokens.controlSpacing)) {
                        Slider(
                            value: Binding(
                                get: { Double(draft.overlayOpacity) },
                                set: { draft.overlayOpacity = Int($0) }
                            ),
                            in: Double(snapshot.options.overlayMinimum)...Double(snapshot.options.overlayMaximum),
                            step: 1
                        )
                        .accessibilityLabel(model.text(.appearanceOverlayOpacity))
                        .accessibilityIdentifier("appearance.overlayOpacity")
                        Text(Double(draft.overlayOpacity) / 100, format: .percent.precision(.fractionLength(0)))
                            .monospacedDigit()
                            .frame(minWidth: 45, alignment: .trailing)
                    }
                    .frame(maxWidth: 280)
                }
            }
        }
        .disabled(isBusy)
    }

    private func optionPicker(
        _ title: PresentationTextKey, selection: Binding<String>, options: [BackendAppearanceOption]
    ) -> some View {
        LabeledContent(model.text(title)) {
            Picker(model.text(title), selection: selection) {
                ForEach(options, id: \.value) { option in
                    Text(model.appearanceText(option.textKey))
                        .tag(option.value)
                        .disabled(!option.supported)
                        .help(option.supported ? "" : model.text(.appearanceUnsupportedOption))
                }
            }
            .labelsHidden()
            .pickerStyle(.menu)
            .frame(maxWidth: 280)
            .accessibilityLabel(model.text(title))
            .accessibilityIdentifier(title.rawValue)
        }
    }

    private func actions(_ snapshot: BackendAppearanceSnapshot) -> some View {
        VStack(alignment: .leading, spacing: CGFloat(model.tokens.controlSpacing)) {
            ViewThatFits(in: .horizontal) {
                HStack(spacing: CGFloat(model.tokens.controlSpacing)) { actionButtons(snapshot) }
                VStack(alignment: .leading, spacing: CGFloat(model.tokens.controlSpacing)) { actionButtons(snapshot) }
            }
            if isBusy {
                ProgressView(model.text(.appearanceBusy))
                    .controlSize(.small)
            }
        }
    }

    @ViewBuilder
    private func actionButtons(_ snapshot: BackendAppearanceSnapshot) -> some View {
        Button(model.text(.appearancePreview)) {
            let settings = draft.settings
            run { try await model.previewAppearance(settings) }
        }
        .buttonStyle(.bordered)
        .disabled(isBusy || !snapshot.actions.preview || !model.canSendAppearanceCommands)
        .accessibilityIdentifier("appearance.preview")

        Button(model.text(.appearanceApply)) {
            let settings = draft.settings
            run { try await model.applyAppearance(settings) }
        }
        .buttonStyle(.borderedProminent)
        .disabled(isBusy || !snapshot.actions.apply || !model.canSendAppearanceCommands)
        .accessibilityIdentifier("appearance.apply")

        if snapshot.preview != nil || snapshot.actions.cancelPreview {
            Button(model.text(.appearanceCancelPreview)) {
                run { try await model.cancelAppearancePreview() }
            }
            .buttonStyle(.bordered)
            .disabled(!snapshot.actions.cancelPreview || !model.canSendAppearanceCommands)
            .accessibilityIdentifier("appearance.cancelPreview")
        }

        Button(model.text(.appearanceRestoreNative)) {
            run { try await model.restoreNativeAppearance() }
        }
        .buttonStyle(.bordered)
        .disabled(isBusy || !snapshot.actions.restoreNative || !model.canSendAppearanceCommands)
        .accessibilityIdentifier("appearance.restoreNative")
    }

    private func savedSettings(_ snapshot: BackendAppearanceSnapshot) -> some View {
        VStack(alignment: .leading, spacing: CGFloat(model.tokens.compactSpacing)) {
            Text(model.text(.appearanceSavedSettings))
                .font(.callout.weight(.medium))
            Text([
                optionTitle(snapshot.saved.theme, in: snapshot.options.themes),
                optionTitle(snapshot.saved.readingLayout, in: snapshot.options.readingLayouts),
                optionTitle(snapshot.saved.backgroundMode, in: snapshot.options.backgroundModes),
            ].joined(separator: " · "))
            .font(.callout)
            .foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
        }
    }

    private func optionTitle(_ value: String, in options: [BackendAppearanceOption]) -> String {
        options.first(where: { $0.value == value }).map { model.appearanceText($0.textKey) }
            ?? model.text(.appearanceUnsupportedOption)
    }

    private var imageDescription: String {
        guard let assetId = draft.imageAssetId else { return model.text(.appearanceImageNone) }
        guard let image = importedImage, image.assetId == assetId else {
            return model.text(.appearanceImageSelected)
        }
        return model.text(.appearanceImageImported, [
            "width": String(image.width), "height": String(image.height), "format": image.format,
        ])
    }

    private func run(_ operation: @escaping @MainActor () async throws -> BackendAppearanceSnapshot) {
        operationVersion &+= 1
        let version = operationVersion
        pendingOperations += 1
        localErrorKey = nil
        Task { @MainActor in
            defer { pendingOperations -= 1 }
            do {
                let snapshot = try await operation()
                guard version == operationVersion else { return }
                draft.accept(snapshot)
            } catch {
                guard version == operationVersion else { return }
                localErrorKey = PresentationTextKey.appearanceRequestFailed.rawValue
            }
        }
    }

    private func selectImage(_ result: Result<[URL], Error>) {
        switch result {
        case let .success(urls):
            guard let url = urls.first else { return }
            operationVersion &+= 1
            let version = operationVersion
            pendingOperations += 1
            localErrorKey = nil
            Task { @MainActor in
                defer { pendingOperations -= 1 }
                do {
                    let image = try await model.importAppearanceImage(from: url)
                    guard version == operationVersion else { return }
                    importedImage = image
                    draft.imageAssetId = image.assetId
                } catch {
                    guard version == operationVersion else { return }
                    localErrorKey = PresentationTextKey.appearanceRequestFailed.rawValue
                }
            }
        case let .failure(error):
            if (error as? CocoaError)?.code != .userCancelled {
                localErrorKey = PresentationTextKey.appearanceRequestFailed.rawValue
            }
        }
    }
}
