'use strict';

/* ==========================================================
   AEGIS v3
   Clean UI Controller
   ========================================================== */

const API_BASE = 'http://localhost:8000';

const STATUS_ENDPOINT = `${API_BASE}/status`;
const ANALYZE_ENDPOINT = `${API_BASE}/analyze`;

const POLL_INTERVAL = 2000;
const STORAGE_KEY = 'aegis_consent_rules';

/* ==========================================================
   DOM HELPERS
   ========================================================== */

const $ = (id) => document.getElementById(id);

/* ==========================================================
   DOM REFERENCES
   ========================================================== */

const connDot = $('conn-dot');
const connLabel = $('conn-label');

const statRisk = $('stat-risk');
const statTotal = $('stat-total');

const riskLabel = $('risk-label');
const riskDescription = $('risk-description');
const riskTag = $('risk-tag');

const summaryText = $('summary-text');

const patternList = $('pattern-list');
const patternCount = $('pattern-count');

const consentRulesEl = $('consent-rules');
const savedBadge = $('persona-saved-badge');

const violationAlert = $('violation-alert');
const violationDetail = $('violation-detail');

const errorPanel = $('error-panel');
const errorText = $('error-text');

const triggerBtn = $('trigger-btn');
const btnLabel = $('btn-label');
const btnIconIdle = $('btn-icon-idle');
const btnIconSpin = $('btn-icon-spin');

/* ==========================================================
   STATE
   ========================================================== */

let currentStatus = 'idle';
let isConnected = false;

/* ==========================================================
   CONSENT STORAGE
   ========================================================== */

async function loadConsentRules() {
  try {
    const result = await chrome.storage.local.get(STORAGE_KEY);

    if (result[STORAGE_KEY]) {
      consentRulesEl.value = result[STORAGE_KEY];
    }
  } catch (err) {
    console.error(err);
  }
}

let saveTimer = null;

consentRulesEl.addEventListener('input', () => {

  clearTimeout(saveTimer);

  saveTimer = setTimeout(async () => {

    try {

      await chrome.storage.local.set({
        [STORAGE_KEY]: consentRulesEl.value
      });

      flashSavedBadge();

    } catch (err) {
      console.error(err);
    }

  }, 500);

});

function flashSavedBadge() {

  savedBadge.classList.remove('hidden');

  clearTimeout(flashSavedBadge.timer);

  flashSavedBadge.timer = setTimeout(() => {
    savedBadge.classList.add('hidden');
  }, 1800);
}

/* ==========================================================
   CONNECTION STATUS
   ========================================================== */

function setConnected(connected) {

  if (connected === isConnected) return;

  isConnected = connected;

  if (connected) {

    connDot.className = 'status-dot conn-dot--online';
    connLabel.textContent = 'Connected';

  } else {

    connDot.className = 'status-dot conn-dot--offline';
    connLabel.textContent = 'Offline';

  }
}

/* ==========================================================
   STATUS POLLING
   ========================================================== */

async function pollStatus() {

  try {

    const response = await fetch(STATUS_ENDPOINT, {
      method: 'GET',
      headers: {
        Accept: 'application/json'
      },
      signal: AbortSignal.timeout(3000)
    });

    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`);
    }

    const data = await response.json();

    setConnected(true);

    renderDashboard(data);

  } catch (err) {

    setConnected(false);

    if (currentStatus === 'analyzing') {
      renderButtonState('idle');
    }
  }
}

/* ==========================================================
   DASHBOARD RENDER
   ========================================================== */

function renderDashboard(data) {

  currentStatus = data.status || 'idle';

  renderRisk(data.riskScore);
  renderSummary(data.summary);
  renderPatterns(data.darkPatterns || []);

  statTotal.textContent = data.totalScans ?? 0;

  renderViolation(
    data.consentViolated,
    data.violationDetails
  );

  if (data.status === 'error' && data.lastError) {

    showError(data.lastError);

  } else {

    hideError();
  }

  renderButtonState(data.status);
}

/* ==========================================================
   RISK SCORE
   ========================================================== */

function renderRisk(score) {

  if (!score) {

    statRisk.textContent = '—';

    riskLabel.textContent = 'Run a Scan';

    riskDescription.textContent =
      'Analyze this website to identify privacy and UX risks.';

    riskTag.textContent = 'Not Scanned';

    return;
  }

  statRisk.textContent = score;

  if (score <= 3) {

    riskLabel.textContent = 'Low Risk';

    riskDescription.textContent =
      'This website appears relatively safe.';

    riskTag.textContent = 'Low';

    riskTag.style.background = '#dcfce7';
    riskTag.style.color = '#166534';

  } else if (score <= 6) {

    riskLabel.textContent = 'Medium Risk';

    riskDescription.textContent =
      'Some concerning privacy or UX practices detected.';

    riskTag.textContent = 'Medium';

    riskTag.style.background = '#fef3c7';
    riskTag.style.color = '#92400e';

  } else {

    riskLabel.textContent = 'High Risk';

    riskDescription.textContent =
      'Multiple privacy or dark pattern concerns detected.';

    riskTag.textContent = 'High';

    riskTag.style.background = '#fee2e2';
    riskTag.style.color = '#991b1b';
  }
}

/* ==========================================================
   SUMMARY
   ========================================================== */

function renderSummary(summary) {

  if (!summary) {

    summaryText.textContent =
      'Run a scan to analyze this website.';

    return;
  }

  summaryText.textContent = summary;
}

/* ==========================================================
   DARK PATTERNS
   ========================================================== */

function renderPatterns(patterns) {

  patternCount.textContent = patterns.length;

  if (!patterns.length) {

    patternList.innerHTML = `
      <div class="empty-state">
        No dark patterns detected.
      </div>
    `;

    return;
  }

  patternList.innerHTML = '';

  patterns.forEach(pattern => {

    const card = document.createElement('div');

    card.className = 'pattern-card';

    card.innerHTML = `
      <div class="pattern-title">
        ⚠ Potential Dark Pattern
      </div>

      <div class="pattern-text">
        ${escapeHtml(pattern)}
      </div>
    `;

    patternList.appendChild(card);

  });
}

/* ==========================================================
   CONSENT VIOLATIONS
   ========================================================== */

function renderViolation(violated, details) {

  if (!violated) {

    violationAlert.classList.add('hidden');

    return;
  }

  violationAlert.classList.remove('hidden');

  violationDetail.textContent =
    details ||
    'This page violates one of your privacy preferences.';
}

/* ==========================================================
   BUTTON STATE
   ========================================================== */

function renderButtonState(status) {

  if (status === 'analyzing') {

    triggerBtn.disabled = true;

    btnLabel.textContent = 'Scanning...';

    btnIconIdle.classList.add('hidden');
    btnIconSpin.classList.remove('hidden');

  } else {

    triggerBtn.disabled = false;

    btnLabel.textContent = 'Scan Page';

    btnIconIdle.classList.remove('hidden');
    btnIconSpin.classList.add('hidden');
  }
}

/* ==========================================================
   ERRORS
   ========================================================== */

function showError(message) {

  errorText.textContent = message;

  errorPanel.classList.remove('hidden');
}

function hideError() {

  errorPanel.classList.add('hidden');

  errorText.textContent = '';
}

/* ==========================================================
   SCAN BUTTON
   ========================================================== */

triggerBtn.addEventListener('click', async () => {

  hideError();

  const [tab] = await chrome.tabs.query({
    active: true,
    currentWindow: true
  });

  if (!tab?.id) {

    showError('Unable to find active tab.');

    return;
  }

  renderButtonState('analyzing');

  let pageText = '';

  try {

    const results =
      await chrome.scripting.executeScript({

        target: {
          tabId: tab.id
        },

        func: () => document.body.innerText
      });

    pageText =
      results?.[0]?.result ?? '';

  } catch (err) {

    showError(
      `Unable to read page content. ${err.message}`
    );

    renderButtonState('idle');

    return;
  }

  if (!pageText.trim()) {

    showError('Page appears empty.');

    renderButtonState('idle');

    return;
  }

  try {

    const response =
      await fetch(ANALYZE_ENDPOINT, {

        method: 'POST',

        headers: {
          'Content-Type': 'application/json'
        },

        body: JSON.stringify({

          page_text: pageText,

          consent_rules:
            consentRulesEl.value.trim()
        }),

        signal: AbortSignal.timeout(10000)
      });

    const data = await response.json();

    if (!response.ok || !data.success) {

      showError(
        data.message ||
        data.error ||
        'Analysis failed.'
      );

      renderButtonState('idle');

      return;
    }

    /*
      Backend accepted scan.
      Polling will update UI.
    */

  } catch (err) {

    showError(
      `Backend connection failed: ${err.message}`
    );

    renderButtonState('idle');
  }
});

/* ==========================================================
   SECURITY
   ========================================================== */

function escapeHtml(text) {

  const div = document.createElement('div');

  div.textContent = text;

  return div.innerHTML;
}

/* ==========================================================
   BOOT
   ========================================================== */

loadConsentRules();

pollStatus();

setInterval(
  pollStatus,
  POLL_INTERVAL
);