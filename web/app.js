// map-settings — страница «Настройки МАП» (отдельная программа).
// Чтение/запись по Modbus TCP к mapgateway. IP и порт хранятся на клиенте
// (localStorage). Тип МАП в настройках не хранится: он определяется
// автоматически при первом чтении и показывается в селекторе модели.
(function () {
  'use strict';

  var DEF_IP = '';
  var DEF_PORT = '502';
  var LS_IP = 'mapSettings.ip';
  var LS_PORT = 'mapSettings.port';

  var modeEl = document.getElementById('msMode');
  var ipEl = document.getElementById('msIP');
  var portEl = document.getElementById('msPort');
  var readBtn = document.getElementById('msRead');
  var statusEl = document.getElementById('msStatus');
  var homeEl = document.getElementById('msHome');
  var homeNavEl = document.getElementById('msHomeNav');
  var sectionEl = document.getElementById('msSection');
  var secReadBtn = document.getElementById('msSecRead');
  var secWriteBtn = document.getElementById('msSecWrite');
  var secHomeBtn = document.getElementById('msSecHome');
  var secTitleEl = document.getElementById('msSecTitle');
  var secStatusEl = document.getElementById('msSecStatus');
  var secNavEl = document.getElementById('msSecNav');
  var secBodyEl = document.getElementById('msSecBody');
  var footEl = document.getElementById('msFoot');
  var timeHEl = document.getElementById('msTimeH');
  var timeMEl = document.getElementById('msTimeM');
  var timeReadBtn = document.getElementById('msTimeRead');
  var timeWriteBtn = document.getElementById('msTimeWrite');
  var timeStatusEl = document.getElementById('msTimeStatus');

  // snapshot — последний прочитанный снимок (общий для всех экранов).
  var snapshot = null;
  // Ключи опасных параметров, разблокированных вручную на время сессии.
  var unlockedKeys = {};

  function lsGet(k, d) { try { var v = localStorage.getItem(k); return v === null ? d : v; } catch (e) { return d; } }
  function lsSet(k, v) { try { localStorage.setItem(k, v); } catch (e) {} }

  // modeDetected — тип МАП уже определён (автоматически или выбран вручную).
  // Пока false, сервер определяет модель по _DevOpt при чтении.
  var modeDetected = false;
  ipEl.value = lsGet(LS_IP, DEF_IP);
  portEl.value = lsGet(LS_PORT, DEF_PORT);
  var autoReadDone = false;
  function maybeAutoRead() {
    if (autoReadDone) return;
    if (!ipEl.value) return;
    autoReadDone = true;
    readAll();
  }
  // Серверные значения по умолчанию (флаги программы) — подставляем, только
  // если в браузере ещё ничего не сохранено.
  fetch('/api/config', { credentials: 'same-origin' }).then(function (r) { return r.json(); }).then(function (cfg) {
    if (lsGet(LS_IP, '') === '') ipEl.value = cfg.ip || '';
    if (lsGet(LS_PORT, '') === '') portEl.value = String(cfg.port || 502);
    if (cfg.debug) { debugEnabled = true; debugNumberAll(); }
    maybeAutoRead();
  }).catch(function () { maybeAutoRead(); });
  function persist() {
    var t = target();
    lsSet(LS_IP, t.ip);
    lsSet(LS_PORT, String(t.port));
    // В mapsettings.json сохраняем только IP/порт подключения (тип МАП не
    // хранится — определяется автоматически).
    fetch('/api/config', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ip: t.ip, port: t.port })
    }).catch(function () {});
  }
  // Ручной выбор модели — использовать его при последующих чтениях.
  modeEl.addEventListener('change', function () { modeDetected = true; persist(); });
  // Смена адреса — тип определяем заново при следующем чтении.
  ipEl.addEventListener('change', function () { modeDetected = false; persist(); });
  portEl.addEventListener('change', function () { modeDetected = false; persist(); });

  function target() {
    return { mode: modeEl.value, ip: ipEl.value.trim(), port: parseInt(portEl.value, 10) || 502 };
  }
  // reqMode — тип для запроса: пусто, пока тип не определён, чтобы сервер
  // определил его сам (иначе после смены IP отправится устаревший тип).
  function reqMode() { return modeDetected ? modeEl.value : ''; }
  function modeQueryPart() {
    var m = reqMode();
    return m ? 'mode=' + encodeURIComponent(m) + '&' : '';
  }
  function api(path, opts) {
    opts = opts || {};
    opts.credentials = 'same-origin';
    if (opts.body && typeof opts.body !== 'string') {
      opts.headers = { 'Content-Type': 'application/json' };
      opts.body = JSON.stringify(opts.body);
    }
    return fetch(path, opts).then(function (r) {
      return r.text().then(function (t) {
        if (!r.ok) throw new Error(t || ('HTTP ' + r.status));
        try { return JSON.parse(t); } catch (e) { throw new Error('Некорректный ответ сервера'); }
      });
    });
  }
  function setStatus(el, text, cls) {
    if (!el) return;
    el.textContent = text || '';
    el.className = 'ms-status' + (cls ? ' ' + cls : '');
  }
  // uiStatus — статус активного экрана (главная или раздел).
  function uiStatus() { return sectionEl.hidden ? statusEl : secStatusEl; }
  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text !== undefined && text !== null) n.textContent = text;
    return n;
  }
  function fmt(v) {
    if (v === null || v === undefined) return '—';
    if (typeof v === 'number') return (Math.round(v * 1000) / 1000).toString();
    return String(v);
  }
  // fmtVer/parseVer — версия «целая.дробная» (младшие 5 бит — целая,
  // старшие 3 — дробная), напр. raw 100 → «4.3».
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
  // miniBtn — маленькая кнопка в колонке «Действия».
  function miniBtn(text) {
    var b = el('button', 'ms-mini', text);
    b.type = 'button';
    return b;
  }
  // setControlsEnabled включает/выключает редактор значения (input/радио/чекбоксы).
  function setControlsEnabled(root, enabled) {
    if (root.classList.contains('ms-input')) {
      root.disabled = !enabled;
      return;
    }
    var ins = root.querySelectorAll('input');
    for (var i = 0; i < ins.length; i++) ins[i].disabled = !enabled;
  }

  // readEditValue — текущее значение поля правки: значение input/select либо
  // выбранной радиокнопки в группе.
  function readEditValue(root) {
    if (root.dataset.verFormat) {
      var pv = parseVer(root.value);
      return isNaN(pv) ? '' : String(pv);
    }
    if (root.classList.contains('ms-fields')) {
      var fields = [];
      try { fields = JSON.parse(root.dataset.fields || '[]'); } catch (e) {}
      var rawOut = parseInt(root.dataset.reserved, 10) || 0;
      var fEls = root.querySelectorAll('[data-field-index]');
      for (var j = 0; j < fEls.length; j++) {
        var fi = parseInt(fEls[j].dataset.fieldIndex, 10);
        var f = fields[fi];
        if (!f) continue;
        var fv;
        if (f.enum) {
          var r = fEls[j].querySelector('input[type=radio]:checked');
          fv = r ? parseInt(r.value, 10) : 0;
        } else {
          fv = parseFloat(fEls[j].value);
          if (isNaN(fv)) fv = 0;
          fv = Math.round(fv - (f.offset || 0));
        }
        rawOut |= ((fv & ((1 << f.bits) - 1)) << f.lsb);
      }
      return String(rawOut);
    }
    if (root.classList.contains('ms-radios')) {
      var r = root.querySelector('input[type=radio]:checked');
      return r ? r.value : '';
    }
    if (root.classList.contains('ms-checks')) {
      var mask = 0;
      var boxes = root.querySelectorAll('input[type=checkbox]');
      for (var i = 0; i < boxes.length; i++) {
        if (boxes[i].checked) {
          var b = parseInt(boxes[i].dataset.bit, 10);
          if (!isNaN(b)) mask += (1 << b);
        }
      }
      // Биты, не показанные чекбоксами (недокументированные), сохраняем как
      // есть, чтобы запись не затирала их.
      var reserved = parseInt(root.dataset.reserved, 10);
      if (isNaN(reserved)) reserved = 0;
      var rawOut = mask | reserved;
      var sc = parseFloat(root.dataset.scale);
      if (isNaN(sc)) sc = 1;
      var of = parseFloat(root.dataset.offset);
      if (isNaN(of)) of = 0;
      return String(Math.round((rawOut * sc + of) * 1000) / 1000);
    }
    return root.value;
  }

  // ---- Подсказки из документации ----
  var popoverEl = null;
  function hidePopover() {
    if (popoverEl && popoverEl.parentNode) popoverEl.parentNode.removeChild(popoverEl);
    popoverEl = null;
  }
  function showPopover(anchor, text) {
    hidePopover();
    var p = el('div', 'ms-popover');
    text.split('\n').forEach(function (line, i) {
      if (i > 0) p.appendChild(document.createElement('br'));
      p.appendChild(document.createTextNode(line));
    });
    document.body.appendChild(p);
    var r = anchor.getBoundingClientRect();
    var top = r.bottom + window.scrollY + 6;
    var left = r.left + window.scrollX;
    p.style.top = top + 'px';
    p.style.left = left + 'px';
    var pr = p.getBoundingClientRect();
    if (pr.right > window.innerWidth - 8) {
      p.style.left = Math.max(8, window.innerWidth - pr.width - 8) + 'px';
    }
    if (r.bottom + pr.height + 12 > window.innerHeight) {
      p.style.top = Math.max(8, r.top + window.scrollY - pr.height - 6) + 'px';
    }
    popoverEl = p;
  }
  function helpBtn(text, title, dbgid) {
    var b = el('button', 'ms-help', '?');
    b.type = 'button';
    b.setAttribute('aria-label', 'Подсказка');
    b.addEventListener('click', function (e) {
      e.preventDefault();
      e.stopPropagation();
      if (popoverEl && popoverEl._anchor === b) { hidePopover(); return; }
      showPopover(b, text || 'Нет описания в документации.');
      if (popoverEl) popoverEl._anchor = b;
    });
    return assignDbgId(b, dbgid);
  }
  function attachHelp(anchorEl, text) {
    if (!anchorEl || !anchorEl.parentNode) return;
    anchorEl.parentNode.insertBefore(helpBtn(text, null, anchorEl.id + ':help'), anchorEl.nextSibling);
  }
  document.addEventListener('click', function (e) {
    if (popoverEl && !popoverEl.contains(e.target) && !e.target.classList.contains('ms-help')) hidePopover();
  });
  document.addEventListener('keydown', function (e) { if (e.key === 'Escape') hidePopover(); });

  // ---- Отладка: нумерация элементов (#N) ----
  // Включается только в отладочной сборке (make build-debug): сервер отдаёт
  // debug=true в /api/config. При наведении сразу всплывает номер элемента.
  // В обычной сборке отключено.
  //
  // Номера детерминированы: сервер (dbgindex.go) строит фиксированный список
  // id → №, клиент подгружает его при чтении и фиксирует в localStorage.
  // Номера в UI совпадают с файлом-индексом .kilo/dbg-index.txt.
  var debugEnabled = false;
  var DBG_LS = 'mapSettings.dbgNums.v2';
  var debugReg = null;
  var dbgTip = null;

  function loadDebugReg() {
    var raw = lsGet(DBG_LS, '');
    if (raw) {
      try {
        var o = JSON.parse(raw);
        if (o && typeof o.next === 'number' && o.ids && typeof o.ids === 'object') return o;
      } catch (e) {}
    }
    return { next: 1, ids: {} };
  }
  function saveDebugReg() {
    try { lsSet(DBG_LS, JSON.stringify(debugReg)); } catch (e) {}
  }
  // seedDebugIndex загружает серверный индекс и проставляет номера по нему.
  function seedDebugIndex(mode) {
    if (!mode) return Promise.resolve();
    return api('/api/dbgindex?mode=' + encodeURIComponent(mode)).then(function (list) {
      if (!list || !list.length) return;
      if (!debugReg) debugReg = loadDebugReg();
      var max = 0;
      for (var i = 0; i < list.length; i++) {
        debugReg.ids[list[i].id] = list[i].n;
        if (list[i].n > max) max = list[i].n;
      }
      if (debugReg.next <= max) debugReg.next = max + 1;
      saveDebugReg();
    }).catch(function () {});
  }
  // assignDbgId — проставляет элементу идентификатор для отладочной нумерации.
  function assignDbgId(node, id) {
    if (node && id) node.setAttribute('data-dbgid', id);
    return node;
  }

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
    if (pr.right > window.innerWidth - 8) {
      p.style.left = Math.max(8, window.innerWidth - pr.width - 8) + 'px';
    }
    if (r.bottom + pr.height + 12 > window.innerHeight) {
      p.style.top = Math.max(8, r.top + window.scrollY - pr.height - 6) + 'px';
    }
    dbgTip = p;
  }
  function bindDbgHover(node) {
    node.addEventListener('mouseenter', function () {
      if (node.dataset.dbgNo) showDbgTip(node, node.dataset.dbgNo);
    });
    node.addEventListener('mouseleave', hideDbgTip);
    node.addEventListener('click', hideDbgTip);
  }
  // debugNumberAll выдаёт номера ещё не нумерованным элементам (по data-dbgid).
  // Уже известные идентификаторы сохраняют свой номер. Вызывается после
  // перерисовки; реестр живёт в localStorage между перезагрузками страницы.
  function debugNumberAll() {
    if (!debugEnabled) return;
    if (!debugReg) debugReg = loadDebugReg();
    var nodes = document.querySelectorAll('[data-dbgid]');
    var changed = false;
    for (var i = 0; i < nodes.length; i++) {
      var n = nodes[i];
      var id = n.dataset.dbgid;
      var no = debugReg.ids[id];
      if (no === undefined) {
        no = debugReg.next++;
        debugReg.ids[id] = no;
        changed = true;
      }
      n.dataset.dbgNo = String(no);
      if (!n.__dbgBound) { bindDbgHover(n); n.__dbgBound = true; }
    }
    if (changed) saveDebugReg();
  }

  // ---- Рендер ----
  function groupedTables(container, groups, editable, section) {
    container.innerHTML = '';
    if (!groups || !groups.length) {
      container.appendChild(el('div', 'missing', 'Нет данных'));
      return;
    }
    section = section || 'grp';
    groups.forEach(function (g) {
      var box = el('div', 'ms-group');
      box.appendChild(assignDbgId(el('h3', 'ms-group-title', g.name), section + ':' + g.name + ':title'));
      var wrap = el('div', 'ms-table-wrap');
      var tbl = el('table', 'ms-table');
      var cg = el('colgroup');
      ['c-name', 'c-addr', 'c-val', 'c-unit', 'c-range', 'c-act'].forEach(function (c) {
        var col = document.createElement('col');
        col.className = c;
        cg.appendChild(col);
      });
      tbl.appendChild(cg);
      var thead = el('thead');
      var hr = el('tr');
      ['Параметр', 'Ячейка', 'Значение', 'Ед.', 'Диапазон', 'Действия'].forEach(function (h) {
        hr.appendChild(assignDbgId(el('th', null, h), section + ':' + g.name + ':th:' + h));
      });
      thead.appendChild(hr);
      tbl.appendChild(thead);
      var tbody = el('tbody');
      g.params.forEach(function (p) {
        var tr = el('tr', 'ms-row');
        tr.dataset.key = p.key;
        var danger = !!p.danger;
        var locked = danger && p.writable && !unlockedKeys[p.key];
        tr.dataset.locked = locked ? '1' : '0';
        tr.dataset.danger = danger ? '1' : '0';
        var tdName = assignDbgId(el('td', 'ms-name'), p.key + ':name');
        if (p.danger) {
          tdName.appendChild(assignDbgId(el('span', 'ms-danger', '▲ Опасно!'), p.key + ':danger'));
        }
        tdName.appendChild(document.createTextNode(p.name));
        if (p.help) tdName.appendChild(helpBtn(p.help, p.name, p.key + ':help'));
        tr.appendChild(tdName);
        tr.appendChild(el('td', 'ms-addr', p.addr + (p.cell ? ' ' + p.cell : '')));
        var tdVal = el('td', 'ms-val');
        var canEdit = !!(editable && p.writable);
        var hasEnum = !!(p.options && p.options.length);
        var hasBits = !!(p.bits && p.bits.length);
        var hasFields = !!(p.fields && p.fields.length);
        var isEnum = canEdit && hasEnum;
        var isBits = canEdit && hasBits;
        var cur = (p.value === null || p.value === undefined) ? '' : String(p.value);
        var rawVal = (p.raw === null || p.raw === undefined) ? 0 : p.raw;

        // Радиогруппа (редактируемая или, для ro, неактивная).
        function makeRadios(edit) {
          var rg = assignDbgId(el('div', 'ms-radios' + (edit ? '' : ' ms-ro-group')), p.key + ':value');
          p.options.forEach(function (o) {
            var lab = assignDbgId(el('label', 'ms-radio'), p.key + ':opt:' + o.value);
            var r = document.createElement('input');
            r.type = 'radio';
            r.name = 'rad_' + p.key;
            r.value = String(o.value);
            if (String(o.value) === cur) r.checked = true;
            if (!edit) r.disabled = true;
            lab.appendChild(r);
            lab.appendChild(document.createTextNode(
              (o.label && o.label !== '') ? o.label : String(o.value)));
            rg.appendChild(lab);
          });
          if (edit) {
            rg.dataset.editKey = p.key;
            rg.dataset.editOrig = cur;
            rg.addEventListener('change', updateState);
            rg.addEventListener('input', updateState);
          }
          return rg;
        }
        // Чекбоксы битовой маски.
        function makeChecks(edit) {
          var cbox = assignDbgId(el('div', 'ms-checks' + (edit ? '' : ' ms-ro-group')), p.key + ':value');
          var bits = p.bits.slice().sort(function (a, b) { return a.bit - b.bit; });
          var bitsMask = 0;
          bits.forEach(function (bit) {
            var lab = assignDbgId(el('label', 'ms-radio'), p.key + ':bit:' + bit.bit);
            var cb = document.createElement('input');
            cb.type = 'checkbox';
            cb.dataset.bit = String(bit.bit);
            bitsMask |= (1 << bit.bit);
            if (rawVal & (1 << bit.bit)) cb.checked = true;
            if (!edit) cb.disabled = true;
            lab.appendChild(cb);
            lab.appendChild(document.createTextNode(bit.name || ('бит ' + bit.bit)));
            cbox.appendChild(lab);
          });
          if (edit) {
            cbox.dataset.editKey = p.key;
            cbox.dataset.editOrig = cur;
            cbox.dataset.scale = String(p.scale === undefined ? 1 : p.scale);
            cbox.dataset.offset = String(p.offset === undefined ? 0 : p.offset);
            cbox.dataset.bitsMask = String(bitsMask);
            cbox.dataset.reserved = String(rawVal & ~bitsMask);
            cbox.addEventListener('change', updateState);
            cbox.addEventListener('input', updateState);
          }
          return cbox;
        }
        // Несколько именованных битовых полей в одном байте.
        function makeFields(edit) {
          var box = assignDbgId(el('div', 'ms-fields' + (edit ? '' : ' ms-ro-group')), p.key + ':value');
          var mask = 0;
          p.fields.forEach(function (f, fi) {
            var fmask = (1 << f.bits) - 1;
            mask |= (fmask << f.lsb);
            var item = el('label', 'ms-field-item');
            item.appendChild(el('span', 'ms-field-label', f.label));
            var fv = (rawVal >> f.lsb) & fmask;
            var fid = p.key + ':field:' + fi;
            if (f.enum) {
              var rg = assignDbgId(el('div', 'ms-radios'), fid);
              Object.keys(f.enum).sort(function (a, b) { return parseInt(a, 10) - parseInt(b, 10); })
                .forEach(function (k) {
                  var l2 = assignDbgId(el('label', 'ms-radio'), fid + ':opt:' + k);
                  var r = document.createElement('input');
                  r.type = 'radio';
                  r.name = 'fld_' + p.key + '_' + fi;
                  r.value = k;
                  if (String(fv) === String(k)) r.checked = true;
                  if (!edit) r.disabled = true;
                  l2.appendChild(r);
                  l2.appendChild(document.createTextNode(f.enum[k]));
                  rg.appendChild(l2);
                });
              rg.dataset.fieldIndex = String(fi);
              if (edit) {
                rg.addEventListener('change', updateState);
                rg.addEventListener('input', updateState);
              }
              item.appendChild(rg);
            } else {
              var inp = document.createElement('input');
              inp.type = 'number';
              inp.step = 'any';
              inp.className = 'ms-input ms-field-input';
              inp.value = String(fv + (f.offset || 0));
              inp.dataset.fieldIndex = String(fi);
              if (!edit) inp.disabled = true;
              if (edit) {
                inp.addEventListener('input', updateState);
                inp.addEventListener('change', updateState);
              }
              item.appendChild(inp);
            }
            box.appendChild(item);
          });
          box.dataset.fields = JSON.stringify(p.fields);
          box.dataset.fieldsMask = String(mask);
          if (edit) {
            box.dataset.editKey = p.key;
            box.dataset.editOrig = String(rawVal);
            box.dataset.reserved = String(rawVal & ~mask);
          }
          return box;
        }

        if (p.error) {
          tdVal.appendChild(el('span', 'ms-err', p.error));
        } else if (p.format === 'ver') {
          var ver = fmtVer(rawVal);
          if (canEdit) {
            var vinp = assignDbgId(document.createElement('input'), p.key + ':value');
            vinp.type = 'text';
            vinp.className = 'ms-input ms-ver';
            vinp.value = ver;
            vinp.dataset.editKey = p.key;
            vinp.dataset.verFormat = '1';
            vinp.dataset.editOrig = String(rawVal);
            vinp.addEventListener('input', updateState);
            vinp.addEventListener('change', updateState);
            tdVal.appendChild(vinp);
          } else {
            tdVal.appendChild(assignDbgId(el('span', 'ms-ro', ver), p.key + ':value'));
          }
          if (p.text) tdVal.appendChild(el('span', 'ms-text', p.text));
        } else if (hasFields) {
          tdVal.appendChild(makeFields(canEdit));
        } else if (canEdit && !isEnum && !isBits) {
          var inp = document.createElement('input');
          inp.type = 'number';
          inp.step = 'any';
          inp.className = 'ms-input';
          inp.value = cur;
          inp.dataset.editKey = p.key;
          inp.dataset.editOrig = cur;
          assignDbgId(inp, p.key + ':value');
          inp.addEventListener('input', updateState);
          inp.addEventListener('change', updateState);
          tdVal.appendChild(inp);
          if (p.text) tdVal.appendChild(el('span', 'ms-text', p.text));
        } else if (isEnum) {
          tdVal.appendChild(makeRadios(true));
        } else if (isBits) {
          tdVal.appendChild(makeChecks(true));
        } else if (hasEnum) {
          tdVal.appendChild(makeRadios(false));
        } else if (hasBits) {
          tdVal.appendChild(makeChecks(false));
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

        // Колонка «Действия»: перечитать/сохранить только этот параметр и
        // (для опасных) разблокировать редактирование на время сессии.
        var root = tdVal.querySelector('[data-edit-key]');
        var tdAct = el('td', 'ms-act');
        var readB = assignDbgId(miniBtn('Читать'), p.key + ':read');
        var saveB = assignDbgId(miniBtn('Сохр.'), p.key + ':save');
        saveB.disabled = true;
        tdAct.appendChild(readB);
        tdAct.appendChild(saveB);
        var lockB = null;
        if (danger && p.writable) {
          lockB = assignDbgId(miniBtn(locked ? '🔒' : '🔓'), p.key + ':lock');
          lockB.className += ' ms-lock';
          lockB.setAttribute('aria-label', locked ? 'Разблокировать параметр' : 'Заблокировать параметр');
          lockB.setAttribute('aria-pressed', locked ? 'false' : 'true');
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
          if (lockB) {
            lockB.textContent = l ? '🔒' : '🔓';
            lockB.setAttribute('aria-label', l ? 'Разблокировать параметр' : 'Заблокировать параметр');
            lockB.setAttribute('aria-pressed', l ? 'false' : 'true');
          }
          updateState();
        }
        function applyRead(v) {
          var rawV = (v.raw === null || v.raw === undefined) ? 0 : v.raw;
          var fieldsBox = (root && root.classList.contains('ms-fields'))
            ? root : tdVal.querySelector('.ms-fields');
          if (fieldsBox) {
            var fields = [];
            try { fields = JSON.parse(fieldsBox.dataset.fields || '[]'); } catch (e) {}
            var fmaskAll = parseInt(fieldsBox.dataset.fieldsMask, 10) || 0;
            if (fieldsBox.dataset.editKey) {
              fieldsBox.dataset.reserved = String(rawV & ~fmaskAll);
              fieldsBox.dataset.editOrig = String(rawV);
            }
            fieldsBox.querySelectorAll('[data-field-index]').forEach(function (el2) {
              var fi = parseInt(el2.dataset.fieldIndex, 10);
              var f = fields[fi];
              if (!f) return;
              var fv = (rawV >> f.lsb) & ((1 << f.bits) - 1);
              if (f.enum) {
                el2.querySelectorAll('input[type=radio]').forEach(function (r) {
                  r.checked = (r.value === String(fv));
                });
              } else {
                el2.value = String(fv + (f.offset || 0));
              }
            });
            var t1 = tdVal.querySelector('.ms-text');
            if (t1) t1.textContent = v.text || '';
            debugNumberAll();
            updateState();
            return;
          }
          if (root && root.dataset.verFormat) {
            root.value = fmtVer(rawV);
            root.dataset.editOrig = String(rawV);
            var t2 = tdVal.querySelector('.ms-text');
            if (t2) t2.textContent = v.text || '';
            updateState();
            return;
          }
          if (root && root.classList.contains('ms-radios')) {
            root.querySelectorAll('input[type=radio]').forEach(function (r) {
              r.checked = (r.value === String(v.value));
            });
            root.dataset.editOrig = String(v.value);
          } else if (root && root.classList.contains('ms-checks')) {
            var rv = (v.raw === null || v.raw === undefined) ? 0 : v.raw;
            var bm = parseInt(root.dataset.bitsMask, 10);
            if (isNaN(bm)) bm = 0;
            root.querySelectorAll('input[type=checkbox]').forEach(function (cb) {
              cb.checked = !!(rv & (1 << parseInt(cb.dataset.bit, 10)));
            });
            root.dataset.reserved = String(rv & ~bm);
            root.dataset.editOrig = String(v.value);
          } else if (root) {
            root.value = fmt(v.value);
            root.dataset.editOrig = root.value;
          } else {
            var ro = tdVal.querySelector('.ms-ro');
            if (!ro) {
              var errEl = tdVal.querySelector('.ms-err');
              if (errEl) {
                ro = assignDbgId(el('span', 'ms-ro'), p.key + ':value');
                errEl.parentNode.replaceChild(ro, errEl);
              }
            }
            if (ro) ro.textContent = fmt(v.value);
          }
          var txt = tdVal.querySelector('.ms-text');
          if (txt) txt.textContent = v.text || '';
          else if (!root && v.text) tdVal.appendChild(el('span', 'ms-text', v.text));
          debugNumberAll();
          updateState();
        }
        function readRow(silent) {
          readB.disabled = true;
          var t = target();
          var q = '?' + modeQueryPart() + 'ip=' + encodeURIComponent(t.ip) +
            '&port=' + t.port + '&key=' + encodeURIComponent(p.key);
          api('/api/param' + q).then(function (v) {
            if (v.error) {
              setStatus(uiStatus(), 'Ошибка чтения «' + p.name + '»: ' + v.error, 'err');
              return;
            }
            // Строка без редактора (была ошибка чтения), а параметр записываемый
            // — перерисовываем таблицу, чтобы появился редактор.
            if (!root && editable && p.writable) { readAll(); return; }
            applyRead(v);
            if (!silent) setStatus(uiStatus(), 'Прочитано: ' + p.name, 'ok');
          }).catch(function (e) {
            setStatus(uiStatus(), 'Ошибка чтения «' + p.name + '»: ' + e.message, 'err');
          }).finally(function () { readB.disabled = false; });
        }
        function saveRow() {
          if (!root) return;
          var val = readEditValue(root);
          var f = parseFloat(val);
          if (val === '' || isNaN(f)) {
            setStatus(uiStatus(), 'Некорректное значение «' + p.name + '»', 'err');
            return;
          }
          if (danger && !window.confirm('Сохранить опасный параметр «' + p.name + '»?')) return;
          saveB.disabled = true;
          var t = target();
          var ch = {};
          ch[p.key] = f;
          api('/api/apply', {
            method: 'POST',
            body: {
              mode: reqMode(), ip: t.ip, port: t.port,
              changes: ch, allowDanger: danger
            }
          }).then(function (resp) {
            var results = resp.results || {};
            if (results[p.key]) {
              setStatus(uiStatus(), 'Не сохранено: ' + results[p.key], 'warn');
              updateState();
              return;
            }
            readRow(true);
            setStatus(uiStatus(), 'Сохранено: ' + p.name, 'ok');
          }).catch(function (e) {
            setStatus(uiStatus(), 'Ошибка записи «' + p.name + '»: ' + e.message, 'err');
            updateState();
          });
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
        tr.appendChild(tdAct);
        if (root && locked) setControlsEnabled(root, false);
        updateState();
        tbody.appendChild(tr);
      });
      tbl.appendChild(tbody);
      wrap.appendChild(tbl);
      box.appendChild(wrap);
      container.appendChild(box);
    });
  }

  // renderActionsInto отрисовывает органы управления в контейнер.
  function renderActionsInto(container, actions) {
    container.innerHTML = '';
    var list = el('div', 'ms-actions');
    (actions || []).forEach(function (a) {
      var wrap = el('span', 'ms-action-wrap');
      var b = assignDbgId(el('button', 'ms-action-btn', a.name), 'action:' + a.key + ':btn');
      b.type = 'button';
      if (a.confirm) b.className += ' danger';
      b.addEventListener('click', function () {
        if (a.confirm && !window.confirm('Выполнить «' + a.name + '»?\n\n' + (a.desc || ''))) return;
        setStatus(uiStatus(), 'Выполняется: ' + a.name + '…');
        httpAction(a.key);
      });
      wrap.appendChild(b);
      wrap.appendChild(helpBtn(a.desc || a.name, a.name, 'action:' + a.key + ':help'));
      list.appendChild(wrap);
    });
    container.appendChild(list);
  }

  // ---- Экраны (внутренняя навигация) ----
  // Группы: одноимённые блоки «Настройки» (rw) и «Мониторинг» (ro) объединяются
  // на одну страницу.
  function allGroups() {
    var order = [];
    var map = {};
    function add(groups, isSettings) {
      (groups || []).forEach(function (g) {
        if (!map[g.name]) {
          map[g.name] = { name: g.name, settings: [], monitor: [] };
          order.push(g.name);
        }
        (isSettings ? map[g.name].settings : map[g.name].monitor).push.apply(
          isSettings ? map[g.name].settings : map[g.name].monitor, g.params);
      });
    }
    if (snapshot) {
      add(snapshot.settings, true);
      add(snapshot.monitor, false);
    }
    return order.map(function (n) { return map[n]; });
  }

  function findGroup(name) {
    var found = null;
    allGroups().forEach(function (g) { if (g.name === name) found = g; });
    return found;
  }

  // buildNavList — кнопки перехода по разделам; current подсвечивается.
  function buildNavList(container, current) {
    container.innerHTML = '';
    if (!snapshot) {
      container.appendChild(el('div', 'missing', 'Нажмите «Перечитать», чтобы загрузить разделы.'));
      return;
    }
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

  function showHome() {
    homeEl.hidden = false;
    sectionEl.hidden = true;
    buildNavList(homeNavEl, null);
    debugNumberAll();
  }

  function showGroup(name) {
    homeEl.hidden = true;
    sectionEl.hidden = false;
    setStatus(secStatusEl, '', '');
    secTitleEl.textContent = name;
    buildNavList(secNavEl, name);
    secBodyEl.innerHTML = '';
    var g = findGroup(name);
    if (!snapshot || !g || (!g.settings.length && !g.monitor.length)) {
      secBodyEl.appendChild(el('div', 'missing', snapshot ? 'Нет данных' : 'Нажмите «Перечитать».'));
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
    homeEl.hidden = true;
    sectionEl.hidden = false;
    setStatus(secStatusEl, '', '');
    secTitleEl.textContent = 'Управляющие воздействия';
    buildNavList(secNavEl, '__actions__');
    if (!snapshot) {
      secBodyEl.innerHTML = '';
      secBodyEl.appendChild(el('div', 'missing', 'Нажмите «Перечитать».'));
      debugNumberAll();
      return;
    }
    renderActionsInto(secBodyEl, snapshot.actions);
    debugNumberAll();
  }

  function renderRoute() {
    var h = location.hash || '#home';
    if (h.indexOf('#group/') === 0) {
      showGroup(decodeURIComponent(h.slice(7)));
    } else if (h === '#actions') {
      showActions();
    } else {
      showHome();
    }
  }
  function goHome() { location.hash = '#home'; }
  function goGroup(name) { location.hash = '#group/' + encodeURIComponent(name); }
  function goActions() { location.hash = '#actions'; }

  // ---- Действия ----
  function readAll() {
    persist();
    setStatus(uiStatus(), 'Чтение параметров МАП…');
    readBtn.disabled = true;
    secReadBtn.disabled = true;
    var t = target();
    // Пока тип не определён — не передаём mode: сервер определит его сам.
    var qs = '?ip=' + encodeURIComponent(t.ip) + '&port=' + t.port;
    if (modeDetected) qs = '?mode=' + encodeURIComponent(t.mode) + '&' + qs.slice(1);
    api('/api/settings' + qs).then(function (snap) {
      if (!modeDetected && snap.mode) {
        modeEl.value = snap.mode;
        modeDetected = true;
      }
      snapshot = snap;
      var errs = (snap.errors && snap.errors.length) ? ' Ошибки чтения: ' + snap.errors.join('; ') : '';
      var st = 'Прочитано ' + (snap.read_at || '') + ' · ' + t.ip + ':' + t.port +
        ' · ' + (modeEl.value === 'titanator' ? 'Титанатор' : 'Доминатор') + errs;
      var finish = function () {
        renderRoute();
        setStatus(uiStatus(), st, errs ? 'warn' : 'ok');
      };
      if (debugEnabled) seedDebugIndex(modeEl.value).then(finish, finish);
      else finish();
    }).catch(function (e) {
      setStatus(uiStatus(), 'Ошибка чтения: ' + e.message, 'err');
    }).finally(function () { readBtn.disabled = false; secReadBtn.disabled = false; });
  }

  function collectChanges(container) {
    var changes = {};
    var hasDanger = false;
    var roots = (container || document).querySelectorAll('[data-edit-key]');
    for (var i = 0; i < roots.length; i++) {
      var root = roots[i];
      // Заблокированный (опасный) параметр в общую запись не попадает.
      var row = root.closest('tr');
      if (row && row.dataset.locked === '1') continue;
      var v = readEditValue(root);
      if (v === '') continue;
      if (v === root.dataset.editOrig) continue;
      var f = parseFloat(v);
      if (isNaN(f)) continue;
      changes[root.dataset.editKey] = f;
      if (row && row.dataset.danger === '1') hasDanger = true;
    }
    return { changes: changes, hasDanger: hasDanger };
  }

  function writeChanged() {
    persist();
    var collected = collectChanges(secBodyEl);
    var changes = collected.changes;
    var keys = Object.keys(changes);
    if (!keys.length) { setStatus(uiStatus(), 'Нет изменённых параметров.', 'warn'); return; }
    if (!window.confirm('Записать ' + keys.length + ' изменённ(ый/ых) параметр(ов) в МАП?')) return;
    secWriteBtn.disabled = true;
    setStatus(uiStatus(), 'Запись ' + keys.length + ' параметр(ов)…');
    var t = target();
    api('/api/apply', {
      method: 'POST',
      body: {
        mode: reqMode(), ip: t.ip, port: t.port,
        changes: changes, allowDanger: collected.hasDanger
      }
    }).then(function (resp) {
      var bad = [];
      var results = resp.results || {};
      Object.keys(results).forEach(function (k) { bad.push(k + ': ' + results[k]); });
      if (resp.error) bad.push(resp.error);
      if (bad.length) setStatus(uiStatus(), 'Записано с замечаниями: ' + bad.join('; '), 'warn');
      else setStatus(uiStatus(), 'Записано и проверено: ' + keys.length + ' параметр(ов).', 'ok');
    }).catch(function (e) {
      setStatus(uiStatus(), 'Ошибка записи: ' + e.message, 'err');
    }).finally(function () {
      secWriteBtn.disabled = false;
      readAll();
    });
  }

  function httpAction(key) {
    var t = target();
    api('/api/action', {
      method: 'POST',
      body: { mode: reqMode(), ip: t.ip, port: t.port, key: key }
    }).then(function () {
      setStatus(uiStatus(), 'Действие выполнено.', 'ok');
    }).catch(function (e) {
      setStatus(uiStatus(), 'Ошибка действия: ' + e.message, 'err');
    });
  }

  // ---- Время ----
  function readTime() {
    persist();
    setStatus(timeStatusEl, 'Чтение времени МАП…');
    var t = target();
    var qs = '?' + modeQueryPart() + 'ip=' + encodeURIComponent(t.ip) + '&port=' + t.port;
    api('/api/time' + qs).then(function (st) {
      timeHEl.value = st.hour;
      timeMEl.value = st.minute;
      setStatus(timeStatusEl, 'Время МАП: ' + pad(st.hour) + ':' + pad(st.minute) + ' (' + (st.raw || '') + ')', 'ok');
    }).catch(function (e) {
      setStatus(timeStatusEl, 'Ошибка чтения времени: ' + e.message, 'err');
    });
  }
  function writeTime() {
    persist();
    var h = parseInt(timeHEl.value, 10);
    var m = parseInt(timeMEl.value, 10);
    if (isNaN(h) || isNaN(m) || h < 0 || h > 23 || m < 0 || m > 59) {
      setStatus(timeStatusEl, 'Некорректное время.', 'err');
      return;
    }
    if (!window.confirm('Записать время МАП ' + pad(h) + ':' + pad(m) + '?')) return;
    var t = target();
    setStatus(timeStatusEl, 'Запись времени…');
    api('/api/time', {
      method: 'POST',
      body: { mode: reqMode(), ip: t.ip, port: t.port, hour: h, minute: m }
    }).then(function () {
      setStatus(timeStatusEl, 'Время записано: ' + pad(h) + ':' + pad(m), 'ok');
    }).catch(function (e) {
      setStatus(timeStatusEl, 'Ошибка записи времени: ' + e.message, 'err');
    });
  }
  function pad(n) { return (n < 10 ? '0' : '') + n; }

  readBtn.addEventListener('click', readAll);
  secReadBtn.addEventListener('click', readAll);
  secWriteBtn.addEventListener('click', writeChanged);
  secHomeBtn.addEventListener('click', goHome);
  timeReadBtn.addEventListener('click', readTime);
  timeWriteBtn.addEventListener('click', writeTime);
  window.addEventListener('hashchange', renderRoute);

  // Подсказки для органов управления (из документации/описания модуля).
  attachHelp(modeEl, 'Тип МАП: Титанатор или Доминатор. Определяется автоматически при первом чтении по ячейке _DevOpt (0x007); можно переопределить вручную. В настройках не сохраняется.');
  attachHelp(ipEl, 'IP-адрес МАП / mapgateway (Modbus TCP). По умолчанию берётся из конфигурации; можно изменить и сохранить в браузере.');
  attachHelp(portEl, 'Порт Modbus TCP (обычно 502). Сохраняется в браузере.');
  attachHelp(readBtn, 'Перечитать: снять с МАП полный снимок всех параметров (настройки и мониторинг). Ничего не записывается.');
  attachHelp(secReadBtn, 'Перечитать: обновить снимок всех параметров с МАП. Ничего не записывается.');
  attachHelp(secWriteBtn, 'Записать: отправить на МАП только изменённые параметры этого раздела. Запись обрамляется служебными командами (03 → запись → 07).');
  attachHelp(secHomeBtn, 'На главную: вернуться к настройкам программы и списку разделов.');
  attachHelp(timeHEl, 'Часы текущего времени МАП (0–23).');
  attachHelp(timeMEl, 'Минуты текущего времени МАП (0–59).');
  attachHelp(timeReadBtn, 'Прочитать текущее время МАП из ячеек _LCD_TimeCyr (0x1B6) и _TimeCyr_MINUT (0x44B).');
  attachHelp(timeWriteBtn, 'Записать время МАП. Отдельная форма, не входит в общую таблицу настроек.');

  // Предзаполняем форму времени текущим локальным временем (затем можно прочитать с МАП).
  var now = new Date();
  timeHEl.value = now.getHours();
  timeMEl.value = now.getMinutes();
  footEl.textContent = 'Каталог ячеек: protocol_MAP_cells_2026_07_15.doc. Запись — со служебным обрамлением (03 → запись → 07).';

  renderRoute();
  maybeAutoRead();
})();
