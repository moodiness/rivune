import XCTest
@testable import RivuneAppCore

final class V22FailoverGateTests: XCTestCase {
  func testGateAllowsOnlyBoundedPreFrameSwitches() {
    var gate = RivunePlaybackFailoverGate(maximumSwitches: 2)
    XCTAssertTrue(gate.beginAdvance())
    XCTAssertTrue(gate.beginAdvance())
    XCTAssertFalse(gate.beginAdvance())
    XCTAssertEqual(gate.switchCount, 2)
  }

  func testFirstFrameAndCancellationPermanentlyCloseGate() {
    var firstFrame = RivunePlaybackFailoverGate(maximumSwitches: 3)
    firstFrame.markFirstFrame()
    XCTAssertFalse(firstFrame.canAdvance)
    XCTAssertFalse(firstFrame.beginAdvance())

    var cancelled = RivunePlaybackFailoverGate(maximumSwitches: 3)
    cancelled.cancel()
    XCTAssertFalse(cancelled.canAdvance)
    XCTAssertFalse(cancelled.beginAdvance())
  }

  func testBudgetIsClampedToProtocolMaximum() {
    var gate = RivunePlaybackFailoverGate(maximumSwitches: 99)
    XCTAssertEqual(gate.maximumSwitches, 3)
    XCTAssertTrue(gate.beginAdvance())
    XCTAssertTrue(gate.beginAdvance())
    XCTAssertTrue(gate.beginAdvance())
    XCTAssertFalse(gate.beginAdvance())
  }
}
