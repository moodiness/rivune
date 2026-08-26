import RivuneAPI

enum RivuneMotionPolicy {
  static func reducesMotion(
    preference: RivuneAnimationPreference,
    systemReduceMotion: Bool
  ) -> Bool {
    systemReduceMotion || preference == .reduced
  }
}

enum RivuneDynamicTitlePolicy {
  static func lineLimit(isAccessibilitySize: Bool, standardLimit: Int = 2) -> Int? {
    isAccessibilitySize ? nil : standardLimit
  }
}

struct RivuneEffectiveAccessibility: Equatable, Sendable {
  let reduceMotion: Bool
  let highContrast: Bool
  let textScale: Int
  let captionsEnabled: Bool
  let audioDescription: Bool
  let enhancedFocusIndicators: Bool
}

enum RivuneAccessibilityPolicy {
  static func resolve(
    _ preferences: AccessibilityPreferencesDocument,
    systemReduceMotion: Bool,
    systemHighContrast: Bool,
    systemCaptionsEnabled: Bool
  ) -> RivuneEffectiveAccessibility {
    let reduceMotion: Bool
    switch preferences.reducedMotion {
    case .system: reduceMotion = systemReduceMotion
    case .reduce: reduceMotion = true
    case .noPreference: reduceMotion = false
    }
    let highContrast: Bool
    switch preferences.highContrast {
    case .system: highContrast = systemHighContrast
    case .more: highContrast = true
    case .standard: highContrast = false
    }
    let captionsEnabled: Bool
    switch preferences.captions {
    case .system: captionsEnabled = systemCaptionsEnabled
    case .on: captionsEnabled = true
    case .off: captionsEnabled = false
    }
    return RivuneEffectiveAccessibility(
      reduceMotion: reduceMotion,
      highContrast: highContrast,
      textScale: [100, 115, 130].contains(preferences.textScale) ? preferences.textScale : 100,
      captionsEnabled: captionsEnabled,
      audioDescription: preferences.audioDescription,
      enhancedFocusIndicators: preferences.focusIndicators == .enhanced)
  }
}
