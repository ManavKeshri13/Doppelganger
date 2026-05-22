/**
 * popup.js — Project Doppelgänger Chrome Extension
 *
 * Responsibilities:
 *  - Poll GET /status every 2 s to keep the dashboard live.
 *  - Handle the "Inject Noise" button → POST /trigger.
 *  - Render persona, query list, counters, and connection health.
 *
 * MV3-compliant: no inline scripts, no eval, no external fetches
 * except the Go backend on localhost:8000 (declared in host_permissions).
 */

'use strict';

// ─── Constants ───────────────────────────────────────────────────────────────

const API_BASE         = 'http://localhost:8000';
const POLL_INTERVAL    = 2000; // ms
const STATUS_ENDPOINT  = `${API_BASE}/status`;
const TRIGGER_ENDPOINT = `${API_BASE}/trigger`;

// ─── DOM refs ─────────────────────────────────────────────────────────────────

const $ = id => document.getElementById(id);

const connDot     = $('conn-dot');
const connLabel   = $('conn-label');
const statusBadge = $('status-badge');
const phaseBar    = $('phase-bar');
const statTotal   = $('stat-total');
const statTime    = $('stat-time');
const personaText = $('persona-text');
const queryList   = $('query-list');
const errorPanel  = $('error-panel');
const errorText   = $('error-text');
const triggerBtn  = $('trigger-btn');
const btnLabel    = $('btn-label');
const btnIconIdle = $('btn-icon-idle');
const btnIconSpin = $('btn-icon-spin');

// ─── App state ────────────────────────────────────────────────────────────────

let isConnected   = false;
let currentStatus = 'idle';

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
    if (currentStatus === 'running') {
      renderBtnState('idle');
    }
  }
}

/** Renders the entire dashboard from a /status payload. */
function renderStatus(data) {
  currentStatus = data.status;

  renderStatusBadge(data.status);
  renderPhaseBar(data.status);

  // ── Stats ──────────────────────────────────────────────────
  statTotal.textContent = data.totalQueriesInjected ?? 0;
  statTime.textContent  =
    (data.lastTriggerTime && data.lastTriggerTime !== '0001-01-01T00:00:00Z')
      ? formatRelativeTime(new Date(data.lastTriggerTime))
      : '—';

  // ── Persona ────────────────────────────────────────────────
  renderPersona(data.activePersona);

  // ── Queries ────────────────────────────────────────────────
  renderQueryList(data.activeQueries || []);

  // ── Error ──────────────────────────────────────────────────
  if (data.status === 'error' && data.lastError) {
    showError(data.lastError);
  } else {
    hideError();
  }

  // ── Button ─────────────────────────────────────────────────
  renderBtnState(data.status);
}

// ─── Status badge ─────────────────────────────────────────────────────────────

function renderStatusBadge(status) {
  statusBadge.textContent = status.toUpperCase();
  statusBadge.className   = `status-badge status-badge--${status in STATUS_CLASSES ? status : 'idle'}`;
}

const STATUS_CLASSES = { idle: true, running: true, error: true };

// ─── Phase bar ────────────────────────────────────────────────────────────────

const PHASE_BAR_WIDTHS = { idle: '0%', running: '75%', error: '100%' };

function renderPhaseBar(status) {
  phaseBar.style.width = PHASE_BAR_WIDTHS[status] ?? '0%';

  if (status === 'running') {
    phaseBar.style.background = 'var(--clr-green)';
    phaseBar.style.boxShadow  = '0 0 10px var(--clr-green)';
  } else if (status === 'error') {
    phaseBar.style.background = 'var(--clr-red)';
    phaseBar.style.boxShadow  = '0 0 10px var(--clr-red)';
    // Flash then reset
    setTimeout(() => { phaseBar.style.width = '0%'; }, 1500);
  } else {
    phaseBar.style.background = 'var(--clr-green)';
    phaseBar.style.boxShadow  = '0 0 8px var(--clr-green)';
  }
}

// ─── Persona ──────────────────────────────────────────────────────────────────

function renderPersona(persona) {
  if (persona) {
    // Use textContent for XSS safety — no innerHTML for LLM output
    personaText.textContent = `"${persona}"`;
    personaText.classList.add('persona-text--active');
  } else {
    // Only this default state uses innerHTML (fully controlled static string)
    personaText.innerHTML = 'No persona generated yet. Hit <span class="hint-keyword">INJECT NOISE</span> to begin.';
    personaText.classList.remove('persona-text--active');
  }
}

// ─── Query list ───────────────────────────────────────────────────────────────

function renderQueryList(queries) {
  if (!queries.length) {
    queryList.innerHTML = '<li class="query-list--empty">No queries yet…</li>';
    return;
  }

  // Build DOM nodes manually — avoids any innerHTML + LLM data combination
  queryList.innerHTML = '';
  queries.forEach((q, i) => {
    const li    = document.createElement('li');
    li.className = 'query-item';

    const idx   = document.createElement('span');
    idx.className = 'query-item__index';
    idx.textContent = `[${String(i + 1).padStart(2, '0')}]`;

    const txt   = document.createElement('span');
    txt.className = 'query-item__text';
    txt.textContent = q;       // textContent is XSS-safe
    txt.title = q;

    li.appendChild(idx);
    li.appendChild(txt);
    queryList.appendChild(li);
  });
}

// ─── Button state ─────────────────────────────────────────────────────────────

function renderBtnState(status) {
  if (status === 'running') {
    triggerBtn.disabled = true;
    btnLabel.textContent = 'Injecting…';
    btnIconIdle.classList.add('hidden');
    btnIconSpin.classList.remove('hidden');
    triggerBtn.classList.add('trigger-btn--running');
  } else {
    triggerBtn.disabled = false;
    btnLabel.textContent = 'Inject Noise';
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
    connDot.className   = 'conn-dot conn-dot--online';
    connLabel.textContent = 'connected';
    connLabel.className   = 'conn-label conn-label--online';
  } else {
    connDot.className   = 'conn-dot conn-dot--offline';
    connLabel.textContent = 'offline';
    connLabel.className   = 'conn-label conn-label--offline';
    personaText.textContent = '⚠ Backend offline. Make sure the Go server is running on :8000.';
    personaText.classList.remove('persona-text--active');
  }
}

// ─── Error panel ──────────────────────────────────────────────────────────────

function showError(msg) {
  errorText.textContent = msg;   // textContent — XSS safe
  errorPanel.classList.remove('hidden');
}

function hideError() {
  errorPanel.classList.add('hidden');
  errorText.textContent = '';
}

// ─── Trigger button ───────────────────────────────────────────────────────────

triggerBtn.addEventListener('click', async () => {
  if (triggerBtn.disabled) return;
  hideError();

  // Optimistic UI update
  renderBtnState('running');
  renderStatusBadge('running');
  renderPhaseBar('running');

  try {
    const res = await fetch(TRIGGER_ENDPOINT, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      signal: AbortSignal.timeout(5000),
    });

    const data = await res.json();

    if (!res.ok || !data.success) {
      // e.g. 409 Conflict — already running
      showError(data.message || `Server returned ${res.status}`);
      if (res.status !== 409) {
        renderBtnState('idle');
        renderStatusBadge('idle');
      }
      return;
    }

    // 202 Accepted — polling will reflect real progress shortly
  } catch (err) {
    showError(`Could not reach backend: ${err.message}`);
    renderBtnState('idle');
    renderStatusBadge('idle');
    renderPhaseBar('idle');
  }
});

// ─── Utilities ────────────────────────────────────────────────────────────────

/** Formats a Date as a human-relative string (e.g. "3m ago"). */
function formatRelativeTime(date) {
  const secs = Math.round((Date.now() - date.getTime()) / 1000);
  if (secs < 5)    return 'just now';
  if (secs < 60)   return `${secs}s ago`;
  if (secs < 3600) return `${Math.floor(secs / 60)}m ago`;
  return `${Math.floor(secs / 3600)}h ago`;
}

// ─── Boot ─────────────────────────────────────────────────────────────────────

pollStatus();
setInterval(pollStatus, POLL_INTERVAL);
