import { translate as t, type TranslationKey } from "./i18n";
import type { PlaybackDecision, PlaybackDecisionReason } from "./types";

const outcomeKeys: Record<PlaybackDecision["reason"], TranslationKey> = {
  direct_supported: "playbackDecision.outcomes.directSupported",
  remux_required: "playbackDecision.outcomes.remuxRequired",
  audio_transcode_required: "playbackDecision.outcomes.audioTranscodeRequired",
  video_transcode_required: "playbackDecision.outcomes.videoTranscodeRequired",
  subtitle_burn_required: "playbackDecision.outcomes.subtitleBurnRequired",
};

const reasonKeys: Record<PlaybackDecisionReason, TranslationKey> = {
  container_not_supported: "playbackDecision.reasons.containerNotSupported",
  video_codec_not_supported: "playbackDecision.reasons.videoCodecNotSupported",
  audio_codec_not_supported: "playbackDecision.reasons.audioCodecNotSupported",
  resolution_limit: "playbackDecision.reasons.resolutionLimit",
  bitrate_limit: "playbackDecision.reasons.bitrateLimit",
  hdr_not_supported: "playbackDecision.reasons.hdrNotSupported",
  subtitle_burn_required: "playbackDecision.reasons.subtitleBurnRequired",
};

export function playbackDecisionOutcome(decision: PlaybackDecision): string {
  return t(outcomeKeys[decision.reason]);
}

export function playbackDecisionReasons(decision: PlaybackDecision): string[] {
  return decision.reasons.map((reason) => t(reasonKeys[reason]));
}
