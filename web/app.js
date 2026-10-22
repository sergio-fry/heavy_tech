(function () {
  'use strict';

  var audio = document.getElementById('player');
  if (!audio) { return; }

  var mini = document.getElementById('mini');
  var miniTitle = document.getElementById('mini-title');
  var cover = document.body.getAttribute('data-cover') || '';
  var brand = (document.querySelector('.brand') || {}).textContent || '';

  var current = null;        // номер играющего выпуска
  var currentDuration = 0;   // из data-duration, если метаданные ещё не загружены
  var pendingSeek = 0;
  var beaconed = null;
  var lastSaved = 0;
  var speed = 1;
  var toastTimer = null;

  // --- безопасное хранилище ---
  function read(key) {
    try { return window.localStorage.getItem(key); } catch (e) { return null; }
  }
  function write(key, value) {
    try { window.localStorage.setItem(key, value); } catch (e) { /* приватный режим */ }
  }
  function remove(key) {
    try { window.localStorage.removeItem(key); } catch (e) { /* приватный режим */ }
  }

  function fmt(sec) {
    if (!isFinite(sec) || sec < 0) { sec = 0; }
    sec = Math.floor(sec);
    var h = Math.floor(sec / 3600);
    var m = Math.floor((sec % 3600) / 60);
    var s = sec % 60;
    var tail = (s < 10 ? '0' : '') + s;
    if (h > 0) { return h + ':' + (m < 10 ? '0' : '') + m + ':' + tail; }
    return m + ':' + tail;
  }

  function total() {
    return (isFinite(audio.duration) && audio.duration > 0) ? audio.duration : currentDuration;
  }

  function showToast(text) {
    var el = document.querySelector('.js-toast') || document.querySelector('.toast');
    if (!el) { return; }
    el.textContent = text;
    el.classList.add('show');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { el.classList.remove('show'); }, 2000);
  }

  function playButtons() {
    return document.querySelectorAll('.js-play');
  }

  function setIcons(playing) {
    playButtons().forEach(function (btn) {
      var isCurrent = btn.id === 'mini-play' || String(btn.getAttribute('data-episode')) === String(current);
      if (!isCurrent) { return; }
      btn.innerHTML = playing ? '&#10074;&#10074;' : '&#9654;';
      btn.setAttribute('aria-label', playing ? 'Пауза' : 'Слушать');
    });
  }

  function setTime() {
    var label = fmt(audio.currentTime) + ' / ' + (total() ? fmt(total()) : '--:--');
    document.querySelectorAll('.js-time').forEach(function (el) { el.textContent = label; });
    document.querySelectorAll('.js-seek').forEach(function (el) {
      if (total() > 0) {
        el.max = String(Math.floor(total()));
        el.value = String(Math.floor(audio.currentTime));
      }
      el.setAttribute('aria-valuetext', fmt(audio.currentTime) + ' из ' + (total() ? fmt(total()) : 'неизвестно'));
    });
  }

  function applySpeed() {
    audio.playbackRate = speed;
    document.querySelectorAll('.js-speed').forEach(function (el) {
      el.textContent = (speed === 1 ? '1' : String(speed)) + 'x';
      el.setAttribute('aria-label', 'Скорость ' + speed + 'x');
    });
  }

  function seekBy(delta) {
    if (!current) { return; }
    var target = audio.currentTime + delta;
    if (target < 0) { target = 0; }
    if (total() > 0 && target > total() - 1) { target = total() - 1; }
    try { audio.currentTime = target; } catch (e) { pendingSeek = target; }
    setTime();
  }

  function updateMini() {
    if (!mini) { return; }
    if (!current) { mini.hidden = true; return; }
    var inline = document.getElementById('inline-player');
    var visible = false;
    if (inline) {
      var rect = inline.getBoundingClientRect();
      visible = rect.bottom > 0 && rect.top < window.innerHeight;
    }
    mini.hidden = visible;
  }

  function setMediaSession(title) {
    if (!('mediaSession' in navigator) || typeof MediaMetadata === 'undefined') { return; }
    var artwork = cover ? [{ src: cover, sizes: '512x512' }] : [];
    try {
      navigator.mediaSession.metadata = new MediaMetadata({
        title: title,
        artist: brand,
        album: document.title,
        artwork: artwork
      });
      if (navigator.mediaSession.setActionHandler) {
        navigator.mediaSession.setActionHandler('play', function () { audio.play().catch(function () {}); });
        navigator.mediaSession.setActionHandler('pause', function () { audio.pause(); });
        navigator.mediaSession.setActionHandler('seekbackward', function () { seekBy(-15); });
        navigator.mediaSession.setActionHandler('seekforward', function () { seekBy(15); });
        navigator.mediaSession.setActionHandler('seekto', function (details) {
          if (details && typeof details.seekTime === 'number') {
            try { audio.currentTime = details.seekTime; } catch (e) { pendingSeek = details.seekTime; }
          }
        });
      }
    } catch (e) { /* старый браузер */ }
  }

  function beacon() {
    if (current === null || beaconed === current) { return; }
    beaconed = current;
    try {
      fetch('/track/play', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ episode: parseInt(current, 10) }),
        keepalive: true
      }).catch(function () {});
    } catch (e) { /* сеть недоступна */ }
  }

  function startFrom(btn) {
    var ep = btn.getAttribute('data-episode');
    var src = btn.getAttribute('data-src');
    if (!ep || !src) { return; }
    var changed = String(current) !== String(ep) || audio.getAttribute('src') !== src;
    if (changed) {
      current = ep;
      currentDuration = parseFloat(btn.getAttribute('data-duration')) || 0;
      beaconed = null;
      pendingSeek = 0;
      var saved = parseFloat(read('ht:pos:' + ep) || '0');
      if (saved > 5 && (!currentDuration || saved < currentDuration - 10)) { pendingSeek = saved; }
      audio.src = src;
      audio.load();
      if (miniTitle) { miniTitle.textContent = btn.getAttribute('data-title') || ''; }
      setMediaSession(btn.getAttribute('data-title') || '');
      applySpeed();
    }
    audio.play().catch(function () {});
    updateMini();
  }

  // --- клики ---
  document.addEventListener('click', function (event) {
    var play = event.target.closest('.js-play');
    if (play) {
      if (play.getAttribute('data-src')) {
        var ep = play.getAttribute('data-episode');
        if (String(current) === String(ep) && !audio.paused) { audio.pause(); return; }
        startFrom(play);
        return;
      }
      if (audio.paused) { audio.play().catch(function () {}); } else { audio.pause(); }
      return;
    }

    var chapter = event.target.closest('.js-chapter');
    if (chapter) {
      var start = parseFloat(chapter.getAttribute('data-start')) || 0;
      if (!current) { return; }
      try { audio.currentTime = start; } catch (e) { pendingSeek = start; }
      if (audio.paused) { audio.play().catch(function () {}); }
      setTime();
      return;
    }

    var share = event.target.closest('.js-share');
    if (share) {
      var url = share.getAttribute('data-url') || window.location.href;
      var title = share.getAttribute('data-title') || document.title;
      if (navigator.share) {
        navigator.share({ title: title, url: url }).catch(function () {});
      } else if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(url).then(function () { showToast('Ссылка скопирована'); });
      }
      return;
    }

    var copy = event.target.closest('.js-copy-rss');
    if (copy) {
      var feed = copy.getAttribute('data-url');
      function done() { showToast('Ссылка на RSS скопирована'); }
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(feed).then(done, fallback);
      } else { fallback(); }
      function fallback() {
        try {
          var ta = document.createElement('textarea');
          ta.value = feed;
          document.body.appendChild(ta);
          ta.select();
          document.execCommand('copy');
          document.body.removeChild(ta);
          done();
        } catch (e) { /* игнорируем */ }
      }
      return;
    }

    var reset = event.target.closest('.js-reset');
    if (reset) {
      if (current !== null) { remove('ht:pos:' + current); }
      try { audio.currentTime = 0; } catch (e) { pendingSeek = 0; }
      pendingSeek = 0;
      lastSaved = 0;
      setTime();
      return;
    }

    var speedBtn = event.target.closest('.js-speed');
    if (speedBtn) {
      var speeds = [1, 1.25, 1.5, 1.75, 2];
      var index = speeds.indexOf(speed);
      speed = speeds[(index + 1) % speeds.length];
      write('ht:speed', String(speed));
      applySpeed();
      return;
    }

    if (event.target.closest('#mini-close')) {
      audio.pause();
      audio.removeAttribute('src');
      audio.load();
      current = null;
      beaconed = null;
      if (mini) { mini.hidden = true; }
      setIcons(false);
      return;
    }
  });

  // --- события аудио ---
  audio.addEventListener('play', function () {
    beacon();
    setIcons(true);
    updateMini();
  });
  audio.addEventListener('pause', function () {
    setIcons(false);
    if (current !== null && audio.currentTime > 5 && total() > 0 && audio.currentTime < total() - 5) {
      write('ht:pos:' + current, String(Math.floor(audio.currentTime)));
    }
  });
  audio.addEventListener('loadedmetadata', function () {
    if (pendingSeek > 0) {
      try { audio.currentTime = pendingSeek; } catch (e) { /* игнорируем */ }
      pendingSeek = 0;
    }
    setTime();
  });
  audio.addEventListener('timeupdate', function () {
    setTime();
    if (current !== null && Math.abs(audio.currentTime - lastSaved) >= 5) {
      lastSaved = audio.currentTime;
      if (audio.currentTime > 5 && total() > 0 && audio.currentTime < total() - 5) {
        write('ht:pos:' + current, String(Math.floor(audio.currentTime)));
      }
    }
  });
  audio.addEventListener('ended', function () {
    if (current !== null) { remove('ht:pos:' + current); }
    setIcons(false);
    setTime();
  });

  // --- перемотка ---
  document.querySelectorAll('.js-seek').forEach(function (el) {
    el.addEventListener('input', function () {
      var value = parseFloat(el.value);
      if (total() > 0) {
        try { audio.currentTime = value; } catch (e) { pendingSeek = value; }
      }
    });
    el.addEventListener('keydown', function (event) {
      if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
        event.preventDefault();
        seekBy(event.key === 'ArrowLeft' ? -15 : 15);
      }
    });
  });

  // --- клавиатура: пробел на кнопке play обрабатывает браузер; глобально не воруем ---

  // --- восстановление скорости и мини-панели ---
  var savedSpeed = parseFloat(read('ht:speed') || '1');
  if ([1, 1.25, 1.5, 1.75, 2].indexOf(savedSpeed) >= 0) { speed = savedSpeed; }
  applySpeed();
  window.addEventListener('scroll', updateMini, { passive: true });
  window.addEventListener('resize', updateMini);

  // --- поиск и фильтры архива ---
  var query = document.getElementById('q');
  if (query) {
    var year = document.getElementById('f-year');
    var guests = document.getElementById('f-guests');
    var video = document.getElementById('f-video');
    var noResults = document.getElementById('no-results');
    var rows = Array.prototype.slice.call(document.querySelectorAll('.row'));

    function applyFilters() {
      var q = (query.value || '').trim().toLowerCase();
      var y = year ? year.value : '';
      var g = guests ? guests.value : '';
      var v = video ? video.value : '';
      var visible = 0;
      rows.forEach(function (row) {
        var ok = true;
        if (q && (row.getAttribute('data-search') || '').indexOf(q) === -1) { ok = false; }
        if (ok && y && row.getAttribute('data-year') !== y) { ok = false; }
        if (ok && g && row.getAttribute('data-guests') !== g) { ok = false; }
        if (ok && v && row.getAttribute('data-video') !== v) { ok = false; }
        row.hidden = !ok;
        if (ok) { visible++; }
      });
      if (noResults) { noResults.hidden = visible !== 0; }
    }

    [query, year, guests, video].forEach(function (el) {
      if (!el) { return; }
      el.addEventListener('input', applyFilters);
      el.addEventListener('change', applyFilters);
    });
  }
})();
