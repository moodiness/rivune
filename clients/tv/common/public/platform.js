(function () {
  "use strict";

  function browserPlayer(host) {
    var video = document.createElement("video");
    var events = null;
    var timeTimer = 0;
    video.className = "tv-native-video";
    video.preload = "auto";
    video.playsInline = true;
    video.setAttribute("playsinline", "");
    host.appendChild(video);

    function duration() {
      return Number.isFinite(video.duration) ? video.duration : 0;
    }
    function publishTracks() {
      if (!events) return;
      var tracks = [];
      var textTracks = video.textTracks || [];
      for (var index = 0; index < textTracks.length; index += 1) {
        var track = textTracks[index];
        tracks.push({
          id: track.id || String(index),
          index: index,
          kind: "subtitle",
          label: track.label || track.language || "Subtitles",
          language: track.language || undefined,
          selected: track.mode === "showing"
        });
      }
      events.onTracks(tracks);
    }
    function bind(nextEvents) {
      events = nextEvents;
      video.addEventListener("loadedmetadata", function () {
        events.onReady(duration());
        publishTracks();
      });
      video.addEventListener("waiting", function () { events.onState("buffering"); });
      video.addEventListener("playing", function () { events.onState("playing"); });
      video.addEventListener("pause", function () { if (!video.ended) events.onState("paused"); });
      video.addEventListener("ended", function () { events.onEnded(); });
      video.addEventListener("error", function () {
        var detail = video.error && video.error.message ? video.error.message : "Playback failed.";
        events.onError(detail);
      });
      timeTimer = window.setInterval(function () {
        if (events) events.onTime(video.currentTime || 0, duration());
      }, 1000);
    }

    return {
      load: async function (request, nextEvents) {
        bind(nextEvents);
        video.replaceChildren();
        request.subtitles.forEach(function (subtitle) {
          var track = document.createElement("track");
          track.kind = "subtitles";
          track.src = subtitle.url;
          track.label = subtitle.label;
          track.srclang = subtitle.language || "und";
          track.id = subtitle.id;
          track.default = subtitle.selected;
          video.appendChild(track);
        });
        video.src = request.url;
        video.currentTime = Math.max(0, request.startSeconds || 0);
        video.load();
      },
      play: async function () { await video.play(); },
      pause: async function () { video.pause(); },
      seek: async function (positionSeconds) { video.currentTime = Math.max(0, positionSeconds); },
      selectAudio: async function () {},
      selectSubtitle: async function (id) {
        var tracks = video.textTracks || [];
        for (var index = 0; index < tracks.length; index += 1) {
          tracks[index].mode = id !== null && (tracks[index].id === id || String(index) === id) ? "showing" : "disabled";
        }
        publishTracks();
      },
      stop: async function () {
        video.pause();
        video.removeAttribute("src");
        video.load();
      },
      destroy: function () {
        window.clearInterval(timeTimer);
        video.pause();
        video.removeAttribute("src");
        video.load();
        video.remove();
        events = null;
      }
    };
  }

  window.RivunePlatformAdapter = {
    platform: "browser",
    deviceName: async function () { return "Rivune TV Browser"; },
    capabilities: function () {
      return {
        streamingProtocols: ["http", "https", "hls", "dash"],
        containers: ["mp4", "m4v", "webm", "m3u8", "mpd"],
        videoCodecs: ["h264", "vp9", "av1"],
        audioCodecs: ["aac", "mp3", "opus", "vorbis"],
        hdrFormats: [],
        processingModes: ["direct", "remux", "transcode_audio", "transcode"],
        maximumHeight: 2160,
        maximumAudioChannels: 8,
        subtitleModes: ["external", "burn"]
      };
    },
    createPlayer: browserPlayer,
    exitApp: function () {
      if (window.history.length > 1) window.history.back();
      else window.close();
    }
  };
})();
