'use strict';
// Каналы 18+: пока не введён PIN-код, их не видно ни в списках, ни в поиске, ни в избранном.
// PIN спрашивается один раз; через 2 часа или после перезапуска TVHub — снова.

const AD = { unlocked: false, at: 0, info: null, loading: false };
const PIN = { active: false, resolve: null, val: '' };
const ADULT_GROUP = 'Для взрослых';

function adultLocked() {
  if (AD.unlocked && Date.now() - AD.at > 2 * 3600e3) AD.unlocked = false;
  return !AD.unlocked;
}
function visibleChannels() {
  return adultLocked() ? S.channels.filter((c) => !c.adult) : S.channels;
}

// Окно ввода PIN-кода: цифры экранные, с пульта или с телефона (вкладка «Цифры»)
function pinPrompt(title) {
  return new Promise((resolve) => {
    PIN.active = true; PIN.val = ''; PIN.resolve = resolve;
    const box = el('div', 'pin-box');
    box.innerHTML = '<div class="pin-dots" id="pinDots"></div>';
    const pad = el('div', 'pin-pad');
    for (const d of ['1', '2', '3', '4', '5', '6', '7', '8', '9', '⌫', '0']) {
      const b = el('button', 'f pin-k', d);
      b.dataset.k = 'pin:' + d;
      b._act = () => (d === '⌫' ? pinErase() : pinDigit(d));
      pad.append(b);
    }
    box.append(pad);
    modal(title, [{ label: 'Отмена' }], box);
    pinDraw();
    focusByKey('pin:1', $('modal'));
  });
}
function pinDraw() {
  const d = $('pinDots');
  if (d) d.innerHTML = [0, 1, 2, 3].map((i) => `<i class="${i < PIN.val.length ? 'on' : ''}"></i>`).join('');
}
function pinDigit(d) {
  if (!PIN.active) return;
  PIN.val += d;
  pinDraw();
  if (PIN.val.length === 4) {
    const r = PIN.resolve, v = PIN.val;
    PIN.active = false; PIN.resolve = null;
    closeModal(); // сразу, чтобы следующие цифры попали в следующее окно (повтор PIN-кода)
    r(v);
  }
}
function pinErase() { PIN.val = PIN.val.slice(0, -1); pinDraw(); }
// Окно закрыли «Назад» или «Отмена» — PIN не введён
function pinCancelled() {
  if (!PIN.active) return;
  const r = PIN.resolve;
  PIN.active = false; PIN.resolve = null;
  if (r) r(null);
}

async function unlockAdult() {
  if (!adultLocked()) return true;
  const pin = await pinPrompt('Каналы 18+ · введите PIN-код');
  if (!pin) return false;
  const r = await api.post('/api/adult', { check: true, pin }).catch(() => ({ ok: false }));
  if (!r.ok) { toast('Неверный PIN-код'); return false; }
  AD.unlocked = true; AD.at = Date.now();
  return true;
}

// ---------- Раздел в настройках ----------

function adultSection(inner) {
  const sec = el('div', 's-section');
  sec.append(el('h2', '', 'Каналы 18+'));
  inner.append(sec);
  if (!AD.info) {
    sec.append(el('p', 'note', 'Загружаю…'));
    if (!AD.loading) {
      AD.loading = true;
      api.post('/api/adult', {}).then((r) => { AD.info = r; }).catch(() => { AD.info = { on: false, hasPin: false }; })
        .finally(() => { AD.loading = false; if (S.tab === 'settings') keepFocus(renderSettings); });
    }
    return;
  }
  const on = !!S.cfg.settings.adult;
  const n = AD.info.count || 0;
  sec.append(el('p', 'note', 'Каналы для взрослых из вашего плейлиста — категории «18+», «XXX», «Для взрослых» и похожие. TVHub собирает их в одну категорию «Для взрослых» и закрывает PIN-кодом: без него их не видно ни в списках, ни в поиске, ни в избранном. Пока переключатель выключен, этих каналов нет совсем.<br>' +
    (n ? `В текущем плейлисте таких каналов: <b>${n}</b>.` : 'В текущем плейлисте таких каналов нет: они бывают в пакетах IPTV-провайдеров. Открытые каталоги каналы 18+ не содержат.')));
  sec.append(toggleRow('Показывать каналы 18+', on, async () => {
    if (on) {
      await api.post('/api/adult', { on: false });
      S.cfg.settings.adult = false; AD.unlocked = false;
      toast('Каналы 18+ скрыты');
      await loadChannels(true);
      keepFocus(renderSettings);
      return;
    }
    modal('Каналы 18+ можно включить, только если вам есть 18 лет.', [
      { label: 'Мне есть 18 лет', cls: 'primary', act: () => setTimeout(enableAdult, 50) },
      { label: 'Отмена' },
    ]);
  }, 'set-adult'));
  if (!on && AD.info.hasPin) {
    const r = el('div', 's-row');
    r.style.background = 'transparent'; r.style.padding = '.2rem 0';
    r.append(btn('Забыли PIN-код? Задать новый', async () => {
      const x = await api.post('/api/adult', { reset: true }).catch((e) => ({ ok: false, error: e.message }));
      if (x.ok) { AD.info.hasPin = false; toast('PIN-код сброшен — при включении задайте новый'); keepFocus(renderSettings); }
      else toast(x.error || 'Не получилось');
    }, 'set-adult-reset'));
    sec.append(r);
  }
}

async function enableAdult() {
  let pin;
  if (AD.info.hasPin) {
    pin = await pinPrompt('Введите PIN-код для каналов 18+');
  } else {
    const p1 = await pinPrompt('Придумайте PIN-код из 4 цифр');
    if (!p1) return;
    const p2 = await pinPrompt('Повторите PIN-код');
    if (!p2) return;
    if (p1 !== p2) { toast('PIN-коды не совпали, попробуйте ещё раз'); return; }
    pin = p1;
  }
  if (!pin) return;
  const r = await api.post('/api/adult', { on: true, pin }).catch((e) => ({ ok: false, error: e.message }));
  if (!r.ok) { toast(r.error || 'Не получилось'); return; }
  S.cfg.settings.adult = true;
  AD.info.hasPin = true; AD.unlocked = true; AD.at = Date.now();
  await loadChannels(true);
  const n = S.channels.filter((c) => c.adult).length;
  AD.info = null; // перечитать, сколько каналов 18+ в плейлисте
  toast(n ? `Каналы 18+ включены: ${n} — категория «Для взрослых» в «Телеканалах», в конце списка` : 'Каналы 18+ включены, но в вашем плейлисте их нет', 6000);
  keepFocus(renderSettings);
}
