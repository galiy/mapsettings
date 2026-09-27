// map-settings — клиентская логика страницы «Настройки МАП».
// Два блока связи (чтение/запись), запуск без авто-чтения, запись только по
// конкретному параметру, после записи — полное перечитывание и диалог.
(function () {
  'use strict';

  // ------------------------------------------------------------------ helpers
  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined && text !== null) e.textContent = String(text);
    return e;
  }
  function api(url, opts) {
    opts = opts || {};
    opts.credentials = 'same-origin';
    if (opts.body && typeof opts.body !== 'string') {
      opts.headers = opts.headers || {};
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(opts.body);
    }
    return fetch(url, opts).then(function (r) {
      return r.text().then(function (t) {
        var data = null;
        try { data = t ? JSON.parse(t) : null; } catch (e) {}
        if (!r.ok) {
          var msg = (data && (data.error || data.message)) || t || ('HTTP ' + r.status);
          throw new Error(msg);
        }
        return data;
      });
    });
  }
  function setStatus(node, text, cls) {
    if (!node) return;
    node.textContent = text || '';
    node.className = 'ms-status' + (cls ? ' ' + cls : '');
  }
  function fmt(v) {
    if (v === null || v === undefined) return '—';
    if (typeof v === 'number') return (Math.round(v * 1000) / 1000).toString();
    return String(v);
  }
  function fmtVer(raw) {
    if (raw === null || raw === undefined) return '—';
    var r = Math.round(raw) & 0xff;
    return (r & 0x1f) + '.' + (r >> 5);
  }
  function parseVer(s) {
    s = String(s).trim().replace(',', '.');
    if (s === '') return NaN;
    var parts = s.split('.');
    var int = parseInt(parts[0], 10);
    var frac = parts.length > 1 ? parseInt(parts[1], 10) : 0;
    if (isNaN(int)) int = 0;
    if (isNaN(frac)) frac = 0;
    if (int < 0 || int > 31 || frac < 0 || frac > 7) return NaN;
    return (frac << 5) | int;
  }
  // fmtHHMM/parseHHMM — время «ЧЧ:ММ» (часы в старших 5 битах, десятки минут в
  // младших 3). Обратный разбор — для записи.
  function pad2(n) { return (n < 10 ? '0' : '') + n; }
  function fmtHHMM(raw) {
    if (raw === null || raw === undefined) return '—';
    var r = Math.round(raw) & 0xff;
    return pad2(r >> 3) + ':' + pad2((r & 7) * 10);
  }
  function parseHHMM(s) {
    var m = String(s).trim().match(/^(\d{1,2}):(\d{1,2})$/);
    if (!m) return NaN;
    var h = parseInt(m[1], 10), min = parseInt(m[2], 10);
    if (h < 0 || h > 23 || min < 0 || min > 59) return NaN;
    return (h << 3) | (Math.floor(min / 10) & 7);
  }
  // fmtChar/parseChar — значение как символ ASCII (буква).
  function fmtChar(raw) {
    if (raw === null || raw === undefined) return '—';
    var n = Math.round(raw) & 0xff;
    if (n >= 32 && n < 127) return String.fromCharCode(n);
    return '·';
  }
  function parseChar(s) {
    s = String(s);
    if (!s.length) return NaN;
    return s.charCodeAt(0) & 0xff;
  }
  // fmtFreq — частота из кода: F(Гц)=6250/код.
  function fmtFreq(raw) {
    if (raw === null || raw === undefined) return '—';
    var r = Math.round(raw);
    if (r <= 0) return '0';
    return String(Math.round(6250 / r * 10) / 10);
  }
  // applyCols — раскладка радиогруппы/полей в N колонок.
  function applyCols(node, n) {
    if (node && n > 1) { node.classList.add('ms-cols'); node.style.columnCount = String(n); }
    return node;
  }
  // fmtDays12/parseDays12 — значение в днях (код/12); 255 показываем как есть.
  function fmtDays12(raw) {
    if (raw === null || raw === undefined) return '—';
    var r = Math.round(raw);
    if (r === 255) return '255';
    return String(Math.round(r / 12 * 100) / 100);
  }
  function parseDays12(s) {
    var v = parseFloat(String(s).replace(',', '.'));
    if (isNaN(v)) return NaN;
    if (v === 255) return 255;
    return Math.round(v * 12);
  }
  function miniBtn(text) {
    var b = el('button', 'ms-mini', text);
    b.type = 'button';
    return b;
  }
  function setControlsEnabled(root, enabled) {
    if (!root) return;
    if (root.classList.contains('ms-input')) { root.disabled = !enabled; return; }
    var ins = root.querySelectorAll('input');
    for (var i = 0; i < ins.length; i++) ins[i].disabled = !enabled;
  }

  // ------------------------------------------------------------------- refs
  function gid(id) { return document.getElementById(id); }
  var rdProto = gid('rdProto'), rdTransport = gid('rdTransport'), rdIP = gid('rdIP'),
    rdPort = gid('rdPort'), rdSerial = gid('rdSerial'), rdBaud = gid('rdBaud'), rdUnit = gid('rdUnit'),
    rdHost = gid('rdHost'), rdLogin = gid('rdLogin'), rdPass = gid('rdPass');
  var wrProto = gid('wrProto'), wrTransport = gid('wrTransport'), wrIP = gid('wrIP'),
    wrPort = gid('wrPort'), wrSerial = gid('wrSerial'), wrBaud = gid('wrBaud'), wrUnit = gid('wrUnit'),
    wrHost = gid('wrHost'), wrLogin = gid('wrLogin'), wrPass = gid('wrPass');
  var modelEl = gid('msModel');
  var readBtn = gid('msRead'), clearBtn = gid('msClear'), statusEl = gid('msStatus');
  var homeEl = gid('msHome'), homeNavEl = gid('msHomeNav');
  var sectionEl = gid('msSection'), secReadBtn = gid('msSecRead'), secHomeBtn = gid('msSecHome'),
    secTitleEl = gid('msSecTitle'), secStatusEl = gid('msSecStatus'), secNavEl = gid('msSecNav'), secBodyEl = gid('msSecBody');
  var timeHEl = gid('msTimeH'), timeMEl = gid('msTimeM'), timeReadBtn = gid('msTimeRead'),
    timeWriteBtn = gid('msTimeWrite'), timeStatusEl = gid('msTimeStatus');
  var footEl = gid('msFoot');
  var dlg = gid('msDialog'), dlgTitle = gid('msDialogTitle'), dlgText = gid('msDialogText'), dlgOk = gid('msDialogOk');

  // ------------------------------------------------------------------- state
  var cfg = { read: { protocol: 'modbus', transport: 'tcp' }, write: { protocol: 'malina' } };
  var ports = [];
  var snapshot = null;
  var mode = '';               // определённый тип МАП
  var unlockedKeys = {};       // разблокированные опасные параметры (на сессию)
  var saveTimer = null;

  // --------------------------------------------------------- debug numbering
  var debugEnabled = false;
  var DBG_LS = 'mapSettings.dbgNums.v2';
  var debugReg = null, dbgTip = null;
  function loadDebugReg() {
    var raw = lsGet(DBG_LS, '');
    if (raw) { try { var o = JSON.parse(raw); if (o && typeof o.next === 'number' && o.ids) return o; } catch (e) {} }
    return { next: 1, ids: {} };
  }
  function saveDebugReg() { try { lsSet(DBG_LS, JSON.stringify(debugReg)); } catch (e) {} }
  function assignDbgId(node, id) { if (node && id) node.setAttribute('data-dbgid', id); return node; }
  function hideDbgTip() {
    if (dbgTip && dbgTip.parentNode) dbgTip.parentNode.removeChild(dbgTip);
    dbgTip = null;
  }
  function showDbgTip(anchor, no) {
    hideDbgTip();
    var p = el('div', 'ms-dbg-tip', '№' + no);
    document.body.appendChild(p);
    var r = anchor.getBoundingClientRect();
    p.style.top = (r.bottom + window.scrollY + 4) + 'px';
    p.style.left = (r.left + window.scrollX) + 'px';
    var pr = p.getBoundingClientRect();
    if (pr.right > window.innerWidth - 8) p.style.left = Math.max(8, window.innerWidth - pr.width - 8) + 'px';
    if (r.bottom + pr.height + 12 > window.innerHeight) p.style.top = Math.max(8, r.top + window.scrollY - pr.height - 6) + 'px';
    dbgTip = p;
  }
  function bindDbgHover(node) {
    node.addEventListener('mouseenter', function () { if (node.dataset.dbgNo) showDbgTip(node, node.dataset.dbgNo); });
    node.addEventListener('mouseleave', hideDbgTip);
    node.addEventListener('click', hideDbgTip);
  }
  function debugNumberAll() {
    if (!debugEnabled) return;
    if (!debugReg) debugReg = loadDebugReg();
    var nodes = document.querySelectorAll('[data-dbgid]');
    var changed = false;
    for (var i = 0; i < nodes.length; i++) {
      var n = nodes[i], id = n.dataset.dbgid;
      var no = debugReg.ids[id];
      if (no === undefined) { no = debugReg.next++; debugReg.ids[id] = no; changed = true; }
      n.dataset.dbgNo = String(no);
      if (!n.__dbgBound) { bindDbgHover(n); n.__dbgBound = true; }
    }
    if (changed) saveDebugReg();
  }
  function seedDebugIndex(m) {
    if (!m) return Promise.resolve();
    return api('/api/dbgindex?mode=' + encodeURIComponent(m)).then(function (list) {
      if (!list || !list.length) return;
      if (!debugReg) debugReg = loadDebugReg();
      var max = 0;
      for (var i = 0; i < list.length; i++) { debugReg.ids[list[i].id] = list[i].n; if (list[i].n > max) max = list[i].n; }
      if (debugReg.next <= max) debugReg.next = max + 1;
      saveDebugReg();
    }).catch(function () {});
  }

  function lsGet(k, d) { try { var v = localStorage.getItem(k); return v === null ? d : v; } catch (e) { return d; } }
  function lsSet(k, v) { try { localStorage.setItem(k, v); } catch (e) {} }

  // --------------------------------------------------------------- popover «?»
  var popoverEl = null;
  function hidePopover() { if (popoverEl && popoverEl.parentNode) popoverEl.parentNode.removeChild(popoverEl); popoverEl = null; }
  function showPopover(anchor, text) {
    hidePopover();
    var p = el('div', 'ms-popover', text);
    document.body.appendChild(p);
    var r = anchor.getBoundingClientRect();
    p.style.top = (r.bottom + window.scrollY + 6) + 'px';
    p.style.left = (r.left + window.scrollX) + 'px';
    var pr = p.getBoundingClientRect();
    if (pr.right > window.innerWidth - 8) p.style.left = Math.max(8, window.innerWidth - pr.width - 8) + 'px';
    if (r.bottom + pr.height + 12 > window.innerHeight) p.style.top = Math.max(8, r.top + window.scrollY - pr.height - 6) + 'px';
    popoverEl = p;
  }
  function helpBtn(text, dbgid) {
    var b = el('button', 'ms-help', '?');
    b.type = 'button';
    b.setAttribute('aria-label', 'Подсказка');
    b.addEventListener('click', function (e) {
      e.preventDefault(); e.stopPropagation();
      if (popoverEl && popoverEl._anchor === b) { hidePopover(); return; }
      showPopover(b, text || 'Нет описания в документации.');
      if (popoverEl) popoverEl._anchor = b;
    });
    return assignDbgId(b, dbgid);
  }
  function attachHelp(anchorEl, text, dbgid) {
    if (!anchorEl || !anchorEl.parentNode) return;
    anchorEl.parentNode.insertBefore(helpBtn(text, dbgid || (anchorEl.id + ':help')), anchorEl.nextSibling);
  }
  document.addEventListener('click', function (e) {
    if (popoverEl && !popoverEl.contains(e.target) && !e.target.classList.contains('ms-help')) hidePopover();
  });
  document.addEventListener('keydown', function (e) { if (e.key === 'Escape') { hidePopover(); hideDialog(); } });

  // ---------------------------------------------------------------- dialog
  function showDialog(title, text, ok) {
    dlgTitle.textContent = title || '';
    dlgText.textContent = text || '';
    dlg.classList.toggle('ok', !!ok);
    dlg.hidden = false;
  }
  function hideDialog() { dlg.hidden = true; }
  dlgOk.addEventListener('click', hideDialog);
  dlg.addEventListener('click', function (e) { if (e.target === dlg) hideDialog(); });

  // --------------------------------------------------------------- settings
  var BAUDS = [9600, 19200, 38400, 57600, 115200];
  function fillBaud(sel) {
    sel.innerHTML = '';
    BAUDS.forEach(function (b) { var o = el('option', null, String(b)); o.value = String(b); sel.appendChild(o); });
  }
  function fillPorts(sel, cur) {
    sel.innerHTML = '';
    var list = ports.slice();
    if (cur && list.indexOf(cur) < 0) list.unshift(cur);
    list.forEach(function (p) { var o = el('option', null, p); o.value = p; sel.appendChild(o); });
    if (!list.length) { var o = el('option', null, '(нет портов)'); o.value = ''; sel.appendChild(o); }
    if (cur) sel.value = cur;
  }
  function renderCfgUI() {
    var r = cfg.read || {}, w = cfg.write || {};
    rdProto.value = r.protocol || 'modbus';
    rdTransport.value = r.transport || 'tcp';
    rdIP.value = r.ip || ''; rdPort.value = r.port || 502; rdUnit.value = r.unit || 1;
    fillBaud(rdBaud); rdBaud.value = String(r.baud || 115200);
    fillPorts(rdSerial, r.serial || '');
    rdHost.value = r.host || ''; rdLogin.value = r.login || ''; rdPass.value = r.password || '';

    wrProto.value = w.protocol || 'malina';
    wrTransport.value = w.transport || 'tcp';
    wrIP.value = w.ip || ''; wrPort.value = w.port || 502; wrUnit.value = w.unit || 1;
    fillBaud(wrBaud); wrBaud.value = String(w.baud || 115200);
    fillPorts(wrSerial, w.serial || '');
    wrHost.value = w.host || ''; wrLogin.value = w.login || ''; wrPass.value = w.password || '';
    applyCfgVisibility();
  }
  function applyCfgVisibility() {
    // чтение
    var rdTC = gid('rdTcpCom'), rdM = gid('rdMalina');
    var rdIsMalina = rdProto.value === 'malina';
    rdM.hidden = !rdIsMalina;
    rdTC.hidden = rdIsMalina;
    document.querySelectorAll('#rdTcpCom .rd-tcp').forEach(function (n) { n.hidden = rdTransport.value === 'com'; });
    document.querySelectorAll('#rdTcpCom .rd-com').forEach(function (n) { n.hidden = rdTransport.value !== 'com'; });
    // запись
    var wrTC = gid('wrTcpCom'), wrM = gid('wrMalina');
    var wrIsMalina = wrProto.value === 'malina';
    wrM.hidden = !wrIsMalina;
    wrTC.hidden = wrIsMalina;
    document.querySelectorAll('#wrTcpCom .wr-tcp').forEach(function (n) { n.hidden = wrTransport.value === 'com'; });
    document.querySelectorAll('#wrTcpCom .wr-com').forEach(function (n) { n.hidden = wrTransport.value !== 'com'; });
  }
  function collectCfg() {
    return {
      read: {
        protocol: rdProto.value, transport: rdTransport.value,
        ip: rdIP.value.trim(), port: parseInt(rdPort.value, 10) || 502,
        serial: rdSerial.value, baud: parseInt(rdBaud.value, 10) || 115200,
        unit: parseInt(rdUnit.value, 10) || 1,
        host: rdHost.value.trim(), login: rdLogin.value, password: rdPass.value
      },
      write: {
        protocol: wrProto.value, transport: wrTransport.value,
        ip: wrIP.value.trim(), port: parseInt(wrPort.value, 10) || 502,
        serial: wrSerial.value, baud: parseInt(wrBaud.value, 10) || 115200,
        unit: parseInt(wrUnit.value, 10) || 1,
        host: wrHost.value.trim(), login: wrLogin.value, password: wrPass.value
      }
    };
  }
  function onCfgChanged() {
    cfg = collectCfg();
    applyCfgVisibility();
    if (saveTimer) clearTimeout(saveTimer);
    saveTimer = setTimeout(function () {
      api('/api/config', { method: 'POST', body: cfg }).then(function () {
        // Любое изменение настроек очищает параметры.
        clearAll();
        setStatus(statusEl, 'Настройки сохранены. Нажмите «Читать».', 'ok');
      }).catch(function (e) {
        setStatus(statusEl, 'Ошибка сохранения настроек: ' + e.message, 'err');
      });
    }, 400);
  }

  // ------------------------------------------------------------------- routes
  function showHome() {
    homeEl.hidden = false; sectionEl.hidden = true;
    buildNavList(homeNavEl, null);
    debugNumberAll();
  }
  function buildNavList(container, current) {
    container.innerHTML = '';
    if (!snapshot) { container.appendChild(el('div', 'missing', 'Нажмите «Читать», чтобы загрузить разделы.')); return; }
    allGroups().forEach(function (g) {
      var cls = 'ms-nav-btn' + (g.name === current ? ' ms-nav-current' : '');
      var b = assignDbgId(el('button', cls, g.name), 'nav:group:' + encodeURIComponent(g.name));
      b.type = 'button';
      b.addEventListener('click', function () { goGroup(g.name); });
      container.appendChild(b);
    });
    if (snapshot.actions && snapshot.actions.length) {
      var cls2 = 'ms-nav-btn ms-nav-accent' + (current === '__actions__' ? ' ms-nav-current' : '');
      var ab = assignDbgId(el('button', cls2, 'Управляющие воздействия'), 'nav:actions');
      ab.type = 'button';
      ab.addEventListener('click', goActions);
      container.appendChild(ab);
    }
  }
  function allGroups() {
    var order = [], map = {};
    function add(groups, isSettings) {
      (groups || []).forEach(function (g) {
        if (!map[g.name]) { map[g.name] = { name: g.name, settings: [], monitor: [] }; order.push(g.name); }
        var dst = isSettings ? map[g.name].settings : map[g.name].monitor;
        for (var i = 0; i < g.params.length; i++) dst.push(g.params[i]);
      });
    }
    if (snapshot) { add(snapshot.settings, true); add(snapshot.monitor, false); }
    return order.map(function (n) { return map[n]; });
  }
  function findGroup(name) {
    var found = null;
    allGroups().forEach(function (g) { if (g.name === name) found = g; });
    return found;
  }
  function showGroup(name) {
    homeEl.hidden = true; sectionEl.hidden = false;
    setStatus(secStatusEl, '', '');
    secTitleEl.textContent = name;
    buildNavList(secNavEl, name);
    secBodyEl.innerHTML = '';
    var g = findGroup(name);
    if (!snapshot || !g || (!g.settings.length && !g.monitor.length)) {
      secBodyEl.appendChild(el('div', 'missing', snapshot ? 'Нет данных' : 'Сначала нажмите «Читать».'));
      debugNumberAll();
      return;
    }
    if (g.settings.length) {
      var c1 = el('div', 'ms-group-wrap');
      groupedTables(c1, [{ name: 'Настройки', params: g.settings }], true, 'settings:' + name);
      secBodyEl.appendChild(c1);
    }
    if (g.monitor.length) {
      var c2 = el('div', 'ms-group-wrap');
      groupedTables(c2, [{ name: 'Мониторинг', params: g.monitor }], false, 'monitor:' + name);
      secBodyEl.appendChild(c2);
    }
    debugNumberAll();
  }
  function showActions() {
    homeEl.hidden = true; sectionEl.hidden = false;
    setStatus(secStatusEl, '', '');
    secTitleEl.textContent = 'Управляющие воздействия';
    buildNavList(secNavEl, '__actions__');
    secBodyEl.innerHTML = '';
    if (!snapshot) { secBodyEl.appendChild(el('div', 'missing', 'Сначала нажмите «Читать».')); debugNumberAll(); return; }
    renderActionsInto(secBodyEl, snapshot.actions);
    var relWrap = el('div', 'ms-relays');
    relWrap.id = 'msRelays';
    secBodyEl.appendChild(relWrap);
    renderRelays(relWrap);
    debugNumberAll();
  }

  // renderRelays — переключатели доп. реле: одна кнопка, подпись по состоянию.
  function renderRelays(container) {
    api('/api/relays' + (mode ? '?mode=' + encodeURIComponent(mode) : '')).then(function (resp) {
      container.innerHTML = '';
      var list = (resp && resp.relays) || [];
      list.forEach(function (rl) {
        var label = rl.on ? ('Реле ' + rl.num + ' выкл') : ('Реле ' + rl.num + ' вкл');
        var b = assignDbgId(el('button', 'ms-action-btn' + (rl.on ? ' relay-on' : ''), label),
          'action:relay' + rl.num + ':btn');
        b.type = 'button';
        b.addEventListener('click', function () {
          if (!window.confirm('Вы уверены?\n\n' + label)) return;
          setStatus(uiStatus(), 'Переключение: ' + label + '…');
          api('/api/action', { method: 'POST', body: { mode: mode, key: 'relay' + rl.num } }).then(function () {
            return readAll().then(function () {
              showDialog('Готово', label + ' — выполнено.', true);
            });
          }).catch(function (e) {
            return readAll()
              .then(function () { showDialog('Ошибка', 'Реле ' + rl.num + ': ' + e.message, false); });
          });
        });
        container.appendChild(b);
      });
      debugNumberAll();
    }).catch(function (e) {
      container.innerHTML = '';
      container.appendChild(el('div', 'missing', 'Состояние реле недоступно: ' + e.message));
    });
  }
  function renderRoute() {
    var h = location.hash || '#home';
    if (h.indexOf('#group/') === 0) showGroup(decodeURIComponent(h.slice(7)));
    else if (h === '#actions') showActions();
    else showHome();
  }
  function goHome() { location.hash = '#home'; }
  function goGroup(n) { location.hash = '#group/' + encodeURIComponent(n); }
  function goActions() { location.hash = '#actions'; }
  window.addEventListener('hashchange', renderRoute);

  // ------------------------------------------------------------------ render
  function renderActionsInto(container, actions) {
    container.innerHTML = '';
    var list = el('div', 'ms-actions');
    (actions || []).forEach(function (a) {
      var wrap = el('span', 'ms-action-wrap');
      var b = assignDbgId(el('button', 'ms-action-btn', a.name), 'action:' + a.key + ':btn');
      b.type = 'button';
      if (a.confirm) b.className += ' danger';
      b.addEventListener('click', function () {
        if (a.confirm) {
          var times = a.confirmCount || 1;
          for (var ci = 0; ci < times; ci++) {
            var q = 'Выполнить «' + a.name + '»?';
            if (times > 1) q += '\n\nПодтверждение ' + (ci + 1) + ' из ' + times + '.';
            q += '\n\n' + (a.desc || '');
            if (!window.confirm(q)) return;
          }
        }
        setStatus(uiStatus(), 'Выполняется: ' + a.name + '…');
        api('/api/action', { method: 'POST', body: { mode: mode, key: a.key } }).then(function () {
          return readAll().then(function () {
            showDialog('Готово', 'Команда «' + a.name + '» выполнена.', true);
          });
        }).catch(function (e) {
          return readAll()
            .then(function () { showDialog('Ошибка', 'Команда «' + a.name + '»: ' + e.message, false); });
        });
      });
      wrap.appendChild(b);
      wrap.appendChild(helpBtn(a.desc || a.name, 'action:' + a.key + ':help'));
      list.appendChild(wrap);
    });
    container.appendChild(list);
  }

  function uiStatus() { return sectionEl.hidden ? statusEl : secStatusEl; }

  // findParamValue — значение параметра из текущего снимка по ключу.
  function findParamValue(key) {
    if (!snapshot) return null;
    var sections = [snapshot.settings, snapshot.monitor];
    for (var s = 0; s < sections.length; s++) {
      for (var i = 0; i < sections[s].length; i++) {
        var ps = sections[s][i].params;
        for (var j = 0; j < ps.length; j++) {
          if (ps[j].key === key) return ps[j].value;
        }
      }
    }
    return null;
  }

  function groupedTables(container, groups, editable, section) {
    container.innerHTML = '';
    if (!groups || !groups.length) { container.appendChild(el('div', 'missing', 'Нет данных')); return; }
    section = section || 'grp';
    groups.forEach(function (g) { container.appendChild(buildGroupTable(g, editable, section)); });
  }

  function buildGroupTable(g, editable, section) {
    var box = el('div', 'ms-group');
    box.appendChild(assignDbgId(el('h3', 'ms-group-title', g.name), section + ':' + g.name + ':title'));
    var wrap = el('div', 'ms-table-wrap');
    var tbl = el('table', 'ms-table');
    var cg = document.createElement('colgroup');
    ['c-name', 'c-addr', 'c-val', 'c-unit', 'c-range', 'c-act'].forEach(function (c) {
      var col = document.createElement('col'); col.className = c; cg.appendChild(col);
    });
    tbl.appendChild(cg);
    var thead = el('thead');
    var hr = el('tr');
    ['Параметр', 'Ячейка', 'Значение', 'Ед.', 'Диапазон', 'Действия'].forEach(function (h) {
      hr.appendChild(assignDbgId(el('th', null, h), section + ':' + g.name + ':th:' + h));
    });
    thead.appendChild(hr); tbl.appendChild(thead);
    var tbody = el('tbody');
    var canEdit = !!editable;

    g.params.forEach(function (p) {
      var tr = el('tr', 'ms-row');
      tr.dataset.key = p.key;
      var danger = !!p.danger;
      var locked = danger && p.writable && !unlockedKeys[p.key];
      tr.dataset.locked = locked ? '1' : '0';
      tr.dataset.danger = danger ? '1' : '0';

      var tdName = assignDbgId(el('td', 'ms-name'), p.key + ':name');
      if (danger) tdName.appendChild(assignDbgId(el('span', 'ms-danger', '▲ Опасно!'), p.key + ':danger'));
      tdName.appendChild(document.createTextNode(p.name));
      if (p.help) tdName.appendChild(helpBtn(p.help, p.key + ':help'));
      tr.appendChild(tdName);
      tr.appendChild(el('td', 'ms-addr', p.addr + (p.cell ? ' ' + p.cell : '')));

      var tdVal = el('td', 'ms-val');
      var writable = !!canEdit && !!p.writable;
      var hasEnum = !!(p.options && p.options.length);
      var hasBits = !!(p.bits && p.bits.length);
      var hasFields = !!(p.fields && p.fields.length);
      var cur = (p.value === null || p.value === undefined) ? '' : String(p.value);
      var rawVal = (p.raw === null || p.raw === undefined) ? 0 : p.raw;
      var edit = writable && !locked;

      function makeRadios(ed) {
        var rg = assignDbgId(el('div', 'ms-radios' + (ed ? '' : ' ms-ro-group')), p.key + ':value');
        p.options.forEach(function (o) {
          var lab = assignDbgId(el('label', 'ms-radio'), p.key + ':opt:' + o.value);
          var r = document.createElement('input');
          r.type = 'radio'; r.name = 'rad_' + p.key; r.value = String(o.value);
          if (String(o.value) === cur) r.checked = true;
          if (!ed) r.disabled = true;
          lab.appendChild(r);
          lab.appendChild(document.createTextNode((o.label && o.label !== '') ? o.label : String(o.value)));
          rg.appendChild(lab);
        });
        if (ed) { rg.dataset.editKey = p.key; rg.dataset.editOrig = cur; rg.addEventListener('change', updateState); }
        return rg;
      }
      function makeChecks(ed) {
        var cbox = assignDbgId(el('div', 'ms-checks' + (ed ? '' : ' ms-ro-group')), p.key + ':value');
        var bits = p.bits.slice().sort(function (a, b) { return a.bit - b.bit; });
        var bitsMask = 0;
        bits.forEach(function (bit) {
          var lab = assignDbgId(el('label', 'ms-radio'), p.key + ':bit:' + bit.bit);
          var cb = document.createElement('input');
          cb.type = 'checkbox'; cb.dataset.bit = String(bit.bit);
          bitsMask |= (1 << bit.bit);
          if (rawVal & (1 << bit.bit)) cb.checked = true;
          if (!ed) cb.disabled = true;
          lab.appendChild(cb);
          lab.appendChild(document.createTextNode(bit.name || ('бит ' + bit.bit)));
          cbox.appendChild(lab);
        });
        if (ed) {
          cbox.dataset.editKey = p.key; cbox.dataset.editOrig = cur;
          cbox.dataset.bitsMask = String(bitsMask); cbox.dataset.reserved = String(rawVal & ~bitsMask);
          cbox.addEventListener('change', updateState);
        }
        return cbox;
      }
      function makeFields(ed) {
        var box2 = assignDbgId(el('div', 'ms-fields' + (ed ? '' : ' ms-ro-group')), p.key + ':value');
        var mask = 0;
        p.fields.forEach(function (f, fi) {
          var fmask = (1 << f.bits) - 1;
          mask |= (fmask << f.lsb);
          var item = el('label', 'ms-field-item');
          item.appendChild(el('span', 'ms-field-label', f.label));
          var fv = (rawVal >> f.lsb) & fmask;
          var fid = p.key + ':field:' + fi;
          if (f.enum) {
            var rg2 = assignDbgId(el('div', 'ms-radios'), fid);
            Object.keys(f.enum).sort(function (a, b) { return parseInt(a, 10) - parseInt(b, 10); }).forEach(function (k) {
              var l2 = assignDbgId(el('label', 'ms-radio'), fid + ':opt:' + k);
              var r2 = document.createElement('input');
              r2.type = 'radio'; r2.name = 'fld_' + p.key + '_' + fi; r2.value = k;
              if (String(fv) === String(k)) r2.checked = true;
              if (!ed) r2.disabled = true;
              l2.appendChild(r2); l2.appendChild(document.createTextNode(k + ' — ' + f.enum[k]));
              rg2.appendChild(l2);
            });
            rg2.dataset.fieldIndex = String(fi);
            if (ed) rg2.addEventListener('change', updateState);
            item.appendChild(rg2);
          } else {
            var inp2 = document.createElement('input');
            inp2.type = 'number'; inp2.step = 'any'; inp2.className = 'ms-input ms-field-input';
            inp2.value = String(fv + (f.offset || 0));
            inp2.dataset.fieldIndex = String(fi);
            if (!ed) inp2.disabled = true; else inp2.addEventListener('input', updateState);
            item.appendChild(inp2);
          }
          box2.appendChild(item);
        });
        box2.dataset.fields = JSON.stringify(p.fields);
        box2.dataset.fieldsMask = String(mask);
        if (ed) {
          box2.dataset.editKey = p.key; box2.dataset.editOrig = String(rawVal);
          box2.dataset.reserved = String(rawVal & ~mask);
        }
        return box2;
      }

      if (p.error) {
        tdVal.appendChild(el('span', 'ms-err', p.error));
      } else if (p.format === 'days12') {
        var dv = fmtDays12(rawVal);
        if (writable) {
          var dinp = assignDbgId(document.createElement('input'), p.key + ':value');
          dinp.type = 'number'; dinp.step = 'any'; dinp.className = 'ms-input'; dinp.value = dv;
          dinp.dataset.editKey = p.key; dinp.dataset.days12 = '1'; dinp.dataset.editOrig = String(rawVal);
          dinp.addEventListener('input', updateState);
          tdVal.appendChild(dinp);
        } else {
          tdVal.appendChild(assignDbgId(el('span', 'ms-ro', dv), p.key + ':value'));
        }
      } else if (p.format === 'freq') {
        tdVal.appendChild(assignDbgId(el('span', 'ms-ro', fmtFreq(rawVal)), p.key + ':value'));
      } else if (p.format === 'char') {
        var cv = fmtChar(rawVal);
        if (writable) {
          var cinp = assignDbgId(document.createElement('input'), p.key + ':value');
          cinp.type = 'text'; cinp.maxLength = 1; cinp.className = 'ms-input ms-char'; cinp.value = cv;
          cinp.dataset.editKey = p.key; cinp.dataset.charFmt = '1'; cinp.dataset.editOrig = String(rawVal);
          cinp.addEventListener('input', updateState);
          tdVal.appendChild(cinp);
        } else {
          tdVal.appendChild(assignDbgId(el('span', 'ms-ro', cv), p.key + ':value'));
        }
      } else if (p.format === 'hhmm') {
        var hv = fmtHHMM(rawVal);
        if (writable) {
          var hinp = assignDbgId(document.createElement('input'), p.key + ':value');
          hinp.type = 'text'; hinp.className = 'ms-input ms-hhmm'; hinp.value = hv;
          hinp.dataset.editKey = p.key; hinp.dataset.hhmm = '1'; hinp.dataset.editOrig = String(rawVal);
          hinp.addEventListener('input', updateState);
          tdVal.appendChild(hinp);
        } else {
          tdVal.appendChild(assignDbgId(el('span', 'ms-ro', hv), p.key + ':value'));
        }
      } else if (p.format === 'ver') {
        var ver = fmtVer(rawVal);
        if (writable) {
          var vinp = assignDbgId(document.createElement('input'), p.key + ':value');
          vinp.type = 'text'; vinp.className = 'ms-input ms-ver'; vinp.value = ver;
          vinp.dataset.editKey = p.key; vinp.dataset.verFormat = '1'; vinp.dataset.editOrig = String(rawVal);
          vinp.addEventListener('input', updateState);
          tdVal.appendChild(vinp);
        } else {
          tdVal.appendChild(assignDbgId(el('span', 'ms-ro', ver), p.key + ':value'));
        }
      } else if (hasFields) {
        tdVal.appendChild(applyCols(makeFields(writable), p.columns));
      } else if (writable && !hasEnum && !hasBits) {
        var inp = document.createElement('input');
        inp.type = 'number'; inp.step = 'any'; inp.className = 'ms-input'; inp.value = cur;
        inp.dataset.editKey = p.key; inp.dataset.editOrig = cur;
        assignDbgId(inp, p.key + ':value');
        inp.addEventListener('input', updateState);
        tdVal.appendChild(inp);
        // Снижение буфера: код в ячейке, справа пересчёт в вольты.
        if (p.key === 'del_uaccchbuf_24h') {
          var dcalc = el('span', 'ms-calc');
          var updD = function () {
            var u = findParamValue('uacc');
            var v = parseFloat(inp.value);
            dcalc.textContent = (u != null && isFinite(v)) ? ('= ' + (v * Math.pow(2, u) / 10).toFixed(2) + ' В') : '';
          };
          inp.addEventListener('input', updD);
          tdVal.appendChild(dcalc);
          updD();
        }
        // Токи в долях ёмкости (ед. «C»): показываем пересчёт в амперы (значение × Cакб).
        if (p.unit === 'C') {
          var calc = el('span', 'ms-calc');
          var updCalc = function () {
            var cap = findParamValue('lcd_cacc');
            var frac = parseFloat(inp.value);
            calc.textContent = (cap != null && isFinite(frac)) ? ('= ' + (frac * cap).toFixed(1) + ' А') : '';
          };
          inp.addEventListener('input', updCalc);
          tdVal.appendChild(calc);
          updCalc();
        }
        if (p.text) tdVal.appendChild(el('span', 'ms-text', p.text));
      } else if (hasEnum) {
        var rnode = applyCols(makeRadios(writable), p.columns);
        tdVal.appendChild(rnode);
        // Числовое значение (только показ) справа внизу под радиокнопками.
        if (p.showValue) {
          var vspan = el('div', 'ms-numval');
          var updV = function () {
            var v = readEditValue(rnode);
            vspan.textContent = (v === '' || v === null) ? '—' : ('значение: ' + v);
          };
          rnode.addEventListener('change', updV);
          tdVal.appendChild(vspan);
          updV();
        }
      } else if (hasBits) {
        tdVal.appendChild(makeChecks(writable));
      } else {
        tdVal.appendChild(assignDbgId(el('span', 'ms-ro', fmt(p.value)), p.key + ':value'));
        if (p.text) tdVal.appendChild(el('span', 'ms-text', p.text));
      }
      tr.appendChild(tdVal);
      tr.appendChild(el('td', 'ms-unit', p.unit || ''));
      var rng = '';
      if (p.min !== null && p.min !== undefined) rng += p.min;
      if (p.max !== null && p.max !== undefined) rng += (rng ? '…' : '') + p.max;
      tr.appendChild(el('td', 'ms-range', rng));

      var root = tdVal.querySelector('[data-edit-key]');
      var tdAct = el('td', 'ms-act');
      var readB = assignDbgId(miniBtn('Читать'), p.key + ':read');
      var saveB = assignDbgId(miniBtn('Сохр.'), p.key + ':save');
      saveB.disabled = true;
      tdAct.appendChild(readB); tdAct.appendChild(saveB);
      var lockB = null;
      if (danger && p.writable) {
        lockB = assignDbgId(miniBtn(locked ? '🔒' : '🔓'), p.key + ':lock');
        lockB.className += ' ms-lock';
        lockB.setAttribute('aria-label', locked ? 'Разблокировать параметр' : 'Заблокировать параметр');
        tdAct.appendChild(lockB);
      }
      function updateState() {
        var val = root ? readEditValue(root) : '';
        var changed = !!root && val !== '' && val !== root.dataset.editOrig;
        tr.classList.toggle('changed', changed);
        saveB.disabled = !(root && tr.dataset.locked !== '1' && changed);
      }
      function setLocked(l) {
        tr.dataset.locked = l ? '1' : '0';
        if (root) setControlsEnabled(root, !l);
        if (lockB) lockB.textContent = l ? '🔒' : '🔓';
        updateState();
      }
      function applyRead(v) {
        var rawV = (v.raw === null || v.raw === undefined) ? 0 : v.raw;
        var fieldsBox = (root && root.classList.contains('ms-fields')) ? root : tdVal.querySelector('.ms-fields');
        if (fieldsBox) {
          var fields = [];
          try { fields = JSON.parse(fieldsBox.dataset.fields || '[]'); } catch (e) {}
          var fmask = parseInt(fieldsBox.dataset.fieldsMask, 10) || 0;
          if (fieldsBox.dataset.editKey) { fieldsBox.dataset.reserved = String(rawV & ~fmask); fieldsBox.dataset.editOrig = String(rawV); }
          fieldsBox.querySelectorAll('[data-field-index]').forEach(function (el2) {
            var fi = parseInt(el2.dataset.fieldIndex, 10), f = fields[fi];
            if (!f) return;
            var fv = (rawV >> f.lsb) & ((1 << f.bits) - 1);
            if (f.enum) el2.querySelectorAll('input[type=radio]').forEach(function (r) { r.checked = (r.value === String(fv)); });
            else el2.value = String(fv + (f.offset || 0));
          });
          updateState();
          return;
        }
        if (root && root.dataset.days12) {
          root.value = fmtDays12(rawV); root.dataset.editOrig = String(rawV); updateState(); return;
        }
        if (root && root.dataset.charFmt) {
          root.value = fmtChar(rawV); root.dataset.editOrig = String(rawV); updateState(); return;
        }
        if (root && root.dataset.hhmm) {
          root.value = fmtHHMM(rawV); root.dataset.editOrig = String(rawV); updateState(); return;
        }
        if (root && root.dataset.verFormat) {
          root.value = fmtVer(rawV); root.dataset.editOrig = String(rawV); updateState(); return;
        }
        if (root && root.classList.contains('ms-radios')) {
          root.querySelectorAll('input[type=radio]').forEach(function (r) { r.checked = (r.value === String(v.value)); });
          root.dataset.editOrig = String(v.value);
        } else if (root && root.classList.contains('ms-checks')) {
          var bm = parseInt(root.dataset.bitsMask, 10) || 0;
          root.querySelectorAll('input[type=checkbox]').forEach(function (cb) { cb.checked = !!(rawV & (1 << parseInt(cb.dataset.bit, 10))); });
          root.dataset.reserved = String(rawV & ~bm);
          root.dataset.editOrig = String(v.value);
        } else if (root) {
          root.value = fmt(v.value); root.dataset.editOrig = root.value;
        } else {
          var ro = tdVal.querySelector('.ms-ro');
          if (!ro) {
            var errEl = tdVal.querySelector('.ms-err');
            if (errEl) { ro = assignDbgId(el('span', 'ms-ro'), p.key + ':value'); errEl.parentNode.replaceChild(ro, errEl); }
          }
          if (ro) ro.textContent = fmt(v.value);
        }
        var txt = tdVal.querySelector('.ms-text');
        if (txt) txt.textContent = v.text || '';
        debugNumberAll();
        updateState();
      }
      function readRow() {
        readB.disabled = true;
        api('/api/param?key=' + encodeURIComponent(p.key) + (mode ? '&mode=' + encodeURIComponent(mode) : ''))
          .then(function (v) {
            if (v.error) { setStatus(uiStatus(), 'Ошибка чтения «' + p.name + '»: ' + v.error, 'err'); return; }
            if (!root && writable) { readAll(); return; }
            applyRead(v);
            setStatus(uiStatus(), 'Прочитано: ' + p.name, 'ok');
          }).catch(function (e) { setStatus(uiStatus(), 'Ошибка чтения «' + p.name + '»: ' + e.message, 'err'); })
          .finally(function () { readB.disabled = false; });
      }
      function saveRow() {
        if (!root) return;
        var val = readEditValue(root);
        var f = parseFloat(val);
        if (val === '' || isNaN(f)) { showDialog('Ошибка', 'Некорректное значение «' + p.name + '»', false); return; }
        if (danger && !window.confirm('Сохранить опасный параметр «' + p.name + '»?')) return;
        saveB.disabled = true;
        var ch = {}; ch[p.key] = f;
        setStatus(uiStatus(), 'Запись «' + p.name + '»…');
        api('/api/apply', { method: 'POST', body: { mode: mode, changes: ch, allowDanger: danger } })
          .then(function (resp) {
            var results = (resp && resp.results) || {};
            var errs = Object.keys(results);
            var text = 'Параметр «' + p.name + '» записан.';
            var ok = true;
            if (errs.length) { ok = false; text = 'Не записано: ' + errs.map(function (k) { return k + ': ' + results[k]; }).join('; '); }
            // После успешного сохранения защищённой ячейки — вернуть защиту.
            if (ok && danger) delete unlockedKeys[p.key];
            // Перечитывание сразу после ответа (обратное чтение и пауза 0,5 с —
            // на сервере), в т.ч. после неуспешной попытки.
            return readAll()
              .then(function () { showDialog(ok ? 'Готово' : 'Ошибка', text, ok); });
          })
          .catch(function (e) {
            return readAll()
              .then(function () { showDialog('Ошибка', 'Запись «' + p.name + '»: ' + e.message, false); });
          })
          .finally(function () { updateState(); });
      }
      readB.addEventListener('click', function () { readRow(); });
      saveB.addEventListener('click', saveRow);
      if (lockB) {
        lockB.addEventListener('click', function () {
          var wasLocked = tr.dataset.locked === '1';
          if (wasLocked) unlockedKeys[p.key] = true; else delete unlockedKeys[p.key];
          setLocked(!wasLocked);
        });
      }
      // Ячейка-флаг, прикреплённая к этому параметру (напр. «заморозить»).
      if (p.flag) {
        var fv = p.flag;
        var fwrap = el('label', 'ms-flag');
        var fcb = assignDbgId(document.createElement('input'), fv.key + ':flag');
        fcb.type = 'checkbox';
        fcb.checked = !!(fv.raw != null ? fv.raw : fv.value);
        fwrap.appendChild(fcb);
        fwrap.appendChild(document.createTextNode(' ' + (fv.name || 'флаг')));
        var fsave = assignDbgId(miniBtn('Сохр.'), fv.key + ':save');
        fsave.disabled = true;
        var fbase = fcb.checked;
        fcb.addEventListener('change', function () { fsave.disabled = (fcb.checked === fbase); });
        fsave.addEventListener('click', function () {
          var v = fcb.checked ? 1 : 0;
          fsave.disabled = true;
          setStatus(uiStatus(), 'Запись «' + fv.name + '»…');
          var ch = {}; ch[fv.key] = v;
          api('/api/apply', { method: 'POST', body: { mode: mode, changes: ch, allowDanger: !!fv.danger } })
            .then(function (resp) {
              var rr = (resp && resp.results) || {};
              var errs = Object.keys(rr);
              var ok = errs.length === 0;
              var text = ok ? ('«' + fv.name + '» записано.') : ('Не записано: ' + errs.map(function (k) { return k + ': ' + rr[k]; }).join('; '));
              return readAll().then(function () { showDialog(ok ? 'Готово' : 'Ошибка', text, ok); });
            })
            .catch(function (e) { return readAll().then(function () { showDialog('Ошибка', 'Запись «' + fv.name + '»: ' + e.message, false); }); });
        });
        tdAct.appendChild(fwrap);
        tdAct.appendChild(fsave);
        if (fv.help) tdAct.appendChild(helpBtn(fv.help, fv.key + ':help'));
      }
      tr.appendChild(tdAct);
      if (root && locked) setControlsEnabled(root, false);
      updateState();
      tbody.appendChild(tr);
    });
    tbl.appendChild(tbody);
    wrap.appendChild(tbl);
    box.appendChild(wrap);
    return box;
  }

  function readEditValue(root) {
    if (root.dataset.days12) { var dd = parseDays12(root.value); return isNaN(dd) ? '' : String(dd); }
    if (root.dataset.charFmt) { var cc = parseChar(root.value); return isNaN(cc) ? '' : String(cc); }
    if (root.dataset.hhmm) { var hh = parseHHMM(root.value); return isNaN(hh) ? '' : String(hh); }
    if (root.dataset.verFormat) { var pv = parseVer(root.value); return isNaN(pv) ? '' : String(pv); }
    if (root.classList.contains('ms-fields')) {
      var fields = [];
      try { fields = JSON.parse(root.dataset.fields || '[]'); } catch (e) {}
      var rawOut = parseInt(root.dataset.reserved, 10) || 0;
      root.querySelectorAll('[data-field-index]').forEach(function (fe) {
        var fi = parseInt(fe.dataset.fieldIndex, 10), f = fields[fi];
        if (!f) return;
        var fv;
        if (f.enum) { var r = fe.querySelector('input[type=radio]:checked'); fv = r ? parseInt(r.value, 10) : 0; }
        else { fv = parseFloat(fe.value); if (isNaN(fv)) fv = 0; fv = Math.round(fv - (f.offset || 0)); }
        rawOut |= ((fv & ((1 << f.bits) - 1)) << f.lsb);
      });
      return String(rawOut);
    }
    if (root.classList.contains('ms-radios')) {
      var r2 = root.querySelector('input[type=radio]:checked');
      return r2 ? r2.value : '';
    }
    if (root.classList.contains('ms-checks')) {
      var mask = 0;
      root.querySelectorAll('input[type=checkbox]').forEach(function (cb) { if (cb.checked) mask |= (1 << parseInt(cb.dataset.bit, 10)); });
      var reserved = parseInt(root.dataset.reserved, 10) || 0;
      return String(mask | reserved);
    }
    return root.value;
  }

  // ------------------------------------------------------------------- read
  function readAll() {
    readBtn.disabled = true; secReadBtn.disabled = true;
    setStatus(uiStatus(), 'Чтение параметров…');
    return api('/api/settings').then(function (snap) {
      snapshot = snap;
      mode = snap.mode || '';
      modelEl.textContent = (mode === 'titanator') ? 'Титанатор' : (mode === 'dominator' ? 'Доминатор' : 'не определён');
      var errs = (snap.errors && snap.errors.length) ? ' Ошибки чтения: ' + snap.errors.join('; ') : '';
      return (debugEnabled ? seedDebugIndex(mode) : Promise.resolve()).then(function () {
        renderRoute();
        setStatus(uiStatus(), 'Прочитано ' + (snap.read_at || '') + errs, errs ? 'warn' : 'ok');
      });
    }).catch(function (e) {
      setStatus(uiStatus(), 'Ошибка чтения: ' + e.message, 'err');
      throw e;
    }).finally(function () { readBtn.disabled = false; secReadBtn.disabled = false; });
  }
  function clearAll() {
    snapshot = null; mode = '';
    modelEl.textContent = 'не определён';
    goHomeIfSection();
    renderRoute();
    setStatus(statusEl, 'Данные очищены. Нажмите «Читать».', '');
  }
  function goHomeIfSection() { if (!sectionEl.hidden) location.hash = '#home'; }

  // ------------------------------------------------------------------- time
  function readTime() {
    setStatus(timeStatusEl, 'Чтение времени…');
    api('/api/time').then(function (st) {
      timeHEl.value = st.hour; timeMEl.value = st.minute;
      setStatus(timeStatusEl, 'Время МАП: ' + String(st.hour).padStart(2, '0') + ':' + String(st.minute).padStart(2, '0'), 'ok');
    }).catch(function (e) { setStatus(timeStatusEl, 'Ошибка чтения времени: ' + e.message, 'err'); });
  }
  function writeTime() {
    var h = parseInt(timeHEl.value, 10), m = parseInt(timeMEl.value, 10);
    setStatus(timeStatusEl, 'Запись времени…');
    api('/api/time', { method: 'POST', body: { mode: mode, hour: h, minute: m } }).then(function () {
      return readAll().then(function () {
        showDialog('Готово', 'Время записано: ' + String(h).padStart(2, '0') + ':' + String(m).padStart(2, '0'), true);
      });
    }).catch(function (e) {
      return readAll()
        .then(function () { showDialog('Ошибка', 'Запись времени: ' + e.message, false); });
    });
  }

  // ------------------------------------------------------------------- init
  rdProto.addEventListener('change', onCfgChanged);
  rdTransport.addEventListener('change', onCfgChanged);
  wrProto.addEventListener('change', onCfgChanged);
  wrTransport.addEventListener('change', onCfgChanged);
  [rdIP, rdPort, rdSerial, rdBaud, rdUnit, rdHost, rdLogin, rdPass,
   wrIP, wrPort, wrSerial, wrBaud, wrUnit, wrHost, wrLogin, wrPass].forEach(function (n) {
    n.addEventListener('change', onCfgChanged);
  });
  readBtn.addEventListener('click', readAll);
  secReadBtn.addEventListener('click', readAll);
  secHomeBtn.addEventListener('click', goHome);
  clearBtn.addEventListener('click', function () { clearAll(); });
  timeReadBtn.addEventListener('click', readTime);
  timeWriteBtn.addEventListener('click', writeTime);

  attachHelp(rdProto, 'Протокол чтения: MODBUS (только чтение), MICROART (родной ASCII) или Малина (HTTP read_memory.php).');
  attachHelp(wrProto, 'Протокол записи: MICROART (родной ASCII) или Малина (write_eeprom.php, mapd сам делает обрамление).');
  attachHelp(readBtn, 'Читать: снять с МАП полный снимок параметров выбранным способом чтения.');
  attachHelp(clearBtn, 'Очистить: сбросить все данные и параметры, как будто программа только что запущена.');

  var now = new Date();
  timeHEl.value = now.getHours();
  timeMEl.value = now.getMinutes();
  footEl.textContent = 'Запись — только по конкретному параметру; после записи данные перечитываются.';

  // Стартуем без чтения: сначала конфиг, затем пустой экран.
  api('/api/config').then(function (c) {
    if (c && c.read) cfg = { read: c.read, write: c.write };
    if (c && c.debug) { debugEnabled = true; debugNumberAll(); }
    return api('/api/ports').catch(function () { return { ports: [] }; });
  }).then(function (pr) {
    ports = (pr && pr.ports) || [];
    renderCfgUI();
    renderRoute();
  }).catch(function () {
    renderCfgUI();
    renderRoute();
  });
})();
