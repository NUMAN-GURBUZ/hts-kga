// HTS-KGA görselleştirme mantığı (E07, ADR-13).
//
// # Bu dosyanın uyduğu üç kural
//
// 1. Hiçbir istek filtresiz atılmaz (ADR-13). `run_id` seçilmeden hiçbir katman
//    yüklenmez; tahmin katmanları ayrıca abone ister.
// 2. Sunucunun 400 yanıtı KULLANICIYA GÖSTERİLİR. 500 geometri sınırı aşıldığında
//    sunucu nasıl daraltılacağını söylüyor; onu yutup "veri yok" demek
//    kullanıcıyı yanıltırdı.
// 3. Harita hareketinde bbox gönderilir — sunucu ST_Intersects uygular.
//
// # Gerçek konum katmanı yok
//
// ADR-33/2: gateway `ground_truth`'u göremez. Bu sayfa da onu istemez.

'use strict';

const api = {
  async get(path, params) {
    const url = new URL(path, window.location.origin);
    for (const [k, v] of Object.entries(params || {})) {
      if (v !== undefined && v !== null && v !== '') url.searchParams.set(k, v);
    }
    const resp = await fetch(url);
    const text = await resp.text();
    if (!resp.ok) {
      // Sunucunun açıklaması kullanıcıya aynen aktarılır (ADR-13).
      let msg = text;
      try { msg = JSON.parse(text).error || text; } catch (_) { /* düz metin */ }
      throw new Error(msg);
    }
    return JSON.parse(text);
  },
};

// ─── Harita ───────────────────────────────────────────────────────────────────

const map = L.map('map', { center: [38.6748, 39.2225], zoom: 12 });

// Çevrimdışı çalışabilmek için karo katmanı yoktur: demo makinesinde internet
// olmayabilir ve boş bir gri zemin, yüklenmeyen karolardan iyidir. Geometriler
// kendi başlarına okunaklıdır.
L.rectangle([[-90, -180], [90, 180]], {
  color: '#1b2430', weight: 0, fillColor: '#141b24', fillOpacity: 1,
}).addTo(map);

const layers = {};          // ad → L.GeoJSON
const state = { runID: '', subscriber: '' };

function setStatus(msg, kind) {
  const el = document.getElementById('status');
  el.textContent = msg || '';
  el.className = kind || '';
}

// clearLayer, katmanı haritadan kaldırır.
function clearLayer(name) {
  if (layers[name]) { map.removeLayer(layers[name]); delete layers[name]; }
}

// bboxParam, görünür alanı `minLon,minLat,maxLon,maxLat` biçiminde döndürür.
function bboxParam() {
  const b = map.getBounds();
  return [b.getWest(), b.getSouth(), b.getEast(), b.getNorth()]
    .map((v) => v.toFixed(6)).join(',');
}

// showPopup, bir bulgunun kanıt gövdesini gösterir.
//
// `evidence` alanı adli açıklanabilirliğin taşıyıcısıdır (ADR-09, ADR-31/5):
// kuralın neden tetiklendiğini somut değerlerle söyler.
function showPopup(title, obj) {
  document.getElementById('popup-body').textContent =
    title + '\n\n' + JSON.stringify(obj, null, 2);
  document.getElementById('popup').classList.remove('hidden');
}
document.getElementById('popup-close').onclick =
  () => document.getElementById('popup').classList.add('hidden');

// ─── Katmanlar ────────────────────────────────────────────────────────────────

const STYLES = {
  cells:    { color: '#4da3ff', weight: 1, fillOpacity: 0.9, radius: 4 },
  activity: { color: '#9d7bff', weight: 1, fillOpacity: 0.75 },
  findings: { color: '#ff5a5a', weight: 1, fillOpacity: 0.9, radius: 6 },
  B0:       { color: '#ffb020', weight: 2, fillOpacity: 0.10 },
  B1:       { color: '#39c46e', weight: 2, fillOpacity: 0.15 },
  M:        { color: '#ff3d71', weight: 2, fillOpacity: 0.45 },
};

async function loadGeoLayer(name, path, params, style, onFeature) {
  clearLayer(name);
  if (!state.runID) { setStatus('önce koşu seçin', 'warn'); return; }

  try {
    const doc = await api.get(path, { run_id: state.runID, ...params });
    // Bazı uçlar (ör. /aggregate/cell-activity) `feature_collection`'ı bir
    // sarmalayıcı içinde, zaten çözülmüş bir nesne olarak döner; diğerleri
    // (cells/estimates/findings) tüm gövdeyi doğrudan GeoJSON olarak döner.
    // İkisini de doğru ele almak için önce tipe bakılır — yalnızca gerçekten
    // metinse tekrar JSON.parse edilir.
    const collection = typeof doc.feature_collection === 'string'
      ? JSON.parse(doc.feature_collection)
      : (doc.feature_collection || doc);

    layers[name] = L.geoJSON(collection, {
      style: () => style,
      pointToLayer: (_, latlng) => L.circleMarker(latlng, style),
      onEachFeature: (feat, layer) => {
        layer.on('click', () => onFeature(feat));
      },
    }).addTo(map);

    const n = (collection.features || []).length;
    setStatus(`${name}: ${n} geometri`, 'ok');
    return doc;
  } catch (err) {
    // ADR-13: sunucunun açıklaması aynen gösterilir.
    setStatus(err.message, 'error');
    return null;
  }
}

async function loadCells() {
  await loadGeoLayer('baz istasyonları', '/api/v1/cells',
    { bbox: bboxParam() }, STYLES.cells,
    (f) => showPopup('Baz istasyonu', f.properties));
}

async function loadActivity() {
  const doc = await loadGeoLayer('hücre yoğunluğu', '/api/v1/aggregate/cell-activity',
    { bbox: bboxParam() }, STYLES.activity,
    (f) => showPopup('Hücre yoğunluğu', f.properties));

  if (doc) {
    // k-anonimlik sessiz kalmaz: kaç hücrenin gizlendiği söylenir (ADR-33/5).
    setStatus(
      `hücre yoğunluğu: ${doc.count} hücre · k=${doc.k} nedeniyle ${doc.suppressed_cells} hücre gizlendi`,
      'ok');
  }
}

async function loadFindings() {
  const rule = document.getElementById('rule').value;
  await loadGeoLayer('bulgular', '/api/v1/findings',
    { rule_id: rule, bbox: bboxParam() }, STYLES.findings,
    (f) => showPopup(`Bulgu — ${f.properties.rule_name}`, f.properties));

  if (rule === '1' && layers['bulgular']) {
    // Kural 1 bulguları tanım gereği konumsuzdur (envanterde olmayan hücre).
    setStatus('kural 1 bulguları konumsuzdur: beyan edilen hücre envanterde yok', 'warn');
  }
}

async function loadEstimates(method, confidence, key) {
  clearLayer(key);
  if (!state.subscriber) { setStatus('tahmin katmanı için abone gerekli (ADR-13)', 'warn'); return; }

  await loadGeoLayer(key, '/api/v1/estimates',
    { subscriber: state.subscriber, method, confidence }, STYLES[method],
    (f) => showPopup(`${f.properties.method}@${f.properties.confidence}`, f.properties));
}

// ─── Ölçüm paneli ─────────────────────────────────────────────────────────────

async function loadMetrics() {
  const el = document.getElementById('metrics');
  if (!state.runID) { el.textContent = 'koşu seçin'; return; }

  try {
    const [m, k7] = await Promise.all([
      api.get('/api/v1/metrics', { run_id: state.runID }),
      api.get('/api/v1/integrity-metrics', { run_id: state.runID }),
    ]);

    const lines = [];
    for (const row of (m.rows || [])) {
      if (row.Method === 'M' && row.Confidence === 0.9) {
        lines.push(`M@90 kapsama ${row.CoverageRate.toFixed(4)}`);
        lines.push(`M@90 medyan alan ${row.MedianAreaKM2.toFixed(4)} km²`);
        if (row.ReductionVsB0 != null) {
          lines.push(`K2 vs B0 %${(row.ReductionVsB0 * 100).toFixed(2)}`);
          lines.push(`K3 vs B1 %${(row.ReductionVsB1 * 100).toFixed(2)}`);
        }
      }
    }
    for (const row of (k7.rows || [])) {
      const p = row.Precision == null ? '—' : row.Precision.toFixed(4);
      lines.push(`K7 kural ${row.RuleID}: precision ${p} · recall ${row.Recall.toFixed(4)}`);
    }
    el.textContent = lines.length ? lines.join('\n') : 'bu koşuda ölçüm yok';
  } catch (err) {
    el.textContent = err.message;
  }
}

// ─── Koşu seçimi ──────────────────────────────────────────────────────────────

async function loadRuns() {
  const sel = document.getElementById('run');
  try {
    const { runs } = await api.get('/api/v1/runs', { limit: 30 });
    sel.innerHTML = '<option value="">— seçin —</option>';
    for (const r of runs) {
      const opt = document.createElement('option');
      opt.value = r.run_id;
      opt.textContent = `${r.scenario} · ${r.run_id.slice(0, 8)} · ${r.published_events || 0} olay`;
      sel.appendChild(opt);
    }
  } catch (err) {
    sel.innerHTML = '<option value="">hata</option>';
    setStatus(err.message, 'error');
  }
}

// loadSubscribers, koşuda tahmin geometrisi olan aboneleri "abone" seçim
// kutusuna doldurur (en zengin veriden başlayarak) — elle psql sorgusuna
// gerek bırakmaz. Gerçek kimlik/konum taşımaz, yalnızca pseudonim listeler
// (bkz. internal/gateway/query/metrics.go: Subscribers).
async function loadSubscribers() {
  const sel = document.getElementById('subscriber');
  const meta = document.getElementById('subscriber-meta');
  sel.innerHTML = '<option value="">yükleniyor…</option>';
  meta.textContent = '';
  if (!state.runID) {
    sel.innerHTML = '<option value="">— önce koşu seçin —</option>';
    return;
  }
  try {
    const { subscribers } = await api.get('/api/v1/subscribers', { run_id: state.runID, limit: 25 });
    if (!subscribers.length) {
      sel.innerHTML = '<option value="">bu koşuda tahmin geometrisi yok</option>';
      return;
    }
    sel.innerHTML = '<option value="">— abone seçin —</option>';
    for (const s of subscribers) {
      const opt = document.createElement('option');
      opt.value = s.pseudo_msisdn;
      opt.textContent = `${s.pseudo_msisdn.slice(0, 12)}… · ${s.estimates} tahmin olayı`;
      sel.appendChild(opt);
    }
  } catch (err) {
    sel.innerHTML = '<option value="">hata</option>';
    setStatus(err.message, 'error');
  }
}

document.getElementById('run').onchange = async (e) => {
  state.runID = e.target.value;
  state.subscriber = '';
  for (const name of Object.keys(layers)) clearLayer(name);
  for (const id of ['l-cells', 'l-activity', 'l-findings', 'l-b0', 'l-b1', 'l-m']) {
    document.getElementById(id).checked = false;
  }
  document.getElementById('run-meta').textContent = state.runID ? `koşu ${state.runID}` : '';
  await Promise.all([loadMetrics(), loadSubscribers()]);

  // Haritayı koşunun hücrelerine göre konumlandır.
  if (state.runID) {
    try {
      const doc = await api.get('/api/v1/cells', { run_id: state.runID });
      const g = L.geoJSON(JSON.parse(doc.feature_collection || JSON.stringify(doc)));
      if (g.getBounds().isValid()) map.fitBounds(g.getBounds().pad(0.1));
    } catch (_) { /* sınır bulunamadı; varsayılan görünüm kalır */ }
  }
};

document.getElementById('subscriber').onchange = (e) => {
  state.subscriber = e.target.value;
  const opt = e.target.selectedOptions[0];
  document.getElementById('subscriber-meta').textContent =
    state.subscriber ? opt.textContent : '';

  // Abone değiştiğinde zaten açık olan tahmin katmanları eski aboneyi
  // göstermeye devam etmesin — sessizce bayatlamak yerine yeniden yüklenir.
  const active = [
    ['l-b0', () => loadEstimates('B0', -1, 'B0')],
    ['l-b1', () => loadEstimates('B1', -1, 'B1')],
    ['l-m',  () => loadEstimates('M', 0.9, 'M')],
  ];
  for (const [id, fn] of active) {
    if (document.getElementById(id).checked) fn();
  }
};

// ─── Katman anahtarları ───────────────────────────────────────────────────────

const toggles = [
  ['l-cells',    loadCells,                                    'baz istasyonları'],
  ['l-activity', loadActivity,                                 'hücre yoğunluğu'],
  ['l-findings', loadFindings,                                 'bulgular'],
  ['l-b0',       () => loadEstimates('B0', -1, 'B0'),          'B0'],
  ['l-b1',       () => loadEstimates('B1', -1, 'B1'),          'B1'],
  ['l-m',        () => loadEstimates('M', 0.9, 'M'),           'M'],
];

for (const [id, fn, layerName] of toggles) {
  document.getElementById(id).onchange = (e) => {
    if (e.target.checked) fn(); else clearLayer(layerName);
  };
}
document.getElementById('rule').onchange = () => {
  if (document.getElementById('l-findings').checked) loadFindings();
};

// Harita hareketinde bbox'lı katmanlar yenilenir (ADR-13 viewport süzgeci).
let moveTimer;
map.on('moveend', () => {
  clearTimeout(moveTimer);
  moveTimer = setTimeout(() => {
    if (document.getElementById('l-cells').checked) loadCells();
    if (document.getElementById('l-activity').checked) loadActivity();
    if (document.getElementById('l-findings').checked) loadFindings();
  }, 400);
});

loadRuns();
