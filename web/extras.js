'use strict';
/* TVHub: телепрограмма, погода, уведомления, заставка, музыка.
   Загружается до app.js; функции вызываются из app.js после запуска. */

// ---------- Общие мелочи ----------

function fmtTime(ts) { return new Date(ts * 1000).toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' }); }
function progPct(p) { const n = Date.now() / 1000; return Math.max(0, Math.min(100, ((n - p.s) / (p.e - p.s)) * 100)); }
function minsLeft(p) { return Math.max(0, Math.round((p.e - Date.now() / 1000) / 60)); }
function ago(ts) {
  const s = Math.max(0, Date.now() / 1000 - ts);
  if (s < 60) return 'только что';
  if (s < 3600) return Math.round(s / 60) + ' мин назад';
  if (s < 86400) return Math.round(s / 3600) + ' ч назад';
  return new Date(ts * 1000).toLocaleDateString('ru-RU', { day: 'numeric', month: 'long' });
}
function fmtDur(sec) {
  sec = Math.max(0, Math.round(sec || 0));
  return Math.floor(sec / 60) + ':' + String(sec % 60).padStart(2, '0');
}

// ---------- Телепрограмма ----------

const EPG = { now: {}, status: null, loaded: false };

function epgNow(url) { return EPG.now[url] || null; }

async function loadEPGNow() {
  let r;
  try { r = await api.get('/api/epg'); } catch (e) { return; }
  EPG.now = r.now || {};
  EPG.status = r.status || null;
  EPG.loaded = true;
  // Русские названия из телепрограммы для каналов, которые остались латиницей
  let renamed = false;
  for (const ch of S.channels) {
    const ru = r.names && r.names[ch.url];
    if (ru && ru !== ch.name) { if (!ch.orig) ch.orig = ch.name; ch.name = ru; renamed = true; }
  }
  if (renamed) computeGroups();
  refreshEPGViews();
}

function refreshEPGViews() {
  if (S.screen === 'main' && $('modal').classList.contains('hidden') && (S.tab === 'home' || S.tab === 'tv')) {
    keepFocus(() => renderPanel(S.tab));
  }
  if (S.screen === 'player' && !$('osd').classList.contains('hidden')) updateOSDProg();
}

// Строка «сейчас в эфире» на карточке канала и в большой карточке сверху
function epgDecorate(b, ch) {
  const e = epgNow(ch.url);
  if (!e || !e.c) return;
  b.classList.add('has-prog');
  b.append(el('div', 'prog', esc(e.c.t)));
  const bar = el('div', 'pbar');
  bar.append(el('i'));
  bar.firstChild.style.width = progPct(e.c).toFixed(1) + '%';
  b.append(bar);
  const next = e.n ? ` · Далее в ${fmtTime(e.n.s)}: ${e.n.t}` : '';
  b._info = { ...b._info, sub: `Сейчас: ${e.c.t} (${fmtTime(e.c.s)}–${fmtTime(e.c.e)}, ещё ${minsLeft(e.c)} мин)${next}` };
}

function updateOSDProg() {
  const box = $('osdProg');
  if (!box) return;
  const e = P.ch && epgNow(P.ch.url);
  if (!e || (!e.c && !e.n)) { box.innerHTML = ''; return; }
  let h = '';
  if (e.c) {
    h += `<div class="op-now"><b>${fmtTime(e.c.s)}–${fmtTime(e.c.e)}</b> ${esc(e.c.t)}</div>` +
      `<div class="pbar"><i style="width:${progPct(e.c).toFixed(1)}%"></i></div>`;
  }
  if (e.n) h += `<div class="op-next">Далее в ${fmtTime(e.n.s)}: ${esc(e.n.t)} · → — программа</div>`;
  box.innerHTML = h;
}

// Панель телепрограммы в плеере (кнопка «вправо»)
async function openGuide() {
  const ch = P.ch;
  if (!ch) return;
  const g = $('guide');
  $('guideHead').innerHTML = `<span>Телепрограмма</span><small>${esc(ch.name)}</small>`;
  const list = $('guideList');
  list.innerHTML = '<div class="g-empty">Загружаю…</div>';
  g.classList.remove('hidden');
  $('osd').classList.add('hidden');
  cur = null;
  let progs = [];
  try { progs = (await api.get('/api/epg/ch?u=' + encodeURIComponent(ch.url))).progs || []; } catch (e) {}
  if (g.classList.contains('hidden') || P.ch !== ch) return;
  renderGuide(ch, progs);
}

function hasReminder(url, start) { return (S.cfg.reminders || []).some((r) => r.chUrl === url && r.start === start); }

function renderGuide(ch, progs) {
  const list = $('guideList');
  list.innerHTML = '';
  if (!progs.length) {
    list.append(el('div', 'g-empty', EPG.status && EPG.status.programmes
      ? 'Для этого канала телепрограммы нет.'
      : 'Телепрограмма не загружена. Её можно подключить в «Настройки» → «Телеканалы и телепрограмма».'));
    const b = el('button', 'f btn', 'Закрыть');
    b._act = closeGuide;
    list.append(b);
    setFocus(b);
    return;
  }
  const now = Date.now() / 1000;
  let curItem = null, lastDay = '';
  progs.forEach((p, i) => {
    const day = new Date(p.s * 1000).toLocaleDateString('ru-RU', { weekday: 'long', day: 'numeric', month: 'long' });
    if (day !== lastDay) { list.append(el('div', 'g-day', esc(day))); lastDay = day; }
    const state = p.e <= now ? 'past' : (p.s <= now ? 'now' : 'future');
    const b = el('button', 'f g-item ' + state);
    b.dataset.k = 'g:' + p.s;
    const rem = hasReminder(ch.url, p.s);
    b.innerHTML = `<span class="g-t">${fmtTime(p.s)}</span><span class="g-body"><span class="g-title">${esc(p.t)}</span>` +
      (p.c ? `<span class="g-cat">${esc(p.c)}</span>` : '') +
      (state === 'now' ? `<span class="pbar"><i style="width:${progPct(p).toFixed(1)}%"></i></span>` : '') +
      `</span>${rem ? '<span class="g-bell">🔔</span>' : ''}`;
    b._act = () => {
      if (state === 'future') toggleReminder(ch, p);
      else closeGuide();
    };
    b._onfocus = () => {
      const d = $('guideDesc');
      d.innerHTML = `<b>${esc(p.t)}</b><br>${fmtTime(p.s)}–${fmtTime(p.e)}${p.c ? ' · ' + esc(p.c) : ''}` +
        (p.d ? `<p>${esc(p.d)}</p>` : '') +
        (state === 'future' ? `<p class="g-hint">OK — ${hasReminder(ch.url, p.s) ? 'отменить напоминание' : 'напомнить о начале'}</p>` : '');
    };
    list.append(b);
    if (state === 'now' || (!curItem && state === 'future')) curItem = curItem || b;
  });
  setFocus(curItem || list.querySelector('.g-item'));
}

async function toggleReminder(ch, p) {
  const remove = hasReminder(ch.url, p.s);
  try {
    const r = await api.post('/api/reminder', { chUrl: ch.url, chName: ch.name, title: p.t, start: p.s, remove });
    S.cfg.reminders = r.reminders || [];
    toast(remove ? 'Напоминание отменено' : `Напомню в ${fmtTime(p.s - 60)}: «${p.t}»`);
  } catch (e) { toast('Ошибка: ' + e.message); }
  const k = cur && cur.dataset.k;
  let progs = [];
  try { progs = (await api.get('/api/epg/ch?u=' + encodeURIComponent(ch.url))).progs || []; } catch (e) {}
  renderGuide(ch, progs);
  focusByKey(k, $('guide'));
}

function closeGuide() {
  $('guide').classList.add('hidden');
  cur = null;
  showOSD();
}

function guideOpen() { return !$('guide').classList.contains('hidden'); }

// ---------- Погода ----------

const WX = { data: null };

const WX_SVG = {
  sun: '<circle cx="12" cy="12" r="4.2"/><path d="M12 2.5v2.2M12 19.3v2.2M2.5 12h2.2M19.3 12h2.2M5.3 5.3l1.6 1.6M17.1 17.1l1.6 1.6M5.3 18.7l1.6-1.6M17.1 6.9l1.6-1.6"/>',
  moon: '<path d="M19.5 14.5A8 8 0 1 1 9.5 4.5a6.4 6.4 0 0 0 10 10z"/>',
  cloud: '<path d="M7 18.5h10.2a4.1 4.1 0 0 0 .4-8.2A6 6 0 0 0 6.1 9.6 4.5 4.5 0 0 0 7 18.5z"/>',
  partly: '<circle cx="8" cy="8" r="3"/><path d="M8 2.2v1.3M2.2 8h1.3M3.9 3.9l.9.9M12.1 3.9l-.9.9"/><path d="M9 19.5h8.6a3.6 3.6 0 0 0 .4-7.1 5.2 5.2 0 0 0-9.7-1.3A3.9 3.9 0 0 0 9 19.5z"/>',
  partlyNight: '<path d="M11 6.5A4.5 4.5 0 1 1 5.5 2a3.6 3.6 0 0 0 5.5 4.5z"/><path d="M9 19.5h8.6a3.6 3.6 0 0 0 .4-7.1 5.2 5.2 0 0 0-9.7-1.3A3.9 3.9 0 0 0 9 19.5z"/>',
  rain: '<path d="M7 15.5h10.2a4.1 4.1 0 0 0 .4-8.2A6 6 0 0 0 6.1 6.6 4.5 4.5 0 0 0 7 15.5z"/><path d="M8 18.5l-1 2.5M12 18.5l-1 2.5M16 18.5l-1 2.5"/>',
  snow: '<path d="M7 15.5h10.2a4.1 4.1 0 0 0 .4-8.2A6 6 0 0 0 6.1 6.6 4.5 4.5 0 0 0 7 15.5z"/><path d="M8 19h.01M12 20.5h.01M16 19h.01M10 22h.01M14 22h.01"/>',
  storm: '<path d="M7 15.5h10.2a4.1 4.1 0 0 0 .4-8.2A6 6 0 0 0 6.1 6.6 4.5 4.5 0 0 0 7 15.5z"/><path d="M12.5 15l-2 3.5h3l-2 3.5"/>',
  fog: '<path d="M4 9h16M3 13h18M5 17h14"/>',
};

function wxInfo(code, day = true) {
  const c = Number(code);
  const map = [
    [[0], day ? 'sun' : 'moon', 'Ясно'], [[1], day ? 'partly' : 'partlyNight', 'Преимущественно ясно'],
    [[2], day ? 'partly' : 'partlyNight', 'Переменная облачность'], [[3], 'cloud', 'Пасмурно'],
    [[45, 48], 'fog', 'Туман'], [[51, 53, 55], 'rain', 'Морось'], [[56, 57], 'rain', 'Ледяная морось'],
    [[61], 'rain', 'Небольшой дождь'], [[63], 'rain', 'Дождь'], [[65], 'rain', 'Сильный дождь'], [[66, 67], 'rain', 'Ледяной дождь'],
    [[71], 'snow', 'Небольшой снег'], [[73], 'snow', 'Снег'], [[75], 'snow', 'Сильный снег'], [[77], 'snow', 'Снежная крупа'],
    [[80], 'rain', 'Ливень'], [[81], 'rain', 'Ливни'], [[82], 'rain', 'Сильный ливень'], [[85, 86], 'snow', 'Снегопад'],
    [[95], 'storm', 'Гроза'], [[96, 99], 'storm', 'Гроза с градом'],
  ];
  for (const [codes, icon, text] of map) if (codes.includes(c)) return { icon, text };
  return { icon: 'cloud', text: '' };
}
function wxIcon(code, day, cls = 'wx-ico') {
  return `<svg class="${cls}" viewBox="0 0 24 24">${WX_SVG[wxInfo(code, day).icon]}</svg>`;
}
const tempStr = (t) => (t > 0 ? '+' : '') + Math.round(t) + '°';
function dayName(date, i) {
  if (i === 0) return 'Сегодня';
  if (i === 1) return 'Завтра';
  return new Date(date + 'T12:00').toLocaleDateString('ru-RU', { weekday: 'short' });
}

async function loadWeather() {
  clearTimeout(WX.retry);
  if (!S.cfg.settings.city) { WX.data = null; renderWeatherChip(); return; }
  let d = null;
  try { d = await api.get('/api/weather'); } catch (e) {}
  // Не получилось (например, интернет ещё не подключился после включения) — пробуем снова чуть позже
  if (!d || !d.now) {
    WX.fails = (WX.fails || 0) + 1;
    WX.retry = setTimeout(loadWeather, Math.min(300000, 20000 * WX.fails));
    if (!d || (WX.data && WX.data.now)) return; // старый прогноз лучше, чем ошибка
  } else WX.fails = 0;
  WX.data = d;
  renderWeatherChip();
  if (SV.on) renderSaverInfo();
  if (S.screen === 'main' && S.tab === 'home' && !cur) setWelcomeHero();
}

function renderWeatherChip() {
  const b = $('weatherBtn');
  const d = WX.data;
  if (!S.cfg.settings.city) {
    b.innerHTML = `<svg class="wx-ico" viewBox="0 0 24 24">${WX_SVG.partly}</svg><span>Погода</span>`;
    b._info = { kicker: 'Погода', title: 'Выберите город', sub: 'OK — указать город, и погода появится здесь и на заставке', color: '#3b5bdb44' };
    b._act = () => openSettingsAt('city-q');
    return;
  }
  if (!d || !d.now) {
    b.innerHTML = `<svg class="wx-ico" viewBox="0 0 24 24">${WX_SVG.cloud}</svg><span>${esc(S.cfg.settings.city.name)}</span>`;
    b._info = { kicker: 'Погода', title: S.cfg.settings.city.name, sub: d && d.error ? d.error : 'Загружаю прогноз…' };
    b._act = showWeatherModal;
    return;
  }
  const info = wxInfo(d.now.code, d.now.day);
  b.innerHTML = `${wxIcon(d.now.code, d.now.day)}<span>${tempStr(d.now.temp)}</span>`;
  const t = d.days && d.days[1];
  b._info = {
    kicker: 'Погода · ' + d.city.name,
    title: `${tempStr(d.now.temp)}, ${info.text.toLowerCase()}`,
    sub: `Ощущается как ${tempStr(d.now.feels)} · ветер ${Math.round(d.now.wind)} м/с · влажность ${Math.round(d.now.hum)}%` +
      (t ? ` · завтра ${tempStr(t.max)} / ${tempStr(t.min)}` : '') + ' · OK — прогноз',
    color: d.now.day ? '#f59f0055' : '#3b5bdb55',
  };
  b._act = showWeatherModal;
  if (cur === b) setHero(b._info);
}

function forecastHTML(d, cls = 'fc') {
  return `<div class="${cls}">` + (d.days || []).slice(0, 5).map((x, i) =>
    `<div class="fc-day"><div class="fc-name">${esc(dayName(x.date, i))}</div>${wxIcon(x.code, true, 'fc-ico')}` +
    `<div class="fc-t"><b>${tempStr(x.max)}</b> <span>${tempStr(x.min)}</span></div></div>`).join('') + '</div>';
}

function showWeatherModal() {
  const d = WX.data;
  if (!d || !d.now) { openSettingsAt('city-q'); return; }
  const box = el('div', 'wx-modal');
  const info = wxInfo(d.now.code, d.now.day);
  box.innerHTML = `<div class="wx-big">${wxIcon(d.now.code, d.now.day, 'wx-big-ico')}<div><div class="wx-temp">${tempStr(d.now.temp)}</div>` +
    `<div class="wx-desc">${esc(info.text)} · ощущается как ${tempStr(d.now.feels)}</div>` +
    `<div class="wx-desc">Ветер ${Math.round(d.now.wind)} м/с · влажность ${Math.round(d.now.hum)}%</div></div></div>` + forecastHTML(d);
  modal(`Погода: ${d.city.name}${d.city.region ? ', ' + d.city.region : ''}`, [
    { label: 'Готово' },
    { label: 'Сменить город', act: () => openSettingsAt('city-q') },
  ], box);
}

function setWelcomeHero() {
  const d = WX.data;
  const h = new Date().getHours();
  const hello = h < 6 ? 'Доброй ночи' : h < 12 ? 'Доброе утро' : h < 18 ? 'Добрый день' : 'Добрый вечер';
  setHero({
    kicker: new Date().toLocaleDateString('ru-RU', { weekday: 'long', day: 'numeric', month: 'long' }),
    title: hello,
    sub: d && d.now ? `${d.city.name}: ${tempStr(d.now.temp)}, ${wxInfo(d.now.code, d.now.day).text.toLowerCase()}` : 'Стрелки — выбор, OK — открыть, Домой — на главный экран.',
  });
}

// ---------- Уведомления ----------

const NT = { list: [], unread: 0 };
const NT_ICON = { reminder: '🔔', phone: '📱', vpn: '🛡️', update: '⬆️', error: '⚠️', info: 'ℹ️' };

async function loadNotices() {
  try {
    const r = await api.get('/api/notices');
    NT.list = r.notices || [];
    NT.unread = r.unread || 0;
  } catch (e) { return; }
  renderBell();
}

function renderBell() {
  const b = $('bellBtn');
  b.innerHTML = '<svg viewBox="0 0 24 24"><path d="M6 9a6 6 0 0 1 12 0c0 5 2 6.5 2 6.5H4S6 14 6 9z"/><path d="M10 19a2 2 0 0 0 4 0"/></svg>' +
    (NT.unread ? `<span class="badge-n">${NT.unread > 9 ? '9+' : NT.unread}</span>` : '');
  b._info = {
    kicker: 'Уведомления',
    title: NT.unread ? `Новых: ${NT.unread}` : 'Нет новых уведомлений',
    sub: NT.list[0] ? `Последнее: ${NT.list[0].title}` : 'Здесь появятся напоминания о передачах и сообщения TVHub',
  };
  b._act = showNotices;
  if (cur === b) setHero(b._info);
}

function noticeAct(n) {
  if (n.action && n.action.type === 'update') { askInstallUpdate(); return; }
  if (n.action && n.action.type === 'play') {
    const ch = S.channels.find((c) => c.url === n.action.url);
    if (ch) { playChannel(ch, S.channels); return; }
    toast('Канал не найден в текущем плейлисте');
  }
}

function showNotices() {
  api.post('/api/notices/read', {}).catch(() => {});
  NT.unread = 0;
  renderBell();
  const box = el('div', 'nt-list');
  if (!NT.list.length) box.append(el('div', 'note', 'Уведомлений пока нет.'));
  for (const n of NT.list.slice(0, 12)) {
    const r = el('div', 'nt-item');
    r.innerHTML = `<span class="nt-ico">${NT_ICON[n.kind] || 'ℹ️'}</span><span class="nt-body"><span class="nt-title">${esc(n.title)}</span>` +
      `<span class="nt-sub">${n.text ? esc(n.text) + ' · ' : ''}${ago(n.at)}</span></span>`;
    if (n.action) {
      const b = btn('Смотреть', () => { closeModal(); noticeAct(n); }, 'nt:' + n.id, 'primary');
      r.append(b);
    }
    box.append(r);
  }
  const buttons = [{ label: 'Закрыть' }];
  if (NT.list.length) buttons.push({ label: 'Очистить всё', act: () => { api.post('/api/notices/read', { clear: true }); NT.list = []; renderBell(); renderHomeIfVisible(); } });
  modal('Уведомления', buttons, box);
  renderHomeIfVisible();
}

function renderHomeIfVisible() {
  if (S.screen === 'main' && S.tab === 'home' && $('modal').classList.contains('hidden')) keepFocus(renderHome);
}

let bannerTimer;
function showBanner(n) {
  const b = $('banner');
  b.innerHTML = `<span class="nt-ico">${NT_ICON[n.kind] || 'ℹ️'}</span><span class="nt-body"><span class="nt-title">${esc(n.title)}</span>` +
    `<span class="nt-sub">${n.text ? esc(n.text) : 'Все уведомления — значок колокольчика вверху'}</span></span>`;
  b.classList.remove('hidden');
  clearTimeout(bannerTimer);
  bannerTimer = setTimeout(() => b.classList.add('hidden'), 7000);
}

function onNoticeEvent(n) {
  NT.list.unshift(n);
  NT.unread++;
  renderBell();
  if (SV.on) hideSaver();
  if (n.kind === 'reminder' && n.action) {
    const watching = S.screen === 'player' && P.ch && P.ch.url === n.action.url;
    if (!watching && $('modal').classList.contains('hidden')) {
      modal(`${n.title} ${n.text || ''}`, [
        { label: 'Смотреть', cls: 'primary', act: () => noticeAct(n) },
        { label: 'Позже' },
      ]);
      return;
    }
  }
  showBanner(n);
  renderHomeIfVisible();
}

// Ряд «Уведомления» на главном экране, пока есть непрочитанные
function homeNoticeRow(box) {
  const fresh = NT.list.slice(0, NT.unread).slice(0, 6);
  if (!fresh.length) return;
  const items = fresh.map((n) => {
    const b = el('button', 'f card nt-card');
    b.dataset.k = 'nt:' + n.id;
    b.innerHTML = `<div class="nt-ico">${NT_ICON[n.kind] || 'ℹ️'}</div><div class="label">${esc(n.title)}</div><div class="nt-sub">${esc(n.text || ago(n.at))}</div>`;
    b._act = () => (n.action ? noticeAct(n) : showNotices());
    b._info = { kicker: 'Уведомление · ' + ago(n.at), title: n.title, sub: (n.text || '') + (n.action ? ' · OK — смотреть' : ' · OK — все уведомления') };
    return b;
  });
  box.append(row('Уведомления', items));
}

// ---------- Заставка ----------

const SV = { on: false, idleSince: Date.now(), walls: null, idx: 0, layer: 0, timers: [], scenes: [] };

function markActivity() { SV.idleSince = Date.now(); }
function saverMinutes() { const m = S.cfg && S.cfg.settings.saverMinutes || 0; return m < 0 ? 0 : (m || 10); }
function saverActive() { return SV.on; }

function canShowSaver() {
  if (!$('modal').classList.contains('hidden')) return false;
  if (S.screen === 'main') return true;
  if (S.screen === 'player') return video.paused || !!video.error;
  return false;
}

setInterval(() => {
  const m = saverMinutes();
  if (!SV.on && m && S.cfg && Date.now() - SV.idleSince > m * 60000 && canShowSaver()) showSaver();
}, 5000);

document.addEventListener('mousemove', (e) => {
  if (Math.abs(e.movementX) + Math.abs(e.movementY) < 3) return;
  markActivity();
  if (SV.on) hideSaver();
}, true);
document.addEventListener('mousedown', () => { markActivity(); if (SV.on) hideSaver(); }, true);

async function showSaver() {
  if (SV.on) return;
  SV.on = true;
  const sv = $('saver');
  sv.classList.remove('hidden');
  requestAnimationFrame(() => sv.classList.add('show'));
  renderSaverInfo();
  if (!WX.data || !WX.data.now) loadWeather();
  SV.cover = ''; SV.colors = null;
  saverRGB();
  SV.timers.push(setInterval(renderSaverInfo, 10000));
  SV.timers.push(setInterval(updateSaverMusic, 3000));
  updateSaverMusic();
}

// Фон заставки — переливающиеся пятна, как в музыке (та же скорость), в чёрном и синем.
function setSaverColors(c) {
  const bg = $('svBg');
  ['--mu1', '--mu2', '--mu3'].forEach((v, i) => bg.style.setProperty(v, c[i]));
}
function saverRGB() {
  setSaverColors(['#1d3fbf', '#03050c', '#0e2a8a']);
}

function renderSaverInfo() {
  const now = new Date();
  $('svClock').textContent = now.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' });
  const ds = now.toLocaleDateString('ru-RU', { weekday: 'long', day: 'numeric', month: 'long' });
  $('svDate').textContent = ds.charAt(0).toUpperCase() + ds.slice(1);
  const d = WX.data, box = $('svWeather');
  if (d && d.now) {
    const info = wxInfo(d.now.code, d.now.day);
    box.innerHTML = `<div class="sv-wnow">${wxIcon(d.now.code, d.now.day, 'sv-wico')}<div><div class="sv-wtemp">${tempStr(d.now.temp)}</div>` +
      `<div class="sv-wdesc">${esc(info.text)} · ${esc(d.city.name)}</div></div></div>` + forecastHTML(d, 'fc sv-fc');
  } else box.innerHTML = !S.cfg.settings.city ? '<div class="sv-wdesc">Выберите город в настройках, чтобы здесь была погода</div>'
    : `<div class="sv-wdesc">${esc(S.cfg.settings.city.name)}: ${d && d.error ? 'погода пока не загрузилась, пробую ещё' : 'загружаю погоду…'}</div>` +
      (d && d.error ? `<div class="sv-werr">${esc(d.error.replace(/^Не удалось получить погоду: /, ''))}</div>` : '');
  // Чуть сдвигаем блок, чтобы экран не выгорал
  const m = now.getMinutes();
  $('svInfo').style.transform = `translate(${(m % 5) * 0.6}rem, ${-(m % 4) * 0.5}rem)`;
}

async function updateSaverMusic() {
  if (!SV.on) return;
  let r;
  try { r = await api.get('/api/media'); } catch (e) { return; }
  const m = r.media, box = $('svMusic');
  if (!SV.on) return;
  if (m && m.title && m.status === 'Playing') {
    const u = coverURL(m);
    const key = m.title + '|' + (m.artist || '') + '|' + u;
    if (box.dataset.k !== key) {
      box.dataset.k = key;
      box.innerHTML = (u ? `<img src="${esc(u)}" alt="">` : '<span class="sv-note">♪</span>') +
        `<div class="sv-mtext"><div class="sv-mk">Сейчас играет</div><div class="sv-mt">${esc(m.title)}</div><div class="sv-ma">${esc(m.artist || '')}</div></div>`;
    }
    box.classList.remove('hidden');
    SV.cover = u;
  } else {
    box.classList.add('hidden');
    box.dataset.k = '';
    SV.cover = '';
  }
}

function hideSaver() {
  if (!SV.on) return;
  SV.on = false;
  SV.timers.forEach(clearInterval);
  SV.timers = [];
  const sv = $('saver');
  sv.classList.remove('show');
  setTimeout(() => { if (!SV.on) sv.classList.add('hidden'); }, 500);
  markActivity();
}

// ---------- Музыка ----------

const MU = { st: null, timer: null, at: 0 };

function musicApp() { return S.cfg.apps.find((a) => a.id === 'yamusic') || { id: 'yamusic', name: 'Яндекс Музыка', type: 'music', target: 'https://music.yandex.ru' }; }

function renderMusic() {
  const p = $('panel-music');
  p.innerHTML = '';
  const wrap = el('div', 'mu-wrap');
  wrap.innerHTML = `<div class="mu-art" id="muArt"><i class="mu-glow" id="muGlow"></i><div class="mu-door" id="muDoor"><div class="mu-cover" id="muCover"><span class="mu-note">♪</span></div></div></div>
    <div class="mu-info">
      <div class="mu-kicker" id="muKicker">Музыка</div>
      <div class="mu-title" id="muTitle">Сейчас ничего не играет</div>
      <div class="mu-artist" id="muArtist">Откройте Яндекс Музыку и включите «Мою волну» или любимый плейлист</div>
      <div class="mu-prog hidden" id="muProg"><span id="muPos">0:00</span><div class="pbar f mu-seek" id="muSeek" data-k="mu:seek"><i id="muBar"></i></div><span id="muDur">0:00</span></div>
      <div class="mu-ctrls" id="muCtrls"></div>
      <div class="mu-actions" id="muActions"></div>
      <p class="mu-hint">Кнопки управляют тем, что сейчас играет на компьютере: Яндекс Музыкой, YouTube или любым плеером. Музыка играет и дальше, когда вы возвращаетесь в TVHub кнопкой «Домой».</p>
    </div>`;
  p.append(wrap);
  const ctrls = wrap.querySelector('#muCtrls');
  const mk = (k, svg, label, act) => {
    const b = el('button', 'f mu-btn', `<svg viewBox="0 0 24 24">${svg}</svg>`);
    b.dataset.k = 'mu:' + k;
    b.title = label;
    b._act = act;
    b._info = { kicker: 'Музыка', title: label, sub: '' };
    ctrls.append(b);
    return b;
  };
  mk('prev', '<path d="M6 5v14"/><path d="M19 5l-10 7 10 7z"/>', 'Предыдущий трек', () => mediaCmd('prev'));
  mk('play', '<path d="M7 5l12 7-12 7z" id="muPlayIco"/>', 'Пауза / продолжить', () => mediaCmd('playpause')).classList.add('mu-play');
  mk('next', '<path d="M18 5v14"/><path d="M5 5l10 7-10 7z"/>', 'Следующий трек', () => mediaCmd('next'));
  const like = mk('like', '<path d="M12 20.5l-1.3-1.2C5.6 14.7 2.5 11.9 2.5 8.4 2.5 5.6 4.7 3.5 7.4 3.5c1.6 0 3.4.8 4.6 2.2 1.2-1.4 3-2.2 4.6-2.2 2.7 0 4.9 2.1 4.9 4.9 0 3.5-3.1 6.3-8.2 10.9z"/>', 'Мне нравится', toggleLike);
  like.id = 'muLike';
  mk('vdown', '<path d="M4 9h4l5-4v14l-5-4H4z"/><path d="M17 12h4"/>', 'Тише', () => mediaCmd('voldown'));
  mk('vup', '<path d="M4 9h4l5-4v14l-5-4H4z"/><path d="M17 12h4M19 10v4"/>', 'Громче', () => mediaCmd('volup'));
  // Перемотка как в Яндекс Музыке: на полосе прогресса ← → — на 10 секунд, если держать — быстрее
  const seek = wrap.querySelector('#muSeek');
  seek._lr = (dir) => musicScrub(dir === 'right' ? 1 : -1);
  seek._act = () => musicScrubApply();
  seek._info = { kicker: 'Музыка', title: 'Перемотка', sub: '← → — перемотать на 10 секунд. Удерживайте кнопку, чтобы перематывать быстрее' };
  const acts = wrap.querySelector('#muActions');
  const open = btn('Открыть Яндекс Музыку', () => launch(musicApp()), 'mu:open', 'primary');
  open._info = { kicker: 'Музыка', title: 'Яндекс Музыка', sub: 'Откроется приложение Яндекс Музыки или сайт music.yandex.ru. Вернуться — «Домой», музыка продолжит играть.' };
  acts.append(open);
  updateMusic();
  clearInterval(MU.timer);
  MU.timer = setInterval(() => {
    if (S.screen === 'main' && S.tab === 'music') updateMusic();
    else clearInterval(MU.timer);
  }, 1000);
  if (!MU.tick) MU.tick = setInterval(tickMusicProgress, 500);
}

async function mediaCmd(action, extra) {
  let r = null;
  if (action === 'next' || action === 'prev') MU.dir = action === 'prev' ? -1 : 1;
  try {
    r = await api.post('/api/media', { action, ...extra });
    if (r.error) toast(r.error, 5000);
    else if (r.info) toast(r.info, 6000);
  } catch (e) { toast('Ошибка: ' + e.message); }
  setTimeout(updateMusic, 400);
  return r && r.ok ? r : null;
}

// Перемотка: нажатия копятся, а команда уходит, когда кнопку отпустили
function musicScrub(dir) {
  const m = MU.st;
  if (!m || !m.dur) return;
  const now = Date.now();
  const sc = MU.scrub && now - MU.scrub.last < 1500 ? MU.scrub : { pos: curMusicPos(), n: 0 };
  sc.n = now - (sc.last || 0) < 350 ? sc.n + 1 : 0;
  sc.last = now;
  const step = sc.n < 5 ? 10 : sc.n < 15 ? 20 : 40;
  sc.pos = Math.max(0, Math.min(m.dur - 1, sc.pos + dir * step));
  MU.scrub = sc;
  clearTimeout(MU.scrubTimer);
  MU.scrubTimer = setTimeout(musicScrubApply, 600);
  tickMusicProgress();
}

async function musicScrubApply() {
  clearTimeout(MU.scrubTimer);
  const sc = MU.scrub;
  if (!sc) return;
  const r = await mediaCmd('seek', { pos: sc.pos });
  if (MU.scrub === sc) {
    if (r && MU.st) { MU.st.pos = sc.pos; MU.at = Date.now(); }
    MU.scrub = null;
  }
  tickMusicProgress();
}

async function toggleLike() {
  const r = await mediaCmd('like');
  if (!r) return;
  if (MU.st) MU.st.liked = r.liked;
  showLike(MU.st);
  toast(r.liked ? 'Добавлено в «Мне нравится»' : 'Убрано из «Мне нравится»', 3000);
}

function showLike(m) {
  const b = $('muLike');
  if (!b) return;
  const known = !!m && typeof m.liked === 'boolean';
  b.classList.toggle('liked', known && m.liked);
  b.classList.toggle('nolike', !known);
  b._info.title = known && m.liked ? 'В «Мне нравится» · OK — убрать' : 'Мне нравится';
  b._info.sub = known ? 'Трек добавится в «Мне нравится» в Яндекс Музыке' : 'Работает с Яндекс Музыкой, открытой из TVHub';
  if (cur === b) setHero(b._info);
}

function curMusicPos() {
  const m = MU.st;
  if (!m) return 0;
  return Math.min(m.dur || 0, m.pos + (m.status === 'Playing' ? (Date.now() - MU.at) / 1000 : 0));
}

async function updateMusic() {
  let r;
  try { r = await api.get('/api/media'); } catch (e) { return; }
  MU.st = r.media; MU.at = Date.now();
  const m = r.media;
  showLike(m && m.title ? m : null);
  const title = $('muTitle');
  if (!title) return;
  const playing = m && m.status === 'Playing';
  if (m && m.ok && m.title) {
    const src = /yandex/i.test(m.app || '') ? 'Яндекс Музыка' : /edge/i.test(m.app || '') ? 'Microsoft Edge' : /chrome/i.test(m.app || '') ? 'Google Chrome' : (m.app || '').split('!')[0].split('\\').pop().replace(/\.exe$/i, '');
    $('muKicker').textContent = (playing ? 'Сейчас играет' : 'На паузе') + (src ? ' · ' + src : '');
    // Название меняем вместе с обложкой: сначала она загружается, потом меняется всё сразу
    const text = () => {
      title.textContent = m.title;
      $('muArtist').textContent = [m.artist, m.album && m.album !== m.title ? m.album : ''].filter(Boolean).join(' — ');
    };
    showCover(m, text);
  } else {
    $('muKicker').textContent = r.available === false ? 'Музыка' : 'Сейчас ничего не играет';
    title.textContent = r.available === false ? 'Не получается узнать, что играет' : 'Включите музыку';
    $('muArtist').textContent = r.startInfo || 'Нажмите «плей» — TVHub откроет Яндекс Музыку и включит «Мою волну»';
  }
  const ico = document.getElementById('muPlayIco');
  if (ico) ico.setAttribute('d', playing ? 'M8 5v14M16 5v14' : 'M7 5l12 7-12 7z');
  // Играет — обложка выезжает вперёд, пауза — возвращается на место
  const art = $('muArt');
  if (art) art.classList.toggle('playing', !!(playing && m.title));
  tickMusicProgress();
}

// Обложка трека. Поворот «дверью» — ровно один на смену трека: Windows присылает настоящую
// обложку чуть позже названия (а сначала — запасную или никакую), поэтому ждём её и только
// потом поворачиваем. Другая картинка для того же трека меняется без поворота.
function showCover(m, text) {
  const cover = $('muCover');
  if (!cover) return;
  const key = m.title + '|' + (m.artist || '');
  const u = coverURL(m);
  if (cover.dataset.key === key) {
    if (!MU.flipKey) text();
    if (u && u !== cover.dataset.u && MU.quietU !== u) {
      MU.quietU = u;
      loadCover(u).then((x) => {
        if (cover.dataset.key !== key || MU.quietU !== u) return;
        MU.quietU = '';
        cover.dataset.u = u;
        if (x.im) { cover.innerHTML = ''; cover.append(x.im); }
        setRing(x.cols);
        setMusicColors(x.cols);
      });
    }
    return;
  }
  if (MU.flipKey === key) return; // уже поворачиваем к этому треку
  if (MU.wantKey !== key) { MU.wantKey = key; MU.wantSince = Date.now(); }
  const wait = Date.now() - MU.wantSince;
  const real = m.coverVer || (m.coverUrl && !/mzstatic\.com/.test(m.coverUrl)); // не запасная обложка из каталога
  const first = !cover.dataset.key; // экран только открыли — показываем сразу и без поворота
  if (!first && !(u && real) && wait < (u ? 1500 : 2500)) return; // ждём настоящую обложку, пока видно прошлый трек
  MU.flipKey = key;
  loadCover(u).then(async (x) => {
    if (MU.wantKey !== key || !cover.isConnected) { MU.flipKey = ''; return; } // трек успел смениться
    const apply = () => {
      cover.dataset.key = key;
      cover.dataset.u = u;
      if (x.im) { cover.innerHTML = ''; cover.append(x.im); } else cover.innerHTML = '<span class="mu-note">♪</span>';
      text();
      setRing(x.cols);
    };
    setMusicColors(x.cols);
    if (first) apply();
    else await flipCover(apply);
    MU.flipKey = '';
  });
}

// loadCover загружает картинку и её цвета (недолго ждём, чтобы не задерживать поворот).
function loadCover(u) {
  if (!u) return Promise.resolve({ im: null, cols: null });
  return new Promise((res) => {
    const im = new Image();
    let done = false;
    const fin = async () => {
      if (done) return;
      done = true;
      const ok = im.complete && im.naturalWidth > 0;
      const cols = ok ? await Promise.race([coverColors(u), new Promise((r) => setTimeout(() => r(null), 1500))]) : null;
      res({ im: ok ? im : null, cols });
    };
    im.onload = fin;
    im.onerror = fin;
    setTimeout(fin, 3000);
    im.src = u;
  });
}

// Обложка: из Windows (coverVer) или с сайта Яндекс Музыки (через свой сервер — тогда можно взять цвета)
function coverURL(m) {
  if (!m) return '';
  if (m.coverVer) return `/api/media/cover?t=${encodeURIComponent(TOKEN)}&v=${m.coverVer}`;
  if (m.coverUrl) return '/p?' + new URLSearchParams({ t: TOKEN, u: m.coverUrl.replace(/^\/\//, 'https://') });
  return '';
}

// Главные цвета обложки — те, что занимают на ней больше всего места. Цвета не придумываем:
// оттенки остаются как на обложке, только слишком тёмные и слишком светлые подтягиваем,
// чтобы фон было видно и текст на нём читался.
function rgbToHsl(r, g, b) {
  r /= 255; g /= 255; b /= 255;
  const mx = Math.max(r, g, b), mn = Math.min(r, g, b), l = (mx + mn) / 2;
  if (mx === mn) return [0, 0, l];
  const d = mx - mn, s = l > 0.5 ? d / (2 - mx - mn) : d / (mx + mn);
  let h = mx === r ? (g - b) / d + (g < b ? 6 : 0) : mx === g ? (b - r) / d + 2 : (r - g) / d + 4;
  return [h * 60, s, l];
}
const hsl = (h, s, l) => `hsl(${Math.round(((h % 360) + 360) % 360)}, ${Math.round(s * 100)}%, ${Math.round(l * 100)}%)`;

function paletteFromPixels(d) {
  // Группы похожих цветов: серые — по яркости, цветные — по оттенку и яркости.
  // Цвет группы — среднее настоящих пикселей (а не усреднённый оттенок).
  const bins = new Map();
  for (let i = 0; i < d.length; i += 4) {
    if (d[i + 3] < 128) continue;
    const r = d[i], g = d[i + 1], b = d[i + 2];
    const [h, s, l] = rgbToHsl(r, g, b);
    const grey = s < 0.15 || l < 0.08 || l > 0.94;
    const key = grey ? 'g' + Math.min(4, Math.floor(l * 5)) : Math.floor(h / 20) + ':' + Math.min(2, Math.floor(l * 3));
    // Площадь важнее всего; цветным пикселям — небольшая прибавка, чтобы яркий акцент
    // на большой серой обложке тоже попал в палитру, но не перекрасил весь фон
    const w = grey ? 1 : 1 + s * 0.8;
    const e = bins.get(key) || { w: 0, n: 0, r: 0, g: 0, b: 0, grey };
    e.w += w; e.n++; e.r += r; e.g += g; e.b += b;
    bins.set(key, e);
  }
  if (!bins.size) return null;
  const list = [...bins.values()].map((e) => ({ w: e.w, grey: e.grey, r: e.r / e.n, g: e.g / e.n, b: e.b / e.n })).sort((a, b) => b.w - a.w);
  const total = list.reduce((a, c) => a + c.w, 0);
  const dist = (a, b) => Math.hypot(a.r - b.r, (a.g - b.g) * 1.2, a.b - b.b);
  // До трёх заметно разных цветов; совсем мелкие пятна (меньше 3% обложки) не берём
  const picked = [];
  for (const c of list) {
    if (picked.length && c.w < total * 0.03) break;
    if (picked.every((p) => dist(p, c) > 55)) picked.push(c);
    if (picked.length === 3) break;
  }
  const cols = picked.map((c) => {
    const [h, s, l] = rgbToHsl(c.r, c.g, c.b);
    return { h, s, l };
  });
  // Обложка почти одного цвета — берём тот же оттенок светлее и темнее, а не соседние оттенки
  const base = cols[0];
  for (let i = 0; cols.length < 3; i++) cols.push({ h: base.h, s: base.s, l: base.l + (i ? -0.14 : 0.14) });
  return cols.map((c) => {
    const s = c.s < 0.12 ? c.s : Math.min(0.85, c.s * 1.1);
    return hsl(c.h, s, Math.min(0.6, Math.max(0.2, c.l)));
  });
}

const coverPalettes = new Map();
function coverColors(src) {
  if (coverPalettes.has(src)) return Promise.resolve(coverPalettes.get(src));
  return new Promise((resolve) => {
    const img = new Image();
    img.onload = () => {
      try {
        const c = document.createElement('canvas');
        c.width = c.height = 64;
        const g = c.getContext('2d', { willReadFrequently: true });
        g.drawImage(img, 0, 0, 64, 64);
        const p = paletteFromPixels(g.getImageData(0, 0, 64, 64).data);
        if (coverPalettes.size > 50) coverPalettes.clear();
        coverPalettes.set(src, p);
        resolve(p);
      } catch (e) { resolve(null); }
    };
    img.onerror = () => resolve(null);
    img.src = src;
  });
}

const MU_DEF = ['#fc3f1d', '#8b5cf6', '#3b5bdb'];

// HEX-цвет: #F36, F36, #FF3366 → #FF3366 (или '' — не цвет)
function normHex(s) {
  const v = String(s || '').trim().replace(/^#/, '');
  if (/^[0-9a-f]{3}$/i.test(v)) return '#' + v.split('').map((x) => x + x).join('').toUpperCase();
  if (/^[0-9a-f]{6}$/i.test(v)) return '#' + v.toUpperCase();
  return '';
}

// Свои цвета из настроек (до трёх) — вместо цветов обложки. Одного-двух цветов мало
// для переливов, поэтому добавляем их оттенки.
function userMusicColors() {
  const c = ((S.cfg && S.cfg.settings.musicColors) || []).map(normHex).filter(Boolean).slice(0, 3);
  if (!c.length) return null;
  if (c.length === 1) return [c[0], `color-mix(in srgb, ${c[0]} 55%, #000)`, `color-mix(in srgb, ${c[0]} 65%, #fff)`];
  if (c.length === 2) return [c[0], c[1], `color-mix(in srgb, ${c[0]} 50%, ${c[1]})`];
  return c;
}

function setMusicColors(cols) {
  const root = document.documentElement.style;
  const c = userMusicColors() || cols || MU_DEF;
  root.setProperty('--mu1', c[0]); root.setProperty('--mu2', c[1]); root.setProperty('--mu3', c[2]);
}

// Обводка обложки — в цветах обложки
function setRing(cols) {
  const d = $('muArt');
  if (!d) return;
  const c = userMusicColors() || cols || MU_DEF;
  d.style.setProperty('--r1', c[0]); d.style.setProperty('--r2', c[1]); d.style.setProperty('--r3', c[2]);
}

// Обложка поворачивается, как дверь вокруг середины: ребром к нам — меняем картинку и обводку
// и доворачиваем. «Следующий» крутит в одну сторону, «предыдущий» — в другую.
async function flipCover(apply) {
  const d = $('muDoor');
  if (!d || !MOTION || !d.animate) { apply(); return; }
  const s = MU.dir === -1 ? -1 : 1;
  MU.dir = 0;
  MU.flipping = true;
  let applied = false;
  const once = () => { if (!applied) { applied = true; apply(); } };
  try {
    await d.animate([
      { transform: 'perspective(140rem) rotateY(0deg)', filter: 'brightness(1)' },
      { transform: `perspective(140rem) rotateY(${-90 * s}deg)`, filter: 'brightness(.55)' },
    ], { duration: 300, easing: 'cubic-bezier(.55,0,.9,.45)', fill: 'forwards' }).finished;
    once();
    await d.animate([
      { transform: `perspective(140rem) rotateY(${90 * s}deg)`, filter: 'brightness(.55)' },
      { transform: `perspective(140rem) rotateY(${-6 * s}deg)`, filter: 'brightness(1.05)', offset: 0.75 },
      { transform: 'perspective(140rem) rotateY(0deg)', filter: 'brightness(1)' },
    ], { duration: 520, easing: 'cubic-bezier(.2,.75,.3,1)' }).finished;
  } catch (e) {
    once();
  } finally {
    d.getAnimations().forEach((a) => { if (!(a instanceof CSSAnimation)) a.cancel(); }); // вращение обводки не трогаем
    MU.flipping = false;
  }
}

// Переливающийся фон в цветах обложки — только на вкладке «Музыка»
function musicBackdrop(on) {
  $('muBg').classList.toggle('on', on);
  $('main').classList.toggle('music-on', on);
  if (on) { $('backdropImg').style.backgroundImage = ''; $('backdropImg').textContent = ''; }
}

function tickMusicProgress() {
  const m = MU.st, box = $('muProg');
  if (!box) return;
  if (!m || !m.dur || m.dur < 1) { box.classList.add('hidden'); return; }
  const pos = MU.scrub ? MU.scrub.pos : curMusicPos();
  box.classList.remove('hidden');
  box.classList.toggle('scrub', !!MU.scrub);
  $('muPos').textContent = fmtDur(pos);
  $('muDur').textContent = fmtDur(m.dur);
  $('muBar').style.width = ((pos / m.dur) * 100).toFixed(1) + '%';
}

// ---------- Дополнительные разделы настроек ----------

// «Цвета музыки»: до трёх своих цветов (HEX) для обводки обложки, свечения и фона
const MC_PRESETS = [
  ['Неон', ['#FF2D95', '#7B2FFF', '#00E5FF']],
  ['Закат', ['#FF5E3A', '#FF2A68', '#FFB347']],
  ['Океан', ['#0066FF', '#00C2FF', '#00FFC6']],
  ['Лес', ['#1DB954', '#0B6623', '#A8E063']],
];
function musicColorsSection(inner) {
  const c = S.cfg;
  const sec = el('div', 's-section');
  sec.append(el('h2', '', 'Цвета музыки'));
  sec.append(el('p', 'note', 'Обводка обложки, свечение и фон на экране музыки переливаются цветами обложки трека. Можно задать свои: до трёх цветов в формате HEX, например <b>#FF3366</b>. Пустые поля не учитываются. Удобно вводить с пульта на телефоне (вкладка «Текст»).'));
  const cur = (c.settings.musicColors || []).map(normHex);
  const f = el('div', 'form');
  const fields = [0, 1, 2].map((i) => {
    const fl = field('Цвет ' + (i + 1), 'mc-' + i, i === 0 ? '#FF3366' : 'необязательно', cur[i] || '');
    f.append(fl);
    return fl.querySelector('input');
  });
  sec.append(f);
  // Как это будет выглядеть
  const prev = el('div', 'mc-preview');
  const paint = () => {
    const v = fields.map((i) => normHex(i.value)).filter(Boolean);
    const cols = v.length === 1 ? [v[0], `color-mix(in srgb, ${v[0]} 55%, #000)`, `color-mix(in srgb, ${v[0]} 65%, #fff)`]
      : v.length === 2 ? [v[0], v[1], `color-mix(in srgb, ${v[0]} 50%, ${v[1]})`] : v.length ? v : null;
    prev.style.setProperty('--p1', cols ? cols[0] : '#555');
    prev.style.setProperty('--p2', cols ? cols[1] : '#777');
    prev.style.setProperty('--p3', cols ? cols[2] : '#999');
    prev.textContent = cols ? '' : 'Сейчас — цвета обложки трека';
    fields.forEach((i) => i.classList.toggle('bad', !!i.value.trim() && !normHex(i.value)));
  };
  fields.forEach((i) => i.addEventListener('input', paint));
  paint();
  sec.append(prev);
  const row = el('div', 'seg mc-row');
  row.append(btn('Сохранить', async () => {
    const bad = fields.find((i) => i.value.trim() && !normHex(i.value));
    if (bad) { toast(`«${bad.value.trim()}» — не HEX-цвет. Пример: #FF3366`, 5000); return; }
    c.settings.musicColors = fields.map((i) => normHex(i.value)).filter(Boolean);
    await saveCfg();
    toast(c.settings.musicColors.length ? 'Цвета музыки сохранены' : 'Снова цвета обложки', 3000);
    keepFocus(renderSettings);
  }, 'mc-save', 'primary'));
  row.append(btn('Цвета обложки', async () => {
    c.settings.musicColors = [];
    await saveCfg();
    toast('Снова цвета обложки трека', 3000);
    keepFocus(renderSettings);
  }, 'mc-reset'));
  for (const [name, cols] of MC_PRESETS) {
    const b = btn(name, () => { cols.forEach((v, i) => { fields[i].value = v; }); paint(); toast(`«${name}»: нажмите «Сохранить»`, 2500); }, 'mc-p-' + name);
    b.style.setProperty('--p1', cols[0]); b.style.setProperty('--p2', cols[1]); b.style.setProperty('--p3', cols[2]);
    b.classList.add('mc-chip');
    row.append(b);
  }
  sec.append(row);
  inner.append(sec);
}

function renderExtraSettings(inner) {
  const c = S.cfg;

  // Телеканалы и телепрограмма
  const tv = el('div', 's-section');
  tv.append(el('h2', '', 'Телеканалы и телепрограмма'));
  tv.append(toggleRow('Русские названия каналов', !c.settings.latinNames, async () => {
    c.settings.latinNames = !c.settings.latinNames; await saveCfg(); keepFocus(renderSettings); loadChannels(true);
  }, 'set-runames'));
  const st = EPG.status;
  let line = 'Телепрограмма загружается…';
  if (st) {
    if (st.programmes) line = `Телепрограмма загружена: ${st.programmes} передач, найдено для ${st.matched} каналов из ${S.channels.length}` + (st.updated ? ` · обновлено ${ago(st.updated)}` : '') + `<br><span class="src">Источник: ${esc(st.source)}</span>`;
    else if (st.loading) line = 'Загружаю телепрограмму…';
    else if (st.error) line = 'Телепрограмма не загрузилась: ' + esc(st.error);
  }
  tv.append(el('p', 'note', line + '<br>Если в плейлисте от провайдера есть своя телепрограмма, TVHub возьмёт её сам. Можно указать и свою ссылку на XMLTV (.xml или .xml.gz).'));
  const f = el('div', 'form');
  const ef = field('Ссылка на телепрограмму (необязательно)', 'epg-url', 'Автоматически', c.settings.epgUrl || '');
  ef.style.flex = '3';
  f.append(ef);
  f.append(btn('Сохранить', async () => {
    c.settings.epgUrl = $('epg-url').value.trim();
    await saveCfg();
    toast('Загружаю телепрограмму…');
    setTimeout(() => keepFocus(renderSettings), 300);
  }, 'epg-save', 'primary'));
  f.append(btn('Обновить', async () => { await api.post('/api/epg/reload', {}); toast('Загружаю телепрограмму, это может занять минуту'); }, 'epg-reload'));
  tv.append(f);
  inner.append(tv);

  // Погода
  const wx = el('div', 's-section');
  wx.append(el('h2', '', 'Погода'));
  wx.append(el('p', 'note', c.settings.city
    ? `Город: <b>${esc(c.settings.city.name)}</b>${c.settings.city.region ? ', ' + esc(c.settings.city.region) : ''}. Погода показывается вверху главного экрана и на заставке.`
    : 'Выберите город: погода появится вверху главного экрана и на заставке.'));
  const wf = el('div', 'form');
  const cf = field('Город', 'city-q', 'Например, Москва');
  cf.style.flex = '3';
  wf.append(cf);
  const results = el('div', 'city-results');
  wf.append(btn('Найти', async () => {
    const q = $('city-q').value.trim();
    if (!q) { toast('Напишите название города'); return; }
    results.innerHTML = '<div class="note">Ищу…</div>';
    let r;
    try { r = await api.get('/api/weather/search?q=' + encodeURIComponent(q)); } catch (e) { results.innerHTML = '<div class="note">Ошибка поиска</div>'; return; }
    results.innerHTML = '';
    if (r.error) results.append(el('div', 'note', esc(r.error)));
    else if (!r.results.length) results.append(el('div', 'note', 'Ничего не найдено'));
    r.results.forEach((city, i) => {
      const b = btn(`${city.name}${city.region ? ', ' + city.region : ''}${city.country ? ' (' + city.country + ')' : ''}`, async () => {
        c.settings.city = city; if (await saveCfg()) toast(`Город: ${city.name}`); loadWeather(); keepFocus(renderSettings);
      }, 'city:' + i);
      results.append(b);
    });
    focusFirst(results);
  }, 'city-find', 'primary'));
  wx.append(wf);
  wx.append(results);
  inner.append(wx);

  musicColorsSection(inner);

  // Заставка
  const sv = el('div', 's-section');
  sv.append(el('h2', '', 'Заставка'));
  sv.append(el('p', 'note', 'Если долго ничего не нажимать, появятся часы, погода и красивая тёмная картинка дня. Любая кнопка возвращает обратно.'));
  const seg = el('div', 'seg');
  const curM = c.settings.saverMinutes || 0;
  for (const [v, l] of [[-1, 'Выкл'], [1, '1 мин'], [5, '5 мин'], [10, '10 мин'], [15, '15 мин'], [30, '30 мин']]) {
    const sel = v === curM || (v === 10 && curM === 0);
    seg.append(btn(l, async () => { c.settings.saverMinutes = v; await saveCfg(); keepFocus(renderSettings); }, 'saver:' + v, sel ? 'sel' : ''));
  }
  const sr = el('div', 's-row');
  sr.style.background = 'transparent'; sr.style.padding = '.2rem 0';
  sr.append(seg);
  sr.append(btn('Показать сейчас', () => setTimeout(showSaver, 200), 'saver-now'));
  sv.append(sr);
  inner.append(sv);

  eqSection(inner);
  insideSection(inner);
  themeSection(inner);

  // Режим администратора
  const ad = el('div', 's-section');
  ad.append(el('h2', '', 'Режим администратора'));
  ad.append(el('p', 'note', 'Нужен, если пульт с телефона не работает в программах, запущенных от имени администратора (например, в Happ). ' +
    'TVHub перезапустится с правами администратора: Windows один раз спросит подтверждение. При автозапуске подтверждение не понадобится.'));
  ad.append(toggleRow('Запускать TVHub с правами администратора', !!c.settings.runAsAdmin, () => {
    const on = !c.settings.runAsAdmin;
    modal(on ? 'TVHub перезапустится с правами администратора. Windows покажет окно подтверждения: нажмите «Да» мышью (пульт в этом окне не работает).' : 'TVHub перезапустится с обычными правами.', [
      { label: on ? 'Перезапустить' : 'Выключить', cls: 'primary', act: async () => {
        const r = await api.post('/api/system', { action: on ? 'admin-on' : 'admin-off' }).catch((e) => ({ ok: false, error: e.message }));
        if (r && !r.ok) toast(r.error || 'Не получилось', 6000);
        else { c.settings.runAsAdmin = on; keepFocus(renderSettings); }
      } },
      { label: 'Отмена' },
    ]);
  }, 'set-admin'));
  inner.append(ad);
}

// ---------- Звук: эквалайзер (Equalizer APO) ----------

const EQS = { d: null, loading: false };

function eqSection(inner) {
  const sec = el('div', 's-section');
  sec.append(el('h2', '', 'Звук: эквалайзер'));
  inner.append(sec);
  const d = EQS.d;
  if (!d) {
    sec.append(el('p', 'note', 'Загружаю…'));
    if (!EQS.loading) {
      EQS.loading = true;
      api.get('/api/eq').then((r) => { EQS.d = r; }).catch(() => { EQS.d = { error: 'Не удалось узнать настройки звука' }; })
        .finally(() => { EQS.loading = false; if (S.tab === 'settings') keepFocus(renderSettings); });
    }
    return;
  }
  if (d.error && !d.eq) { sec.append(el('p', 'note', esc(d.error))); return; }
  const e = d.eq, inst = d.status && d.status.installed;
  const apply = async (body) => {
    try {
      const r = await api.post('/api/eq', body);
      EQS.d = r;
      if (r.error) toast(r.error, 6000);
    } catch (err) { toast('Не получилось: ' + err.message); }
    keepFocus(renderSettings);
  };
  if (!inst) {
    sec.append(el('p', 'note', 'В Windows нет общего эквалайзера, поэтому TVHub управляет бесплатной программой <b>Equalizer APO</b>. ' +
      'Нажмите «Установить», в установщике выберите «Да» и «Далее», в конце отметьте устройство, через которое звук идёт на телевизор (HDMI), и перезагрузите компьютер.'));
    const r = el('div', 's-row');
    r.style.background = 'transparent'; r.style.padding = '.2rem 0';
    r.append(btn('Установить Equalizer APO', async () => {
      const x = await api.post('/api/eq', { download: true }).catch((err) => ({ ok: false, error: err.message }));
      if (!x.ok) toast(x.error || 'Не получилось', 6000);
      setTimeout(() => { EQS.d = null; if (S.tab === 'settings') keepFocus(renderSettings); }, 20000);
    }, 'eq-install', 'primary'));
    r.append(btn('Проверить ещё раз', () => { EQS.d = null; keepFocus(renderSettings); }, 'eq-recheck'));
    sec.append(r);
    return;
  }
  const devWarn = d.status && d.status.active === false;
  if (devWarn) {
    sec.append(el('p', 'note', `<b style="color:#ffb4a8">Эквалайзер сейчас не действует:</b> Equalizer APO не подключён к устройству «${esc(d.status.device || 'звук по умолчанию')}», через которое идёт звук. ` +
      'Нажмите «Открыть Configurator», Windows спросит разрешение — нажмите «Да». В списке отметьте это устройство (обычно HDMI или название телевизора), нажмите «OK» и перезагрузите компьютер.'));
    const r0 = el('div', 's-row');
    r0.style.background = 'transparent'; r0.style.padding = '.2rem 0';
    r0.append(btn('Открыть Configurator', async () => {
      const x = await api.post('/api/eq', { configurator: true }).catch((e) => ({ ok: false, error: e.message }));
      toast(x.ok ? 'Configurator открыт — отметьте устройство и нажмите OK. Управлять им удобно тачпадом на пульте' : (x.error || 'Не получилось'), 7000);
    }, 'eq-cfg', 'primary'));
    r0.append(btn('Проверить ещё раз', () => { EQS.d = null; keepFocus(renderSettings); }, 'eq-recheck2'));
    sec.append(r0);
  } else {
    sec.append(el('p', 'note', 'Тонкая настройка по полосам — на пульте в телефоне, вкладка «Звук».' + (d.status && d.status.device ? ` Звук идёт через «${esc(d.status.device)}», эквалайзер к нему подключён.` : '')));
  }
  sec.append(toggleRow('Эквалайзер включён', !!e.on, () => apply({ on: !e.on }), 'eq-on'));
  const seg = el('div', 'seg');
  seg.style.flexWrap = 'wrap';
  d.presets.forEach((p) => seg.append(btn(p.name, () => apply({ preset: p.id }), 'eq:' + p.id, e.on && e.preset === p.id ? 'sel' : '')));
  const r = el('div', 's-row');
  r.style.background = 'transparent'; r.style.padding = '.2rem 0';
  r.append(seg);
  sec.append(r);
}

// ---------- Обновления ----------

const UP = { st: null };

async function loadUpdate() {
  try { UP.st = await api.get('/api/update'); } catch (e) {}
  return UP.st;
}

// Окно «Доступна новая версия» — показываем сразу, как только TVHub её нашёл
let pendingUpdate = null;
function onUpdateEvent(ev) {
  if (UP.st) UP.st.available = { version: ev.version, notes: ev.notes };
  else UP.st = { available: { version: ev.version, notes: ev.notes } };
  if (SV.on) hideSaver();
  // Во время просмотра не мешаем: покажем, когда выйдете из плеера
  if (S.screen === 'player' || !$('modal').classList.contains('hidden')) { pendingUpdate = ev; showBanner({ kind: 'update', title: 'Доступна новая версия TVHub ' + ev.version, text: 'Предложу обновиться, когда закончите смотреть' }); return; }
  askInstallUpdate();
}
setInterval(() => {
  if (pendingUpdate && S.screen === 'main' && $('modal').classList.contains('hidden')) { pendingUpdate = null; askInstallUpdate(); }
}, 2000);

function notesNode(notes) {
  if (!notes) return null;
  const box = el('div', 'up-notes');
  // Описание версии с GitHub: убираем служебные ссылки и разметку
  const lines = notes.split('\n').map((l) => l.replace(/^[#*\-\s]+/, '').replace(/\*\*/g, '').replace(/https?:\/\/\S+/g, '').replace(/\bby @\S+( in)?/g, '').trim())
    .filter((l) => l && !/^(full changelog|what's changed|new contributors)/i.test(l)).slice(0, 12);
  if (!lines.length) return null;
  box.innerHTML = '<b>Что нового:</b><ul>' + lines.map((l) => `<li>${esc(l)}</li>`).join('') + '</ul>';
  return box;
}

async function showWhatsNew() {
  let r;
  try { r = await fetch('/api/whatsnew', { method: 'POST', headers: { 'X-Token': TOKEN } }).then((x) => x.json()); } catch (e) { return; }
  if (!r || !r.version) return;
  modal(`TVHub обновлён до версии ${r.version}`, [{ label: 'Отлично' }], notesNode(r.notes) || el('div', 'up-notes', 'Обновление установлено, все настройки сохранены.'));
}

function askInstallUpdate() {
  const a = UP.st && UP.st.available;
  if (!a) { loadUpdate().then((st) => (st && st.available ? askInstallUpdate() : toast('Новой версии нет'))); return; }
  modal(`Доступна новая версия TVHub ${a.version}. Обновить сейчас? Программа перезапустится, настройки сохранятся.`, [
    { label: 'Обновить', cls: 'primary', act: async () => {
      const r = await api.post('/api/update', { action: 'install' }).catch((e) => ({ ok: false, error: e.message }));
      toast(r.ok ? 'Скачиваю обновление, TVHub перезапустится сам…' : (r.error || 'Не получилось'), 6000);
    } },
    { label: 'Позже' },
  ], notesNode(a.notes));
}

function updateSection(inner) {
  const c = S.cfg;
  const sec = el('div', 's-section');
  sec.append(el('h2', '', 'Обновления'));
  const st = UP.st;
  let line = `Версия TVHub: <b>${esc((st && st.current) || S.info.version)}</b>`;
  if (st) {
    if (st.available) line += ` · доступна новая версия <b>${esc(st.available.version)}</b>`;
    else if (st.checked && !st.error) line += ' · это последняя версия';
    if (st.state === 'downloading') line += ' · скачиваю…';
    if (st.error) line += '<br>' + esc(st.error);
    if (st.checked) line += `<br><span class="src">Проверено ${ago(st.checked)}${st.repo ? ' · github.com/' + esc(st.repo) : ''}</span>`;
  }
  sec.append(el('p', 'note', line + '<br>TVHub проверяет обновления при запуске и каждый час и сразу предлагает обновиться. Перед установкой проверяется, что файл скачался целиком.'));
  const r = el('div', 's-row');
  r.style.background = 'transparent'; r.style.padding = '.2rem 0';
  if (st && st.available) r.append(btn(`Обновить до ${st.available.version}`, askInstallUpdate, 'upd-install', 'primary'));
  r.append(btn('Проверить сейчас', async () => {
    toast('Проверяю…');
    const res = await api.post('/api/update', { action: 'check' }).catch(() => null);
    if (res && res.status) UP.st = res.status;
    toast(UP.st && UP.st.available ? `Доступна версия ${UP.st.available.version}` : (UP.st && UP.st.error ? UP.st.error : 'У вас последняя версия'), 5000);
    keepFocus(renderSettings);
  }, 'upd-check'));
  if (st && st.canRollback) r.append(btn('Вернуть прошлую версию', () => modal('Вернуть прошлую версию TVHub? Программа перезапустится.', [
    { label: 'Вернуть', cls: 'danger', act: async () => {
      const res = await api.post('/api/update', { action: 'rollback' }).catch((e) => ({ ok: false, error: e.message }));
      if (!res.ok) toast(res.error || 'Не получилось', 5000);
    } },
    { label: 'Отмена' },
  ]), 'upd-rollback'));
  sec.append(r);
  sec.append(toggleRow('Проверять обновления автоматически', !c.settings.autoUpdateOff, async () => {
    c.settings.autoUpdateOff = !c.settings.autoUpdateOff; await saveCfg(); keepFocus(renderSettings);
  }, 'upd-auto'));
  inner.append(sec);
}

// ---------- Яркость ----------

const BR = { level: 100 };
let brTimer;
function onBrightnessEvent(ev) {
  BR.level = ev.level;
  const o = $('brOsd');
  const ds = ev.displays || [];
  const how = ds.length && ds.every((d) => d.method === 'none') ? 'Этот экран не даёт менять яркость'
    : ds.some((d) => d.method === 'ddc') ? '' : 'Затемнение картинки';
  o.innerHTML = `<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="4.5"/><path d="M12 2v2.5M12 19.5V22M2 12h2.5M19.5 12H22M4.9 4.9l1.8 1.8M17.3 17.3l1.8 1.8M4.9 19.1l1.8-1.8M17.3 6.7l1.8-1.8"/></svg>` +
    `<div class="br-bar"><i style="width:${ev.level}%"></i></div><b>${ev.level}%</b>` + (how ? `<small>${how}</small>` : '');
  o.classList.remove('hidden');
  clearTimeout(brTimer);
  brTimer = setTimeout(() => o.classList.add('hidden'), 2500);
  const v = document.getElementById('brModalVal');
  if (v) v.textContent = ev.level + '%';
}

async function setBrightness(delta) {
  try { const r = await api.post('/api/brightness', { delta }); BR.level = r.level; } catch (e) { toast('Ошибка: ' + e.message); }
}

async function showBrightness() {
  try { BR.level = (await api.get('/api/brightness')).level; } catch (e) {}
  const box = el('div', 'br-modal');
  const minus = btn('☀ −', () => setBrightness(-10), 'br-minus');
  const plus = btn('☀ +', () => setBrightness(10), 'br-plus');
  const val = el('div', 'br-modal-val', `<span id="brModalVal">${BR.level}%</span>`);
  box.append(minus, val, plus);
  modal('Яркость экрана', [{ label: 'Готово' }], box);
  setFocus(plus);
}

// ---------- Тёмная тема ----------

const TH = { windows: false, sites: false };

async function loadTheme() {
  try { Object.assign(TH, await api.get('/api/theme')); } catch (e) {}
  return TH;
}

async function setTheme(part, on) {
  try {
    const r = await api.post('/api/theme', { [part]: on });
    if (!r.ok) { toast(r.error || 'Не получилось', 5000); return; }
    Object.assign(TH, r);
    if (part === 'windows') toast(on ? 'Тёмная тема включена для всего компьютера' : 'Светлая тема включена');
    else toast(on ? 'Сайты будут тёмными. Открытые сайты закрыты — откройте их заново' : 'Сайты снова как обычно. Откройте их заново');
  } catch (e) { toast('Ошибка: ' + e.message); }
}

async function showThemeModal() {
  await loadTheme();
  const box = el('div', 'theme-modal');
  const render = () => {
    box.innerHTML = '';
    box.append(toggleRow('Тёмная тема Windows и программ', TH.windows, async () => { await setTheme('windows', !TH.windows); render(); focusByKey('th-win', $('modal')); }, 'th-win'));
    box.append(toggleRow('Тёмные сайты (Кинопоиск, YouTube и другие)', TH.sites, async () => { await setTheme('sites', !TH.sites); render(); focusByKey('th-sites', $('modal')); }, 'th-sites'));
    box.append(el('div', 'note', 'Тёмная тема Windows перекрашивает Проводник, Параметры, Edge и программы, которые поддерживают тему Windows. «Тёмные сайты» затемняют в Edge даже сайты без своей тёмной темы.'));
  };
  render();
  modal('Тёмная тема', [{ label: 'Готово' }], box);
  focusByKey('th-win', $('modal'));
}

function themeSection(inner) {
  const sec = el('div', 's-section');
  sec.append(el('h2', '', 'Тёмная тема'));
  sec.append(el('p', 'note', 'Включает тёмную тему для всего компьютера: Windows, Проводник, Параметры, Edge и программы, которые поддерживают тему Windows.'));
  sec.append(toggleRow('Тёмная тема Windows и программ', TH.windows, async () => { await setTheme('windows', !TH.windows); keepFocus(renderSettings); }, 'set-darkwin'));
  sec.append(toggleRow('Тёмные сайты (Edge затемняет сайты без своей тёмной темы)', TH.sites, async () => { await setTheme('sites', !TH.sites); keepFocus(renderSettings); }, 'set-darksites'));
  inner.append(sec);
}

// ---------- Запуск ----------

function startExtras() {
  renderWeatherChip();
  renderBell();
  loadEPGNow();
  setInterval(loadEPGNow, 120000);
  loadWeather();
  setInterval(loadWeather, 15 * 60000);
  loadNotices();
  loadUpdate();
  loadTheme();
  setTimeout(showWhatsNew, 1500);
  $('weatherBtn')._onfocus = null;
  if (!cur || !cur.isConnected) setWelcomeHero();
}


// ---------- Встроенный браузер (сайты внутри окна TVHub) ----------

const DRM = { res: null, busy: false };

// Какие системы защиты фильмов есть во встроенном движке: от этого зависит, будут ли
// онлайн-кинотеатры показывать фильмы прямо в окне TVHub.
async function drmCheck() {
  const cfg = [{ initDataTypes: ['cenc'],
    audioCapabilities: [{ contentType: 'audio/mp4; codecs="mp4a.40.2"' }],
    videoCapabilities: [{ contentType: 'video/mp4; codecs="avc1.42E01E"' }] }];
  const test = async (key) => {
    if (!navigator.requestMediaKeySystemAccess) return false;
    try { await navigator.requestMediaKeySystemAccess(key, cfg); return true; } catch (e) { return false; }
  };
  return {
    widevine: await test('com.widevine.alpha'),
    playready: (await test('com.microsoft.playready.recommendation')) || (await test('com.microsoft.playready')),
  };
}

function insideSection(inner) {
  const sec = el('div', 's-section');
  sec.append(el('h2', '', 'Встроенный браузер'));
  sec.append(el('p', 'note', 'Сайты открываются прямо в окне TVHub, без отдельных окон. Если какому-то сайту это не подходит (например, фильм не запускается), «Меню» на его карточке → «Открывать в Edge». ' +
    '«Домой» возвращает на главный экран, «Назад» — на прошлую страницу сайта. Онлайн-кинотеатры защищают фильмы от копирования — посмотрите ниже, что умеет встроенный браузер на этом компьютере.'));
  inner.append(sec);
  const r = DRM.res;
  if (!r) {
    sec.append(el('p', 'note', 'Проверяю защиту фильмов…'));
    if (!DRM.busy) {
      DRM.busy = true;
      drmCheck().then((x) => { DRM.res = x; }).catch(() => { DRM.res = { widevine: false, playready: false }; })
        .finally(() => { DRM.busy = false; if (S.tab === 'settings') keepFocus(renderSettings); });
    }
    return;
  }
  const yes = (v) => (v ? '<b style="color:#2fbf71">есть</b>' : '<b style="color:#ff5a5f">нет</b>');
  sec.append(el('p', 'note', `Защита Widevine (как в Chrome и Android TV): ${yes(r.widevine)}<br>Защита PlayReady (как в Edge и Smart TV): ${yes(r.playready)}<br>` +
    (r.widevine || r.playready
      ? 'Онлайн-кинотеатры могут заработать внутри TVHub — попробуйте. Если фильм не запускается, включите для этого сайта «Открывать в Edge».'
      : 'Фильмы онлайн-кинотеатров внутри TVHub, скорее всего, не запустятся — оставьте их в Edge. Внутри TVHub хорошо работают YouTube, VK Видео, Rutube, Twitch.')));
}


// ---------- Свой VPN (Xray, без Happ) ----------

const OV = { st: null, loading: false, busy: false };

async function ovPost(body, okText) {
  if (OV.busy) return;
  OV.busy = true;
  toast(body.sub !== undefined ? 'Загружаю серверы из подписки…' : body.on ? 'Включаю VPN… В первый раз TVHub скачает движок Xray — это займёт минуту' : 'Секунду…', 6000);
  try {
    const r = await api.post('/api/ownvpn', body);
    OV.st = r;
    if (r.error) toast(r.error, 8000);
    else if (okText) toast(okText(r), 5000);
  } catch (e) { toast('Ошибка: ' + e.message, 6000); }
  OV.busy = false;
  refreshVPN();
  if (S.tab === 'settings') keepFocus(renderSettings);
}

function ownVpnSection(inner) {
  const sec = el('div', 's-section');
  sec.append(el('h2', '', 'Свой VPN (без Happ)'));
  inner.append(sec);
  if (!OV.st) {
    sec.append(el('p', 'note', 'Загружаю…'));
    if (!OV.loading) {
      OV.loading = true;
      api.get('/api/ownvpn').then((r) => { OV.st = r; }).catch(() => { OV.st = { configured: false, servers: [] }; })
        .finally(() => { OV.loading = false; if (S.tab === 'settings') keepFocus(renderSettings); });
    }
    return;
  }
  const st = OV.st;
  sec.append(el('p', 'note', 'TVHub сам подключается к вашим VPN-серверам — на том же движке Xray, что и Happ. Российские сайты идут напрямую, всё остальное — через VPN, поэтому VPN не нужно выключать ради Кинопоиска. ' +
    'Вставьте ссылку-подписку вашего VPN-сервиса (https://…). Её дают в личном кабинете или боте сервиса, а в Happ она видна в настройках подписки. Подойдёт и ссылка для Happ вида happ://add/…, и ссылки на серверы (vless://, vmess://, trojan://, ss://). Набрать ссылку удобно с телефона: вкладка «Текст».'));
  const form = el('div', 'form');
  const f = field('Ссылка-подписка', 'ownvpn-sub', 'https://… или vless://…', st.sub || '');
  f.style.flex = '3';
  form.append(f);
  form.append(btn(st.configured ? 'Обновить' : 'Загрузить серверы', () => {
    const v = $('ownvpn-sub').value.trim();
    if (!v) { toast('Вставьте ссылку-подписку'); return; }
    ovPost({ sub: v }, (r) => `Серверов: ${r.servers.length}. Выберите сервер и включите VPN`);
  }, 'ownvpn-load', 'primary'));
  sec.append(form);
  if (!st.configured) return;
  sec.append(toggleRow('VPN включён', !!st.want, () => ovPost({ on: !st.want }, (r) => (r.on ? 'VPN включён: российские сайты — напрямую, остальные — через VPN' : 'VPN выключен')), 'ownvpn-on'));
  const line = st.busy ? st.busy : st.error ? 'Ошибка: ' + st.error : st.on ? `Подключено: <b>${esc((st.servers[st.server] || {}).name || '')}</b>` : (st.want ? 'Не подключено' : 'Выключен');
  sec.append(el('p', 'note', line));
  const seg = el('div', 'seg');
  seg.style.flexWrap = 'wrap';
  st.servers.forEach((s, i) => seg.append(btn(s.name || s.host, () => ovPost({ server: i }, () => `Сервер: ${s.name || s.host}`), 'ownvpn-srv:' + i, i === st.server ? 'sel' : '')));
  const r = el('div', 's-row');
  r.style.background = 'transparent'; r.style.padding = '.2rem 0';
  r.append(seg);
  sec.append(r);
  const r2 = el('div', 's-row');
  r2.style.background = 'transparent'; r2.style.padding = '.2rem 0';
  r2.append(btn('Удалить подписку', () => modal('Удалить подписку и выключить свой VPN?', [
    { label: 'Удалить', cls: 'danger', act: () => ovPost({ remove: true }, () => 'Подписка удалена') },
    { label: 'Отмена' },
  ]), 'ownvpn-del', 'danger'));
  sec.append(r2);
}
