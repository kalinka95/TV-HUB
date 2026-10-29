/* TVHub: Bluetooth и Wi-Fi — значки вверху главного экрана и панели подключения.
   Наушники, колонку, геймпад или сеть можно подключить, не выходя в настройки Windows. */

const CN = { st: null, devs: [], scanning: false, scanned: false, busy: {}, nets: null, netsLoading: false, hint: '', pass: null, view: '' };

const CN_ICON = {
  bt: '<path d="M7 7l10 10-5 5V2l5 5L7 17"/>',
  headphones: '<path d="M3 18v-6a9 9 0 0 1 18 0v6"/><path d="M21 19a2 2 0 0 1-2 2h-1v-6h3zM3 19a2 2 0 0 0 2 2h1v-6H3z"/>',
  speaker: '<rect x="5" y="2" width="14" height="20" rx="2"/><circle cx="12" cy="14" r="4"/><circle cx="12" cy="6" r="1"/>',
  gamepad: '<path d="M6 12h4M8 10v4"/><circle cx="15" cy="12" r=".6"/><circle cx="17.5" cy="10" r=".6"/><path d="M17.3 5H6.7a4 4 0 0 0-3.9 3.2l-1.3 6.6A3 3 0 0 0 6.5 17l2-2h7l2 2a3 3 0 0 0 5-2.2l-1.3-6.6A4 4 0 0 0 17.3 5z"/>',
  phone: '<rect x="7" y="2" width="10" height="20" rx="2"/><path d="M11 18h2"/>',
  keyboard: '<rect x="2" y="6" width="20" height="12" rx="2"/><path d="M6 10h.01M10 10h.01M14 10h.01M18 10h.01M7 14h10"/>',
  mouse: '<rect x="6" y="3" width="12" height="18" rx="6"/><path d="M12 7v4"/>',
  computer: '<rect x="3" y="4" width="18" height="12" rx="2"/><path d="M2 20h20"/>',
  tv: '<rect x="2" y="5" width="20" height="13" rx="2"/><path d="M8 21h8"/>',
  lock: '<rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/>',
  check: '<path d="M5 12l5 5L20 7"/>',
};
const cnSvg = (k, cls = '') => `<svg class="${cls}" viewBox="0 0 24 24">${CN_ICON[k] || CN_ICON.bt}</svg>`;

// Значок Wi-Fi: дуги светятся по силе сигнала (0–4)
function wifiSvg(bars, off) {
  const on = (n) => (bars >= n ? '' : ' class="dim"');
  return `<svg viewBox="0 0 24 24" class="wf">${off ? '<path d="M3 3l18 18"/>' : ''}<path${on(4)} d="M2 8.5a15 15 0 0 1 20 0"/><path${on(3)} d="M5 12a10 10 0 0 1 14 0"/><path${on(2)} d="M8.5 15.5a5 5 0 0 1 7 0"/><circle cx="12" cy="19" r="1.1" fill="currentColor"/></svg>`;
}

const KIND_NAME = { headphones: 'Наушники', speaker: 'Колонка', gamepad: 'Геймпад', keyboard: 'Клавиатура', mouse: 'Мышь', phone: 'Телефон', computer: 'Компьютер', tv: 'Телевизор', other: 'Устройство' };

async function loadConn() {
  try { CN.st = await api.get('/api/conn'); } catch (e) { return; }
  renderConnChips();
  if (qsOpen() && CN.view === 'bt' && !CN.scanning) { mergeDevs(((CN.st.bt || {}).devices) || [], true); renderQS(); }
}

function renderConnChips() {
  const st = CN.st || {};
  const bt = st.bt || {}, wf = st.wifi || {};
  const b = $('btBtn'), w = $('wifiBtn');
  if (!b || !w) return;
  const conn = (bt.devices || []).filter((d) => d.connected);
  b.classList.toggle('hidden', bt.radio === 'none' || !!st.error);
  b.classList.toggle('lit', conn.length > 0);
  b.classList.toggle('off', bt.radio === 'off');
  b.innerHTML = cnSvg(conn.length ? conn[0].kind : 'bt'); // подключено — значок устройства и подсветка
  b._act = () => openBT();
  b._info = { kicker: 'Bluetooth', title: conn.length ? conn.map((d) => d.name).join(', ') : bt.radio === 'off' ? 'Bluetooth выключен' : 'Ничего не подключено', sub: 'OK — наушники, колонки и геймпады' };
  const net = wf.current;
  w.classList.toggle('hidden', !wf.adapter || !!st.error);
  w.classList.toggle('off', !net);
  w.innerHTML = wifiSvg(net ? net.bars : 0, !net);
  w._act = () => openWifi();
  w._info = { kicker: 'Wi-Fi', title: net ? net.ssid : 'Не подключено', sub: 'OK — выбрать сеть' };
  if (cur === b || cur === w) setHero(cur._info);
}

// ---------- Панель ----------

// Панель открыта (её закрывают и кнопкой, и «Назад» на пульте)
function qsOpen() { return !!$('qsBody') && !$('modal').classList.contains('hidden'); }

function openQS(view) {
  CN.view = view;
  CN.autoFocus = true; // как только появится список — встаём на первую строку
  modal('', [{ label: 'Закрыть' }], el('div', 'qs', '<div id="qsBody"></div>'));
  $('modal').firstElementChild.classList.add('qs-box');
  renderQS(true);
}

function qsRow(key, icon, title, sub, opts = {}) {
  const r = el('button', 'f qs-row' + (opts.on ? ' on' : '') + (opts.busy ? ' busy' : ''));
  r.dataset.k = key;
  r.innerHTML = `<span class="qs-ico${opts.on ? ' on' : ''}">${icon}</span><span class="qs-txt"><span class="qs-t">${esc(title)}</span><span class="qs-s">${opts.busy ? '<i class="qs-spin"></i>' : ''}${esc(sub)}</span></span>` +
    (opts.pill ? `<span class="qs-pill${opts.on ? ' on' : ''}">${esc(opts.pill)}</span>` : '');
  if (opts.act) r._act = opts.act;
  return r;
}

function renderQS(first) {
  const body = $('qsBody');
  if (!body || !qsOpen()) return;
  const k = cur && cur.dataset.k;
  body.innerHTML = '';
  if (CN.view === 'bt') renderBTBody(body); else renderWifiBody(body);
  const row = body.querySelector('.qs-row:not(.busy)');
  if (CN.autoFocus && row) { CN.autoFocus = false; setFocus(row); return; }
  if (first || !focusByKey(k, $('modal'))) {
    const f = row || body.querySelector('.f');
    if (f) setFocus(f);
  }
}

// ---------- Bluetooth ----------

function mergeDevs(list, pairedOnly) {
  const map = new Map(CN.devs.map((d) => [d.id, d]));
  if (pairedOnly) for (const d of CN.devs) if (d.paired) map.delete(d.id); // список сопряжённых пришёл заново
  for (const d of list) {
    // Одно и то же устройство поиск может прислать под другим id — узнаём его по имени
    const same = [...map.values()].find((x) => x.id !== d.id && x.name === d.name);
    if (same) { if (d.connected) same.connected = true; continue; }
    map.set(d.id, { ...map.get(d.id), ...d });
  }
  CN.devs = [...map.values()];
}

function openBT() {
  CN.devs = []; CN.scanned = false;
  mergeDevs(((CN.st && CN.st.bt) || {}).devices || [], true);
  openQS('bt');
  if (((CN.st && CN.st.bt) || {}).radio !== 'off') btScan();
}

async function btScan() {
  if (CN.scanning) return;
  CN.scanning = true;
  renderQS();
  const r = await api.post('/api/bt', { action: 'scan' }).catch((e) => ({ ok: false, error: e.message }));
  CN.scanning = false;
  CN.scanned = true;
  if (r.ok) { mergeDevs(r.devices || []); CN.seen = r.seen || 0; }
  else toast(r.error, 5000);
  if (qsOpen() && CN.view === 'bt') renderQS();
}

async function btDo(d, action, okText) {
  CN.busy[d.id] = action;
  renderQS();
  const r = await api.post('/api/bt', { action, id: d.id }).catch((e) => ({ ok: false, error: e.message }));
  delete CN.busy[d.id];
  if (r.ok) {
    if (action === 'unpair') { d.paired = false; d.connected = false; }
    else { d.paired = true; d.connected = true; }
    toast(okText, 4000);
  } else toast(r.error, 7000);
  await loadConn();
  if (qsOpen() && CN.view === 'bt') renderQS();
}

function renderBTBody(body) {
  const bt = (CN.st && CN.st.bt) || {};
  const on = bt.radio !== 'off';
  const head = el('div', 'qs-head', `<span class="qs-hico">${cnSvg('bt')}</span><span class="qs-htxt"><span class="qs-title">Bluetooth</span><span class="qs-sub">${on ? 'Наушники, колонки, геймпады' : 'Выключен'}</span></span>`);
  const sw = toggleRow(on ? 'Вкл' : 'Выкл', on, async () => {
    const r = await api.post('/api/bt', { action: 'radio', on: !on }).catch((e) => ({ ok: false, error: e.message }));
    if (!r.ok) { toast(r.error, 5000); return; }
    await loadConn();
    renderQS();
    if (!on) btScan();
  }, 'qs-radio');
  sw.classList.add('qs-switch');
  head.append(sw);
  body.append(head);
  if (!on) { body.append(el('p', 'qs-note', 'Включите Bluetooth, чтобы подключить наушники или колонку.')); return; }

  const paired = CN.devs.filter((d) => d.paired).sort((a, b) => b.connected - a.connected);
  const found = CN.devs.filter((d) => !d.paired);
  const list = el('div', 'qs-list');
  if (paired.length) list.append(el('div', 'qs-sec', 'Мои устройства'));
  for (const d of paired) {
    const busy = CN.busy[d.id];
    const row = el('div', 'qs-line');
    row.append(qsRow('bt:' + d.id, cnSvg(d.kind), d.name,
      busy === 'connect' ? 'Подключаю…' : busy === 'unpair' ? 'Удаляю…' : d.connected ? 'Подключено' : KIND_NAME[d.kind] + ' · OK — подключить',
      { on: d.connected, busy: !!busy, pill: d.connected ? 'Подключено' : '', act: () => (d.connected ? toast('«' + d.name + '» уже подключено', 3000) : btDo(d, 'connect', '«' + d.name + '» подключено')) }));
    const forget = btn('Забыть', () => modal(`Забыть «${d.name}»?`, [
      { label: 'Забыть', cls: 'danger', act: () => { openQS('bt'); btDo(d, 'unpair', '«' + d.name + '» удалено'); } },
      { label: 'Отмена', act: () => openQS('bt') },
    ]), 'btf:' + d.id, 'qs-mini');
    row.append(forget);
    list.append(row);
  }
  list.append(el('div', 'qs-sec', 'Доступные устройства'));
  for (const d of found) {
    const busy = CN.busy[d.id];
    list.append(qsRow('bt:' + d.id, cnSvg(d.kind), d.name, busy ? 'Подключаю… Подтвердите на устройстве, если оно попросит' : KIND_NAME[d.kind] + ' · OK — подключить',
      { busy: !!busy, act: () => btDo(d, 'pair', '«' + d.name + '» подключено') }));
  }
  if (CN.scanning) list.append(el('div', 'qs-scan', '<i class="qs-spin"></i>Ищу устройства… Переведите наушники в режим сопряжения: обычно нужно зажать кнопку питания или Bluetooth, пока не замигает индикатор'));
  else if (CN.scanned && !found.length) list.append(el('p', 'qs-note', CN.seen
    ? 'Новых устройств не нашлось. Включите на наушниках режим сопряжения (зажмите кнопку питания или Bluetooth, пока индикатор не замигает) и нажмите «Искать ещё раз». Если наушники подключены к телефону — выключите Bluetooth на телефоне.'
    : 'Windows не увидела рядом ни одного Bluetooth-устройства. Проверьте, что Bluetooth на ноутбуке включён (переключатель вверху) и работает в параметрах Windows, переведите наушники в режим сопряжения и нажмите «Искать ещё раз».'));
  body.append(list);
  if (!CN.scanning) {
    const again = btn('Искать ещё раз', () => btScan(), 'qs-scan-btn', 'qs-wide');
    body.append(again);
  }
}

// ---------- Wi-Fi ----------

function openWifi() {
  CN.pass = null;
  openQS('wifi');
  wifiScan();
}

async function wifiScan() {
  CN.netsLoading = true;
  renderQS();
  const r = await api.post('/api/wifi', { action: 'scan' }).catch((e) => ({ ok: false, error: e.message }));
  CN.netsLoading = false;
  if (r.ok) { CN.nets = r.networks || []; CN.hint = r.hint || ''; } else { CN.nets = CN.nets || []; toast(r.error, 5000); }
  if (qsOpen() && CN.view === 'wifi') renderQS();
}

async function wifiConnect(n, password) {
  CN.busy[n.ssid] = 1;
  CN.pass = null;
  renderQS();
  const r = await api.post('/api/wifi', { action: 'connect', ssid: n.ssid, password: password || '' }).catch((e) => ({ ok: false, error: e.message }));
  delete CN.busy[n.ssid];
  if (r.ok && r.needPassword) { CN.pass = n; renderQS(); return; }
  if (r.ok) toast('Подключено к «' + n.ssid + '»', 4000);
  else { toast(r.error, 6000); if (/пароль/i.test(r.error)) { CN.pass = n; } }
  await loadConn();
  await wifiScan();
}

function renderWifiBody(body) {
  const wf = (CN.st && CN.st.wifi) || {};
  const cur = wf.current;
  body.append(el('div', 'qs-head', `<span class="qs-hico">${wifiSvg(cur ? cur.bars : 4, !cur)}</span><span class="qs-htxt"><span class="qs-title">Wi-Fi</span><span class="qs-sub">${cur ? 'Подключено к «' + esc(cur.ssid) + '»' : 'Не подключено'}</span></span>`));
  // Ввод пароля
  if (CN.pass) {
    const n = CN.pass;
    const box = el('div', 'qs-pass');
    box.append(el('div', 'qs-sec', 'Пароль для «' + esc(n.ssid) + '»'));
    const f = field('Пароль сети', 'qs-pw', 'Пароль', '');
    const inp = f.querySelector('input');
    inp.type = 'password';
    box.append(f);
    box.append(el('p', 'qs-note', 'Пароль удобно ввести с телефона: пульт → вкладка «Текст».'));
    const row = el('div', 'seg');
    row.append(btn('Показать пароль', () => { inp.type = inp.type === 'password' ? 'text' : 'password'; }, 'qs-pw-show'));
    row.append(btn('Подключить', () => { if (inp.value.length < 8) { toast('Пароль Wi-Fi — не короче 8 символов', 3000); return; } wifiConnect(n, inp.value); }, 'qs-pw-ok', 'primary'));
    row.append(btn('Назад', () => { CN.pass = null; renderQS(); }, 'qs-pw-back'));
    box.append(row);
    body.append(box);
    setTimeout(() => focusByKey('qs-pw', $('modal')), 0);
    return;
  }
  const list = el('div', 'qs-list');
  const nets = (CN.nets || []).slice().sort((a, b) => b.connected - a.connected || b.bars - a.bars);
  if (nets.length) list.append(el('div', 'qs-sec', 'Сети рядом'));
  for (const n of nets) {
    const busy = CN.busy[n.ssid];
    const row = el('div', 'qs-line');
    row.append(qsRow('wf:' + n.ssid, wifiSvg(n.bars) + (n.secure ? cnSvg('lock', 'qs-lock') : ''), n.ssid,
      busy ? 'Подключаю…' : n.connected ? 'Подключено' : n.secure ? 'Защищённая сеть · OK — подключить' : 'Открытая сеть · OK — подключить',
      { on: n.connected, busy: !!busy, pill: n.connected ? 'Подключено' : '', act: () => (n.connected ? toast('Уже подключено', 2500) : wifiConnect(n)) }));
    if (n.connected) {
      row.append(btn('Отключить', async () => {
        const r = await api.post('/api/wifi', { action: 'disconnect' }).catch((e) => ({ ok: false, error: e.message }));
        if (!r.ok) toast(r.error, 5000);
        await loadConn(); wifiScan();
      }, 'wfd:' + n.ssid, 'qs-mini'));
    }
    list.append(row);
  }
  if (CN.netsLoading) list.append(el('div', 'qs-scan', '<i class="qs-spin"></i>Ищу сети…'));
  else if (!nets.length) list.append(el('p', 'qs-note', esc(CN.hint || 'Сетей не видно.')));
  body.append(list);
  if (!CN.netsLoading) body.append(btn('Обновить список', () => wifiScan(), 'qs-wscan', 'qs-wide'));
}

// Значки вверху: обновляем раз в полминуты, пока виден главный экран
loadConn();
setInterval(() => { if (S.screen === 'main' && document.visibilityState === 'visible' && !qsOpen()) loadConn(); }, 30000);
