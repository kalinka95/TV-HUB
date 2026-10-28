'use strict';
/* TVHub — интерфейс в стиле Google TV. Управление: пульт, клавиатура, геймпад, мышь. */

const $ = (id) => document.getElementById(id);
const TOKEN = new URLSearchParams(location.search).get('t') || '';

const api = {
  async get(p) {
    const r = await fetch(p, { headers: { 'X-Token': TOKEN } });
    if (!r.ok) throw new Error('HTTP ' + r.status);
    return r.json();
  },
  async post(p, body) {
    const r = await fetch(p, { method: 'POST', headers: { 'X-Token': TOKEN, 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    if (!r.ok) throw new Error(await r.text());
    return r.json();
  },
};

const S = {
  cfg: null, info: null,
  channels: [], groups: [], chLoading: true, chError: '',
  screen: 'main', tab: '',
  tvGroup: '__all', search: '',
  newAppType: 'web',
};

const ICONS = {
  gear: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="3.2"/><path d="M12 2.5v3M12 18.5v3M2.5 12h3M18.5 12h3M5.3 5.3l2.1 2.1M16.6 16.6l2.1 2.1M5.3 18.7l2.1-2.1M16.6 7.4l2.1-2.1"/></svg>',
  remote: '<svg viewBox="0 0 24 24"><rect x="7" y="2" width="10" height="20" rx="3"/><circle cx="12" cy="8" r="2"/><path d="M10 14h4M10 17.5h4"/></svg>',
  refresh: '<svg viewBox="0 0 24 24"><path d="M20 11a8 8 0 1 0-2.3 5.7"/><path d="M20 4v7h-7"/></svg>',
  minimize: '<svg viewBox="0 0 24 24"><rect x="3" y="4" width="18" height="13" rx="2"/><path d="M8 21h8M9 11h6"/></svg>',
  moon: '<svg viewBox="0 0 24 24"><path d="M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z"/></svg>',
  power: '<svg viewBox="0 0 24 24"><path d="M12 3v9"/><path d="M6.3 6.8a8 8 0 1 0 11.4 0"/></svg>',
  restart: '<svg viewBox="0 0 24 24"><path d="M4 12a8 8 0 1 0 2.3-5.7"/><path d="M4 4v5h5"/></svg>',
  exit: '<svg viewBox="0 0 24 24"><path d="M14 4h5v16h-5"/><path d="M10 8l-4 4 4 4"/><path d="M6 12h10"/></svg>',
  tv: '<svg viewBox="0 0 24 24"><rect x="3" y="6" width="18" height="13" rx="2"/><path d="M8 2l4 4 4-4"/></svg>',
  plus: '<svg viewBox="0 0 24 24"><path d="M12 5v14M5 12h14"/></svg>',
  phone: '<svg viewBox="0 0 24 24"><rect x="6" y="2" width="12" height="20" rx="2.5"/><path d="M11 18h2"/><circle cx="12" cy="10" r="2.6"/></svg>',
  theme: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="8.5"/><path d="M12 3.5a8.5 8.5 0 0 0 0 17z" fill="currentColor"/></svg>',
  sun: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="4.5"/><path d="M12 2v2.5M12 19.5V22M2 12h2.5M19.5 12H22M4.9 4.9l1.8 1.8M17.3 17.3l1.8 1.8M4.9 19.1l1.8-1.8M17.3 6.7l1.8-1.8"/></svg>',
  full: '<svg viewBox="0 0 24 24"><path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5"/></svg>',
};

// ---------- Утилиты ----------

const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
function initials(name) {
  const w = String(name || '?').replace(/[^\p{L}\p{N} ]/gu, ' ').trim().split(/\s+/);
  return ((w[0] || '?')[0] + (w[1] ? w[1][0] : (w[0] || '')[1] || '')).toUpperCase();
}
function hueColor(s, a = '66') {
  let h = 0;
  for (const c of String(s)) h = (h * 31 + c.charCodeAt(0)) >>> 0;
  const hue = h % 360;
  return `hsl(${hue} 70% 50% / 0.${a === '66' ? '45' : '9'})`;
}
function el(tag, cls, html) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (html != null) e.innerHTML = html;
  return e;
}
function uid() { return Math.random().toString(36).slice(2, 9); }
const isFav = (url) => S.cfg.favorites.includes(url);

let toastTimer;
function toast(msg, ms = 3000) {
  const t = $('toast');
  t.textContent = msg;
  t.classList.remove('hidden');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.add('hidden'), ms);
}

async function saveCfg() {
  try { await api.post('/api/config', S.cfg); }
  catch (e) { toast('Не удалось сохранить настройки: ' + e.message); }
}

function tickClock() {
  const d = new Date();
  const s = d.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' });
  document.querySelectorAll('[data-clock]').forEach((c) => (c.textContent = s));
}

// ---------- Фокус и навигация ----------

let cur = null;

function scopeEl() {
  if (!$('modal').classList.contains('hidden')) return $('modal');
  if (S.screen === 'player') return guideOpen() ? $('guide') : ($('zap').classList.contains('hidden') ? null : $('zap'));
  return $(S.screen);
}
function focusables(scope) {
  return [...scope.querySelectorAll('.f')].filter((e) => e.offsetParent !== null && !e.disabled);
}

function ensureVisible(node) {
  let p = node.parentElement;
  while (p && p !== document.body) {
    const scrollY = p.scrollHeight > p.clientHeight + 1;
    const scrollX = p.scrollWidth > p.clientWidth + 1;
    if (scrollY || scrollX) {
      const er = node.getBoundingClientRect();
      const pr = p.getBoundingClientRect();
      if (scrollY) {
        if (p.classList.contains('rows')) {
          // Как в Google TV: ряд с фокусом поднимается наверх
          const row = node.closest('.row');
          if (row) p.scrollTop = row.offsetTop - parseFloat(getComputedStyle(p).paddingTop);
        } else {
          const pad = Math.min(pr.height * 0.18, 120);
          if (er.top < pr.top + pad) p.scrollTop -= pr.top + pad - er.top;
          else if (er.bottom > pr.bottom - pad) p.scrollTop += er.bottom - (pr.bottom - pad);
        }
      }
      if (scrollX) {
        const pad = Math.min(pr.width * 0.08, 90);
        if (er.left < pr.left + pad) p.scrollLeft -= pr.left + pad - er.left;
        else if (er.right > pr.right - pad) p.scrollLeft += er.right - (pr.right - pad);
      }
    }
    p = p.parentElement;
  }
}

function setFocus(node, { scroll = true } = {}) {
  if (cur === node) return;
  document.querySelectorAll('.f.focus').forEach((x) => x.classList.remove('focus'));
  cur = node;
  if (!node) return;
  node.classList.add('focus');
  if (node.tagName === 'INPUT') node.focus({ preventScroll: true });
  else if (document.activeElement && document.activeElement.tagName === 'INPUT') document.activeElement.blur();
  if (scroll) ensureVisible(node);
  if (node._onfocus) node._onfocus();
  if (node._info) setHero(node._info);
}

function overlap(a1, a2, b1, b2) { return Math.max(0, Math.max(a1, b1) - Math.min(a2, b2)); }

function move(dir) {
  const scope = scopeEl();
  if (!scope) return false;
  const list = focusables(scope);
  if (!list.length) return false;
  if (!cur || !scope.contains(cur) || cur.offsetParent === null) { setFocus(list[0]); return true; }
  // Из вкладок вниз — на первый элемент открытой вкладки
  if (dir === 'down' && S.screen === 'main' && cur.closest('.topbar')) {
    if (cur.dataset.tab && cur.dataset.tab !== S.tab) switchTab(cur.dataset.tab);
    const panel = $('panel-' + S.tab);
    const first = panel && (panel.querySelector('.grp.active') || focusables(panel)[0]);
    if (first) { setFocus(first); return true; }
  }
  const r = cur.getBoundingClientRect();
  let best = null, bs = Infinity;
  for (const e of list) {
    if (e === cur) continue;
    const c = e.getBoundingClientRect();
    let primary, orth;
    if (dir === 'right') { if (c.left < r.right - 4) continue; primary = c.left - r.right; orth = overlap(r.top, r.bottom, c.top, c.bottom); }
    else if (dir === 'left') { if (c.right > r.left + 4) continue; primary = r.left - c.right; orth = overlap(r.top, r.bottom, c.top, c.bottom); }
    else if (dir === 'down') { if (c.top < r.bottom - 4) continue; primary = c.top - r.bottom; orth = overlap(r.left, r.right, c.left, c.right); }
    else { if (c.bottom > r.top + 4) continue; primary = r.top - c.bottom; orth = overlap(r.left, r.right, c.left, c.right); }
    const s = Math.max(0, primary) + orth * 3;
    if (s < bs) { bs = s; best = e; }
  }
  if (!best) return false;
  // При входе в меню вкладок/категорий фокус встаёт на выбранный пункт
  const sticky = best.closest('[data-sticky]');
  if (sticky && !sticky.contains(cur)) {
    const sel = sticky.querySelector('.f.sel, .f.active');
    if (sel) best = sel;
  }
  setFocus(best);
  return true;
}

function focusByKey(k, scope = scopeEl()) {
  if (!k || !scope) return false;
  const e = [...scope.querySelectorAll('.f')].find((x) => x.dataset.k === k && x.offsetParent !== null);
  if (e) { cur = null; setFocus(e); return true; }
  return false;
}
function focusFirst(scope = scopeEl(), sel = '.f') {
  const e = scope && [...scope.querySelectorAll(sel)].find((x) => x.offsetParent !== null);
  if (e) { cur = null; setFocus(e); }
}
// Перерисовать и вернуть фокус на тот же элемент
function keepFocus(fn) {
  const k = cur && cur.dataset.k;
  fn();
  if (!focusByKey(k)) {
    const tab = $('tabs').querySelector('.tab.sel');
    cur = null;
    if (S.screen === 'main' && tab) setFocus(tab); else focusFirst();
  }
}

// ---------- Большая карточка и фон ----------

function setHero(info) {
  if (S.screen !== 'main') return;
  $('heroKicker').textContent = info.kicker || '';
  $('heroTitle').textContent = info.title || '';
  $('heroSub').textContent = info.sub || '';
  document.documentElement.style.setProperty('--hero-color', info.color || '#3b5bdb66');
  const img = $('backdropImg');
  const showArt = S.tab === 'home';
  img.classList.remove('letter');
  img.style.backgroundImage = '';
  img.textContent = '';
  if (!showArt) return;
  if (info.logo) img.style.backgroundImage = `url("${info.logo.replace(/"/g, '%22')}")`;
  else if (info.letter) {
    img.classList.add('letter');
    img.classList.toggle('long', info.letter.length > 3);
    img.textContent = info.letter;
  }
}

// ---------- Карточки ----------

function logoBox(ch, cls = 'logo') {
  const box = el('div', cls);
  if (ch.logo) {
    const img = new Image();
    img.loading = 'lazy';
    img.referrerPolicy = 'no-referrer';
    img.src = ch.logo;
    img.onerror = () => { box.innerHTML = `<span class="initials">${esc(initials(ch.name))}</span>`; };
    box.append(img);
  } else box.innerHTML = `<span class="initials">${esc(initials(ch.name))}</span>`;
  return box;
}

function channelCard(ch, list, key, label) {
  const b = el('button', 'f card');
  b.dataset.k = key + ':' + ch.n;
  b.append(logoBox(ch));
  b.append(el('div', 'num', String(ch.n)));
  if (isFav(ch.url)) b.append(el('div', 'fav', '★'));
  b.append(el('div', 'label', esc(ch.name)));
  if (S.cfg.lastChannel === ch.url && label === 'recent') b.classList.add('now');
  b._act = () => playChannel(ch, list);
  b._menu = () => toggleFav(ch);
  b._info = {
    kicker: 'Телеканал · ' + (ch.group || ''),
    title: ch.name,
    sub: `№ ${ch.n} · OK — смотреть · Меню — ${isFav(ch.url) ? 'убрать из избранного' : 'добавить в избранное'}`,
    color: hueColor(ch.name), logo: ch.logo, letter: initials(ch.name),
  };
  epgDecorate(b, ch);
  return b;
}

function appCard(app, key = 'app') {
  const b = el('button', 'f card app-card');
  b.dataset.k = key + ':' + app.id;
  b.style.background = `linear-gradient(135deg, ${app.color || '#2a3142'}, color-mix(in srgb, ${app.color || '#2a3142'} 55%, #000))`;
  b.innerHTML = `<div class="app-name">${esc(app.name)}</div><div class="app-type">${app.type === 'exe' ? 'программа' : 'онлайн'}</div>`;
  b._act = () => launch(app);
  b._menu = () => appMenu(app);
  b._info = {
    kicker: 'Приложение',
    title: app.name,
    sub: app.type === 'exe'
      ? 'Запустится программа на компьютере. Меню — переместить или удалить.'
      : 'Откроется на весь экран. Вернуться в TVHub — кнопка «Домой» на пульте. Меню — переместить или удалить.',
    color: (app.color || '#3b5bdb') + '88', letter: app.name,
  };
  return b;
}

function tile(icon, label, act, key, opts = {}) {
  const b = el('button', 'f card tile' + (opts.cls ? ' ' + opts.cls : ''));
  b.dataset.k = 'tile:' + key;
  b.innerHTML = ICONS[icon] + `<div class="label">${esc(label)}</div>`;
  b._act = act;
  b._info = { kicker: opts.kicker || 'Система', title: label, sub: opts.sub || '', color: opts.color || '#3b5bdb55' };
  return b;
}

function row(title, items, emptyText) {
  const r = el('div', 'row');
  r.append(el('div', 'row-title', esc(title)));
  const t = el('div', 'row-track');
  if (!items.length && emptyText) t.append(el('div', 'empty-note', esc(emptyText)));
  items.forEach((i) => t.append(i));
  r.append(t);
  return r;
}

// ---------- Вкладки ----------

function switchTab(name) {
  if (S.tab === name && !$('panel-' + name).classList.contains('hidden')) return;
  S.tab = name;
  document.querySelectorAll('#tabs .tab').forEach((t) => t.classList.toggle('sel', t.dataset.tab === name));
  ['home', 'tv', 'apps', 'music', 'settings'].forEach((p) => $('panel-' + p).classList.toggle('hidden', p !== name));
  renderPanel(name);
  const titles = { home: null, tv: 'Телеканалы', apps: 'Приложения', music: 'Музыка', settings: 'Настройки' };
  if (titles[name]) setHero({ title: titles[name], color: name === 'music' ? 'transparent' : '#3b5bdb44' });
  musicBackdrop(name === 'music');
}

function renderPanel(name) {
  if (name === 'home') renderHome();
  else if (name === 'tv') renderTV();
  else if (name === 'apps') renderApps();
  else if (name === 'music') renderMusic();
  else if (name === 'settings') renderSettings();
}

function setupTabs() {
  const tabs = $('tabs');
  tabs.closest('.topbar').dataset.sticky = '1';
  let tabTimer;
  tabs.querySelectorAll('.tab').forEach((t) => {
    // Вкладка открывается, если на ней задержаться: быстро «пролистать» вкладки можно без лишних переключений
    t._onfocus = () => { clearTimeout(tabTimer); tabTimer = setTimeout(() => { if (cur === t) switchTab(t.dataset.tab); }, 260); };
    t._act = () => { switchTab(t.dataset.tab); move('down'); };
    t._info = null;
  });
  const prof = $('profileBtn');
  prof._act = () => { switchTab('settings'); focusFirst($('panel-settings')); };
  prof._info = { title: 'Настройки', kicker: 'TVHub', sub: 'Плейлисты, приложения, автозапуск' };
}

// ---------- Главная ----------

function renderHome() {
  const box = $('homeRows');
  box.innerHTML = '';
  homeNoticeRow(box);
  const chs = S.channels;

  // Избранное и недавнее
  const recent = [];
  const last = chs.find((c) => c.url === S.cfg.lastChannel);
  if (last) recent.push(last);
  for (const u of S.cfg.favorites) {
    const c = chs.find((x) => x.url === u);
    if (c && c !== last) recent.push(c);
  }
  if (recent.length) {
    const favList = chs.filter((c) => isFav(c.url));
    box.append(row('Продолжить и избранное', recent.map((c) => channelCard(c, isFav(c.url) && favList.length > 1 ? favList : chs, 'rec', c === last ? 'recent' : ''))));
  }

  // Каналы
  const items = [];
  if (S.chLoading) items.push(el('div', 'empty-note', 'Загружаю список каналов…'));
  else if (S.chError) items.push(el('div', 'empty-note', 'Не удалось загрузить каналы: ' + esc(S.chError)));
  else chs.slice(0, 18).forEach((c) => items.push(channelCard(c, chs, 'top')));
  if (!S.chLoading && chs.length) {
    items.push(tile('tv', `Все каналы (${chs.length})`, () => { focusTab('tv'); }, 'allch', { kicker: 'Телеканалы', sub: 'Список по категориям и поиск' }));
  }
  if (!S.chLoading && !chs.length) {
    items.push(tile('plus', 'Добавить плейлист', () => openSettingsAt('pl-name'), 'addpl', { kicker: 'Телеканалы', sub: 'Вставьте ссылку на M3U-плейлист от вашего IPTV-провайдера' }));
  }
  box.append(row('Телеканалы', items));

  // Приложения
  const apps = S.cfg.apps.map((a) => appCard(a, 'happ'));
  apps.push(tile('plus', 'Добавить приложение', () => openSettingsAt('app-name'), 'addapp', { kicker: 'Приложения', sub: 'Любой сайт или программа на компьютере' }));
  box.append(row('Кино и сервисы', apps));

  // Категории
  if (S.groups.length > 1) {
    box.append(row('Категории', S.groups.slice(0, 30).map((g) => {
      const b = el('button', 'f card group-card');
      b.dataset.k = 'grp:' + g.name;
      b.innerHTML = `<div class="label">${esc(g.name)}</div><div class="count">${g.count} каналов</div>`;
      b._act = () => { S.tvGroup = g.name; S.search = ''; focusTab('tv', true); };
      b._info = { kicker: 'Категория', title: g.name, sub: `${g.count} каналов`, color: hueColor(g.name) };
      return b;
    })));
  }

  // Система
  box.append(row('Система', [
    tile('gear', 'Настройки', () => focusTab('settings'), 'set', { sub: 'Плейлисты, приложения, автозапуск' }),
    tile('phone', 'Пульт на телефоне', showRemoteQR, 'phone', { sub: 'Отсканируйте QR-код камерой телефона — и он станет пультом' }),
    tile('sun', 'Яркость', showBrightness, 'bright', { sub: 'Ярче или темнее экран телевизора. Удобно и с пульта на телефоне' }),
    tile('theme', 'Тёмная тема', showThemeModal, 'theme', { sub: 'Тёмное оформление Windows, всех программ и сайтов' }),
    tile('remote', 'Проверка пульта', () => show('keytest'), 'kt', { sub: 'Покажет, какие сигналы отправляют кнопки пульта' }),
    tile('refresh', 'Обновить каналы', () => loadChannels(true), 'reload', { sub: 'Заново скачать плейлист' }),
    tile('full', 'Полный экран', () => api.post('/api/system', { action: 'fullscreen' }), 'fs', { sub: 'Включить или выключить (F11)' }),
    tile('minimize', 'Свернуть', () => api.post('/api/system', { action: 'minimize' }), 'min', { sub: 'Вернуться к рабочему столу Windows' }),
    tile('moon', 'Спящий режим', () => confirmSys('sleep', 'Перевести компьютер в спящий режим?'), 'sleep', {}),
    tile('restart', 'Перезагрузка', () => confirmSys('restart', 'Перезагрузить компьютер?'), 'restart', {}),
    tile('power', 'Выключить', () => confirmSys('shutdown', 'Выключить компьютер?'), 'off', { cls: 'danger' }),
    tile('exit', 'Закрыть TVHub', () => confirmSys('exit', 'Закрыть TVHub?'), 'exit', {}),
  ]));

  if (!recent.length && !cur) setWelcomeHero();
}

function focusTab(name, focusContent = true) {
  switchTab(name);
  if (focusContent) {
    const p = $('panel-' + name);
    if (name === 'tv') { const a = p.querySelector('.grp.active'); if (a) { cur = null; setFocus(a); return; } }
    focusFirst(p);
  } else {
    focusByKey('tab-' + name, $('main'));
  }
}

// ---------- Телеканалы ----------

function filteredChannels() {
  let list = S.channels;
  if (S.tvGroup === '__fav') list = list.filter((c) => isFav(c.url));
  else if (S.tvGroup !== '__all') list = list.filter((c) => c.group === S.tvGroup);
  if (S.search) {
    const q = S.search.toLowerCase();
    list = list.filter((c) => c.name.toLowerCase().includes(q) || (c.orig || '').toLowerCase().includes(q) || String(c.n) === q);
  }
  return list;
}

function renderTV() {
  const g = $('groups');
  g.dataset.sticky = '1';
  g.innerHTML = '';
  const favCount = S.channels.filter((c) => isFav(c.url)).length;
  const groups = [{ id: '__all', name: 'Все каналы', count: S.channels.length }, { id: '__fav', name: '★ Избранное', count: favCount }]
    .concat(S.groups.map((x) => ({ id: x.name, name: x.name, count: x.count })));
  for (const gr of groups) {
    const b = el('button', 'f grp' + (gr.id === S.tvGroup ? ' active' : ''));
    b.dataset.k = 'g:' + gr.id;
    b.innerHTML = `<span>${esc(gr.name)}</span><small>${gr.count}</small>`;
    b._onfocus = () => {
      if (S.tvGroup === gr.id) return;
      S.tvGroup = gr.id;
      g.querySelectorAll('.grp').forEach((x) => x.classList.toggle('active', x === b));
      scheduleGrid();
    };
    b._act = () => move('right');
    g.append(b);
  }
  renderGrid();
}

let gridTimer;
function scheduleGrid() { clearTimeout(gridTimer); gridTimer = setTimeout(renderGrid, 120); }

function renderGrid() {
  const grid = $('chGrid');
  grid.innerHTML = '';
  $('chGridWrap').scrollTop = 0;
  const list = filteredChannels();
  $('search').innerHTML = S.search
    ? `Поиск: <b>${esc(S.search)}</b> · найдено ${list.length} · Backspace — стереть`
    : 'Наберите название канала на клавиатуре для поиска';
  if (S.chLoading) { grid.append(el('div', 'grid-more', 'Загружаю список каналов…')); return; }
  if (!list.length) {
    grid.append(el('div', 'grid-more', S.tvGroup === '__fav' ? 'В избранном пока пусто. Нажмите «Меню» на канале, чтобы добавить его.' : (S.chError ? 'Ошибка: ' + esc(S.chError) : 'Ничего не найдено')));
    return;
  }
  const max = 800;
  list.slice(0, max).forEach((c) => grid.append(channelCard(c, list, 'grid')));
  if (list.length > max) grid.append(el('div', 'grid-more', `Показаны первые ${max} из ${list.length}. Выберите категорию или воспользуйтесь поиском.`));
}

function computeGroups() {
  const m = new Map();
  for (const c of S.channels) m.set(c.group, (m.get(c.group) || 0) + 1);
  S.groups = [...m].map(([name, count]) => ({ name, count }));
}

async function loadChannels(force = false) {
  S.chLoading = true; S.chError = '';
  if (force) toast('Обновляю список каналов…');
  keepFocus(() => renderPanel(S.tab));
  try {
    const r = await api.get('/api/channels' + (force ? '?force=1' : ''));
    S.channels = r.channels || [];
    S.chError = r.error || '';
  } catch (e) {
    S.channels = []; S.chError = e.message;
  }
  S.chLoading = false;
  computeGroups();
  if (EPG.loaded) loadEPGNow();
  if (S.chError) toast('Каналы не загрузились: ' + S.chError, 5000);
  else if (force) toast(`Загружено каналов: ${S.channels.length}`);
  if (S.screen === 'main') keepFocus(() => renderPanel(S.tab));
}

function toggleFav(ch) {
  const f = S.cfg.favorites;
  const i = f.indexOf(ch.url);
  if (i >= 0) { f.splice(i, 1); toast(`«${ch.name}» убран из избранного`); }
  else { f.push(ch.url); toast(`«${ch.name}» добавлен в избранное`); }
  saveCfg();
  if (S.screen === 'main') keepFocus(() => renderPanel(S.tab));
  if (S.screen === 'player' && !$('zap').classList.contains('hidden')) keepFocus(renderZap);
}

// ---------- Приложения ----------

function renderApps() {
  const g = $('appsGrid');
  g.innerHTML = '';
  S.cfg.apps.forEach((a) => g.append(appCard(a, 'gapp')));
  const add = tile('plus', 'Добавить приложение', () => openSettingsAt('app-name'), 'gadd', { kicker: 'Приложения', sub: 'Любой сайт или программа на компьютере' });
  add.style.width = 'auto';
  g.append(add);
}

async function launch(app) {
  if (S.screen === 'player') stopPlayback();
  toast(`Открываю «${app.name}»…`, 2500);
  try {
    const r = await api.post('/api/launch', { id: app.id });
    if (!r.ok) toast('Не удалось открыть: ' + r.error, 5000);
  } catch (e) { toast('Ошибка: ' + e.message, 5000); }
}

function moveApp(app, d) {
  const a = S.cfg.apps;
  const i = a.indexOf(app);
  const j = i + d;
  if (i < 0 || j < 0 || j >= a.length) return;
  [a[i], a[j]] = [a[j], a[i]];
  saveCfg();
}

function appMenu(app) {
  const k = cur && cur.dataset.k;
  modal(`«${app.name}»`, [
    { label: '← Левее', act: () => { moveApp(app, -1); rerenderMain(k); } },
    { label: 'Правее →', act: () => { moveApp(app, 1); rerenderMain(k); } },
    { label: 'Удалить', cls: 'danger', act: () => {
      S.cfg.apps = S.cfg.apps.filter((x) => x !== app); saveCfg(); rerenderMain(null);
    } },
    { label: 'Отмена' },
  ]);
}

function rerenderMain(k) {
  renderPanel(S.tab);
  if (!focusByKey(k, $('main'))) focusFirst($('panel-' + S.tab));
}

// ---------- Настройки ----------

function btn(label, act, key, cls = '') {
  const b = el('button', 'f btn ' + cls, esc(label));
  b.dataset.k = key;
  b._act = act;
  return b;
}
function field(label, key, placeholder, value = '') {
  const f = el('div', 'field');
  f.append(el('label', '', esc(label)));
  const i = document.createElement('input');
  i.className = 'f';
  i.dataset.k = key;
  i.id = key;
  i.placeholder = placeholder;
  i.value = value;
  i.spellcheck = false;
  f.append(i);
  return f;
}
function toggleRow(label, on, act, key) {
  const b = el('button', 'f toggle' + (on ? ' on' : ''), `<span>${esc(label)}</span><span class="sw"></span>`);
  b.dataset.k = key;
  b._act = act;
  return b;
}

function renderSettings() {
  const body = $('settingsBody');
  const scroll = body.scrollTop;
  body.innerHTML = '';
  const inner = el('div', 'settings-inner');
  const c = S.cfg;

  // Плейлисты
  const pl = el('div', 's-section');
  pl.append(el('h2', '', 'Плейлисты телеканалов'));
  pl.append(el('p', 'note', 'Ссылка на M3U-плейлист от вашего IPTV-провайдера или путь к файлу на компьютере, например <b>C:\\TV\\list.m3u</b>.'));
  for (const p of c.playlists) {
    const r = el('div', 's-row');
    const active = p.id === c.activePlaylist;
    r.innerHTML = `<div class="grow"><div class="title">${esc(p.name)}${active ? '<span class="badge">Активный</span>' : ''}</div><div class="sub">${esc(p.url)}</div></div>`;
    if (!active) r.append(btn('Выбрать', () => { c.activePlaylist = p.id; saveCfg().then(() => loadChannels(true)); keepFocus(renderSettings); }, 'pl-use:' + p.id, 'primary'));
    r.append(btn('Удалить', () => modal(`Удалить плейлист «${p.name}»?`, [
      { label: 'Удалить', cls: 'danger', act: () => {
        c.playlists = c.playlists.filter((x) => x !== p);
        if (active) c.activePlaylist = c.playlists[0] ? c.playlists[0].id : '';
        saveCfg().then(() => loadChannels(true));
        renderSettings(); focusFirst($('panel-settings'));
      } },
      { label: 'Отмена' },
    ]), 'pl-del:' + p.id, 'danger'));
    pl.append(r);
  }
  const plForm = el('div', 'form');
  plForm.append(field('Название', 'pl-name', 'Мой провайдер'));
  const urlF = field('Ссылка или путь к файлу', 'pl-url', 'http://…/playlist.m3u');
  urlF.style.flex = '2.5';
  plForm.append(urlF);
  plForm.append(btn('Добавить', () => {
    const name = $('pl-name').value.trim(), url = $('pl-url').value.trim();
    if (!url) { toast('Укажите ссылку или путь к файлу'); return; }
    const p = { id: 'pl' + uid(), name: name || 'Плейлист', url };
    c.playlists.push(p);
    c.activePlaylist = p.id;
    saveCfg().then(() => loadChannels(true));
    keepFocus(renderSettings);
    toast('Плейлист добавлен и выбран');
  }, 'pl-add', 'primary'));
  pl.append(plForm);
  const plBtns = el('div', 's-row');
  plBtns.style.background = 'transparent';
  plBtns.style.padding = '.6rem 0';
  plBtns.append(btn('Обновить список каналов', () => loadChannels(true), 'pl-reload'));
  pl.append(plBtns);
  inner.append(pl);

  // Приложения
  const ap = el('div', 's-section');
  ap.append(el('h2', '', 'Приложения'));
  ap.append(el('p', 'note', 'Сайты открываются в Microsoft Edge на весь экран в отдельном профиле: вход в аккаунт сохраняется. Вернуться в TVHub — кнопка «Домой» на пульте или Ctrl+Shift+H.'));
  c.apps.forEach((a, i) => {
    const r = el('div', 's-row');
    r.innerHTML = `<div class="grow"><div class="title">${esc(a.name)}</div><div class="sub">${a.type === 'exe' ? 'Программа: ' : ''}${esc(a.target)}${a.args ? ' ' + esc(a.args) : ''}</div></div>`;
    r.append(btn('←', () => { moveApp(a, -1); keepFocus(renderSettings); }, 'app-l:' + a.id));
    r.append(btn('→', () => { moveApp(a, 1); keepFocus(renderSettings); }, 'app-r:' + a.id));
    r.append(btn('Открыть', () => launch(a), 'app-open:' + a.id));
    r.append(btn('Удалить', () => { c.apps.splice(i, 1); saveCfg(); keepFocus(renderSettings); }, 'app-del:' + a.id, 'danger'));
    ap.append(r);
  });
  const apForm = el('div', 'form');
  const seg = el('div', 'seg');
  for (const [t, l] of [['web', 'Сайт'], ['exe', 'Программа']]) {
    const b = btn(l, () => { S.newAppType = t; keepFocus(renderSettings); }, 'app-type:' + t, S.newAppType === t ? 'sel' : '');
    seg.append(b);
  }
  const segF = el('div', 'field');
  segF.style.flex = 'none';
  segF.append(el('label', '', 'Тип'));
  segF.append(seg);
  apForm.append(segF);
  apForm.append(field('Название', 'app-name', 'Например, Netflix'));
  const tF = field(S.newAppType === 'exe' ? 'Путь к программе' : 'Адрес сайта', 'app-target',
    S.newAppType === 'exe' ? 'C:\\Program Files\\VideoLAN\\VLC\\vlc.exe' : 'https://…');
  tF.style.flex = '2';
  apForm.append(tF);
  if (S.newAppType === 'exe') apForm.append(field('Параметры (необязательно)', 'app-args', '--fullscreen'));
  apForm.append(btn('Добавить', () => {
    const name = $('app-name').value.trim();
    let target = $('app-target').value.trim().replace(/^"|"$/g, '');
    if (!name || !target) { toast('Заполните название и адрес'); return; }
    if (S.newAppType === 'web' && !/^https?:\/\//i.test(target)) target = 'https://' + target;
    const colors = ['#4f8cff', '#8b5cf6', '#e64980', '#f76707', '#12b886', '#1098ad', '#e03131', '#5f3dc4'];
    c.apps.push({
      id: 'a' + uid(), name, type: S.newAppType, target,
      args: S.newAppType === 'exe' && $('app-args') ? $('app-args').value.trim() : '',
      color: colors[c.apps.length % colors.length],
    });
    saveCfg();
    keepFocus(renderSettings);
    toast(`«${name}» добавлено`);
  }, 'app-add', 'primary'));
  ap.append(apForm);
  inner.append(ap);

  renderExtraSettings(inner);

  // VPN
  const vp = el('div', 's-section');
  vp.append(el('h2', '', 'VPN'));
  const vst = VPN.st;
  vp.append(el('p', 'note', vst && vst.found
    ? `Найден ${esc(vst.name)}: ${esc(vst.path)} · сейчас ${vst.on ? '<b>включён</b>' : 'выключен'}. ` +
      'Выключение: TVHub закрывает Happ. Включение: TVHub запускает Happ; если VPN не подключится сам, откроется окно Happ, и в нём нужно нажать кнопку подключения.'
    : 'Happ не найден. Если он установлен, укажите путь к Happ.exe (например, <b>C:\\Program Files\\Happ\\Happ.exe</b>).'));
  if (vst && vst.found) {
    vp.append(el('p', 'note', 'Что видит TVHub: ' +
      `процессы Happ — ${vst.procs && vst.procs.length ? esc(vst.procs.join(', ')) : 'нет'} · ` +
      `сетевой адаптер VPN — ${vst.tunnel ? esc(vst.tunnel) : 'нет'} · ` +
      `системный прокси — ${vst.proxy ? esc(vst.proxy) : 'нет'}`));
  }
  vp.append(toggleRow('Показывать переключатель VPN на главном экране', !c.settings.vpnHidden, () => {
    c.settings.vpnHidden = !c.settings.vpnHidden; saveCfg(); renderVPN(); keepFocus(renderSettings);
  }, 'set-vpnshow'));
  const vpForm = el('div', 'form');
  const vpF = field('Путь к программе VPN (необязательно)', 'vpn-path', 'Найти автоматически', c.settings.vpnPath || '');
  vpF.style.flex = '3';
  vpForm.append(vpF);
  vpForm.append(btn('Сохранить', async () => {
    c.settings.vpnPath = $('vpn-path').value.trim().replace(/^"|"$/g, '');
    await saveCfg(); await refreshVPN(); keepFocus(renderSettings);
    toast(VPN.st && VPN.st.found ? 'VPN найден' : 'По этому пути программа не найдена');
  }, 'vpn-save', 'primary'));
  vp.append(vpForm);
  vp.append(el('p', 'note', '<br>Совет: чтобы не переключать VPN вручную, настройте в Happ маршрутизацию, при которой российские сайты идут напрямую (например, правило «geoip:ru / geosite:category-ru → direct»). Тогда Кинопоиск и другие российские сервисы работают и с включённым VPN.'));
  inner.append(vp);

  // Пульт на телефоне
  const rm = el('div', 's-section');
  rm.append(el('h2', '', 'Пульт на телефоне'));
  rm.append(el('p', 'note', 'Телефон и компьютер должны быть в одной сети Wi-Fi. Отсканируйте QR-код камерой телефона, откроется пульт в браузере. Он работает везде: в TVHub, Кинопоиске, YouTube и других программах. Совет: добавьте страницу на главный экран телефона.'));
  rm.append(toggleRow('Пульт на телефоне включён', !c.settings.phoneRemoteOff, () => {
    c.settings.phoneRemoteOff = !c.settings.phoneRemoteOff; saveCfg().then(() => setTimeout(() => keepFocus(renderSettings), 400)); keepFocus(renderSettings);
  }, 'set-phone'));
  const rbox = el('div', 'remote-box');
  rm.append(rbox);
  if (!c.settings.phoneRemoteOff) fillRemoteBox(rbox, true);
  inner.append(rm);

  // Общие
  const gen = el('div', 's-section');
  gen.append(el('h2', '', 'Общие'));
  gen.append(toggleRow('Запускать TVHub вместе с Windows (режим приставки)', c.settings.autostart, () => { c.settings.autostart = !c.settings.autostart; saveCfg(); keepFocus(renderSettings); }, 'set-auto'));
  gen.append(toggleRow('Открываться на весь экран', c.settings.fullscreen, () => { c.settings.fullscreen = !c.settings.fullscreen; saveCfg(); keepFocus(renderSettings); }, 'set-fs'));
  gen.append(toggleRow('Выключать звук на рекламных вставках (если канал их помечает)', !c.settings.keepAdSound, () => { c.settings.keepAdSound = !c.settings.keepAdSound; saveCfg(); keepFocus(renderSettings); }, 'set-admute'));
  gen.append(toggleRow('При запуске включать последний канал', c.settings.resumeLastChannel, () => { c.settings.resumeLastChannel = !c.settings.resumeLastChannel; saveCfg(); keepFocus(renderSettings); }, 'set-resume'));
  const gb = el('div', 's-row');
  gb.style.background = 'transparent';
  gb.style.padding = '.6rem 0';
  gb.append(btn('Блокировщик рекламы для сайтов', () => modal(
    'Откроется браузер с профилем TVHub. Нажмите «Получить» у uBlock Origin Lite, затем закройте окно. Блокировщик будет работать во всех сайтах внутри TVHub (Смотрим, Rutube и других).',
    [{ label: 'Открыть', cls: 'primary', act: async () => {
      const r = await api.post('/api/system', { action: 'browser-setup' });
      if (!r.ok) toast('Не получилось: ' + r.error, 5000);
    } }, { label: 'Отмена' }]), 'set-adblock'));
  gb.append(btn('Проверка пульта', () => show('keytest'), 'set-kt'));
  gb.append(btn('Полный экран вкл/выкл', () => api.post('/api/system', { action: 'fullscreen' }), 'set-fsnow'));
  gen.append(gb);
  inner.append(gen);

  // О программе
  const about = el('div', 's-section');
  about.append(el('h2', '', 'Управление'));
  about.append(el('p', 'note',
    'Стрелки — выбор · OK/Enter — открыть · Назад/Esc — вернуться · Домой — главный экран из любого места · Меню — избранное и действия · ' +
    'В плеере: вверх/вниз или CH+/CH− — переключение каналов, OK — список каналов, цифры — номер канала, Меню — «переждать рекламу» и избранное.<br><br>' +
    `TVHub ${esc(S.info.version)} · настройки хранятся в ${esc(S.info.dataDir)}`));
  inner.append(about);

  body.append(inner);
  body.scrollTop = scroll;
}

// ---------- Пульт на телефоне ----------

async function fillRemoteBox(box, withButtons) {
  box.innerHTML = '<div class="note">Получаю адрес…</div>';
  let info;
  try { info = await api.get('/api/remote'); } catch (e) { box.innerHTML = '<div class="note">Ошибка: ' + esc(e.message) + '</div>'; return; }
  box.innerHTML = '';
  if (!info.enabled) { box.append(el('div', 'note', 'Пульт выключен.')); return; }
  if (!info.urls || !info.urls.length) {
    box.append(el('div', 'note', info.error
      ? 'Не удалось запустить пульт: ' + esc(info.error)
      : 'Компьютер не подключён к домашней сети. Подключитесь к Wi-Fi или кабелю и откройте этот экран снова.'));
    return;
  }
  const u = info.urls[0];
  const wrap = el('div', 'qr-wrap');
  const img = new Image();
  img.className = 'qr';
  img.alt = 'QR-код пульта';
  img.src = '/api/remote/qr.png?' + new URLSearchParams({ t: TOKEN, u });
  wrap.append(img);
  const txt = el('div', 'qr-text');
  txt.innerHTML = '<div class="qr-step">1. Подключите телефон к тому же Wi-Fi, что и компьютер</div>' +
    '<div class="qr-step">2. Наведите камеру телефона на QR-код</div>' +
    '<div class="qr-step">3. Откройте ссылку, и пульт готов</div>' +
    `<div class="qr-url">Или наберите адрес вручную:<br><b>${esc(u)}</b></div>` +
    (info.urls.length > 1 ? `<div class="qr-alt">Если не открывается, попробуйте: ${info.urls.slice(1).map(esc).join(' · ')}</div>` : '') +
    '<div class="qr-alt">Если Windows спросит про брандмауэр, разрешите доступ в частных сетях.</div>';
  wrap.append(txt);
  box.append(wrap);
  // Приложение для Android: пульт открывается сразу, без браузера
  if (info.app) {
    const aw = el('div', 'qr-wrap app-qr');
    const ai = new Image();
    ai.className = 'qr';
    ai.alt = 'QR-код приложения';
    ai.src = '/api/remote/qr.png?' + new URLSearchParams({ t: TOKEN, u: info.app });
    aw.append(ai);
    aw.append(el('div', 'qr-text', '<div class="qr-step"><b>Приложение «Пульт TVHub» для Android</b></div>' +
      '<div class="qr-step">1. Отсканируйте этот код и скачайте файл TVHub-Remote.apk</div>' +
      '<div class="qr-step">2. Откройте его и разрешите установку из этого источника</div>' +
      '<div class="qr-step">3. В приложении нажмите «Сканировать QR-код» и наведите на код слева</div>' +
      '<div class="qr-alt">Кнопки громкости телефона в приложении управляют громкостью компьютера.</div>'));
    box.append(aw);
  }
  if (withButtons) {
    const r = el('div', 's-row');
    r.style.background = 'transparent'; r.style.padding = '.6rem 0';
    r.append(btn('Новая ссылка (старые перестанут работать)', () => modal('Создать новую ссылку? Пульты на всех телефонах придётся подключить заново.', [
      { label: 'Создать', cls: 'primary', act: async () => { await api.post('/api/remote/regen', {}); keepFocus(renderSettings); toast('Новая ссылка готова, отсканируйте QR-код'); } },
      { label: 'Отмена' },
    ]), 'phone-regen'));
    box.append(r);
  }
}

function showRemoteQR() {
  if (S.cfg.settings.phoneRemoteOff) {
    modal('Пульт на телефоне выключен. Включить?', [
      { label: 'Включить', cls: 'primary', act: async () => { S.cfg.settings.phoneRemoteOff = false; await saveCfg(); setTimeout(showRemoteQR, 400); } },
      { label: 'Отмена' },
    ]);
    return;
  }
  const box = el('div', 'remote-box in-modal');
  fillRemoteBox(box, false);
  modal('Пульт на телефоне', [{ label: 'Готово' }], box);
}

// ---------- VPN (Happ) ----------

const VPN = { st: null, busy: false };

async function refreshVPN() {
  try { VPN.st = await api.get('/api/vpn'); } catch (e) { return; }
  renderVPN();
}

function renderVPN() {
  const b = $('vpnBtn');
  const st = VPN.st;
  const show = st && st.found && !S.cfg.settings.vpnHidden;
  b.classList.toggle('hidden', !show);
  if (!st || !st.found) return;
  b.classList.toggle('on', !!st.on);
  b.classList.toggle('busy', VPN.busy);
  $('vpnText').textContent = VPN.busy ? (st.on ? 'VPN: выключаю…' : 'VPN: включаю…')
    : `VPN ${st.name}: ${st.on ? 'вкл' : (st.running ? 'не подключён' : 'выкл')}`;
  b._info = {
    kicker: 'VPN',
    title: `${st.name} ${st.on ? 'включён' : 'выключен'}`,
    sub: st.on
      ? 'Пока VPN включён, российские сервисы (Кинопоиск, Okko, Wink и другие) могут не открываться. OK — выключить.'
      : (st.running ? `${st.name} открыт, но VPN не подключён. OK — открыть ${st.name} и подключиться.` : 'OK — включить VPN. Российские сервисы лучше смотреть с выключенным VPN.'),
    color: st.on ? '#2fbf7155' : '#3b5bdb44',
  };
  if (cur === b) setHero(b._info);
}

async function toggleVPN() {
  if (!VPN.st || VPN.busy) return;
  const want = !VPN.st.on;
  VPN.busy = true; renderVPN();
  try {
    const st = await api.post('/api/vpn', { on: want });
    VPN.st = st;
    if (st.error) toast(st.error, 6000);
    else if (st.needClick) {
      toast(want
        ? `Открыл ${st.name}: нажмите в нём кнопку подключения (удобно тачпадом на пульте). Вернуться — «Домой»`
        : `${st.name} не дал отключить VPN снаружи. Открыл его окно: нажмите в нём «Отключить», потом «Домой»`, 9000);
      watchVPN(want);
    } else toast(want ? 'VPN включён' : 'VPN выключен, российские сервисы доступны');
  } catch (e) { toast('Ошибка: ' + e.message, 5000); }
  VPN.busy = false;
  renderVPN();
  // После запуска Happ проверяем ещё раз: VPN поднимается не мгновенно
  setTimeout(refreshVPN, 2500);
}

// Следим, когда пользователь сам нажмёт кнопку в Happ, и обновляем переключатель
let vpnWatch;
function watchVPN(want) {
  clearInterval(vpnWatch);
  const until = Date.now() + 90000;
  vpnWatch = setInterval(async () => {
    await refreshVPN();
    if ((VPN.st && !!VPN.st.on === want) || Date.now() > until) {
      clearInterval(vpnWatch);
      if (VPN.st && !!VPN.st.on === want) toast(want ? 'VPN подключён' : 'VPN выключен');
    }
  }, 1500);
}

// ---------- События от TVHub (пульт на телефоне и др.) ----------

function listenEvents() {
  const es = new EventSource('/api/events?t=' + encodeURIComponent(TOKEN));
  const KEYS = { up: 'ArrowUp', down: 'ArrowDown', left: 'ArrowLeft', right: 'ArrowRight', ok: 'Enter', enter: 'Enter', back: 'Escape', esc: 'Escape',
    menu: 'ContextMenu', chup: 'PageUp', chdown: 'PageDown', playpause: 'MediaPlayPause', backspace: 'Backspace', space: ' ' };
  es.onmessage = (m) => {
    let ev;
    try { ev = JSON.parse(m.data); } catch (e) { return; }
    if (ev.type === 'toast') toast(ev.text);
    else if (ev.type === 'notice') onNoticeEvent(ev.notice);
    else if (ev.type === 'epg') loadEPGNow();
    else if (ev.type === 'update') onUpdateEvent(ev);
    else if (ev.type === 'brightness') onBrightnessEvent(ev);
    else if (ev.type === 'home') window.tvHome();
    else if (ev.type === 'key') {
      const key = KEYS[ev.key] || ev.key;
      if (key.length === 1 && cur && cur.tagName === 'INPUT' && /[0-9 ]/.test(key)) { cur.setRangeText(key, cur.selectionStart, cur.selectionEnd, 'end'); return; }
      onKey(new KeyboardEvent('keydown', { key }));
    } else if (ev.type === 'text') {
      if (cur && cur.tagName === 'INPUT') { cur.setRangeText(ev.text, cur.selectionStart, cur.selectionEnd, 'end'); }
      else for (const ch of ev.text) onKey(new KeyboardEvent('keydown', { key: ch }));
    }
  };
}

function openSettingsAt(key) {
  switchTab('settings');
  focusByKey(key, $('main'));
}

// ---------- Модальное окно ----------

let modalReturn = null;
function modal(text, buttons, extra) {
  modalReturn = cur;
  $('modalText').textContent = text;
  if (extra) $('modalText').append(extra);
  const box = $('modalBtns');
  box.innerHTML = '';
  box.classList.toggle('col', buttons.length > 3);
  buttons.forEach((b, i) => {
    const e = btn(b.label, () => { closeModal(); if (b.act) b.act(); }, 'm' + i, b.cls || '');
    box.append(e);
  });
  $('modal').classList.remove('hidden');
  cur = null;
  setFocus(box.firstChild);
}
function closeModal() {
  $('modal').classList.add('hidden');
  const r = modalReturn;
  modalReturn = null;
  cur = null;
  if (r && r.isConnected && r.offsetParent !== null) setFocus(r, { scroll: false });
  else focusFirst();
}
function confirmSys(action, text) {
  modal(text, [
    { label: 'Да', cls: action === 'shutdown' ? 'danger' : 'primary', act: () => api.post('/api/system', { action }) },
    { label: 'Отмена' },
  ]);
}

// ---------- Экраны ----------

const mainFocus = { k: null };
function show(name) {
  if (S.screen === 'main' && cur) mainFocus.k = cur.dataset.k;
  S.screen = name;
  ['main', 'player', 'keytest'].forEach((s) => $(s).classList.toggle('hidden', s !== name));
  cur = null;
  if (name === 'main') {
    renderPanel(S.tab);
    if (!focusByKey(mainFocus.k, $('main'))) focusFirst($('panel-' + S.tab));
  }
  if (name === 'keytest') { $('ktLog').innerHTML = ''; $('ktBig').textContent = 'Нажмите любую кнопку на пульте'; $('ktAction').textContent = ''; }
}

// Вызывается из Go, когда нажата кнопка «Домой» на пульте
window.tvHome = function () {
  hideSaver();
  if (!$('modal').classList.contains('hidden')) closeModal();
  if (S.screen === 'player') stopPlayback();
  mainFocus.k = null;
  S.tab = '';
  show('main');
  switchTab('home');
  focusFirst($('homeRows'));
};

// ---------- Плеер ----------

const video = $('video');
const P = { hls: null, ts: null, list: [], idx: 0, ch: null, gen: 0, watchdog: null, zapTimer: null, osdTimer: null, num: '', numTimer: null };

function proxied(ch) {
  const q = new URLSearchParams({ t: TOKEN, u: ch.url });
  if (ch.ua) q.set('ua', ch.ua);
  if (ch.ref) q.set('ref', ch.ref);
  return '/p?' + q.toString();
}

function playChannel(ch, list) {
  P.list = list && list.length ? list : S.channels;
  P.idx = P.list.findIndex((c) => c.url === ch.url);
  if (P.idx < 0) { P.list = [ch]; P.idx = 0; }
  show('player');
  startPlayback(ch);
}

// Рекламная вставка в потоке: приглушаем звук и показываем значок
function setAd(on) {
  if (on === P.inAd) return;
  P.inAd = on;
  const mute = on && !S.cfg.settings.keepAdSound;
  $('adBadge').classList.toggle('hidden', !mute);
  if (mute) { P.mutedByAd = true; video.muted = true; }
  else if (P.mutedByAd) { P.mutedByAd = false; video.muted = false; }
}

function stopPlayback() {
  P.gen++;
  setAd(false);
  clearTimeout(P.watchdog);
  clearTimeout(P.zapTimer);
  if (P.hls) { try { P.hls.destroy(); } catch (e) {} P.hls = null; }
  if (P.ts) { try { P.ts.destroy(); } catch (e) {} P.ts = null; }
  video.onerror = null;
  video.removeAttribute('src');
  try { video.load(); } catch (e) {}
  $('spinner').classList.add('hidden');
}

function setStatus(text, err = false) {
  const s = $('osdStatus');
  s.textContent = text;
  s.classList.toggle('err', err);
}

function showOSD(persist = false) {
  const ch = P.ch;
  if (!ch) return;
  $('osdNum').textContent = ch.n;
  const logo = $('osdLogo');
  logo.innerHTML = '';
  logo.append(logoBox(ch, 'osd-logo-in'));
  $('osdName').textContent = (isFav(ch.url) ? '★ ' : '') + ch.name;
  $('osdMeta').textContent = `${ch.group || ''} · ${P.idx + 1} из ${P.list.length}`;
  updateOSDProg();
  $('osd').classList.remove('hidden');
  clearTimeout(P.osdTimer);
  if (!persist) P.osdTimer = setTimeout(() => $('osd').classList.add('hidden'), 5000);
}

function fail(gen, msg) {
  if (gen !== P.gen) return;
  clearTimeout(P.watchdog);
  $('spinner').classList.add('hidden');
  setStatus('Канал недоступен: ' + msg, true);
  showOSD(true);
}

async function probe(src, gen) {
  // Узнаём тип потока по заголовку Content-Type, не скачивая его целиком
  const ctl = new AbortController();
  const t = setTimeout(() => ctl.abort(), 12000);
  try {
    const r = await fetch(src, { signal: ctl.signal });
    const ct = (r.headers.get('content-type') || '').toLowerCase();
    ctl.abort();
    if (!r.ok) return { err: 'сервер ответил ' + r.status };
    return { ct };
  } catch (e) {
    if (gen !== P.gen) return { err: 'cancel' };
    return { err: 'нет связи с сервером' };
  } finally { clearTimeout(t); }
}

function startPlayback(ch) {
  stopPlayback();
  const gen = P.gen;
  P.ch = ch;
  if (P.lastPlayed && P.lastPlayed.url !== ch.url) P.prevCh = P.lastPlayed;
  P.lastPlayed = ch;
  S.cfg.lastChannel = ch.url;
  saveCfg();
  setStatus('Загрузка…');
  showOSD(true);
  $('spinner').classList.remove('hidden');
  const src = proxied(ch);
  const path = ch.url.toLowerCase().split('?')[0];
  P.watchdog = setTimeout(() => fail(gen, 'не отвечает'), 25000);

  if (/\.m3u8$/.test(path)) return tryHls(src, gen);
  if (/\.flv$/.test(path)) return tryTs(src, 'flv', gen);
  if (/\.(mp4|webm|mov|m4v)$/.test(path)) return tryNative(src, gen);
  probe(src, gen).then((r) => {
    if (gen !== P.gen) return;
    if (r.err) return fail(gen, r.err);
    if (r.ct.includes('mpegurl')) tryHls(src, gen);
    else if (r.ct.includes('flv')) tryTs(src, 'flv', gen);
    else if (r.ct.includes('mp4') || r.ct.includes('webm')) tryNative(src, gen);
    else if (r.ct.includes('mp2t') || r.ct.includes('octet-stream') || r.ct.includes('video')) tryTs(src, 'mpegts', gen);
    else tryHls(src, gen);
  });
}

function tryHls(src, gen) {
  if (!window.Hls || !Hls.isSupported()) return tryNative(src, gen);
  const hls = new Hls({ maxBufferLength: 30, liveSyncDurationCount: 3, manifestLoadingTimeOut: 10000, manifestLoadingMaxRetry: 1, fragLoadingMaxRetry: 4 });
  P.hls = hls;
  let net = 0, media = 0;
  hls.on(Hls.Events.MANIFEST_PARSED, () => { video.play().catch(() => {}); });
  hls.on(Hls.Events.FRAG_CHANGED, (ev, d) => { if (gen === P.gen) setAd(/[?&]ad=1(&|$)/.test(d.frag.url || '')); });
  hls.on(Hls.Events.ERROR, (ev, d) => {
    if (gen !== P.gen || !d.fatal) return;
    if (['manifestParsingError', 'manifestLoadTimeOut', 'manifestIncompatibleCodecsError'].includes(d.details)) {
      hls.destroy(); P.hls = null; return tryTs(src, 'mpegts', gen);
    }
    if (d.details === 'manifestLoadError') return fail(gen, 'плейлист канала не загружается');
    if (d.type === Hls.ErrorTypes.NETWORK_ERROR && net++ < 4) {
      setStatus('Переподключение…');
      setTimeout(() => gen === P.gen && hls.startLoad(), 1500);
    } else if (d.type === Hls.ErrorTypes.MEDIA_ERROR && media++ < 2) {
      hls.recoverMediaError();
    } else fail(gen, d.type === Hls.ErrorTypes.MEDIA_ERROR ? 'формат видео не поддерживается' : 'обрыв связи');
  });
  hls.loadSource(src);
  hls.attachMedia(video);
}

function tryTs(src, type, gen) {
  if (!window.mpegts || !mpegts.isSupported()) return tryNative(src, gen);
  const p = mpegts.createPlayer({ type, isLive: true, url: location.origin + src }, { enableWorker: true, liveBufferLatencyChasing: true, lazyLoad: false });
  P.ts = p;
  p.attachMediaElement(video);
  p.on(mpegts.Events.ERROR, () => {
    if (gen !== P.gen) return;
    try { p.destroy(); } catch (e) {}
    P.ts = null;
    tryNative(src, gen);
  });
  p.load();
  const r = p.play();
  if (r && r.catch) r.catch(() => {});
}

function tryNative(src, gen) {
  if (gen !== P.gen) return;
  video.onerror = () => fail(gen, 'формат не поддерживается');
  video.src = src;
  video.play().catch(() => {});
}

video.addEventListener('playing', () => {
  clearTimeout(P.watchdog);
  $('spinner').classList.add('hidden');
  setStatus('Прямой эфир');
  clearTimeout(P.osdTimer);
  P.osdTimer = setTimeout(() => $('osd').classList.add('hidden'), 4000);
});
video.addEventListener('waiting', () => { if (S.screen === 'player') $('spinner').classList.remove('hidden'); });

function zap(delta) {
  if (!P.list.length) return;
  P.idx = (P.idx + delta + P.list.length) % P.list.length;
  P.ch = P.list[P.idx];
  stopPlayback();
  setStatus('…');
  showOSD(true);
  // Небольшая пауза, чтобы можно было быстро пролистать несколько каналов
  P.zapTimer = setTimeout(() => startPlayback(P.ch), 350);
}

function digit(d) {
  P.num = (P.num + d).slice(-4);
  $('numEntry').textContent = P.num;
  $('numEntry').classList.remove('hidden');
  clearTimeout(P.numTimer);
  P.numTimer = setTimeout(() => {
    const n = parseInt(P.num, 10);
    P.num = '';
    $('numEntry').classList.add('hidden');
    const ch = S.channels.find((c) => c.n === n);
    if (ch) { P.list = S.channels; P.idx = S.channels.indexOf(ch); startPlayback(ch); }
    else toast(`Канала № ${n} нет`);
  }, 1500);
}

function openZap() {
  $('zap').classList.remove('hidden');
  $('osd').classList.add('hidden');
  renderZap();
  cur = null;
  focusByKey('z:' + P.idx) || focusFirst($('zap'));
}
function closeZap() {
  $('zap').classList.add('hidden');
  cur = null;
}

function renderZap() {
  const list = $('zapList');
  list.innerHTML = '';
  const label = S.tvGroup === '__fav' ? 'Избранное' : (S.tvGroup && S.tvGroup !== '__all' && P.list !== S.channels ? S.tvGroup : 'Каналы');
  $('zapHead').innerHTML = `<span>${esc(label)}</span><small>${P.list.length}</small>`;
  // Для очень длинных списков рисуем только окно вокруг текущего канала
  const from = P.list.length > 1200 ? Math.max(0, P.idx - 400) : 0;
  const to = Math.min(P.list.length, from + 1200);
  for (let i = from; i < to; i++) {
    const ch = P.list[i];
    const b = el('button', 'f zap-item' + (i === P.idx ? ' playing' : ''));
    b.dataset.k = 'z:' + i;
    b.innerHTML = `<span class="n">${ch.n}</span>`;
    b.append(logoBox(ch, 'lg'));
    const e = epgNow(ch.url);
    b.insertAdjacentHTML('beforeend', `<span class="nm">${esc(ch.name)}${e && e.c ? `<small>${esc(e.c.t)}</small>` : ''}</span>${isFav(ch.url) ? '<span class="st">★</span>' : ''}`);
    b._act = () => { P.idx = i; closeZap(); startPlayback(ch); };
    b._menu = () => toggleFav(ch);
    list.append(b);
  }
}

// ---------- «Переждать рекламу» ----------

function playerMenu() {
  if (!P.ch) return;
  const ch = P.ch;
  const items = [];
  if (P.wait) items.push({ label: `Вернуться на «${P.wait.ch.name}» сейчас`, cls: 'primary', act: () => endWait(true) });
  else {
    items.push({ label: 'Переждать рекламу: 3 минуты', cls: 'primary', act: () => startWait(3) });
    items.push({ label: 'Переждать рекламу: 5 минут', act: () => startWait(5) });
  }
  items.push({ label: isFav(ch.url) ? 'Убрать из избранного' : 'Добавить в избранное', act: () => { toggleFav(ch); showOSD(); } });
  items.push({ label: 'Отмена' });
  modal(`«${ch.name}»`, items);
}

function startWait(min) {
  const back = { ch: P.ch, list: P.list, idx: P.idx };
  // Куда уйти на время рекламы: на предыдущий канал или на следующий в списке
  let other = P.prevCh && P.prevCh.url !== P.ch.url ? P.prevCh : null;
  let idx;
  if (other) { idx = P.list.findIndex((c) => c.url === other.url); }
  if (!other || idx < 0) { idx = (P.idx + 1) % P.list.length; other = P.list[idx]; }
  if (!other || other.url === back.ch.url) { toast('Нет другого канала, на который можно переключиться'); return; }
  P.wait = { ...back, until: Date.now() + min * 60000 };
  P.idx = idx;
  startPlayback(other);
  tickWait();
  P.waitTimer = setInterval(tickWait, 1000);
  toast(`Вернёмся на «${back.ch.name}» через ${min} мин`);
}

function tickWait() {
  const w = P.wait;
  if (!w) return;
  const left = Math.max(0, Math.round((w.until - Date.now()) / 1000));
  if (left <= 0) { endWait(true); return; }
  const chip = $('waitChip');
  chip.textContent = `Вернёмся на «${w.ch.name}» через ${Math.floor(left / 60)}:${String(left % 60).padStart(2, '0')} · Меню — вернуться сейчас`;
  chip.classList.remove('hidden');
}

function endWait(goBack) {
  const w = P.wait;
  clearInterval(P.waitTimer);
  P.wait = null;
  $('waitChip').classList.add('hidden');
  if (goBack && w && S.screen === 'player') {
    P.list = w.list; P.idx = w.idx;
    closeZap();
    startPlayback(w.ch);
    toast(`Возвращаемся на «${w.ch.name}»`);
  }
}

function exitPlayer() {
  endWait(false);
  $('guide').classList.add('hidden');
  stopPlayback();
  closeZap();
  $('osd').classList.add('hidden');
  show('main');
}

function playerKey(k, e) {
  switch (k) {
    case 'up': case 'chup': zap(1); break;
    case 'down': case 'chdown': zap(-1); break;
    case 'ok': case 'left': openZap(); break;
    case 'right': openGuide(); break;
    case 'info': showOSD(); break;
    case 'back': exitPlayer(); break;
    case 'menu': playerMenu(); break;
    case 'space': case 'playpause': if (video.paused) video.play().catch(() => {}); else video.pause(); break;
    case 'digit': digit(e.key); break;
    default: return false;
  }
  return true;
}

// ---------- Проверка пульта ----------

const KT_NAMES = { up: 'Вверх', down: 'Вниз', left: 'Влево', right: 'Вправо', ok: 'OK', back: 'Назад', menu: 'Меню', playpause: 'Пауза/Плей',
  chup: 'Канал +', chdown: 'Канал −', home: 'Домой', digit: 'Цифра', space: 'Пробел', info: 'Инфо', backspace: 'Стереть', fullscreen: 'Полный экран' };
let ktBack = 0;
function keytestKey(k, e) {
  $('ktBig').textContent = e.key === ' ' ? 'Space' : e.key;
  $('ktAction').textContent = k ? 'В TVHub это: ' + KT_NAMES[k] : 'Кнопка не используется в TVHub';
  const line = `key=${e.key}  code=${e.code || '—'}  keyCode=${e.keyCode}`;
  const log = $('ktLog');
  log.insertAdjacentHTML('afterbegin', `<div>${esc(line)}</div>`);
  while (log.children.length > 8) log.lastChild.remove();
  if (k === 'back') {
    if (Date.now() - ktBack < 1200) show('main');
    ktBack = Date.now();
  }
}

// ---------- Обработка клавиш ----------

function keyName(e) {
  const k = e.key, c = e.keyCode;
  if (k === 'ArrowUp' || c === 38) return 'up';
  if (k === 'ArrowDown' || c === 40) return 'down';
  if (k === 'ArrowLeft' || c === 37) return 'left';
  if (k === 'ArrowRight' || c === 39) return 'right';
  if (k === 'Enter' || c === 13) return 'ok';
  if (k === 'Escape' || k === 'BrowserBack' || k === 'GoBack' || c === 27 || c === 166 || c === 461 || c === 10009) return 'back';
  if (k === 'Backspace') return 'backspace';
  if (k === 'ContextMenu' || c === 93 || k === 'F2') return 'menu';
  if (k === 'MediaPlayPause' || k === 'MediaPlay' || k === 'MediaPause' || c === 179) return 'playpause';
  if (k === 'PageUp' || k === 'ChannelUp' || k === 'MediaTrackNext' || c === 427 || c === 176) return 'chup';
  if (k === 'PageDown' || k === 'ChannelDown' || k === 'MediaTrackPrevious' || c === 428 || c === 177) return 'chdown';
  if (k === 'BrowserHome' || c === 172) return 'home';
  if (k === 'F11') return 'fullscreen';
  if (k === ' ') return 'space';
  if (/^[0-9]$/.test(k)) return 'digit';
  if (k === 'i' || k === 'Info') return 'info';
  return null;
}

function activate() {
  if (!cur) return;
  if (cur.tagName === 'INPUT') { move('right') || move('down'); return; }
  if (cur._act) cur._act();
}

function back() {
  if (!$('modal').classList.contains('hidden')) { closeModal(); return; }
  if (S.screen === 'player') {
    if (guideOpen()) closeGuide();
    else if (!$('zap').classList.contains('hidden')) { closeZap(); showOSD(); }
    else exitPlayer();
    return;
  }
  if (S.screen === 'keytest') { show('main'); return; }
  if (S.screen === 'main') {
    const inTabs = cur && (cur.closest('#tabs') || cur.id === 'profileBtn');
    if (S.tab === 'tv' && S.search) { S.search = ''; renderGrid(); focusByKey('g:' + S.tvGroup, $('main')); return; }
    if (!inTabs) { focusByKey('tab-' + S.tab, $('main')); return; }
    if (S.tab !== 'home') { switchTab('home'); focusByKey('tab-home', $('main')); }
  }
}

let userMoved = false;
function onKey(e) {
  const k = keyName(e);
  userMoved = true;
  markActivity();
  if (saverActive()) { e.preventDefault(); hideSaver(); return; }
  if (S.screen === 'keytest') {
    e.preventDefault();
    keytestKey(k, e);
    return;
  }
  if (k === 'home') { e.preventDefault(); window.tvHome(); return; }
  if (k === 'fullscreen') { e.preventDefault(); api.post('/api/system', { action: 'fullscreen' }); return; }

  // Поле ввода: печатаем как обычно, стрелки вверх/вниз уводят из поля
  if (cur && cur.tagName === 'INPUT' && document.activeElement === cur) {
    const atStart = cur.selectionStart === 0 && cur.selectionEnd === 0;
    const atEnd = cur.selectionStart === cur.value.length;
    if (k === 'up' || k === 'down') { e.preventDefault(); move(k); return; }
    if ((k === 'left' && atStart) || (k === 'right' && atEnd)) { e.preventDefault(); move(k); return; }
    if (k === 'ok') { e.preventDefault(); activate(); return; }
    if (k === 'back' && e.key !== 'Backspace') { e.preventDefault(); back(); return; }
    return;
  }

  if (S.screen === 'player' && $('modal').classList.contains('hidden') && $('zap').classList.contains('hidden') && !guideOpen()) {
    if (playerKey(k, e)) e.preventDefault();
    return;
  }

  // Поиск каналов набором с клавиатуры
  if (S.screen === 'main' && S.tab === 'tv' && $('modal').classList.contains('hidden') && cur && cur.closest('#panel-tv')) {
    if (e.key.length === 1 && !e.ctrlKey && !e.altKey && /[\p{L}\p{N} +\-]/u.test(e.key) && !(e.key === ' ' && !S.search)) {
      e.preventDefault();
      S.search += e.key;
      renderGrid();
      focusFirst($('chGrid'));
      return;
    }
    if (k === 'backspace' && S.search) {
      e.preventDefault();
      S.search = S.search.slice(0, -1);
      renderGrid();
      if (S.search) focusFirst($('chGrid'));
      else focusByKey('g:' + S.tvGroup, $('main'));
      return;
    }
  }

  switch (k) {
    case 'up': case 'down': case 'left': case 'right': e.preventDefault(); move(k); break;
    case 'ok': case 'space': e.preventDefault(); activate(); break;
    case 'back': case 'backspace': e.preventDefault(); back(); break;
    case 'menu': e.preventDefault(); if (cur && cur._menu) cur._menu(); break;
    case 'chup': e.preventDefault(); for (let i = 0; i < 5; i++) move('up'); break;
    case 'chdown': e.preventDefault(); for (let i = 0; i < 5; i++) move('down'); break;
  }
}

document.addEventListener('keydown', onKey, true);

// Кнопка «Назад» на некоторых пультах работает как «Назад» в браузере — перехватываем
history.pushState({ tv: 1 }, '');
window.addEventListener('popstate', () => { history.pushState({ tv: 1 }, ''); if (S.screen !== 'keytest') back(); });

// ---------- Мышь ----------

let lastMouse = 0, cursorTimer;
document.addEventListener('mousemove', () => {
  lastMouse = Date.now();
  document.body.classList.remove('no-cursor');
  clearTimeout(cursorTimer);
  cursorTimer = setTimeout(() => document.body.classList.add('no-cursor'), 3000);
});
document.addEventListener('mouseover', (e) => {
  if (Date.now() - lastMouse > 200) return;
  const f = e.target.closest('.f');
  const scope = scopeEl();
  if (f && scope && scope.contains(f) && !f.closest('#tabs')) setFocus(f, { scroll: false });
});
document.addEventListener('click', (e) => {
  const f = e.target.closest('.f');
  if (S.screen === 'player' && !f) { if ($('zap').classList.contains('hidden')) openZap(); return; }
  if (!f) return;
  const scope = scopeEl();
  if (!scope || !scope.contains(f)) return;
  setFocus(f, { scroll: false });
  if (f.tagName !== 'INPUT') activate();
});
document.addEventListener('wheel', (e) => {
  if (S.screen !== 'main') return;
  const sc = e.target.closest('.rows, .ch-grid-wrap, .groups, .settings-body, .apps-grid, .zap-list, .row-track');
  if (sc) { if (sc.classList.contains('row-track')) sc.scrollLeft += e.deltaY; else sc.scrollTop += e.deltaY; }
}, { passive: true });

// ---------- Геймпад ----------

const GP = { held: {}, t: {} };
function gpLoop() {
  const pads = navigator.getGamepads ? navigator.getGamepads() : [];
  const pad = [...pads].find((p) => p);
  if (pad) {
    const ax = pad.axes || [];
    const st = {
      up: pad.buttons[12]?.pressed || ax[1] < -0.6, down: pad.buttons[13]?.pressed || ax[1] > 0.6,
      left: pad.buttons[14]?.pressed || ax[0] < -0.6, right: pad.buttons[15]?.pressed || ax[0] > 0.6,
      ok: pad.buttons[0]?.pressed, back: pad.buttons[1]?.pressed, menu: pad.buttons[9]?.pressed || pad.buttons[3]?.pressed,
      chdown: pad.buttons[4]?.pressed, chup: pad.buttons[5]?.pressed,
    };
    const now = performance.now();
    for (const [name, on] of Object.entries(st)) {
      if (on && !GP.held[name]) { GP.held[name] = true; GP.t[name] = now + 400; fireGp(name); }
      else if (on && ['up', 'down', 'left', 'right'].includes(name) && now > GP.t[name]) { GP.t[name] = now + 110; fireGp(name); }
      else if (!on) GP.held[name] = false;
    }
  }
  requestAnimationFrame(gpLoop);
}
function fireGp(name) {
  const map = { up: 'ArrowUp', down: 'ArrowDown', left: 'ArrowLeft', right: 'ArrowRight', ok: 'Enter', back: 'Escape', menu: 'ContextMenu', chup: 'PageUp', chdown: 'PageDown' };
  onKey(new KeyboardEvent('keydown', { key: map[name] }));
}
window.addEventListener('gamepadconnected', () => toast('Геймпад подключён'));

// ---------- Запуск ----------

async function init() {
  tickClock();
  setInterval(tickClock, 5000);
  setupTabs();
  try {
    const st = await api.get('/api/state');
    S.info = st;
    S.cfg = st.config;
    S.cfg.favorites = S.cfg.favorites || [];
    S.cfg.apps = S.cfg.apps || [];
    S.cfg.playlists = S.cfg.playlists || [];
    S.cfg.settings = S.cfg.settings || {};
  } catch (e) {
    document.body.innerHTML = '<p style="padding:3rem;font-size:2rem">Не удалось подключиться к TVHub. Перезапустите программу.</p>';
    return;
  }
  listenEvents();
  const vb = $('vpnBtn');
  vb._act = toggleVPN;
  refreshVPN();
  setInterval(() => { if (!VPN.busy && S.screen === 'main') refreshVPN(); }, 5000);
  switchTab('home');
  focusFirst($('homeRows'));
  await loadChannels(false);
  startExtras();
  // Пока грузились каналы, пользователь ничего не нажимал — ставим фокус на первый ряд
  if (S.screen === 'main' && !userMoved && S.tab === 'home') { $('homeRows').scrollTop = 0; focusFirst($('homeRows')); }
  if (S.cfg.settings.resumeLastChannel && S.screen === 'main') {
    const ch = S.channels.find((c) => c.url === S.cfg.lastChannel);
    if (ch) playChannel(ch, S.channels);
  }
  requestAnimationFrame(gpLoop);
}

init();
