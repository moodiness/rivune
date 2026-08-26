import SwiftUI

private struct RivuneAnimationPreferenceKey: EnvironmentKey {
  static let defaultValue = RivuneAnimationPreference.system
}

extension EnvironmentValues {
  var rivuneAnimationPreference: RivuneAnimationPreference {
    get { self[RivuneAnimationPreferenceKey.self] }
    set { self[RivuneAnimationPreferenceKey.self] = newValue }
  }
}

private struct RivuneAnimationModifier<Value: Equatable>: ViewModifier {
  @Environment(\.rivuneAnimationPreference) private var preference
  @Environment(\.accessibilityReduceMotion) private var systemReduceMotion
  let animation: Animation
  let value: Value

  func body(content: Content) -> some View {
    content.animation(
      RivuneMotionPolicy.reducesMotion(
        preference: preference,
        systemReduceMotion: systemReduceMotion
      ) ? nil : animation,
      value: value
    )
  }
}

private struct RivuneTransitionModifier: ViewModifier {
  @Environment(\.rivuneAnimationPreference) private var preference
  @Environment(\.accessibilityReduceMotion) private var systemReduceMotion
  let transition: AnyTransition

  func body(content: Content) -> some View {
    content.transition(
      RivuneMotionPolicy.reducesMotion(
        preference: preference,
        systemReduceMotion: systemReduceMotion
      ) ? .identity : transition
    )
  }
}

extension View {
  func rivuneAnimation<Value: Equatable>(_ animation: Animation, value: Value) -> some View {
    modifier(RivuneAnimationModifier(animation: animation, value: value))
  }

  func rivuneTransition(_ transition: AnyTransition) -> some View {
    modifier(RivuneTransitionModifier(transition: transition))
  }
}
