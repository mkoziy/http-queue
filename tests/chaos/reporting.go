package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type event struct {
	TS       time.Time      `json:"ts"`
	RunID    string         `json:"run_id"`
	Seed     int64          `json:"seed"`
	Level    string         `json:"level"`
	Actor    string         `json:"actor"`
	Kind     string         `json:"event_type"`
	Queue    string         `json:"queue,omitempty"`
	JobID    string         `json:"job_id,omitempty"`
	WorkerID string         `json:"worker_id,omitempty"`
	Status   int            `json:"status,omitempty"`
	OK       bool           `json:"ok"`
	Err      string         `json:"err,omitempty"`
	Fields   map[string]any `json:"fields,omitempty"`
}

type eventWriter struct {
	mu   sync.Mutex
	f    *os.File
	enc  *json.Encoder
	run  string
	seed int64
}

func newEventWriter(path, runID string, seed int64) (*eventWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &eventWriter{
		f:    f,
		enc:  json.NewEncoder(f),
		run:  runID,
		seed: seed,
	}, nil
}

func (w *eventWriter) Write(level, actor, kind string, fields map[string]any) {
	if w == nil {
		return
	}
	ev := event{
		TS:    time.Now().UTC(),
		RunID: w.run,
		Seed:  w.seed,
		Level: level,
		Actor: actor,
		Kind:  kind,
		OK:    true,
	}
	if len(fields) > 0 {
		ev.Fields = make(map[string]any, len(fields))
		for k, v := range fields {
			switch k {
			case "queue":
				if s, ok := v.(string); ok {
					ev.Queue = s
				}
			case "job_id":
				if s, ok := v.(string); ok {
					ev.JobID = s
				}
			case "worker_id":
				if s, ok := v.(string); ok {
					ev.WorkerID = s
				}
			case "status":
				switch n := v.(type) {
				case int:
					ev.Status = n
				case int64:
					ev.Status = int(n)
				case float64:
					ev.Status = int(n)
				}
			case "ok":
				if b, ok := v.(bool); ok {
					ev.OK = b
				}
			case "err":
				if s, ok := v.(string); ok {
					ev.Err = s
					if s != "" {
						ev.OK = false
					}
				}
			default:
				ev.Fields[k] = v
			}
		}
		if len(ev.Fields) == 0 {
			ev.Fields = nil
		}
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.enc.Encode(ev)
}

func (w *eventWriter) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}

type summary struct {
	RunID        string            `json:"run_id"`
	Seed         int64             `json:"seed"`
	StartedAt    time.Time         `json:"started_at"`
	FinishedAt   time.Time         `json:"finished_at"`
	DurationMS   int64             `json:"duration_ms"`
	Status       string            `json:"status"`
	Config       map[string]any    `json:"config"`
	Counters     map[string]int64  `json:"counters"`
	Artifacts    map[string]string `json:"artifacts"`
	ReproCommand string            `json:"repro_command"`
	Audit        auditSummary      `json:"audit"`
}

type auditSummary struct {
	Passed     bool        `json:"passed"`
	Violations []violation `json:"violations,omitempty"`
}

type reportData struct {
	Summary summary
	Events  []event
}

func writeSummary(path string, data summary) error {
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o644)
}

func buildReport(eventsPath, summaryPath, outputPath string) error {
	data, err := loadReportData(eventsPath, summaryPath)
	if err != nil {
		return err
	}
	doc, err := renderReport(data)
	if err != nil {
		return err
	}
	return os.WriteFile(outputPath, doc, 0o644)
}

func loadReportData(eventsPath, summaryPath string) (reportData, error) {
	var out reportData

	summaryRaw, err := os.ReadFile(summaryPath)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(summaryRaw, &out.Summary); err != nil {
		return out, fmt.Errorf("decode summary: %w", err)
	}

	f, err := os.Open(eventsPath)
	if err != nil {
		return out, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var ev event
		if err := json.Unmarshal(line, &ev); err != nil {
			return out, fmt.Errorf("decode event: %w", err)
		}
		out.Events = append(out.Events, ev)
	}
	if err := scanner.Err(); err != nil {
		return out, err
	}

	sort.Slice(out.Events, func(i, j int) bool {
		return out.Events[i].TS.After(out.Events[j].TS)
	})

	return out, nil
}

func renderReport(data reportData) ([]byte, error) {
	summaryJSON, err := json.Marshal(data.Summary)
	if err != nil {
		return nil, err
	}
	eventsJSON, err := json.Marshal(data.Events)
	if err != nil {
		return nil, err
	}

	tpl, err := template.New("report").Parse(reportHTML)
	if err != nil {
		return nil, err
	}

	view := struct {
		Title       string
		SummaryJSON template.JS
		EventsJSON  template.JS
		CSS         template.CSS
		JS          template.JS
	}{
		Title:       fmt.Sprintf("Chaos Report %s", data.Summary.RunID),
		SummaryJSON: template.JS(summaryJSON),
		EventsJSON:  template.JS(eventsJSON),
		CSS:         template.CSS(reportCSS),
		JS:          template.JS(reportJS),
	}

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, view); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func defaultReportPath(runID string) string {
	return filepath.Join(".", fmt.Sprintf("chaos-report-%s.html", runID))
}

func reproCommand(c cfg) string {
	parts := []string{
		"go run ./tests/chaos",
		fmt.Sprintf("-duration=%s", c.duration),
		fmt.Sprintf("-publishers=%d", c.publishers),
		fmt.Sprintf("-workers=%d", c.workers),
		fmt.Sprintf("-seed=%d", c.seed),
		fmt.Sprintf("-queues=%d", c.queues),
		fmt.Sprintf("-restart-probability=%g", c.restartProb),
		fmt.Sprintf("-visibility-timeout=%s", c.visibilityTimeout),
		fmt.Sprintf("-worker-expiry=%s", c.workerExpiry),
		fmt.Sprintf("-sweep-interval=%s", c.sweepInterval),
		fmt.Sprintf("-max-attempts=%d", c.maxAttempts),
	}
	return strings.Join(parts, " ")
}

const reportHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}}</title>
  <style>{{.CSS}}</style>
</head>
<body>
  <div id="app"></div>
  <script id="summary-data" type="application/json">{{.SummaryJSON}}</script>
  <script id="events-data" type="application/json">{{.EventsJSON}}</script>
  <script>{{.JS}}</script>
</body>
</html>
`

const reportCSS = `
:root {
  --bg: #f4efe7;
  --panel: #fffaf3;
  --panel-2: #f0e7da;
  --text: #1d1a17;
  --muted: #62584f;
  --accent: #b84c2a;
  --accent-2: #295f4e;
  --danger: #aa2f2f;
  --ok: #216a52;
  --line: #d6c9b8;
  --shadow: 0 14px 40px rgba(54, 37, 21, 0.08);
  --mono: "SFMono-Regular", ui-monospace, monospace;
}
* { box-sizing: border-box; }
body {
  margin: 0;
  font-family: Georgia, "Iowan Old Style", serif;
  color: var(--text);
  background:
    radial-gradient(circle at top left, rgba(184,76,42,0.14), transparent 28rem),
    radial-gradient(circle at right, rgba(41,95,78,0.12), transparent 24rem),
    var(--bg);
}
#app {
  max-width: 1280px;
  margin: 0 auto;
  padding: 32px 20px 48px;
}
.hero, .panel {
  background: var(--panel);
  border: 1px solid var(--line);
  border-radius: 20px;
  box-shadow: var(--shadow);
}
.hero {
  padding: 24px;
  margin-bottom: 20px;
}
.eyebrow {
  letter-spacing: 0.08em;
  text-transform: uppercase;
  font-size: 12px;
  color: var(--muted);
  margin-bottom: 10px;
}
h1, h2, h3 { margin: 0; font-weight: 600; }
h1 { font-size: clamp(32px, 5vw, 54px); line-height: 0.95; }
.subtitle { color: var(--muted); margin-top: 10px; }
.status {
  display: inline-block;
  margin-top: 18px;
  padding: 8px 12px;
  border-radius: 999px;
  font-size: 13px;
  font-weight: 700;
}
.status.passed { background: rgba(33,106,82,0.12); color: var(--ok); }
.status.failed { background: rgba(170,47,47,0.12); color: var(--danger); }
.grid {
  display: grid;
  grid-template-columns: repeat(12, 1fr);
  gap: 20px;
}
.span-8 { grid-column: span 8; }
.span-4 { grid-column: span 4; }
.span-12 { grid-column: span 12; }
.panel { padding: 20px; }
.metrics {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: 12px;
}
.metric {
  background: var(--panel-2);
  border-radius: 16px;
  padding: 14px;
}
.metric .label { font-size: 12px; color: var(--muted); text-transform: uppercase; letter-spacing: 0.08em; }
.metric .value { font-size: 30px; margin-top: 8px; font-weight: 700; }
.meta-list, .audit-list, .detail-list { display: grid; gap: 10px; margin-top: 16px; }
.meta-item, .audit-item, .detail-item {
  padding: 12px 14px;
  background: var(--panel-2);
  border-radius: 14px;
}
.mono { font-family: var(--mono); font-size: 12px; word-break: break-word; }
.toolbar {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin: 18px 0;
}
.toolbar input, .toolbar select {
  border: 1px solid var(--line);
  background: #fff;
  border-radius: 12px;
  padding: 10px 12px;
  font: inherit;
}
.timeline { display: grid; gap: 10px; max-height: 760px; overflow: auto; }
.event {
  padding: 14px;
  border: 1px solid var(--line);
  border-radius: 14px;
  background: #fff;
}
.event-top {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  align-items: baseline;
}
.event-kind {
  font-family: var(--mono);
  font-size: 13px;
  color: var(--accent);
}
.chips { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 10px; }
.chip {
  font-family: var(--mono);
  font-size: 11px;
  background: var(--panel-2);
  color: var(--muted);
  border-radius: 999px;
  padding: 5px 8px;
}
.bar {
  display: flex;
  height: 12px;
  margin-top: 14px;
  border-radius: 999px;
  overflow: hidden;
  background: var(--panel-2);
}
.segment-a { background: var(--accent); }
.segment-b { background: var(--accent-2); }
.segment-c { background: #8f6b3c; }
.segment-d { background: #57738a; }
.segment-e { background: #9f4d7f; }
button.copy {
  border: 0;
  border-radius: 12px;
  padding: 10px 12px;
  font: inherit;
  background: var(--accent);
  color: #fff;
  cursor: pointer;
}
@media (max-width: 920px) {
  .span-8, .span-4, .span-12 { grid-column: span 12; }
  #app { padding: 18px 14px 28px; }
}
`

const reportJS = `
const summary = JSON.parse(document.getElementById('summary-data').textContent);
const events = JSON.parse(document.getElementById('events-data').textContent);

const app = document.getElementById('app');

function escapeHtml(value) {
  return String(value ?? '').replace(/[&<>"]/g, (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));
}

function formatTS(ts) {
  return new Date(ts).toLocaleString();
}

function chipsFor(ev) {
  const chips = [];
  if (ev.actor) chips.push(['actor', ev.actor]);
  if (ev.queue) chips.push(['queue', ev.queue]);
  if (ev.job_id) chips.push(['job', ev.job_id]);
  if (ev.worker_id) chips.push(['worker', ev.worker_id]);
  if (ev.status) chips.push(['status', ev.status]);
  if (ev.err) chips.push(['err', ev.err]);
  if (ev.fields) {
    for (const [k, v] of Object.entries(ev.fields)) {
      if (v === '' || v === null || v === false) continue;
      if (typeof v === 'object') continue;
      chips.push([k, v]);
    }
  }
  return chips.slice(0, 12);
}

function metric(name, value) {
  return '<div class="metric"><div class="label">' + escapeHtml(name) + '</div><div class="value">' + escapeHtml(value) + '</div></div>';
}

function render(filtered) {
  const counters = summary.counters || {};
  const auditItems = (summary.audit.violations || []).map(v =>
    '<div class="audit-item"><strong>' + escapeHtml(v.rule) + '</strong><div class="subtitle">' + escapeHtml(v.detail) + '</div><div class="mono">' +
    escapeHtml([v.job_id, v.index_key].filter(Boolean).join(' ')) + '</div></div>'
  ).join('') || '<div class="audit-item">No invariant violations.</div>';

  const actionTotal = ['acks','nacks','abandoned','slow_acks','double_acks'].reduce((sum, key) => sum + (counters[key] || 0), 0) || 1;
  const bars = [
    ['segment-a', counters.acks || 0],
    ['segment-b', counters.nacks || 0],
    ['segment-c', counters.abandoned || 0],
    ['segment-d', counters.slow_acks || 0],
    ['segment-e', counters.double_acks || 0],
  ].map(([cls, value]) => '<div class="' + cls + '" style="width:' + ((value / actionTotal) * 100) + '%"></div>').join('');

  const eventHTML = filtered.map(ev => {
    const chips = chipsFor(ev).map(([k, v]) => '<span class="chip">' + escapeHtml(k) + ': ' + escapeHtml(v) + '</span>').join('');
    return '<div class="event">' +
      '<div class="event-top"><div><div class="event-kind">' + escapeHtml(ev.event_type) + '</div><div>' + escapeHtml(formatTS(ev.ts)) + '</div></div>' +
      '<div class="mono">' + escapeHtml(ev.level || 'info') + '</div></div>' +
      '<div class="chips">' + chips + '</div></div>';
  }).join('') || '<div class="event">No events match the current filter.</div>';

  app.innerHTML = '' +
    '<section class="hero">' +
      '<div class="eyebrow">Chaos Binary Report</div>' +
      '<h1>' + escapeHtml(summary.run_id) + '</h1>' +
      '<div class="subtitle">Seed ' + escapeHtml(summary.seed) + ' · ' + escapeHtml(summary.config.duration) + ' · ' + escapeHtml(filtered.length) + ' visible events</div>' +
      '<div class="status ' + escapeHtml(summary.status) + '">' + escapeHtml(summary.status.toUpperCase()) + '</div>' +
    '</section>' +
    '<section class="grid">' +
      '<div class="panel span-8">' +
        '<h2>Run Summary</h2>' +
        '<div class="metrics">' +
          metric('Publishes', counters.publishes || 0) +
          metric('Claims', counters.claims || 0) +
          metric('ACKs', counters.acks || 0) +
          metric('NACKs', counters.nacks || 0) +
          metric('Abandoned', counters.abandoned || 0) +
          metric('Slow ACKs', counters.slow_acks || 0) +
          metric('Double ACKs', counters.double_acks || 0) +
          metric('Restarts', counters.restarts || 0) +
          metric('HTTP Errors', counters.http_errors || 0) +
          metric('Invariant Fails', counters.invariant_fails || 0) +
        '</div>' +
        '<div class="bar">' + bars + '</div>' +
        '<div class="meta-list">' +
          '<div class="meta-item"><strong>Repro command</strong><div class="mono" id="repro">' + escapeHtml(summary.repro_command) + '</div><button class="copy" id="copy-repro">Copy</button></div>' +
          '<div class="meta-item"><strong>Artifacts</strong><div class="mono">' + escapeHtml(JSON.stringify(summary.artifacts, null, 2)) + '</div></div>' +
        '</div>' +
      '</div>' +
      '<div class="panel span-4">' +
        '<h2>Audit</h2>' +
        '<div class="subtitle">' + (summary.audit.passed ? 'All invariants satisfied.' : 'Invariant failures require investigation.') + '</div>' +
        '<div class="audit-list">' + auditItems + '</div>' +
      '</div>' +
      '<div class="panel span-12">' +
        '<h2>Timeline</h2>' +
        '<div class="toolbar">' +
          '<input id="search" type="search" placeholder="Search queue, worker, job, error">' +
          '<select id="event-type"><option value="">All event types</option>' + Array.from(new Set(events.map(ev => ev.event_type))).sort().map(kind => '<option value="' + escapeHtml(kind) + '">' + escapeHtml(kind) + '</option>').join('') + '</select>' +
        '</div>' +
        '<div class="timeline" id="timeline">' + eventHTML + '</div>' +
      '</div>' +
    '</section>';

  document.getElementById('copy-repro').addEventListener('click', async () => {
    await navigator.clipboard.writeText(summary.repro_command);
  });
  document.getElementById('search').value = state.search;
  document.getElementById('event-type').value = state.kind;
  document.getElementById('search').addEventListener('input', (e) => {
    state.search = e.target.value;
    rerender();
  });
  document.getElementById('event-type').addEventListener('change', (e) => {
    state.kind = e.target.value;
    rerender();
  });
}

const state = { search: '', kind: '' };

function rerender() {
  const search = state.search.trim().toLowerCase();
  const filtered = events.filter(ev => {
    if (state.kind && ev.event_type !== state.kind) return false;
    if (!search) return true;
    const hay = JSON.stringify(ev).toLowerCase();
    return hay.includes(search);
  });
  render(filtered);
}

rerender();
`
