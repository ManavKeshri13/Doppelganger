/**
 * popup.js — Project Aegis Chrome Extension
 *
 * Responsibilities:
 *  - Poll GET /status every 2 s to keep the dashboard live.
 *  - Handle the "Scan Page Risks" button:
 *      1. Extract document.body.innerText from the active tab via chrome.scripting
 *      2. POST the text to the backend's /analyze endpoint
 *  - Render Risk Score, Dark Patterns list, Summary, and connection health.
 *
 * MV3-compliant: no inline scripts, no eval, no external fetches
 * except the Go backend on localhost:8000 (declared in host_permissions).
 */

'use strict';

// ─── Constants ───────────────────────────────────────────────────────────────

const API_BASE          = 'http://localhost:8000';
const POLL_INTERVAL     = 2000; // ms
const STATUS_ENDPOINT   = `${API_BASE}/status`;
const ANALYZE_ENDPOINT  = `${API_BASE}/analyze`;

// Risk score colour thresholds
const RISK_LOW    = 3;  // score 1-3  → green
const RISK_MEDIUM = 6;  // score 4-6  → orange
                        // score 7-10 → red

// ─── DOM refs ─────────────────────────────────────────────────────────────────

const $ = id => document.getElementById(id);

const connDot     = $('conn-dot');
const connLabel   = $('conn-label');
const statusBadge = $('status-badge');
const phaseBar    = $('phase-bar');
const statRisk    = $('stat-risk');
const statTotal   = $('stat-total');
const summaryText = $('summary-text');
const patternList = $('pattern-list');
const errorPanel  = $('error-panel');
const errorText   = $('error-text');
const triggerBtn  = $('trigger-btn');
const btnLabel    = $('btn-label');
const btnIconIdle = $('btn-icon-idle');
const btnIconSpin = $('btn-icon-spin');

// ─── App state ────────────────────────────────────────────────────────────────

let isConnected    = false;
let currentStatus  = 'idle';

// ─── Polling ──────────────────────────────────────────────────────────────────

/** Fetches /status and updates all UI elements. */
async function pollStatus() {
  try {
    const res = await fetch(STATUS_ENDPOINT, {
      method: 'GET',
      headers: { 'Accept': 'application/json' },
      signal: AbortSignal.timeout(3000),
    });

    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const data = await res.json();

    setConnected(true);
    renderStatus(data);
  } catch (_err) {
    setConnected(false);
    if (currentStatus === 'analyzing') {
      renderBtnState('idle');
    }
  }
}

/** Renders the entire dashboard from a /status payload. */
function renderStatus(data) {
  currentStatus = data.status;

  renderStatusBadge(data.status);
  renderPhaseBar(data.status);

  // ── Risk Score ─────────────────────────────────────────────
  renderRiskScore(data.riskScore, data.status);

  // ── Total Scans ────────────────────────────────────────────
  statTotal.textContent = data.totalScans ?? 0;

  // ── Summary ────────────────────────────────────────────────
  renderSummary(data.summary, data.status);

  // ── Dark Patterns ──────────────────────────────────────────
  renderPatternList(data.darkPatterns || []);

  // ── Error ──────────────────────────────────────────────────
  if (data.status === 'error' && data.lastError) {
    showError(data.lastError);
  } else {
    hideError();
  }

  // ── Button ─────────────────────────────────────────────────
  renderBtnState(data.status);
}

// ─── Risk Score ───────────────────────────────────────────────────────────────

function renderRiskScore(score, status) {
  if (!score || status === 'idle' || status === 'analyzing') {
    if (status === 'analyzing') {
      statRisk.textContent = '…';
      statRisk.style.color = 'var(--clr-cyan)';
      statRisk.style.textShadow = '0 0 6px var(--clr-cyan)';
    } else if (!score) {
      statRisk.textContent = '—';
      statRisk.style.color = 'var(--txt-muted)';
      statRisk.style.textShadow = 'none';
    }
    return;
  }

  statRisk.textContent = `${score}/10`;

  if (score <= RISK_LOW) {
    statRisk.style.color = 'var(--clr-green)';
    statRisk.style.textShadow = '0 0 6px var(--clr-green), 0 0 12px var(--clr-green)';
  } else if (score <= RISK_MEDIUM) {
    statRisk.style.color = 'var(--clr-orange)';
    statRisk.style.textShadow = '0 0 6px var(--clr-orange), 0 0 12px var(--clr-orange)';
  } else {
    statRisk.style.color = 'var(--clr-red)';
    statRisk.style.textShadow = '0 0 6px var(--clr-red), 0 0 12px var(--clr-red)';
  }
}

// ─── Summary ──────────────────────────────────────────────────────────────────

function renderSummary(summary, status) {
  if (status === 'analyzing') {
    summaryText.textContent = '🔍 Analysing page content with Groq LLM…';
    summaryText.classList.remove('persona-text--active');
    return;
  }

  if (summary) {
    summaryText.textContent = summary;
    summaryText.classList.add('persona-text--active');
  } else {
    summaryText.innerHTML = 'No scan performed yet. Hit <span class="hint-keyword">SCAN PAGE RISKS</span> to analyse the current tab.';
    summaryText.classList.remove('persona-text--active');
  }
}

// ─── Dark Patterns list ───────────────────────────────────────────────────────

function renderPatternList(patterns) {
  if (!patterns || !patterns.length) {
    patternList.innerHTML = '<li class="query-list--empty">No dark patterns detected yet…</li>';
    return;
  }

  patternList.innerHTML = '';
  patterns.forEach((p, i) => {
    const li  = document.createElement('li');
    li.className = 'query-item';

    const idx = document.createElement('span');
    idx.className = 'query-item__index';
    idx.textContent = `[${String(i + 1).padStart(2, '0')}]`;

    const txt = document.createElement('span');
    txt.className = 'query-item__text';
    txt.textContent = p;   // textContent is XSS-safe
    txt.title = p;

    li.appendChild(idx);
    li.appendChild(txt);
    patternList.appendChild(li);
  });
}

// ─── Status badge ─────────────────────────────────────────────────────────────

const STATUS_CLASSES = { idle: true, analyzing: true, error: true };

function renderStatusBadge(status) {
  const label = status === 'analyzing' ? 'SCANNING' : status.toUpperCase();
  statusBadge.textContent = label;
  statusBadge.className = `status-badge status-badge--${status in STATUS_CLASSES ? status : 'idle'}`;
}

// ─── Phase bar ────────────────────────────────────────────────────────────────

const PHASE_BAR_WIDTHS = { idle: '0%', analyzing: '75%', error: '100%' };

function renderPhaseBar(status) {
  phaseBar.style.width = PHASE_BAR_WIDTHS[status] ?? '0%';

  if (status === 'analyzing') {
    phaseBar.style.background = 'var(--clr-cyan)';
    phaseBar.style.boxShadow  = '0 0 10px var(--clr-cyan)';
  } else if (status === 'error') {
    phaseBar.style.background = 'var(--clr-red)';
    phaseBar.style.boxShadow  = '0 0 10px var(--clr-red)';
    setTimeout(() => { phaseBar.style.width = '0%'; }, 1500);
  } else {
    phaseBar.style.background = 'var(--clr-green)';
    phaseBar.style.boxShadow  = '0 0 8px var(--clr-green)';
  }
}

// ─── Button state ─────────────────────────────────────────────────────────────

function renderBtnState(status) {
  if (status === 'analyzing') {
    triggerBtn.disabled = true;
    btnLabel.textContent = 'Scanning…';
    btnIconIdle.classList.add('hidden');
    btnIconSpin.classList.remove('hidden');
    triggerBtn.classList.add('trigger-btn--running');
  } else {
    triggerBtn.disabled = false;
    btnLabel.textContent = 'Scan Page Risks';
    btnIconIdle.classList.remove('hidden');
    btnIconSpin.classList.add('hidden');
    triggerBtn.classList.remove('trigger-btn--running');
  }
}

// ─── Connection indicator ─────────────────────────────────────────────────────

function setConnected(ok) {
  if (ok === isConnected) return;
  isConnected = ok;

  if (ok) {
    connDot.className    = 'conn-dot conn-dot--online';
    connLabel.textContent = 'connected';
    connLabel.className   = 'conn-label conn-label--online';
  } else {
    connDot.className    = 'conn-dot conn-dot--offline';
    connLabel.textContent = 'offline';
    connLabel.className   = 'conn-label conn-label--offline';
    summaryText.textContent = '⚠ Backend offline. Make sure the Go server is running on :8000.';
    summaryText.classList.remove('persona-text--active');
  }
}

// ─── Error panel ──────────────────────────────────────────────────────────────

function showError(msg) {
  errorText.textContent = msg;
  errorPanel.classList.remove('hidden');
}

function hideError() {
  errorPanel.classList.add('hidden');
  errorText.textContent = '';
}

// ─── Scan button ──────────────────────────────────────────────────────────────

triggerBtn.addEventListener('click', async () => {
  if (triggerBtn.disabled) return;
  hideError();

  // ── Step 1: Get the active tab ────────────────────────────────────────────
  let [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tab || !tab.id) {
    showError('Could not identify the active tab.');
    return;
  }

  // Optimistic UI update
  renderBtnState('analyzing');
  renderStatusBadge('analyzing');
  renderPhaseBar('analyzing');

  // ── Step 2: Extract page text via chrome.scripting ────────────────────────
  let pageText = '';
  try {
    const results = await chrome.scripting.executeScript({
      target: { tabId: tab.id },
      func: () => document.body.innerText,
    });
    pageText = results?.[0]?.result ?? '';
  } catch (err) {
    showError(`Could not read page content: ${err.message}. Try on a regular http/https page.`);
    renderBtnState('idle');
    renderStatusBadge('idle');
    renderPhaseBar('idle');
    return;
  }

  if (!pageText.trim()) {
    showError('Page appears to be empty or unreadable.');
    renderBtnState('idle');
    renderStatusBadge('idle');
    renderPhaseBar('idle');
    return;
  }

  // ── Step 3: POST to /analyze ──────────────────────────────────────────────
  try {
    const res = await fetch(ANALYZE_ENDPOINT, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ page_text: pageText }),
      signal: AbortSignal.timeout(8000),
    });

    const data = await res.json();

    if (!res.ok || !data.success) {
      showError(data.message || data.error || `Server returned ${res.status}`);
      if (res.status !== 409) {
        renderBtnState('idle');
        renderStatusBadge('idle');
        renderPhaseBar('idle');
      }
      return;
    }

    // 202 Accepted — polling will pick up the results within ~5-10 s
  } catch (err) {
    showError(`Could not reach backend: ${err.message}`);
    renderBtnState('idle');
    renderStatusBadge('idle');
    renderPhaseBar('idle');
  }
});

// ─── Boot ─────────────────────────────────────────────────────────────────────

pollStatus();
setInterval(pollStatus, POLL_INTERVAL);
