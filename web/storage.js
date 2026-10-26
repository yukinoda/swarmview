import { $ } from './dom.js';

export const load = (k, d) => {
  try {
    return JSON.parse(localStorage.getItem(k)) ?? d;
  } catch {
    return d;
  }
};
export const save = (k, v) => {
  try {
    localStorage.setItem(k, JSON.stringify(v));
  } catch {}
};

// Local appearance only; never changes the server capability or provider configuration.
export function setupPreferences(beforeOpen) {
  const themes = ['system', 'light', 'dark'], system = matchMedia('(prefers-color-scheme: dark)');
  let theme = load('swarmview.appearance', 'system');
  if (!themes.includes(theme)) theme = 'system';
  const apply = () => { document.documentElement.dataset.theme = theme === 'system' ? system.matches ? 'dark' : 'light' : theme; };
  apply(); $('appearance').value = theme;
  system.addEventListener('change', apply);
  $('appearance').onchange = () => {
    theme = themes.includes($('appearance').value) ? $('appearance').value : 'system';
    save('swarmview.appearance', theme); apply(); beforeOpen();
  };
  const dialog = $('preferences-dialog');
  let previous;
  const close = () => {
    dialog.close();
    (previous?.isConnected && !previous.closest('[hidden]') ? previous : $('preferences-toggle')).focus({ preventScroll: true });
    previous = null;
  };
  $('preferences-toggle').onclick = () => {
    beforeOpen(); previous = document.activeElement; dialog.showModal();
  };
  $('close-preferences').onclick = close;
  dialog.oncancel = event => { event.preventDefault(); close(); };
  dialog.onkeydown = event => { if (event.key === 'Escape') { event.preventDefault(); close(); } };
}
