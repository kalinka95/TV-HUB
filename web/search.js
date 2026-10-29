'use strict';
// Поиск каналов: окно с экранной клавиатурой. Открывается кнопкой OK во время просмотра,
// кнопкой «Поиск» в «Телеканалах», набором букв и кнопкой «ТВ» на пульте в телефоне.

const SR = { open: false, q: '', lang: 'ru', timer: 0 };
const SR_ROWS = {
  ru: ['АБВГДЕЖЗИЙК', 'ЛМНОПРСТУФХ', 'ЦЧШЩЪЫЬЭЮЯ', '1234567890'],
  en: ['ABCDEFGHIJ', 'KLMNOPQRST', 'UVWXYZ', '1234567890'],
};

function srNorm(s) {
  return String(s || '').toLowerCase().replace(/ё/g, 'е').replace(/[^\p{L}\p{N}]+/gu, ' ').trim();
}

// Подбор каналов: номер, начало названия, начало слова, просто внутри названия
function srFind(q) {
  q = srNorm(q);
  if (!q) return null;
  const qs = q.replace(/ /g, '');
  const hits = [];
  for (const c of visibleChannels()) {
    let sc = String(c.n) === q ? 0 : -1;
    for (const name of [c.name, c.orig]) {
      const n = srNorm(name);
      if (!n) continue;
      const s = n.startsWith(q) ? 1 : (' ' + n).includes(' ' + q) ? 2 : n.replace(/ /g, '').includes(qs) ? 3 : -1;
      if (s >= 0 && (sc < 0 || s < sc)) sc = s;
    }
    if (sc >= 0) hits.push([sc, c]);
  }
  hits.sort((a, b) => a[0] - b[0]);
  return hits.slice(0, 60).map((h) => h[1]);
}

function openSearch(q = '') {
  if (typeof hideSaver === 'function') hideSaver();
  if (!$('modal').classList.contains('hidden')) closeModal();
  if (S.screen === 'player' && typeof closeZap === 'function') closeZap();
  if (S.screen !== 'main' && S.screen !== 'player') show('main');
  SR.open = true;
  SR.q = q;
  SR.ret = cur;
  $('srch').classList.remove('hidden');
  $('srch').classList.toggle('over-video', S.screen === 'player');
  renderSearchKb();
  renderSearch();
  cur = null;
  if (q) focusFirst($('srchRes')) || focusFirst($('srchKb'));
  else focusFirst($('srchKb'));
}

function closeSearch() {
  if (!SR.open) return;
  SR.open = false;
  $('srch').classList.add('hidden');
  cur = null;
  const r = SR.ret;
  SR.ret = null;
  if (S.screen === 'main') {
    if (r && r.isConnected && r.offsetParent !== null) setFocus(r, { scroll: false });
    else focusFirst($('panel-' + S.tab));
  }
}

function srType(text) {
  SR.q = (SR.q + text).slice(0, 40);
  clearTimeout(SR.timer);
  SR.timer = setTimeout(renderSearch, 60);
  $('srchText').textContent = SR.q;
}

function srErase(all) {
  SR.q = all ? '' : SR.q.slice(0, -1);
  $('srchText').textContent = SR.q;
  renderSearch();
}

function renderSearchKb() {
  const kb = $('srchKb');
  const k = cur && kb.contains(cur) ? cur.dataset.k : null;
  kb.innerHTML = '';
  for (const row of SR_ROWS[SR.lang]) {
    const r = el('div', 'srch-row');
    for (const ch of row) {
      const b = el('button', 'f srch-k', esc(ch));
      b.dataset.k = 'sk:' + ch;
      b._act = () => srType(ch.toLowerCase());
      r.append(b);
    }
    kb.append(r);
  }
  const act = el('div', 'srch-row');
  const mk = (label, key, fn, cls = '') => {
    const b = el('button', 'f srch-k wide ' + cls, label);
    b.dataset.k = 'sk:' + key;
    b._act = fn;
    act.append(b);
  };
  mk('Пробел', 'space', () => srType(' '));
  mk('⌫ Стереть', 'bs', () => srErase(false));
  mk('Очистить', 'clear', () => srErase(true));
  mk(SR.lang === 'ru' ? 'ABC' : 'АБВ', 'lang', () => { SR.lang = SR.lang === 'ru' ? 'en' : 'ru'; renderSearchKb(); focusByKey('sk:lang', $('srch')); });
  kb.append(act);
  if (k) focusByKey(k, $('srch'));
}

function renderSearch() {
  $('srchText').textContent = SR.q;
  const box = $('srchRes');
  const had = cur && box.contains(cur);
  box.innerHTML = '';
  let list = srFind(SR.q);
  let head;
  if (!list) {
    // Без запроса — что сейчас идёт и избранное, в просмотре — каналы текущего списка
    if (S.screen === 'player' && P.list && P.list.length) {
      const from = Math.max(0, P.idx - 3);
      list = P.list.slice(from, from + 40);
      head = 'Каналы рядом с текущим';
    } else {
      const fav = visibleChannels().filter((c) => isFav(c.url));
      list = fav.length ? fav : visibleChannels().slice(0, 40);
      head = fav.length ? '★ Избранное' : 'Каналы';
    }
  } else head = list.length ? `Найдено: ${list.length}${list.length === 60 ? '+' : ''}` : 'Ничего не найдено';
  $('srchHead').textContent = head;
  if (!S.channels.length) { box.append(el('div', 'srch-empty', S.chLoading ? 'Загружаю список каналов…' : 'Список каналов пуст')); return; }
  if (!list.length) { box.append(el('div', 'srch-empty', 'Попробуйте набрать название иначе или номер канала')); return; }
  for (const ch of list) {
    const b = el('button', 'f zap-item srch-item' + (S.screen === 'player' && P.ch && P.ch.url === ch.url ? ' playing' : ''));
    b.dataset.k = 'sr:' + ch.url;
    b.innerHTML = `<span class="n">${ch.n}</span>`;
    b.append(logoBox(ch, 'lg'));
    const e = typeof epgNow === 'function' ? epgNow(ch.url) : null;
    b.insertAdjacentHTML('beforeend', `<span class="nm">${esc(ch.name)}<small>${e && e.c ? esc(e.c.t) : esc(ch.group || '')}</small></span>${isFav(ch.url) ? '<span class="st">★</span>' : ''}`);
    b._act = () => srPlay(ch, list);
    b._menu = () => { toggleFav(ch); renderSearch(); };
    box.append(b);
  }
  if (had) focusFirst(box);
}

function srPlay(ch, list) {
  closeSearch();
  // Кнопки «канал +/−» после поиска листают весь список, если канал из него
  playChannel(ch, visibleChannels().includes(ch) ? visibleChannels() : list);
}

// Кнопки в окне поиска. true — нажатие обработано.
function searchKey(k, e) {
  if (!SR.open) return false;
  if (k === 'back') { if (SR.q && cur && $('srchRes').contains(cur)) { focusFirst($('srchKb')); return true; } closeSearch(); return true; }
  if (k === 'backspace') { srErase(false); return true; }
  if (e && e.key && e.key.length === 1 && !e.ctrlKey && !e.altKey && /[\p{L}\p{N} +\-]/u.test(e.key) && !(e.key === ' ' && cur && cur.dataset.k && cur.dataset.k.startsWith('sk:'))) {
    srType(e.key.toLowerCase());
    return true;
  }
  return false;
}

// С телефона: открыть поиск (и сразу показать набранное) или включить канал
function onSearchEvent(ev) {
  if (!SR.open) openSearch(ev.q || '');
  else { SR.q = ev.q || ''; renderSearch(); }
}
function onPlayEvent(ev) {
  const ch = S.channels.find((c) => c.url === ev.url);
  if (!ch) { toast('Канал не найден в списке'); return; }
  srPlay(ch, S.channels);
}
window.tvSearch = (q) => openSearch(q || '');
