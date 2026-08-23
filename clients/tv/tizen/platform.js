(function () {
  "use strict";

  var MEDIA_KEYS = ["MediaPlay", "MediaPause", "MediaPlayPause", "MediaStop", "MediaRewind", "MediaFastForward", "MediaTrackPrevious", "MediaTrackNext"];
  var activePlayers = [];

  function errorMessage(error) {
    if (error && typeof error.message === "string" && error.message) return error.message;
    if (typeof error === "string" && error) return error;
    return "Samsung TV playback failed.";
  }

  function emit(events, name) {
    if (!events || typeof events[name] !== "function") return;
    var args = Array.prototype.slice.call(arguments, 2);
    try { events[name].apply(events, args); }
    catch (error) { window.setTimeout(function () { throw error; }, 0); }
  }

  function registerRemoteKeys() {
    try {
      if (!window.tizen || !window.tizen.tvinputdevice) return;
      var manager = window.tizen.tvinputdevice;
      var supported = typeof manager.getSupportedKeys === "function" ? manager.getSupportedKeys() : [];
      var supportedNames = supported.map(function (key) { return key.name; });
      var keys = supportedNames.length > 0 ? MEDIA_KEYS.filter(function (key) { return supportedNames.indexOf(key) !== -1; }) : MEDIA_KEYS;
      function registerIndividually() {
        keys.forEach(function (key) {
          try { manager.registerKey(key); } catch (error) {}
        });
      }
      if (typeof manager.registerKeyBatch === "function") manager.registerKeyBatch(keys, function () {}, registerIndividually);
      else registerIndividually();
    } catch (error) {
      console.warn("Rivune could not register Samsung media keys:", errorMessage(error));
    }
  }

  function exitApp() {
    try {
      if (window.tizen && window.tizen.application) {
        window.tizen.application.getCurrentApplication().exit();
        return;
      }
    } catch (error) {
      console.error("Rivune could not exit the Samsung application:", errorMessage(error));
    }
    window.close();
  }

  function installLifecycleHandlers() {
    window.addEventListener("keydown", function (event) {
      if (event.keyCode !== 10009 && event.key !== "BrowserBack" && event.key !== "GoBack") return;
      window.setTimeout(function () {
        if (event.defaultPrevented) return;
        if (window.history.length > 1) window.history.back();
        else exitApp();
      }, 0);
    });
    document.addEventListener("visibilitychange", function () {
      if (!document.hidden) return;
      activePlayers.slice().forEach(function (player) { player.pause().catch(function () {}); });
    });
  }

  function addPlayer(player) { activePlayers.push(player); }
  function removePlayer(player) {
    var index = activePlayers.indexOf(player);
    if (index !== -1) activePlayers.splice(index, 1);
  }

  function parseTrack(track) {
    var details = {};
    if (track && typeof track.extra_info === "string" && track.extra_info) {
      try { details = JSON.parse(track.extra_info); } catch (error) {}
    }
    return {
      language: details.track_lang || details.language || details.lang || undefined,
      label: details.track_name || details.track_lang || details.language || "Unknown"
    };
  }

  function subtitleTimestamp(value) {
    var match = String(value).trim().match(/^(?:(\d+):)?(\d{1,2}):(\d{2}(?:[.,]\d+)?)/);
    if (!match) return null;
    return (Number(match[1]) || 0) * 3600 + Number(match[2]) * 60 + Number(match[3].replace(",", "."));
  }

  function subtitleCueText(lines) {
    return lines.join("\n")
      .replace(/<br\s*\/?\s*>/gi, "\n")
      .replace(/<[^>]+>/g, "")
      .replace(/&#(\d+);/g, function (match, value) { return String.fromCharCode(Number(value)); })
      .replace(/&#x([0-9a-f]+);/gi, function (match, value) { return String.fromCharCode(parseInt(value, 16)); })
      .replace(/&lt;/gi, "<")
      .replace(/&gt;/gi, ">")
      .replace(/&quot;/gi, "\"")
      .replace(/&#39;|&apos;/gi, "'")
      .replace(/&amp;/gi, "&")
      .trim();
  }

  function parseExternalSubtitles(source) {
    var blocks = String(source).replace(/^\uFEFF/, "").replace(/\r\n?/g, "\n").split(/\n{2,}/);
    var cues = [];
    blocks.forEach(function (block) {
      var lines = block.split("\n").filter(function (line) { return line.trim() !== ""; });
      var timingIndex = lines.findIndex(function (line) { return line.indexOf("-->") !== -1; });
      if (timingIndex < 0) return;
      var timing = lines[timingIndex].split("-->");
      var start = subtitleTimestamp(timing[0]);
      var end = subtitleTimestamp(timing[1]);
      var text = subtitleCueText(lines.slice(timingIndex + 1));
      if (start !== null && end !== null && end > start && text) cues.push({ start: start, end: end, text: text });
    });
    cues.sort(function (left, right) { return left.start - right.start; });
    return cues;
  }


  function AvPlayPlayer(host, avplay) {
    this.host = host;
    this.avplay = avplay;
    this.events = null;
    this.request = null;
    this.destroyed = false;
    this.generation = 0;
    this.durationSeconds = 0;
    this.desiredState = "paused";
    this.externalSubtitleId = null;
    this.pendingAudioIndex = null;
    this.pendingNativeSubtitleIndex = null;
    this.subtitleCues = [];
    this.subtitleRequestToken = 0;
    this.currentPositionSeconds = 0;
    this.resizeObserver = null;
    this.object = document.createElement("object");
    this.object.type = "application/avplayer";
    this.object.setAttribute("aria-label", "Rivune video player");
    this.object.style.position = "absolute";
    this.object.style.top = "0";
    this.object.style.left = "0";
    this.object.style.width = "100%";
    this.object.style.height = "100%";
    this.object.style.pointerEvents = "none";
    this.host.appendChild(this.object);
    this.subtitleOverlay = document.createElement("div");
    this.subtitleOverlay.setAttribute("aria-live", "off");
    this.subtitleOverlay.style.position = "absolute";
    this.subtitleOverlay.style.left = "5%";
    this.subtitleOverlay.style.right = "5%";
    this.subtitleOverlay.style.bottom = "7%";
    this.subtitleOverlay.style.zIndex = "2";
    this.subtitleOverlay.style.color = "white";
    this.subtitleOverlay.style.fontSize = "3.1vw";
    this.subtitleOverlay.style.fontWeight = "600";
    this.subtitleOverlay.style.lineHeight = "1.25";
    this.subtitleOverlay.style.textAlign = "center";
    this.subtitleOverlay.style.whiteSpace = "pre-line";
    this.subtitleOverlay.style.textShadow = "0 2px 4px black, 0 0 8px black";
    this.subtitleOverlay.style.pointerEvents = "none";
    this.host.appendChild(this.subtitleOverlay);
    this.boundLayout = this.updateDisplayRect.bind(this);
    window.addEventListener("resize", this.boundLayout);
    if (typeof window.ResizeObserver === "function") {
      this.resizeObserver = new window.ResizeObserver(this.boundLayout);
      this.resizeObserver.observe(this.host);
    }
    addPlayer(this);
  }

  AvPlayPlayer.prototype.assertAvailable = function () {
    if (this.destroyed) throw new Error("The Samsung player has been destroyed.");
  };

  AvPlayPlayer.prototype.updateDisplayRect = function () {
    if (this.destroyed) return;
    var rect = this.host.getBoundingClientRect();
    var scaleX = window.innerWidth > 0 ? window.screen.width / window.innerWidth : 1;
    var scaleY = window.innerHeight > 0 ? window.screen.height / window.innerHeight : 1;
    try {
      this.avplay.setDisplayRect(
        Math.max(0, Math.round(rect.left * scaleX)),
        Math.max(0, Math.round(rect.top * scaleY)),
        Math.max(1, Math.round(rect.width * scaleX)),
        Math.max(1, Math.round(rect.height * scaleY))
      );
      this.avplay.setDisplayMethod("PLAYER_DISPLAY_MODE_LETTER_BOX");
    } catch (error) {
      if (this.events) emit(this.events, "onError", errorMessage(error));
    }
  };

  AvPlayPlayer.prototype.closeCurrent = function () {
    try {
      var state = this.avplay.getState();
      if (state === "PLAYING" || state === "PAUSED") this.avplay.stop();
      if (this.avplay.getState() !== "NONE") this.avplay.close();
    } catch (error) {
      try { this.avplay.close(); } catch (closeError) {}
    }
  };

  AvPlayPlayer.prototype.updateSubtitle = function (positionSeconds) {
    this.currentPositionSeconds = positionSeconds;
    var text = "";
    for (var index = 0; index < this.subtitleCues.length; index += 1) {
      var cue = this.subtitleCues[index];
      if (cue.start > positionSeconds) break;
      if (cue.start <= positionSeconds && cue.end > positionSeconds) text = text ? text + "\n" + cue.text : cue.text;
    }
    if (this.subtitleOverlay.textContent !== text) this.subtitleOverlay.textContent = text;
  };

  AvPlayPlayer.prototype.activateExternalSubtitle = function (id) {
    this.subtitleRequestToken += 1;
    var token = this.subtitleRequestToken;
    this.subtitleCues = [];
    this.externalSubtitleId = null;
    this.updateSubtitle(this.currentPositionSeconds);
    if (id === null) {
      this.emitTracks();
      return Promise.resolve();
    }
    var subtitle = this.request && this.request.subtitles.find(function (item) { return item.id === id; });
    if (!subtitle) return Promise.reject(new Error("The selected subtitle is unavailable."));
    var player = this;
    return window.fetch(subtitle.url, { credentials: "omit", redirect: "error", cache: "no-store" }).then(function (response) {
      if (!response.ok) throw new Error("The selected subtitle could not be downloaded.");
      return response.text();
    }).then(function (source) {
      if (token !== player.subtitleRequestToken || player.destroyed) return;
      var cues = parseExternalSubtitles(source);
      if (cues.length === 0) throw new Error("The selected subtitle format is unsupported or empty.");
      player.subtitleCues = cues;
      player.externalSubtitleId = subtitle.id;
      player.updateSubtitle(player.currentPositionSeconds);
      player.emitTracks();
    });
  };

  AvPlayPlayer.prototype.emitTracks = function () {
    var tracks = [];
    var nativeTracks = [];
    var currentTracks = [];
    try { nativeTracks = this.avplay.getTotalTrackInfo() || []; } catch (error) {}
    try { currentTracks = this.avplay.getCurrentStreamInfo() || []; } catch (error) {}
    var pendingAudioIndex = this.pendingAudioIndex;
    var pendingNativeSubtitleIndex = this.pendingNativeSubtitleIndex;
    function selected(track) {
      return currentTracks.some(function (current) { return current && current.type === track.type && Number(current.index) === Number(track.index); });
    }
    nativeTracks.forEach(function (track) {
      if (!track || (track.type !== "AUDIO" && track.type !== "TEXT")) return;
      var details = parseTrack(track);
      var audio = track.type === "AUDIO";
      tracks.push({
        id: (audio ? "audio:" : "subtitle:native:") + String(track.index),
        index: Number(track.index),
        kind: audio ? "audio" : "subtitle",
        label: details.label,
        language: details.language,
        selected: selected(track)
          || (audio && pendingAudioIndex === Number(track.index))
          || (!audio && pendingNativeSubtitleIndex === Number(track.index))
          || (audio && pendingAudioIndex === null && currentTracks.length === 0 && tracks.every(function (item) { return item.kind !== "audio"; }))
      });
    });
    if (this.request) {
      var player = this;
      this.request.subtitles.forEach(function (subtitle, index) {
        tracks.push({ id: subtitle.id, index: index, kind: "subtitle", label: subtitle.label, language: subtitle.language, selected: subtitle.id === player.externalSubtitleId });
      });
    }
    emit(this.events, "onTracks", tracks);
  };

  AvPlayPlayer.prototype.load = function (request, events) {
    this.assertAvailable();
    if (!request || typeof request.url !== "string" || !request.url) return Promise.reject(new Error("A playback URL is required."));
    this.generation += 1;
    var generation = this.generation;
    this.closeCurrent();
    this.events = events;
    this.request = request;
    this.durationSeconds = 0;
    this.desiredState = "paused";
    this.pendingAudioIndex = null;
    this.pendingNativeSubtitleIndex = null;
    this.externalSubtitleId = null;
    var player = this;

    return new Promise(function (resolve, reject) {
      var settled = false;
      function fail(error) {
        if (settled || generation !== player.generation) return;
        settled = true;
        var message = errorMessage(error);
        emit(player.events, "onError", message);
        reject(new Error(message));
      }
      try {
        player.avplay.setListener({
          onbufferingstart: function () { if (generation === player.generation) emit(player.events, "onState", "buffering"); },
          onbufferingprogress: function () {},
          onbufferingcomplete: function () { if (generation === player.generation) emit(player.events, "onState", player.desiredState); },
          oncurrentplaytime: function (milliseconds) {
            if (generation !== player.generation) return;
            var positionSeconds = Number(milliseconds) / 1000;
            player.updateSubtitle(positionSeconds);
            emit(player.events, "onTime", positionSeconds, player.durationSeconds);
          },
          onstreamcompleted: function () { if (generation === player.generation) emit(player.events, "onEnded"); },
          onevent: function () {},
          onerror: function (eventType) { if (generation === player.generation) emit(player.events, "onError", String(eventType || "Samsung AVPlay error")); },
          onerrormsg: function (eventType, message) { if (generation === player.generation) emit(player.events, "onError", message || String(eventType)); },
          onsubtitlechange: function () {}
        });
        player.avplay.open(request.url);
        player.updateDisplayRect();
        player.avplay.prepareAsync(function () {
          if (generation !== player.generation || player.destroyed) return;
          try {
            player.durationSeconds = Math.max(0, Number(player.avplay.getDuration()) / 1000 || 0);
            var selectedSubtitle = request.subtitles.find(function (subtitle) { return subtitle.selected; });
            var subtitleReady = selectedSubtitle ? player.activateExternalSubtitle(selectedSubtitle.id) : player.activateExternalSubtitle(null);
            subtitleReady.then(function () {
              function finish() {
                if (settled || generation !== player.generation) return;
                settled = true;
                var startSeconds = Math.max(0, Number(request.startSeconds) || 0);
                player.updateSubtitle(startSeconds);
                emit(player.events, "onReady", player.durationSeconds);
                emit(player.events, "onTime", startSeconds, player.durationSeconds);
                emit(player.events, "onState", "paused");
                player.emitTracks();
                resolve();
              }
              if (Number(request.startSeconds) > 0) player.avplay.seekTo(Math.round(Number(request.startSeconds) * 1000), finish, fail);
              else finish();
            }).catch(fail);
          } catch (error) { fail(error); }
        }, fail);
      } catch (error) { fail(error); }
    });
  };

  AvPlayPlayer.prototype.play = function () {
    this.assertAvailable();
    try {
      this.avplay.play();
      if (this.pendingAudioIndex !== null) {
        this.avplay.setSelectTrack("AUDIO", this.pendingAudioIndex);
        this.pendingAudioIndex = null;
      }
      if (this.pendingNativeSubtitleIndex !== null) {
        this.avplay.setSelectTrack("TEXT", this.pendingNativeSubtitleIndex);
        this.pendingNativeSubtitleIndex = null;
      }
      this.desiredState = "playing";
      emit(this.events, "onState", "playing");
      this.emitTracks();
      return Promise.resolve();
    } catch (error) { return Promise.reject(error); }
  };

  AvPlayPlayer.prototype.pause = function () {
    this.assertAvailable();
    try {
      if (this.avplay.getState() === "PLAYING") this.avplay.pause();
      this.desiredState = "paused";
      emit(this.events, "onState", "paused");
      return Promise.resolve();
    } catch (error) { return Promise.reject(error); }
  };

  AvPlayPlayer.prototype.seek = function (positionSeconds) {
    this.assertAvailable();
    var player = this;
    var target = Math.max(0, Number(positionSeconds) || 0);
    if (this.durationSeconds > 0) target = Math.min(target, this.durationSeconds);
    return new Promise(function (resolve, reject) {
      try {
        player.avplay.seekTo(Math.round(target * 1000), function () { player.updateSubtitle(target); emit(player.events, "onTime", target, player.durationSeconds); resolve(); }, function (error) { reject(new Error(errorMessage(error))); });
      } catch (error) { reject(error); }
    });
  };

  AvPlayPlayer.prototype.selectAudio = function (index) {
    this.assertAvailable();
    try {
      var selected = Number(index);
      if (this.avplay.getState() === "PLAYING") this.avplay.setSelectTrack("AUDIO", selected);
      else this.pendingAudioIndex = selected;
      this.emitTracks();
      return Promise.resolve();
    } catch (error) { return Promise.reject(error); }
  };

  AvPlayPlayer.prototype.selectSubtitle = function (id) {
    this.assertAvailable();
    try {
      if (id !== null && String(id).indexOf("subtitle:native:") === 0) {
        this.subtitleRequestToken += 1;
        this.subtitleCues = [];
        this.externalSubtitleId = null;
        this.updateSubtitle(this.currentPositionSeconds);
        var selected = Number(String(id).slice("subtitle:native:".length));
        var state = this.avplay.getState();
        if (state === "PLAYING" || state === "PAUSED") this.avplay.setSelectTrack("TEXT", selected);
        else this.pendingNativeSubtitleIndex = selected;
        this.avplay.setSilentSubtitle(false);
        this.emitTracks();
        return Promise.resolve();
      }
      this.pendingNativeSubtitleIndex = null;
      this.avplay.setSilentSubtitle(true);
      return this.activateExternalSubtitle(id);
    } catch (error) { return Promise.reject(error); }
  };

  AvPlayPlayer.prototype.stop = function () {
    if (this.destroyed) return Promise.resolve();
    this.generation += 1;
    this.closeCurrent();
    this.events = null;
    this.request = null;
    this.durationSeconds = 0;
    this.externalSubtitleId = null;
    this.pendingAudioIndex = null;
    this.pendingNativeSubtitleIndex = null;
    this.subtitleRequestToken += 1;
    this.subtitleCues = [];
    this.currentPositionSeconds = 0;
    this.subtitleOverlay.textContent = "";
    this.desiredState = "paused";
    return Promise.resolve();
  };

  AvPlayPlayer.prototype.destroy = function () {
    if (this.destroyed) return;
    this.generation += 1;
    this.closeCurrent();
    this.destroyed = true;
    this.events = null;
    this.request = null;
    window.removeEventListener("resize", this.boundLayout);
    this.pendingAudioIndex = null;
    this.pendingNativeSubtitleIndex = null;
    if (this.resizeObserver) this.resizeObserver.disconnect();
    if (this.object.parentNode) this.object.parentNode.removeChild(this.object);
    if (this.subtitleOverlay.parentNode) this.subtitleOverlay.parentNode.removeChild(this.subtitleOverlay);
    removePlayer(this);
  };

  function Html5Player(host) {
    this.host = host;
    this.events = null;
    this.request = null;
    this.destroyed = false;
    this.generation = 0;
    this.listeners = [];
    this.video = document.createElement("video");
    this.video.preload = "auto";
    this.video.controls = false;
    this.video.autoplay = false;
    this.video.playsInline = true;
    this.video.setAttribute("webkit-playsinline", "");
    this.video.setAttribute("aria-label", "Rivune video player");
    this.video.style.width = "100%";
    this.video.style.height = "100%";
    this.video.style.objectFit = "contain";
    this.video.style.background = "black";
    this.host.appendChild(this.video);
    addPlayer(this);
  }

  Html5Player.prototype.assertAvailable = AvPlayPlayer.prototype.assertAvailable;
  Html5Player.prototype.listen = function (name, handler) { this.video.addEventListener(name, handler); this.listeners.push([name, handler]); };
  Html5Player.prototype.clearListeners = function () {
    var video = this.video;
    this.listeners.forEach(function (entry) { video.removeEventListener(entry[0], entry[1]); });
    this.listeners = [];
  };

  Html5Player.prototype.tracks = function () {
    var tracks = [];
    var audioTracks = this.video.audioTracks;
    if (audioTracks) {
      for (var audioIndex = 0; audioIndex < audioTracks.length; audioIndex += 1) {
        var audio = audioTracks[audioIndex];
        tracks.push({ id: "audio:" + String(audioIndex), index: audioIndex, kind: "audio", label: audio.label || audio.language || "Audio " + String(audioIndex + 1), language: audio.language || undefined, selected: Boolean(audio.enabled) });
      }
    }
    if (this.request) {
      var elements = this.video.querySelectorAll("track");
      this.request.subtitles.forEach(function (subtitle, index) {
        var textTrack = elements[index] && elements[index].track;
        tracks.push({ id: subtitle.id, index: index, kind: "subtitle", label: subtitle.label, language: subtitle.language, selected: Boolean(textTrack && textTrack.mode === "showing") });
      });
    }
    return tracks;
  };

  Html5Player.prototype.load = function (request, events) {
    this.assertAvailable();
    if (!request || typeof request.url !== "string" || !request.url) return Promise.reject(new Error("A playback URL is required."));
    this.generation += 1;
    var generation = this.generation;
    this.clearListeners();
    while (this.video.firstChild) this.video.removeChild(this.video.firstChild);
    this.video.removeAttribute("src");
    this.video.load();
    this.events = events;
    this.request = request;
    var player = this;
    request.subtitles.forEach(function (subtitle) {
      var track = document.createElement("track");
      track.kind = "subtitles";
      track.src = subtitle.url;
      track.label = subtitle.label;
      if (subtitle.language) track.srclang = subtitle.language;
      track.default = Boolean(subtitle.selected);
      track.dataset.rivuneSubtitleId = subtitle.id;
      player.video.appendChild(track);
    });

    return new Promise(function (resolve, reject) {
      var settled = false;
      function fail() {
        if (settled || generation !== player.generation) return;
        settled = true;
        var message = player.video.error && player.video.error.message ? player.video.error.message : "The television could not play this media.";
        emit(player.events, "onError", message);
        reject(new Error(message));
      }
      player.listen("error", fail);
      player.listen("loadedmetadata", function () {
        if (settled || generation !== player.generation) return;
        settled = true;
        if (Number(request.startSeconds) > 0) { try { player.video.currentTime = Number(request.startSeconds); } catch (error) {} }
        var duration = Number.isFinite(player.video.duration) ? player.video.duration : 0;
        emit(player.events, "onReady", duration);
        emit(player.events, "onTime", player.video.currentTime || 0, duration);
        emit(player.events, "onState", "paused");
        emit(player.events, "onTracks", player.tracks());
        resolve();
      });
      player.listen("waiting", function () { emit(player.events, "onState", "buffering"); });
      player.listen("playing", function () { emit(player.events, "onState", "playing"); });
      player.listen("pause", function () { if (!player.video.ended) emit(player.events, "onState", "paused"); });
      player.listen("timeupdate", function () { emit(player.events, "onTime", player.video.currentTime || 0, Number.isFinite(player.video.duration) ? player.video.duration : 0); });
      player.listen("ended", function () { emit(player.events, "onEnded"); });
      player.video.src = request.url;
      player.video.load();
    });
  };

  Html5Player.prototype.play = function () { this.assertAvailable(); return this.video.play(); };
  Html5Player.prototype.pause = function () { this.assertAvailable(); this.video.pause(); return Promise.resolve(); };
  Html5Player.prototype.seek = function (positionSeconds) {
    this.assertAvailable();
    var target = Math.max(0, Number(positionSeconds) || 0);
    if (Number.isFinite(this.video.duration)) target = Math.min(target, this.video.duration);
    try { if (typeof this.video.fastSeek === "function") this.video.fastSeek(target); else this.video.currentTime = target; return Promise.resolve(); }
    catch (error) { return Promise.reject(error); }
  };
  Html5Player.prototype.selectAudio = function (index) {
    this.assertAvailable();
    var tracks = this.video.audioTracks;
    var selected = Number(index);
    if (!tracks || selected < 0 || selected >= tracks.length) return Promise.reject(new Error("The selected audio track is unavailable."));
    for (var trackIndex = 0; trackIndex < tracks.length; trackIndex += 1) tracks[trackIndex].enabled = trackIndex === selected;
    emit(this.events, "onTracks", this.tracks());
    return Promise.resolve();
  };
  Html5Player.prototype.selectSubtitle = function (id) {
    this.assertAvailable();
    var found = id === null;
    var elements = this.video.querySelectorAll("track");
    for (var index = 0; index < elements.length; index += 1) {
      var selected = id !== null && elements[index].dataset.rivuneSubtitleId === id;
      elements[index].track.mode = selected ? "showing" : "disabled";
      if (selected) found = true;
    }
    if (!found) return Promise.reject(new Error("The selected subtitle is unavailable."));
    emit(this.events, "onTracks", this.tracks());
    return Promise.resolve();
  };
  Html5Player.prototype.stop = function () {
    if (this.destroyed) return Promise.resolve();
    this.generation += 1;
    this.clearListeners();
    this.video.pause();
    this.video.removeAttribute("src");
    while (this.video.firstChild) this.video.removeChild(this.video.firstChild);
    this.video.load();
    this.events = null;
    this.request = null;
    return Promise.resolve();
  };
  Html5Player.prototype.destroy = function () {
    if (this.destroyed) return;
    this.stop();
    this.destroyed = true;
    if (this.video.parentNode) this.video.parentNode.removeChild(this.video);
    removePlayer(this);
  };

  function avplayApi() {
    var webapis = window.webapis;
    if (!webapis || !webapis.avplay) return null;
    var avplay = webapis.avplay;
    var required = ["open", "close", "prepareAsync", "play", "pause", "seekTo", "stop"];
    return required.every(function (name) { return typeof avplay[name] === "function"; }) ? avplay : null;
  }

  function deviceName() {
    try {
      if (window.webapis && window.webapis.productinfo && typeof window.webapis.productinfo.getModelCode === "function") {
        var model = window.webapis.productinfo.getModelCode();
        if (model) return Promise.resolve("Samsung " + model);
      }
      if (window.tizen && window.tizen.systeminfo && typeof window.tizen.systeminfo.getCapability === "function") {
        var modelName = window.tizen.systeminfo.getCapability("http://tizen.org/system/model_name");
        if (modelName) return Promise.resolve("Samsung " + modelName);
      }
    } catch (error) {}
    return Promise.resolve("Samsung TV");
  }

  window.RivunePlatformAdapter = {
    platform: "tizen",
    deviceName: deviceName,
    capabilities: function () {
      var maximumHeight = 1080;
      var hdrFormats = [];
      try {
        if (window.webapis && window.webapis.productinfo && window.webapis.productinfo.isUdPanelSupported()) maximumHeight = 2160;
        if (window.webapis && window.webapis.avinfo && window.webapis.avinfo.isHdrTvSupport()) hdrFormats = ["hdr10", "hlg"];
      } catch (error) {}
      return {
        streamingProtocols: ["hls", "dash", "http"],
        containers: ["mp4", "mkv", "mpegts", "webm"],
        videoCodecs: ["h264", "hevc", "vp9"],
        audioCodecs: ["aac", "ac3", "eac3", "mp3", "opus", "vorbis"],
        hdrFormats: hdrFormats,
        processingModes: ["direct", "remux", "transcode_audio", "transcode"],
        maximumHeight: maximumHeight,
        maximumAudioChannels: 8,
        subtitleModes: ["embedded", "external", "burn"]
      };
    },
    createPlayer: function (host) {
      if (!host || typeof host.appendChild !== "function") throw new Error("A player host element is required.");
      var avplay = avplayApi();
      return avplay ? new AvPlayPlayer(host, avplay) : new Html5Player(host);
    },
    exitApp: exitApp
  };

  registerRemoteKeys();
  installLifecycleHandlers();
}());
