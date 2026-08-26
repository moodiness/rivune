import XCTest
@testable import RivuneAPI
@testable import RivuneAppCore

final class V22AccessibilityPolicyTests: XCTestCase {
  func testSystemPreferencesFollowSystemSignals() {
    let preferences = AccessibilityPreferencesDocument(
      revision: 1, reducedMotion: .system, highContrast: .system, textScale: 115,
      captions: .system, audioDescription: true, focusIndicators: .enhanced)
    XCTAssertEqual(
      RivuneAccessibilityPolicy.resolve(
        preferences, systemReduceMotion: true, systemHighContrast: false,
        systemCaptionsEnabled: true),
      RivuneEffectiveAccessibility(
        reduceMotion: true, highContrast: false, textScale: 115, captionsEnabled: true,
        audioDescription: true, enhancedFocusIndicators: true))
  }

  func testExplicitPreferencesOverrideSystemAndInvalidScaleIsBounded() {
    let preferences = AccessibilityPreferencesDocument(
      revision: 2, reducedMotion: .noPreference, highContrast: .more, textScale: 999,
      captions: .off, audioDescription: false, focusIndicators: .standard)
    XCTAssertEqual(
      RivuneAccessibilityPolicy.resolve(
        preferences, systemReduceMotion: true, systemHighContrast: false,
        systemCaptionsEnabled: true),
      RivuneEffectiveAccessibility(
        reduceMotion: false, highContrast: true, textScale: 100, captionsEnabled: false,
        audioDescription: false, enhancedFocusIndicators: false))
  }


  func testDuplicateNotificationTitlesHaveDistinctActionLabels() throws {
    let first = try JSONDecoder().decode(MediaNotification.self, from: Data(#"{"id":"1","kind":"movie-release","titleId":"11111111-1111-4111-8111-111111111111","title":"Arrival","availableAt":"2026-08-26T00:00:00Z","createdAt":"2026-08-25T00:00:00Z"}"#.utf8))
    let second = try JSONDecoder().decode(MediaNotification.self, from: Data(#"{"id":"2","kind":"episode-available","titleId":"22222222-2222-4222-8222-222222222222","title":"Arrival","availableAt":"2026-08-27T00:00:00Z","createdAt":"2026-08-25T00:00:00Z"}"#.utf8))
    let firstLabel = rivuneMediaNotificationActionLabel("Dismiss", notification: first)
    let secondLabel = rivuneMediaNotificationActionLabel("Dismiss", notification: second)
    XCTAssertNotEqual(firstLabel, secondLabel)
    XCTAssertTrue(firstLabel.contains("movie release"))
    XCTAssertTrue(secondLabel.contains("episode available"))
  }

  func testDuplicateAddonNamesHaveCodeSpecificSafeLabels() throws {
    let timeout = try incident(code: "timeout")
    let invalid = try incident(code: "invalid_response")
    let timeoutLabel = rivuneIncidentActionLabel("Acknowledge", incident: timeout)
    let invalidLabel = rivuneIncidentActionLabel("Acknowledge", incident: invalid)
    XCTAssertNotEqual(timeoutLabel, invalidLabel)
    XCTAssertTrue(timeoutLabel.contains("Catalog incident, timeout"))
    XCTAssertTrue(invalidLabel.contains("Catalog incident, invalid response"))
    XCTAssertFalse(invalidLabel.contains("http"))
    XCTAssertFalse(invalidLabel.contains("token"))
  }

  func testStatusAnnouncementsDeduplicateRerendersButAllowTransitions() {
    var deduplicator = RivuneAnnouncementDeduplicator()
    XCTAssertEqual(deduplicator.take("Loading accessibility preferences"), "Loading accessibility preferences")
    XCTAssertNil(deduplicator.take("Loading accessibility preferences"))
    XCTAssertEqual(deduplicator.take("Accessibility preferences failed"), "Accessibility preferences failed")
    XCTAssertNil(deduplicator.take("Accessibility preferences failed"))
    XCTAssertNil(deduplicator.take(nil))
  }

  private func incident(code: String) throws -> AddonIncident {
    try JSONDecoder().decode(AddonIncident.self, from: Data("""
      {"id":"33333333-3333-4333-8333-333333333333","profileId":"11111111-1111-4111-8111-111111111111","addonId":"66666666-6666-4666-8666-666666666666","addonName":"Catalog","code":"\(code)","state":"open","impact":"availability","occurrenceCount":1,"firstOccurredAt":"2026-08-26T00:00:00Z","lastOccurredAt":"2026-08-26T00:00:00Z","lastSuccessAt":null,"recoveryStartedAt":null,"resolvedAt":null,"acknowledgedAt":null,"acknowledgedByUserId":null,"updatedAt":"2026-08-26T00:00:00Z"}
      """.utf8))
  }
}