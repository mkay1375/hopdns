const platformTabs = document.querySelector('.platform-tabs');
const tabs = Array.from(platformTabs.querySelectorAll('button'));
const panels = tabs.map(tab => document.getElementById(tab.dataset.platform));

function detectPlatform() {
  const agent = navigator.userAgent;
  if (/Android|iPhone|iPad|iPod|Xbox|PlayStation|CrOS/i.test(agent)) return;

  const platform = navigator.userAgentData?.platform || navigator.platform || agent;
  if (/Win/i.test(platform)) return 'windows';
  if (/Mac/i.test(platform) && navigator.maxTouchPoints < 2) return 'macos';
  if (/Linux/i.test(platform)) return 'linux';
}

function selectTab(index) {
  tabs.forEach((tab, tabIndex) => {
    const selected = tabIndex === index;
    tab.setAttribute('aria-selected', String(selected));
    tab.tabIndex = selected ? 0 : -1;
    panels[tabIndex].hidden = !selected;
  });
}

platformTabs.setAttribute('role', 'tablist');
platformTabs.setAttribute('aria-labelledby', 'downloads-title');
tabs.forEach((tab, index) => {
  const panel = panels[index];
  tab.setAttribute('role', 'tab');
  tab.setAttribute('aria-controls', panel.id);
  panel.setAttribute('role', 'tabpanel');
  panel.setAttribute('aria-labelledby', tab.id);
  panel.tabIndex = 0;
  tab.addEventListener('click', () => selectTab(index));
});

const arrowStep = document.documentElement.dir === 'rtl' ? -1 : 1;
platformTabs.addEventListener('keydown', event => {
  const current = tabs.indexOf(event.target);
  if (current < 0) return;

  let next;
  switch (event.key) {
    case 'ArrowRight': next = (current + arrowStep + tabs.length) % tabs.length; break;
    case 'ArrowLeft': next = (current - arrowStep + tabs.length) % tabs.length; break;
    case 'Home': next = 0; break;
    case 'End': next = tabs.length - 1; break;
    default: return;
  }
  event.preventDefault();
  selectTab(next);
  tabs[next].focus();
});

const detected = detectPlatform();
selectTab(Math.max(0, tabs.findIndex(tab => tab.dataset.platform === detected)));
platformTabs.hidden = false;
