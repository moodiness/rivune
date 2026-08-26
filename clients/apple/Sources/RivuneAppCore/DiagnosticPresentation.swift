import RivuneAPI
import SwiftUI
import UniformTypeIdentifiers
#if os(iOS) || os(visionOS)
import UIKit
#endif

#if os(tvOS)
import CoreImage
import CoreImage.CIFilterBuiltins

struct RivuneTelevisionDiagnosticView: View {
    let report: String
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            ScrollView {
                VStack(alignment: .leading, spacing: 24) {
                    HStack {
                        VStack(alignment: .leading, spacing: 8) {
                            Text("Private diagnostics").font(.largeTitle.bold())
                            Text("Nothing is uploaded. Scan the QR code or photograph the allowlisted report, then close this view.")
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        Button("Done") { dismiss() }.buttonStyle(.borderedProminent)
                    }
                    if let image = rivuneQRCode(report, correctionLevel: "L") {
                        image
                            .interpolation(.none)
                            .resizable()
                            .scaledToFit()
                            .frame(width: 520, height: 520)
                            .padding(24)
                            .background(.white, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
                            .frame(maxWidth: .infinity)
                    } else {
                        Text("The recent event history is too large for one QR code. The complete bounded report remains visible below.")
                            .foregroundStyle(.yellow)
                    }
                    Text(report)
                        .font(.system(.body, design: .monospaced))
                        .foregroundStyle(.white)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                .padding(48)
            }
        }
        .preferredColorScheme(.dark)
    }
}

struct RivuneTelevisionUpdateView: View {
    let update: RivuneAppleUpdate
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            VStack(alignment: .leading, spacing: 28) {
                HStack {
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Rivune \(update.latestVersion)").font(.largeTitle.bold())
                        Text("Scan the QR code to open the verified GitHub release. The public tvOS package is unsigned and must be signed before installation.")
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    Button("Done") { dismiss() }.buttonStyle(.borderedProminent)
                }
                if let image = rivuneQRCode(update.releaseURL.absoluteString, correctionLevel: "M") {
                    image
                        .interpolation(.none)
                        .resizable()
                        .scaledToFit()
                        .frame(width: 620, height: 620)
                        .padding(28)
                        .background(.white, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
                        .frame(maxWidth: .infinity)
                }
                Text(update.releaseURL.absoluteString)
                    .font(.system(.body, design: .monospaced))
                    .frame(maxWidth: .infinity, alignment: .center)
            }
            .padding(48)
        }
        .preferredColorScheme(.dark)
    }
}

private func rivuneQRCode(_ value: String, correctionLevel: String) -> Image? {
    let filter = CIFilter.qrCodeGenerator()
    filter.message = Data(value.utf8)
    filter.correctionLevel = correctionLevel
    guard let output = filter.outputImage?.transformed(by: CGAffineTransform(scaleX: 12, y: 12)),
          let image = CIContext(options: [.useSoftwareRenderer: false]).createCGImage(output, from: output.extent) else {
        return nil
    }
    return Image(decorative: image, scale: 1)
}
#else
struct RivuneDiagnosticTextDocument: FileDocument {
    static var readableContentTypes: [UTType] { [.utf8PlainText] }
    static var writableContentTypes: [UTType] { [.utf8PlainText] }

    let report: String

    init(report: String) {
        self.report = report
    }

    init(configuration: ReadConfiguration) throws {
        guard let data = configuration.file.regularFileContents,
              data.count <= rivuneMaximumDiagnosticReportBytes,
              let report = String(data: data, encoding: .utf8) else {
            throw CocoaError(.fileReadCorruptFile)
        }
        self.report = report
    }

    func fileWrapper(configuration: WriteConfiguration) throws -> FileWrapper {
        FileWrapper(regularFileWithContents: Data(report.utf8))
    }
}

extension UTType {
    static let rivuneProfileArchive = UTType(exportedAs: "io.rivune.profile-archive", conformingTo: .json)
}

struct RivuneProfileArchiveFileDocument: FileDocument {
    static var readableContentTypes: [UTType] { [.rivuneProfileArchive, .json] }
    static var writableContentTypes: [UTType] { [.rivuneProfileArchive] }

    let archive: ProfileArchiveDocument

    init(archive: ProfileArchiveDocument) { self.archive = archive }

    init(configuration: ReadConfiguration) throws {
        guard let data = configuration.file.regularFileContents else {
            throw ProfileArchiveError.invalidDocument
        }
        archive = try ProfileArchiveDocument(data: data)
    }

    func fileWrapper(configuration: WriteConfiguration) throws -> FileWrapper {
        FileWrapper(regularFileWithContents: try archive.encodedData())
    }
}

#if os(iOS) || os(visionOS)
@MainActor
func copyRivuneDiagnosticReport(_ report: String) -> Bool {
    UIPasteboard.general.setItems(
        [[UTType.utf8PlainText.identifier: report]],
        options: [
            .localOnly: true,
            .expirationDate: Date().addingTimeInterval(60),
        ]
    )
    return true
}
#endif
#endif
