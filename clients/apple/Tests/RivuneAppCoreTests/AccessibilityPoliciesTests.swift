import XCTest

@testable import RivuneAppCore

final class AccessibilityPoliciesTests: XCTestCase {
  func testMotionPolicyCombinesSystemSettingAndAppPreference() {
    XCTAssertFalse(
      RivuneMotionPolicy.reducesMotion(preference: .system, systemReduceMotion: false)
    )
    XCTAssertTrue(
      RivuneMotionPolicy.reducesMotion(preference: .system, systemReduceMotion: true)
    )
    XCTAssertTrue(
      RivuneMotionPolicy.reducesMotion(preference: .reduced, systemReduceMotion: false)
    )
  }

  func testSystemReduceMotionCannotBeOverriddenByFullPreference() {
    XCTAssertTrue(
      RivuneMotionPolicy.reducesMotion(preference: .full, systemReduceMotion: true)
    )
    XCTAssertFalse(
      RivuneMotionPolicy.reducesMotion(preference: .full, systemReduceMotion: false)
    )
  }

  func testAccessibilityDynamicTypeRemovesTitleLineLimit() {
    XCTAssertEqual(
      RivuneDynamicTitlePolicy.lineLimit(isAccessibilitySize: false),
      2
    )
    XCTAssertNil(
      RivuneDynamicTitlePolicy.lineLimit(isAccessibilitySize: true)
    )
  }
}
