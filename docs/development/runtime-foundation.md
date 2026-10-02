# Runtime foundation

This document owns the reliability requirements established before new companion features. Product behavior belongs in the PRD; delivery order and evidence status belong in the [implementation plan](../desktop-enhancement/IMPLEMENTATION.md). Extend the existing Go/native architecture and its tests at coherent boundaries.

## Diagnostics

Go owns runtime diagnostics. Keep INFO/WARN/ERROR, UTC timestamps, serialized writes and the existing log preview. Bound disk retention and each entry, and make persistence failure visible through the existing diagnostic surface with a nonrecursive stderr fallback. stdout is reserved for JSON RPC.

Record event summaries, operation identity and error categories by default. Node stderr, compiler output, session text, credential material and arbitrary third-party payloads are not safe log fields. Known-token masking and control-character normalization provide additional protection, not proof that arbitrary content is private-data-free. User-triggered diagnostic export must retain these boundaries.

The runtime uses the isolated companion state root. Native startup diagnostics have their own bounded fallback because sidecar initialization may fail before Go logging exists; they must report safe summaries rather than a raw stderr transcript. Log level controls and telemetry backends are unnecessary for the first release.

Go keeps at most three 2 MiB log files with 4 KiB diagnostic bodies; Windows startup diagnostics keep two 256 KiB files with 1 KiB entries. Native stderr drains fixed-size chunks and records counts, not the external text. Known credential formats and user paths are redacted; this is not a claim that arbitrary text is classified for privacy.

## Transport and errors

Every RPC response and event has a validated, newline-delimited JSON frame. Encoding failures, short writes and disconnected output fail the transport; they must not report successful delivery. A failed transport cancels the application lifecycle and runs the same bounded shutdown as explicit exit. Preserve error causes for `errors.Is` and tests while returning concise, actionable client messages.

Business rejection, unavailable capability, cancellation and incomplete restoration are different outcomes. Retrying a failed operation must not observe an in-memory setting as successfully persisted. Async operations report completion or failure through the shared snapshot rather than a misleading early success message.

## Configuration and data ownership

Keep the existing schema-checked JSON and same-directory atomic replacement. Validate a candidate, persist it, then commit in-memory state and trigger side effects. Failure preserves the last accepted state. Unknown versions and corrupt files produce a usable diagnostic; preserve the original bytes until the explicitly defined recovery action runs.

Application, package, data/cache, update and uninstall identities are one ownership boundary. First fork launch starts with independent state and permissions. Existing upstream settings, enabled packages and Node trust are not automatically imported. Source module/type names can stay stable while distribution identifiers change.

## Cancellation and process ownership

A single runtime operation boundary serializes attachment, injection and restoration. Stop or shutdown cancels in-flight work; stale work cannot publish an active state or restart Node after stop. Network calls respect the operation context, including cancellation after connection establishment.

Use one overall shutdown budget covering Node stop, in-flight cancellation and page cleanup. Native clients wait for this budget plus transport margin. Forced termination, when unavoidable after the budget, addresses only the private sidecar or its owned worker; it cannot recursively terminate an official Codex process launched from that sidecar.

Official process identity and local listener ownership must be verified before CDP effects. PID alone, an executable name or a responding loopback port does not prove identity. Bind process creation identity and endpoint; reject replacement, ambiguity, redirects and foreign WebSocket authority. An existing foreign enhancement runtime owns its renderer until explicitly stopped; the companion must not remove it to claim the window.

Page work binds the companion instance and an activation epoch. Stop invalidates this lease before cleanup; delayed evaluation cannot reactivate a stopped epoch. A fixed-size inactive lease remains until page exit, while owned style/DOM/events and successful callbacks are removed. Failed callbacks are retained for retry and cannot be reported as restored. A stopped sidecar does not prove the official debug listener was closed.

## Test and dependency boundaries

Ordinary Controller and RPC tests inject fake platform and runtime collaborators. `DisableBackground` controls scheduling, not capability: it is not sufficient isolation. Unit tests use temporary project roots and synthetic HTTP/WebSocket/DOM fixtures. Failure cases include cancellation, cleanup callback exceptions, partial target failure, output failure and failed persistence.

Pure Go tests, generated contract checks and native build tests complement runtime validation. Direct product dependencies stay small: Go standard library plus the existing WebSocket library, WinUI/.NET and SwiftUI, and Node only for package build or explicitly authorized package backends. Add a library when it solves a demonstrated requirement with a clear contract; avoid new logging, state-machine or service frameworks for existing seams.

Run [CONTRIBUTING](../../CONTRIBUTING.md) checks before delivery. Missing optional navigation tooling does not grant permission to bypass a safety control; direct source navigation is available when an index or language server is unavailable. Each PR receives an independent agent review, fixes and affected retests before merge.
