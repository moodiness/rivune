(function installRivuneWebOSAdapter(window, document) {
  "use strict";

  var activePlayers = [];
  var palmSystem = window.PalmSystem;

  function deviceInfo() {
    if (!palmSystem || !palmSystem.deviceInfo) return {};
    if (typeof palmSystem.deviceInfo === "object") return palmSystem.deviceInfo;
    try {
      return JSON.parse(palmSystem.deviceInfo);
    } catch (_) {
      return {};
    }
  }

  function mediaErrorMessage(video) {
    var error = video.error;
    if (!error) return "The TV could not play this media.";
    if (error.code === 1) return "Playback was aborted.";
    if (error.code === 2) return "A network error interrupted playback.";
    if (error.code === 3) return "The TV could not decode this media.";
    if (error.code === 4) return "This media format is not supported by the TV.";
    return error.message || "The TV could not play this media.";
  }

  function finiteTime(value) {
    return Number.isFinite(value) && value >= 0 ? value : 0;
  }

  function removeAllChildren(element) {
    while (element.firstChild) element.removeChild(element.firstChild);
  }

  function trackLabel(track, fallback) {
    return track.label || track.language || fallback;
  }

  function WebOSPlayer(host) {
    if (!host || typeof host.appendChild !== "function") {
      throw new TypeError("createPlayer requires an HTML element host.");
    }

    this._host = host;
    this._video = document.createElement("video");
    this._video.className = "rivune-native-player";
    this._video.preload = "metadata";
    this._video.autoplay = false;
    this._video.controls = false;
    this._video.playsInline = true;
    this._video.setAttribute("playsinline", "");
    this._video.setAttribute("aria-label", "Video playback");
    this._video.style.width = "100%";
    this._video.style.height = "100%";
    this._video.style.display = "block";
    this._video.style.backgroundColor = "#000";

    this._events = null;
    this._request = null;
    this._destroyed = false;
    this._readyEmitted = false;
    this._subtitleTracks = [];
    this._listeners = [];
    this._trackListListeners = [];

    removeAllChildren(host);
    host.appendChild(this._video);
    this._listen("loadedmetadata", this._onLoadedMetadata.bind(this));
    this._listen("durationchange", this._onTime.bind(this));
    this._listen("timeupdate", this._onTime.bind(this));
    this._listen("waiting", this._emitState.bind(this, "buffering"));
    this._listen("stalled", this._emitState.bind(this, "buffering"));
    this._listen("seeking", this._emitState.bind(this, "buffering"));
    this._listen("playing", this._emitState.bind(this, "playing"));
    this._listen("pause", this._onPause.bind(this));
    this._listen("ended", this._emit.bind(this, "onEnded"));
    this._listen("error", this._onError.bind(this));
    activePlayers.push(this);
  }

  WebOSPlayer.prototype._assertUsable = function () {
    if (this._destroyed) throw new Error("This player has been destroyed.");
  };

  WebOSPlayer.prototype._listen = function (name, listener) {
    this._video.addEventListener(name, listener);
    this._listeners.push([name, listener]);
  };

  WebOSPlayer.prototype._emit = function (name) {
    if (!this._events || typeof this._events[name] !== "function") return;
    var args = Array.prototype.slice.call(arguments, 1);
    this._events[name].apply(this._events, args);
  };

  WebOSPlayer.prototype._emitState = function (state) {
    this._emit("onState", state);
  };

  WebOSPlayer.prototype._onPause = function () {
    if (!this._video.ended && this._video.currentSrc) this._emitState("paused");
  };

  WebOSPlayer.prototype._onTime = function () {
    this._emit("onTime", finiteTime(this._video.currentTime), finiteTime(this._video.duration));
  };

  WebOSPlayer.prototype._onError = function () {
    if (this._video.currentSrc || this._video.getAttribute("src")) {
      this._emit("onError", mediaErrorMessage(this._video));
    }
  };

  WebOSPlayer.prototype._onLoadedMetadata = function () {
    if (!this._request) return;
    if (!this._readyEmitted) {
      this._readyEmitted = true;
      this._emit("onReady", finiteTime(this._video.duration));
    }
    var startSeconds = finiteTime(this._request.startSeconds);
    if (startSeconds > 0) {
      try {
        this._video.currentTime = startSeconds;
      } catch (_) {
        this._video.addEventListener("canplay", function seekWhenReady() {
          this.removeEventListener("canplay", seekWhenReady);
          this.currentTime = startSeconds;
        }, { once: true });
      }
    }
    this._bindTrackLists();
    this._reportTracks();
    this._onTime();
  };

  WebOSPlayer.prototype._clearTrackListListeners = function () {
    for (var i = 0; i < this._trackListListeners.length; i += 1) {
      var registration = this._trackListListeners[i];
      registration[0].removeEventListener(registration[1], registration[2]);
    }
    this._trackListListeners = [];
  };

  WebOSPlayer.prototype._bindTrackLists = function () {
    this._clearTrackListListeners();
    var self = this;
    function bind(list, eventName) {
      if (!list || typeof list.addEventListener !== "function") return;
      var listener = self._reportTracks.bind(self);
      list.addEventListener(eventName, listener);
      self._trackListListeners.push([list, eventName, listener]);
    }
    bind(this._video.audioTracks, "addtrack");
    bind(this._video.audioTracks, "removetrack");
    bind(this._video.audioTracks, "change");
    bind(this._video.textTracks, "addtrack");
    bind(this._video.textTracks, "removetrack");
    bind(this._video.textTracks, "change");
  };

  WebOSPlayer.prototype._subtitleId = function (textTrack, index) {
    for (var i = 0; i < this._subtitleTracks.length; i += 1) {
      if (this._subtitleTracks[i].track === textTrack) return this._subtitleTracks[i].id;
    }
    return textTrack.id || "subtitle-" + index;
  };

  WebOSPlayer.prototype._reportTracks = function () {
    if (!this._events) return;
    var tracks = [];
    var audioTracks = this._video.audioTracks;
    if (audioTracks) {
      for (var audioIndex = 0; audioIndex < audioTracks.length; audioIndex += 1) {
        var audio = audioTracks[audioIndex];
        tracks.push({
          id: audio.id || "audio-" + audioIndex,
          index: audioIndex,
          kind: "audio",
          label: trackLabel(audio, "Audio " + (audioIndex + 1)),
          language: audio.language || undefined,
          selected: Boolean(audio.enabled)
        });
      }
    }

    var textTracks = this._video.textTracks;
    if (textTracks) {
      for (var textIndex = 0; textIndex < textTracks.length; textIndex += 1) {
        var text = textTracks[textIndex];
        if (text.kind !== "subtitles" && text.kind !== "captions") continue;
        tracks.push({
          id: this._subtitleId(text, textIndex),
          index: textIndex,
          kind: "subtitle",
          label: trackLabel(text, "Subtitles " + (textIndex + 1)),
          language: text.language || undefined,
          selected: text.mode === "showing"
        });
      }
    }
    this._emit("onTracks", tracks);
  };

  WebOSPlayer.prototype._resetMedia = function () {
    this._clearTrackListListeners();
    this._video.pause();
    this._video.removeAttribute("src");
    removeAllChildren(this._video);
    this._video.load();
    this._request = null;
    this._readyEmitted = false;
    this._subtitleTracks = [];
  };

  WebOSPlayer.prototype.load = function (request, events) {
    this._assertUsable();
    if (!request || typeof request.url !== "string" || !request.url) {
      return Promise.reject(new TypeError("Playback requires a media URL."));
    }
    if (!events) return Promise.reject(new TypeError("Playback event callbacks are required."));

    this._events = null;
    this._resetMedia();
    this._events = events;
    this._request = request;
    this._video.setAttribute("data-rivune-protocol", request.protocol || "direct");
    this._video.setAttribute("aria-label", request.title || "Video playback");

    var subtitles = Array.isArray(request.subtitles) ? request.subtitles : [];
    for (var i = 0; i < subtitles.length; i += 1) {
      var subtitle = subtitles[i];
      if (!subtitle || !subtitle.url) continue;
      var element = document.createElement("track");
      element.kind = "subtitles";
      element.src = subtitle.url;
      element.label = subtitle.label || subtitle.language || "Subtitles " + (i + 1);
      element.srclang = subtitle.language || "und";
      element.default = Boolean(subtitle.selected);
      this._video.appendChild(element);
      this._subtitleTracks.push({ id: subtitle.id, track: element.track });
    }

    var self = this;
    return new Promise(function (resolve, reject) {
      function cleanup() {
        self._video.removeEventListener("loadedmetadata", loaded);
        self._video.removeEventListener("error", failed);
      }
      function loaded() {
        cleanup();
        resolve();
      }
      function failed() {
        cleanup();
        reject(new Error(mediaErrorMessage(self._video)));
      }
      self._video.addEventListener("loadedmetadata", loaded);
      self._video.addEventListener("error", failed);
      self._video.src = request.url;
      self._video.load();
    });
  };

  WebOSPlayer.prototype.play = function () {
    this._assertUsable();
    var result = this._video.play();
    return result && typeof result.then === "function" ? result : Promise.resolve();
  };

  WebOSPlayer.prototype.pause = function () {
    this._assertUsable();
    this._video.pause();
    return Promise.resolve();
  };

  WebOSPlayer.prototype.seek = function (positionSeconds) {
    this._assertUsable();
    if (!Number.isFinite(positionSeconds) || positionSeconds < 0) {
      return Promise.reject(new RangeError("Seek position must be a non-negative number."));
    }
    try {
      this._video.currentTime = positionSeconds;
      return Promise.resolve();
    } catch (error) {
      return Promise.reject(error);
    }
  };

  WebOSPlayer.prototype.selectAudio = function (index) {
    this._assertUsable();
    var tracks = this._video.audioTracks;
    if (!tracks || !Number.isInteger(index) || index < 0 || index >= tracks.length) {
      return Promise.reject(new RangeError("The selected audio track is unavailable."));
    }
    for (var i = 0; i < tracks.length; i += 1) tracks[i].enabled = i === index;
    this._reportTracks();
    return Promise.resolve();
  };

  WebOSPlayer.prototype.selectSubtitle = function (id) {
    this._assertUsable();
    var tracks = this._video.textTracks;
    if (!tracks) return Promise.reject(new Error("Subtitle selection is unavailable."));
    var found = id === null;
    for (var i = 0; i < tracks.length; i += 1) {
      var selected = id !== null && this._subtitleId(tracks[i], i) === id;
      tracks[i].mode = selected ? "showing" : "disabled";
      if (selected) found = true;
    }
    if (!found) return Promise.reject(new RangeError("The selected subtitle track is unavailable."));
    this._reportTracks();
    return Promise.resolve();
  };

  WebOSPlayer.prototype.stop = function () {
    this._assertUsable();
    this._events = null;
    this._resetMedia();
    return Promise.resolve();
  };

  WebOSPlayer.prototype.destroy = function () {
    if (this._destroyed) return;
    this._events = null;
    this._resetMedia();
    for (var i = 0; i < this._listeners.length; i += 1) {
      this._video.removeEventListener(this._listeners[i][0], this._listeners[i][1]);
    }
    this._listeners = [];
    if (this._video.parentNode) this._video.parentNode.removeChild(this._video);
    var playerIndex = activePlayers.indexOf(this);
    if (playerIndex >= 0) activePlayers.splice(playerIndex, 1);
    this._destroyed = true;
  };

  WebOSPlayer.prototype._suspend = function () {
    if (!this._destroyed && !this._video.paused) this._video.pause();
  };

  WebOSPlayer.prototype._release = function () {
    if (this._destroyed) return;
    this._events = null;
    this._resetMedia();
  };

  function canPlay(probe, mimeType) {
    return probe.canPlayType(mimeType) !== "";
  }

  function frozenCapabilities() {
    var probe = document.createElement("video");
    var videoCodecs = ["h264"];
    var audioCodecs = ["aac", "mp3", "ac3"];
    var containers = ["mp4", "mpegts"];

    if (canPlay(probe, 'video/mp4; codecs="hvc1.1.6.L120.90"') || canPlay(probe, 'video/mp4; codecs="hev1.1.6.L120.90"')) videoCodecs.push("hevc");
    if (canPlay(probe, 'video/webm; codecs="vp9"')) videoCodecs.push("vp9");
    if (canPlay(probe, 'video/mp4; codecs="av01.0.08M.08"')) videoCodecs.push("av1");
    if (canPlay(probe, "video/x-matroska")) containers.push("mkv", "matroska");
    if (canPlay(probe, "video/webm")) containers.push("webm");
    if (canPlay(probe, 'audio/mp4; codecs="ec-3"')) audioCodecs.push("eac3");
    if (canPlay(probe, 'audio/mp4; codecs="flac"') || canPlay(probe, "audio/flac")) audioCodecs.push("flac");
    if (canPlay(probe, 'audio/webm; codecs="opus"')) audioCodecs.push("opus");

    var hdrFormats = ["sdr"];
    var highDynamicRange = window.matchMedia && window.matchMedia("(dynamic-range: high)").matches;
    if (highDynamicRange) hdrFormats.push("hdr10", "hlg");
    if (highDynamicRange && (canPlay(probe, 'video/mp4; codecs="dvh1.05.06"') || canPlay(probe, 'video/mp4; codecs="dvhe.05.06"'))) hdrFormats.push("dolby_vision");
    var maximumAudioChannels = 6;
    var info = deviceInfo();
    var reportedHeight = Number(info.screenHeight || info.displayHeight || window.screen.height || 1080);
    var maximumHeight = Math.max(144, Math.min(4320, Math.round(reportedHeight)));

    function freezeList(list) { return Object.freeze(list); }
    return Object.freeze({
      streamingProtocols: freezeList(["http", "hls", "dash"]),
      containers: freezeList(containers),
      videoCodecs: freezeList(videoCodecs),
      audioCodecs: freezeList(audioCodecs),
      hdrFormats: freezeList(hdrFormats),
      processingModes: freezeList(["direct", "remux", "transcode_audio", "transcode"]),
      maximumHeight: maximumHeight,
      maximumVideoBitrateKbps: maximumHeight >= 2160 ? 40000 : 20000,
      maximumAudioChannels: maximumAudioChannels,
      subtitleModes: freezeList(["embedded", "external", "burn"])
    });
  }

  var capabilities = frozenCapabilities();

  function activePlayer() {
    return activePlayers.length ? activePlayers[activePlayers.length - 1] : null;
  }

  function runRemoteAction(action) {
    var player = activePlayer();
    if (!player || player._destroyed) return;
    var operation;
    if (action === "play") operation = player.play();
    else if (action === "pause") operation = player.pause();
    else if (action === "toggle") operation = player._video.paused ? player.play() : player.pause();
    else if (action === "stop") operation = player.stop();
    else if (action === "rewind") operation = player.seek(Math.max(0, finiteTime(player._video.currentTime) - 30));
    else if (action === "fastForward") {
      var duration = finiteTime(player._video.duration);
      operation = player.seek(duration ? Math.min(duration, finiteTime(player._video.currentTime) + 30) : finiteTime(player._video.currentTime) + 30);
    }
    if (operation && typeof operation.catch === "function") {
      operation.catch(function (error) { player._emit("onError", error.message || String(error)); });
    }
  }

  var remoteKeys = {
    19: "toggle",
    412: "rewind",
    413: "stop",
    415: "play",
    417: "fastForward"
  };

  window.addEventListener("keydown", function handleWebOSMediaRemote(event) {
    var action = remoteKeys[event.keyCode];
    if (!action) return;
    event.preventDefault();
    runRemoteAction(action);
  }, true);

  window.addEventListener("keydown", function exitOnUnhandledBack(event) {
    if (event.keyCode !== 461) return;
    window.setTimeout(function () {
      if (!event.defaultPrevented) window.close();
    }, 0);
  }, true);

  document.addEventListener("visibilitychange", function suspendHiddenPlayback() {
    if (!document.hidden) return;
    activePlayers.slice().forEach(function (player) { player._suspend(); });
  });

  window.addEventListener("pagehide", function releasePlaybackResources() {
    activePlayers.slice().forEach(function (player) { player._release(); });
  });

  window.webOSRelaunch = function webOSRelaunch() {
    if (palmSystem && typeof palmSystem.activate === "function") palmSystem.activate();
    window.dispatchEvent(new Event("webosrelaunch"));
    return true;
  };

  function signalReady() {
    if (palmSystem && typeof palmSystem.stageReady === "function") palmSystem.stageReady();
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", signalReady, { once: true });
  else signalReady();

  window.RivunePlatformAdapter = Object.freeze({
    platform: "webos",
    deviceName: function () {
      var info = deviceInfo();
      var name = info.modelName || info.modelNumber || info.productName || "LG webOS TV";
      return Promise.resolve(String(name));
    },
    capabilities: function () { return capabilities; },
    createPlayer: function (host) { return new WebOSPlayer(host); },
    exitApp: function () { window.close(); }
  });
})(window, document);
