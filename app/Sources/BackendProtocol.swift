import Foundation

enum BackendUpdateChannel: String, Codable, Sendable {
    case stable
    case beta
}

struct AppLanguageOption: Identifiable, Hashable, Sendable {
    let id: String
    let title: String
}

struct GitHubAsset: Codable, Equatable, Sendable {
    let name: String
    let browserDownloadURL: URL?

    private enum CodingKeys: String, CodingKey {
        case name
        case browserDownloadURL = "browser_download_url"
    }
}

struct GitHubRelease: Codable, Equatable, Identifiable, Sendable {
    let tagName: String
    let draft: Bool
    let prerelease: Bool
    let publishedAt: Date?
    let htmlURL: URL?
    let assets: [GitHubAsset]

    var id: String { tagName }

    private enum CodingKeys: String, CodingKey {
        case tagName = "tag_name"
        case draft, prerelease
        case publishedAt = "published_at"
        case htmlURL = "html_url"
        case assets
    }
}

struct BackendUpdateSnapshot: Codable, Equatable, Sendable {
    let channel: BackendUpdateChannel
    let packageChannel: BackendUpdateChannel
    let autoCheck: Bool
    let checking: Bool
    let latestRelease: GitHubRelease?
    let lastError: String?
    let lastCheckAt: Date?
    let pendingUpdate: GitHubRelease?
    let currentVersion: String
    let buildNumber: String
    let hasNewerVersion: Bool
    let updateAvailable: Bool
    let latestVersionString: String
    let latestVersionIsSkipped: Bool
    let downloadURL: URL?
}

struct BackendAppStatus: Codable, Equatable, Sendable {
    enum Kind: String, Codable, Sendable {
        case starting
        case launchingCodex
        case codexNotRunning
        case waitingForCDP
        case restartRequired
        case waitingForPage
        case connected
        case disabled
        case recoveryPending
        case error
    }

    let kind: Kind
    let targetCount: Int?
    let message: String?
}

struct BackendAppSnapshot: Codable, Equatable, Sendable {
    let protocolVersion: Int
    let presentation: BackendPresentationContract
    let status: BackendAppStatus
    let appearance: BackendAppearanceSnapshot
    let signals: BackendSignalsSnapshot
    let enabled: Bool
    let disableGPUAcceleration: Bool
    let developerMode: Bool
    let developerAllowUnknownNode: Bool
    let packages: [TweakPackage]
    let disabledPackageIDs: [String]
    let buildingPackageIDs: [String]
    let exportingPackageIDs: [String]
    let packageBuildErrors: [String: String]
    let packageRuntimeErrors: [String: String]
    let packagePayloadErrors: [String: String]
    let packageDependencyStatuses: [String: [TweakPackageDependencyStatus]]
    let packageDependencyIssues: [String: [String]]
    let packagePriorityConstraints: [String: TweakPackagePriorityConstraint]
    let nodeEnvironment: NodeEnvironment?
    let checkingNode: Bool
    let gitEnvironment: GitEnvironment?
    let checkingGit: Bool
    let checkingRemoteUpdates: Bool
    let remotePackageUpdates: [String: TweakPackageRemoteUpdate]
    let remotePackageErrors: [String: String]
    let installingPackageIDs: [String]
    let installingRemotePackage: Bool
    let remoteOperationMessage: String?
    let remoteOperationError: String?
    let installingLocalPackage: Bool
    let localOperationMessage: String?
    let localOperationError: String?
    let tweaksDirectory: String
    let packagesDirectory: String
    let logPath: String
    let logText: String
    let enabledPackageCount: Int
    let activePackageCount: Int
    let update: BackendUpdateSnapshot
}

struct BackendInitializeParams: Encodable, Sendable {
    let applicationSupportDirectory: String?
    let cacheDirectory: String?
    let bundledPackagesDirectory: String?
    let skillPath: String?
    let preferredLanguages: [String]
    let currentVersion: String
    let buildNumber: String
}

struct BackendAccepted: Decodable, Sendable {
    let accepted: Bool?
    let shutdown: Bool?
}

struct AppearanceSettingsParameter: Encodable, Sendable {
    let settings: BackendAppearanceSettings
}

struct AppearanceImageParameter: Encodable, Sendable {
    let path: String
}

// Local form state only: Go validates values and owns saved/preview settings.
// Incoming snapshots may refresh the baseline without replacing unsent edits.
struct AppearanceDraft: Equatable {
    var theme: String
    var readingLayout: String
    var backgroundMode: String
    var solidColor: String
    var imageAssetId: String?
    var overlayOpacity: Int
    private var baseline: BackendAppearanceSettings

    init(settings: BackendAppearanceSettings = GeneratedAppearanceDefaults.settings) {
        theme = settings.theme
        readingLayout = settings.readingLayout
        backgroundMode = settings.backgroundMode
        solidColor = settings.solidColor
        imageAssetId = settings.imageAssetId
        overlayOpacity = settings.overlayOpacity
        baseline = settings
    }

    var settings: BackendAppearanceSettings {
        BackendAppearanceSettings(
            theme: theme, readingLayout: readingLayout, backgroundMode: backgroundMode,
            solidColor: solidColor, imageAssetId: imageAssetId, overlayOpacity: overlayOpacity
        )
    }

    var hasLocalEdits: Bool { settings != baseline }

    mutating func receive(_ snapshot: BackendAppearanceSnapshot) {
        let incoming = snapshot.preview ?? snapshot.saved
        let preserveEdits = hasLocalEdits
        baseline = incoming
        if !preserveEdits { self = AppearanceDraft(settings: incoming) }
    }

    mutating func accept(_ snapshot: BackendAppearanceSnapshot) {
        self = AppearanceDraft(settings: snapshot.preview ?? snapshot.saved)
    }
}
