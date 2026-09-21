/* Tickr GUI — vanilla JS, no build step. */
(() => {
  const $ = (sel, el = document) => el.querySelector(sel);
  const $$ = (sel, el = document) => Array.from(el.querySelectorAll(sel));
  const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

  const state = { catalog: [], byId: {}, cfg: null, status: {}, dirty: false, openSection: null };
  let revealedApiToken = null;

  // ── API ──────────────────────────────────────────────────────────────────
  async function api(method, path, body) {
    const res = await fetch(path, { method, headers: body ? { 'Content-Type': 'application/json' } : {}, body: body ? JSON.stringify(body) : undefined });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || res.statusText);
    return data;
  }

  function toast(msg, isErr) {
    const t = $('#toast');
    t.textContent = msg; t.classList.toggle('err', !!isErr); t.classList.remove('hidden');
    clearTimeout(t._h); t._h = setTimeout(() => t.classList.add('hidden'), isErr ? 6000 : 2800);
  }

  function setDirty(d) { state.dirty = d; $('#dirty').classList.toggle('hidden', !d); }
  window.addEventListener('beforeunload', (e) => { if (state.dirty) { e.preventDefault(); e.returnValue = ''; } });

  // ── tabs ─────────────────────────────────────────────────────────────────
  const titles = { report: 'Report layout', connectors: 'Connectors', schedule: 'Schedule & print', general: 'General', preview: 'Preview & runs' };
  $$('.nav').forEach((b) => b.addEventListener('click', () => showTab(b.dataset.tab)));
  window.addEventListener('hashchange', () => { const t = location.hash.replace('#', ''); if (titles[t]) showTab(t); });
  function showTab(name) {
    $$('.nav').forEach((b) => b.classList.toggle('active', b.dataset.tab === name));
    $$('.tab').forEach((t) => t.classList.toggle('active', t.id === 'tab-' + name));
    $('#tab-title').textContent = titles[name];
    if (name === 'preview') refreshPreview();
    if (name === 'connectors') renderConnectors();
    location.hash = name;
  }

  // ── load ─────────────────────────────────────────────────────────────────
  async function load() {
    const [catalog, cfg] = await Promise.all([api('GET', '/api/catalog'), api('GET', '/api/config')]);
    state.catalog = catalog; state.byId = Object.fromEntries(catalog.map((m) => [m.id, m])); state.cfg = cfg;
    renderSections(); renderGeneral(); renderSchedules(); renderConnectors();
    await refreshStatus();
    const tab = location.hash.replace('#', '');
    if (titles[tab]) showTab(tab);
  }

  async function refreshStatus() {
    try {
      const st = await api('GET', '/api/status');
      state.status = st;
      $('#version').textContent = 'v' + st.version;
      $('#data-dir').textContent = st.dataDir;
      $('#next-run').textContent = st.nextRun ? `Next run: ${st.nextRun.replace(/:\d\d [A-Z]+$/, '')} (${st.nextRunName})` : 'No schedule active';
      $('#btn-generate').textContent = st.generating ? 'Generating…' : 'Generate now';
      $('#btn-generate').classList.toggle('busy', !!st.generating);
      $('#btn-print').disabled = !st.pdfExists;
      $$('.tag.needs').forEach((t) => t.classList.toggle('ok', !!st.connectors[t.dataset.need]));
    } catch (e) { /* server restarting */ }
  }
  setInterval(refreshStatus, 15000);

  // ── sections (report layout) ─────────────────────────────────────────────
  function renderSections() {
    const root = $('#sections');
    root.innerHTML = '';
    const filter = $('#filter').value.trim().toLowerCase();
    const onlyEnabled = $('#only-enabled').checked;
    state.cfg.sections.forEach((sc, idx) => {
      const info = state.byId[sc.id];
      if (!info) return;
      if (onlyEnabled && !sc.enabled) return;
      if (filter && !(info.name + ' ' + info.description + ' ' + info.category).toLowerCase().includes(filter)) return;
      const el = document.createElement('div');
      el.className = 'sec' + (sc.enabled ? '' : ' off') + (state.openSection === sc.id ? ' open' : '');
      el.dataset.id = sc.id; el.draggable = true;
      const needs = (info.needs || []).map((n) => `<span class="tag needs ${state.status.connectors?.[n] ? 'ok' : ''}" data-need="${n}" title="needs the ${n} connector">${n}</span>`).join('');
      el.innerHTML = `
        <div class="sec-row">
          <span class="handle" title="Drag to reorder">⠿</span>
          <span class="pos">${idx + 1}</span>
          <input type="checkbox" class="sec-toggle" ${sc.enabled ? 'checked' : ''} title="Include in report">
          <div class="sec-name">${esc(info.name)}<small>${esc(info.description)}</small></div>
          ${needs}<span class="tag">${esc(info.category)}</span>
          <div class="order-btns"><button data-move="-1" title="Move up">▲</button><button data-move="1" title="Move down">▼</button></div>
        </div>
        <div class="sec-body">
          <div class="sec-opts"></div>
          <div class="sec-preview"><button class="btn small btn-preview">Preview this section</button><div class="strip"><iframe sandbox="allow-same-origin"></iframe></div><div class="result small muted"></div></div>
        </div>`;
      el.querySelector('.sec-toggle').addEventListener('change', (e) => { sc.enabled = e.target.checked; el.classList.toggle('off', !sc.enabled); setDirty(true); });
      el.querySelector('.sec-toggle').addEventListener('click', (e) => e.stopPropagation());
      el.querySelector('.sec-row').addEventListener('click', (e) => {
        if (e.target.closest('.order-btns') || e.target.closest('.handle')) return;
        state.openSection = state.openSection === sc.id ? null : sc.id;
        renderSections();
      });
      el.querySelectorAll('[data-move]').forEach((b) => b.addEventListener('click', (e) => { e.stopPropagation(); move(sc.id, +b.dataset.move); }));
      // drag & drop
      el.addEventListener('dragstart', (e) => { e.dataTransfer.setData('text/plain', sc.id); e.dataTransfer.effectAllowed = 'move'; el.classList.add('dragging'); });
      el.addEventListener('dragend', () => { el.classList.remove('dragging'); $$('.sec.over').forEach((x) => x.classList.remove('over')); });
      el.addEventListener('dragover', (e) => { e.preventDefault(); e.dataTransfer.dropEffect = 'move'; el.classList.add('over'); });
      el.addEventListener('dragleave', () => el.classList.remove('over'));
      el.addEventListener('drop', (e) => { e.preventDefault(); const from = e.dataTransfer.getData('text/plain'); if (from && from !== sc.id) moveBefore(from, sc.id); });
      if (state.openSection === sc.id) buildOptions(el, sc, info);
      root.appendChild(el);
    });
    if (!root.children.length) root.innerHTML = '<p class="muted">Nothing matches.</p>';
  }
  $('#filter').addEventListener('input', renderSections);
  $('#only-enabled').addEventListener('change', renderSections);

  function move(id, delta) {
    const s = state.cfg.sections; const i = s.findIndex((x) => x.id === id); const j = i + delta;
    if (i < 0 || j < 0 || j >= s.length) return;
    [s[i], s[j]] = [s[j], s[i]]; setDirty(true); renderSections();
  }
  function moveBefore(id, targetId) {
    const s = state.cfg.sections; const i = s.findIndex((x) => x.id === id);
    const [item] = s.splice(i, 1); const j = s.findIndex((x) => x.id === targetId);
    s.splice(j, 0, item); setDirty(true); renderSections();
  }

  function buildOptions(el, sc, info) {
    const box = el.querySelector('.sec-opts');
    sc.options = sc.options || {};
    let html = `<p class="desc">${esc(info.description)}${info.source ? ` <span class="muted">· source: ${esc(info.source)}</span>` : ''}</p>`;
    if (!info.fields.length) html += '<p class="muted small">This section has no options.</p>';
    html += '<div class="grid2">';
    for (const f of info.fields) {
      const v = sc.options[f.key] ?? f.default ?? '';
      const wide = ['textarea', 'list', 'ha_entities'].includes(f.type) ? ' style="grid-column:1/-1"' : '';
      html += `<label${wide}>${esc(f.label)}`;
      switch (f.type) {
        case 'bool': html += `<br><input type="checkbox" data-key="${f.key}" ${v ? 'checked' : ''} style="width:auto">`; break;
        case 'number': html += `<input type="number" data-key="${f.key}" value="${esc(v)}" ${f.min != null ? `min="${f.min}"` : ''} ${f.max != null ? `max="${f.max}"` : ''}>`; break;
        case 'select': html += `<select data-key="${f.key}">${f.options.map((o) => `<option ${o === v ? 'selected' : ''}>${esc(o)}</option>`).join('')}</select>`; break;
        case 'textarea': html += `<textarea data-key="${f.key}">${esc(v)}</textarea>`; break;
        case 'list': html += `<textarea data-key="${f.key}" data-list="1">${esc(Array.isArray(v) ? v.join('\n') : v)}</textarea>`; break;
        case 'ha_entities': html += `<textarea data-key="${f.key}" data-list="1">${esc(Array.isArray(v) ? v.join('\n') : v)}</textarea><button type="button" class="btn small" data-browse="${f.key}" style="margin-top:6px">Browse Home Assistant entities…</button>`; break;
        case 'secret': html += `<input type="password" data-key="${f.key}" value="${esc(v)}">`; break;
        default: html += `<input type="text" data-key="${f.key}" value="${esc(v)}">`;
      }
      if (f.help) html += `<small class="muted">${esc(f.help)}</small>`;
      html += '</label>';
    }
    html += '</div>';
    box.innerHTML = html;
    box.querySelectorAll('[data-key]').forEach((inp) => {
      inp.addEventListener('input', () => { sc.options[inp.dataset.key] = readInput(inp); setDirty(true); });
      inp.addEventListener('change', () => { sc.options[inp.dataset.key] = readInput(inp); setDirty(true); });
    });
    box.querySelectorAll('[data-browse]').forEach((b) => b.addEventListener('click', () => browseEntities(box.querySelector(`[data-key="${b.dataset.browse}"]`), sc)));
    el.querySelector('.btn-preview').addEventListener('click', () => previewSection(el, sc));
  }

  function readInput(inp) {
    if (inp.type === 'checkbox') return inp.checked;
    if (inp.type === 'number') return inp.value === '' ? null : Number(inp.value);
    if (inp.dataset.list) return inp.value.split('\n').map((s) => s.trim()).filter(Boolean);
    return inp.value;
  }

  async function previewSection(el, sc) {
    const frame = el.querySelector('iframe'); const out = el.querySelector('.sec-preview .result'); const btn = el.querySelector('.btn-preview');
    btn.classList.add('busy'); out.textContent = 'Fetching…'; out.className = 'result small muted';
    try {
      const r = await api('POST', `/api/preview/${sc.id}`, { options: sc.options, config: state.cfg });
      if (r.error) { out.textContent = r.error; out.className = 'result err'; frame.srcdoc = ''; }
      else if (r.empty) { out.textContent = 'Nothing to show today (section would be skipped).'; frame.srcdoc = ''; }
      else {
        frame.srcdoc = r.html;
        frame.onload = () => { try { frame.style.height = Math.min(900, frame.contentDocument.body.scrollHeight + 8) + 'px'; } catch (e) {} };
        out.textContent = `Rendered in ${r.ms} ms` + (r.log?.length ? '\n' + r.log.join('\n') : '');
      }
    } catch (e) { out.textContent = e.message; out.className = 'result err'; }
    btn.classList.remove('busy');
  }

  async function browseEntities(textarea, sc) {
    openModal('Home Assistant entities', '<p class="muted small">Loading…</p>');
    try {
      const list = await api('GET', '/api/ha/entities');
      const body = $('#modal-body');
      body.innerHTML = `<input type="search" id="ent-filter" placeholder="Filter by id, name or state…"><div id="ent-list" style="margin-top:8px"></div>`;
      const render = () => {
        const q = $('#ent-filter').value.toLowerCase();
        $('#ent-list').innerHTML = list.filter((e) => !q || (e.ID + ' ' + e.Name + ' ' + e.State).toLowerCase().includes(q)).slice(0, 300)
          .map((e) => `<div class="ent" data-id="${esc(e.ID)}" data-name="${esc(e.Name)}"><code>${esc(e.ID)}</code><span>${esc(e.Name)}</span><span class="muted">${esc(e.State)} ${esc(e.Unit)}</span></div>`).join('') || '<p class="muted">No matches.</p>';
        $$('#ent-list .ent').forEach((d) => d.addEventListener('click', () => {
          const line = `${d.dataset.id} | ${d.dataset.name}`;
          textarea.value = (textarea.value.trim() ? textarea.value.trim() + '\n' : '') + line;
          sc.options[textarea.dataset.key] = readInput(textarea); setDirty(true); toast('Added ' + d.dataset.id);
        }));
      };
      $('#ent-filter').addEventListener('input', render); render();
    } catch (e) { $('#modal-body').innerHTML = `<p class="result err">${esc(e.message)}</p><p class="muted small">Set the Home Assistant URL and token on the Connectors tab and save first.</p>`; }
  }

  function openModal(title, html) { $('#modal-title').textContent = title; $('#modal-body').innerHTML = html; $('#modal').classList.remove('hidden'); }
  $('#modal-close').addEventListener('click', () => $('#modal').classList.add('hidden'));
  $('#modal').addEventListener('click', (e) => { if (e.target.id === 'modal') $('#modal').classList.add('hidden'); });

  $('#btn-reset-layout').addEventListener('click', async () => {
    if (!confirm('Reset section order and enabled flags to the default layout? Section options are kept only for defaults.')) return;
    state.cfg = await api('POST', '/api/layout/reset'); setDirty(false); renderSections(); toast('Layout reset');
  });

  // ── general ──────────────────────────────────────────────────────────────
  const gmap = { 'g-timezone': ['general', 'timezone'], 'g-location': ['general', 'location'], 'g-locname': ['general', 'locationName'], 'g-units': ['general', 'units'],
    'g-child': ['general', 'childName'], 'g-datefmt': ['general', 'dateFormat'], 'g-icons': ['general', 'iconStyle'], 'g-paper': ['general', 'paperWidthMm'], 'g-margin': ['general', 'marginBottomMm'],
    'g-wk': ['general', 'wkhtmltopdf'], 'g-port': ['server', 'port'], 't-source': ['tasks', 'source'], 't-tasks': ['tasks', 'tasksPath'], 't-reminders': ['tasks', 'remindersPath'],
    'print-command': ['print', 'command'] };
  function renderGeneral() {
    for (const [id, [a, b]] of Object.entries(gmap)) {
      const inp = $('#' + id); inp.value = state.cfg[a][b] ?? '';
      inp.oninput = () => { state.cfg[a][b] = inp.type === 'number' ? Number(inp.value) : inp.value; setDirty(true); };
    }
  }

  // ── schedules ────────────────────────────────────────────────────────────
  const dayNames = ['S', 'M', 'T', 'W', 'T', 'F', 'S'];
  function scheduleStatusText(i) {
    const st = (state.status.schedules || [])[i];
    if (!st) return '';
    const parts = [];
    if (st.state?.running) parts.push(`running (attempt ${st.state.attempts})`);
    if (st.state?.lastRun && st.state.lastRun !== '0001-01-01T00:00:00Z') parts.push(`last: ${new Date(st.state.lastRun).toLocaleString()} · ${st.state.lastOk ? 'OK' : 'FAILED'}${st.state.lastMessage && st.state.lastMessage !== 'ok' ? ' · ' + st.state.lastMessage : ''}`);
    else if (st.state?.lastMessage) parts.push(st.state.lastMessage);
    if (st.next && st.enabled) parts.push(`next: ${new Date(st.next).toLocaleString()}`);
    return parts.join(' — ');
  }
  function renderSchedules() {
    const root = $('#schedules'); root.innerHTML = '';
    state.cfg.schedules.forEach((sch, i) => {
      const wrap = document.createElement('div'); wrap.className = 'sched-wrap';
      const row = document.createElement('div'); row.className = 'sched';
      row.innerHTML = `<input type="text" value="${esc(sch.name)}" placeholder="Name">
        <input type="time" value="${esc(sch.time)}">
        <div class="days">${dayNames.map((d, k) => `<label><input type="checkbox" data-day="${k}" ${sch.days.includes(k) ? 'checked' : ''}><span>${d}</span></label>`).join('')}</div>
        <label class="chk"><input type="checkbox" class="s-print" ${sch.print ? 'checked' : ''}> Print</label>
        <label class="chk"><input type="checkbox" class="s-on" ${sch.enabled ? 'checked' : ''}> Enabled</label>
        <span><button class="btn small s-run" title="Generate (and print if ticked) right now">Run now</button> <button class="btn small s-more">Options</button> <button class="btn small danger">Remove</button></span>`;
      const adv = document.createElement('div'); adv.className = 'sched-adv hidden';
      adv.innerHTML = `<div class="grid2">
        <label>Sections <input type="text" class="s-sections" value="${esc((sch.sections || []).join(', '))}" placeholder="blank = every enabled section"><small class="muted">Comma-separated section ids (header, weather_daily, kids_maze…). Lets an evening run print just the kids page.</small></label>
        <label>Catch up if missed within (minutes) <input type="number" class="s-catch" min="0" max="1440" value="${sch.catchUpMinutes ?? 120}"><small class="muted">If the PC was asleep at the scheduled time, run late up to this long after. 0 = never.</small></label>
        <label>Retries on failure <input type="number" class="s-retries" min="0" max="10" value="${sch.retries ?? 2}"></label>
        <label>Minutes between retries <input type="number" class="s-delay" min="1" max="120" value="${sch.retryDelayMinutes ?? 5}"></label>
        <label class="chk"><input type="checkbox" class="s-complete" ${sch.printOnlyIfComplete ? 'checked' : ''}> Only print when every section succeeded</label>
      </div>`;
      const status = document.createElement('div'); status.className = 'muted small sched-status'; status.textContent = scheduleStatusText(i);
      row.querySelector('input[type=text]').oninput = (e) => { sch.name = e.target.value; setDirty(true); };
      row.querySelector('input[type=time]').oninput = (e) => { sch.time = e.target.value; setDirty(true); };
      row.querySelectorAll('[data-day]').forEach((c) => c.onchange = () => { sch.days = $$('[data-day]', row).filter((x) => x.checked).map((x) => +x.dataset.day); setDirty(true); });
      row.querySelector('.s-print').onchange = (e) => { sch.print = e.target.checked; setDirty(true); };
      row.querySelector('.s-on').onchange = (e) => { sch.enabled = e.target.checked; setDirty(true); };
      row.querySelector('.s-more').onclick = () => adv.classList.toggle('hidden');
      row.querySelector('.s-run').onclick = async () => {
        try { await saveIfDirty(); await api('POST', `/api/schedules/${i}/run`, {}); toast(`Running "${sch.name}"…`); setTimeout(() => refreshStatus().then(renderSchedules), 4000); }
        catch (e) { toast(e.message, true); }
      };
      row.querySelector('.danger').onclick = () => { state.cfg.schedules.splice(i, 1); setDirty(true); renderSchedules(); };
      adv.querySelector('.s-sections').oninput = (e) => { sch.sections = e.target.value.split(',').map((s) => s.trim()).filter(Boolean); setDirty(true); };
      adv.querySelector('.s-catch').oninput = (e) => { sch.catchUpMinutes = Number(e.target.value); setDirty(true); };
      adv.querySelector('.s-retries').oninput = (e) => { sch.retries = Number(e.target.value); setDirty(true); };
      adv.querySelector('.s-delay').oninput = (e) => { sch.retryDelayMinutes = Number(e.target.value); setDirty(true); };
      adv.querySelector('.s-complete').onchange = (e) => { sch.printOnlyIfComplete = e.target.checked; setDirty(true); };
      wrap.append(row, status, adv);
      root.appendChild(wrap);
    });
    if (!state.cfg.schedules.length) root.innerHTML = '<p class="muted small">No schedules. Add one to print automatically every morning.</p>';
  }
  $('#btn-add-schedule').addEventListener('click', () => { state.cfg.schedules.push({ name: 'Morning', enabled: true, time: '06:30', days: [1, 2, 3, 4, 5], print: true, catchUpMinutes: 120, retries: 2, retryDelayMinutes: 5 }); setDirty(true); renderSchedules(); });
  $('#btn-test-print').addEventListener('click', async () => {
    const out = $('#print-result'); out.textContent = 'Printing…';
    try { await saveIfDirty(); const r = await api('POST', '/api/print'); out.textContent = 'OK ' + (r.output || ''); }
    catch (e) { out.textContent = e.message; }
  });

  // ── connectors ───────────────────────────────────────────────────────────
  function renderConnectors() {
    const c = state.cfg.connectors; const st = state.status.connectors || {};
    const root = $('#connector-cards');
    root.innerHTML = `
      <div class="card"><h3>Weather <span class="status ${st.weather ? 'ok' : 'warn'}">${st.weather ? 'ready' : 'needs location'}</span></h3>
        <label>Provider <select id="w-provider"><option value="openmeteo">Open-Meteo (free, no key)</option><option value="tomorrowio">Tomorrow.io (API key)</option></select></label>
        <label id="w-keylabel">Tomorrow.io API key <input id="w-key" type="password" placeholder="leave blank to keep"></label>
        <div class="hint">Coordinates come from General settings (<b>${esc(state.cfg.general.location || 'not set')}</b>).</div>
        <button class="btn small" id="w-test">Test</button><div class="result" id="w-result"></div></div>

      <div class="card"><h3>Google Calendar <span class="status ${st.google ? 'ok' : ''}">${st.google ? 'linked' : 'not linked'}</span></h3>
        <label>OAuth client id <input id="gc-id" placeholder="…apps.googleusercontent.com"></label>
        <label>OAuth client secret <input id="gc-secret" type="password"></label>
        <label>Calendar <input id="gc-cal" placeholder="primary, a calendar name, or its id"></label>
        <div class="grid2" style="margin-bottom:8px">
          <label class="chk"><input type="checkbox" id="gc-scope-calendar"> Calendar</label>
          <label class="chk"><input type="checkbox" id="gc-scope-tasks"> Tasks</label>
          <label class="chk"><input type="checkbox" id="gc-scope-keep"> Keep (Workspace accounts only)</label>
        </div>
        <div class="hint">Create a Desktop OAuth client in Google Cloud with redirect URI <code>http://localhost:3031/auth/google/callback</code>, and enable the Calendar API (plus the Tasks API and Keep API if ticked). Changing the ticks requires re-linking.</div>
        <button class="btn small primary" id="gc-connect">${st.google ? 'Re-link' : 'Connect Google'}</button>
        <button class="btn small" id="gc-list" ${st.google ? '' : 'disabled'}>List calendars</button>
        ${st.google ? '<button class="btn small danger" id="gc-disconnect">Disconnect</button>' : ''}
        <div class="result" id="gc-result"></div></div>

      <div class="card"><h3>Dropbox <span class="status ${st.dropbox ? 'ok' : ''}">${st.dropbox ? 'linked' : 'not linked'}</span></h3>
        <label>App key <input id="db-key"></label>
        <label>File path in app folder <input id="db-path" placeholder="/tasks.json (blank = first .json found)"></label>
        <div class="hint">Links the task list stored in Dropbox. When linked, the task source in General switches to Dropbox.</div>
        <button class="btn small primary" id="db-connect">${st.dropbox ? 'Re-link' : 'Connect Dropbox'}</button>
        ${st.dropbox ? '<button class="btn small danger" id="db-disconnect">Disconnect</button>' : ''}
        <div class="result" id="db-result"></div></div>

      <div class="card"><h3>Home Assistant <span class="status ${st.homeassistant ? 'ok' : ''}">${st.homeassistant ? 'configured' : 'not configured'}</span></h3>
        <label>URL <input id="ha-url" placeholder="http://homeassistant.local:8123"></label>
        <label>Long-lived access token <input id="ha-token" type="password" placeholder="leave blank to keep"></label>
        <div class="hint">Create the token in Home Assistant under your profile → Security → Long-lived access tokens. Then add the “Home Assistant readings” section and browse entities from there.</div>
        <button class="btn small" id="ha-test">Test connection</button>
        ${st.homeassistant ? '<button class="btn small danger" id="ha-disconnect">Forget token</button>' : ''}
        <div class="result" id="ha-result"></div></div>

      <div class="card"><h3>Aeris / Xweather station <span class="status ${st.aeris ? 'ok' : ''}">${st.aeris ? 'configured' : 'not configured'}</span></h3>
        <label>Client id <input id="ae-id"></label>
        <label>Client secret <input id="ae-secret" type="password" placeholder="leave blank to keep"></label>
        <label>Station id <input id="ae-station" placeholder="pws_xxxxxxx"></label>
        <button class="btn small" id="ae-test">Test</button><div class="result" id="ae-result"></div></div>

      <div class="card"><h3>AI <span class="status ${st.ai ? 'ok' : ''}">${st.ai ? 'key available' : 'no key'}</span></h3>
        <label>Anthropic API key <input id="ai-anthropic" type="password" placeholder="${state.status.aiEnv?.anthropic ? 'using ANTHROPIC_API_KEY from the environment' : 'sk-ant-…'}"></label>
        <label>OpenAI API key <input id="ai-openai" type="password" placeholder="${state.status.aiEnv?.openai ? 'using OPENAI_API_KEY from the environment' : 'sk-…'}"></label>
        <label>OpenAI-compatible base URL <input id="ai-base" placeholder="blank = api.openai.com; e.g. http://localhost:11434/v1 for Ollama"></label>
        <label>OpenRouter API key <input id="ai-openrouter" type="password" placeholder="${state.status.aiEnv?.openrouter ? 'using OPENROUTER_API_KEY from the environment' : 'sk-or-…'}"></label>
        <label>Local server URL (llama.cpp, Ollama, LM Studio…) <input id="ai-local-base" placeholder="e.g. http://localhost:8080/v1 for llama.cpp's llama-server"></label>
        <label>Local server API key <input id="ai-local-key" type="password" placeholder="only if the server was started with an API key"></label>
        <div class="hint">Used by the “Ask an AI” section. Keys left blank fall back to the ANTHROPIC_API_KEY / OPENAI_API_KEY / OPENROUTER_API_KEY environment variables. A local server usually needs no key at all. Model, prompts and tokens are set per section.</div>
        <button class="btn small" id="ai-test-anthropic">Test Anthropic</button>
        <button class="btn small" id="ai-test-openai">Test OpenAI</button>
        <button class="btn small" id="ai-test-openrouter">Test OpenRouter</button>
        <button class="btn small" id="ai-test-local">Test local server</button>
        <div class="result" id="ai-result"></div></div>

      <div class="card"><h3>Grafana <span class="status ${st.grafana ? 'ok' : ''}">${st.grafana ? 'configured' : 'not configured'}</span></h3>
        <label>URL <input id="gf-url" placeholder="http://localhost:3000"></label>
        <label>Service account token <input id="gf-token" type="password" placeholder="leave blank to keep"></label>
        <div class="hint">Grafana → Administration → Service accounts → add a token with Viewer role. The “Grafana query” section then runs any query through Grafana's data sources (InfluxDB, Prometheus, SQL…).</div>
        <button class="btn small" id="gf-test">Test &amp; list data sources</button>
        <div class="result" id="gf-result"></div></div>

      <div class="card"><h3>Ticketmaster <span class="status ${st.ticketmaster ? 'ok' : ''}">${st.ticketmaster ? 'key saved' : 'optional'}</span></h3>
        <label>Discovery API key <input id="tm-key" type="password" placeholder="leave blank to keep"></label>
        <label>SeatGeek client id <input id="sg-id" placeholder="optional, free at seatgeek.com/account/develop"></label>
        <div class="hint">Both are free and optional. The “Local events” section uses them for concerts, sports and shows near your coordinates. Calendar and RSS feeds need no key and are configured on the section itself.</div></div>

      <div class="card"><h3>Telegram <span class="status ${st.telegram ? 'ok' : ''}">${st.telegram ? 'configured' : 'not configured'}</span></h3>
        <label>Bot token <input id="tg-token" type="password" placeholder="leave blank to keep"></label>
        <label>Allowed chat IDs <textarea id="tg-chats" placeholder="one per line" style="min-height:60px"></textarea></label>
        <div class="hint">Create a bot with <a href="https://t.me/BotFather" target="_blank" rel="noopener">@BotFather</a>, paste its token above and save. Then message the bot from your phone — if your chat isn't on the allowed list yet, it replies with your chat id to add here. Once allowed, any text you send it prints immediately, no report involved.</div>
        <button class="btn small" id="tg-test">Test connection</button>
        <div class="result" id="tg-result"></div></div>

      <div class="card"><h3>Print via HTTP <span class="status ${st.messaging ? 'ok' : ''}">${st.messaging ? 'token set' : 'not set'}</span></h3>
        <label>API token <input id="pt-token" type="text" readonly></label>
        <div class="hint">Runs a separate listener on port <b>${esc(state.cfg.server.printPort)}</b> that accepts only <code>POST /print-text</code> with this token as a bearer header — for Shortcuts, Home Assistant, curl, etc. It stays off until a token exists, and never shares the GUI's unauthenticated port.</div>
        <pre class="log small" id="pt-example" style="white-space:pre-wrap"></pre>
        <button class="btn small" id="pt-generate">Generate new token</button>
        <div class="result" id="pt-result"></div></div>`;

    const bind = (id, obj, key, opts = {}) => {
      const inp = $('#' + id);
      inp.value = obj[key] === '********' ? '' : (obj[key] ?? '');
      if (obj[key] === '********') inp.placeholder = 'saved — leave blank to keep';
      inp.oninput = () => { obj[key] = inp.value === '' && opts.secret ? '********' : inp.value; setDirty(true); if (opts.after) opts.after(); };
    };
    bind('w-provider', c.weather, 'provider', { after: () => $('#w-keylabel').classList.toggle('hidden', c.weather.provider !== 'tomorrowio') });
    $('#w-keylabel').classList.toggle('hidden', c.weather.provider !== 'tomorrowio');
    bind('w-key', c.weather, 'tomorrowApiKey', { secret: true });
    bind('gc-id', c.google, 'clientId'); bind('gc-secret', c.google, 'clientSecret', { secret: true }); bind('gc-cal', c.google, 'calendarId');
    const scopes = new Set((c.google.scopes && c.google.scopes.length) ? c.google.scopes : ['calendar']);
    for (const name of ['calendar', 'tasks', 'keep']) {
      const box = $('#gc-scope-' + name); box.checked = scopes.has(name);
      box.onchange = () => { if (box.checked) scopes.add(name); else scopes.delete(name); c.google.scopes = Array.from(scopes); setDirty(true); };
    }
    bind('db-key', c.dropbox, 'appKey'); bind('db-path', c.dropbox, 'filePath');
    bind('ha-url', c.homeAssistant, 'url'); bind('ha-token', c.homeAssistant, 'token', { secret: true });
    bind('ae-id', c.aeris, 'clientId'); bind('ae-secret', c.aeris, 'clientSecret', { secret: true }); bind('ae-station', c.aeris, 'stationId');
    c.events = c.events || {};
    bind('tm-key', c.events, 'ticketmasterKey', { secret: true }); bind('sg-id', c.events, 'seatgeekClientId');
    c.grafana = c.grafana || {};
    bind('gf-url', c.grafana, 'url'); bind('gf-token', c.grafana, 'token', { secret: true });
    $('#gf-test').onclick = () => showResult('gf-result', api('POST', '/api/test/grafana', c.grafana));
    c.ai = c.ai || {};
    bind('ai-anthropic', c.ai, 'anthropicKey', { secret: true }); bind('ai-openai', c.ai, 'openaiKey', { secret: true }); bind('ai-base', c.ai, 'openaiBaseUrl');
    bind('ai-openrouter', c.ai, 'openrouterKey', { secret: true }); bind('ai-local-base', c.ai, 'localBaseUrl'); bind('ai-local-key', c.ai, 'localKey', { secret: true });
    $('#ai-test-anthropic').onclick = () => showResult('ai-result', api('POST', '/api/test/ai', { provider: 'anthropic', ai: c.ai }));
    $('#ai-test-openai').onclick = () => showResult('ai-result', api('POST', '/api/test/ai', { provider: 'openai', ai: c.ai }));
    $('#ai-test-openrouter').onclick = () => showResult('ai-result', api('POST', '/api/test/ai', { provider: 'openrouter', ai: c.ai }));
    $('#ai-test-local').onclick = () => showResult('ai-result', api('POST', '/api/test/ai', { provider: 'local', ai: c.ai }));

    c.telegram = c.telegram || {};
    bind('tg-token', c.telegram, 'botToken', { secret: true });
    const tgChats = $('#tg-chats');
    tgChats.value = (c.telegram.allowedChatIds || []).join('\n');
    tgChats.oninput = () => { c.telegram.allowedChatIds = tgChats.value.split('\n').map((s) => s.trim()).filter(Boolean); setDirty(true); };

    c.messaging = c.messaging || {};
    const ptToken = $('#pt-token');
    ptToken.value = revealedApiToken || '';
    ptToken.placeholder = c.messaging.apiToken === '********' ? 'saved — generate a new one to view it again' : 'none yet — generate one';
    const updatePtExample = () => {
      const port = state.cfg.server.printPort || 8788;
      const tok = revealedApiToken || '<token>';
      $('#pt-example').textContent = `curl -X POST http://${esc(location.hostname)}:${port}/print-text \\\n  -H "Authorization: Bearer ${esc(tok)}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"title":"Note","text":"hello from Shortcuts"}'`;
    };
    updatePtExample();
    $('#pt-generate').onclick = async () => {
      try {
        const r = await api('POST', '/api/messaging/token', {});
        revealedApiToken = r.token;
        c.messaging.apiToken = '********';
        await refreshStatus();
        renderConnectors();
        toast('New token generated and saved — copy it now, it will not be shown again');
      } catch (e) { toast(e.message, true); }
    };

    const showResult = (id, p) => p.then((r) => { const el = $('#' + id); el.textContent = r.message || 'OK'; el.className = 'result ok'; })
      .catch((e) => { const el = $('#' + id); el.textContent = e.message; el.className = 'result err'; });
    $('#w-test').onclick = () => showResult('w-result', api('POST', '/api/test/weather', { location: state.cfg.general.location, weather: c.weather }));
    $('#ha-test').onclick = () => showResult('ha-result', api('POST', '/api/test/homeassistant', c.homeAssistant).then((r) => ({ message: `${r.message} — ${r.entities} entities visible` })));
    $('#ae-test').onclick = () => showResult('ae-result', api('POST', '/api/test/aeris', c.aeris));
    $('#tg-test').onclick = () => showResult('tg-result', api('POST', '/api/test/telegram', c.telegram));
    $('#gc-connect').onclick = async () => {
      try { await saveIfDirty(); const r = await api('POST', '/api/auth/google/start', c.google); window.open(r.url, '_blank'); $('#gc-result').textContent = 'Finish signing in in the new tab, then come back here.'; $('#gc-result').className = 'result'; pollConnector('google'); }
      catch (e) { $('#gc-result').textContent = e.message; $('#gc-result').className = 'result err'; }
    };
    $('#db-connect').onclick = async () => {
      try { await saveIfDirty(); const r = await api('POST', '/api/auth/dropbox/start', c.dropbox); window.open(r.url, '_blank'); $('#db-result').textContent = 'Finish linking in the new tab, then come back here.'; $('#db-result').className = 'result'; pollConnector('dropbox'); }
      catch (e) { $('#db-result').textContent = e.message; $('#db-result').className = 'result err'; }
    };
    $('#gc-list').onclick = () => showResult('gc-result', api('GET', '/api/google/calendars').then((list) => ({ message: list.map((x) => `${x.Summary}  (${x.ID})`).join('\n') })));
    for (const [id, name] of [['gc-disconnect', 'google'], ['db-disconnect', 'dropbox'], ['ha-disconnect', 'homeassistant']]) {
      const b = $('#' + id); if (b) b.onclick = async () => { state.cfg = await api('POST', `/api/disconnect/${name}`); setDirty(false); await refreshStatus(); renderConnectors(); renderGeneral(); };
    }
  }

  function pollConnector(name) {
    let tries = 0;
    const h = setInterval(async () => {
      await refreshStatus();
      if (state.status.connectors?.[name] || ++tries > 120) { clearInterval(h); if (state.status.connectors?.[name]) { state.cfg = await api('GET', '/api/config'); renderConnectors(); renderGeneral(); toast(name + ' linked!'); } }
    }, 2500);
  }

  // ── save / generate / preview ────────────────────────────────────────────
  async function save() {
    const btn = $('#btn-save'); btn.disabled = true;
    try { state.cfg = await api('PUT', '/api/config', state.cfg); setDirty(false); toast('Saved'); await refreshStatus(); renderSections(); renderConnectors(); }
    catch (e) { toast('Save failed: ' + e.message, true); throw e; }
    finally { btn.disabled = false; }
  }
  async function saveIfDirty() { if (state.dirty) await save(); }
  $('#btn-save').addEventListener('click', save);
  document.addEventListener('keydown', (e) => { if ((e.ctrlKey || e.metaKey) && e.key === 's') { e.preventDefault(); save(); } });

  $('#btn-generate').addEventListener('click', async () => {
    const btn = $('#btn-generate');
    try {
      await saveIfDirty();
      btn.classList.add('busy'); btn.textContent = 'Generating…'; showTab('preview');
      $('#run-log').textContent = 'Fetching sections… this can take up to a minute.';
      const r = await api('POST', '/api/generate', {});
      showRun(r.result); toast(`Report ready: ${r.result.heightMm} mm`); refreshPreview();
    } catch (e) { $('#run-log').textContent = 'Failed: ' + e.message; toast(e.message, true); }
    finally { btn.classList.remove('busy'); btn.textContent = 'Generate now'; refreshStatus(); }
  });
  $('#btn-print').addEventListener('click', async () => {
    try { const r = await api('POST', '/api/print'); toast('Sent to printer ' + (r.output || '')); } catch (e) { toast(e.message, true); }
  });
  $('#note-send').addEventListener('click', async () => {
    const out = $('#note-result'); const text = $('#note-text').value.trim();
    if (!text) { out.textContent = 'Type something first.'; out.className = 'muted small err'; return; }
    out.textContent = 'Printing…'; out.className = 'muted small';
    try {
      await api('POST', '/api/print-text', { title: $('#note-title').value.trim(), text });
      out.textContent = 'Sent to printer.'; out.className = 'muted small';
      $('#note-text').value = ''; $('#note-title').value = '';
      refreshStatus();
    } catch (e) { out.textContent = e.message; out.className = 'muted small err'; }
  });
  $('#btn-refresh-preview').addEventListener('click', refreshPreview);

  function showRun(res) {
    if (!res) return;
    const lines = (res.sections || []).filter((s) => s.status !== 'disabled').map((s) => `${{ ok: '✓', empty: '·', error: '✗' }[s.status] || '?'} ${s.id}${s.error ? ' — ' + s.error : ''}`);
    $('#run-log').textContent = lines.join('\n') + '\n\n' + (res.log || []).join('\n');
    $('#preview-meta').textContent = `${res.heightMm} mm tall · generated ${new Date(res.generated).toLocaleTimeString()}`;
  }
  async function refreshLog() {
    try {
      const r = await api('GET', '/api/log?n=300');
      $('#server-log').textContent = (r.lines || []).join('\n') || 'Log is empty.';
      $('#log-path').textContent = r.path || '';
      $('#server-log').scrollTop = $('#server-log').scrollHeight;
    } catch (e) { $('#server-log').textContent = e.message; }
  }
  $('#btn-refresh-log').addEventListener('click', refreshLog);
  async function refreshPreview() {
    await refreshStatus();
    refreshLog();
    const st = state.status;
    if (st.last) showRun(st.last); else if (st.lastError) $('#run-log').textContent = 'Last run failed: ' + st.lastError;
    $('#pdf-frame').src = st.pdfExists ? '/output/tickr.pdf?t=' + Date.now() + '#toolbar=0&view=FitH' : 'about:blank';
    $('#history').innerHTML = (st.history || []).map((h) => `<div class="hist"><b class="${h.ok ? 'ok' : 'err'}">${h.ok ? 'OK' : 'FAIL'}</b> ${new Date(h.time).toLocaleString()} · ${esc(h.trigger)} · ${esc(h.message)}${h.printed ? ' · printed' : ''}</div>`).join('') || '<p class="muted small">No runs yet.</p>';
    $('#notes-history').innerHTML = (st.notes || []).slice().reverse().map((n) => `<div class="hist"><b class="${n.ok ? 'ok' : 'err'}">${n.ok ? 'OK' : 'FAIL'}</b> ${new Date(n.time).toLocaleString()} · ${esc(n.source)}${n.error ? ' · ' + esc(n.error) : ''}<div class="muted small">${esc(n.text.length > 140 ? n.text.slice(0, 140) + '…' : n.text)}</div></div>`).join('') || '<p class="muted small">No notes printed yet.</p>';
  }

  load().catch((e) => { document.body.innerHTML = `<pre style="padding:20px">Failed to load: ${esc(e.message)}</pre>`; });
})();
